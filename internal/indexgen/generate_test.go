package indexgen

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGenerateProducesCanonicalDeterministicIndex(t *testing.T) {
	input := fixtureGenerateInput()

	first, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := Encode(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := Encode(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("generation is not byte deterministic")
	}

	want := `{
  "apiVersion": "agent-studio.dev/v1alpha1",
  "kind": "NodePackageIndex",
  "metadata": {
    "release": "v0.1.0",
    "generatedAt": "2026-08-20T07:30:00Z",
    "sourceCommit": "0123456789abcdef0123456789abcdef01234567"
  },
  "packages": [
    {
      "name": "github.com/example/a-nodes",
      "categories": [
        "integration",
        "search"
      ],
      "keywords": [
        "alpha",
        "beta"
      ],
      "versions": [
        {
          "version": "v1.9.0",
          "source": {
            "repository": "https://github.com/example/a-nodes",
            "moduleDir": ".",
            "tag": "v1.9.0",
            "commit": "1111111111111111111111111111111111111111",
            "manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
          },
          "review": {
            "status": "approved",
            "reviewedAt": "2026-08-19T23:20:00Z",
            "indexCommit": "2222222222222222222222222222222222222222"
          },
          "lifecycle": {
            "status": "active",
            "message": ""
          },
          "manifest": {
            "apiVersion": "agent-studio.dev/v1alpha1",
            "kind": "NodePackage",
            "metadata": {
              "name": "github.com/example/a-nodes",
              "displayName": "a-nodes",
              "description": "uses <search>",
              "license": "Apache-2.0",
              "repository": "https://github.com/example/a-nodes"
            },
            "compatibility": {
              "nodeAPI": "agent-studio.dev/v1alpha1",
              "runtime": {
                "minVersion": "v0.3.0",
                "maxVersionExclusive": "v0.4.0"
              }
            },
            "registrations": [
              {
                "package": "github.com/example/a-nodes/alpha",
                "nodes": [
                  {
                    "type": "a.node",
                    "version": "2"
                  },
                  {
                    "type": "b.node",
                    "version": "1"
                  }
                ]
              },
              {
                "package": "github.com/example/a-nodes/zeta",
                "nodes": [
                  {
                    "type": "z.node",
                    "version": "1"
                  }
                ]
              }
            ]
          }
        },
        {
          "version": "v1.10.0",
          "source": {
            "repository": "https://github.com/example/a-nodes",
            "moduleDir": ".",
            "tag": "v1.10.0",
            "commit": "1111111111111111111111111111111111111111",
            "manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
          },
          "review": {
            "status": "approved",
            "reviewedAt": "2026-08-19T23:20:00Z",
            "indexCommit": "2222222222222222222222222222222222222222"
          },
          "lifecycle": {
            "status": "active",
            "message": ""
          },
          "manifest": {
            "apiVersion": "agent-studio.dev/v1alpha1",
            "kind": "NodePackage",
            "metadata": {
              "name": "github.com/example/a-nodes",
              "displayName": "a-nodes",
              "description": "uses <search>",
              "license": "Apache-2.0",
              "repository": "https://github.com/example/a-nodes"
            },
            "compatibility": {
              "nodeAPI": "agent-studio.dev/v1alpha1",
              "runtime": {
                "minVersion": "v0.3.0",
                "maxVersionExclusive": "v0.4.0"
              }
            },
            "registrations": [
              {
                "package": "github.com/example/a-nodes/alpha",
                "nodes": [
                  {
                    "type": "a.node",
                    "version": "2"
                  },
                  {
                    "type": "b.node",
                    "version": "1"
                  }
                ]
              },
              {
                "package": "github.com/example/a-nodes/zeta",
                "nodes": [
                  {
                    "type": "z.node",
                    "version": "1"
                  }
                ]
              }
            ]
          }
        }
      ]
    },
    {
      "name": "github.com/example/z-nodes",
      "categories": [],
      "keywords": [],
      "versions": [
        {
          "version": "v1.0.0",
          "source": {
            "repository": "https://github.com/example/z-nodes",
            "moduleDir": ".",
            "tag": "v1.0.0",
            "commit": "1111111111111111111111111111111111111111",
            "manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
          },
          "review": {
            "status": "approved",
            "reviewedAt": "2026-08-19T23:20:00Z",
            "indexCommit": "2222222222222222222222222222222222222222"
          },
          "lifecycle": {
            "status": "active",
            "message": ""
          },
          "manifest": {
            "apiVersion": "agent-studio.dev/v1alpha1",
            "kind": "NodePackage",
            "metadata": {
              "name": "github.com/example/z-nodes",
              "displayName": "z-nodes",
              "description": "uses <search>",
              "license": "Apache-2.0",
              "repository": "https://github.com/example/z-nodes"
            },
            "compatibility": {
              "nodeAPI": "agent-studio.dev/v1alpha1",
              "runtime": {
                "minVersion": "v0.3.0",
                "maxVersionExclusive": "v0.4.0"
              }
            },
            "registrations": [
              {
                "package": "github.com/example/z-nodes/alpha",
                "nodes": [
                  {
                    "type": "a.node",
                    "version": "2"
                  },
                  {
                    "type": "b.node",
                    "version": "1"
                  }
                ]
              },
              {
                "package": "github.com/example/z-nodes/zeta",
                "nodes": [
                  {
                    "type": "z.node",
                    "version": "1"
                  }
                ]
              }
            ]
          }
        }
      ]
    }
  ]
}
`
	if string(firstJSON) != want {
		t.Fatalf("encoded index mismatch\ngot:\n%s\nwant:\n%s", firstJSON, want)
	}
}

func TestGenerateDeepCopiesEverySlice(t *testing.T) {
	input := fixtureGenerateInput()
	got, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}

	input.Submissions[0].Submission.Categories[0] = "changed"
	input.Submissions[0].Submission.Keywords[0] = "changed"
	input.Submissions[0].Submission.Manifest.Registrations[0].Package = "changed"
	input.Submissions[0].Submission.Manifest.Registrations[0].Nodes[0].Type = "changed"

	if !slices.Equal(got.Packages[0].Categories, []string{"integration", "search"}) {
		t.Fatalf("categories changed through input alias: %v", got.Packages[0].Categories)
	}
	if !slices.Equal(got.Packages[0].Keywords, []string{"alpha", "beta"}) {
		t.Fatalf("keywords changed through input alias: %v", got.Packages[0].Keywords)
	}
	registration := got.Packages[0].Versions[0].Manifest.Registrations[0]
	if registration.Package != "github.com/example/a-nodes/alpha" || registration.Nodes[0].Type != "a.node" {
		t.Fatalf("manifest changed through input alias: %+v", registration)
	}
}

func TestGenerateRejectsConflictingCuratedMetadata(t *testing.T) {
	for _, field := range []string{"categories", "keywords"} {
		t.Run(field, func(t *testing.T) {
			input := fixtureGenerateInput()
			if field == "categories" {
				input.Submissions[1].Submission.Categories = []string{"utility"}
			} else {
				input.Submissions[1].Submission.Keywords = []string{"different"}
			}
			_, err := Generate(input)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGenerateRejectsSemanticallyEquivalentPackageVersions(t *testing.T) {
	for _, versions := range [][2]string{
		{"v1", "v1.0"},
		{"v1.0", "v1.0.0"},
		{"v1.2.3+first", "v1.2.3+second"},
	} {
		t.Run(versions[0]+" and "+versions[1], func(t *testing.T) {
			input := fixtureGenerateInput()
			input.Submissions = []SubmissionFile{
				fixtureSubmissionFile(fixtureSubmission("github.com/example/a-nodes", versions[0])),
				fixtureSubmissionFile(fixtureSubmission("github.com/example/a-nodes", versions[1])),
			}
			_, err := Generate(input)
			if err == nil || !strings.Contains(err.Error(), "duplicate package version") {
				t.Fatalf("err=%v", err)
			}
		})
	}

	input := fixtureGenerateInput()
	input.Submissions = []SubmissionFile{
		fixtureSubmissionFile(fixtureSubmission("github.com/example/a-nodes", "v1.2.4")),
		fixtureSubmissionFile(fixtureSubmission("github.com/example/a-nodes", "v1.2.3")),
	}
	index, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	gotVersions := []string{index.Packages[0].Versions[0].Version, index.Packages[0].Versions[1].Version}
	if !slices.Equal(gotVersions, []string{"v1.2.3", "v1.2.4"}) {
		t.Fatalf("versions=%v", gotVersions)
	}

	index.Packages[0].Versions[1].Version = "v1.2.3+build"
	if _, err := Encode(index); err == nil || !strings.Contains(err.Error(), "duplicate version") {
		t.Fatalf("Encode err=%v", err)
	}
}

func TestGenerateRejectsInvalidEnvelopeAndBudgets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GenerateInput)
		want   string
	}{
		{"shorthand release", func(v *GenerateInput) { v.Release = "v1.2" }, "release"},
		{"prerelease", func(v *GenerateInput) { v.Release = "v1.2.3-rc.1" }, "release"},
		{"build release", func(v *GenerateInput) { v.Release = "v1.2.3+build" }, "release"},
		{"source commit", func(v *GenerateInput) { v.SourceCommit = strings.Repeat("A", 40) }, "source commit"},
		{"generated fraction", func(v *GenerateInput) { v.GeneratedAt = time.Date(2026, 8, 20, 7, 30, 0, 1, time.UTC) }, "generated at"},
		{"generated year zero", func(v *GenerateInput) { v.GeneratedAt = time.Date(0, 8, 20, 7, 30, 0, 0, time.UTC) }, "generated at"},
		{"reviewed fraction", func(v *GenerateInput) { v.Submissions[0].ReviewedAt = time.Date(2026, 8, 20, 7, 30, 0, 1, time.UTC) }, "reviewed at"},
		{"index commit", func(v *GenerateInput) { v.Submissions[0].IndexCommit = "bad" }, "index commit"},
		{"duplicate version", func(v *GenerateInput) { v.Submissions = append(v.Submissions, v.Submissions[0]) }, "duplicate"},
		{"too many versions", func(v *GenerateInput) {
			v.Submissions = v.Submissions[:0]
			for i := range MaxVersionsPerPackage + 1 {
				submission := fixtureSubmission("github.com/example/a-nodes", fmt.Sprintf("v1.0.%d", i))
				v.Submissions = append(v.Submissions, fixtureSubmissionFile(submission))
			}
		}, "versions"},
		{"too many packages", func(v *GenerateInput) {
			v.Submissions = v.Submissions[:0]
			for i := range MaxPackages + 1 {
				name := fmt.Sprintf("github.com/example/nodes-%04d", i)
				v.Submissions = append(v.Submissions, fixtureSubmissionFile(fixtureSubmission(name, "v1.0.0")))
			}
		}, "packages"},
		{"registration budget", func(v *GenerateInput) {
			registration := v.Submissions[0].Submission.Manifest.Registrations[0]
			v.Submissions[0].Submission.Manifest.Registrations = make([]Registration, 129)
			for i := range v.Submissions[0].Submission.Manifest.Registrations {
				registration.Package = fmt.Sprintf("github.com/example/a-nodes/p%d", i)
				registration.Nodes = []NodeRef{{Type: fmt.Sprintf("node.%d", i), Version: "1"}}
				v.Submissions[0].Submission.Manifest.Registrations[i] = registration
			}
		}, "registrations"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := fixtureGenerateInput()
			test.mutate(&input)
			_, err := Generate(input)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("err=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestEncodeRejectsInvalidOrOversizedIndex(t *testing.T) {
	t.Run("unapproved review", func(t *testing.T) {
		index, err := Generate(fixtureGenerateInput())
		if err != nil {
			t.Fatal(err)
		}
		index.Packages[0].Versions[0].Review.Status = "pending"
		if _, err := Encode(index); err == nil || !strings.Contains(err.Error(), "approved") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("maximum bytes", func(t *testing.T) {
		input := fixtureGenerateInput()
		input.Submissions = input.Submissions[:0]
		for i := range MaxPackages {
			name := fmt.Sprintf("github.com/example/nodes-%04d", i)
			submission := fixtureSubmission(name, "v1.0.0")
			submission.Manifest.Metadata.Description = strings.Repeat("d", 2048)
			submission.Lifecycle = Lifecycle{Status: "deprecated", Message: strings.Repeat("m", 2048)}
			input.Submissions = append(input.Submissions, fixtureSubmissionFile(submission))
		}
		index, err := Generate(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Encode(index); err == nil || !strings.Contains(err.Error(), "maximum size") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestEncodeRejectsStructuralBudgetsBeforeLargeScaleAllocation(t *testing.T) {
	base, err := Generate(fixtureGenerateInput())
	if err != nil {
		t.Fatal(err)
	}

	overPackages := base
	overPackages.Packages = make([]Package, MaxPackages+1)
	var packageErr error
	allocations := testing.AllocsPerRun(3, func() {
		_, packageErr = Encode(overPackages)
	})
	if packageErr == nil || !strings.Contains(packageErr.Error(), "packages") {
		t.Fatalf("package err=%v", packageErr)
	}
	if allocations > 20 {
		t.Fatalf("over-budget package input allocated %.0f times before rejection", allocations)
	}

	tests := []struct {
		name   string
		mutate func(*Index)
		want   string
	}{
		{"versions", func(index *Index) {
			index.Packages[0].Versions = make([]PackageVersion, MaxVersionsPerPackage+1)
		}, "versions"},
		{"registrations", func(index *Index) {
			index.Packages[0].Versions[0].Manifest.Registrations = make([]Registration, 129)
		}, "registrations"},
		{"nodes", func(index *Index) {
			index.Packages[0].Versions[0].Manifest.Registrations[0].Nodes = make([]NodeRef, 513)
		}, "nodes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := base
			index.Packages = slices.Clone(base.Packages)
			index.Packages[0].Versions = slices.Clone(base.Packages[0].Versions)
			index.Packages[0].Versions[0].Manifest.Registrations = slices.Clone(base.Packages[0].Versions[0].Manifest.Registrations)
			test.mutate(&index)
			_, err := Encode(index)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func fixtureGenerateInput() GenerateInput {
	aOld := fixtureSubmission("github.com/example/a-nodes", "v1.9.0")
	aOld.Categories = []string{"search", "integration"}
	aOld.Keywords = []string{" Beta ", "ALPHA", "alpha"}
	aNew := fixtureSubmission("github.com/example/a-nodes", "v1.10.0")
	aNew.Categories = []string{"integration", "search"}
	aNew.Keywords = []string{"beta", " alpha "}
	z := fixtureSubmission("github.com/example/z-nodes", "v1.0.0")
	z.Categories = nil
	z.Keywords = nil
	return GenerateInput{
		Release:      "v0.1.0",
		SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		GeneratedAt:  time.Date(2026, 8, 20, 15, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
		Submissions: []SubmissionFile{
			fixtureSubmissionFile(aNew),
			fixtureSubmissionFile(aOld),
			fixtureSubmissionFile(z),
		},
	}
}

func fixtureSubmission(name, version string) Submission {
	repository := "https://" + strings.Join(strings.Split(name, "/")[:3], "/")
	displayName := strings.TrimPrefix(name, "github.com/example/")
	return Submission{
		APIVersion: APIVersion,
		Kind:       SubmissionKind,
		Name:       name,
		Version:    version,
		Source: Source{
			Repository:     repository,
			ModuleDir:      ".",
			Tag:            version,
			Commit:         strings.Repeat("1", 40),
			ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		},
		Categories: []string{},
		Keywords:   []string{},
		Lifecycle:  Lifecycle{Status: "active", Message: ""},
		Manifest: NodePackageManifest{
			APIVersion: APIVersion,
			Kind:       "NodePackage",
			Metadata: NodePackageMetadata{
				Name:        name,
				DisplayName: displayName,
				Description: "uses <search>",
				License:     "Apache-2.0",
				Repository:  repository,
			},
			Compatibility: Compatibility{
				NodeAPI: APIVersion,
				Runtime: RuntimeRange{MinVersion: "v0.3.0", MaxVersionExclusive: "v0.4.0"},
			},
			Registrations: []Registration{
				{Package: name + "/zeta", Nodes: []NodeRef{{Type: "z.node", Version: "1"}}},
				{Package: name + "/alpha", Nodes: []NodeRef{{Type: "b.node", Version: "1"}, {Type: "a.node", Version: "2"}}},
			},
		},
	}
}

func fixtureSubmissionFile(submission Submission) SubmissionFile {
	return SubmissionFile{
		Path:        "packages/fixture.json",
		Submission:  submission,
		IndexCommit: strings.Repeat("2", 40),
		ReviewedAt:  time.Date(2026, 8, 20, 7, 20, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
	}
}
