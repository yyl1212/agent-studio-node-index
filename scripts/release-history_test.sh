#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

ruby - "$repository_root" <<'RUBY'
require "fileutils"
require "open3"
require "tmpdir"

root = ARGV.fetch(0)
history_script = File.join(root, "scripts", "release-history.sh")
abort "missing production history script: #{history_script}" unless File.file?(history_script)

CURRENT_COMMIT = "a" * 40
CURRENT_OBJECT = "b" * 40
PREVIOUS_COMMIT = "c" * 40
PREVIOUS_OBJECT = "d" * 40
NEWER_COMMIT = "e" * 40
NEWER_OBJECT = "f" * 40

FAKE_GIT = <<~'RUBY_SCRIPT'
  #!/usr/bin/env ruby
  args = ARGV
  unless args == ["ls-remote", "--tags", "origin", "refs/tags/v*"]
    warn "unexpected git arguments: #{args.join(' ')}"
    exit 2
  end
  File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("git:enumerate-tags") }
  scenario = ENV.fetch("FAKE_SCENARIO")
  if scenario == "too_many_tags"
    101.times do |index|
      tag = "v1.0.#{index}"
      puts "#{ENV.fetch('PREVIOUS_OBJECT')}\trefs/tags/#{tag}"
      puts "#{ENV.fetch('PREVIOUS_COMMIT')}\trefs/tags/#{tag}^{}"
    end
    exit
  end

  puts "#{ENV.fetch('PREVIOUS_OBJECT')}\trefs/tags/v0.1.0"
  puts "#{ENV.fetch('PREVIOUS_COMMIT')}\trefs/tags/v0.1.0^{}"
  if scenario == "ambiguous_tag"
    puts "#{ENV.fetch('NEWER_OBJECT')}\trefs/tags/v0.1.0"
  end
  puts "#{ENV.fetch('CURRENT_OBJECT')}\trefs/tags/v0.2.0"
  puts "#{ENV.fetch('CURRENT_COMMIT')}\trefs/tags/v0.2.0^{}"
  if scenario == "lagged_newer_release"
    puts "#{ENV.fetch('NEWER_OBJECT')}\trefs/tags/v0.3.0"
    puts "#{ENV.fetch('NEWER_COMMIT')}\trefs/tags/v0.3.0^{}"
  end
RUBY_SCRIPT

FAKE_GH = <<~'RUBY_SCRIPT'
  #!/usr/bin/env ruby
  require "json"

  args = ARGV
  unless args[0] == "api" && args.each_cons(2).any? { |left, right| left == "-H" && right == "X-GitHub-Api-Version: 2026-03-10" }
    warn "missing exact API contract: #{args.join(' ')}"
    exit 2
  end
  endpoint = args.find { |arg| arg.start_with?("repos/") }
  repository = ENV.fetch("GITHUB_REPOSITORY")
  prefix = "repos/#{repository}/releases/tags/"
  if endpoint == "repos/#{repository}/releases?per_page=100"
    File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("gh:stale-list") }
    puts "v0.1.0"
    exit
  end
  unless endpoint&.start_with?(prefix)
    warn "unexpected gh endpoint: #{endpoint.inspect}"
    exit 2
  end
  tag = endpoint.delete_prefix(prefix)
  File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("gh:exact-tag:#{tag}") }
  scenario = ENV.fetch("FAKE_SCENARIO")
  if tag == ENV.fetch("GITHUB_REF_NAME")
    if scenario == "existing_current"
      puts JSON.generate({
        "id" => 20,
        "tag_name" => tag,
        "target_commitish" => "main",
        "draft" => false,
        "prerelease" => false,
        "published_at" => "2026-08-20T00:00:00Z",
      })
      exit
    end
    warn "gh: Not Found (HTTP 404)"
    exit 1
  end
  if scenario == "release_error" && tag == "v0.1.0"
    warn "gh: server failure (HTTP 500)"
    exit 1
  end
  if scenario == "no_release" && tag == "v0.1.0"
    warn "gh: Not Found (HTTP 404)"
    exit 1
  end
  if scenario == "missing_published_at" && tag == "v0.1.0"
    puts JSON.generate({
      "id" => 10,
      "tag_name" => tag,
      "target_commitish" => "main",
      "draft" => false,
      "prerelease" => false,
    })
    exit
  end

  commit = tag == "v0.3.0" ? ENV.fetch("NEWER_COMMIT") : ENV.fetch("PREVIOUS_COMMIT")
  draft = scenario == "draft_history" && tag == "v0.1.0"
  prerelease = scenario == "prerelease_history" && tag == "v0.1.0"
  published_at = if scenario == "null_published_at" && tag == "v0.1.0"
                   nil
                 elsif scenario == "empty_published_at" && tag == "v0.1.0"
                   ""
                 else
                   "2026-08-20T00:00:00Z"
                 end
  puts JSON.generate({
    "id" => tag == "v0.3.0" ? 30 : 10,
    "tag_name" => tag,
      "target_commitish" => "main",
    "draft" => draft,
    "prerelease" => prerelease,
    "published_at" => published_at,
  })
RUBY_SCRIPT

Result = Struct.new(:status, :stdout, :stderr, :log, keyword_init: true)

def executable(path, content)
  File.write(path, content)
  File.chmod(0o700, path)
end

def run_case(root, history_script, scenario)
  Dir.mktmpdir("release-history-behavior") do |directory|
    bin = File.join(directory, "bin")
    runner = File.join(directory, "runner")
    Dir.mkdir(bin)
    Dir.mkdir(runner)
    executable(File.join(bin, "git"), FAKE_GIT)
    executable(File.join(bin, "gh"), FAKE_GH)
    log = File.join(directory, "calls.log")
    environment = {
      "PATH" => "#{bin}:#{ENV.fetch('PATH')}",
      "FAKE_CALL_LOG" => log,
      "FAKE_SCENARIO" => scenario,
      "GITHUB_REF_NAME" => "v0.2.0",
      "GITHUB_REPOSITORY" => "example/index",
      "RUNNER_TEMP" => runner,
      "CURRENT_COMMIT" => CURRENT_COMMIT,
      "CURRENT_OBJECT" => CURRENT_OBJECT,
      "PREVIOUS_COMMIT" => PREVIOUS_COMMIT,
      "PREVIOUS_OBJECT" => PREVIOUS_OBJECT,
      "NEWER_COMMIT" => NEWER_COMMIT,
      "NEWER_OBJECT" => NEWER_OBJECT,
      "CGO_ENABLED" => "0",
      "GOCACHE" => File.join(directory, "go-cache"),
    }
    stdout, stderr, status = Open3.capture3(environment, "bash", history_script, chdir: root)
    calls = File.file?(log) ? File.read(log) : ""
    return Result.new(status: status, stdout: stdout, stderr: stderr, log: calls)
  end
end

failures = []
check = ->(condition, message) { failures << message unless condition }

valid = run_case(root, history_script, "success")
check.call(valid.status.success?, "valid exact Tag history failed: #{valid.stderr}")
check.call(valid.stdout == "v0.1.0\t#{PREVIOUS_OBJECT}\t#{PREVIOUS_COMMIT}\n", "highest previous Release output did not preserve the annotated Tag object and commit")
check.call(valid.log.include?("gh:exact-tag:v0.1.0"), "stable Tag was not queried by exact Release endpoint")
check.call(!valid.log.include?("gh:stale-list"), "Release list remained authoritative")

lag = run_case(root, history_script, "lagged_newer_release")
check.call(!lag.status.success?, "replication-lag fixture accepted a lower current Release")
check.call(lag.log.include?("gh:exact-tag:v0.3.0"), "fresh higher stable Tag was not queried exactly")
check.call(!lag.log.include?("gh:stale-list"), "lag fixture consulted the stale Release list")

%w[existing_current draft_history missing_published_at null_published_at empty_published_at release_error ambiguous_tag too_many_tags].each do |scenario|
  result = run_case(root, history_script, scenario)
  check.call(!result.status.success?, "#{scenario} fixture did not fail closed")
end

%w[no_release prerelease_history].each do |scenario|
  result = run_case(root, history_script, scenario)
  check.call(result.status.success?, "#{scenario} fixture failed: #{result.stderr}")
  check.call(result.stdout.empty?, "#{scenario} fixture incorrectly counted a published stable Release")
end

unless failures.empty?
  failures.each { |failure| warn "release history behavior violation: #{failure}" }
  exit 1
end
RUBY

printf 'Release history behavior tests passed\n'
