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
  EXPECTED_TAG_OBJECT \
  RELEASE_DIST_DIR \
  RELEASE_ID \
  RELEASE_POLL_TIMEOUT_SECONDS

release_json=$(mktemp "$RUNNER_TEMP/immutable-release.XXXXXX.json")
trap 'rm -f "$release_json"' EXIT
expected_state=$(printf "false\tfalse\ttrue\t$GITHUB_REF_NAME")
deadline=$((SECONDS + RELEASE_POLL_TIMEOUT_SECONDS))

while true; do
  remaining=$((deadline - SECONDS))
  if (( remaining <= 1 )); then
    exit 124
  fi

  assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
  gh api \
    "repos/$GITHUB_REPOSITORY/releases/tags/$GITHUB_REF_NAME" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    >"$release_json"

  state=$(jq -r '[.draft,.prerelease,.immutable,.tag_name] | @tsv' "$release_json")
  if [[ "$state" == "$expected_state" ]] &&
    jq -e \
      --argjson id "$RELEASE_ID" \
      --arg target "$EXPECTED_COMMIT" \
      '.id == $id and .target_commitish == $target' \
      "$release_json" >/dev/null &&
    assert_release_api_assets "$release_json" "$RELEASE_DIST_DIR"; then
    # Re-resolve after the single Release API response so an observable Tag
    # mutation during that response cannot satisfy final verification.
    assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
    exit 0
  fi

  remaining=$((deadline - SECONDS))
  if (( remaining <= 1 )); then
    exit 124
  fi
  if (( remaining > 2 )); then
    sleep 2
  else
    sleep 1
  fi
done
