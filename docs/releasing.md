# immutable stable Release 发布手册

发布由 `.github/workflows/release.yml` 唯一执行。workflow 只响应新建的 `v*` Tag；日常 PR、分支 push、Tag 删除或 Tag 更新都不能成为发布入口。

## 发布前条件

- `master` 已通过 `Validate submissions`，本地回归、vet 和 workflow fixture 均通过。
- 新版本是完整 stable SemVer，例如 `v0.1.0`；必须严格大于此前所有完整 stable Release。
- 上一个完整 Release 的 Tag commit 是当前 commit 的祖先。
- 仓库已启用 immutable Releases；发布 token 只在 `publish` job 获得 `contents:write`。
- 当前版本不存在任何 draft、prerelease、stable 或 immutable Release。

## 创建 annotated Tag

以下是 `v0.1.0` 的精确命令；发布其他版本时只替换所有 `v0.1.0`：

```bash
git switch master
git pull --ff-only origin master
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin refs/tags/v0.1.0
```

不要使用 lightweight Tag，不要强推 Tag。已经发布的 immutable Tag 绝不删除、移动或复用；任何修复都必须使用更高的新 SemVer。

## 工作流架构

```text
新 annotated Tag
      │
      v
build（contents:read）
  历史/SemVer/祖先/Tag commit 校验
  test + vet + cmd/indexgen
  精确资产/checksum/source 校验
      │ Actions artifact
      v
publish（contents:write）
  下载并用 cmd/indexgen 重建、逐字节复核
  创建 draft → 上传三资产 → API 校验
  回下载 → 逐字节比较
  转为 stable/latest → 60 秒 immutable 轮询
```

`publish` 在所有只读验证完成前不会创建 Release。draft 只能上传 `checksums.txt`、`index.json` 和 `node-index-v1alpha1.schema.json`。转正前会回下载三者并逐字节比较；转正后必须在 60 秒内观察到字面元组 `false false true <当前 Tag>`，并验证 API 中三个 `sha256:` digest 与本地文件一致。

## 发布后核验

```bash
tag=v0.1.0
gh api "repos/yyl1212/agent-studio-node-index/releases/tags/$tag" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  --jq '[.draft,.prerelease,.immutable,.tag_name] | @tsv'

download_dir=$(mktemp -d)
gh release download "$tag" --dir "$download_dir"
(cd "$download_dir" && sha256sum --check checksums.txt)
find "$download_dir" -mindepth 1 -maxdepth 1 -type f -print | LC_ALL=C sort
```

预期 API 元组为 `false<TAB>false<TAB>true<TAB>v0.1.0`，下载目录只有三个非空资产。

## 失败处理

- 在 draft 创建前失败：修复代码后发布更高版本；不要移动已推送的 Tag。
- draft 创建后、转正前失败：先保留 draft 取证。确认根因和资产后，可删除该失败 draft（不要清理 Tag），再从 GitHub Actions 重跑原失败 job；不得通过删除并重新推送 Tag 来触发。
- 转正后任何核验失败：把 Release 视为不可修改的事故证据，停止消费并发布更高修复版本。不可覆盖资产、改写 Tag 或复用版本号。
