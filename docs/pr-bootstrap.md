# 投稿、信任边界与首个 PR bootstrap

## 投稿边界

外部投稿者只能新增或修改 `packages/*.json`，不能删除文件，也不能修改 workflow、生成器、schema、脚本或治理配置。PR 校验使用默认分支的只读 `pull_request_target` 控制面：

```text
默认分支受信任代码 ──读取──> 外部候选 checkout（仅数据）
        │
        ├── scripts/check-pr-scope.sh
        ├── cmd/indexcheck
        └── cmd/indexgen（两次生成并逐字节比较）
```

外部候选代码不得被 source、构建或执行。只有“同仓库分支 + 触发者恰为维护者 `yyl1212`”同时成立时，CI 才进入维护者候选路径；该路径不向候选步骤注入 token。分支保护仍要求固定的 `Validate submissions` 检查、CODEOWNERS 审查、会话解决和线性历史。单维护者仓库仅保留管理员 bootstrap 通道，不能把该通道扩展为外部投稿绕过。

## 空索引 bootstrap

仓库可以从空的 `packages/` 目录启动。此时 `cmd/indexgen` 仍必须生成完整的三个资产，且 `index.json` 中的 `packages` 为 `[]`。在准备首个受保护 PR 前，本地执行：

```bash
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go run ./cmd/indexcheck -root .
sh scripts/check-pr-scope_test.sh
sh scripts/check-release-workflow_test.sh
CGO_ENABLED=0 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

source_commit=$(git rev-parse HEAD)
generated_at=$(git show -s --format=%cI HEAD)
out_dir=$(mktemp -d)/dist
CGO_ENABLED=0 go run ./cmd/indexgen \
  -root . \
  -release v0.0.1 \
  -source-commit "$source_commit" \
  -generated-at "$generated_at" \
  -out "$out_dir"
expected=$(printf '%s\n' checksums.txt index.json node-index-v1alpha1.schema.json)
actual=$(find "$out_dir" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)
test "$actual" = "$expected"
printf '%s\n' "$actual"
(cd "$out_dir" && sha256sum --check checksums.txt)
```

输出必须精确为：

```text
checksums.txt
index.json
node-index-v1alpha1.schema.json
```

首个 PR 合并前，如果默认分支尚未包含 CI 或分支保护尚未应用，应先人工审查同一组命令的完整输出，再由单一维护者使用管理员 bootstrap 通道合并。随后立即由治理任务应用分支保护；不要为了通过首个 PR 而让不受信任候选代码进入可执行路径。

## 普通投稿检查

投稿文件名是 `name + LF + version` 的 SHA-256，并位于 `packages/<sha256>.json`；LF 是单个 `0x0a` 字节，末尾没有额外换行。提交前至少运行：

```bash
CGO_ENABLED=0 go run ./cmd/indexcheck -root . -changed-file packages/<sha256>.json
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go vet ./...
```

CI 还会联网确认 source Tag、commit 和 manifest digest；本地成功不替代默认分支的 `Validate submissions` 检查。
