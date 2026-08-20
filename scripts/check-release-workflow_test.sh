#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

ruby - "$repository_root" <<'RUBY'
require "yaml"
require "shellwords"
require "open3"
require "tmpdir"
require "json"
require "digest"

root = ARGV.fetch(0)
workflow_path = File.join(root, ".github", "workflows", "release.yml")
abort "release workflow is missing: #{workflow_path}" unless File.file?(workflow_path)
actionlint_config_path = File.join(root, ".github", "actionlint.yaml")
abort "actionlint forward-compatibility config is missing: #{actionlint_config_path}" unless File.file?(actionlint_config_path)

actionlint_config = YAML.safe_load(File.read(actionlint_config_path), permitted_classes: [], permitted_symbols: [], aliases: false)
expected_actionlint_config = {
  "paths" => {
    ".github/workflows/release.yml" => {
      "ignore" => ['\Aunexpected key "queue" for "concurrency" section\. expected one of "cancel-in-progress", "group"\z'],
    },
  },
}
abort "actionlint config must ignore only the pinned v1.7.12 queue schema lag" unless actionlint_config == expected_actionlint_config

raw_workflow = File.read(workflow_path)
workflow = YAML.safe_load(
  raw_workflow,
  permitted_classes: [],
  permitted_symbols: [],
  aliases: false,
)

CHECKOUT_SHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"
SETUP_GO_SHA = "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
UPLOAD_ARTIFACT_SHA = "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
DOWNLOAD_ARTIFACT_SHA = "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"

def validate(workflow, raw_workflow)
  failures = []
  assert = ->(condition, message) { failures << message unless condition }

  assert.call(workflow.fetch("on", nil) == {"push" => {"tags" => ["v*"]}}, "release trigger must be tag-only v*")
  assert.call(!raw_workflow.include?("pull_request_target"), "release workflow must not use pull_request_target")
  assert.call(workflow.fetch("permissions", nil) == {"contents" => "read"}, "global permissions must be contents: read")
  assert.call(workflow.fetch("concurrency", nil) == {
    "group" => "agent-studio-node-index-release",
    "cancel-in-progress" => false,
    "queue" => "max",
  }, "release workflow must use one fixed non-cancelling concurrency group")

  jobs = workflow.fetch("jobs", {})
  assert.call(jobs.keys == ["build", "publish"], "release workflow must contain only build and publish jobs")
  return failures unless jobs.key?("build") && jobs.key?("publish")

  build = jobs.fetch("build")
  publish = jobs.fetch("publish")
  assert.call(build.fetch("runs-on", nil) == "ubuntu-24.04", "build runner changed")
  assert.call(!build.key?("permissions"), "build must inherit contents: read")
  assert.call(publish.fetch("runs-on", nil) == "ubuntu-24.04", "publish runner changed")
  assert.call(publish.fetch("needs", nil) == "build", "publish must wait for build")
  assert.call(publish.fetch("permissions", nil) == {"contents" => "write"}, "publish permissions must be contents: write")

  build_steps = build.fetch("steps", [])
  publish_steps = publish.fetch("steps", [])
  all_steps = build_steps + publish_steps
  all_steps.select { |step| step.key?("uses") }.each do |step|
    assert.call(step.fetch("uses").match?(/@[0-9a-f]{40}\z/), "mutable Action ref: #{step.fetch('name', '<unnamed>')}")
  end

  build_by_name = build_steps.to_h { |step| [step.fetch("name", ""), step] }
  publish_by_name = publish_steps.to_h { |step| [step.fetch("name", ""), step] }
  required_build_steps = [
    "Checkout release tag",
    "Setup Go 1.26.5",
    "Validate release workflow regressions",
    "Validate release history",
    "Test and generate exact assets",
    "Upload verified release assets",
  ]
  required_publish_steps = [
    "Checkout release tag",
    "Setup Go 1.26.5",
    "Download verified release assets",
    "Regenerate expected release assets",
    "Publish verified immutable release",
  ]
  assert.call(build_steps.map { |step| step.fetch("name", "") } == required_build_steps, "build step order changed")
  assert.call(publish_steps.map { |step| step.fetch("name", "") } == required_publish_steps, "publish ordering may allow publication before validation")
  return failures unless required_build_steps.all? { |name| build_by_name.key?(name) } && required_publish_steps.all? { |name| publish_by_name.key?(name) }

  [build_by_name, publish_by_name].each do |steps|
    checkout = steps.fetch("Checkout release tag")
    assert.call(checkout.fetch("uses", nil) == "actions/checkout@#{CHECKOUT_SHA}", "checkout Action SHA changed")
    assert.call(checkout.fetch("with", nil) == {"fetch-depth" => 0, "persist-credentials" => false}, "checkout must fetch full history without persisted credentials")
    setup_go = steps.fetch("Setup Go 1.26.5")
    assert.call(setup_go.fetch("uses", nil) == "actions/setup-go@#{SETUP_GO_SHA}", "setup-go Action SHA changed")
    assert.call(setup_go.fetch("with", nil) == {"go-version" => "1.26.5", "cache" => false}, "Go toolchain inputs changed")
  end

  workflow_regression_step = build_by_name.fetch("Validate release workflow regressions")
  assert.call(!workflow_regression_step.key?("env"), "Release regression gates must not receive a token environment")
  assert.call(workflow_regression_step.fetch("shell", nil) == "bash", "Release regression gates must use bash")
  assert.call(workflow_regression_step.fetch("run", "") == <<~'SH', "Release regression gate commands changed")
    set -euo pipefail

    sh scripts/check-release-workflow_test.sh
    CGO_ENABLED=0 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
  SH
  regression_index = build_steps.index(workflow_regression_step)
  upload_index = build_steps.index(build_by_name.fetch("Upload verified release assets"))
  assert.call(regression_index && upload_index && regression_index < upload_index, "Release regression gates must precede artifact handoff")

  history_run = build_by_name.fetch("Validate release history").fetch("run", "")
  [
    ".created == true and .deleted == false and .forced == false",
    "release tags must be newly created and must never be reused",
    "git rev-parse \"${GITHUB_REF_NAME}^{commit}\"",
    "EXPECTED_COMMIT=$tag_commit",
    "EXPECTED_TAG_OBJECT=$(git rev-parse \"$GITHUB_REF_NAME\")",
    "export EXPECTED_COMMIT EXPECTED_TAG_OBJECT",
    "bash scripts/release-gate.sh",
  ].each do |fragment|
    assert.call(history_run.include?(fragment), "release history validation missing: #{fragment}")
  end
  assert.call(history_run.include?('[[ "$head_commit" != "$tag_commit" ]]'), "release history validation must compare HEAD with the Tag commit")
  assert.call(!history_run.include?("releases?per_page=") && !history_run.include?("gh api --paginate"), "Release list must not be authoritative history")

  generate_run = build_by_name.fetch("Test and generate exact assets").fetch("run", "")
  [
    "CGO_ENABLED=0 go test ./... -count=1",
    "CGO_ENABLED=0 go vet ./...",
    "CGO_ENABLED=0 go run ./cmd/indexgen",
    "-release \"$GITHUB_REF_NAME\"",
    "-source-commit \"$(git rev-parse HEAD)\"",
    "-generated-at \"$(git show -s --format=%cI HEAD)\"",
    "-out dist",
    "assert_exact_assets dist",
    "sha256sum --check checksums.txt",
  ].each do |fragment|
    assert.call(generate_run.include?(fragment), "build asset validation missing: #{fragment}")
  end
  assert.call(generate_run.include?('[[ "$source_commit" != "$tag_commit" ]]'), "generated index source must match the Tag commit")

  upload = build_by_name.fetch("Upload verified release assets")
  assert.call(upload.fetch("uses", nil) == "actions/upload-artifact@#{UPLOAD_ARTIFACT_SHA}", "upload-artifact Action SHA changed")
  assert.call(upload.dig("with", "if-no-files-found") == "error", "artifact upload must fail when an asset is missing")
  assert.call(upload.dig("with", "path").to_s.lines.map(&:strip).reject(&:empty?).sort == [
    "dist/checksums.txt",
    "dist/index.json",
    "dist/node-index-v1alpha1.schema.json",
  ], "artifact upload path must contain exactly three assets")

  download = publish_by_name.fetch("Download verified release assets")
  assert.call(download.fetch("uses", nil) == "actions/download-artifact@#{DOWNLOAD_ARTIFACT_SHA}", "download-artifact Action SHA changed")
  assert.call(download.dig("with", "path") == "dist", "artifact download path changed")

  revalidate_step = publish_by_name.fetch("Regenerate expected release assets")
  revalidate_run = revalidate_step.fetch("run", "")
  assert.call(!revalidate_step.key?("env"), "write token must not be exposed to indexgen during publish revalidation")
  [
    "assert_exact_assets dist",
    "sha256sum --check checksums.txt",
    "CGO_ENABLED=0 go run ./cmd/indexgen",
    "-out expected",
    "cmp --silent",
  ].each do |fragment|
    assert.call(revalidate_run.include?(fragment), "publish revalidation missing: #{fragment}")
  end
  assert.call(revalidate_run.include?('[[ "$source_commit" != "$tag_commit" ]]'), "downloaded index source must match the Tag commit")

  publish_step = publish_by_name.fetch("Publish verified immutable release")
  assert.call(publish_step.fetch("env", nil) == {
    "GITHUB_TOKEN" => "${{ github.token }}",
    "RELEASE_DIST_DIR" => "dist",
    "RELEASE_EXPECTED_DIR" => "expected",
  }, "publish state machine environment changed")
  publish_run = publish_step.fetch("run", "")
  assert.call(publish_run == "bash scripts/release-publish.sh\n", "publish job must execute the tested Release state machine")
  assert.call(!build_steps.any? { |step| step.fetch("run", "").match?(/gh release (create|upload|edit)/) }, "build job must not publish")
  revalidate_index = publish_steps.index(publish_by_name.fetch("Regenerate expected release assets"))
  publish_index = publish_steps.index(publish_by_name.fetch("Publish verified immutable release"))
  assert.call(revalidate_index && publish_index && revalidate_index < publish_index, "publication starts before deterministic regeneration")

  failures
end

failures = validate(workflow, raw_workflow)
unless failures.empty?
  failures.each { |message| warn "release workflow contract violation: #{message}" }
  exit 1
end

generate_run = workflow.fetch("jobs").fetch("build").fetch("steps").find { |step| step.fetch("name", "") == "Test and generate exact assets" }.fetch("run")
asset_checker = generate_run[/(assert_exact_assets\(\) \{.*?\n\})\n\nCGO_ENABLED/m, 1]
abort "unable to extract the exact asset checker" unless asset_checker

Dir.mktmpdir("release-workflow-assets") do |directory|
  assets = File.join(directory, "assets")
  Dir.mkdir(assets)
  expected_names = ["checksums.txt", "index.json", "node-index-v1alpha1.schema.json"]
  expected_names.each { |name| File.write(File.join(assets, name), "fixture\n") }
  run_checker = lambda do
    Open3.capture3(
      "bash", "-c", "#{asset_checker}\nassert_exact_assets \"$1\"", "asset-checker", assets,
    ).last.success?
  end

  abort "exact asset checker rejected the valid fixture" unless run_checker.call
  File.write(File.join(assets, "unexpected.json"), "fixture\n")
  abort "exact asset checker accepted an unexpected asset" if run_checker.call
  File.delete(File.join(assets, "unexpected.json"))
  File.truncate(File.join(assets, "index.json"), 0)
  abort "exact asset checker accepted an empty asset" if run_checker.call
  File.write(File.join(assets, "index.json"), "fixture\n")
  File.delete(File.join(assets, "checksums.txt"))
  abort "exact asset checker accepted a missing asset" if run_checker.call
  File.symlink("index.json", File.join(assets, "checksums.txt"))
  abort "exact asset checker accepted a symbolic-link asset" if run_checker.call
end

def deep_copy(value)
  Marshal.load(Marshal.dump(value))
end

mutations = {
  "wrong trigger" => ->(copy) { copy["on"] = {"pull_request_target" => {}} },
  "global write permission" => ->(copy) { copy["permissions"] = {"contents" => "write"} },
  "default single pending concurrency" => ->(copy) { copy["concurrency"].delete("queue") },
  "publish read permission" => ->(copy) { copy["jobs"]["publish"]["permissions"] = {"contents" => "read"} },
  "mutable Action ref" => ->(copy) { copy["jobs"]["build"]["steps"][0]["uses"] = "actions/checkout@v6" },
  "shallow checkout" => ->(copy) { copy["jobs"]["build"]["steps"][0]["with"]["fetch-depth"] = 1 },
  "missing Release fixture gate" => ->(copy) { copy["jobs"]["build"]["steps"].find { |step| step["name"] == "Validate release workflow regressions" }["run"].sub!("sh scripts/check-release-workflow_test.sh", ":") },
  "missing pinned actionlint gate" => ->(copy) { copy["jobs"]["build"]["steps"].find { |step| step["name"] == "Validate release workflow regressions" }["run"].sub!("CGO_ENABLED=0 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12", ":") },
  "Release gates after artifact handoff" => lambda do |copy|
    steps = copy["jobs"]["build"]["steps"]
    regression = steps.delete_at(steps.index { |step| step["name"] == "Validate release workflow regressions" })
    steps << regression
  end,
  "reused Tag accepted" => ->(copy) { copy["jobs"]["build"]["steps"].find { |step| step["name"] == "Validate release history" }["run"].sub!(".created == true", ".created == false") },
  "missing exact history gate" => ->(copy) { copy["jobs"]["build"]["steps"].find { |step| step["name"] == "Validate release history" }["run"].sub!("bash scripts/release-gate.sh", ":") },
  "missing source commit check" => ->(copy) { copy["jobs"]["build"]["steps"].find { |step| step["name"] == "Test and generate exact assets" }["run"].sub!('[[ "$source_commit" != "$tag_commit" ]]', "false") },
  "missing exact asset check" => ->(copy) { copy["jobs"]["publish"]["steps"][3]["run"].sub!("assert_exact_assets dist", ":") },
}

mutations.each do |name, mutate|
  copy = deep_copy(workflow)
  mutate.call(copy)
  if validate(copy, copy.to_s).empty?
    abort "release workflow checker accepted mutation: #{name}"
  end
end
RUBY

sh "$script_dir/release-history_test.sh"
sh "$script_dir/release-publish_test.sh"

printf 'Release workflow contract tests passed\n'
