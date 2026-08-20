# 单维护者分支治理

`main-branch-protection.json` 是供自动化配置 `master` 分支保护的 API payload。普通及外部贡献者必须通过严格的 `Validate submissions` 检查、获得一次 CODEOWNER 批准、在新推送后重新获得批准，并解决全部对话；同时要求线性历史，禁止 force push 和删除分支。

仓库当前只有一个 CODEOWNER 和管理员 `yyl1212`。为解除唯一管理员无法批准自己 PR 的死锁，payload 使用 `"enforce_admins": false`。这意味着管理员能够绕过上述分支保护，是单维护者方案明确接受的安全成本，而不是保护等价于双人审核。

管理员绕过前必须手工确认检查和回归结果。PR 验证只信任默认分支提供的只读 `pull_request_target` workflow；`yyl1212` 绝不能点击 **Approve and run workflows** 来批准 fork/外部 PR 提供的 workflow，也不得 source、构建或执行不可信候选 shell/Go 代码。外部 candidate checkout 始终只能作为数据交给默认分支中的可信验证器。
