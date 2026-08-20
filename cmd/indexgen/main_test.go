package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestRunProducesIdenticalPrivateAssetsFromEquivalentGitRoots(t *testing.T) {
	firstRoot, firstCommit := makeGitRoot(t)
	secondRoot, secondCommit := makeGitRoot(t)
	if firstCommit != secondCommit {
		t.Fatalf("fixture commits differ: %s != %s", firstCommit, secondCommit)
	}
	firstOut := filepath.Join(t.TempDir(), "assets")
	secondOut := filepath.Join(t.TempDir(), "assets")
	common := []string{
		"-release", "v0.1.0",
		"-source-commit", "0123456789abcdef0123456789abcdef01234567",
		"-generated-at", "2026-08-20T15:30:00+08:00",
	}
	if err := run(append(slices.Clone(common), "-root", firstRoot, "-out", firstOut)); err != nil {
		t.Fatal(err)
	}
	if err := run(append(slices.Clone(common), "-root", secondRoot, "-out", secondOut)); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"checksums.txt", "index.json", "node-index-v1alpha1.schema.json"} {
		first := readFile(t, filepath.Join(firstOut, name))
		second := readFile(t, filepath.Join(secondOut, name))
		if !bytes.Equal(first, second) {
			t.Fatalf("%s differs across equivalent Git roots", name)
		}
		info, err := os.Stat(filepath.Join(firstOut, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode=%o", name, got)
		}
	}
}

func TestRunFailsClosedWhenOutputDirectoryExists(t *testing.T) {
	root, _ := makeGitRoot(t)
	out := filepath.Join(t.TempDir(), "assets")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(out, "keep")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{
		"-root", root,
		"-release", "v0.1.0",
		"-source-commit", "0123456789abcdef0123456789abcdef01234567",
		"-generated-at", "2026-08-20T07:30:00Z",
		"-out", out,
	})
	if err == nil {
		t.Fatal("existing output directory accepted")
	}
	if got := string(readFile(t, sentinel)); got != "unchanged" {
		t.Fatalf("existing output modified: %q", got)
	}
}

func TestRunRejectsInvalidFlagsBeforeCreatingOutput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing root", []string{"-release", "v0.1.0", "-source-commit", "0123456789abcdef0123456789abcdef01234567", "-generated-at", "2026-08-20T07:30:00Z", "-out", "unused"}},
		{"invalid time", []string{"-root", "unused", "-release", "v0.1.0", "-source-commit", "0123456789abcdef0123456789abcdef01234567", "-generated-at", "now", "-out", "unused"}},
		{"positional argument", []string{"-root", "unused", "-release", "v0.1.0", "-source-commit", "0123456789abcdef0123456789abcdef01234567", "-generated-at", "2026-08-20T07:30:00Z", "-out", "unused", "extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := run(test.args); err == nil {
				t.Fatal("invalid flags accepted")
			}
		})
	}
}

func makeGitRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "schema"), 0o700); err != nil {
		t.Fatal(err)
	}
	submission := readFile(t, filepath.Join("..", "..", "testdata", "valid", "submission.json"))
	if err := os.WriteFile(filepath.Join(root, "packages", "1410eb79d258a3f257af52215f8d0aa54fc09d5aca26ebe9ddaffbf04bda41b7.json"), submission, 0o600); err != nil {
		t.Fatal(err)
	}
	schema := readFile(t, filepath.Join("..", "..", "schema", "node-index-v1alpha1.schema.json"))
	if err := os.WriteFile(filepath.Join(root, "schema", "node-index-v1alpha1.schema.json"), schema, 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "Index Test")
	git(t, root, "config", "user.email", "index@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "add", "packages", "schema")
	command := exec.Command("git", "commit", "--quiet", "-m", "fixture")
	command.Dir = root
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-08-20T15:30:00+08:00",
		"GIT_COMMITTER_DATE=2026-08-20T15:30:00+08:00",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	return root, git(t, root, "rev-parse", "HEAD")
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(bytes.TrimSpace(output))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
