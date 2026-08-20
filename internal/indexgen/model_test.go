package indexgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestContractConstantsAndNodePackageSource(t *testing.T) {
	if APIVersion != "agent-studio.dev/v1alpha1" || SubmissionKind != "NodePackageSubmission" || IndexKind != "NodePackageIndex" {
		t.Fatalf("contract constants changed")
	}
	raw, err := os.ReadFile("../../schema/node-package-v1alpha1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != "e09f444bfd1b3bfde2ddc5a47cea72bd556287c5ee7363e30389a85b74ca4107" {
		t.Fatalf("schema digest=%s", got)
	}

	sourceRaw, err := os.ReadFile("../../schema/node-package-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]string
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal(err)
	}
	wantSource := map[string]string{
		"repository": "https://github.com/yyl1212/agent-studio",
		"ref":        "835aeebd543d7447527d2eb5ebe296e77f110081",
		"path":       "contracts/node-package.schema.json",
		"sha256":     "e09f444bfd1b3bfde2ddc5a47cea72bd556287c5ee7363e30389a85b74ca4107",
	}
	if !reflect.DeepEqual(source, wantSource) {
		t.Fatalf("node package source=%v", source)
	}
}
