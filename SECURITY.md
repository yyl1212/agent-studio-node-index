# 安全报告

## 私下报告恶意包或漏洞

如果节点包可能包含恶意代码、供应链攻击、凭据窃取、远程代码执行或其他安全问题，请不要创建公开 Issue 或 Pull Request，也不要在公开讨论中披露利用细节。

请在本仓库的 **Security** 页面打开 **Security Advisories**，选择 **Report a vulnerability**，通过私有漏洞报告提交以下信息：

- 节点包名称、版本和索引文件路径；
- 受影响的上游仓库、标签与固定提交；
- 风险说明、复现条件和已知影响；
- 可安全共享的证据以及建议处置方式。

维护者会在私有 Advisory 中确认收到并协调调查、撤回或修正。在维护者确认可以公开前，请保持报告和证据私密。

## 普通元数据修正

不涉及恶意行为或漏洞的普通元数据问题，例如拼写、分类、关键词、生命周期状态，或可公开核对的来源固定信息错误，请按 [CONTRIBUTING.md](CONTRIBUTING.md) 提交 Pull Request。

索引中的“已收录/已审核元数据”不构成节点包代码安全保证。使用者仍需独立评估节点包及其依赖。

## 单维护者与 workflow 信任边界

PR 校验使用默认分支提供的只读 `pull_request_target` 控制面。外部候选 checkout 仅作为数据，由默认分支中的 scope checker、`indexcheck` 和 `indexgen` 处理；外部候选代码不得被 source、构建或执行。只有同仓库且触发者 `github.actor` 恰为 `yyl1212` 时，CI 才允许执行候选 Go 代码。

仓库为避免唯一 CODEOWNER/管理员的自审死锁，不对管理员强制 branch protection。普通及外部贡献者仍需要一次批准和 CODEOWNER 审核，但管理员能够绕过必需检查与审核，这是单维护者模式的已知安全成本。管理员必须在绕过前手工完成等价验证，并且绝不能批准或运行 fork/外部 PR 提供的 workflow；遇到 **Approve and run workflows** 提示时必须拒绝。
