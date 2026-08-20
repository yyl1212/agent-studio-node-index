package indexgen

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

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
