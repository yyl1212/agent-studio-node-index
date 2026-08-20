package indexgen

import (
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/mod/semver"
)

const validSubmissionJSON = `{
  "apiVersion": "agent-studio.dev/v1alpha1",
  "kind": "NodePackageSubmission",
  "name": "github.com/example/agent-nodes",
  "version": "v1.2.3",
  "source": {
    "repository": "https://github.com/example/agent-nodes",
    "moduleDir": ".",
    "tag": "v1.2.3",
    "commit": "0123456789abcdef0123456789abcdef01234567",
    "manifestDigest": "sha256:89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef89abcdef"
  },
  "categories": ["integration", "search"],
  "keywords": ["http", "搜索"],
  "lifecycle": {"status": "active", "message": ""},
  "manifest": {
    "apiVersion": "agent-studio.dev/v1alpha1",
    "kind": "NodePackage",
    "metadata": {
      "name": "github.com/example/agent-nodes",
      "displayName": "Example Agent Nodes",
      "description": "Example search integration nodes",
      "license": "Apache-2.0",
      "repository": "https://github.com/example/agent-nodes"
    },
    "compatibility": {
      "nodeAPI": "agent-studio.dev/v1alpha1",
      "runtime": {"minVersion": "v0.3.0", "maxVersionExclusive": "v0.4.0"}
    },
    "registrations": [{
      "package": "github.com/example/agent-nodes/search",
      "nodes": [{"type": "example.search", "version": "1"}]
    }]
  }
}`

func TestSubmissionSchemaAcceptsCanonicalSubmission(t *testing.T) {
	sch := compileContractSchema(t, "../../schema/submission.schema.json")
	if err := sch.Validate(decodeJSON(t, validSubmissionJSON)); err != nil {
		t.Fatalf("valid submission rejected: %v", err)
	}
}

func TestSubmissionSchemaRejectsContractViolations(t *testing.T) {
	sch := compileContractSchema(t, "../../schema/submission.schema.json")
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown top-level field", func(v map[string]any) { v["unexpected"] = true }},
		{"non-canonical repository", func(v map[string]any) {
			object(v, "source")["repository"] = "https://github.com/example/agent-nodes?ref=main"
		}},
		{"uppercase git oid", func(v map[string]any) { object(v, "source")["commit"] = "0123456789ABCDEF0123456789ABCDEF01234567" }},
		{"unprefixed manifest digest", func(v map[string]any) { object(v, "source")["manifestDigest"] = strings.Repeat("a", 64) }},
		{"too many categories", func(v map[string]any) { v["categories"] = repeated("integration", 9) }},
		{"non-slug category", func(v map[string]any) { v["categories"] = []any{"Search"} }},
		{"keyword over 64 code points", func(v map[string]any) { v["keywords"] = []any{strings.Repeat("界", 65)} }},
		{"active lifecycle message", func(v map[string]any) { object(v, "lifecycle")["message"] = "not empty" }},
		{"deprecated lifecycle without message", func(v map[string]any) { object(v, "lifecycle")["status"] = "deprecated" }},
		{"lifecycle html", func(v map[string]any) {
			object(v, "lifecycle")["status"] = "withdrawn"
			object(v, "lifecycle")["message"] = "<b>unsafe</b>"
		}},
		{"unknown manifest field", func(v map[string]any) { object(v, "manifest")["unexpected"] = true }},
		{"too many registrations", func(v map[string]any) {
			object(v, "manifest")["registrations"] = repeated(map[string]any{"package": "example", "nodes": []any{map[string]any{"type": "example", "version": "1"}}}, 129)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := decodeJSONObject(t, validSubmissionJSON)
			tt.mutate(value)
			if err := sch.Validate(value); err == nil {
				t.Fatal("invalid submission accepted")
			}
		})
	}
}

func TestIndexSchemaAcceptsCanonicalIndex(t *testing.T) {
	sch := compileContractSchema(t, "../../schema/node-index-v1alpha1.schema.json")
	if err := sch.Validate(validIndex(t)); err != nil {
		t.Fatalf("valid index rejected: %v", err)
	}
}

func TestIndexSchemaRejectsContractViolations(t *testing.T) {
	sch := compileContractSchema(t, "../../schema/node-index-v1alpha1.schema.json")
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"prerelease release", func(v map[string]any) { object(v, "metadata")["release"] = "v1.2.3-rc.1" }},
		{"invalid generated time", func(v map[string]any) { object(v, "metadata")["generatedAt"] = "2026-08-20" }},
		{"uppercase source commit", func(v map[string]any) {
			object(v, "metadata")["sourceCommit"] = "0123456789ABCDEF0123456789ABCDEF01234567"
		}},
		{"too many packages", func(v map[string]any) { v["packages"] = repeated(firstPackage(v), 1001) }},
		{"empty versions", func(v map[string]any) { firstPackage(v)["versions"] = []any{} }},
		{"too many versions", func(v map[string]any) { firstPackage(v)["versions"] = repeated(firstVersion(v), 21) }},
		{"unapproved review", func(v map[string]any) { object(firstVersion(v), "review")["status"] = "pending" }},
		{"invalid review time", func(v map[string]any) { object(firstVersion(v), "review")["reviewedAt"] = "yesterday" }},
		{"unknown version field", func(v map[string]any) { firstVersion(v)["unexpected"] = true }},
		{"too many nodes", func(v map[string]any) {
			firstRegistration(v)["nodes"] = repeated(map[string]any{"type": "example.search", "version": "1"}, 513)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := validIndex(t)
			tt.mutate(value)
			if err := sch.Validate(value); err == nil {
				t.Fatal("invalid index accepted")
			}
		})
	}
}

func TestSchemasRejectInvalidGoSemVer(t *testing.T) {
	const invalidVersion = "v1.2.3-01"
	if semver.IsValid(invalidVersion) {
		t.Fatalf("test fixture %q unexpectedly is valid Go SemVer", invalidVersion)
	}

	submission := decodeJSONObject(t, validSubmissionJSON)
	submission["version"] = invalidVersion
	if err := compileContractSchema(t, "../../schema/submission.schema.json").Validate(submission); err == nil {
		t.Fatal("submission schema accepted invalid Go SemVer")
	}

	index := validIndex(t)
	firstVersion(index)["version"] = invalidVersion
	if err := compileContractSchema(t, "../../schema/node-index-v1alpha1.schema.json").Validate(index); err == nil {
		t.Fatal("index schema accepted invalid Go SemVer")
	}
}

func TestSchemasAcceptGoSemVerShorthand(t *testing.T) {
	for _, version := range []string{"v1", "v1.2"} {
		t.Run(version, func(t *testing.T) {
			if !semver.IsValid(version) {
				t.Fatalf("test fixture %q unexpectedly is not valid Go SemVer", version)
			}

			submission := decodeJSONObject(t, validSubmissionJSON)
			submission["version"] = version
			if err := compileContractSchema(t, "../../schema/submission.schema.json").Validate(submission); err != nil {
				t.Fatalf("submission schema rejected valid Go SemVer %q: %v", version, err)
			}

			index := validIndex(t)
			firstVersion(index)["version"] = version
			if err := compileContractSchema(t, "../../schema/node-index-v1alpha1.schema.json").Validate(index); err != nil {
				t.Fatalf("index schema rejected valid Go SemVer %q: %v", version, err)
			}
		})
	}
}

func TestIndexSchemaRequiresUTCSecondPrecision(t *testing.T) {
	sch := compileContractSchema(t, "../../schema/node-index-v1alpha1.schema.json")
	if err := sch.Validate(validIndex(t)); err != nil {
		t.Fatalf("canonical UTC second-precision timestamps rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"generatedAt offset", func(v map[string]any) { object(v, "metadata")["generatedAt"] = "2026-08-20T15:30:00+08:00" }},
		{"generatedAt fractional seconds", func(v map[string]any) { object(v, "metadata")["generatedAt"] = "2026-08-20T07:30:00.123Z" }},
		{"reviewedAt offset", func(v map[string]any) { object(firstVersion(v), "review")["reviewedAt"] = "2026-08-20T15:30:00+08:00" }},
		{"reviewedAt fractional seconds", func(v map[string]any) { object(firstVersion(v), "review")["reviewedAt"] = "2026-08-20T07:30:00.123Z" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := validIndex(t)
			tt.mutate(value)
			if err := sch.Validate(value); err == nil {
				t.Fatal("non-canonical timestamp accepted")
			}
		})
	}
}

func TestIndexSchemaReferencesComplexObjectDefinitions(t *testing.T) {
	document := schemaDocument(t, "../../schema/node-index-v1alpha1.schema.json")
	properties := object(document, "properties")
	if got := object(properties, "metadata")["$ref"]; got != "#/$defs/indexMetadata" {
		t.Fatalf("metadata $ref=%v", got)
	}
	packages := object(properties, "packages")
	if got := object(packages, "items")["$ref"]; got != "#/$defs/package" {
		t.Fatalf("package items $ref=%v", got)
	}
	definitions := object(document, "$defs")
	for _, name := range []string{"indexMetadata", "package"} {
		if _, ok := definitions[name].(map[string]any); !ok {
			t.Fatalf("missing object definition %q", name)
		}
	}
}

func compileContractSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	sch, err := compiler.Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func decodeJSON(t *testing.T, raw string) any {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func decodeJSONObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	return decodeJSON(t, raw).(map[string]any)
}

func schemaDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decodeJSONObject(t, string(raw))
}

func repeated(value any, count int) []any {
	values := make([]any, count)
	for i := range values {
		values[i] = value
	}
	return values
}

func object(value map[string]any, key string) map[string]any {
	return value[key].(map[string]any)
}

func validIndex(t *testing.T) map[string]any {
	t.Helper()
	submission := decodeJSONObject(t, validSubmissionJSON)
	return map[string]any{
		"apiVersion": "agent-studio.dev/v1alpha1",
		"kind":       "NodePackageIndex",
		"metadata": map[string]any{
			"release":      "v1.2.3",
			"generatedAt":  "2026-08-20T07:30:00Z",
			"sourceCommit": "0123456789abcdef0123456789abcdef01234567",
		},
		"packages": []any{map[string]any{
			"name":       submission["name"],
			"categories": submission["categories"],
			"keywords":   submission["keywords"],
			"versions": []any{map[string]any{
				"version": submission["version"],
				"source":  submission["source"],
				"review": map[string]any{
					"status":      "approved",
					"reviewedAt":  "2026-08-20T07:30:00Z",
					"indexCommit": "89abcdef0123456789abcdef0123456789abcdef",
				},
				"lifecycle": submission["lifecycle"],
				"manifest":  submission["manifest"],
			}},
		}},
	}
}

func firstPackage(index map[string]any) map[string]any {
	return index["packages"].([]any)[0].(map[string]any)
}

func firstVersion(index map[string]any) map[string]any {
	return firstPackage(index)["versions"].([]any)[0].(map[string]any)
}

func firstRegistration(index map[string]any) map[string]any {
	manifest := object(firstVersion(index), "manifest")
	return manifest["registrations"].([]any)[0].(map[string]any)
}
