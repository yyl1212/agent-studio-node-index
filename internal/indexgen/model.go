package indexgen

import "time"

const (
	APIVersion            = "agent-studio.dev/v1alpha1"
	SubmissionKind        = "NodePackageSubmission"
	IndexKind             = "NodePackageIndex"
	MaxSubmissionBytes    = 512 << 10
	MaxIndexBytes         = 4 << 20
	MaxPackages           = 1000
	MaxVersionsPerPackage = 20
)

type Submission struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Name       string              `json:"name"`
	Version    string              `json:"version"`
	Source     Source              `json:"source"`
	Categories []string            `json:"categories"`
	Keywords   []string            `json:"keywords"`
	Lifecycle  Lifecycle           `json:"lifecycle"`
	Manifest   NodePackageManifest `json:"manifest"`
}

type SubmissionFile struct {
	Path        string
	Submission  Submission
	IndexCommit string
	ReviewedAt  time.Time
}

type Source struct {
	Repository     string `json:"repository"`
	ModuleDir      string `json:"moduleDir"`
	Tag            string `json:"tag"`
	Commit         string `json:"commit"`
	ManifestDigest string `json:"manifestDigest"`
}

type Lifecycle struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Review struct {
	Status      string    `json:"status"`
	ReviewedAt  time.Time `json:"reviewedAt"`
	IndexCommit string    `json:"indexCommit"`
}

type NodePackageManifest struct {
	APIVersion    string              `json:"apiVersion"`
	Kind          string              `json:"kind"`
	Metadata      NodePackageMetadata `json:"metadata"`
	Compatibility Compatibility       `json:"compatibility"`
	Registrations []Registration      `json:"registrations"`
}

type NodePackageMetadata struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	License     string `json:"license"`
	Repository  string `json:"repository"`
}

type Compatibility struct {
	NodeAPI string       `json:"nodeAPI"`
	Runtime RuntimeRange `json:"runtime"`
}

type RuntimeRange struct {
	MinVersion          string `json:"minVersion"`
	MaxVersionExclusive string `json:"maxVersionExclusive"`
}

type Registration struct {
	Package string    `json:"package"`
	Nodes   []NodeRef `json:"nodes"`
}

type NodeRef struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

type Index struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   IndexMetadata `json:"metadata"`
	Packages   []Package     `json:"packages"`
}

type IndexMetadata struct {
	Release      string    `json:"release"`
	GeneratedAt  time.Time `json:"generatedAt"`
	SourceCommit string    `json:"sourceCommit"`
}

type Package struct {
	Name       string           `json:"name"`
	Categories []string         `json:"categories"`
	Keywords   []string         `json:"keywords"`
	Versions   []PackageVersion `json:"versions"`
}

type PackageVersion struct {
	Version   string              `json:"version"`
	Source    Source              `json:"source"`
	Review    Review              `json:"review"`
	Lifecycle Lifecycle           `json:"lifecycle"`
	Manifest  NodePackageManifest `json:"manifest"`
}
