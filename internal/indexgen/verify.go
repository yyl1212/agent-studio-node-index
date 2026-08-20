package indexgen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const (
	githubAPIHost       = "api.github.com"
	githubVerifierLimit = 30 * time.Second
	manifestLimit       = 256 << 10
	goModLimit          = 1 << 20
	objectResponseLimit = 64 << 10
)

type GitHubVerifier struct {
	client *http.Client
}

func NewGitHubVerifier(client *http.Client, token string) *GitHubVerifier {
	clientCopy := http.Client{}
	if client != nil {
		clientCopy = *client
	}
	if clientCopy.Timeout <= 0 || clientCopy.Timeout > githubVerifierLimit {
		clientCopy.Timeout = githubVerifierLimit
	}
	transport := clientCopy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clientCopy.Transport = &githubTransport{base: transport, token: token}
	clientCopy.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		if err := validateGitHubTarget(request.URL); err != nil {
			return errors.New("GitHub redirect target rejected")
		}
		return nil
	}
	return &GitHubVerifier{client: &clientCopy}
}

func (verifier *GitHubVerifier) Verify(ctx context.Context, submission Submission) error {
	if err := validateSubmission(submission); err != nil {
		return fmt.Errorf("invalid submission: %w", err)
	}
	owner, repo, err := parseGitHubRepository(submission.Source.Repository)
	if err != nil {
		return err
	}
	commit, err := verifier.resolveTag(ctx, owner, repo, submission.Source.Tag)
	if err != nil {
		return err
	}
	if commit != submission.Source.Commit {
		return errors.New("source.tag does not resolve to source.commit")
	}
	manifestRaw, err := verifier.content(ctx, owner, repo, path.Join(submission.Source.ModuleDir, "agent-studio.node-package.json"), commit, manifestLimit)
	if err != nil {
		return err
	}
	goModRaw, err := verifier.content(ctx, owner, repo, path.Join(submission.Source.ModuleDir, "go.mod"), commit, goModLimit)
	if err != nil {
		return err
	}
	return verifyPinnedFiles(submission, manifestRaw, goModRaw)
}

type githubTransport struct {
	base  http.RoundTripper
	token string
}

func (transport *githubTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateGitHubTarget(request.URL); err != nil {
		return nil, err
	}
	requestCopy := request.Clone(request.Context())
	requestCopy.Header = request.Header.Clone()
	if transport.token != "" {
		requestCopy.Header.Set("Authorization", "Bearer "+transport.token)
	}
	return transport.base.RoundTrip(requestCopy)
}

func validateGitHubTarget(target *url.URL) error {
	if target == nil || target.Scheme != "https" || target.Host != githubAPIHost || target.User != nil {
		return errors.New("GitHub API target rejected")
	}
	return nil
}

func parseGitHubRepository(repository string) (string, string, error) {
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("source.repository must be a canonical GitHub repository URL")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[0], "%") || strings.Contains(parts[1], "%") {
		return "", "", errors.New("source.repository must identify one GitHub repository")
	}
	return parts[0], parts[1], nil
}

type gitObject struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

func (verifier *GitHubVerifier) resolveTag(ctx context.Context, owner, repo, tag string) (string, error) {
	var reference gitObject
	if err := verifier.getJSON(ctx, githubURL([]string{"repos", owner, repo, "git", "ref", "tags", tag}, nil), "source.tag", objectResponseLimit, &reference); err != nil {
		return "", err
	}
	switch reference.Object.Type {
	case "commit":
		if !gitOIDPattern.MatchString(reference.Object.SHA) {
			return "", errors.New("source.tag returned an invalid commit")
		}
		return reference.Object.SHA, nil
	case "tag":
		if !gitOIDPattern.MatchString(reference.Object.SHA) {
			return "", errors.New("source.tag returned an invalid tag object")
		}
	default:
		return "", errors.New("source.tag must resolve to a commit or annotated tag")
	}

	var annotated gitObject
	if err := verifier.getJSON(ctx, githubURL([]string{"repos", owner, repo, "git", "tags", reference.Object.SHA}, nil), "source.tag", objectResponseLimit, &annotated); err != nil {
		return "", err
	}
	if annotated.Object.Type != "commit" || !gitOIDPattern.MatchString(annotated.Object.SHA) {
		return "", errors.New("source.tag annotated tag must resolve directly to a commit")
	}
	return annotated.Object.SHA, nil
}

type contentResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

func (verifier *GitHubVerifier) content(ctx context.Context, owner, repo, filePath, commit string, maximum int64) ([]byte, error) {
	segments := []string{"repos", owner, repo, "contents"}
	segments = append(segments, strings.Split(filePath, "/")...)
	var response contentResponse
	if err := verifier.getJSON(ctx, githubURL(segments, url.Values{"ref": []string{commit}}), "pinned file", maximum*2+objectResponseLimit, &response); err != nil {
		return nil, err
	}
	if response.Encoding != "base64" {
		return nil, errors.New("pinned file encoding must be base64")
	}
	if int64(len(response.Content)) > maximum*2+4 {
		return nil, errors.New("pinned file base64 content exceeds size limit")
	}
	reader := io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(response.Content)), maximum+1)
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.New("pinned file contains invalid base64")
	}
	if int64(len(decoded)) > maximum {
		return nil, errors.New("pinned file decoded content exceeds size limit")
	}
	return decoded, nil
}

func githubURL(segments []string, query url.Values) string {
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	result := "https://" + githubAPIHost + "/" + strings.Join(escaped, "/")
	if len(query) != 0 {
		result += "?" + query.Encode()
	}
	return result
}

func (verifier *GitHubVerifier) getJSON(ctx context.Context, target, field string, maximum int64, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%s request is invalid", field)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := verifier.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return fmt.Errorf("GitHub API request failed for %s", field)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned status %d for %s", response.StatusCode, field)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return fmt.Errorf("GitHub API response could not be read for %s", field)
	}
	if int64(len(body)) > maximum {
		return fmt.Errorf("GitHub API response exceeds size limit for %s", field)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("GitHub API returned invalid JSON for %s", field)
	}
	return nil
}

func verifyPinnedFiles(submission Submission, manifestRaw, goModRaw []byte) error {
	digest := sha256.Sum256(manifestRaw)
	wantDigest, err := hex.DecodeString(strings.TrimPrefix(submission.Source.ManifestDigest, "sha256:"))
	if err != nil || len(wantDigest) != sha256.Size || !bytes.Equal(digest[:], wantDigest) {
		return errors.New("source.manifestDigest does not match pinned manifest bytes")
	}
	manifest, err := parsePinnedManifest(submission, manifestRaw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(manifest, submission.Manifest) {
		return errors.New("pinned manifest does not match submission.manifest")
	}
	parsedGoMod, err := modfile.Parse("go.mod", goModRaw, nil)
	if err != nil {
		return errors.New("pinned go.mod is invalid")
	}
	if parsedGoMod.Module == nil || parsedGoMod.Module.Mod.Path != submission.Name {
		return errors.New("pinned go.mod module path does not match submission.name")
	}
	_, pathMajor, ok := module.SplitPathVersion(parsedGoMod.Module.Mod.Path)
	if !ok || module.CheckPathMajor(submission.Version, pathMajor) != nil {
		return errors.New("submission.version does not match pinned go.mod path-major")
	}
	return nil
}

func parsePinnedManifest(submission Submission, data []byte) (NodePackageManifest, error) {
	if len(data) > manifestLimit || !utf8.Valid(data) {
		return NodePackageManifest{}, errors.New("pinned manifest has invalid size or UTF-8")
	}
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return NodePackageManifest{}, errors.New("pinned manifest is invalid")
	}
	var wire manifestWire
	if err := decodeOneStrict(data, &wire); err != nil {
		return NodePackageManifest{}, errors.New("pinned manifest is invalid")
	}
	manifest, err := wire.value("manifest")
	if err != nil {
		return NodePackageManifest{}, errors.New("pinned manifest is invalid")
	}
	withPinnedManifest := submission
	withPinnedManifest.Manifest = manifest
	if err := validateManifest(withPinnedManifest); err != nil {
		return NodePackageManifest{}, errors.New("pinned manifest is invalid")
	}
	return manifest, nil
}
