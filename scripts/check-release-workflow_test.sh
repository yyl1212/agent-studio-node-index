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
    "Validate release history",
    "Test and generate exact assets",
    "Upload verified release assets",
  ]
  required_publish_steps = [
    "Checkout release tag",
    "Setup Go 1.26.5",
    "Download verified release assets",
    "Revalidate release assets",
    "Create draft and upload exact assets",
    "Download draft and compare bytes",
    "Promote immutable stable release",
    "Verify immutable release",
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

  history_run = build_by_name.fetch("Validate release history").fetch("run", "")
  [
    ".created == true and .deleted == false and .forced == false",
    "release tags must be newly created and must never be reused",
    "gh api --include",
    "a Release already exists for $GITHUB_REF_NAME",
    "gh api --paginate",
    "semver.Compare(previous, current) >= 0",
    "git rev-parse \"${previous_tag}^{commit}\"",
    "git merge-base --is-ancestor",
    "git rev-parse \"${GITHUB_REF_NAME}^{commit}\"",
    "releases/tags/$GITHUB_REF_NAME",
  ].each do |fragment|
    assert.call(history_run.include?(fragment), "release history validation missing: #{fragment}")
  end
  assert.call(history_run.include?('[[ "$head_commit" != "$tag_commit" ]]'), "release history validation must compare HEAD with the Tag commit")

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

  revalidate_step = publish_by_name.fetch("Revalidate release assets")
  revalidate_run = revalidate_step.fetch("run", "")
  assert.call(!revalidate_step.key?("env"), "write token must not be exposed to indexgen during publish revalidation")
  [
    "assert_exact_assets dist",
    "sha256sum --check checksums.txt",
    "CGO_ENABLED=0 go run ./cmd/indexgen",
    "-out expected",
    "cmp --silent",
    "a Release appeared before publication",
    'GITHUB_TOKEN="${{ github.token }}" gh api --include',
  ].each do |fragment|
    assert.call(revalidate_run.include?(fragment), "publish revalidation missing: #{fragment}")
  end
  assert.call(revalidate_run.include?('[[ "$source_commit" != "$tag_commit" ]]'), "downloaded index source must match the Tag commit")

  create_run = publish_by_name.fetch("Create draft and upload exact assets").fetch("run", "")
  assert.call(create_run.include?("gh release create") && create_run.include?("--draft"), "release must be created as draft")
  upload_lines = create_run.lines.drop_while { |line| !line.lstrip.start_with?("gh release upload ") }
  upload_command = upload_lines.take_while.with_index { |line, index| index == 0 || upload_lines[index - 1].rstrip.end_with?("\\") }.join.gsub(/\\\s*\n/, " ")
  upload_arguments = Shellwords.shellsplit(upload_command)
  assert.call(upload_arguments == [
    "gh",
    "release",
    "upload",
    "$GITHUB_REF_NAME",
    "dist/checksums.txt",
    "dist/index.json",
    "dist/node-index-v1alpha1.schema.json",
  ], "draft upload must name exactly three assets")
  assert.call(create_run.include?("assert_api_assets"), "draft API assets must be verified before promotion")
  assert.call(!build_steps.any? { |step| step.fetch("run", "").match?(/gh release (create|upload|edit)/) }, "build job must not publish")
  revalidate_index = publish_steps.index(publish_by_name.fetch("Revalidate release assets"))
  create_index = publish_steps.index(publish_by_name.fetch("Create draft and upload exact assets"))
  assert.call(revalidate_index && create_index && revalidate_index < create_index, "publication starts before asset validation")

  compare_run = publish_by_name.fetch("Download draft and compare bytes").fetch("run", "")
  assert.call(compare_run.include?("gh release download") && compare_run.include?("assert_exact_assets") && compare_run.include?("cmp --silent"), "draft assets must be downloaded and byte-compared")

  promote_run = publish_by_name.fetch("Promote immutable stable release").fetch("run", "")
  assert.call(promote_run.include?("gh release edit") && promote_run.include?("--draft=false") && promote_run.include?("--prerelease=false") && promote_run.include?("--latest"), "final promotion flags changed")

  verify_run = publish_by_name.fetch("Verify immutable release").fetch("run", "")
  [
    "repos/$GITHUB_REPOSITORY/releases/tags/$GITHUB_REF_NAME",
    "X-GitHub-Api-Version: 2026-03-10",
    "[.draft,.prerelease,.immutable,.tag_name] | @tsv",
    "false\\tfalse\\ttrue\\t${GITHUB_REF_NAME}",
    "deadline=$((SECONDS + 60))",
    "assert_api_assets",
    "sha256:",
  ].each do |fragment|
    assert.call(verify_run.include?(fragment), "immutable Release poll missing: #{fragment}")
  end

  failures
end

failures = validate(workflow, raw_workflow)
unless failures.empty?
  failures.each { |message| warn "release workflow contract violation: #{message}" }
  exit 1
end

history_run = workflow.fetch("jobs").fetch("build").fetch("steps")[2].fetch("run")
semver_source = history_run[/cat >"\$semver_checker" <<'GO'\n(.*?)\nGO\n/m, 1]
abort "unable to extract the Release SemVer checker" unless semver_source

Dir.mktmpdir("release-workflow-semver") do |directory|
  checker = File.join(directory, "release-semver.go")
  File.write(checker, semver_source)
  environment = {
    "CGO_ENABLED" => "0",
    "GOCACHE" => File.join(directory, "go-cache"),
  }

  stdout, stderr, status = Open3.capture3(
    environment,
    "go", "run", checker, "v0.3.0",
    stdin_data: "v0.1.0\nv0.2.0\nlegacy-release\n",
    chdir: root,
  )
  abort "increasing stable Release fixture failed: #{stderr}" unless status.success? && stdout == "v0.2.0"

  [
    ["v0.2.0", "v0.2.0\n", "equal Release"],
    ["v0.1.0", "v0.2.0\n", "decreasing Release"],
    ["v0.3.0-rc.1", "v0.2.0\n", "prerelease"],
  ].each do |current, previous, description|
    _stdout, _stderr, failed_status = Open3.capture3(
      environment,
      "go", "run", checker, current,
      stdin_data: previous,
      chdir: root,
    )
    abort "Release SemVer checker accepted #{description}" if failed_status.success?
  end
end

generate_run = workflow.fetch("jobs").fetch("build").fetch("steps")[3].fetch("run")
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

verify_run = workflow.fetch("jobs").fetch("publish").fetch("steps")[7].fetch("run")
api_asset_checker = verify_run[/(assert_api_assets\(\) \{.*?\n\})\n\nrelease_json/m, 1]
abort "unable to extract the API asset checker" unless api_asset_checker

Dir.mktmpdir("release-workflow-api-assets") do |directory|
  dist = File.join(directory, "dist")
  Dir.mkdir(dist)
  expected_names = ["checksums.txt", "index.json", "node-index-v1alpha1.schema.json"]
  expected_names.each { |name| File.write(File.join(dist, name), "#{name}\n") }
  valid_assets = expected_names.map do |name|
    path = File.join(dist, name)
    {
      "name" => name,
      "size" => File.size(path),
      "digest" => "sha256:#{Digest::SHA256.file(path).hexdigest}",
    }
  end
  release_json = File.join(directory, "release.json")
  run_checker = lambda do |assets|
    File.write(release_json, JSON.generate({"assets" => assets}))
    Open3.capture3(
      "bash", "-c", "#{api_asset_checker}\nassert_api_assets \"$1\"", "api-asset-checker", release_json,
      chdir: directory,
    ).last.success?
  end

  abort "API asset checker rejected the valid fixture" unless run_checker.call(valid_assets)
  mismatched = Marshal.load(Marshal.dump(valid_assets))
  mismatched[1]["digest"] = "sha256:#{'0' * 64}"
  abort "API asset checker accepted a checksum mismatch" if run_checker.call(mismatched)
  extra = Marshal.load(Marshal.dump(valid_assets)) + [{"name" => "unexpected.json", "size" => 1, "digest" => "sha256:#{'0' * 64}"}]
  abort "API asset checker accepted an unexpected asset" if run_checker.call(extra)
  empty = Marshal.load(Marshal.dump(valid_assets))
  empty[0]["size"] = 0
  abort "API asset checker accepted an empty asset" if run_checker.call(empty)
end

def deep_copy(value)
  Marshal.load(Marshal.dump(value))
end

mutations = {
  "wrong trigger" => ->(copy) { copy["on"] = {"pull_request_target" => {}} },
  "global write permission" => ->(copy) { copy["permissions"] = {"contents" => "write"} },
  "publish read permission" => ->(copy) { copy["jobs"]["publish"]["permissions"] = {"contents" => "read"} },
  "mutable Action ref" => ->(copy) { copy["jobs"]["build"]["steps"][0]["uses"] = "actions/checkout@v6" },
  "shallow checkout" => ->(copy) { copy["jobs"]["build"]["steps"][0]["with"]["fetch-depth"] = 1 },
  "reused Tag accepted" => ->(copy) { copy["jobs"]["build"]["steps"][2]["run"].sub!(".created == true", ".created == false") },
  "existing Release accepted" => ->(copy) { copy["jobs"]["build"]["steps"][2]["run"].sub!("a Release already exists for $GITHUB_REF_NAME", "Release reuse allowed") },
  "missing SemVer ordering" => ->(copy) { copy["jobs"]["build"]["steps"][2]["run"].sub!("semver.Compare(previous, current) >= 0", "false") },
  "missing ancestor check" => ->(copy) { copy["jobs"]["build"]["steps"][2]["run"].sub!("git merge-base --is-ancestor", "git merge-base") },
  "missing source commit check" => ->(copy) { copy["jobs"]["build"]["steps"][3]["run"].sub!('[[ "$source_commit" != "$tag_commit" ]]', "false") },
  "missing exact asset check" => ->(copy) { copy["jobs"]["publish"]["steps"][3]["run"].sub!("assert_exact_assets dist", ":") },
  "unexpected uploaded asset" => ->(copy) do
    run = copy["jobs"]["publish"]["steps"][4]["run"]
    run.sub!("  dist/node-index-v1alpha1.schema.json\n", '  dist/node-index-v1alpha1.schema.json \\' + "\n  dist/unexpected.json\n")
  end,
  "publish before validation" => ->(copy) { copy["jobs"]["publish"]["steps"][3], copy["jobs"]["publish"]["steps"][4] = copy["jobs"]["publish"]["steps"][4], copy["jobs"]["publish"]["steps"][3] },
  "missing immutable poll" => ->(copy) { copy["jobs"]["publish"]["steps"][7]["run"].sub!("false\\tfalse\\ttrue\\t${GITHUB_REF_NAME}", "false\\tfalse\\tfalse\\t${GITHUB_REF_NAME}") },
}

mutations.each do |name, mutate|
  copy = deep_copy(workflow)
  mutate.call(copy)
  if validate(copy, copy.to_s).empty?
    abort "release workflow checker accepted mutation: #{name}"
  end
end
RUBY

printf 'Release workflow contract tests passed\n'
