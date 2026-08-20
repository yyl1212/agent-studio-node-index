# 单维护者分支治理

`main-branch-protection.json` 是供自动化配置 `main` 分支保护的 API payload。普通及外部贡献者必须通过严格的 `Validate submissions` 检查、获得一次 CODEOWNER 批准、在新推送后重新获得批准，并解决全部对话；同时要求线性历史，禁止 force push 和删除分支。

该仓库归个人账户所有。GitHub REST API 要求个人仓库省略仅适用于组织仓库的 `dismissal_restrictions`；payload 因此不包含该字段。省略表示不配置额外的 review dismissal 权限，不改变上述审批和 CODEOWNER 门禁。

仓库当前只有一个 CODEOWNER 和管理员 `yyl1212`。为解除唯一管理员无法批准自己 PR 的死锁，payload 使用 `"enforce_admins": false`。这意味着管理员能够绕过上述分支保护，是单维护者方案明确接受的安全成本，而不是保护等价于双人审核。

管理员绕过前必须手工确认检查和回归结果。PR 验证只信任默认分支提供的只读 `pull_request_target` workflow；`yyl1212` 绝不能点击 **Approve and run workflows** 来批准 fork/外部 PR 提供的 workflow，也不得 source、构建或执行不可信候选 shell/Go 代码。外部 candidate checkout 始终只能作为数据交给默认分支中的可信验证器。

checkout v7 为在 `pull_request_target` 中取得 fork PR merge ref，要求 candidate checkout 使用显式命名为 `allow-unsafe-pr-checkout: true` 的输入。该输入本身不安全，也不降低 fork 内容的信任风险；它只允许出现在独立的 candidate checkout，并必须与 `persist-credentials: false`、只读仓库权限和“外部 candidate 仅作数据、绝不执行”的不变量同时成立。trusted/default-branch checkout 禁止使用此输入。
