---
name: 完成开发分支
description: 当用户以 完成开发分支 名称请求 super-agent-v3 分支收尾、提交、推送、PR 或 worktree 清理时使用。
aliases: ["@完成开发分支", "@结束开发分支", "@finish-branch"]
---

# 完成开发分支

这是 `结束开发分支` 的同名兼容入口。收尾必须先验证再行动：

1. `git status --short`、`git diff --stat`，区分 owned 与 unrelated。
2. 按改动面运行 fresh verification：Go 用 `./scripts/test_with_guard.sh`，当前前端用 `frontend-app` 的 lint/test/build；打包/嵌入链路再覆盖 `cmd/agent-terminal/web-dist` 相关守卫。
3. 等用户选择后再提交、推送、创建 PR 或清理 worktree。
4. 永远不要默认 merge 回 main、默认 push、默认删除 worktree，或用 `git add .` 扫入文件。

详细步骤见 repo-local `结束开发分支` 技能。
