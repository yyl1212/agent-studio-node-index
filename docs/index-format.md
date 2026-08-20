# 索引格式与资产校验

正式 Release 是节点包索引的唯一稳定分发边界。消费者必须把 Release Tag、`index.json` 元数据、资产集合和 SHA-256 摘要作为一个整体校验，不能只信任下载 URL 或单个文件名。

## 精确资产集合

每个 Release 必须且只能包含三个非空普通文件：

| 资产 | 用途 |
| --- | --- |
| `index.json` | 按 `agent-studio.dev/v1alpha1` 契约生成的精选节点包索引 |
| `node-index-v1alpha1.schema.json` | 与该索引配套的 JSON Schema |
| `checksums.txt` | 前两个资产的 SHA-256 校验清单 |

缺少资产、额外资产、空文件、符号链接或 checksum 不匹配都应视为无效 Release。

## `index.json` 顶层结构

```json
{
  "apiVersion": "agent-studio.dev/v1alpha1",
  "kind": "NodePackageIndex",
  "metadata": {
    "release": "v0.1.0",
    "generatedAt": "2026-08-20T07:30:00Z",
    "sourceCommit": "0123456789abcdef0123456789abcdef01234567"
  },
  "packages": []
}
```

- `metadata.release` 必须是完整、无 prerelease/build 后缀的稳定 Go SemVer，并与当前 Release Tag 完全相等。
- `metadata.generatedAt` 来自 Tag commit 的提交时间，统一编码为 UTC 秒精度。
- `metadata.sourceCommit` 必须是小写 40 或 64 位 Git OID，并与 annotated Tag 解引用后的 commit 完全相等。
- `packages` 由 `packages/*.json` 确定性生成；空仓 bootstrap 时必须是 `[]`，不能是 `null`。

包、版本、source、review、lifecycle 和 manifest 的详细字段约束以 Release 内的 `node-index-v1alpha1.schema.json` 为准。生成器还会执行严格 JSON 解码、规范化排序、重复项拒绝和大小预算检查。

## 下载后验证

在只含三个下载资产的目录执行：

```bash
expected=$(printf '%s\n' checksums.txt index.json node-index-v1alpha1.schema.json)
actual=$(find . -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)
test "$actual" = "$expected"
test -s index.json
test -s node-index-v1alpha1.schema.json
test -s checksums.txt
sha256sum --check checksums.txt
```

然后使用 GitHub Releases API 核对：

- `.draft == false`
- `.prerelease == false`
- `.immutable == true`
- `.tag_name` 等于期望 Tag
- API 资产名排序后精确等于上述三个名称
- 每个资产 `.size > 0`
- 每个 `.digest` 以 `sha256:` 开头且等于本地文件摘要

Release workflow 在 draft 阶段上传后会再次下载并逐字节比较；只有所有检查通过才会转正。
