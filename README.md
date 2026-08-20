# agent-studio-node-index

Agent Studio 官方精选节点包索引。本仓库通过受信任的默认分支校验投稿，并从新建的 annotated Tag 生成 immutable stable Release。

每个正式 Release 只包含以下三个非空资产：

- `index.json`
- `node-index-v1alpha1.schema.json`
- `checksums.txt`

## 文档

- [投稿、信任边界与首个 PR bootstrap](docs/pr-bootstrap.md)
- [索引格式与资产校验](docs/index-format.md)
- [发布操作手册](docs/releasing.md)

外部投稿只能修改 `packages/*.json`。CI 始终执行默认分支中的 scope checker、`indexcheck` 和 `indexgen`，外部候选 checkout 只作为数据读取，不会 source、构建或执行候选代码。具体治理约束见 [governance/README.md](governance/README.md)。
