# 贡献节点包元数据

本仓库收录的是节点包的固定元数据，不接收节点包实现代码。外部贡献者的 Pull Request 只能新增或修改 `packages/` 目录直属的 `.json` 文件，不得删除文件；代码、Schema、工作流和治理文件由维护者单独变更。

## 1. 使用精确哈希文件名

提交文件名必须按下面的字节序列计算：

```text
sha256(name + "\n" + version).json
```

其中 `name` 和 `version` 使用提交 JSON 中的原始字符串，二者之间是一个 LF（`0x0a`），末尾没有额外换行。文件名使用 64 位小写十六进制摘要。例如，不要使用显示名称、标签或清单摘要来计算文件名。

## 2. 固定并核对来源

`source` 必须把一次发布固定到可复核的 GitHub 内容：

- `repository` 是与 Go module path 对应的规范 GitHub 仓库 URL；
- `moduleDir` 指向仓库内包含 `go.mod` 与 `agent-studio.node-package.json` 的目录；
- `tag` 是发布标签，且必须直接或经一层 annotated tag 解析到 `commit`；
- `commit` 是小写的完整 Git 对象 ID，不能使用分支名或短哈希；
- `manifestDigest` 是上述固定提交中 `agent-studio.node-package.json` 原始字节的 SHA-256，格式为 `sha256:<64 位小写十六进制>`。

固定提交内 `go.mod` 的 module path 必须等于提交的 `name`，版本还必须符合该 module path 的 Go major-version 规则。

## 3. 复制固定清单

提交中的 `manifest` 是固定提交内 `agent-studio.node-package.json` 的类型化字段值副本。请复制全部字段和值，不要在索引提交中改写描述、兼容范围、注册包或节点列表。CI 会同时核对原始清单摘要、清单字段值和 `go.mod`。

## 4. 提交与审核

1. 从最新 `master` 创建分支。
2. 仅提交本次需要的 `packages/*.json` 文件；每个版本使用一个精确哈希文件名。
3. 填写 Pull Request 模板并等待 `Validate submissions` 通过。
4. 由 CODEOWNER 审核；后续推送会使旧批准失效，最后一次推送也需要批准，并须解决全部对话。

维护者批准的含义仅为“已收录/已审核元数据”：它表示来源固定信息、清单副本和索引字段已经过自动校验与人工审阅。它不表示节点包代码已经过安全审计，也不构成对代码、依赖、许可证、可用性或运行行为的安全保证。使用者仍需根据自己的威胁模型审查上游仓库及其依赖。

疑似恶意节点包或安全漏洞不要在公开 Pull Request 中披露，请按 [SECURITY.md](SECURITY.md) 私下报告。普通的元数据拼写、分类、生命周期或来源固定信息修正可以提交 Pull Request。
