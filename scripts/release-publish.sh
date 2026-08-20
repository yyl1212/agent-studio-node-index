#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/release-lib.sh
. "$script_dir/release-lib.sh"

if [[ -z ${RELEASE_POLL_TIMEOUT_SECONDS:-} ]]; then
  RELEASE_POLL_TIMEOUT_SECONDS=60
fi
require_release_environment \
  GITHUB_REF_NAME \
  GITHUB_REPOSITORY \
  RUNNER_TEMP \
  RELEASE_DIST_DIR \
  RELEASE_EXPECTED_DIR \
  RELEASE_POLL_TIMEOUT_SECONDS

assert_exact_release_assets "$RELEASE_DIST_DIR"
assert_exact_release_assets "$RELEASE_EXPECTED_DIR"
(cd "$RELEASE_DIST_DIR" && sha256sum --check checksums.txt)
(cd "$RELEASE_EXPECTED_DIR" && sha256sum --check checksums.txt)
for name in checksums.txt index.json node-index-v1alpha1.schema.json; do
  cmp --silent "$RELEASE_DIST_DIR/$name" "$RELEASE_EXPECTED_DIR/$name"
done

release=$(jq -er '.metadata.release' "$RELEASE_DIST_DIR/index.json")
EXPECTED_COMMIT=$(jq -er '.metadata.sourceCommit' "$RELEASE_DIST_DIR/index.json")
EXPECTED_TAG_OBJECT=$(git rev-parse "$GITHUB_REF_NAME")
local_tag_commit=$(git rev-parse "$GITHUB_REF_NAME^{commit}")
if [[ "$release" != "$GITHUB_REF_NAME" ]]; then
  release_error "downloaded index release does not match the current Tag"
  exit 1
fi
if [[ "$EXPECTED_COMMIT" != "$local_tag_commit" ]]; then
  release_error "downloaded index source commit does not match the checked-out Tag commit"
  exit 1
fi
export EXPECTED_COMMIT EXPECTED_TAG_OBJECT RELEASE_DIST_DIR RELEASE_EXPECTED_DIR RELEASE_POLL_TIMEOUT_SECONDS

draft_response=$(mktemp "$RUNNER_TEMP/draft-create.XXXXXX.json")
cleanup_response=$(mktemp "$RUNNER_TEMP/draft-cleanup.XXXXXX.json")
draft_json=$(mktemp "$RUNNER_TEMP/draft-release.XXXXXX.json")
download_dir=$(mktemp -d "$RUNNER_TEMP/draft-assets.XXXXXX")
draft_id=
cleanup_armed=0

cleanup_release_draft() {
  local original_status=$?
  local cleanup_id=
  trap - EXIT INT TERM
  set +e

  rm -rf "$download_dir"
  if [[ "$cleanup_armed" == 1 ]]; then
    cleanup_id=$draft_id
    if [[ -z "$cleanup_id" && -s "$draft_response" ]]; then
      cleanup_id=$(jq -er '.id | select(type == "number" and . > 0)' "$draft_response" 2>/dev/null)
    fi
    if [[ "$cleanup_id" =~ ^[1-9][0-9]*$ ]]; then
      printf 'Release publication failed; inspecting exact draft ID %s before cleanup\n' "$cleanup_id" >&2
      if gh api \
        "repos/$GITHUB_REPOSITORY/releases/$cleanup_id" \
        -H "X-GitHub-Api-Version: 2026-03-10" >"$cleanup_response"; then
        if jq -e \
          --argjson id "$cleanup_id" \
          --arg tag "$GITHUB_REF_NAME" \
          --arg target "$EXPECTED_COMMIT" \
          '
            .id == $id and
            .tag_name == $tag and
            .target_commitish == $target and
            .draft == true and
            .immutable != true
          ' "$cleanup_response" >/dev/null; then
          if assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"; then
            if gh api --method DELETE \
              "repos/$GITHUB_REPOSITORY/releases/$cleanup_id" \
              -H "X-GitHub-Api-Version: 2026-03-10" >/dev/null; then
              printf 'Deleted failed draft Release ID %s; original failure follows above\n' "$cleanup_id" >&2
            else
              release_error "unable to delete failed draft Release ID $cleanup_id"
            fi
          else
            release_error "refusing draft cleanup because the remote Tag no longer matches the expected target"
          fi
        else
          release_error "refusing cleanup because exact Release ID $cleanup_id is no longer the expected draft"
        fi
      else
        release_error "unable to inspect exact Release ID $cleanup_id for safe cleanup"
      fi
    else
      release_error "draft may have been created but no exact Release ID was captured; refusing fuzzy cleanup"
    fi
  fi

  rm -f "$draft_response" "$cleanup_response" "$draft_json"
  exit "$original_status"
}

trap cleanup_release_draft EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# This gate is deliberately the final operation before draft creation. It
# re-enumerates stable Releases and resolves remote annotated Tags afresh.
bash "$script_dir/release-gate.sh"

cleanup_armed=1
# Release-by-tag 404 covers published Releases only. This create request is the
# atomic conflict boundary for an existing draft; never adopt or overwrite it.
gh api --method POST \
  "repos/$GITHUB_REPOSITORY/releases" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  -f tag_name="$GITHUB_REF_NAME" \
  -f target_commitish="$EXPECTED_COMMIT" \
  -f name="$GITHUB_REF_NAME" \
  -f body="Agent Studio 官方精选节点包索引 $GITHUB_REF_NAME" \
  -F draft=true \
  -F prerelease=false \
  -F generate_release_notes=false \
  -f make_latest=false \
  >"$draft_response"
draft_id=$(jq -er '.id | select(type == "number" and . > 0)' "$draft_response")
jq -e \
  --argjson id "$draft_id" \
  --arg tag "$GITHUB_REF_NAME" \
  --arg target "$EXPECTED_COMMIT" \
  '
    .id == $id and
    .tag_name == $tag and
    .target_commitish == $target and
    .draft == true and
    .prerelease == false and
    .immutable != true
  ' "$draft_response" >/dev/null

gh release upload "$GITHUB_REF_NAME" \
  "$RELEASE_DIST_DIR/checksums.txt" \
  "$RELEASE_DIST_DIR/index.json" \
  "$RELEASE_DIST_DIR/node-index-v1alpha1.schema.json"

gh api \
  "repos/$GITHUB_REPOSITORY/releases/$draft_id" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  >"$draft_json"
jq -e \
  --argjson id "$draft_id" \
  --arg tag "$GITHUB_REF_NAME" \
  --arg target "$EXPECTED_COMMIT" \
  '
    .id == $id and
    .tag_name == $tag and
    .target_commitish == $target and
    .draft == true and
    .prerelease == false and
    .immutable != true
  ' "$draft_json" >/dev/null
assert_release_api_assets "$draft_json" "$RELEASE_DIST_DIR"

gh release download "$GITHUB_REF_NAME" \
  --dir "$download_dir" \
  --pattern checksums.txt \
  --pattern index.json \
  --pattern node-index-v1alpha1.schema.json
assert_exact_release_assets "$download_dir"
(cd "$download_dir" && sha256sum --check checksums.txt)
for name in checksums.txt index.json node-index-v1alpha1.schema.json; do
  cmp --silent "$RELEASE_DIST_DIR/$name" "$download_dir/$name"
done

# Re-resolve both the annotated Tag object and its peeled commit immediately
# before the irreversible promotion.
assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
gh api \
  "repos/$GITHUB_REPOSITORY/releases/$draft_id" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  >"$draft_json"
jq -e \
  --argjson id "$draft_id" \
  --arg tag "$GITHUB_REF_NAME" \
  --arg target "$EXPECTED_COMMIT" \
  '
    .id == $id and
    .tag_name == $tag and
    .target_commitish == $target and
    .draft == true and
    .prerelease == false and
    .immutable != true
  ' "$draft_json" >/dev/null
assert_release_api_assets "$draft_json" "$RELEASE_DIST_DIR"

# Keep this as the final remote operation before promotion. The earlier lookup
# protects the draft read itself; this one closes any observable drift window.
assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
gh release edit "$GITHUB_REF_NAME" --draft=false --prerelease=false --latest

RELEASE_ID=$draft_id
export RELEASE_ID
set +e
timeout --signal=KILL "$RELEASE_POLL_TIMEOUT_SECONDS"s \
  bash "$script_dir/release-poll.sh"
poll_status=$?
set -e
if [[ "$poll_status" != 0 ]]; then
  release_error "Release did not become immutable with the exact verified assets within $RELEASE_POLL_TIMEOUT_SECONDS seconds"
  exit "$poll_status"
fi

cleanup_armed=0
rm -rf "$download_dir"
rm -f "$draft_response" "$cleanup_response" "$draft_json"
trap - EXIT INT TERM
