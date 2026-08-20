package indexgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
)

const IndexSchemaAssetName = "node-index-v1alpha1.schema.json"

type ReleaseInput struct {
	GenerateInput GenerateInput
	IndexSchema   []byte
}

func BuildReleaseAssets(input ReleaseInput) (map[string][]byte, error) {
	index, err := Generate(input.GenerateInput)
	if err != nil {
		return nil, err
	}
	indexJSON, err := Encode(index)
	if err != nil {
		return nil, err
	}
	assets := map[string][]byte{
		"index.json":         indexJSON,
		IndexSchemaAssetName: slices.Clone(input.IndexSchema),
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	var checksums bytes.Buffer
	for _, name := range names {
		sum := sha256.Sum256(assets[name])
		digest := hex.EncodeToString(sum[:])
		if _, err := fmt.Fprintf(&checksums, "%s  %s\n", digest, name); err != nil {
			return nil, err
		}
	}
	assets["checksums.txt"] = slices.Clone(checksums.Bytes())
	return assets, nil
}
