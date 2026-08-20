package indexgen

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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
}
