#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
# shellcheck source=scripts/release-lib.sh
. "$script_dir/release-lib.sh"

require_release_environment GITHUB_REF_NAME GITHUB_REPOSITORY RUNNER_TEMP

max_remote_ref_lines=200
max_stable_candidates=100
remote_refs=$(mktemp "$RUNNER_TEMP/release-remote-refs.XXXXXX")
candidates=$(mktemp "$RUNNER_TEMP/release-candidates.XXXXXX")
published=$(mktemp "$RUNNER_TEMP/release-published.XXXXXX")
previous=$(mktemp "$RUNNER_TEMP/release-previous.XXXXXX")
release_json=$(mktemp "$RUNNER_TEMP/release-history.XXXXXX.json")
release_error_output=$(mktemp "$RUNNER_TEMP/release-history.XXXXXX.err")
trap 'rm -f "$remote_refs" "$candidates" "$published" "$previous" "$release_json" "$release_error_output"' EXIT

assert_current_published_release_absent() {
  : >"$release_json"
  : >"$release_error_output"
  if gh api \
    "repos/$GITHUB_REPOSITORY/releases/tags/$GITHUB_REF_NAME" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    >"$release_json" 2>"$release_error_output"; then
    release_error "a published Release already exists for $GITHUB_REF_NAME"
    return 1
  elif ! grep -Eq 'HTTP/[0-9.]+ 404|HTTP 404' "$release_error_output"; then
    cat "$release_error_output" >&2
    release_error "unable to prove that the current published Release does not exist"
    return 1
  fi
}

assert_current_published_release_absent

set +e
git ls-remote --tags origin 'refs/tags/v*' |
  head -n "$((max_remote_ref_lines + 1))" >"$remote_refs"
pipeline_status=("${PIPESTATUS[@]}")
set -e
remote_ref_lines=$(wc -l <"$remote_refs" | tr -d ' ')
if ((remote_ref_lines > max_remote_ref_lines)); then
  release_error "remote Tag history exceeds the $max_remote_ref_lines-record bound"
  exit 1
fi
if [[ "${pipeline_status[1]}" != 0 || "${pipeline_status[0]}" != 0 ]]; then
  release_error "unable to enumerate bounded remote Tag history"
  exit 1
fi

(
  cd "$repository_root"
  CGO_ENABLED=0 go run ./cmd/releasehistory candidates --max "$max_stable_candidates"
) <"$remote_refs" >"$candidates"

while IFS=$'\t' read -r tag object commit; do
  [[ -n "$tag" ]] || continue
  [[ "$tag" != "$GITHUB_REF_NAME" ]] || continue
  : >"$release_json"
  : >"$release_error_output"
  if gh api \
    "repos/$GITHUB_REPOSITORY/releases/tags/$tag" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    >"$release_json" 2>"$release_error_output"; then
    if ! jq -e \
      --arg tag "$tag" \
      '
        type == "object" and
        has("id") and has("tag_name") and has("target_commitish") and
        has("draft") and has("prerelease") and has("published_at") and
        (.id | type) == "number" and .id > 0 and
        .tag_name == $tag and
        (.target_commitish | type) == "string" and (.target_commitish | length) > 0 and
        .draft == false and
        (.prerelease | type) == "boolean" and
        (.published_at | type) == "string" and (.published_at | length) > 0
      ' "$release_json" >/dev/null; then
      release_error "malformed exact Release response for $tag"
      exit 1
    fi
    if jq -e '.prerelease == false' "$release_json" >/dev/null; then
      printf '%s\t%s\t%s\n' "$tag" "$object" "$commit" >>"$published"
    fi
  elif ! grep -Eq 'HTTP/[0-9.]+ 404|HTTP 404' "$release_error_output"; then
    cat "$release_error_output" >&2
    release_error "exact Release lookup failed for $tag"
    exit 1
  fi
done <"$candidates"

(
  cd "$repository_root"
  CGO_ENABLED=0 go run ./cmd/releasehistory previous --current "$GITHUB_REF_NAME"
) <"$published" >"$previous"

# Repeat the exact current-Tag lookup after all candidate queries so a published
# Release that appeared during history enumeration cannot reach draft creation.
# A clean 404 proves only published absence; draft conflicts are closed by POST.
assert_current_published_release_absent
cat "$previous"
