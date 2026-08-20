#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
checker="$script_dir/check-pr-scope.sh"

assert_ok() {
	if ! "$checker" "$@"; then
		printf 'expected scope checker to accept:' >&2
		printf ' <%s>' "$@" >&2
		printf '\n' >&2
		exit 1
	fi
}

assert_fail() {
	if "$checker" "$@" >/dev/null 2>&1; then
		printf 'expected scope checker to reject:' >&2
		printf ' <%s>' "$@" >&2
		printf '\n' >&2
		exit 1
	fi
}

assert_ok packages/abc.json packages/def.json
assert_fail .github/workflows/ci.yml packages/abc.json
assert_fail cmd/indexgen/main.go
assert_fail packages/nested/abc.json
assert_fail packages/abc.yaml

repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

ruby - "$repository_root" <<'RUBY'
require "json"
require "yaml"

root = ARGV.fetch(0)

def assert_contract(condition, message)
  raise message unless condition
end

workflow_path = File.join(root, ".github", "workflows", "ci.yml")
workflow = YAML.safe_load(
  File.read(workflow_path),
  permitted_classes: [],
  permitted_symbols: [],
  aliases: false,
)

assert_contract(workflow.fetch("on").keys == ["pull_request"], "CI must only use pull_request")
assert_contract(!workflow.fetch("on").key?("pull_request_target"), "pull_request_target is forbidden")
assert_contract(workflow.fetch("permissions") == {"contents" => "read"}, "CI permissions must be contents: read only")

job = workflow.fetch("jobs").fetch("validate-submissions")
assert_contract(job.fetch("name") == "Validate submissions", "required check name changed")
assert_contract(job.fetch("runs-on") == "ubuntu-24.04", "unexpected runner")
steps = job.fetch("steps")
steps_by_name = steps.to_h { |step| [step.fetch("name"), step] }

checkout_sha = "3d3c42e5aac5ba805825da76410c181273ba90b1"
setup_go_sha = "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
trusted_checkout = steps_by_name.fetch("Checkout trusted validator")
assert_contract(trusted_checkout.fetch("uses") == "actions/checkout@#{checkout_sha}", "trusted checkout is not pinned")
assert_contract(trusted_checkout.fetch("with") == {
  "ref" => "${{ github.event.pull_request.base.sha || github.sha }}",
  "path" => "trusted",
  "fetch-depth" => 0,
  "persist-credentials" => false,
}, "trusted checkout inputs changed")

candidate_checkout = steps_by_name.fetch("Checkout candidate as data")
assert_contract(candidate_checkout.fetch("if") == "github.event_name == 'pull_request'", "candidate checkout guard changed")
assert_contract(candidate_checkout.fetch("uses") == "actions/checkout@#{checkout_sha}", "candidate checkout is not pinned")
assert_contract(candidate_checkout.fetch("with") == {
  "ref" => "refs/pull/${{ github.event.pull_request.number }}/merge",
  "path" => "candidate",
  "fetch-depth" => 2,
  "persist-credentials" => false,
}, "candidate checkout inputs changed")

setup_go = steps_by_name.fetch("Setup Go 1.26.5")
assert_contract(setup_go.fetch("uses") == "actions/setup-go@#{setup_go_sha}", "setup-go is not pinned")
assert_contract(setup_go.fetch("with") == {"go-version" => "1.26.5", "cache" => false}, "setup-go inputs changed")
steps.select { |step| step.key?("uses") }.each do |step|
  assert_contract(step.fetch("uses").match?(/@[0-9a-f]{40}\z/), "an action is not pinned to a commit: #{step.fetch('name')}")
end

external_condition = "github.event_name == 'pull_request' && (github.event.pull_request.head.repo.full_name != github.repository || github.event.pull_request.user.login != 'yyl1212')"
maintainer_condition = "github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.user.login == 'yyl1212'"

source_verification = steps_by_name.fetch("Verify external submission sources")
assert_contract(source_verification.fetch("if") == external_condition, "external verification identity guard changed")
assert_contract(source_verification.fetch("working-directory") == "trusted", "external verification must execute trusted code")
assert_contract(source_verification.fetch("env") == {"GITHUB_TOKEN" => "${{ github.token }}"}, "source verification token environment is not minimal")
source_run = source_verification.fetch("run")
[
  "HEAD^1..HEAD",
  "--diff-filter=D",
  "--name-only -z",
  "--no-renames",
  "while IFS= read -r -d '' changed_file; do",
  "./scripts/check-pr-scope.sh \"$changed_file\"",
  "indexcheck_args+=(\"-changed-file\" \"$changed_file\")",
  "\"${indexcheck_args[@]}\"",
  "CGO_ENABLED=0 go run ./cmd/indexcheck -root \"$GITHUB_WORKSPACE/candidate\"",
].each do |required_fragment|
  assert_contract(source_run.include?(required_fragment), "external verification lost safety behavior: #{required_fragment}")
end
assert_contract(!source_run.match?(/\beval\b/), "external verification must not use eval")
assert_contract(!source_run.match?(/(^|\n)\s*(source|\.)\s/), "external verification must not source candidate files")

external_generation = steps_by_name.fetch("Generate external candidate index from trusted code")
assert_contract(external_generation.fetch("if") == external_condition, "external generation identity guard changed")
assert_contract(external_generation.fetch("working-directory") == "trusted", "external generation must execute trusted code")
assert_contract(!external_generation.key?("env"), "external generation must not receive a token environment")
assert_contract(external_generation.fetch("run").include?("CGO_ENABLED=0 go run ./cmd/indexgen"), "external generation must use trusted indexgen")
assert_contract(external_generation.fetch("run").include?("-root \"$GITHUB_WORKSPACE/candidate\""), "external generation must treat candidate as data root")

maintainer_validation = steps_by_name.fetch("Validate trusted maintainer candidate")
assert_contract(maintainer_validation.fetch("if") == maintainer_condition, "maintainer identity guard changed")
assert_contract(maintainer_validation.fetch("working-directory") == "candidate", "maintainer checks must execute in candidate checkout")
assert_contract(!maintainer_validation.key?("env"), "candidate checks must not receive a token environment")
maintainer_run = maintainer_validation.fetch("run")
[
  "CGO_ENABLED=0 go test ./... -count=1",
  "CGO_ENABLED=0 go vet ./...",
  "CGO_ENABLED=0 go run ./cmd/indexcheck -root \"$GITHUB_WORKSPACE/candidate\"",
  "CGO_ENABLED=0 go test ./internal/indexgen -run",
  "CGO_ENABLED=0 go run ./cmd/indexgen",
  "diff -r",
].each do |required_fragment|
  assert_contract(maintainer_run.include?(required_fragment), "maintainer validation is incomplete: #{required_fragment}")
end

token_steps = steps.select do |step|
  step.fetch("env", {}).values.include?("${{ github.token }}")
end
assert_contract(token_steps.map { |step| step.fetch("name") } == ["Verify external submission sources"], "GITHUB_TOKEN escaped the trusted source verification step")

protection = JSON.parse(File.read(File.join(root, "governance", "main-branch-protection.json")))
status_checks = protection.fetch("required_status_checks")
assert_contract(status_checks.fetch("strict") == true, "status checks must be strict")
assert_contract(status_checks.fetch("contexts") == ["Validate submissions"], "required status check changed")
reviews = protection.fetch("required_pull_request_reviews")
assert_contract(reviews.fetch("required_approving_review_count") == 1, "one approval is required")
assert_contract(reviews.fetch("require_code_owner_reviews") == true, "code-owner review is required")
assert_contract(reviews.fetch("dismiss_stale_reviews") == true, "stale reviews must be dismissed")
assert_contract(reviews.fetch("require_last_push_approval") == true, "last-push approval is required")
assert_contract(protection.fetch("required_conversation_resolution") == true, "conversation resolution is required")
assert_contract(protection.fetch("required_linear_history") == true, "linear history is required")
assert_contract(protection.fetch("enforce_admins") == true, "admin enforcement is required")
assert_contract(protection.fetch("allow_force_pushes") == false, "force pushes must be disabled")
assert_contract(protection.fetch("allow_deletions") == false, "branch deletion must be disabled")
RUBY

printf 'PR scope and governance contract tests passed\n'
