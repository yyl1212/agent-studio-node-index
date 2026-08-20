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
history_script = File.join(root, "scripts", "release-history.sh")
case_names = [
  "existing Release blocks draft creation",
  "lagged newer stable Release blocks draft creation",
  "malformed or failed exact history lookup blocks draft creation",
  "preexisting foreign draft conflict is never adopted or deleted",
  "draft creation request has an exact typed API contract",
  "non-ancestor stable Release blocks draft creation",
  "unmerged Tag commit blocks draft creation",
  "missing duplicate malformed or drifting main blocks draft creation",
  "previous annotated Tag drift blocks draft creation",
  "history and assets precede draft creation",
  "post-create failure deletes the exact safe draft ID",
  "post-create cancellation deletes the exact safe draft ID",
  "Release target_commitish main is not treated as Tag proof",
  "mismatched Tag is never deleted",
  "published Release is never deleted",
  "remote Tag drift after history blocks draft creation",
  "remote Tag drift blocks promotion",
  "remote Tag drift after draft recheck blocks promotion",
  "remote Tag drift after final API response blocks verification",
  "hard poll timeout terminates an overrun request",
]
missing = [publish_script, gate_script, lib_script, history_script].reject { |path| File.file?(path) }
unless missing.empty?
  case_names.each { |name| warn "not ok - #{name}: release state machine is missing" }
  abort "missing production scripts: #{missing.join(', ')}"
end

EXPECTED_COMMIT = "a" * 40
EXPECTED_TAG_OBJECT = "b" * 40
PREVIOUS_COMMIT = "c" * 40
PREVIOUS_TAG_OBJECT = "d" * 40
REMOTE_MAIN_COMMIT = "e" * 40

def resolve_host_executable(name, search_path, base_directory)
  search_path.split(File::PATH_SEPARATOR, -1).each do |entry|
    directory = entry.empty? ? base_directory : File.expand_path(entry, base_directory)
    candidate = File.join(directory, name)
    return candidate if File.file?(candidate) && File.executable?(candidate)
  end
  nil
end

HOST_SHA256SUM = resolve_host_executable("sha256sum", ENV.fetch("PATH"), Dir.pwd)
raise "host sha256sum is unavailable" unless HOST_SHA256SUM
HOST_GOCACHE = File.join(Dir.tmpdir, "agent-studio-node-index-go-cache")

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
      # GitHub may report the repository default branch here when the Tag
      # already exists. The annotated Tag object is the commit authority.
      "target_commitish" => "main",
      "draft" => draft,
      "prerelease" => false,
      "immutable" => immutable,
      "assets" => assets,
    }
  end

  args = ARGV
  scenario = ENV.fetch("FAKE_SCENARIO")
  if args[0] == "api"
    unless args.each_cons(2).any? { |left, right| left == "-H" && right == "X-GitHub-Api-Version: 2026-03-10" }
      log("invalid-api-version")
      warn "missing fixed GitHub API version"
      exit 2
    end
    method_index = args.index("--method")
    method = method_index ? args.fetch(method_index + 1) : "GET"
    endpoint = args.find { |arg| arg.start_with?("repos/") }
    releases_endpoint = "repos/#{ENV.fetch('GITHUB_REPOSITORY')}/releases"
    if method == "POST" && (endpoint == releases_endpoint || endpoint&.start_with?("#{releases_endpoint}?"))
      expected_arguments = [
        "api", "--method", "POST", releases_endpoint,
        "-H", "X-GitHub-Api-Version: 2026-03-10",
        "-f", "tag_name=#{ENV.fetch('GITHUB_REF_NAME')}",
        "-f", "name=#{ENV.fetch('GITHUB_REF_NAME')}",
        "-f", "body=Agent Studio 官方精选节点包索引 #{ENV.fetch('GITHUB_REF_NAME')}",
        "-F", "draft=true",
        "-F", "prerelease=false",
        "-F", "generate_release_notes=false",
        "-f", "make_latest=false",
      ]
      unless args == expected_arguments
        log("invalid-create-arguments:#{args.join('|')}")
        warn "invalid exact draft creation request"
        exit 2
      end
      if scenario == "preexisting_draft_conflict"
        log("create-conflict-existing-draft")
        warn "gh: Validation Failed (HTTP 422)"
        exit 1
      end
      log("create-validated-id:42")
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
      if scenario == "cleanup_wrong_tag"
        value = release(draft: true, immutable: false)
        value["tag_name"] = "v9.9.9"
        puts JSON.generate(value)
      elsif scenario == "cleanup_nondraft"
        puts JSON.generate(release(draft: false, immutable: false))
      elsif state == "published"
        puts JSON.generate(release(draft: false, immutable: true))
      else
        puts JSON.generate(release(draft: true, immutable: false))
      end
    elsif method == "GET" && endpoint&.start_with?("#{releases_endpoint}/tags/")
      tag = endpoint.delete_prefix("#{releases_endpoint}/tags/")
      if tag == ENV.fetch("GITHUB_REF_NAME") && state == "published"
        log("final-release")
        File.write(File.join(ENV.fetch("FAKE_STATE_DIR"), "final-response-drift"), "") if scenario == "final_response_drift"
        sleep 5 if scenario == "timeout"
        puts JSON.generate(release(draft: false, immutable: true))
      elsif tag == ENV.fetch("GITHUB_REF_NAME") && scenario == "existing_release"
        log("current-release:200")
        puts JSON.generate(release(draft: false, immutable: true))
      elsif tag == ENV.fetch("GITHUB_REF_NAME")
        log("current-release:404")
        warn "gh: Not Found (HTTP 404)"
        exit 1
      elsif scenario == "malformed_history" && tag == "v0.1.0"
        log("exact-history-malformed:#{tag}")
        puts JSON.generate({
          "id" => 10,
          "tag_name" => tag,
          "target_commitish" => "main",
          "draft" => false,
          "prerelease" => false,
        })
      elsif scenario == "history_error" && tag == "v0.1.0"
        log("exact-history-error:#{tag}")
        warn "gh: server failure (HTTP 500)"
        exit 1
      elsif tag == "v0.1.0" || tag == "v0.3.0"
        log("exact-history-published:#{tag}")
        puts JSON.generate({
          "id" => tag == "v0.3.0" ? 30 : 10,
          "tag_name" => tag,
          "target_commitish" => "main",
          "draft" => false,
          "prerelease" => false,
          "published_at" => "2026-08-20T00:00:00Z",
        })
      else
        log("exact-history-404:#{tag}")
        warn "gh: Not Found (HTTP 404)"
        exit 1
      end
    elsif method == "GET" && endpoint == "#{releases_endpoint}?per_page=100"
      log("stale-list-stable-releases")
      puts "v0.1.0"
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
    exit 1 if ["upload_failure", "cleanup_nondraft", "cleanup_wrong_tag"].include?(scenario)
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
    expected = ["release", "edit", ENV.fetch("GITHUB_REF_NAME"), "--draft=false", "--prerelease=false", "--latest"]
    unless args == expected
      log("invalid-edit:#{args.join('|')}")
      warn "invalid immutable promotion fields"
      exit 2
    end
    log("edit-validated:#{args.drop(2).join(':')}")
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
    revision = args.last
    case revision
    when "HEAD", "#{ENV.fetch('GITHUB_REF_NAME')}^{commit}"
      puts ENV.fetch("EXPECTED_COMMIT")
    when ENV.fetch("GITHUB_REF_NAME")
      puts ENV.fetch("EXPECTED_TAG_OBJECT")
    when "FETCH_HEAD^{commit}"
      puts ENV.fetch("REMOTE_MAIN_COMMIT")
    else
      puts ENV.fetch("PREVIOUS_COMMIT")
    end
  when "fetch"
    if args == ["fetch", "--no-tags", "origin", "refs/heads/main"]
      log("fetch-main")
    else
      log("unexpected-fetch:#{args.join(' ')}")
      warn "unexpected fake git fetch: #{args.join(' ')}"
      exit 2
    end
  when "ls-remote"
    if args == ["ls-remote", "--tags", "origin", "refs/tags/v*"]
      log("enumerate-tags")
      if scenario == "too_many_tags"
        101.times do |index|
          puts "#{ENV.fetch('PREVIOUS_TAG_OBJECT')}\trefs/tags/v1.0.#{index}"
          puts "#{ENV.fetch('PREVIOUS_COMMIT')}\trefs/tags/v1.0.#{index}^{}"
        end
        exit
      end
      puts "#{ENV.fetch('PREVIOUS_TAG_OBJECT')}\trefs/tags/v0.1.0"
      puts "#{ENV.fetch('PREVIOUS_COMMIT')}\trefs/tags/v0.1.0^{}"
      puts "#{ENV.fetch('EXPECTED_TAG_OBJECT')}\trefs/tags/#{ENV.fetch('GITHUB_REF_NAME')}"
      puts "#{ENV.fetch('EXPECTED_COMMIT')}\trefs/tags/#{ENV.fetch('GITHUB_REF_NAME')}^{}"
      if scenario == "lagged_newer_release"
        puts "#{'e' * 40}\trefs/tags/v0.3.0"
        puts "#{'f' * 40}\trefs/tags/v0.3.0^{}"
      end
    elsif args == ["ls-remote", "--heads", "origin", "refs/heads/main"]
      log("ls-remote-main")
      case scenario
      when "main_missing"
        # No matching remote default-branch ref.
      when "main_duplicate"
        puts "#{ENV.fetch('REMOTE_MAIN_COMMIT')}\trefs/heads/main"
        puts "#{'f' * 40}\trefs/heads/main"
      when "main_malformed"
        puts "not-an-object-id\trefs/heads/main"
      when "main_drift"
        puts "#{'f' * 40}\trefs/heads/main"
      else
        puts "#{ENV.fetch('REMOTE_MAIN_COMMIT')}\trefs/heads/main"
      end
    elsif args.join(" ").include?("refs/tags/#{ENV.fetch('GITHUB_REF_NAME')}")
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
      object = scenario == "previous_tag_drift" ? "e" * 40 : ENV.fetch("PREVIOUS_TAG_OBJECT")
      commit = scenario == "previous_tag_drift" ? "f" * 40 : ENV.fetch("PREVIOUS_COMMIT")
      puts "#{object}\trefs/tags/v0.1.0"
      puts "#{commit}\trefs/tags/v0.1.0^{}"
    end
  when "merge-base"
    log(args.join(" "))
    exit 1 if scenario == "nonancestor"
    if scenario == "unmerged_from_main" && args == ["merge-base", "--is-ancestor", ENV.fetch("EXPECTED_COMMIT"), ENV.fetch("REMOTE_MAIN_COMMIT")]
      exit 1
    end
  else
    log("unexpected:#{args.join(' ')}")
    warn "unexpected fake git call: #{args.join(' ')}"
    exit 2
  end
FAKE_GIT_RUBY

FAKE_TIMEOUT = <<~'FAKE_TIMEOUT_RUBY'
  #!/usr/bin/env ruby
  arguments = ARGV.dup
  unless arguments.shift == "--signal=KILL"
    warn "timeout must use --signal=KILL"
    exit 2
  end
  budget = arguments.shift
  unless budget&.match?(/\A[1-9][0-9]*s\z/)
    warn "timeout must use a positive integral second budget"
    exit 2
  end
  seconds = Float(budget.delete_suffix("s"))
  role = case File.basename(arguments.fetch(1, ""))
         when "release-history.sh" then "history"
         when "release-poll.sh" then "poll"
         else
           warn "timeout wrapped an unexpected command: #{arguments.join(' ')}"
           exit 2
         end
  started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  File.open(ENV.fetch("FAKE_CALL_LOG"), "a") do |file|
    file.puts("timeout:#{role}:signal:KILL:budget:#{seconds}")
    file.puts("timeout:#{role}:start:#{started}")
  end
  pid = Process.spawn(*arguments, pgroup: true)
  deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + seconds
  loop do
    waited = Process.waitpid2(pid, Process::WNOHANG)
    exit waited[1].exitstatus if waited
    if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
      finished = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      File.open(ENV.fetch("FAKE_CALL_LOG"), "a") { |file| file.puts("timeout:#{role}:kill:#{finished}") }
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
  exec "$REAL_SHA256SUM" "$@"
SH

FAKE_CMP = <<~'SH'
  #!/bin/sh
  set -eu
  printf 'cmp:%s\n' "$*" >> "$FAKE_CALL_LOG"
  exec /usr/bin/cmp "$@"
SH

Result = Struct.new(:status, :stderr, :log, :elapsed, :state_created, keyword_init: true)

def write_executable(path, content)
  File.write(path, content)
  File.chmod(0o700, path)
end

def setup_fixture(directory, fake_gh = FAKE_GH)
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

  write_executable(File.join(paths["bin"], "gh"), fake_gh)
  write_executable(File.join(paths["bin"], "git"), FAKE_GIT)
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
    "REMOTE_MAIN_COMMIT" => REMOTE_MAIN_COMMIT,
    "REAL_SHA256SUM" => HOST_SHA256SUM,
    "CGO_ENABLED" => "0",
    "GOCACHE" => HOST_GOCACHE,
  }
end

def mutate_script_line(source, label, needle)
  lines = source.lines
  indexes = lines.each_index.select { |index| lines.fetch(index).include?(needle) }
  raise "#{label}: expected exactly one production line, found #{indexes.length}" unless indexes.length == 1

  replacement = Array(yield(lines.fetch(indexes.fetch(0)).chomp)).map { |line| "#{line}\n" }
  lines[indexes.fetch(0), 1] = replacement
  lines.join
end

def materialize_mutated_state_machine(publish_script, directory, mutator)
  source_script_dir = File.dirname(publish_script)
  source_root = File.dirname(source_script_dir)
  target_root = File.join(directory, "mutated-production")
  target_script_dir = File.join(target_root, "scripts")
  FileUtils.mkdir_p(target_script_dir)
  %w[release-publish.sh release-gate.sh release-lib.sh release-history.sh release-poll.sh].each do |name|
    FileUtils.cp(File.join(source_script_dir, name), File.join(target_script_dir, name))
  end
  %w[cmd internal go.mod go.sum].each do |name|
    source_path = File.join(source_root, name)
    FileUtils.ln_s(source_path, File.join(target_root, name)) if File.exist?(source_path)
  end

  target_publish_script = File.join(target_script_dir, "release-publish.sh")
  source = File.read(target_publish_script)
  mutated = mutator.call(source)
  raise "production mutation did not change release-publish.sh" if mutated == source
  File.write(target_publish_script, mutated)
  target_publish_script
end

def run_case(publish_script, scenario, publish_mutator: nil, fake_gh: FAKE_GH)
  Dir.mktmpdir("release-publish-behavior") do |directory|
    environment = setup_fixture(directory, fake_gh)
    environment["FAKE_SCENARIO"] = scenario
    yield(environment) if block_given?
    script_under_test = if publish_mutator
      materialize_mutated_state_machine(publish_script, directory, publish_mutator)
    else
      publish_script
    end
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    repository_root = File.dirname(File.dirname(publish_script))
    _stdout, stderr, status = Open3.capture3(environment, "bash", script_under_test, chdir: repository_root)
    elapsed = Process.clock_gettime(Process::CLOCK_MONOTONIC) - started
    log = File.file?(environment.fetch("FAKE_CALL_LOG")) ? File.read(environment.fetch("FAKE_CALL_LOG")) : ""
    state_created = File.file?(File.join(environment.fetch("FAKE_STATE_DIR"), "state"))
    return Result.new(status: status, stderr: stderr, log: log, elapsed: elapsed, state_created: state_created)
  end
end

failures = []
assert = ->(condition, message) { failures << message unless condition }

Dir.mktmpdir("host-tool-resolution") do |directory|
  tool = File.join(directory, "fixture-tool")
  write_executable(tool, "#!/bin/sh\nexit 0\n")
  assert.call(
    resolve_host_executable("fixture-tool", ".:/not-present", directory) == tool,
    "host tool resolver did not normalize a relative PATH entry",
  )
  assert.call(
    resolve_host_executable("fixture-tool", ":/not-present", directory) == tool,
    "host tool resolver did not interpret an empty PATH entry as the initial directory",
  )
end

Dir.mktmpdir("go-cache-location") do |directory|
  environment = setup_fixture(directory)
  temp_root = "#{File.expand_path(Dir.tmpdir)}#{File::SEPARATOR}"
  assert.call(
    File.expand_path(environment.fetch("GOCACHE")).start_with?(temp_root),
    "release fixture Go cache escaped the host temporary directory",
  )
end

Dir.mktmpdir("sha256sum-wrapper-behavior") do |directory|
  environment = setup_fixture(directory)

  probe = File.join(directory, "sha256sum-probe")
  delegate = File.join(directory, "sha256sum-delegate")
  write_executable(delegate, <<~'SH')
    #!/bin/sh
    set -eu
    : > "$SHA256SUM_PROBE"
    exec "$HOST_SHA256SUM" "$@"
  SH
  environment["REAL_SHA256SUM"] = delegate
  environment["HOST_SHA256SUM"] = HOST_SHA256SUM
  environment["SHA256SUM_PROBE"] = probe
  _stdout, stderr, status = Open3.capture3(
    environment,
    "sha256sum", "--check", "checksums.txt",
    chdir: environment.fetch("RELEASE_DIST_DIR"),
  )
  assert.call(status.success?, "sha256sum wrapper rejected a valid checksum fixture: #{stderr}")
  assert.call(File.file?(probe), "sha256sum wrapper ignored the resolved host tool")
end

invalid_create_requests = {
  "raw draft boolean" => lambda do |source|
    mutate_script_line(source, "raw draft boolean", "-F draft=true") { |line| line.sub("-F draft=true", "-f draft=true") }
  end,
  "raw generated-notes boolean" => lambda do |source|
    mutate_script_line(source, "raw generated-notes boolean", "-F generate_release_notes=false") do |line|
      line.sub("-F generate_release_notes=false", "-f generate_release_notes=false")
    end
  end,
  "typed make-latest string" => lambda do |source|
    mutate_script_line(source, "typed make-latest string", "-f make_latest=false") { |line| line.sub("-f make_latest=false", "-F make_latest=false") }
  end,
  "input body" => lambda do |source|
    mutate_script_line(source, "input body", "-f tag_name=") do |line|
      ['  --input "$RUNNER_TEMP/create-request.json" \\', line]
    end
  end,
  "query migration" => lambda do |source|
    mutate_script_line(source, "query migration", '"repos/$GITHUB_REPOSITORY/releases"') do |line|
      line.sub('/releases"', '/releases?draft=true"')
    end
  end,
  "unknown parameter" => lambda do |source|
    mutate_script_line(source, "unknown parameter", "-f tag_name=") { |line| [line, "  -f unexpected=value \\"] }
  end,
  "reintroduced target_commitish" => lambda do |source|
    mutate_script_line(source, "reintroduced target_commitish", "-f tag_name=") do |line|
      [line, '  -f target_commitish="$EXPECTED_COMMIT" \\']
    end
  end,
  "duplicate field" => lambda do |source|
    mutate_script_line(source, "duplicate field", "-F draft=true") { |line| [line, line] }
  end,
  "changed body field" => lambda do |source|
    mutate_script_line(source, "changed body field", "-f body=") do |line|
      line.sub('body="Agent Studio 官方精选节点包索引 $GITHUB_REF_NAME"', "body=unexpected")
    end
  end,
  "missing generate-release-notes field" => lambda do |source|
    mutate_script_line(source, "missing generate-release-notes field", "-F generate_release_notes=false") { [] }
  end,
}
invalid_create_requests.each do |label, mutator|
  result = run_case(publish_script, "success", publish_mutator: mutator)
  assert.call(!result.status.success?, "strict fake accepted production mutation: #{label}")
  assert.call(result.log.include?("git:enumerate-tags"), "#{label} did not execute the production release state machine")
  assert.call(result.log.include?("gh:invalid-create-arguments:"), "#{label} did not reach rejection by the strict GitHub boundary fake")
  assert.call(!result.state_created, "strict fake created state for production mutation: #{label}")
  assert.call(!result.log.include?("gh:create-validated-id:"), "strict fake validated production mutation: #{label}")
  assert.call(!result.log.include?("gh:upload:") && !result.log.include?("gh:edit-validated:") && !result.log.include?("gh:delete-id:"), "#{label} crossed a forbidden release state transition")
end

weakened_fake = FAKE_GH.sub("unless args == expected_arguments", "unless true")
raise "strict fake weakening canary did not change the fake" if weakened_fake == FAKE_GH
canary = run_case(
  publish_script,
  "success",
  publish_mutator: invalid_create_requests.fetch("raw draft boolean"),
  fake_gh: weakened_fake,
)
assert.call(canary.status.success?, "strict fake weakening canary failed before the mutated POST boundary: #{canary.stderr}")
assert.call(canary.log.include?("gh:create-validated-id:42") && canary.state_created, "strict fake weakening canary did not cross fake state creation")
assert.call(canary.log.include?("gh:upload:") && canary.log.include?("gh:edit-validated:"), "strict fake weakening canary did not prove later release transitions were reachable")

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
assert.call(!existing.log.include?("gh:create-validated-id:"), "existing Release did not block draft creation")

foreign_draft = run_case(publish_script, "preexisting_draft_conflict")
assert.call(!foreign_draft.status.success?, "preexisting foreign draft conflict unexpectedly succeeded")
assert.call(foreign_draft.log.include?("gh:current-release:404"), "foreign draft fixture did not model published by-tag absence")
assert.call(foreign_draft.log.include?("gh:create-conflict-existing-draft"), "draft POST did not expose the atomic foreign draft conflict")
assert.call(!foreign_draft.log.include?("gh:create-validated-id:"), "foreign draft was adopted as this run's draft")
assert.call(!foreign_draft.log.include?("gh:upload:"), "assets were uploaded after a foreign draft conflict")
assert.call(!foreign_draft.log.include?("gh:edit-validated:"), "foreign draft was promoted")
assert.call(!foreign_draft.log.include?("gh:get-id:"), "cleanup inspected a foreign draft without an exact captured ID")
assert.call(!foreign_draft.log.include?("gh:delete-id:"), "cleanup deleted a foreign draft")

newer = run_case(publish_script, "lagged_newer_release")
assert.call(!newer.status.success?, "newer stable Release fixture unexpectedly succeeded")
assert.call(newer.log.include?("git:enumerate-tags"), "publish did not freshly enumerate remote Tags")
assert.call(newer.log.include?("gh:exact-history-published:v0.3.0"), "publish did not query the lagged newer Release by exact Tag")
assert.call(!newer.log.include?("gh:stale-list-stable-releases"), "publish still trusted the lagged Release list")
assert.call(!newer.log.include?("gh:create-validated-id:"), "newer stable Release did not block draft creation")

%w[malformed_history history_error too_many_tags].each do |scenario|
  failed_history = run_case(publish_script, scenario)
  assert.call(!failed_history.status.success?, "#{scenario} fixture unexpectedly succeeded")
  assert.call(!failed_history.log.include?("gh:create-validated-id:"), "#{scenario} did not block draft creation")
end

nonancestor = run_case(publish_script, "nonancestor")
assert.call(!nonancestor.status.success?, "non-ancestor fixture unexpectedly succeeded")
assert.call(nonancestor.log.include?("git:merge-base --is-ancestor"), "publish did not check the fresh previous Tag ancestor")
assert.call(!nonancestor.log.include?("gh:create-validated-id:"), "non-ancestor stable Release did not block draft creation")

unmerged = run_case(publish_script, "unmerged_from_main")
assert.call(!unmerged.status.success?, "Tag commit outside remote main unexpectedly succeeded")
assert.call(unmerged.log.include?("git:fetch-main"), "publish did not freshly fetch remote main")
assert.call(unmerged.log.include?("git:ls-remote-main"), "publish did not strictly resolve refs/heads/main")
assert.call(
  unmerged.log.include?("git:merge-base --is-ancestor #{EXPECTED_COMMIT} #{REMOTE_MAIN_COMMIT}"),
  "publish did not prove the expected commit is reachable from remote main",
)
assert.call(!unmerged.log.include?("gh:create-validated-id:"), "unmerged Tag commit did not block draft creation")

%w[main_missing main_duplicate main_malformed main_drift].each do |scenario|
  invalid_main = run_case(publish_script, scenario)
  assert.call(!invalid_main.status.success?, "#{scenario} fixture unexpectedly succeeded")
  assert.call(invalid_main.log.include?("git:fetch-main"), "#{scenario} did not use a fresh main fetch")
  assert.call(invalid_main.log.include?("git:ls-remote-main"), "#{scenario} did not resolve the exact remote main ref")
  assert.call(!invalid_main.log.include?("gh:create-validated-id:"), "#{scenario} did not block draft creation")
end

previous_drift = run_case(publish_script, "previous_tag_drift")
assert.call(!previous_drift.status.success?, "previous Tag drift fixture unexpectedly succeeded")
assert.call(previous_drift.log.include?("git:ls-remote-previous"), "publish did not freshly resolve the highest previous annotated Tag")
assert.call(!previous_drift.log.include?("gh:create-validated-id:"), "previous annotated Tag drift did not block draft creation")

invalid_assets = run_case(publish_script, "success") do |environment|
  File.delete(File.join(environment.fetch("RELEASE_DIST_DIR"), "node-index-v1alpha1.schema.json"))
end
assert.call(!invalid_assets.status.success?, "missing asset fixture unexpectedly succeeded")
assert.call(!invalid_assets.log.include?("gh:create-validated-id:"), "draft creation occurred before exact asset validation")

ordered = run_case(publish_script, "success") do |environment|
  environment.delete("RELEASE_POLL_TIMEOUT_SECONDS")
end
assert.call(ordered.status.success?, "valid publication fixture failed: #{ordered.stderr}")
calls = ordered.log.lines.map(&:strip)
create_index = calls.index { |line| line.start_with?("gh:create-validated-id:") }
checksum_index = calls.index { |line| line.start_with?("sha256sum:--check") }
compare_index = calls.index { |line| line.start_with?("cmp:") }
history_index = calls.index("gh:exact-history-published:v0.1.0")
main_fetch_index = calls.index("git:fetch-main")
main_ref_index = calls.index("git:ls-remote-main")
main_ancestor_index = calls.index("git:merge-base --is-ancestor #{EXPECTED_COMMIT} #{REMOTE_MAIN_COMMIT}")
assert.call(create_index && checksum_index && checksum_index < create_index, "checksum validation did not precede draft creation")
assert.call(create_index && compare_index && compare_index < create_index, "byte comparison did not precede draft creation")
assert.call(create_index && history_index && history_index < create_index, "fresh history validation did not precede draft creation")
assert.call(create_index && main_fetch_index && main_fetch_index < create_index, "fresh remote main fetch did not precede draft creation")
assert.call(create_index && main_ref_index && main_ref_index < create_index, "strict remote main resolution did not precede draft creation")
assert.call(create_index && main_ancestor_index && main_ancestor_index < create_index, "remote main ancestry proof did not precede draft creation")
assert.call(main_ancestor_index && create_index == main_ancestor_index + 1, "remote main ancestry proof is not the final observable gate before draft creation")
assert.call(!ordered.log.include?("gh:delete-id:"), "successful publication left cleanup armed")
assert.call(ordered.log.include?("gh:create-validated-id:42"), "draft POST fields were not validated by the GitHub boundary fake")
assert.call(ordered.log.include?("gh:edit-validated:"), "promotion fields were not validated by the GitHub boundary fake")
assert.call(ordered.log.include?("timeout:history:signal:KILL:budget:60.0"), "history enumeration lacks the exact hard timeout contract")
assert.call(ordered.log.include?("timeout:poll:signal:KILL:budget:60.0"), "final verification does not default to the exact hard 60-second timeout contract")

cleanup = run_case(publish_script, "upload_failure")
assert.call(!cleanup.status.success?, "post-create failure fixture unexpectedly succeeded")
assert.call(cleanup.status.exitstatus == 1, "cleanup did not preserve the original upload failure status")
assert.call(cleanup.log.include?("gh:create-validated-id:42"), "draft ID was not captured from validated creation")
assert.call(cleanup.log.include?("gh:get-id:42"), "cleanup did not fetch the exact draft ID")
assert.call(cleanup.log.include?("gh:delete-id:42"), "cleanup did not delete the exact safe draft ID")
assert.call(cleanup.stderr.include?("inspecting exact draft ID 42"), "cleanup did not preserve exact-ID diagnostics")

cancelled = run_case(publish_script, "upload_cancel")
assert.call(!cancelled.status.success?, "post-create cancellation fixture unexpectedly succeeded")
assert.call(cancelled.log.include?("gh:get-id:42"), "cancellation cleanup did not fetch the exact draft ID")
assert.call(cancelled.log.include?("gh:delete-id:42"), "cancellation cleanup did not delete the exact safe draft ID")

wrong_tag = run_case(publish_script, "cleanup_wrong_tag")
assert.call(!wrong_tag.status.success?, "wrong-Tag cleanup fixture unexpectedly succeeded")
assert.call(wrong_tag.log.include?("gh:get-id:42"), "cleanup did not inspect the wrong-Tag Release")
assert.call(!wrong_tag.log.include?("gh:delete-id:"), "cleanup deleted a draft with the wrong Tag")

nondraft = run_case(publish_script, "cleanup_nondraft")
assert.call(!nondraft.status.success?, "non-draft cleanup fixture unexpectedly succeeded")
assert.call(nondraft.log.include?("gh:get-id:42"), "cleanup did not inspect the exact non-draft Release")
assert.call(!nondraft.log.include?("gh:delete-id:"), "cleanup deleted a published/non-draft Release")

history_drift = run_case(publish_script, "tag_drift_after_history")
assert.call(!history_drift.status.success?, "post-history Tag drift fixture unexpectedly succeeded")
assert.call(!history_drift.log.include?("gh:create-validated-id:"), "remote Tag drift after fresh history did not block draft creation")

promotion_drift = run_case(publish_script, "promotion_window_drift")
assert.call(!promotion_drift.status.success?, "promotion-window Tag drift fixture unexpectedly succeeded")
assert.call(!promotion_drift.log.include?("gh:edit-validated:"), "remote Tag drift after the draft recheck did not block promotion")

final_drift = run_case(publish_script, "final_response_drift")
assert.call(!final_drift.status.success?, "post-response final Tag drift fixture unexpectedly succeeded")
assert.call(final_drift.log.include?("gh:edit-validated:"), "final drift fixture never reached promotion")
assert.call(!final_drift.log.include?("gh:delete-id:"), "final drift cleanup deleted a published Release")

timeout = run_case(publish_script, "timeout")
assert.call(!timeout.status.success?, "timeout fixture unexpectedly succeeded")
assert.call(timeout.log.include?("gh:final-release"), "timeout fixture did not enter the final API request")
timeout_started = timeout.log[/^timeout:poll:start:([0-9.]+)$/, 1]&.to_f
timeout_killed = timeout.log[/^timeout:poll:kill:([0-9.]+)$/, 1]&.to_f
timeout_elapsed = timeout_started && timeout_killed ? timeout_killed - timeout_started : nil
assert.call(timeout_elapsed && timeout_elapsed < 3.0, "hard timeout did not terminate the overrun request within its 2-second test budget")
assert.call(!timeout.log.include?("gh:delete-id:"), "timeout cleanup deleted a published Release")

unless failures.empty?
  failures.each { |message| warn "release publish behavior violation: #{message}" }
  exit 1
end
RUBY

printf 'Release publish behavior tests passed\n'
