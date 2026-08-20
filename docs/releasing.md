# immutable stable Release 发布手册

发布由 `.github/workflows/release.yml` 唯一执行。workflow 只响应新建的 `v*` Tag；日常 PR、分支 push、Tag 删除或 Tag 更新都不能成为发布入口。

## 发布前条件

- `master` 已通过 `Validate submissions`，本地回归、vet 和 workflow fixture 均通过。
- 新版本是完整 stable SemVer，例如 `v0.1.0`；必须严格大于此前所有完整 stable Release。
- 上一个完整 Release 的 Tag commit 是当前 commit 的祖先。
- 仓库已启用 immutable Releases；发布 token 只在 `publish` job 获得 `contents:write`。
- 当前版本不存在任何 draft、prerelease、stable 或 immutable Release。
- 从 Tag push 到最终 immutable 核验结束，远端 annotated Tag 对象及其解引用 commit 都不得被移动或删除；workflow 会在 draft 创建前、转正前和最终核验期间重新读取远端 Tag 并失败关闭。

## 创建 annotated Tag

以下是 `v0.1.0` 的精确命令；发布其他版本时只替换所有 `v0.1.0`：

```bash
git switch master
git pull --ff-only origin master
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin refs/tags/v0.1.0
```

不要使用 lightweight Tag，不要强推 Tag。Tag 一旦推送，即使 Release 仍处于 draft 或 workflow 正在排队，也禁止删除、移动或复用。已经发布的 immutable Tag 同样绝不删除、移动或复用；任何修复都必须使用更高的新 SemVer。

## 工作流架构

```text
新 annotated Tag
      │
      v
固定 repository-wide concurrency group（不取消运行）
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
  重新枚举完整 stable Release、SemVer、祖先和远端 Tag
  创建 draft 并记录精确 Release ID → 上传三资产 → API 校验
  回下载 → 逐字节比较
  再次解析远端 Tag → 转为 stable/latest
  硬墙钟 60 秒内逐轮解析远端 Tag、单次读取 Release API
```

所有 Release workflow 共享固定并发组，后来的 Tag run 不会取消正在发布的 run；但 workflow 不依赖排队顺序保证正确性，`publish` 会在创建 draft 的紧邻门禁中重新读取全部完整 stable Release、严格比较 SemVer、解析前一远端 annotated Tag 并验证祖先关系。

`publish` 在所有只读验证完成前不会创建 Release。draft 只能上传 `checksums.txt`、`index.json` 和 `node-index-v1alpha1.schema.json`。创建响应中的精确 Release ID 会被保留到整个 publish 状态机结束；失败或取消时，只在该 ID 仍是同 Tag、同 target commit、非 immutable draft 且远端 Tag 仍未漂移时才删除，绝不按模糊 Tag 删除，也绝不删除已发布 Release。转正前会回下载三者并逐字节比较；转正后必须在覆盖整个 poll 的硬 60 秒墙钟内观察到字面元组 `false false true <当前 Tag>`，并从每轮唯一一次 Release API 响应验证 ID、target 和三个 `sha256:` digest。

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
- draft 创建后、转正前失败或取消：workflow 会记录精确 Release ID，输出清理诊断，并仅在安全条件仍成立时自动删除该 draft。若日志显示因 ID、状态、target 或远端 Tag 不匹配而拒绝清理，保留现场并人工核对精确 ID；不要按 Tag 模糊删除，也不要清理、移动或重新推送 Tag。
- 转正后任何核验失败：把 Release 视为不可修改的事故证据，停止消费并发布更高修复版本。不可覆盖资产、改写 Tag 或复用版本号。

## 原子性边界

workflow 会在 draft 创建前、promotion 前和 final poll 的成功判定前重新解析远端 annotated Tag，从而拒绝所有可观察到的 Tag 漂移。但 GitHub 没有把“读取 Tag 状态”和“创建/转正 Release”合并成同一个原子 API 事务；真正关闭这两个 API 调用之间的竞态仍依赖仓库级 Tag rules 阻止官方凭据移动或删除已推送的发布 Tag。该外部规则不在本任务中应用，启用前不得把 workflow 检查描述为对已攻陷官方维护者凭据的防护。
