package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
)

type recordingVerifier struct {
	submissions []indexgen.Submission
	err         error
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
