package indexgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReleaseRequiresStableCurrentVersion(t *testing.T) {
	input := fixtureReleaseInput()
	assets, err := BuildReleaseAssets(input)
	if err != nil {
		t.Fatal(err)
	}
	var index Index
	if err := json.Unmarshal(assets["index.json"], &index); err != nil {
		t.Fatal(err)
	}
	if index.Metadata.Release != input.GenerateInput.Release {
		t.Fatalf("release=%q, want current release %q", index.Metadata.Release, input.GenerateInput.Release)
	}

	for _, release := range []string{"v1.2", "v1.2.3-rc.1", "v1.2.3+build"} {
		t.Run(release, func(t *testing.T) {
			input := fixtureReleaseInput()
			input.GenerateInput.Release = release
			if _, err := BuildReleaseAssets(input); err == nil || !strings.Contains(err.Error(), "stable full SemVer") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReleaseRejectsInvalidSourceCommit(t *testing.T) {
	for _, sourceCommit := range []string{"bad", strings.Repeat("A", 40), strings.Repeat("0", 39)} {
		t.Run(sourceCommit, func(t *testing.T) {
			input := fixtureReleaseInput()
			input.GenerateInput.SourceCommit = sourceCommit
			if _, err := BuildReleaseAssets(input); err == nil || !strings.Contains(err.Error(), "source commit") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReleaseAssetsAreDeterministicAndExact(t *testing.T) {
	first, err := BuildReleaseAssets(fixtureReleaseInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReleaseAssets(fixtureReleaseInput())
	if err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, len(first))
	for name := range first {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"checksums.txt", "index.json", "node-index-v1alpha1.schema.json"}) {
		t.Fatalf("asset names=%v", keys)
	}
	for _, name := range keys {
		if !bytes.Equal(first[name], second[name]) {
			t.Fatalf("%s is not byte deterministic", name)
		}
	}
}

func TestReleaseChecksumsDetectAssetMismatch(t *testing.T) {
	assets, err := BuildReleaseAssets(fixtureReleaseInput())
	if err != nil {
		t.Fatal(err)
	}
	if mismatch := releaseChecksumMismatch(assets); mismatch != "" {
		t.Fatalf("generated checksum mismatch: %s", mismatch)
	}

	tampered := make(map[string][]byte, len(assets))
	for name, data := range assets {
		tampered[name] = slices.Clone(data)
	}
	tampered["index.json"][0] ^= 1
	if mismatch := releaseChecksumMismatch(tampered); mismatch != "index.json" {
		t.Fatalf("mismatch=%q, want index.json", mismatch)
	}
}

func releaseChecksumMismatch(assets map[string][]byte) string {
	lines := strings.Split(strings.TrimSuffix(string(assets["checksums.txt"]), "\n"), "\n")
	if len(lines) != 2 {
		return "checksums.txt"
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "checksums.txt"
		}
		data, exists := assets[fields[1]]
		if !exists {
			return fields[1]
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != fields[0] {
			return fields[1]
		}
	}
	return ""
}

func TestBuildReleaseAssetsProducesExactAssetsAndChecksums(t *testing.T) {
	input := fixtureReleaseInput()
	assets, err := BuildReleaseAssets(input)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(assets))
	for name := range assets {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"checksums.txt", "index.json", "node-index-v1alpha1.schema.json"}) {
		t.Fatalf("asset names=%v", keys)
	}

	wantIndex := `{
  "apiVersion": "agent-studio.dev/v1alpha1",
  "kind": "NodePackageIndex",
  "metadata": {
    "release": "v0.1.0",
    "generatedAt": "2026-08-20T07:30:00Z",
    "sourceCommit": "0123456789abcdef0123456789abcdef01234567"
  },
  "packages": []
}
`
	if string(assets["index.json"]) != wantIndex {
		t.Fatalf("index.json=%q", assets["index.json"])
	}
	if !bytes.Equal(assets["node-index-v1alpha1.schema.json"], []byte("{\"schema\":true}\n")) {
		t.Fatalf("schema=%q", assets["node-index-v1alpha1.schema.json"])
	}
	wantChecksums := "bc8202bbf75166afc7be33e0a7942d0d04c7511f9e2dc7c53a3989c9db78f8a5  index.json\n" +
		"76125bd37f4a2719a4495bb71d446bea6f560e96647371d7d44348ef2b36afc7  node-index-v1alpha1.schema.json\n"
	if string(assets["checksums.txt"]) != wantChecksums {
		t.Fatalf("checksums.txt=%q", assets["checksums.txt"])
	}

	input.IndexSchema[0] = 'x'
	if !bytes.Equal(assets["node-index-v1alpha1.schema.json"], []byte("{\"schema\":true}\n")) {
		t.Fatal("schema asset aliases ReleaseInput")
	}
}

func fixtureReleaseInput() ReleaseInput {
	return ReleaseInput{
		GenerateInput: GenerateInput{
			Release:      "v0.1.0",
			SourceCommit: "0123456789abcdef0123456789abcdef01234567",
			GeneratedAt:  time.Date(2026, 8, 20, 7, 30, 0, 0, time.UTC),
			Submissions:  []SubmissionFile{},
		},
		IndexSchema: []byte("{\"schema\":true}\n"),
	}
}
