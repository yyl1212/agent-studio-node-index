package indexgen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type submissionWire struct {
	APIVersion *string        `json:"apiVersion"`
	Kind       *string        `json:"kind"`
	Name       *string        `json:"name"`
	Version    *string        `json:"version"`
	Source     *sourceWire    `json:"source"`
	Categories *[]string      `json:"categories"`
	Keywords   *[]string      `json:"keywords"`
	Lifecycle  *lifecycleWire `json:"lifecycle"`
	Manifest   *manifestWire  `json:"manifest"`
}

type sourceWire struct {
	Repository     *string `json:"repository"`
	ModuleDir      *string `json:"moduleDir"`
	Tag            *string `json:"tag"`
	Commit         *string `json:"commit"`
	ManifestDigest *string `json:"manifestDigest"`
}

type lifecycleWire struct {
	Status  *string `json:"status"`
	Message *string `json:"message"`
}

type manifestWire struct {
	APIVersion    *string             `json:"apiVersion"`
	Kind          *string             `json:"kind"`
	Metadata      *metadataWire       `json:"metadata"`
	Compatibility *compatibilityWire  `json:"compatibility"`
	Registrations *[]registrationWire `json:"registrations"`
}

type metadataWire struct {
	Name        *string `json:"name"`
	DisplayName *string `json:"displayName"`
	Description *string `json:"description"`
	License     *string `json:"license"`
	Repository  *string `json:"repository"`
}

type compatibilityWire struct {
	NodeAPI *string      `json:"nodeAPI"`
	Runtime *runtimeWire `json:"runtime"`
}

type runtimeWire struct {
	MinVersion          *string `json:"minVersion"`
	MaxVersionExclusive *string `json:"maxVersionExclusive"`
}

type registrationWire struct {
	Package *string     `json:"package"`
	Nodes   *[]nodeWire `json:"nodes"`
}

type nodeWire struct {
	Type    *string `json:"type"`
	Version *string `json:"version"`
}

func (w submissionWire) value() (Submission, error) {
	if w.APIVersion == nil {
		return Submission{}, errors.New("apiVersion is required")
	}
	if w.Kind == nil {
		return Submission{}, errors.New("kind is required")
	}
	if w.Name == nil {
		return Submission{}, errors.New("name is required")
	}
	if w.Version == nil {
		return Submission{}, errors.New("version is required")
	}
	if w.Source == nil {
		return Submission{}, errors.New("source is required")
	}
	source, err := w.Source.value("source")
	if err != nil {
		return Submission{}, err
	}
	if w.Categories == nil {
		return Submission{}, errors.New("categories is required")
	}
	if w.Keywords == nil {
		return Submission{}, errors.New("keywords is required")
	}
	if w.Lifecycle == nil {
		return Submission{}, errors.New("lifecycle is required")
	}
	lifecycle, err := w.Lifecycle.value("lifecycle")
	if err != nil {
		return Submission{}, err
	}
	if w.Manifest == nil {
		return Submission{}, errors.New("manifest is required")
	}
	manifest, err := w.Manifest.value("manifest")
	if err != nil {
		return Submission{}, err
	}
	return Submission{
		APIVersion: *w.APIVersion,
		Kind:       *w.Kind,
		Name:       *w.Name,
		Version:    *w.Version,
		Source:     source,
		Categories: append([]string(nil), (*w.Categories)...),
		Keywords:   append([]string(nil), (*w.Keywords)...),
		Lifecycle:  lifecycle,
		Manifest:   manifest,
	}, nil
}

func (w sourceWire) value(field string) (Source, error) {
	if w.Repository == nil {
		return Source{}, requiredField(field + ".repository")
	}
	if w.ModuleDir == nil {
		return Source{}, requiredField(field + ".moduleDir")
	}
	if w.Tag == nil {
		return Source{}, requiredField(field + ".tag")
	}
	if w.Commit == nil {
		return Source{}, requiredField(field + ".commit")
	}
	if w.ManifestDigest == nil {
		return Source{}, requiredField(field + ".manifestDigest")
	}
	return Source{*w.Repository, *w.ModuleDir, *w.Tag, *w.Commit, *w.ManifestDigest}, nil
}

func (w lifecycleWire) value(field string) (Lifecycle, error) {
	if w.Status == nil {
		return Lifecycle{}, requiredField(field + ".status")
	}
	if w.Message == nil {
		return Lifecycle{}, requiredField(field + ".message")
	}
	return Lifecycle{Status: *w.Status, Message: *w.Message}, nil
}

func (w manifestWire) value(field string) (NodePackageManifest, error) {
	if w.APIVersion == nil {
		return NodePackageManifest{}, requiredField(field + ".apiVersion")
	}
	if w.Kind == nil {
		return NodePackageManifest{}, requiredField(field + ".kind")
	}
	if w.Metadata == nil {
		return NodePackageManifest{}, requiredField(field + ".metadata")
	}
	metadata, err := w.Metadata.value(field + ".metadata")
	if err != nil {
		return NodePackageManifest{}, err
	}
	if w.Compatibility == nil {
		return NodePackageManifest{}, requiredField(field + ".compatibility")
	}
	compatibility, err := w.Compatibility.value(field + ".compatibility")
	if err != nil {
		return NodePackageManifest{}, err
	}
	if w.Registrations == nil {
		return NodePackageManifest{}, requiredField(field + ".registrations")
	}
	registrations := make([]Registration, len(*w.Registrations))
	for i, registration := range *w.Registrations {
		registrations[i], err = registration.value(fmt.Sprintf("%s.registrations[%d]", field, i))
		if err != nil {
			return NodePackageManifest{}, err
		}
	}
	return NodePackageManifest{
		APIVersion:    *w.APIVersion,
		Kind:          *w.Kind,
		Metadata:      metadata,
		Compatibility: compatibility,
		Registrations: registrations,
	}, nil
}

func (w metadataWire) value(field string) (NodePackageMetadata, error) {
	if w.Name == nil {
		return NodePackageMetadata{}, requiredField(field + ".name")
	}
	if w.DisplayName == nil {
		return NodePackageMetadata{}, requiredField(field + ".displayName")
	}
	if w.Description == nil {
		return NodePackageMetadata{}, requiredField(field + ".description")
	}
	if w.License == nil {
		return NodePackageMetadata{}, requiredField(field + ".license")
	}
	if w.Repository == nil {
		return NodePackageMetadata{}, requiredField(field + ".repository")
	}
	return NodePackageMetadata{*w.Name, *w.DisplayName, *w.Description, *w.License, *w.Repository}, nil
}

func (w compatibilityWire) value(field string) (Compatibility, error) {
	if w.NodeAPI == nil {
		return Compatibility{}, requiredField(field + ".nodeAPI")
	}
	if w.Runtime == nil {
		return Compatibility{}, requiredField(field + ".runtime")
	}
	runtimeRange, err := w.Runtime.value(field + ".runtime")
	if err != nil {
		return Compatibility{}, err
	}
	return Compatibility{NodeAPI: *w.NodeAPI, Runtime: runtimeRange}, nil
}

func (w runtimeWire) value(field string) (RuntimeRange, error) {
	if w.MinVersion == nil {
		return RuntimeRange{}, requiredField(field + ".minVersion")
	}
	if w.MaxVersionExclusive == nil {
		return RuntimeRange{}, requiredField(field + ".maxVersionExclusive")
	}
	return RuntimeRange{MinVersion: *w.MinVersion, MaxVersionExclusive: *w.MaxVersionExclusive}, nil
}

func (w registrationWire) value(field string) (Registration, error) {
	if w.Package == nil {
		return Registration{}, requiredField(field + ".package")
	}
	if w.Nodes == nil {
		return Registration{}, requiredField(field + ".nodes")
	}
	nodes := make([]NodeRef, len(*w.Nodes))
	for i, node := range *w.Nodes {
		value, err := node.value(fmt.Sprintf("%s.nodes[%d]", field, i))
		if err != nil {
			return Registration{}, err
		}
		nodes[i] = value
	}
	return Registration{Package: *w.Package, Nodes: nodes}, nil
}

func (w nodeWire) value(field string) (NodeRef, error) {
	if w.Type == nil {
		return NodeRef{}, requiredField(field + ".type")
	}
	if w.Version == nil {
		return NodeRef{}, requiredField(field + ".version")
	}
	return NodeRef{Type: *w.Type, Version: *w.Version}, nil
}

func requiredField(field string) error {
	return fmt.Errorf("%s is required", field)
}

func ParseSubmission(source string, data []byte) (Submission, error) {
	if len(data) > MaxSubmissionBytes || !utf8.Valid(data) {
		return Submission{}, invalid(source, "invalid size or UTF-8")
	}
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return Submission{}, invalid(source, err.Error())
	}
	var wire submissionWire
	if err := decodeOneStrict(data, &wire); err != nil {
		return Submission{}, invalid(source, err.Error())
	}
	value, err := wire.value()
	if err != nil {
		return Submission{}, invalid(source, err.Error())
	}
	value = normalizeSubmission(value)
	if err := validateSubmission(value); err != nil {
		return Submission{}, invalid(source, err.Error())
	}
	return value, nil
}

func rejectDuplicateObjectKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := scanJSONValue(decoder, token); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, token json.Token) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid object terminator")
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid array terminator")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func decodeOneStrict(data []byte, destination any) error {
	if err := rejectUnknownFieldsExact(data, reflect.TypeOf(destination), ""); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectUnknownFieldsExact(data []byte, destinationType reflect.Type, fieldPath string) error {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	return validateJSONShape(raw, destinationType, fieldPath)
}

func validateJSONShape(raw json.RawMessage, destinationType reflect.Type, fieldPath string) error {
	for destinationType.Kind() == reflect.Pointer {
		destinationType = destinationType.Elem()
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch destinationType.Kind() {
	case reflect.Struct:
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return err
		}
		fields := make(map[string]reflect.Type, destinationType.NumField())
		for i := 0; i < destinationType.NumField(); i++ {
			structField := destinationType.Field(i)
			name := strings.Split(structField.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = structField.Type
			}
		}
		for name, value := range object {
			valueType, exists := fields[name]
			if !exists {
				return fmt.Errorf("json: unknown field %q", name)
			}
			childPath := name
			if fieldPath != "" {
				childPath = fieldPath + "." + name
			}
			if err := validateJSONShape(value, valueType, childPath); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return err
		}
		for i, value := range values {
			childPath := fmt.Sprintf("%s[%d]", fieldPath, i)
			if err := validateJSONShape(value, destinationType.Elem(), childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeSubmission(value Submission) Submission {
	categories := make([]string, len(value.Categories))
	copy(categories, value.Categories)
	value.Categories = categories
	keywords := make([]string, len(value.Keywords))
	copy(keywords, value.Keywords)
	value.Keywords = keywords
	sort.Strings(value.Categories)
	sort.Strings(value.Keywords)
	return value
}

var (
	categorySlugPattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	githubRepoPattern     = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9._-]+$`)
	gitOIDPattern         = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	digestPattern         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	rfc3339SecondsPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|[+-][0-9]{2}:[0-9]{2})$`)
)

func validateSubmission(value Submission) error {
	if value.APIVersion != APIVersion {
		return errors.New("apiVersion is unsupported")
	}
	if value.Kind != SubmissionKind {
		return errors.New("kind is unsupported")
	}
	if !codePointLength(value.Name, 1, 512) || module.CheckPath(value.Name) != nil {
		return errors.New("name must be a valid Go module path of at most 512 code points")
	}
	if !codePointLength(value.Version, 1, 128) || !semver.IsValid(value.Version) {
		return errors.New("version must be valid Go SemVer")
	}
	_, pathMajor, ok := module.SplitPathVersion(value.Name)
	if !ok || module.CheckPathMajor(value.Version, pathMajor) != nil {
		return errors.New("version does not match name path-major")
	}
	if !githubRepoPattern.MatchString(value.Source.Repository) || value.Source.Repository != repositoryForModule(value.Name) {
		return errors.New("source.repository must be the canonical GitHub repository for name")
	}
	if !validModuleDir(value.Source.ModuleDir) {
		return errors.New("source.moduleDir must be a clean relative path of 1..1024 code points")
	}
	if !codePointLength(value.Source.Tag, 1, 512) || !validGitTag(value.Source.Tag) {
		return errors.New("source.tag must be a valid Git tag of 1..512 code points")
	}
	if !gitOIDPattern.MatchString(value.Source.Commit) {
		return errors.New("source.commit must be a lowercase 40- or 64-hex Git OID")
	}
	if !digestPattern.MatchString(value.Source.ManifestDigest) {
		return errors.New("source.manifestDigest must be a lowercase sha256 digest")
	}
	if err := validateCategories(value.Categories); err != nil {
		return err
	}
	if err := validateKeywords(value.Keywords); err != nil {
		return err
	}
	if err := validateLifecycle(value.Lifecycle); err != nil {
		return err
	}
	return validateManifest(value)
}

func validateCategories(values []string) error {
	if len(values) > 8 {
		return errors.New("categories must contain at most 8 values")
	}
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if !codePointLength(value, 1, 32) || !categorySlugPattern.MatchString(value) {
			return fmt.Errorf("categories[%d] must be a 1..32 code point slug", i)
		}
		if _, exists := seen[value]; exists {
			return errors.New("categories must be unique")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateKeywords(values []string) error {
	if len(values) > 16 {
		return errors.New("keywords must contain at most 16 values")
	}
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if !codePointLength(value, 1, 64) {
			return fmt.Errorf("keywords[%d] must contain 1..64 Unicode code points", i)
		}
		if _, exists := seen[value]; exists {
			return errors.New("keywords must be unique")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateLifecycle(value Lifecycle) error {
	if !codePointLength(value.Message, 0, 2048) {
		return errors.New("lifecycle.message must contain at most 2048 code points")
	}
	switch value.Status {
	case "active":
		if value.Message != "" {
			return errors.New("lifecycle.message must be empty when status is active")
		}
	case "deprecated", "withdrawn":
		if value.Message == "" || strings.ContainsAny(value.Message, "<>") {
			return errors.New("lifecycle.message must be non-empty plain text when status is deprecated or withdrawn")
		}
	default:
		return errors.New("lifecycle.status must be active, deprecated, or withdrawn")
	}
	return nil
}

func validateManifest(value Submission) error {
	manifest := value.Manifest
	if manifest.APIVersion != APIVersion {
		return errors.New("manifest.apiVersion is unsupported")
	}
	if manifest.Kind != "NodePackage" {
		return errors.New("manifest.kind is unsupported")
	}
	metadata := manifest.Metadata
	if metadata.Name != value.Name {
		return errors.New("manifest.metadata.name must match name")
	}
	if !codePointLength(metadata.DisplayName, 1, 128) {
		return errors.New("manifest.metadata.displayName must contain 1..128 code points")
	}
	if !codePointLength(metadata.Description, 0, 2048) {
		return errors.New("manifest.metadata.description must contain at most 2048 code points")
	}
	if !codePointLength(metadata.License, 1, 128) {
		return errors.New("manifest.metadata.license must contain 1..128 code points")
	}
	if !codePointLength(metadata.Repository, 1, 2048) || metadata.Repository != value.Source.Repository {
		return errors.New("manifest.metadata.repository must match source.repository")
	}
	compatibility := manifest.Compatibility
	if compatibility.NodeAPI != APIVersion || !codePointLength(compatibility.NodeAPI, 1, 128) {
		return errors.New("manifest.compatibility.nodeAPI must match apiVersion")
	}
	if !validFullSemver(compatibility.Runtime.MinVersion) {
		return errors.New("manifest.compatibility.runtime.minVersion must be full SemVer")
	}
	if !validFullSemver(compatibility.Runtime.MaxVersionExclusive) {
		return errors.New("manifest.compatibility.runtime.maxVersionExclusive must be full SemVer")
	}
	if semver.Compare(compatibility.Runtime.MinVersion, compatibility.Runtime.MaxVersionExclusive) >= 0 {
		return errors.New("manifest.compatibility.runtime minVersion must be less than maxVersionExclusive")
	}
	return validateRegistrations(value.Name, manifest.Registrations)
}

func validateRegistrations(modulePath string, registrations []Registration) error {
	if len(registrations) > 128 {
		return errors.New("manifest.registrations must contain at most 128 values")
	}
	totalNodes := 0
	seenNodes := make(map[string]struct{})
	for i, registration := range registrations {
		field := fmt.Sprintf("manifest.registrations[%d]", i)
		if !codePointLength(registration.Package, 1, 512) || module.CheckImportPath(registration.Package) != nil || (registration.Package != modulePath && !strings.HasPrefix(registration.Package, modulePath+"/")) {
			return fmt.Errorf("%s.package must be a valid import path within the submitted module", field)
		}
		if len(registration.Nodes) < 1 || len(registration.Nodes) > 512 {
			return fmt.Errorf("%s.nodes must contain 1..512 values", field)
		}
		totalNodes += len(registration.Nodes)
		for j, node := range registration.Nodes {
			nodeField := fmt.Sprintf("%s.nodes[%d]", field, j)
			if !codePointLength(node.Type, 1, 256) {
				return fmt.Errorf("%s.type must contain 1..256 code points", nodeField)
			}
			if !codePointLength(node.Version, 1, 128) {
				return fmt.Errorf("%s.version must contain 1..128 code points", nodeField)
			}
			key := node.Type + "\x00" + node.Version
			if _, exists := seenNodes[key]; exists {
				return fmt.Errorf("duplicate manifest node (%s, %s)", node.Type, node.Version)
			}
			seenNodes[key] = struct{}{}
		}
	}
	if totalNodes > 512 {
		return errors.New("manifest.registrations nodes must contain at most 512 nodes in total")
	}
	return nil
}

func codePointLength(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func repositoryForModule(modulePath string) string {
	parts := strings.Split(modulePath, "/")
	if len(parts) < 3 || parts[0] != "github.com" {
		return ""
	}
	return "https://" + strings.Join(parts[:3], "/")
}

func validModuleDir(value string) bool {
	if !codePointLength(value, 1, 1024) || strings.Contains(value, `\`) || pathpkg.IsAbs(value) || pathpkg.Clean(value) != value {
		return false
	}
	return value == "." || (value != ".." && !strings.HasPrefix(value, "../"))
}

func validGitTag(value string) bool {
	if value == "@" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func validFullSemver(value string) bool {
	if !codePointLength(value, 1, 128) || !semver.IsValid(value) {
		return false
	}
	core := strings.TrimPrefix(value, "v")
	if index := strings.IndexAny(core, "-+"); index >= 0 {
		core = core[:index]
	}
	return strings.Count(core, ".") == 2
}

func LoadSubmissions(root string) ([]SubmissionFile, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	packagesPath := filepath.Join(rootPath, "packages")
	packagesInfo, err := os.Lstat(packagesPath)
	if errors.Is(err, os.ErrNotExist) {
		return []SubmissionFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !packagesInfo.IsDir() || packagesInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: packages directory must be a real directory", packagesPath)
	}
	paths, err := filepath.Glob(filepath.Join(packagesPath, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	submissions := make([]SubmissionFile, 0, len(paths))
	seen := make(map[string]string, len(paths))
	for _, submissionPath := range paths {
		relativeToPackages, err := filepath.Rel(packagesPath, submissionPath)
		if err != nil || relativeToPackages == "." || relativeToPackages == ".." || strings.HasPrefix(relativeToPackages, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToPackages) {
			return nil, fmt.Errorf("%s: path must remain within root/packages", submissionPath)
		}
		info, err := os.Lstat(submissionPath)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: submission must be a regular file", submissionPath)
		}
		if info.Size() < 0 || info.Size() > MaxSubmissionBytes {
			return nil, fmt.Errorf("%s: invalid submission size", submissionPath)
		}
		data, err := os.ReadFile(submissionPath)
		if err != nil {
			return nil, err
		}
		if len(data) > MaxSubmissionBytes {
			return nil, fmt.Errorf("%s: invalid submission size", submissionPath)
		}
		relativePath := filepath.ToSlash(filepath.Join("packages", relativeToPackages))
		submission, err := ParseSubmission(relativePath, data)
		if err != nil {
			return nil, err
		}
		if filepath.Base(submissionPath) != submissionFilename(submission) {
			return nil, fmt.Errorf("%s: filename hash does not match name and version", relativePath)
		}
		key := submission.Name + "\x00" + submission.Version
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s: duplicate submission (%s, %s), already loaded from %s", relativePath, submission.Name, submission.Version, previous)
		}
		seen[key] = relativePath
		output, err := runGit(context.Background(), "-C", root, "log", "-1", "--format=%H%x00%cI", "--", relativePath)
		if err != nil {
			return nil, fmt.Errorf("%s: git log: %w", relativePath, err)
		}
		indexCommit, reviewedAt, err := parseGitMetadata(output)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", relativePath, err)
		}
		submissions = append(submissions, SubmissionFile{
			Path:        relativePath,
			Submission:  submission,
			IndexCommit: indexCommit,
			ReviewedAt:  reviewedAt,
		})
	}
	return submissions, nil
}

var runGit = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "git", args...).Output()
}

func submissionFilename(submission Submission) string {
	sum := sha256.Sum256([]byte(submission.Name + "\n" + submission.Version))
	return fmt.Sprintf("%x.json", sum)
}

func parseGitMetadata(output []byte) (string, time.Time, error) {
	line := strings.TrimSuffix(string(output), "\n")
	if line == "" {
		return "", time.Time{}, errors.New("missing Git commit metadata")
	}
	if strings.Count(line, "\x00") != 1 {
		return "", time.Time{}, errors.New("invalid Git metadata")
	}
	commit, timestamp, _ := strings.Cut(line, "\x00")
	if commit == "" {
		return "", time.Time{}, errors.New("missing Git commit")
	}
	if !gitOIDPattern.MatchString(commit) {
		return "", time.Time{}, errors.New("invalid Git commit OID")
	}
	if !rfc3339SecondsPattern.MatchString(timestamp) {
		return "", time.Time{}, errors.New("invalid Git timestamp")
	}
	reviewedAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid Git timestamp: %w", err)
	}
	return commit, reviewedAt.UTC(), nil
}

func invalid(source, message string) error {
	return fmt.Errorf("invalid submission %s: %s", source, message)
}
