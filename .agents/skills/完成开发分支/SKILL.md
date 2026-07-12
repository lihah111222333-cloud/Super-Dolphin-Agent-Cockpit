---
name: 完成开发分支
description: "仅当用户明确点名 `完成开发分支` 技能时使用。"
disable_model_invocation: true
aliases: ["@完成开发分支", "@结束开发分支", "@finish-branch"]
---

# 完成开发分支

这是 `结束开发分支` 的同名兼容入口。收尾必须先验证再行动：

1. `git status --short`、`git diff --stat`，区分 owned 与 unrelated。
2. 按改动面运行 fresh verification：Go 用 `./scripts/test_with_guard.sh`，当前前端用 `frontend-app` 的 lint/test/build。
3. 等用户选择后再提交、推送、创建 PR 或清理 worktree。
4. 永远不要默认 merge 回 main、默认 push、默认删除 worktree，或用 `git add .` 扫入文件。

详细步骤见 repo-local `结束开发分支` 技能。
