---
name: Git工作树
description: "仅当用户明确点名 `Git工作树` 技能时使用。"
disable_model_invocation: true
aliases: ["@Git工作树", "@使用git工作区", "@git-worktrees"]
---

# Git 工作树

这是 `使用git工作区` 的同名兼容入口。执行时必须遵守：

- 默认目录 `.worktrees/`，默认分支前缀 `codex/`。
- 先跑 `git status --short` 和 `git worktree list --porcelain`。
- 保留 unrelated dirty 文件，不使用 `git add .`、`git reset --hard`、`git checkout --`。
- `.gitignore`、hooks、baseline、policy 等治理文件只有用户明确同意才改。
- 基线验证按 super-agent-v3 的 `./scripts/test_with_guard.sh`、`make guard`、`frontend-app`、`make sqlc-verify`、`make codemap-check` 选择。

详细步骤见 repo-local `使用git工作区` 技能。
