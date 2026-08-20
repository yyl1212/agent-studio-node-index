package indexgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseSubmissionStrictness(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"duplicate-key.json", "duplicate object key"},
		{"path-traversal.json", "source.moduleDir"},
		{"manifest-mismatch.json", "manifest.metadata.name"},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../testdata/invalid", test.file))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseSubmission(test.file, data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestParseSubmissionNormalizesSlices(t *testing.T) {
	data := validSubmissionBytes(t)
	got, err := ParseSubmission("valid.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Categories, []string{"integration", "search"}) {
		t.Fatalf("categories=%v", got.Categories)
	}
	if !slices.Equal(got.Keywords, []string{"http", "搜索"}) {
		t.Fatalf("keywords=%v", got.Keywords)
	}
}

func TestParseSubmissionNormalizesEmptySlicesToArrays(t *testing.T) {
	data := mutateValidSubmission(t, func(value map[string]any) {
		value["categories"] = []any{}
		value["keywords"] = []any{}
	})
	got, err := ParseSubmission("empty-slices.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Categories == nil || got.Keywords == nil {
		t.Fatalf("nil slices: categories=%v keywords=%v", got.Categories, got.Keywords)
	}
}

func TestParseSubmissionAcceptsGoSemverShorthandAndPathMajor(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"v1 shorthand", func(value map[string]any) { value["version"] = "v1" }},
		{"v1.2 shorthand", func(value map[string]any) { value["version"] = "v1.2" }},
		{"v2 module", func(value map[string]any) {
			value["name"] = "github.com/example/agent-nodes/v2"
			value["version"] = "v2.0.0"
			object(object(value, "manifest"), "metadata")["name"] = value["name"]
			firstSubmissionRegistration(value)["package"] = "github.com/example/agent-nodes/v2/search"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseSubmission(test.name, mutateValidSubmission(t, test.mutate)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRejectDuplicateObjectKeysUsesJSONNumbers(t *testing.T) {
	if err := rejectDuplicateObjectKeys([]byte(`{"large":1e400,"nested":[{"ok":true}]}`)); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	if err := rejectDuplicateObjectKeys([]byte(`{"nested":[{"key":1,"key":2}]}`)); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("nested duplicate err=%v", err)
	}
}

func TestParseSubmissionRejectsInvalidInputEnvelope(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"oversized", make([]byte, MaxSubmissionBytes+1), "invalid size or UTF-8"},
		{"invalid UTF-8", []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, "invalid size or UTF-8"},
		{"trailing JSON", append(validSubmissionBytes(t), []byte(` {}`)...), "multiple JSON values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSubmission(test.name, test.data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestParseSubmissionRequiresEverySchemaField(t *testing.T) {
	tests := []struct {
		name   string
		remove func(map[string]any)
		want   string
	}{
		{"top-level categories", func(v map[string]any) { delete(v, "categories") }, "categories"},
		{"source tag", func(v map[string]any) { delete(object(v, "source"), "tag") }, "source.tag"},
		{"lifecycle message", func(v map[string]any) { delete(object(v, "lifecycle"), "message") }, "lifecycle.message"},
		{"metadata description", func(v map[string]any) { delete(object(object(v, "manifest"), "metadata"), "description") }, "manifest.metadata.description"},
		{"runtime max", func(v map[string]any) {
			manifest := object(v, "manifest")
			compatibility := object(manifest, "compatibility")
			delete(object(compatibility, "runtime"), "maxVersionExclusive")
		}, "manifest.compatibility.runtime.maxVersionExclusive"},
		{"registration nodes", func(v map[string]any) { delete(firstSubmissionRegistration(v), "nodes") }, "manifest.registrations[0].nodes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateValidSubmission(t, func(value map[string]any) { test.remove(value) })
			_, err := ParseSubmission(test.name, data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}

	data := mutateValidSubmission(t, func(value map[string]any) { value["categories"] = nil })
	if _, err := ParseSubmission("null categories", data); err == nil || !strings.Contains(err.Error(), "categories") {
		t.Fatalf("null categories err=%v", err)
	}
}

func TestParseSubmissionRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"additional property", func(value map[string]any) { object(value, "source")["unexpected"] = true }},
		{"wrong field case", func(value map[string]any) {
			source := object(value, "source")
			source["Repository"] = source["repository"]
			delete(source, "repository")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateValidSubmission(t, test.mutate)
			_, err := ParseSubmission(test.name, data)
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestParseSubmissionSemanticValidation(t *testing.T) {
	uniqueStrings := func(prefix string, count int) []any {
		values := make([]any, count)
		for i := range values {
			values[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return values
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"api version", func(v map[string]any) { v["apiVersion"] = "agent-studio.dev/v2" }, "apiVersion"},
		{"kind", func(v map[string]any) { v["kind"] = "Other" }, "kind"},
		{"empty name", func(v map[string]any) {
			v["name"] = ""
			object(object(v, "manifest"), "metadata")["name"] = ""
		}, "name"},
		{"long name", func(v map[string]any) {
			v["name"] = strings.Repeat("a", 513)
			object(object(v, "manifest"), "metadata")["name"] = v["name"]
		}, "name"},
		{"invalid module name", func(v map[string]any) {
			v["name"] = "github.com/example/agent nodes"
			object(object(v, "manifest"), "metadata")["name"] = v["name"]
		}, "name"},
		{"invalid Go semver", func(v map[string]any) { v["version"] = "v1.2.3-01" }, "version"},
		{"path major mismatch", func(v map[string]any) {
			v["name"] = "github.com/example/agent-nodes/v2"
			object(object(v, "manifest"), "metadata")["name"] = v["name"]
		}, "path-major"},
		{"non-GitHub repository", func(v map[string]any) { object(v, "source")["repository"] = "https://example.com/agent-nodes" }, "source.repository"},
		{"repository module mismatch", func(v map[string]any) { object(v, "source")["repository"] = "https://github.com/other/agent-nodes" }, "source.repository"},
		{"empty module dir", func(v map[string]any) { object(v, "source")["moduleDir"] = "" }, "source.moduleDir"},
		{"long module dir", func(v map[string]any) { object(v, "source")["moduleDir"] = strings.Repeat("a", 1025) }, "source.moduleDir"},
		{"backslash module dir", func(v map[string]any) { object(v, "source")["moduleDir"] = `a\b` }, "source.moduleDir"},
		{"empty tag", func(v map[string]any) { object(v, "source")["tag"] = "" }, "source.tag"},
		{"long tag", func(v map[string]any) { object(v, "source")["tag"] = strings.Repeat("a", 513) }, "source.tag"},
		{"invalid tag", func(v map[string]any) { object(v, "source")["tag"] = "bad..tag" }, "source.tag"},
		{"invalid commit", func(v map[string]any) { object(v, "source")["commit"] = strings.Repeat("A", 40) }, "source.commit"},
		{"invalid digest", func(v map[string]any) { object(v, "source")["manifestDigest"] = "sha256:" + strings.Repeat("g", 64) }, "source.manifestDigest"},
		{"too many categories", func(v map[string]any) { v["categories"] = uniqueStrings("category", 9) }, "categories"},
		{"duplicate category", func(v map[string]any) { v["categories"] = []any{"search", "search"} }, "categories"},
		{"invalid category slug", func(v map[string]any) { v["categories"] = []any{"Search"} }, "categories[0]"},
		{"long category", func(v map[string]any) { v["categories"] = []any{strings.Repeat("a", 33)} }, "categories[0]"},
		{"too many keywords", func(v map[string]any) { v["keywords"] = uniqueStrings("keyword", 17) }, "keywords"},
		{"duplicate keyword", func(v map[string]any) { v["keywords"] = []any{"搜索", "搜索"} }, "keywords"},
		{"empty keyword", func(v map[string]any) { v["keywords"] = []any{""} }, "keywords[0]"},
		{"long Unicode keyword", func(v map[string]any) { v["keywords"] = []any{strings.Repeat("界", 65)} }, "keywords[0]"},
		{"invalid lifecycle status", func(v map[string]any) { object(v, "lifecycle")["status"] = "pending" }, "lifecycle.status"},
		{"active lifecycle message", func(v map[string]any) { object(v, "lifecycle")["message"] = "not empty" }, "lifecycle.message"},
		{"deprecated lifecycle empty message", func(v map[string]any) {
			object(v, "lifecycle")["status"] = "deprecated"
		}, "lifecycle.message"},
		{"lifecycle HTML", func(v map[string]any) {
			object(v, "lifecycle")["status"] = "withdrawn"
			object(v, "lifecycle")["message"] = "<unsafe>"
		}, "lifecycle.message"},
		{"long lifecycle message", func(v map[string]any) {
			object(v, "lifecycle")["status"] = "deprecated"
			object(v, "lifecycle")["message"] = strings.Repeat("界", 2049)
		}, "lifecycle.message"},
		{"manifest api version", func(v map[string]any) { object(v, "manifest")["apiVersion"] = "v2" }, "manifest.apiVersion"},
		{"manifest kind", func(v map[string]any) { object(v, "manifest")["kind"] = "Other" }, "manifest.kind"},
		{"empty display name", func(v map[string]any) { object(object(v, "manifest"), "metadata")["displayName"] = "" }, "manifest.metadata.displayName"},
		{"long display name", func(v map[string]any) {
			object(object(v, "manifest"), "metadata")["displayName"] = strings.Repeat("界", 129)
		}, "manifest.metadata.displayName"},
		{"long description", func(v map[string]any) {
			object(object(v, "manifest"), "metadata")["description"] = strings.Repeat("界", 2049)
		}, "manifest.metadata.description"},
		{"empty license", func(v map[string]any) { object(object(v, "manifest"), "metadata")["license"] = "" }, "manifest.metadata.license"},
		{"long license", func(v map[string]any) {
			object(object(v, "manifest"), "metadata")["license"] = strings.Repeat("a", 129)
		}, "manifest.metadata.license"},
		{"manifest repository mismatch", func(v map[string]any) {
			object(object(v, "manifest"), "metadata")["repository"] = "https://github.com/example/other"
		}, "manifest.metadata.repository"},
		{"node API mismatch", func(v map[string]any) {
			object(object(v, "manifest"), "compatibility")["nodeAPI"] = "agent-studio.dev/v2"
		}, "manifest.compatibility.nodeAPI"},
		{"runtime shorthand", func(v map[string]any) {
			object(object(object(v, "manifest"), "compatibility"), "runtime")["minVersion"] = "v0.3"
		}, "manifest.compatibility.runtime.minVersion"},
		{"runtime order", func(v map[string]any) {
			object(object(object(v, "manifest"), "compatibility"), "runtime")["maxVersionExclusive"] = "v0.2.0"
		}, "manifest.compatibility.runtime"},
		{"too many registrations", func(v map[string]any) {
			registration := map[string]any{"package": "github.com/example/agent-nodes/search", "nodes": []any{map[string]any{"type": "example.search", "version": "1"}}}
			object(v, "manifest")["registrations"] = repeated(registration, 129)
		}, "manifest.registrations"},
		{"empty registration package", func(v map[string]any) { firstSubmissionRegistration(v)["package"] = "" }, "manifest.registrations[0].package"},
		{"long registration package", func(v map[string]any) { firstSubmissionRegistration(v)["package"] = strings.Repeat("a", 513) }, "manifest.registrations[0].package"},
		{"empty registration nodes", func(v map[string]any) { firstSubmissionRegistration(v)["nodes"] = []any{} }, "manifest.registrations[0].nodes"},
		{"too many total nodes", func(v map[string]any) {
			nodes := make([]any, 513)
			for i := range nodes {
				nodes[i] = map[string]any{"type": fmt.Sprintf("example.node%d", i), "version": "1"}
			}
			object(v, "manifest")["registrations"] = []any{
				map[string]any{"package": "github.com/example/agent-nodes/a", "nodes": nodes[:256]},
				map[string]any{"package": "github.com/example/agent-nodes/b", "nodes": nodes[256:]},
			}
		}, "manifest.registrations nodes"},
		{"empty node type", func(v map[string]any) { firstSubmissionNode(v)["type"] = "" }, "manifest.registrations[0].nodes[0].type"},
		{"long node type", func(v map[string]any) { firstSubmissionNode(v)["type"] = strings.Repeat("a", 257) }, "manifest.registrations[0].nodes[0].type"},
		{"empty node version", func(v map[string]any) { firstSubmissionNode(v)["version"] = "" }, "manifest.registrations[0].nodes[0].version"},
		{"long node version", func(v map[string]any) { firstSubmissionNode(v)["version"] = strings.Repeat("a", 129) }, "manifest.registrations[0].nodes[0].version"},
		{"duplicate node identity", func(v map[string]any) {
			node := map[string]any{"type": "example.search", "version": "1"}
			object(v, "manifest")["registrations"] = []any{
				map[string]any{"package": "github.com/example/agent-nodes/a", "nodes": []any{node}},
				map[string]any{"package": "github.com/example/agent-nodes/b", "nodes": []any{node}},
			}
		}, "duplicate manifest node"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateValidSubmission(t, test.mutate)
			_, err := ParseSubmission(test.name, data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestLoadSubmissionsRejectsSymlinkBeforeReading(t *testing.T) {
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	if err := os.Mkdir(packages, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, validSubmissionBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(packages, strings.Repeat("a", 64)+".json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSubmissions(root)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSubmissionsReadsSortedFilesAndGitMetadata(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join("packages", "1410eb79d258a3f257af52215f8d0aa54fc09d5aca26ebe9ddaffbf04bda41b7.json")
	secondPath := filepath.Join("packages", "a75865b74ed3c6f6942aa404106c919678468d8a00284015d4178f98d07eea5e.json")
	writeSubmissionFile(t, root, firstPath, validSubmissionBytes(t))
	second := mutateValidSubmission(t, func(value map[string]any) { value["version"] = "v1.2.4" })
	writeSubmissionFile(t, root, secondPath, second)

	originalRunGit := runGit
	t.Cleanup(func() { runGit = originalRunGit })
	var calls [][]string
	runGit = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte("89abcdef0123456789abcdef0123456789abcdef\x002026-08-20T15:30:00+08:00\n"), nil
	}

	got, err := LoadSubmissions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != firstPath || got[1].Path != secondPath {
		if len(got) != 2 {
			t.Fatalf("submission count=%d", len(got))
		}
		t.Fatalf("paths=%v", []string{got[0].Path, got[1].Path})
	}
	if got[0].Submission.Version != "v1.2.3" || got[1].Submission.Version != "v1.2.4" {
		t.Fatalf("versions=%q,%q", got[0].Submission.Version, got[1].Submission.Version)
	}
	if got[0].IndexCommit != "89abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("index commit=%q", got[0].IndexCommit)
	}
	wantTime := time.Date(2026, 8, 20, 7, 30, 0, 0, time.UTC)
	if !got[0].ReviewedAt.Equal(wantTime) || got[0].ReviewedAt.Location() != time.UTC {
		t.Fatalf("reviewed at=%s (%s)", got[0].ReviewedAt, got[0].ReviewedAt.Location())
	}
	wantFirstCall := []string{"-C", root, "log", "-1", "--format=%H%x00%cI", "--", firstPath}
	if len(calls) != 2 || !slices.Equal(calls[0], wantFirstCall) {
		t.Fatalf("git calls=%v", calls)
	}
}

func TestLoadSubmissionsRejectsWrongFilenameHashBeforeGit(t *testing.T) {
	root := t.TempDir()
	wrongPath := filepath.Join("packages", strings.Repeat("a", 64)+".json")
	writeSubmissionFile(t, root, wrongPath, validSubmissionBytes(t))
	originalRunGit := runGit
	t.Cleanup(func() { runGit = originalRunGit })
	runGit = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("git must not run before filename validation")
		return nil, nil
	}

	_, err := LoadSubmissions(root)
	if err == nil || !strings.Contains(err.Error(), "filename hash") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSubmissionsRejectsOversizedFileBeforeGit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join("packages", "1410eb79d258a3f257af52215f8d0aa54fc09d5aca26ebe9ddaffbf04bda41b7.json")
	writeSubmissionFile(t, root, path, make([]byte, MaxSubmissionBytes+1))
	originalRunGit := runGit
	t.Cleanup(func() { runGit = originalRunGit })
	runGit = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("git must not run for oversized input")
		return nil, nil
	}

	_, err := LoadSubmissions(root)
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSubmissionsRejectsPackagesDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	path := "1410eb79d258a3f257af52215f8d0aa54fc09d5aca26ebe9ddaffbf04bda41b7.json"
	if err := os.WriteFile(filepath.Join(external, path), validSubmissionBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "packages")); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSubmissions(root)
	if err == nil || !strings.Contains(err.Error(), "packages directory") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSubmissionsRejectsInvalidGitMetadata(t *testing.T) {
	tests := []struct {
		name   string
		output string
		gitErr error
		want   string
	}{
		{"git failure", "", errors.New("exit 1"), "git log"},
		{"missing commit", "", nil, "missing Git commit"},
		{"invalid commit", "BAD\x002026-08-20T07:30:00Z\n", nil, "Git commit"},
		{"invalid timestamp", "89abcdef0123456789abcdef0123456789abcdef\x00yesterday\n", nil, "timestamp"},
		{"extra metadata", "89abcdef0123456789abcdef0123456789abcdef\x002026-08-20T07:30:00Z\x00extra\n", nil, "Git metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join("packages", "1410eb79d258a3f257af52215f8d0aa54fc09d5aca26ebe9ddaffbf04bda41b7.json")
			writeSubmissionFile(t, root, path, validSubmissionBytes(t))
			originalRunGit := runGit
			t.Cleanup(func() { runGit = originalRunGit })
			runGit = func(context.Context, ...string) ([]byte, error) {
				return []byte(test.output), test.gitErr
			}

			_, err := LoadSubmissions(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func validSubmissionBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/valid/submission.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mutateValidSubmission(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(validSubmissionBytes(t), &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func firstSubmissionRegistration(value map[string]any) map[string]any {
	return object(value, "manifest")["registrations"].([]any)[0].(map[string]any)
}

func firstSubmissionNode(value map[string]any) map[string]any {
	return firstSubmissionRegistration(value)["nodes"].([]any)[0].(map[string]any)
}

func writeSubmissionFile(t *testing.T, root, relativePath string, data []byte) {
	t.Helper()
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
