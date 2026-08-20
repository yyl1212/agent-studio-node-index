package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
	"golang.org/x/mod/semver"
)

type recordingVerifier struct {
	submissions []indexgen.Submission
	err         error
}

type countingTransport struct {
	calls int
}

func (transport *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("unexpected HTTP request")
}

func (verifier *recordingVerifier) Verify(_ context.Context, submission indexgen.Submission) error {
	verifier.submissions = append(verifier.submissions, submission)
	return verifier.err
}

func TestIndexCheckChecksAllPackagesWhenChangedFileIsAbsent(t *testing.T) {
	root := t.TempDir()
	firstPath := writeCandidateSubmission(t, root, validCandidateBytes(t))
	second := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, `"version": "v1.2.4"`)
	secondPath := writeCandidateSubmission(t, root, second)
	writeCandidateTrapFiles(t, root)

	verifier := &recordingVerifier{}
	if err := runIndexCheck(context.Background(), []string{"-root", root}, func(string) string { return "" }, func(*http.Client, string) submissionVerifier {
		return verifier
	}); err != nil {
		t.Fatal(err)
	}
	if got := versions(verifier.submissions); !slices.Equal(got, []string{"v1.2.3", "v1.2.4"}) {
		t.Fatalf("verified versions=%v", got)
	}
	if firstPath == secondPath {
		t.Fatal("fixtures unexpectedly have the same path")
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
		t.Fatalf("candidate code was executed: %v", err)
	}
}

func TestIndexCheckProcessesMultipleChangedFiles(t *testing.T) {
	root := t.TempDir()
	firstPath := writeCandidateSubmission(t, root, validCandidateBytes(t))
	second := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, `"version": "v1.2.4"`)
	secondPath := writeCandidateSubmission(t, root, second)

	verifier := &recordingVerifier{}
	args := []string{"-root", root, "-changed-file", filepath.ToSlash(firstPath), "-changed-file", filepath.ToSlash(secondPath)}
	if err := runIndexCheck(context.Background(), args, func(string) string { return "" }, func(*http.Client, string) submissionVerifier {
		return verifier
	}); err != nil {
		t.Fatal(err)
	}
	if got := versions(verifier.submissions); !slices.Equal(got, []string{"v1.2.3", "v1.2.4"}) {
		t.Fatalf("verified versions=%v", got)
	}
}

func TestIndexCheckPreflightsEntireAggregateBeforeHTTP(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string) string
		want    string
	}{
		{
			name: "semver-equivalent duplicate",
			prepare: func(t *testing.T, root string) string {
				changed := writeCandidateSubmission(t, root, validCandidateBytes(t))
				duplicate := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, `"version": "v1.2.3+build"`)
				writeCandidateSubmission(t, root, duplicate)
				return changed
			},
			want: "duplicate",
		},
		{
			name: "too many versions",
			prepare: func(t *testing.T, root string) string {
				var changed string
				for i := 0; i <= indexgen.MaxVersionsPerPackage; i++ {
					data := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, fmt.Sprintf(`"version": "v1.3.%d"`, i))
					path := writeCandidateSubmission(t, root, data)
					if i == 0 {
						changed = path
					}
				}
				return changed
			},
			want: "versions",
		},
		{
			name: "oversized unchanged file",
			prepare: func(t *testing.T, root string) string {
				changed := writeCandidateSubmission(t, root, validCandidateBytes(t))
				writeCandidatePath(t, root, filepath.Join("packages", strings.Repeat("f", 64)+".json"), make([]byte, indexgen.MaxSubmissionBytes+1))
				return changed
			},
			want: "size",
		},
		{
			name: "duplicate tuple in unchanged file",
			prepare: func(t *testing.T, root string) string {
				changed := writeCandidateSubmission(t, root, validCandidateBytes(t))
				submission, err := indexgen.ParseSubmission("fixture", validCandidateBytes(t))
				if err != nil {
					t.Fatal(err)
				}
				submission.Version = "v1.2.4"
				submission.Manifest.Registrations[0].Nodes = append(
					submission.Manifest.Registrations[0].Nodes,
					submission.Manifest.Registrations[0].Nodes[0],
				)
				data, err := json.Marshal(submission)
				if err != nil {
					t.Fatal(err)
				}
				writeCandidatePath(t, root, filepath.Join("packages", candidateFilename(submission)), data)
				return changed
			},
			want: "valid submission",
		},
		{
			name: "node budget in unchanged file",
			prepare: func(t *testing.T, root string) string {
				changed := writeCandidateSubmission(t, root, validCandidateBytes(t))
				submission, err := indexgen.ParseSubmission("fixture", validCandidateBytes(t))
				if err != nil {
					t.Fatal(err)
				}
				submission.Version = "v1.2.4"
				submission.Manifest.Registrations[0].Nodes = make([]indexgen.NodeRef, 513)
				for i := range submission.Manifest.Registrations[0].Nodes {
					submission.Manifest.Registrations[0].Nodes[i] = indexgen.NodeRef{Type: fmt.Sprintf("node.%03d", i), Version: "1"}
				}
				data, err := json.Marshal(submission)
				if err != nil {
					t.Fatal(err)
				}
				writeCandidatePath(t, root, filepath.Join("packages", candidateFilename(submission)), data)
				return changed
			},
			want: "valid submission",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			changed := test.prepare(t, root)
			transport := &countingTransport{}
			err := runIndexCheck(
				context.Background(),
				[]string{"-root", root, "-changed-file", changed},
				func(string) string { return "" },
				func(*http.Client, string) submissionVerifier {
					return indexgen.NewGitHubVerifier(&http.Client{Transport: transport}, "")
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want substring %q", err, test.want)
			}
			if transport.calls != 0 {
				t.Fatalf("HTTP calls=%d, want zero before aggregate preflight", transport.calls)
			}
		})
	}
}

func TestIndexCheckMaximumMetadataEnvelopeIsContractValid(t *testing.T) {
	release := independentMaximumStableRelease()
	commit := independentMaximumGitOID()
	if got := maximumPreflightStableRelease(); got != release {
		t.Fatalf("production maximum release=%q, want independently constructed %q", got, release)
	}
	if got := maximumPreflightGitOID(); got != commit {
		t.Fatalf("production maximum Git OID=%q, want independently constructed %q", got, commit)
	}
	if got := utf8.RuneCountInString(release); got != 128 {
		t.Fatalf("maximum release code points=%d, want 128", got)
	}
	if len(release) != 128 || !semver.IsValid(release) || semver.Prerelease(release) != "" || semver.Build(release) != "" {
		t.Fatalf("maximum release is not a 128-byte stable SemVer: %q", release)
	}
	if len(commit) != 64 || strings.Trim(commit, "0123456789abcdef") != "" {
		t.Fatalf("maximum Git OID is not 64 lowercase hex bytes: %q", commit)
	}

	submission, err := indexgen.ParseSubmission("fixture", validCandidateBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	index := generateEnvelopeIndex(t, []indexgen.Submission{submission}, release, commit)
	if index.Metadata.SourceCommit != commit || index.Packages[0].Versions[0].Review.IndexCommit != commit {
		t.Fatalf("maximum OID did not reach every generated field: metadata=%q review=%q", index.Metadata.SourceCommit, index.Packages[0].Versions[0].Review.IndexCommit)
	}
	encoded := referenceCanonicalIndexEncoding(t, index)
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.NewCompiler().Compile("../../schema/node-index-v1alpha1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("maximum metadata envelope violates the real index schema: %v", err)
	}
}

func TestIndexCheckPreflightUsesMaximumMetadataEnvelopeBeforeHTTP(t *testing.T) {
	boundary := newEnvelopeBoundaryFixture(t)
	if boundary.shortCrossingBytes > indexgen.MaxIndexBytes || boundary.maximumCrossingBytes <= indexgen.MaxIndexBytes {
		t.Fatalf(
			"crossing fixture sizes: old-short=%d maximum=%d limit=%d",
			boundary.shortCrossingBytes,
			boundary.maximumCrossingBytes,
			indexgen.MaxIndexBytes,
		)
	}
	const releaseDelta = 128 - len("v0.0.0")
	const sourceCommitDelta = 64 - 40
	wantEnvelopeDelta := releaseDelta + sourceCommitDelta + indexgen.MaxPackages*(64-40)
	if got := boundary.maximumCrossingBytes - boundary.shortCrossingBytes; got != wantEnvelopeDelta {
		t.Fatalf("metadata envelope delta=%d, want hand-derived %d", got, wantEnvelopeDelta)
	}
	t.Logf(
		"old-short=%d maximum=%d limit=%d envelope-delta=%d",
		boundary.shortCrossingBytes,
		boundary.maximumCrossingBytes,
		indexgen.MaxIndexBytes,
		wantEnvelopeDelta,
	)

	crossingRoot := t.TempDir()
	crossingChanged := writeEnvelopeCorpus(t, crossingRoot, boundary.crossing)
	crossingTransport := &successfulCountingTransport{}
	err := runIndexCheck(
		context.Background(),
		[]string{"-root", crossingRoot, "-changed-file", crossingChanged},
		func(string) string { return "" },
		httpProbeVerifierFactory(crossingTransport),
	)
	if err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("crossing preflight err=%v, want maximum size rejection", err)
	}
	if crossingTransport.calls != 0 {
		t.Fatalf("crossing preflight HTTP calls=%d, want zero", crossingTransport.calls)
	}

	underRoot := t.TempDir()
	underChanged := writeEnvelopeCorpus(t, underRoot, boundary.under)
	underTransport := &successfulCountingTransport{}
	if err := runIndexCheck(
		context.Background(),
		[]string{"-root", underRoot, "-changed-file", underChanged},
		func(string) string { return "" },
		httpProbeVerifierFactory(underTransport),
	); err != nil {
		t.Fatalf("just-under-boundary corpus was rejected: %v", err)
	}
	if underTransport.calls != 1 {
		t.Fatalf("just-under-boundary HTTP calls=%d, want one", underTransport.calls)
	}
}

func TestIndexCheckDeduplicatesChangedPathsBeforeVerification(t *testing.T) {
	root := t.TempDir()
	path := writeCandidateSubmission(t, root, validCandidateBytes(t))
	verifier := &recordingVerifier{}
	err := runIndexCheck(
		context.Background(),
		[]string{"-root", root, "-changed-file", path, "-changed-file", path},
		func(string) string { return "" },
		fixedVerifierFactory(verifier),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier.submissions) != 1 {
		t.Fatalf("verification count=%d, want 1", len(verifier.submissions))
	}
}

type deadlineRecordingVerifier struct {
	deadlines []time.Time
}

func (verifier *deadlineRecordingVerifier) Verify(ctx context.Context, _ indexgen.Submission) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing command deadline")
	}
	verifier.deadlines = append(verifier.deadlines, deadline)
	return nil
}

func TestIndexCheckAppliesOneSharedThirtySecondVerificationDeadline(t *testing.T) {
	root := t.TempDir()
	writeCandidateSubmission(t, root, validCandidateBytes(t))
	second := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, `"version": "v1.2.4"`)
	writeCandidateSubmission(t, root, second)
	verifier := &deadlineRecordingVerifier{}
	if err := runIndexCheck(context.Background(), []string{"-root", root}, func(string) string { return "" }, fixedVerifierFactory(verifier)); err != nil {
		t.Fatal(err)
	}
	if len(verifier.deadlines) != 2 || !verifier.deadlines[0].Equal(verifier.deadlines[1]) {
		t.Fatalf("deadlines=%v, want one shared deadline", verifier.deadlines)
	}
	remaining := time.Until(verifier.deadlines[0])
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("command deadline budget=%s, want (0,30s]", remaining)
	}
}

type slowSequenceVerifier struct {
	calls int
}

func (verifier *slowSequenceVerifier) Verify(ctx context.Context, _ indexgen.Submission) error {
	verifier.calls++
	if verifier.calls == 1 {
		select {
		case <-time.After(40 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestIndexCheckSlowSequenceStopsAtOverallDeadline(t *testing.T) {
	root := t.TempDir()
	writeCandidateSubmission(t, root, validCandidateBytes(t))
	second := replaceCandidate(t, validCandidateBytes(t), `"version": "v1.2.3"`, `"version": "v1.2.4"`)
	writeCandidateSubmission(t, root, second)
	verifier := &slowSequenceVerifier{}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runIndexCheck(ctx, []string{"-root", root}, func(string) string { return "" }, fixedVerifierFactory(verifier))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if verifier.calls != 2 || time.Since(started) > time.Second {
		t.Fatalf("calls=%d elapsed=%s", verifier.calls, time.Since(started))
	}
}

func TestIndexCheckRejectsDeletedOutsideAndHashMismatchedFiles(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*testing.T, string) string
		wantError string
	}{
		{
			name: "deleted",
			prepare: func(_ *testing.T, _ string) string {
				return "packages/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"
			},
			wantError: "does not exist",
		},
		{
			name: "outside packages",
			prepare: func(t *testing.T, root string) string {
				if err := os.WriteFile(filepath.Join(root, "outside.json"), validCandidateBytes(t), 0o644); err != nil {
					t.Fatal(err)
				}
				return "outside.json"
			},
			wantError: "packages/",
		},
		{
			name: "hash mismatch",
			prepare: func(t *testing.T, root string) string {
				path := filepath.Join("packages", strings.Repeat("a", 64)+".json")
				writeCandidatePath(t, root, path, validCandidateBytes(t))
				return path
			},
			wantError: "filename hash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			changed := test.prepare(t, root)
			err := runIndexCheck(context.Background(), []string{"-root", root, "-changed-file", changed}, func(string) string { return "" }, func(*http.Client, string) submissionVerifier {
				return &recordingVerifier{}
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestIndexCheckRejectsSymlinkAndOversizedCandidate(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, validCandidateBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join("packages", strings.Repeat("a", 64)+".json")
	if err := os.MkdirAll(filepath.Join(root, "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, symlinkPath)); err != nil {
		t.Fatal(err)
	}
	err := runIndexCheck(context.Background(), []string{"-root", root, "-changed-file", symlinkPath}, func(string) string { return "" }, fixedVerifierFactory(&recordingVerifier{}))
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink err=%v", err)
	}

	oversizedRoot := t.TempDir()
	oversizedPath := filepath.Join("packages", strings.Repeat("b", 64)+".json")
	writeCandidatePath(t, oversizedRoot, oversizedPath, make([]byte, indexgen.MaxSubmissionBytes+1))
	err = runIndexCheck(context.Background(), []string{"-root", oversizedRoot, "-changed-file", oversizedPath}, func(string) string { return "" }, fixedVerifierFactory(&recordingVerifier{}))
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversized err=%v", err)
	}
}

func TestIndexCheckRejectsPackagesDirectorySymlinkForChangedFile(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	data := validCandidateBytes(t)
	submission, err := indexgen.ParseSubmission("fixture", data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(submission.Name + "\n" + submission.Version))
	name := fmt.Sprintf("%x.json", digest)
	if err := os.WriteFile(filepath.Join(external, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "packages")); err != nil {
		t.Fatal(err)
	}
	err = runIndexCheck(context.Background(), []string{"-root", root, "-changed-file", "packages/" + name}, func(string) string { return "" }, fixedVerifierFactory(&recordingVerifier{}))
	if err == nil || !strings.Contains(err.Error(), "packages directory") {
		t.Fatalf("err=%v", err)
	}
}

func TestIndexCheckUsesOnlyGitHubTokenAndDoesNotExposeIt(t *testing.T) {
	root := t.TempDir()
	path := writeCandidateSubmission(t, root, validCandidateBytes(t))
	const token = "secret-token"
	capturedToken := ""
	verifier := &recordingVerifier{}
	getenv := func(name string) string {
		if name != "GITHUB_TOKEN" {
			t.Fatalf("unexpected environment lookup %q", name)
		}
		return token
	}
	err := runIndexCheck(context.Background(), []string{"-root", root, "-changed-file", path}, getenv, func(_ *http.Client, gotToken string) submissionVerifier {
		capturedToken = gotToken
		return verifier
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedToken != token {
		t.Fatalf("token was not passed to verifier")
	}

	err = runIndexCheck(context.Background(), []string{"-root", root, "-token", token}, getenv, fixedVerifierFactory(verifier))
	if err == nil {
		t.Fatal("token flag was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestIndexCheckPropagatesCancellation(t *testing.T) {
	root := t.TempDir()
	writeCandidateSubmission(t, root, validCandidateBytes(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runIndexCheck(ctx, []string{"-root", root}, func(string) string { return "" }, fixedVerifierFactory(&recordingVerifier{}))
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("err=%v", err)
	}
}

func fixedVerifierFactory(verifier submissionVerifier) verifierFactory {
	return func(*http.Client, string) submissionVerifier { return verifier }
}

func validCandidateBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "valid", "submission.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeCandidateSubmission(t *testing.T, root string, data []byte) string {
	t.Helper()
	submission, err := indexgen.ParseSubmission("fixture", data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(submission.Name + "\n" + submission.Version))
	path := filepath.Join("packages", fmt.Sprintf("%x.json", digest))
	writeCandidatePath(t, root, path, data)
	return path
}

func writeCandidatePath(t *testing.T, root, path string, data []byte) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceCandidate(t *testing.T, data []byte, old, replacement string) []byte {
	t.Helper()
	result := []byte(strings.Replace(string(data), old, replacement, 1))
	if string(result) == string(data) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return result
}

func writeCandidateTrapFiles(t *testing.T, root string) {
	t.Helper()
	writeCandidatePath(t, root, filepath.Join("cmd", "indexcheck", "main.go"), []byte("this is deliberately invalid Go and must never be built\n"))
	writeCandidatePath(t, root, filepath.Join(".github", "workflows", "pwn.yml"), []byte("run: touch executed\n"))
	writeCandidatePath(t, root, "go.mod", []byte("replace trusted/module => ./candidate\n"))
}

func versions(submissions []indexgen.Submission) []string {
	result := make([]string, len(submissions))
	for index, submission := range submissions {
		result[index] = submission.Version
	}
	return result
}

const envelopeFixturePackageCount = indexgen.MaxPackages

type envelopeBoundaryFixture struct {
	crossing             []indexgen.Submission
	under                []indexgen.Submission
	shortCrossingBytes   int
	maximumCrossingBytes int
}

func newEnvelopeBoundaryFixture(t *testing.T) envelopeBoundaryFixture {
	t.Helper()
	maximumRelease := independentMaximumStableRelease()
	maximumCommit := independentMaximumGitOID()
	base := envelopeCorpus(t, 1)
	baseBytes := len(referenceCanonicalIndexEncoding(t, generateEnvelopeIndex(t, base, maximumRelease, maximumCommit)))
	second := envelopeCorpus(t, 2)
	secondBytes := len(referenceCanonicalIndexEncoding(t, generateEnvelopeIndex(t, second, maximumRelease, maximumCommit)))
	step := secondBytes - baseBytes
	wantStep := 2 * envelopeFixturePackageCount
	if step != wantStep {
		t.Fatalf("one-rune filler step=%d, want hand-derived %d", step, wantStep)
	}
	if baseBytes > indexgen.MaxIndexBytes {
		t.Fatalf("base envelope fixture size=%d already exceeds limit", baseBytes)
	}
	crossingFiller := 2 + (indexgen.MaxIndexBytes-baseBytes)/step
	if crossingFiller > 2048 {
		t.Fatalf("crossing filler=%d exceeds domain limit", crossingFiller)
	}

	crossing := envelopeCorpus(t, crossingFiller)
	maximumCrossing := generateEnvelopeIndex(t, crossing, maximumRelease, maximumCommit)
	maximumCrossingBytes := len(referenceCanonicalIndexEncoding(t, maximumCrossing))
	shortCrossing := generateEnvelopeIndex(t, crossing, "v0.0.0", strings.Repeat("0", 40))
	shortCrossingBytes := len(referenceCanonicalIndexEncoding(t, shortCrossing))
	if _, err := indexgen.Encode(shortCrossing); err != nil {
		t.Fatalf("old short envelope should fit: size=%d err=%v", shortCrossingBytes, err)
	}
	if _, err := indexgen.Encode(maximumCrossing); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("maximum envelope should exceed limit: size=%d err=%v", maximumCrossingBytes, err)
	}

	under := envelopeCorpus(t, crossingFiller-1)
	maximumUnderBytes := len(referenceCanonicalIndexEncoding(t, generateEnvelopeIndex(t, under, maximumRelease, maximumCommit)))
	if maximumUnderBytes > indexgen.MaxIndexBytes || indexgen.MaxIndexBytes-maximumUnderBytes >= step {
		t.Fatalf("under fixture size=%d is not immediately below limit=%d with step=%d", maximumUnderBytes, indexgen.MaxIndexBytes, step)
	}
	return envelopeBoundaryFixture{
		crossing:             crossing,
		under:                under,
		shortCrossingBytes:   shortCrossingBytes,
		maximumCrossingBytes: maximumCrossingBytes,
	}
}

func envelopeCorpus(t *testing.T, fillerLength int) []indexgen.Submission {
	t.Helper()
	base, err := indexgen.ParseSubmission("fixture", validCandidateBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	submissions := make([]indexgen.Submission, 0, envelopeFixturePackageCount)
	for i := range envelopeFixturePackageCount {
		name := fmt.Sprintf("github.com/example/envelope-nodes-%04d", i)
		repository := "https://" + name
		submission := base
		submission.Name = name
		submission.Source.Repository = repository
		submission.Categories = slices.Clone(base.Categories)
		submission.Keywords = slices.Clone(base.Keywords)
		submission.Lifecycle = indexgen.Lifecycle{Status: "deprecated", Message: strings.Repeat("m", fillerLength)}
		submission.Manifest.Metadata.Name = name
		submission.Manifest.Metadata.Description = strings.Repeat("d", fillerLength)
		submission.Manifest.Metadata.Repository = repository
		submission.Manifest.Registrations = make([]indexgen.Registration, len(base.Manifest.Registrations))
		for j, registration := range base.Manifest.Registrations {
			submission.Manifest.Registrations[j] = registration
			submission.Manifest.Registrations[j].Nodes = slices.Clone(registration.Nodes)
		}
		submission.Manifest.Registrations[0].Package = name + "/search"
		submissions = append(submissions, submission)
	}
	return submissions
}

func generateEnvelopeIndex(t *testing.T, submissions []indexgen.Submission, release, commit string) indexgen.Index {
	t.Helper()
	files := make([]indexgen.SubmissionFile, len(submissions))
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, submission := range submissions {
		files[i] = indexgen.SubmissionFile{
			Path:        fmt.Sprintf("packages/envelope-%04d.json", i),
			Submission:  submission,
			IndexCommit: commit,
			ReviewedAt:  stamp,
		}
	}
	index, err := indexgen.Generate(indexgen.GenerateInput{
		Release:      release,
		SourceCommit: commit,
		GeneratedAt:  stamp,
		Submissions:  files,
	})
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func referenceCanonicalIndexEncoding(t *testing.T, index indexgen.Index) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(index); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func independentMaximumStableRelease() string {
	return "v" + strings.Repeat("9", 123) + ".1.1"
}

func independentMaximumGitOID() string {
	return strings.Repeat("f", 64)
}

func writeEnvelopeCorpus(t *testing.T, root string, submissions []indexgen.Submission) string {
	t.Helper()
	changed := ""
	for _, submission := range submissions {
		data, err := json.Marshal(submission)
		if err != nil {
			t.Fatal(err)
		}
		path := writeCandidateSubmission(t, root, data)
		if changed == "" {
			changed = filepath.ToSlash(path)
		}
	}
	return changed
}

type successfulCountingTransport struct {
	calls int
}

func (transport *successfulCountingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

type httpProbeVerifier struct {
	client *http.Client
}

func (verifier *httpProbeVerifier) Verify(ctx context.Context, _ indexgen.Submission) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://preflight.invalid/verify", nil)
	if err != nil {
		return err
	}
	response, err := verifier.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return nil
}

func httpProbeVerifierFactory(transport http.RoundTripper) verifierFactory {
	return func(*http.Client, string) submissionVerifier {
		return &httpProbeVerifier{client: &http.Client{Transport: transport}}
	}
}
