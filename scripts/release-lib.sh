#!/usr/bin/env bash

release_error() {
  printf 'release validation failed: %s\n' "$*" >&2
}

require_release_environment() {
  local name
  for name in "$@"; do
    if [[ -z ${!name:-} ]]; then
      release_error "required environment variable is empty: $name"
      return 1
    fi
  done
}

assert_exact_release_assets() {
  local directory=$1
  local expected actual name
  expected=$(printf '%s\n' checksums.txt index.json node-index-v1alpha1.schema.json)
  actual=$(find "$directory" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)
  if [[ "$actual" != "$expected" ]]; then
    release_error "unexpected Release asset set in $directory"
    printf 'expected:\n%s\nactual:\n%s\n' "$expected" "$actual" >&2
    return 1
  fi
  for name in checksums.txt index.json node-index-v1alpha1.schema.json; do
    if [[ ! -f "$directory/$name" || -L "$directory/$name" || ! -s "$directory/$name" ]]; then
      release_error "Release asset must be a non-empty regular file: $directory/$name"
      return 1
    fi
  done
}

read_remote_annotated_tag() {
  local tag=$1
  local refs direct peeled direct_count peeled_count
  refs=$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}")
  direct=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/$tag" '$2 == ref { print $1 }')
  peeled=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/$tag^{}" '$2 == ref { print $1 }')
  direct_count=$(printf '%s\n' "$direct" | sed '/^$/d' | wc -l | tr -d ' ')
  peeled_count=$(printf '%s\n' "$peeled" | sed '/^$/d' | wc -l | tr -d ' ')
  if [[ "$direct_count" != 1 || "$peeled_count" != 1 ]]; then
    release_error "remote Tag is missing, ambiguous, or not annotated: $tag"
    return 1
  fi
  printf '%s\t%s\n' "$direct" "$peeled"
}

assert_remote_annotated_tag() {
  local tag=$1
  local expected_object=$2
  local expected_commit=$3
  local resolved object commit
  resolved=$(read_remote_annotated_tag "$tag")
  IFS=$'\t' read -r object commit <<<"$resolved"
  if [[ "$object" != "$expected_object" || "$commit" != "$expected_commit" ]]; then
    release_error "remote annotated Tag drifted: $tag"
    printf 'expected object/commit: %s %s\nactual object/commit: %s %s\n' \
      "$expected_object" "$expected_commit" "$object" "$commit" >&2
    return 1
  fi
}

read_fresh_remote_main() {
  local fetched_commit refs ref_count remote_commit remote_ref extra

  # Fetch the exact branch so the commit used by merge-base is fresh and
  # present locally. Resolve the advertised ref separately and require the two
  # observations to agree; any drift between them fails closed.
  git fetch --no-tags origin refs/heads/main >/dev/null
  fetched_commit=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
  if [[ ! "$fetched_commit" =~ ^[0-9a-f]{40}$ ]]; then
    release_error "freshly fetched remote main did not resolve to a commit"
    return 1
  fi

  refs=$(git ls-remote --heads origin refs/heads/main)
  ref_count=$(printf '%s\n' "$refs" | sed '/^$/d' | wc -l | tr -d ' ')
  if [[ "$ref_count" != 1 ]]; then
    release_error "remote refs/heads/main is missing or ambiguous"
    return 1
  fi
  IFS=$'\t' read -r remote_commit remote_ref extra <<<"$refs"
  if [[ ! "$remote_commit" =~ ^[0-9a-f]{40}$ || "$remote_ref" != "refs/heads/main" || -n "$extra" ]]; then
    release_error "remote refs/heads/main is malformed"
    return 1
  fi
  if [[ "$fetched_commit" != "$remote_commit" ]]; then
    release_error "remote refs/heads/main drifted during fresh resolution"
    return 1
  fi
  printf '%s\n' "$remote_commit"
}

assert_expected_commit_on_remote_main() {
  local expected_commit=$1
  local remote_main_commit
  remote_main_commit=$(read_fresh_remote_main)
  if ! git merge-base --is-ancestor "$expected_commit" "$remote_main_commit"; then
    release_error "expected Release commit is not reachable from fresh remote main"
    return 1
  fi
}

assert_release_api_assets() {
  local release_json=$1
  local directory=$2
  local checksums_digest index_digest schema_digest
  checksums_digest="sha256:$(sha256sum "$directory/checksums.txt" | awk '{print $1}')"
  index_digest="sha256:$(sha256sum "$directory/index.json" | awk '{print $1}')"
  schema_digest="sha256:$(sha256sum "$directory/node-index-v1alpha1.schema.json" | awk '{print $1}')"
  jq -e \
    --arg checksums_digest "$checksums_digest" \
    --arg index_digest "$index_digest" \
    --arg schema_digest "$schema_digest" \
    '
      (.assets | length) == 3 and
      ([.assets[].name] | sort) == ["checksums.txt", "index.json", "node-index-v1alpha1.schema.json"] and
      all(.assets[];
        (.size | type) == "number" and .size > 0 and
        (.digest | type) == "string" and
        (.digest | startswith("sha256:"))
      ) and
      (first(.assets[] | select(.name == "checksums.txt")).digest == $checksums_digest) and
      (first(.assets[] | select(.name == "index.json")).digest == $index_digest) and
      (first(.assets[] | select(.name == "node-index-v1alpha1.schema.json")).digest == $schema_digest)
    ' "$release_json" >/dev/null
}
