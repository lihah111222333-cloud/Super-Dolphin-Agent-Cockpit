---
name: 结束开发分支
description: 当实现已完成、验证已跑完，并且需要决定如何在 super-agent-v3 中提交、保留、推送或清理 worktree 时使用。
aliases: ["@结束开发分支", "@完成开发分支", "@finish-branch"]
---

# 完成开发分支

## 核心原则

证据先于选项。开始时声明：“我正在使用 结束开发分支 技能来完成这项工作。”

## 第 1 步：读取状态

```bash
git status --short
git diff --stat
git branch --show-current
git rev-parse --show-toplevel
```

- 未跟踪或未暂存文件要区分 owned / unrelated。
- 不要 `git add .`。
- 不要清理、stash、revert 或格式化 unrelated 文件。

## 第 2 步：验证

按实际改动面选择验证：

| 改动面 | 命令 |
|---|---|
| Go 文件 | `./scripts/test_with_guard.sh <file.go>`，再跑受影响包 `./scripts/test_with_guard.sh <packages> -count=1` |
| guard/archtest | `./scripts/test_with_guard.sh ./internal/archtest -count=1` 或 `make guard` |
| frontend-app | `cd frontend-app && npm run lint && npm test && npm run build` |
| SQL/store | `make sqlc-verify` |
| codemap | `make codemap-check` |
| docs/skills-only | `git diff --check` + 对应文本校验脚本 |

测试失败时停止，不呈现合并/PR 选项。报告失败命令和摘要。

## 第 3 步：呈现选项

验证后只呈现这些选项，等待用户选择：

```text
验证已完成。你想怎么处理？

1. 只保留当前分支/worktree
2. 提交 owned 文件到当前分支
3. 推送当前分支
4. 创建 PR
5. 清理 worktree/分支

请选择一个选项。
```

不要默认 merge 回 main。只有用户明确要求时才本地合并。

## 第 4 步：执行用户选择

### 提交

```bash
git add <owned-file-1> <owned-file-2>
git diff --cached --check
git status --short
git commit -m "<type>: <summary>"
```

fix/hotfix/bugfix/修复 类提交必须包含同提交回归测试、fixture、golden、snapshot 或可执行验收脚本。

### 推送

```bash
git status --short
git push -u origin "$(git branch --show-current)"
```

推送前必须确认工作区/index/untracked 状态符合 hook 要求。

### 创建 PR

```bash
gh pr create --title "<title>" --body "$(cat <<'EOF'
## Summary
- <what changed>

## Test Plan
- <fresh verification command>
EOF
)"
```

### 清理

清理 worktree 或强删分支属于破坏性动作，必须再次确认目标路径、分支名和要丢弃的提交。没有明确确认，不执行。

## 红旗

绝不要：

- 没有新鲜验证就说 ready/done/fixed。
- 把 unrelated dirty 文件 stage 进提交。
- 默认本地 merge、push、删除 worktree 或删除分支。
- 使用 `--no-verify`，除非用户明确要求紧急旁路并补跑遗漏检查。
- 用 `git reset --hard` 或 `git checkout --` 清理用户改动。
