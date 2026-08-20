package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
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
