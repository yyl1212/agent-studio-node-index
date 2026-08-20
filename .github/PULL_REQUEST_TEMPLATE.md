## 提交内容

- 节点包名称：
- 节点包版本：
- 上游仓库：
- 固定标签：
- 固定提交：

## 提交前检查

- [ ] 本 PR 只新增或修改 `packages/*.json`，没有删除文件。
- [ ] 文件名为 `sha256(name + "\n" + version).json` 的小写十六进制结果。
- [ ] `source.tag` 固定到 `source.commit`，并填写了固定清单原始字节的 `source.manifestDigest`。
- [ ] `manifest` 是该固定提交中 `agent-studio.node-package.json` 的字段值副本。
- [ ] `name` 与固定提交中 `go.mod` 的 module path 一致。

## 审核边界

维护者批准仅表示“已收录/已审核元数据”，不构成对节点包代码、依赖或运行行为的安全保证。
