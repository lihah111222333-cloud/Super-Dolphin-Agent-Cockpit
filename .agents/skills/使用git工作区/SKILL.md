---
name: 使用git工作区
description: 开始需要与当前工作区隔离的功能工作，或执行实现计划前使用；在 super-agent-v3 中创建 codex/ 分支 worktree 并保护 dirty 边界。
aliases: ["@使用git工作区", "@Git工作树", "@git-worktrees"]
---

# 使用 Git Worktree

## 核心原则

在 super-agent-v3 中，worktree 用来隔离实现，不用来绕过主工作区的脏状态。开始时先说：“我正在使用 使用git工作区 技能来设置隔离工作区。”

## 第 0 步：只读检查

```bash
git status --short
git rev-parse --show-toplevel
git branch --show-current
git worktree list --porcelain
```

- 保留已有 dirty / untracked 文件边界，不要 revert、format、stage 或移动无关文件。
- 如果已经在链接 worktree 中，优先复用当前 worktree，除非用户明确要求再建一个。
- detached HEAD、冲突中、rebase/merge 进行中时，不要自行创建分支；先报告状态。

## 目录与分支

- 本仓库默认使用 `.worktrees/`，它已经在 `.gitignore` 中忽略。
- 新分支默认前缀 `codex/`，例如 `codex/fix-skill-routing-20260626`。
- 只有用户明确要求其他目录或分支名时才改变默认。

## 创建流程

```bash
base_branch=$(git branch --show-current)
branch="codex/<short-task-name>"
path=".worktrees/<short-task-name>"
git worktree add "$path" -b "$branch" "$base_branch"
```

Shell 安全要求：

- 不要把 `git worktree add ...` 拼成字符串变量再执行；zsh 不会像 bash 那样默认对普通变量做空白分词，字符串命令容易被当成一个参数。
- 如果必须动态组装命令，使用 shell 数组并以 `"${cmd[@]}"` 执行：

```bash
cmd=(git worktree add "$path" -b "$branch" "$base_branch")
"${cmd[@]}"
```

- 不要用 `eval` 或 zsh 全局分词兼容选项绕过参数边界。

进入新 worktree 后：

```bash
cd "$path"
git status --short
```

立即准备并验证当前 worktree 自己的 Codex LSP：

```bash
# Unix/macOS 可使用 make codex-worktree-ready；Windows 直接运行以下 Go 命令。
go run ./cmd/codex-worktree-setup ready
go run ./cmd/codex-worktree-setup verify
```

- 两条命令都必须成功；失败时立即停止并报告，不得用其他 checkout 的 binary、config 或无 LSP 模式兜底。
- `ready` 只构建当前 worktree 的 `bin/mcp-lsp` 并更新当前 worktree 被忽略的 `.codex/config.toml`，不得修改全局 `~/.codex/config.toml`。
- 验证成功后必须启动一个新的 Codex task，让 Codex 重新加载 worktree-local MCP server；旧 task 不能作为验收依据。

当前默认不安装 hooks。只有用户或控制器明确要求验证 hook 安装/提交链路时，才在对应 worktree 内运行 `make install-hooks`。

如果 `.worktrees/` 意外未被忽略，停止并请用户确认是否允许修改 `.gitignore`。不要自动修改 `.gitignore`，更不要自动提交治理文件。

## 基线验证

按任务面选择轻量基线，不要无脑跑全量：

| 改动面 | 基线 |
|---|---|
| Go 包 | `./scripts/test_with_guard.sh <affected packages> -count=1` |
| guard/archtest | `./scripts/test_with_guard.sh ./internal/archtest -count=1` 或 `make guard` |
| frontend-app | `cd frontend-app && npm run lint && npm test && npm run build` |
| SQL/store | `make sqlc-verify` |
| docs-only/skills-only | `git diff --check` + 对应技能/文档校验 |

基线失败时报告具体命令和失败摘要，征得用户方向后再继续。

## 红旗

绝不要：

- 未经用户明确同意就在 main/master 上开始实现。
- 使用 `git add .`、`git reset --hard`、`git checkout --` 清理问题。
- 自动修改 `.gitignore`、hooks、baseline 或 policy 文件来“顺手修好”。
- 把 unrelated dirty 文件带进 worktree 任务提交。
- 基线失败还继续声称 worktree ready。

## 收口

完成后交给 `结束开发分支` 技能处理验证、提交、PR 或保留 worktree。不要擅自删除 worktree 或分支。
