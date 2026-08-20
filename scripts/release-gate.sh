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

history_result=$(mktemp "$RUNNER_TEMP/release-history-result.XXXXXX")
trap 'rm -f "$history_result"' EXIT

set +e
timeout --signal=KILL 60s bash "$script_dir/release-history.sh" >"$history_result"
history_status=$?
set -e
if [[ "$history_status" != 0 ]]; then
  release_error "bounded exact Release history validation failed"
  exit "$history_status"
fi

previous_tag=
previous_object=
previous_commit=
extra=
if [[ -s "$history_result" ]]; then
  IFS=$'\t' read -r previous_tag previous_object previous_commit extra <"$history_result"
  if [[ -z "$previous_tag" || ! "$previous_object" =~ ^[0-9a-f]{40}$ || ! "$previous_commit" =~ ^[0-9a-f]{40}$ || -n "$extra" ]] ||
    [[ $(wc -l <"$history_result" | tr -d ' ') != 1 ]]; then
    release_error "malformed bounded Release history result"
    exit 1
  fi
  assert_remote_annotated_tag "$previous_tag" "$previous_object" "$previous_commit"
  if ! git merge-base --is-ancestor "$previous_commit" "$EXPECTED_COMMIT"; then
    release_error "fresh previous complete Release Tag is not an ancestor of the expected commit"
    exit 1
  fi
fi

# History enumeration and the previous-Tag ancestor check may take time. Resolve
# the current remote annotated Tag once more, then prove the expected commit is
# reachable from a freshly fetched and unambiguously advertised remote main.
# This is the common build/publish gate and its final operation before creation.
assert_remote_annotated_tag "$GITHUB_REF_NAME" "$EXPECTED_TAG_OBJECT" "$EXPECTED_COMMIT"
assert_expected_commit_on_remote_main "$EXPECTED_COMMIT"
