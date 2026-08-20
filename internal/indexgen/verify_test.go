package indexgen

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const fixtureCommit = "0123456789abcdef0123456789abcdef01234567"

type recordedRequest struct {
	Method        string
	URL           string
	Host          string
	Authorization string
}

type fixtureResponse struct {
	status int
	body   string
}

type recordingTransport struct {
	responses []fixtureResponse
	requests  []recordedRequest
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, recordedRequest{
		Method:        request.Method,
		URL:           request.URL.String(),
		Host:          request.URL.Host,
		Authorization: request.Header.Get("Authorization"),
	})
	if len(transport.responses) == 0 {
		return nil, fmt.Errorf("unexpected request")
	}
	response := transport.responses[0]
	transport.responses = transport.responses[1:]
	return &http.Response{
		StatusCode: response.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    request,
	}, nil
}

func (transport *recordingTransport) methods() []string {
	methods := make([]string, len(transport.requests))
	for index, request := range transport.requests {
		methods[index] = request.Method
	}
	return methods
}

func TestVerifyReadsOnlyPinnedGitHubObjects(t *testing.T) {
	submission, manifestRaw, goModRaw := validPinnedFixture(t)
	transport := &recordingTransport{responses: verifiedFixtureResponses(t, manifestRaw, goModRaw)}
	verifier := NewGitHubVerifier(&http.Client{Transport: transport}, "read-only-token")
	if err := verifier.Verify(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	if got := transport.methods(); !slices.Equal(got, []string{"GET", "GET", "GET", "GET"}) {
		t.Fatalf("methods=%v", got)
	}
	for _, request := range transport.requests {
		if request.Host != "api.github.com" {
			t.Fatalf("host=%s", request.Host)
		}
	}
	wantURLs := []string{
		"https://api.github.com/repos/example/agent-nodes/git/ref/tags/v1.2.3",
		"https://api.github.com/repos/example/agent-nodes/git/tags/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"https://api.github.com/repos/example/agent-nodes/contents/agent-studio.node-package.json?ref=" + fixtureCommit,
		"https://api.github.com/repos/example/agent-nodes/contents/go.mod?ref=" + fixtureCommit,
	}
	for index, want := range wantURLs {
		if got := transport.requests[index].URL; got != want {
			t.Fatalf("request %d URL=%q, want %q", index, got, want)
		}
	}
}

func TestVerifyRejectsTagCommitAndManifestDigestMismatch(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(Submission, []byte, []byte) (Submission, []fixtureResponse)
		wantError string
	}{
		{
			name: "tag resolves to another commit",
			mutate: func(submission Submission, manifestRaw, goModRaw []byte) (Submission, []fixtureResponse) {
				responses := verifiedFixtureResponses(t, manifestRaw, goModRaw)
				responses[1].body = `{"object":{"type":"commit","sha":"1123456789abcdef0123456789abcdef01234567"}}`
				return submission, responses
			},
			wantError: "source.tag does not resolve to source.commit",
		},
		{
			name: "manifest raw digest mismatch",
			mutate: func(submission Submission, manifestRaw, goModRaw []byte) (Submission, []fixtureResponse) {
				manifestRaw = append(append([]byte(nil), manifestRaw...), '\n')
				return submission, verifiedFixtureResponses(t, manifestRaw, goModRaw)
			},
			wantError: "source.manifestDigest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			submission, manifestRaw, goModRaw := validPinnedFixture(t)
			submission, responses := test.mutate(submission, manifestRaw, goModRaw)
			transport := &recordingTransport{responses: responses}
			err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGitHubVerifierRejectsHTTPFailuresAndUnsafeResponseData(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "not found", status: http.StatusNotFound},
		{name: "forbidden", status: http.StatusForbidden},
		{name: "rate limited", status: http.StatusTooManyRequests},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingTransport{responses: []fixtureResponse{{
				status: test.status,
				body:   "remote-body-secret-token",
			}}}
			submission, _, _ := validPinnedFixture(t)
			err := NewGitHubVerifier(&http.Client{Transport: transport}, "secret-token").Verify(context.Background(), submission)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("status %d", test.status)) {
				t.Fatalf("err=%v", err)
			}
			if strings.Contains(err.Error(), "remote-body") || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe error=%q", err)
			}
		})
	}

	t.Run("transport error", func(t *testing.T) {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport leaked secret-token and remote-body")
		})
		submission, _, _ := validPinnedFixture(t)
		err := NewGitHubVerifier(&http.Client{Transport: transport}, "secret-token").Verify(context.Background(), submission)
		if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "remote-body") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("untrusted manifest validation", func(t *testing.T) {
		submission, manifestRaw, goModRaw := validPinnedFixture(t)
		var manifest map[string]any
		if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
			t.Fatal(err)
		}
		registrations := manifest["registrations"].([]any)
		registration := registrations[0].(map[string]any)
		registration["nodes"] = []any{
			map[string]any{"type": "secret-token", "version": "remote-body"},
			map[string]any{"type": "secret-token", "version": "remote-body"},
		}
		unsafeManifest, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		setManifestDigest(&submission, unsafeManifest)
		transport := &recordingTransport{responses: verifiedFixtureResponses(t, unsafeManifest, goModRaw)}
		err = NewGitHubVerifier(&http.Client{Transport: transport}, "secret-token").Verify(context.Background(), submission)
		if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "remote-body") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestGitHubVerifierEnforcesResponseAndDecodeLimits(t *testing.T) {
	t.Run("object response", func(t *testing.T) {
		transport := &recordingTransport{responses: []fixtureResponse{{
			status: http.StatusOK,
			body:   strings.Repeat("x", objectResponseLimit+1),
		}}}
		submission, _, _ := validPinnedFixture(t)
		err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("encoded content", func(t *testing.T) {
		submission, _, goModRaw := validPinnedFixture(t)
		responses := verifiedFixtureResponses(t, nil, goModRaw)
		responses[2].body = contentFixture(t, []byte(strings.Repeat("a", manifestLimit+1)))
		transport := &recordingTransport{responses: responses}
		err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("invalid base64", func(t *testing.T) {
		submission, manifestRaw, goModRaw := validPinnedFixture(t)
		responses := verifiedFixtureResponses(t, manifestRaw, goModRaw)
		responses[2].body = `{"encoding":"base64","content":"%%%"}`
		transport := &recordingTransport{responses: responses}
		err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
		if err == nil || !strings.Contains(err.Error(), "invalid base64") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestGitHubVerifierRejectsRecursiveAnnotatedTag(t *testing.T) {
	submission, manifestRaw, goModRaw := validPinnedFixture(t)
	responses := verifiedFixtureResponses(t, manifestRaw, goModRaw)
	responses[1].body = `{"object":{"type":"tag","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`
	transport := &recordingTransport{responses: responses}
	err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
	if err == nil || !strings.Contains(err.Error(), "directly to a commit") {
		t.Fatalf("err=%v", err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("request count=%d", len(transport.requests))
	}
}

func TestGitHubVerifierPreservesCancellation(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	submission, _, _ := validPinnedFixture(t)
	err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(ctx, submission)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubVerifierPreservesClientDeadline(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	submission, _, _ := validPinnedFixture(t)
	err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubVerifierCopiesClientBoundsTimeoutAndScopesAuthorization(t *testing.T) {
	transport := &recordingTransport{}
	originalRedirect := func(*http.Request, []*http.Request) error { return errors.New("original") }
	client := &http.Client{Transport: transport, Timeout: 2 * time.Hour, CheckRedirect: originalRedirect}
	verifier := NewGitHubVerifier(client, "read-only-token")
	if client.Transport != transport || client.Timeout != 2*time.Hour {
		t.Fatal("constructor mutated caller client")
	}
	if verifier.client.Timeout <= 0 || verifier.client.Timeout > githubVerifierLimit {
		t.Fatalf("timeout=%s", verifier.client.Timeout)
	}

	submission, manifestRaw, goModRaw := validPinnedFixture(t)
	transport.responses = verifiedFixtureResponses(t, manifestRaw, goModRaw)
	if err := verifier.Verify(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	for _, request := range transport.requests {
		if request.Authorization != "Bearer read-only-token" || request.Host != githubAPIHost {
			t.Fatalf("request=%+v", request)
		}
	}

	called := false
	redirectingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if called {
			t.Fatalf("redirect reached transport: %s", request.URL)
		}
		called = true
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://example.com/stolen"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	err := NewGitHubVerifier(&http.Client{Transport: redirectingTransport}, "secret-token").Verify(context.Background(), submission)
	if err == nil || strings.Contains(err.Error(), "example.com") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubVerifierEscapesEveryPathSegment(t *testing.T) {
	submission, manifestRaw, goModRaw := validPinnedFixture(t)
	submission.Source.Tag = "release/v1.2.3"
	submission.Source.ModuleDir = "dir ?#%"
	transport := &recordingTransport{responses: verifiedFixtureResponses(t, manifestRaw, goModRaw)}
	if err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	if got, want := transport.requests[0].URL, "https://api.github.com/repos/example/agent-nodes/git/ref/tags/release%2Fv1.2.3"; got != want {
		t.Fatalf("tag URL=%q, want %q", got, want)
	}
	if got, want := transport.requests[2].URL, "https://api.github.com/repos/example/agent-nodes/contents/dir%20%3F%23%25/agent-studio.node-package.json?ref="+fixtureCommit; got != want {
		t.Fatalf("content URL=%q, want %q", got, want)
	}
}

func TestGitHubVerifierChecksPinnedManifestAndGoModuleValues(t *testing.T) {
	t.Run("manifest typed value", func(t *testing.T) {
		submission, manifestRaw, goModRaw := validPinnedFixture(t)
		manifestRaw = bytesReplace(t, manifestRaw, "Example Agent Nodes", "Changed Agent Nodes")
		setManifestDigest(&submission, manifestRaw)
		transport := &recordingTransport{responses: verifiedFixtureResponses(t, manifestRaw, goModRaw)}
		err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
		if err == nil || !strings.Contains(err.Error(), "submission.manifest") {
			t.Fatalf("err=%v", err)
		}
	})

	for _, test := range []struct {
		name  string
		goMod string
	}{
		{name: "invalid go.mod", goMod: "not a go.mod"},
		{name: "wrong module", goMod: "module github.com/example/other\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			submission, manifestRaw, _ := validPinnedFixture(t)
			transport := &recordingTransport{responses: verifiedFixtureResponses(t, manifestRaw, []byte(test.goMod))}
			err := NewGitHubVerifier(&http.Client{Transport: transport}, "").Verify(context.Background(), submission)
			if err == nil || !strings.Contains(err.Error(), "go.mod") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func setManifestDigest(submission *Submission, manifestRaw []byte) {
	digest := sha256.Sum256(manifestRaw)
	submission.Source.ManifestDigest = fmt.Sprintf("sha256:%x", digest)
}

func bytesReplace(t *testing.T, data []byte, old, replacement string) []byte {
	t.Helper()
	result := []byte(strings.Replace(string(data), old, replacement, 1))
	if string(result) == string(data) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return result
}

func validPinnedFixture(t *testing.T) (Submission, []byte, []byte) {
	t.Helper()
	data, err := io.ReadAll(strings.NewReader(pinnedSubmissionJSON))
	if err != nil {
		t.Fatal(err)
	}
	submission, err := ParseSubmission("fixture.json", data)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(submission.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(manifestRaw)
	submission.Source.ManifestDigest = fmt.Sprintf("sha256:%x", digest)
	return submission, manifestRaw, []byte("module github.com/example/agent-nodes\n\ngo 1.26.0\n")
}

func verifiedFixtureResponses(t *testing.T, manifestRaw, goModRaw []byte) []fixtureResponse {
	t.Helper()
	return []fixtureResponse{
		{status: http.StatusOK, body: `{"object":{"type":"tag","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`},
		{status: http.StatusOK, body: `{"object":{"type":"commit","sha":"` + fixtureCommit + `"}}`},
		{status: http.StatusOK, body: contentFixture(t, manifestRaw)},
		{status: http.StatusOK, body: contentFixture(t, goModRaw)},
	}
}

func contentFixture(t *testing.T, data []byte) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{
		"encoding": "base64",
		"content":  base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

const pinnedSubmissionJSON = `{
  "apiVersion":"agent-studio.dev/v1alpha1",
  "kind":"NodePackageSubmission",
  "name":"github.com/example/agent-nodes",
  "version":"v1.2.3",
  "source":{"repository":"https://github.com/example/agent-nodes","moduleDir":".","tag":"v1.2.3","commit":"` + fixtureCommit + `","manifestDigest":"sha256:89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef"},
  "categories":["search","integration"],
  "keywords":["搜索","http"],
  "lifecycle":{"status":"active","message":""},
  "manifest":{
    "apiVersion":"agent-studio.dev/v1alpha1",
    "kind":"NodePackage",
    "metadata":{"name":"github.com/example/agent-nodes","displayName":"Example Agent Nodes","description":"Example search integration nodes","license":"Apache-2.0","repository":"https://github.com/example/agent-nodes"},
    "compatibility":{"nodeAPI":"agent-studio.dev/v1alpha1","runtime":{"minVersion":"v0.3.0","maxVersionExclusive":"v0.4.0"}},
    "registrations":[{"package":"github.com/example/agent-nodes/search","nodes":[{"type":"example.search","version":"1"}]}]
  }
}`
