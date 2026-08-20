package indexgen

import "unicode/utf8"

// canonicalJSONSize returns the exact byte length produced by Encode's
// encoding/json configuration. It saturates at MaxIndexBytes+1, so an
// oversized index is rejected without allocating an output-sized buffer.
func canonicalJSONSize(index Index) int {
	counter := canonicalSizeCounter{}
	counter.index(index, 0)
	counter.add(1) // json.Encoder.Encode appends one final LF.
	return counter.size
}

type canonicalSizeCounter struct {
	size int
}

func (counter *canonicalSizeCounter) add(size int) {
	if counter.exceeded() || size <= 0 {
		return
	}
	if size > MaxIndexBytes-counter.size {
		counter.size = MaxIndexBytes + 1
		return
	}
	counter.size += size
}

func (counter *canonicalSizeCounter) exceeded() bool {
	return counter.size > MaxIndexBytes
}

func (counter *canonicalSizeCounter) newlineIndent(depth int) {
	counter.add(1 + 2*depth)
}

func (counter *canonicalSizeCounter) field(depth, index int, name string) {
	if index > 0 {
		counter.add(1) // comma
	}
	counter.newlineIndent(depth + 1)
	counter.string(name)
	counter.add(2) // colon and space
}

func (counter *canonicalSizeCounter) endObject(depth int) {
	counter.newlineIndent(depth)
	counter.add(1) // closing brace
}

func (counter *canonicalSizeCounter) arrayElement(depth, index int) {
	if index > 0 {
		counter.add(1) // comma
	}
	counter.newlineIndent(depth + 1)
}

func (counter *canonicalSizeCounter) endArray(depth, length int) {
	if length > 0 {
		counter.newlineIndent(depth)
	}
	counter.add(1) // closing bracket
}

func (counter *canonicalSizeCounter) string(value string) {
	counter.add(1) // opening quote
	for offset := 0; offset < len(value) && !counter.exceeded(); {
		current := value[offset]
		if current < utf8.RuneSelf {
			switch current {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				counter.add(2)
			default:
				if current < 0x20 {
					counter.add(6) // \u00XX
				} else {
					counter.add(1)
				}
			}
			offset++
			continue
		}

		decoded, width := utf8.DecodeRuneInString(value[offset:])
		switch {
		case decoded == utf8.RuneError && width == 1:
			counter.add(6) // encoding/json writes the escaped replacement rune: \ufffd.
		case decoded == '\u2028' || decoded == '\u2029':
			counter.add(6) // encoding/json always escapes JavaScript line separators.
		default:
			counter.add(width)
		}
		offset += width
	}
	counter.add(1) // closing quote
}

func (counter *canonicalSizeCounter) timestamp() {
	// validateIndex requires UTC, second precision, and years 0001..9999.
	// time.Time.MarshalJSON therefore always emits "YYYY-MM-DDTHH:MM:SSZ".
	counter.add(22)
}

func (counter *canonicalSizeCounter) stringArray(values []string, depth int) {
	counter.add(1) // opening bracket
	for index, value := range values {
		if counter.exceeded() {
			return
		}
		counter.arrayElement(depth, index)
		counter.string(value)
	}
	counter.endArray(depth, len(values))
}

func (counter *canonicalSizeCounter) index(value Index, depth int) {
	counter.add(1)
	counter.field(depth, 0, "apiVersion")
	counter.string(value.APIVersion)
	counter.field(depth, 1, "kind")
	counter.string(value.Kind)
	counter.field(depth, 2, "metadata")
	counter.indexMetadata(value.Metadata, depth+1)
	counter.field(depth, 3, "packages")
	counter.packages(value.Packages, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) indexMetadata(value IndexMetadata, depth int) {
	counter.add(1)
	counter.field(depth, 0, "release")
	counter.string(value.Release)
	counter.field(depth, 1, "generatedAt")
	counter.timestamp()
	counter.field(depth, 2, "sourceCommit")
	counter.string(value.SourceCommit)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) packages(values []Package, depth int) {
	counter.add(1)
	for index, value := range values {
		if counter.exceeded() {
			return
		}
		counter.arrayElement(depth, index)
		counter.packageValue(value, depth+1)
	}
	counter.endArray(depth, len(values))
}

func (counter *canonicalSizeCounter) packageValue(value Package, depth int) {
	counter.add(1)
	counter.field(depth, 0, "name")
	counter.string(value.Name)
	counter.field(depth, 1, "categories")
	counter.stringArray(value.Categories, depth+1)
	counter.field(depth, 2, "keywords")
	counter.stringArray(value.Keywords, depth+1)
	counter.field(depth, 3, "versions")
	counter.packageVersions(value.Versions, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) packageVersions(values []PackageVersion, depth int) {
	counter.add(1)
	for index, value := range values {
		if counter.exceeded() {
			return
		}
		counter.arrayElement(depth, index)
		counter.packageVersion(value, depth+1)
	}
	counter.endArray(depth, len(values))
}

func (counter *canonicalSizeCounter) packageVersion(value PackageVersion, depth int) {
	counter.add(1)
	counter.field(depth, 0, "version")
	counter.string(value.Version)
	counter.field(depth, 1, "source")
	counter.source(value.Source, depth+1)
	counter.field(depth, 2, "review")
	counter.review(value.Review, depth+1)
	counter.field(depth, 3, "lifecycle")
	counter.lifecycle(value.Lifecycle, depth+1)
	counter.field(depth, 4, "manifest")
	counter.manifest(value.Manifest, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) source(value Source, depth int) {
	counter.add(1)
	counter.field(depth, 0, "repository")
	counter.string(value.Repository)
	counter.field(depth, 1, "moduleDir")
	counter.string(value.ModuleDir)
	counter.field(depth, 2, "tag")
	counter.string(value.Tag)
	counter.field(depth, 3, "commit")
	counter.string(value.Commit)
	counter.field(depth, 4, "manifestDigest")
	counter.string(value.ManifestDigest)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) review(value Review, depth int) {
	counter.add(1)
	counter.field(depth, 0, "status")
	counter.string(value.Status)
	counter.field(depth, 1, "reviewedAt")
	counter.timestamp()
	counter.field(depth, 2, "indexCommit")
	counter.string(value.IndexCommit)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) lifecycle(value Lifecycle, depth int) {
	counter.add(1)
	counter.field(depth, 0, "status")
	counter.string(value.Status)
	counter.field(depth, 1, "message")
	counter.string(value.Message)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) manifest(value NodePackageManifest, depth int) {
	counter.add(1)
	counter.field(depth, 0, "apiVersion")
	counter.string(value.APIVersion)
	counter.field(depth, 1, "kind")
	counter.string(value.Kind)
	counter.field(depth, 2, "metadata")
	counter.nodePackageMetadata(value.Metadata, depth+1)
	counter.field(depth, 3, "compatibility")
	counter.compatibility(value.Compatibility, depth+1)
	counter.field(depth, 4, "registrations")
	counter.registrations(value.Registrations, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) nodePackageMetadata(value NodePackageMetadata, depth int) {
	counter.add(1)
	counter.field(depth, 0, "name")
	counter.string(value.Name)
	counter.field(depth, 1, "displayName")
	counter.string(value.DisplayName)
	counter.field(depth, 2, "description")
	counter.string(value.Description)
	counter.field(depth, 3, "license")
	counter.string(value.License)
	counter.field(depth, 4, "repository")
	counter.string(value.Repository)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) compatibility(value Compatibility, depth int) {
	counter.add(1)
	counter.field(depth, 0, "nodeAPI")
	counter.string(value.NodeAPI)
	counter.field(depth, 1, "runtime")
	counter.runtimeRange(value.Runtime, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) runtimeRange(value RuntimeRange, depth int) {
	counter.add(1)
	counter.field(depth, 0, "minVersion")
	counter.string(value.MinVersion)
	counter.field(depth, 1, "maxVersionExclusive")
	counter.string(value.MaxVersionExclusive)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) registrations(values []Registration, depth int) {
	counter.add(1)
	for index, value := range values {
		if counter.exceeded() {
			return
		}
		counter.arrayElement(depth, index)
		counter.registration(value, depth+1)
	}
	counter.endArray(depth, len(values))
}

func (counter *canonicalSizeCounter) registration(value Registration, depth int) {
	counter.add(1)
	counter.field(depth, 0, "package")
	counter.string(value.Package)
	counter.field(depth, 1, "nodes")
	counter.nodes(value.Nodes, depth+1)
	counter.endObject(depth)
}

func (counter *canonicalSizeCounter) nodes(values []NodeRef, depth int) {
	counter.add(1)
	for index, value := range values {
		if counter.exceeded() {
			return
		}
		counter.arrayElement(depth, index)
		counter.node(value, depth+1)
	}
	counter.endArray(depth, len(values))
}

func (counter *canonicalSizeCounter) node(value NodeRef, depth int) {
	counter.add(1)
	counter.field(depth, 0, "type")
	counter.string(value.Type)
	counter.field(depth, 1, "version")
	counter.string(value.Version)
	counter.endObject(depth)
}
