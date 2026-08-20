#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/release-lib.sh
. "$script_dir/release-lib.sh"

require_release_environment \
  GITHUB_REF_NAME \
  GITHUB_REPOSITORY \
  RUNNER_TEMP \
  EXPECTED_COMMIT \
  EXPECTED_TAG_OBJECT

assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"

release_probe=$(mktemp "$RUNNER_TEMP/release-probe.XXXXXX")
release_tags=$(mktemp "$RUNNER_TEMP/release-tags.XXXXXX")
semver_checker=$(mktemp "$RUNNER_TEMP/release-semver.XXXXXX.go")
trap 'rm -f "$release_probe" "$release_tags" "$semver_checker"' EXIT

if gh api --include \
  "repos/$GITHUB_REPOSITORY/releases/tags/$GITHUB_REF_NAME" \
  -H "X-GitHub-Api-Version: 2026-03-10" >"$release_probe" 2>&1; then
  release_error "a Release already exists for $GITHUB_REF_NAME"
  exit 1
elif ! grep -Eq 'HTTP/[0-9.]+ 404|HTTP 404' "$release_probe"; then
  cat "$release_probe" >&2
  release_error "unable to prove that the current Release does not exist"
  exit 1
fi

gh api --paginate \
  "repos/$GITHUB_REPOSITORY/releases?per_page=100" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name' \
  >"$release_tags"

cat >"$semver_checker" <<'GO'
package main

import (
  "bufio"
  "fmt"
  "os"
  "strings"

  "golang.org/x/mod/semver"
)

func stableFull(version string) bool {
  return semver.IsValid(version) && semver.Canonical(version) == version && semver.Prerelease(version) == "" && semver.Build(version) == ""
}

func main() {
  current := os.Args[1]
  if !stableFull(current) {
    fmt.Fprintf(os.Stderr, "%s is not stable full SemVer\n", current)
    os.Exit(1)
  }
  previous := ""
  scanner := bufio.NewScanner(os.Stdin)
  for scanner.Scan() {
    candidate := strings.TrimSpace(scanner.Text())
    if !stableFull(candidate) {
      continue
    }
    if previous == "" || semver.Compare(candidate, previous) > 0 {
      previous = candidate
    }
  }
  if err := scanner.Err(); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
  }
  if previous != "" && semver.Compare(previous, current) >= 0 {
    fmt.Fprintf(os.Stderr, "current release %s must be greater than previous release %s\n", current, previous)
    os.Exit(1)
  }
  fmt.Print(previous)
}
GO

previous_tag=$(CGO_ENABLED=0 go run "$semver_checker" "$GITHUB_REF_NAME" <"$release_tags")
if [[ -n "$previous_tag" ]]; then
  previous_remote=$(read_remote_annotated_tag "$previous_tag")
  IFS=$'\t' read -r _previous_object previous_commit <<<"$previous_remote"
  if ! git merge-base --is-ancestor "$previous_commit" "$EXPECTED_COMMIT"; then
    release_error "fresh previous complete Release Tag is not an ancestor of the expected commit"
    exit 1
  fi
fi

# History enumeration and the previous-Tag ancestor check may take time. Resolve
# the current remote annotated Tag once more as the last gate before creation.
assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
