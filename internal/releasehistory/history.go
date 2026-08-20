package releasehistory

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

var objectIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Tag identifies an annotated Tag object and its peeled commit.
type Tag struct {
	Name   string
	Object string
	Commit string
}

type remoteTag struct {
	direct []string
	peeled []string
}

func stableFull(version string) bool {
	return semver.IsValid(version) &&
		semver.Canonical(version) == version &&
		semver.Prerelease(version) == "" &&
		semver.Build(version) == ""
}

// StableAnnotatedTags parses git ls-remote output and returns the bounded set
// of unambiguous, annotated, full stable SemVer Tags in ascending order.
func StableAnnotatedTags(reader io.Reader, max int) ([]Tag, error) {
	if max <= 0 {
		return nil, fmt.Errorf("candidate limit must be positive")
	}

	records := make(map[string]*remoteTag)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !objectIDPattern.MatchString(fields[0]) || !strings.HasPrefix(fields[1], "refs/tags/") {
			return nil, fmt.Errorf("malformed remote Tag record: %q", scanner.Text())
		}
		name := strings.TrimPrefix(fields[1], "refs/tags/")
		peeled := strings.HasSuffix(name, "^{}")
		name = strings.TrimSuffix(name, "^{}")
		if name == "" {
			return nil, fmt.Errorf("empty remote Tag name")
		}
		record := records[name]
		if record == nil {
			record = &remoteTag{}
			records[name] = record
		}
		if peeled {
			record.peeled = append(record.peeled, fields[0])
		} else {
			record.direct = append(record.direct, fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read remote Tags: %w", err)
	}

	tags := make([]Tag, 0, len(records))
	for name, record := range records {
		if !stableFull(name) {
			continue
		}
		if len(record.direct) != 1 {
			return nil, fmt.Errorf("stable Tag %s has %d direct objects", name, len(record.direct))
		}
		if len(record.peeled) == 0 {
			continue
		}
		if len(record.peeled) != 1 {
			return nil, fmt.Errorf("stable Tag %s has %d peeled commits", name, len(record.peeled))
		}
		tags = append(tags, Tag{Name: name, Object: record.direct[0], Commit: record.peeled[0]})
		if len(tags) > max {
			return nil, fmt.Errorf("stable annotated Tag count exceeds %d", max)
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		return semver.Compare(tags[i].Name, tags[j].Name) < 0
	})
	return tags, nil
}

// HighestPrevious returns the highest published stable Release below current.
// An equal or newer published Release fails closed.
func HighestPrevious(reader io.Reader, current string) (Tag, bool, error) {
	if !stableFull(current) {
		return Tag{}, false, fmt.Errorf("%s is not stable full SemVer", current)
	}

	var previous Tag
	found := false
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || !stableFull(fields[0]) || !objectIDPattern.MatchString(fields[1]) || !objectIDPattern.MatchString(fields[2]) {
			return Tag{}, false, fmt.Errorf("malformed published Release record: %q", scanner.Text())
		}
		if _, duplicate := seen[fields[0]]; duplicate {
			return Tag{}, false, fmt.Errorf("duplicate published Release Tag: %s", fields[0])
		}
		seen[fields[0]] = struct{}{}
		if semver.Compare(fields[0], current) >= 0 {
			return Tag{}, false, fmt.Errorf("current release %s must be greater than previous release %s", current, fields[0])
		}
		if !found || semver.Compare(fields[0], previous.Name) > 0 {
			previous = Tag{Name: fields[0], Object: fields[1], Commit: fields[2]}
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		return Tag{}, false, fmt.Errorf("read published Releases: %w", err)
	}
	return previous, found, nil
}
