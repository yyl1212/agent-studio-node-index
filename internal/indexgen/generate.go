package indexgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/mod/semver"
)

type GenerateInput struct {
	Release      string
	SourceCommit string
	GeneratedAt  time.Time
	Submissions  []SubmissionFile
}

func Generate(input GenerateInput) (Index, error) {
	if !validFullSemver(input.Release) || semver.Prerelease(input.Release) != "" || semver.Build(input.Release) != "" {
		return Index{}, errors.New("release must be stable full SemVer")
	}
	if !gitOIDPattern.MatchString(input.SourceCommit) {
		return Index{}, errors.New("source commit must be a lowercase 40- or 64-hex Git OID")
	}
	generatedAt, err := canonicalTimestamp(input.GeneratedAt, "generated at")
	if err != nil {
		return Index{}, err
	}

	grouped := make(map[string]*Package)
	seenVersions := make(map[string]struct{}, len(input.Submissions))
	for _, file := range input.Submissions {
		if !gitOIDPattern.MatchString(file.IndexCommit) {
			return Index{}, fmt.Errorf("%s: index commit must be a lowercase 40- or 64-hex Git OID", file.Path)
		}
		reviewedAt, err := canonicalTimestamp(file.ReviewedAt, "reviewed at")
		if err != nil {
			return Index{}, fmt.Errorf("%s: %w", file.Path, err)
		}
		submission, err := canonicalSubmission(file.Submission)
		if err != nil {
			return Index{}, fmt.Errorf("%s: %w", file.Path, err)
		}
		key := submission.Name + "\x00" + submission.Version
		if _, exists := seenVersions[key]; exists {
			return Index{}, fmt.Errorf("duplicate package version (%s, %s)", submission.Name, submission.Version)
		}
		seenVersions[key] = struct{}{}

		pkg, exists := grouped[submission.Name]
		if !exists {
			if len(grouped) >= MaxPackages {
				return Index{}, fmt.Errorf("packages must contain at most %d values", MaxPackages)
			}
			pkg = &Package{
				Name:       submission.Name,
				Categories: slices.Clone(submission.Categories),
				Keywords:   slices.Clone(submission.Keywords),
				Versions:   []PackageVersion{},
			}
			grouped[submission.Name] = pkg
		} else {
			if !slices.Equal(pkg.Categories, submission.Categories) {
				return Index{}, fmt.Errorf("package %s has conflicting categories across versions", submission.Name)
			}
			if !slices.Equal(pkg.Keywords, submission.Keywords) {
				return Index{}, fmt.Errorf("package %s has conflicting keywords across versions", submission.Name)
			}
		}
		if len(pkg.Versions) >= MaxVersionsPerPackage {
			return Index{}, fmt.Errorf("package %s versions must contain at most %d values", submission.Name, MaxVersionsPerPackage)
		}
		pkg.Versions = append(pkg.Versions, PackageVersion{
			Version: submission.Version,
			Source:  submission.Source,
			Review: Review{
				Status:      "approved",
				ReviewedAt:  reviewedAt,
				IndexCommit: file.IndexCommit,
			},
			Lifecycle: submission.Lifecycle,
			Manifest:  submission.Manifest,
		})
	}

	packageNames := make([]string, 0, len(grouped))
	for name := range grouped {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)
	packages := make([]Package, 0, len(packageNames))
	for _, name := range packageNames {
		pkg := grouped[name]
		sort.Slice(pkg.Versions, func(i, j int) bool {
			comparison := semver.Compare(pkg.Versions[i].Version, pkg.Versions[j].Version)
			if comparison != 0 {
				return comparison < 0
			}
			return pkg.Versions[i].Version < pkg.Versions[j].Version
		})
		packages = append(packages, *pkg)
	}

	return Index{
		APIVersion: APIVersion,
		Kind:       IndexKind,
		Metadata: IndexMetadata{
			Release:      input.Release,
			GeneratedAt:  generatedAt,
			SourceCommit: input.SourceCommit,
		},
		Packages: packages,
	}, nil
}

func Encode(index Index) ([]byte, error) {
	canonical := canonicalIndexSlices(index)
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonical); err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	if len(data) > MaxIndexBytes {
		return nil, fmt.Errorf("index exceeds maximum size of %d bytes", MaxIndexBytes)
	}
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return nil, fmt.Errorf("encoded index is invalid: %w", err)
	}
	var reparsed Index
	if err := decodeOneStrict(data, &reparsed); err != nil {
		return nil, fmt.Errorf("encoded index is invalid: %w", err)
	}
	if err := validateIndex(reparsed); err != nil {
		return nil, fmt.Errorf("encoded index is invalid: %w", err)
	}
	return slices.Clone(data), nil
}

func canonicalSubmission(submission Submission) (Submission, error) {
	copy := submission
	copy.Categories = normalizeStrings(submission.Categories, true)
	copy.Keywords = normalizeStrings(submission.Keywords, true)
	for i, keyword := range copy.Keywords {
		if strings.IndexFunc(keyword, unicode.IsControl) >= 0 {
			return Submission{}, fmt.Errorf("keywords[%d] must not contain control characters", i)
		}
	}
	copy.Manifest.Registrations = cloneAndSortRegistrations(submission.Manifest.Registrations)
	if err := validateSubmission(copy); err != nil {
		return Submission{}, err
	}
	return copy, nil
}

func normalizeStrings(values []string, lowercase bool) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lowercase {
			value = strings.ToLower(value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func cloneAndSortRegistrations(values []Registration) []Registration {
	registrations := make([]Registration, len(values))
	for i, registration := range values {
		registrations[i] = registration
		registrations[i].Nodes = slices.Clone(registration.Nodes)
		sort.Slice(registrations[i].Nodes, func(a, b int) bool {
			left, right := registrations[i].Nodes[a], registrations[i].Nodes[b]
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			return left.Version < right.Version
		})
	}
	sort.Slice(registrations, func(i, j int) bool {
		return registrations[i].Package < registrations[j].Package
	})
	return registrations
}

func canonicalTimestamp(value time.Time, field string) (time.Time, error) {
	if value.IsZero() || value.Nanosecond() != 0 || value.Year() < 1 || value.Year() > 9999 {
		return time.Time{}, fmt.Errorf("%s must have valid second precision", field)
	}
	return value.UTC(), nil
}

func canonicalIndexSlices(index Index) Index {
	copy := index
	copy.Packages = make([]Package, len(index.Packages))
	for i, pkg := range index.Packages {
		copy.Packages[i] = pkg
		copy.Packages[i].Categories = nonNilClone(pkg.Categories)
		copy.Packages[i].Keywords = nonNilClone(pkg.Keywords)
		copy.Packages[i].Versions = make([]PackageVersion, len(pkg.Versions))
		for j, version := range pkg.Versions {
			copy.Packages[i].Versions[j] = version
			copy.Packages[i].Versions[j].Manifest.Registrations = cloneAndSortRegistrations(version.Manifest.Registrations)
		}
	}
	return copy
}

func nonNilClone[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return slices.Clone(values)
}

func validateIndex(index Index) error {
	if index.APIVersion != APIVersion {
		return errors.New("apiVersion is unsupported")
	}
	if index.Kind != IndexKind {
		return errors.New("kind is unsupported")
	}
	if !validFullSemver(index.Metadata.Release) || semver.Prerelease(index.Metadata.Release) != "" || semver.Build(index.Metadata.Release) != "" {
		return errors.New("metadata.release must be stable full SemVer")
	}
	if _, err := canonicalTimestamp(index.Metadata.GeneratedAt, "metadata.generatedAt"); err != nil || index.Metadata.GeneratedAt.Location() != time.UTC {
		return errors.New("metadata.generatedAt must be UTC second precision")
	}
	if !gitOIDPattern.MatchString(index.Metadata.SourceCommit) {
		return errors.New("metadata.sourceCommit must be a lowercase 40- or 64-hex Git OID")
	}
	if len(index.Packages) > MaxPackages {
		return fmt.Errorf("packages must contain at most %d values", MaxPackages)
	}
	seenPackages := make(map[string]struct{}, len(index.Packages))
	for i, pkg := range index.Packages {
		if _, exists := seenPackages[pkg.Name]; exists {
			return fmt.Errorf("duplicate package %s", pkg.Name)
		}
		seenPackages[pkg.Name] = struct{}{}
		if len(pkg.Versions) < 1 || len(pkg.Versions) > MaxVersionsPerPackage {
			return fmt.Errorf("packages[%d].versions must contain 1..%d values", i, MaxVersionsPerPackage)
		}
		if err := validateCategories(pkg.Categories); err != nil {
			return fmt.Errorf("packages[%d]: %w", i, err)
		}
		if err := validateKeywords(pkg.Keywords); err != nil {
			return fmt.Errorf("packages[%d]: %w", i, err)
		}
		seenVersions := make(map[string]struct{}, len(pkg.Versions))
		for j, version := range pkg.Versions {
			if _, exists := seenVersions[version.Version]; exists {
				return fmt.Errorf("packages[%d]: duplicate version %s", i, version.Version)
			}
			seenVersions[version.Version] = struct{}{}
			if version.Review.Status != "approved" {
				return fmt.Errorf("packages[%d].versions[%d].review.status must be approved", i, j)
			}
			if _, err := canonicalTimestamp(version.Review.ReviewedAt, "review.reviewedAt"); err != nil || version.Review.ReviewedAt.Location() != time.UTC {
				return fmt.Errorf("packages[%d].versions[%d].review.reviewedAt must be UTC second precision", i, j)
			}
			if !gitOIDPattern.MatchString(version.Review.IndexCommit) {
				return fmt.Errorf("packages[%d].versions[%d].review.indexCommit must be a lowercase 40- or 64-hex Git OID", i, j)
			}
			submission := Submission{
				APIVersion: index.APIVersion,
				Kind:       SubmissionKind,
				Name:       pkg.Name,
				Version:    version.Version,
				Source:     version.Source,
				Categories: pkg.Categories,
				Keywords:   pkg.Keywords,
				Lifecycle:  version.Lifecycle,
				Manifest:   version.Manifest,
			}
			if err := validateSubmission(submission); err != nil {
				return fmt.Errorf("packages[%d].versions[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}
