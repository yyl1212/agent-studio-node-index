#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

ruby - "$repository_root" <<'RUBY'
require "digest"
require "fileutils"
require "json"
require "open3"
require "tmpdir"

root = ARGV.fetch(0)
publish_script = File.join(root, "scripts", "release-publish.sh")
gate_script = File.join(root, "scripts", "release-gate.sh")
lib_script = File.join(root, "scripts", "release-lib.sh")
case_names = [
  "existing Release blocks draft creation",
  "newer stable Release blocks draft creation",
  "non-ancestor stable Release blocks draft creation",
  "history and assets precede draft creation",
  "post-create failure deletes the exact safe draft ID",
  "post-create cancellation deletes the exact safe draft ID",
  "mismatched target is never deleted",
  "published Release is never deleted",
  "remote Tag drift after history blocks draft creation",
  "remote Tag drift blocks promotion",
  "remote Tag drift after draft recheck blocks promotion",
  "remote Tag drift after final API response blocks verification",
  "hard poll timeout terminates an overrun request",
]
missing = [publish_script, gate_script, lib_script].reject { |path| File.file?(path) }
unless missing.empty?
  case_names.each { |name| warn "not ok - #{name}: release state machine is missing" }
  abort "missing production scripts: #{missing.join(', ')}"
end

EXPECTED_COMMIT = "a" * 40
EXPECTED_TAG_OBJECT = "b" * 40
PREVIOUS_COMMIT = "c" * 40
PREVIOUS_TAG_OBJECT = "d" * 40

FAKE_GH = <<~'FAKE_GH_RUBY'
  #!/usr/bin/env ruby
  require "digest"
  require "fileutils"
  require "json"

  def log(message)
    File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("gh:#{message}") }
  end

  def state
    path = File.join(ENV.fetch("FAKE_STATE_DIR"), "state")
    File.file?(path) ? File.read(path).strip : ""
  end

  def set_state(value)
    File.write(File.join(ENV.fetch("FAKE_STATE_DIR"), "state"), "#{value}\n")
  end

  def release(draft:, immutable:)
    assets = []
    if File.file?(File.join(ENV.fetch("FAKE_STATE_DIR"), "uploaded"))
      dist = ENV.fetch("RELEASE_DIST_DIR")
      assets = [
        [101, "checksums.txt"],
        [102, "index.json"],
        [103, "node-index-v1alpha1.schema.json"],
      ].map do |id, name|
        path = File.join(dist, name)
        {
          "id" => id,
          "name" => name,
          "size" => File.size(path),
          "digest" => "sha256:#{Digest::SHA256.file(path).hexdigest}",
        }
      end
    end
    {
      "id" => 42,
      "tag_name" => ENV.fetch("GITHUB_REF_NAME"),
      "target_commitish" => ENV.fetch("EXPECTED_COMMIT"),
      "draft" => draft,
      "prerelease" => false,
      "immutable" => immutable,
      "assets" => assets,
    }
  end

  args = ARGV
  scenario = ENV.fetch("FAKE_SCENARIO")
  if args[0] == "api"
    method_index = args.index("--method")
    method = method_index ? args.fetch(method_index + 1) : "GET"
    endpoint = args.find { |arg| arg.start_with?("repos/") }
    releases_endpoint = "repos/#{ENV.fetch('GITHUB_REPOSITORY')}/releases"
    if method == "POST" && endpoint == releases_endpoint
      log("create-id:42")
      set_state("draft")
      puts JSON.generate(release(draft: true, immutable: false))
    elsif method == "DELETE" && endpoint == "#{releases_endpoint}/42"
      log("delete-id:42")
      set_state("deleted")
    elsif method == "GET" && endpoint == "#{releases_endpoint}/42"
      log("get-id:42")
      exact_get_count_path = File.join(ENV.fetch("FAKE_STATE_DIR"), "exact-release-gets")
      exact_get_count = File.file?(exact_get_count_path) ? File.read(exact_get_count_path).to_i + 1 : 1
      File.write(exact_get_count_path, "#{exact_get_count}\n")
      if scenario == "promotion_window_drift" && state == "draft" && exact_get_count >= 2
        File.write(File.join(ENV.fetch("FAKE_STATE_DIR"), "promotion-window-drift"), "")
      end
      if scenario == "cleanup_wrong_target"
        value = release(draft: true, immutable: false)
        value["target_commitish"] = "f" * 40
        puts JSON.generate(value)
      elsif scenario == "cleanup_nondraft"
        puts JSON.generate(release(draft: false, immutable: false))
      elsif state == "published"
        puts JSON.generate(release(draft: false, immutable: true))
      else
        puts JSON.generate(release(draft: true, immutable: false))
      end
    elsif method == "GET" && endpoint == "#{releases_endpoint}/tags/#{ENV.fetch('GITHUB_REF_NAME')}"
      if state == "published"
        log("final-release")
        File.write(File.join(ENV.fetch("FAKE_STATE_DIR"), "final-response-drift"), "") if scenario == "final_response_drift"
        sleep 5 if scenario == "timeout"
        puts JSON.generate(release(draft: false, immutable: true))
      elsif scenario == "existing_release"
        log("current-release:200")
        puts JSON.generate(release(draft: false, immutable: true))
      else
        log("current-release:404")
        warn "gh: Not Found (HTTP 404)"
        exit 1
      end
    elsif method == "GET" && endpoint == "#{releases_endpoint}?per_page=100"
      log("list-stable-releases")
      puts(scenario == "newer_release" ? "v0.3.0" : "v0.1.0")
    else
      log("unexpected-api:#{args.join(' ')}")
      warn "unexpected fake gh api call: #{args.join(' ')}"
      exit 2
    end
    exit
  end

  if args[0, 2] == ["release", "upload"]
    log("upload:#{args.fetch(2)}")
    if scenario == "upload_cancel"
      Process.kill("TERM", Process.ppid)
      sleep 0.1
      exit 1
    end
    exit 1 if ["upload_failure", "cleanup_nondraft", "cleanup_wrong_target"].include?(scenario)
    File.write(File.join(ENV.fetch("FAKE_STATE_DIR"), "uploaded"), "")
    exit
  end

  if args[0, 2] == ["release", "download"]
    log("download:#{args.fetch(2)}")
    directory = args.fetch(args.index("--dir") + 1)
    FileUtils.mkdir_p(directory)
    %w[checksums.txt index.json node-index-v1alpha1.schema.json].each do |name|
      FileUtils.cp(File.join(ENV.fetch("RELEASE_DIST_DIR"), name), File.join(directory, name))
    end
    exit
  end

  if args[0, 2] == ["release", "edit"]
    log("edit:#{args.drop(2).join(':')}")
    set_state("published")
    exit
  end

  log("unexpected:#{args.join(' ')}")
  warn "unexpected fake gh call: #{args.join(' ')}"
  exit 2
FAKE_GH_RUBY

FAKE_GIT = <<~'FAKE_GIT_RUBY'
  #!/usr/bin/env ruby
  def log(message)
    File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("git:#{message}") }
  end

  args = ARGV
  scenario = ENV.fetch("FAKE_SCENARIO")
  case args[0]
  when "rev-parse"
    log(args.join(" "))
    case args.fetch(1)
    when "HEAD", "#{ENV.fetch('GITHUB_REF_NAME')}^{commit}"
      puts ENV.fetch("EXPECTED_COMMIT")
    when ENV.fetch("GITHUB_REF_NAME")
      puts ENV.fetch("EXPECTED_TAG_OBJECT")
    else
      puts ENV.fetch("PREVIOUS_COMMIT")
    end
  when "ls-remote"
    if args.join(" ").include?("refs/tags/#{ENV.fetch('GITHUB_REF_NAME')}")
      count_path = File.join(ENV.fetch("FAKE_STATE_DIR"), "current-tag-lookups")
      count = File.file?(count_path) ? File.read(count_path).to_i + 1 : 1
      File.write(count_path, "#{count}\n")
      log("ls-remote-current:#{count}")
      object = ENV.fetch("EXPECTED_TAG_OBJECT")
      commit = ENV.fetch("EXPECTED_COMMIT")
      drift_after_history = scenario == "tag_drift_after_history" && count >= 2
      drift_in_promotion_window = scenario == "promotion_window_drift" && File.file?(File.join(ENV.fetch("FAKE_STATE_DIR"), "promotion-window-drift"))
      drift_after_final_response = scenario == "final_response_drift" && File.file?(File.join(ENV.fetch("FAKE_STATE_DIR"), "final-response-drift"))
      if drift_after_history || drift_in_promotion_window || drift_after_final_response
        object = "e" * 40
        commit = "f" * 40
      end
      puts "#{object}\trefs/tags/#{ENV.fetch('GITHUB_REF_NAME')}"
      puts "#{commit}\trefs/tags/#{ENV.fetch('GITHUB_REF_NAME')}^{}"
    else
      log("ls-remote-previous")
      puts "#{ENV.fetch('PREVIOUS_TAG_OBJECT')}\trefs/tags/v0.1.0"
      puts "#{ENV.fetch('PREVIOUS_COMMIT')}\trefs/tags/v0.1.0^{}"
    end
  when "merge-base"
    log(args.join(" "))
    exit 1 if scenario == "nonancestor"
  else
    log("unexpected:#{args.join(' ')}")
    warn "unexpected fake git call: #{args.join(' ')}"
    exit 2
  end
FAKE_GIT_RUBY

FAKE_GO = <<~'FAKE_GO_RUBY'
  #!/usr/bin/env ruby
  input = $stdin.read
  File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("go:semver:#{input.lines.map(&:strip).join(',')}") }
  if input.lines.map(&:strip).include?("v0.3.0")
    warn "current release must be greater than previous release"
    exit 1
  end
  print "v0.1.0" if input.lines.map(&:strip).include?("v0.1.0")
FAKE_GO_RUBY

FAKE_TIMEOUT = <<~'FAKE_TIMEOUT_RUBY'
  #!/usr/bin/env ruby
  arguments = ARGV.dup
  arguments.shift while arguments.first&.start_with?("--")
  seconds = Float(arguments.shift.delete_suffix("s"))
  started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  File.open(ENV.fetch("FAKE_CALL_LOG"), "a") do |file|
    file.puts("timeout:budget:#{seconds}")
    file.puts("timeout:start:#{started}")
  end
  pid = Process.spawn(*arguments, pgroup: true, out: File::NULL, err: File::NULL)
  deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + seconds
  loop do
    waited = Process.waitpid2(pid, Process::WNOHANG)
    exit waited[1].exitstatus if waited
    if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
      finished = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("timeout:kill:#{finished}") }
      Process.kill("KILL", -pid)
      Process.waitpid(pid)
      exit 124
    end
    sleep 0.02
  end
FAKE_TIMEOUT_RUBY

FAKE_SHA256SUM = <<~'SH'
  #!/bin/sh
  set -eu
  printf 'sha256sum:%s\n' "$*" >> "$FAKE_CALL_LOG"
  exec /sbin/sha256sum "$@"
SH

FAKE_CMP = <<~'SH'
  #!/bin/sh
  set -eu
  printf 'cmp:%s\n' "$*" >> "$FAKE_CALL_LOG"
  exec /usr/bin/cmp "$@"
SH

Result = Struct.new(:status, :stderr, :log, :elapsed, keyword_init: true)

def write_executable(path, content)
  File.write(path, content)
  File.chmod(0o700, path)
end

def setup_fixture(directory)
  paths = %w[bin dist expected runner state].to_h { |name| [name, File.join(directory, name)] }
  paths.each_value { |path| Dir.mkdir(path) }
  index = <<~JSON
    {
      "apiVersion": "agent-studio.dev/v1alpha1",
      "kind": "NodePackageIndex",
      "metadata": {
        "release": "v0.2.0",
        "generatedAt": "2026-08-20T07:30:00Z",
        "sourceCommit": "#{EXPECTED_COMMIT}"
      },
      "packages": []
    }
  JSON
  schema = "{\"schema\":true}\n"
  File.write(File.join(paths["dist"], "index.json"), index)
  File.write(File.join(paths["dist"], "node-index-v1alpha1.schema.json"), schema)
  File.write(File.join(paths["dist"], "checksums.txt"), [
    "#{Digest::SHA256.hexdigest(index)}  index.json",
    "#{Digest::SHA256.hexdigest(schema)}  node-index-v1alpha1.schema.json",
  ].join("\n") + "\n")
  FileUtils.cp_r(Dir[File.join(paths["dist"], "*")], paths["expected"])

  write_executable(File.join(paths["bin"], "gh"), FAKE_GH)
  write_executable(File.join(paths["bin"], "git"), FAKE_GIT)
  write_executable(File.join(paths["bin"], "go"), FAKE_GO)
  write_executable(File.join(paths["bin"], "timeout"), FAKE_TIMEOUT)
  write_executable(File.join(paths["bin"], "sha256sum"), FAKE_SHA256SUM)
  write_executable(File.join(paths["bin"], "cmp"), FAKE_CMP)
  {
    "PATH" => "#{paths['bin']}:#{ENV.fetch('PATH')}",
    "FAKE_CALL_LOG" => File.join(directory, "calls.log"),
    "FAKE_STATE_DIR" => paths["state"],
    "GITHUB_REF_NAME" => "v0.2.0",
    "GITHUB_REPOSITORY" => "example/index",
    "GITHUB_RUN_ID" => "123",
    "GITHUB_RUN_ATTEMPT" => "1",
    "GITHUB_TOKEN" => "test-token",
    "RUNNER_TEMP" => paths["runner"],
    "RELEASE_DIST_DIR" => paths["dist"],
    "RELEASE_EXPECTED_DIR" => paths["expected"],
    "RELEASE_POLL_TIMEOUT_SECONDS" => "2",
    "EXPECTED_COMMIT" => EXPECTED_COMMIT,
    "EXPECTED_TAG_OBJECT" => EXPECTED_TAG_OBJECT,
    "PREVIOUS_COMMIT" => PREVIOUS_COMMIT,
    "PREVIOUS_TAG_OBJECT" => PREVIOUS_TAG_OBJECT,
  }
end

def run_case(publish_script, scenario)
  Dir.mktmpdir("release-publish-behavior") do |directory|
    environment = setup_fixture(directory)
    environment["FAKE_SCENARIO"] = scenario
    yield(environment) if block_given?
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    _stdout, stderr, status = Open3.capture3(environment, "bash", publish_script, chdir: directory)
    elapsed = Process.clock_gettime(Process::CLOCK_MONOTONIC) - started
    log = File.file?(environment.fetch("FAKE_CALL_LOG")) ? File.read(environment.fetch("FAKE_CALL_LOG")) : ""
    return Result.new(status: status, stderr: stderr, log: log, elapsed: elapsed)
  end
end

failures = []
assert = ->(condition, message) { failures << message unless condition }

Dir.mktmpdir("release-lib-behavior") do |directory|
  environment = setup_fixture(directory)
  dist = environment.fetch("RELEASE_DIST_DIR")
  assets = [
    ["checksums.txt", File.join(dist, "checksums.txt")],
    ["index.json", File.join(dist, "index.json")],
    ["node-index-v1alpha1.schema.json", File.join(dist, "node-index-v1alpha1.schema.json")],
  ].map do |name, path|
    {"name" => name, "size" => File.size(path), "digest" => "sha256:#{Digest::SHA256.file(path).hexdigest}"}
  end
  release_json = File.join(directory, "release.json")
  run_api_check = lambda do |fixture_assets|
    File.write(release_json, JSON.generate({"assets" => fixture_assets}))
    Open3.capture3(
      environment,
      "bash", "-c", '. "$1"; assert_release_api_assets "$2" "$3"',
      "release-api-assets", lib_script, release_json, dist,
      chdir: directory,
    ).last.success?
  end
  assert.call(run_api_check.call(assets), "API asset checker rejected a valid exact asset fixture")
  digest_mismatch = Marshal.load(Marshal.dump(assets))
  digest_mismatch[0]["digest"] = "sha256:#{'0' * 64}"
  assert.call(!run_api_check.call(digest_mismatch), "API asset checker accepted a digest mismatch")
  empty = Marshal.load(Marshal.dump(assets))
  empty[1]["size"] = 0
  assert.call(!run_api_check.call(empty), "API asset checker accepted an empty asset")
  assert.call(!run_api_check.call(assets.drop(1)), "API asset checker accepted a missing asset")
  unexpected = Marshal.load(Marshal.dump(assets)) << {"name" => "unexpected.json", "size" => 1, "digest" => "sha256:#{'0' * 64}"}
  assert.call(!run_api_check.call(unexpected), "API asset checker accepted an unexpected asset")

  run_local_check = lambda do
    Open3.capture3(
      environment,
      "bash", "-c", '. "$1"; assert_exact_release_assets "$2"',
      "release-local-assets", lib_script, dist,
      chdir: directory,
    ).last.success?
  end
  assert.call(run_local_check.call, "local asset checker rejected a valid exact fixture")
  File.write(File.join(dist, "unexpected.json"), "unexpected\n")
  assert.call(!run_local_check.call, "local asset checker accepted an unexpected asset")
  File.delete(File.join(dist, "unexpected.json"))
  File.truncate(File.join(dist, "node-index-v1alpha1.schema.json"), 0)
  assert.call(!run_local_check.call, "local asset checker accepted an empty asset")
  File.delete(File.join(dist, "node-index-v1alpha1.schema.json"))
  File.symlink("index.json", File.join(dist, "node-index-v1alpha1.schema.json"))
  assert.call(!run_local_check.call, "local asset checker accepted a symbolic-link asset")
end

existing = run_case(publish_script, "existing_release")
assert.call(!existing.status.success?, "existing Release fixture unexpectedly succeeded")
assert.call(existing.log.include?("gh:current-release:200"), "existing Release was not queried")
assert.call(!existing.log.include?("gh:create-id:"), "existing Release did not block draft creation")

newer = run_case(publish_script, "newer_release")
assert.call(!newer.status.success?, "newer stable Release fixture unexpectedly succeeded")
assert.call(newer.log.include?("gh:list-stable-releases"), "publish did not freshly enumerate stable Releases")
assert.call(!newer.log.include?("gh:create-id:"), "newer stable Release did not block draft creation")

nonancestor = run_case(publish_script, "nonancestor")
assert.call(!nonancestor.status.success?, "non-ancestor fixture unexpectedly succeeded")
assert.call(nonancestor.log.include?("git:merge-base --is-ancestor"), "publish did not check the fresh previous Tag ancestor")
assert.call(!nonancestor.log.include?("gh:create-id:"), "non-ancestor stable Release did not block draft creation")

invalid_assets = run_case(publish_script, "success") do |environment|
  File.delete(File.join(environment.fetch("RELEASE_DIST_DIR"), "node-index-v1alpha1.schema.json"))
end
assert.call(!invalid_assets.status.success?, "missing asset fixture unexpectedly succeeded")
assert.call(!invalid_assets.log.include?("gh:create-id:"), "draft creation occurred before exact asset validation")

ordered = run_case(publish_script, "success") do |environment|
  environment.delete("RELEASE_POLL_TIMEOUT_SECONDS")
end
assert.call(ordered.status.success?, "valid publication fixture failed: #{ordered.stderr}")
calls = ordered.log.lines.map(&:strip)
create_index = calls.index { |line| line.start_with?("gh:create-id:") }
checksum_index = calls.index { |line| line.start_with?("sha256sum:--check") }
compare_index = calls.index { |line| line.start_with?("cmp:") }
history_index = calls.index("gh:list-stable-releases")
assert.call(create_index && checksum_index && checksum_index < create_index, "checksum validation did not precede draft creation")
assert.call(create_index && compare_index && compare_index < create_index, "byte comparison did not precede draft creation")
assert.call(create_index && history_index && history_index < create_index, "fresh history validation did not precede draft creation")
assert.call(!ordered.log.include?("gh:delete-id:"), "successful publication left cleanup armed")
assert.call(ordered.log.include?("timeout:budget:60.0"), "final verification does not default to a hard 60-second wall-clock budget")

cleanup = run_case(publish_script, "upload_failure")
assert.call(!cleanup.status.success?, "post-create failure fixture unexpectedly succeeded")
assert.call(cleanup.status.exitstatus == 1, "cleanup did not preserve the original upload failure status")
assert.call(cleanup.log.include?("gh:create-id:42"), "draft ID was not captured from creation")
assert.call(cleanup.log.include?("gh:get-id:42"), "cleanup did not fetch the exact draft ID")
assert.call(cleanup.log.include?("gh:delete-id:42"), "cleanup did not delete the exact safe draft ID")
assert.call(cleanup.stderr.include?("inspecting exact draft ID 42"), "cleanup did not preserve exact-ID diagnostics")

cancelled = run_case(publish_script, "upload_cancel")
assert.call(!cancelled.status.success?, "post-create cancellation fixture unexpectedly succeeded")
assert.call(cancelled.log.include?("gh:get-id:42"), "cancellation cleanup did not fetch the exact draft ID")
assert.call(cancelled.log.include?("gh:delete-id:42"), "cancellation cleanup did not delete the exact safe draft ID")

wrong_target = run_case(publish_script, "cleanup_wrong_target")
assert.call(!wrong_target.status.success?, "wrong-target cleanup fixture unexpectedly succeeded")
assert.call(wrong_target.log.include?("gh:get-id:42"), "cleanup did not inspect the wrong-target Release")
assert.call(!wrong_target.log.include?("gh:delete-id:"), "cleanup deleted a draft with the wrong target")

nondraft = run_case(publish_script, "cleanup_nondraft")
assert.call(!nondraft.status.success?, "non-draft cleanup fixture unexpectedly succeeded")
assert.call(nondraft.log.include?("gh:get-id:42"), "cleanup did not inspect the exact non-draft Release")
assert.call(!nondraft.log.include?("gh:delete-id:"), "cleanup deleted a published/non-draft Release")

history_drift = run_case(publish_script, "tag_drift_after_history")
assert.call(!history_drift.status.success?, "post-history Tag drift fixture unexpectedly succeeded")
assert.call(!history_drift.log.include?("gh:create-id:"), "remote Tag drift after fresh history did not block draft creation")

promotion_drift = run_case(publish_script, "promotion_window_drift")
assert.call(!promotion_drift.status.success?, "promotion-window Tag drift fixture unexpectedly succeeded")
assert.call(!promotion_drift.log.include?("gh:edit:"), "remote Tag drift after the draft recheck did not block promotion")

final_drift = run_case(publish_script, "final_response_drift")
assert.call(!final_drift.status.success?, "post-response final Tag drift fixture unexpectedly succeeded")
assert.call(final_drift.log.include?("gh:edit:"), "final drift fixture never reached promotion")
assert.call(!final_drift.log.include?("gh:delete-id:"), "final drift cleanup deleted a published Release")

timeout = run_case(publish_script, "timeout")
assert.call(!timeout.status.success?, "timeout fixture unexpectedly succeeded")
assert.call(timeout.log.include?("gh:final-release"), "timeout fixture did not enter the final API request")
timeout_started = timeout.log[/^timeout:start:([0-9.]+)$/, 1]&.to_f
timeout_killed = timeout.log[/^timeout:kill:([0-9.]+)$/, 1]&.to_f
timeout_elapsed = timeout_started && timeout_killed ? timeout_killed - timeout_started : nil
assert.call(timeout_elapsed && timeout_elapsed < 3.0, "hard timeout did not terminate the overrun request within its 2-second test budget")
assert.call(!timeout.log.include?("gh:delete-id:"), "timeout cleanup deleted a published Release")

unless failures.empty?
  failures.each { |message| warn "release publish behavior violation: #{message}" }
  exit 1
end
RUBY

printf 'Release publish behavior tests passed\n'
