---
name: 使用git工作区
description: 开始需要与当前工作区隔离的功能工作，或执行实现计划前使用；在 super-agent-v3 中创建 codex/ 分支 worktree 并保护 dirty 边界。
---

# 使用 Git Worktree

## 概览

Git worktree 会创建共享同一仓库的隔离工作区，使你可以在不切换分支的情况下同时处理多个分支。

**核心原则：** worktree 用来隔离实现，不用来绕过主工作区的脏状态。

**开始时声明：** “我正在使用 使用git工作区 技能来设置隔离工作区。”

## 目录选择流程

按此优先级顺序执行：

### 1. 检查现有目录

```bash
# Check in priority order
ls -d .worktrees 2>/dev/null     # Preferred (hidden)
ls -d worktrees 2>/dev/null      # Alternative
```

**如果找到：** 使用该目录。如果两者都存在，`.worktrees` 优先。

### 2. 检查 CLAUDE.md

```bash
grep -i "worktree.*director" CLAUDE.md 2>/dev/null
```

**如果指定了偏好：** 不询问，直接使用。

### 3. 询问用户

如果没有现有目录，也没有 CLAUDE.md 偏好：

```
没有找到 worktree 目录。你希望我在哪里创建 worktree？

1. .worktrees/（项目本地，隐藏目录）
2. ~/.config/superpowers/worktrees/<project-name>/（全局位置）

你更倾向哪一个？
```

## 安全验证

### 对项目本地目录（.worktrees 或 worktrees）

**创建 worktree 前必须验证目录被忽略：**

```bash
# Check if directory is ignored (respects local, global, and system gitignore)
git check-ignore -q .worktrees 2>/dev/null || git check-ignore -q worktrees 2>/dev/null
```

**如果未被忽略：**

停止并请用户确认是否允许修改 `.gitignore`。不要自动修改 `.gitignore`，更不要自动提交治理文件。

**为什么关键：** 防止意外把 worktree 内容提交到仓库。

### 对全局目录（~/.config/superpowers/worktrees）

不需要 .gitignore 验证：它完全在项目外部。

## 创建步骤

### 1. 检测项目名称

```bash
project=$(basename "$(git rev-parse --show-toplevel)")
```

### 2. 创建 Worktree

```bash
base_branch=$(git branch --show-current)
branch="codex/<short-task-name>"
path=".worktrees/<short-task-name>"

# Create worktree with new branch
git worktree add "$path" -b "$branch" "$base_branch"
cd "$path"
```

Shell 安全要求：

- 不要把 `git worktree add ...` 拼成字符串变量再执行；zsh 不会像 bash 那样默认对普通变量做空白分词，字符串命令容易被当成一个参数。
- 如果必须动态组装命令，使用 shell 数组并以 `"${cmd[@]}"` 执行：

```bash
cmd=(git worktree add "$path" -b "$branch" "$base_branch")
"${cmd[@]}"
```

- 不要用 `eval` 或 zsh 全局分词兼容选项绕过参数边界。

### 3. 运行项目设置

自动检测并运行合适的设置：

```bash
# Node.js
if [ -f package.json ]; then npm install; fi

# Rust
if [ -f Cargo.toml ]; then cargo build; fi

# Python
if [ -f requirements.txt ]; then pip install -r requirements.txt; fi
if [ -f pyproject.toml ]; then poetry install; fi

# Go
if [ -f go.mod ]; then go mod download; fi
```

### 4. 验证干净基线

运行测试，确保 worktree 初始状态干净：

```bash
# Examples - use project-appropriate command
npm test
cargo test
pytest
go test ./...
```

**如果测试失败：** 报告失败，询问是否继续或调查。

**如果测试通过：** 报告已准备好。

### 5. 报告位置

```
Worktree ready at <full-path>
Tests passing (<N> tests, 0 failures)
Ready to implement <feature-name>
```

## 快速参考

| 情况 | 动作 |
|-----------|--------|
| `.worktrees/` 存在 | 使用它（验证被忽略） |
| `worktrees/` 存在 | 使用它（验证被忽略） |
| 两者都存在 | 使用 `.worktrees/` |
| 两者都不存在 | 检查 CLAUDE.md → 询问用户 |
| 目录未被忽略 | 停止并请用户确认 |
| 基线测试失败 | 报告失败 + 询问 |
| 没有 package.json/Cargo.toml | 跳过依赖安装 |

## 常见错误

### 跳过忽略验证

- **问题：** worktree 内容被跟踪，污染 git 状态
- **修复：** 创建项目本地 worktree 前始终使用 `git check-ignore`

### 假设目录位置

- **问题：** 制造不一致，违反项目约定
- **修复：** 遵循优先级：现有 > CLAUDE.md > 询问

### 带着失败测试继续

- **问题：** 无法区分新 bug 和既有问题
- **修复：** 报告失败，取得明确许可后再继续

### 硬编码设置命令

- **问题：** 在使用不同工具的项目上失败
- **修复：** 根据项目文件自动检测（package.json 等）

## 示例工作流

```
You: 我正在使用 使用git工作区 技能来设置隔离工作区。

[Check .worktrees/ - exists]
[Verify ignored - git check-ignore confirms .worktrees/ is ignored]
[Create worktree: git worktree add .worktrees/auth -b feature/auth]
[Run npm install]
[Run npm test - 47 passing]

Worktree ready at /Users/jesse/myproject/.worktrees/auth
Tests passing (47 tests, 0 failures)
Ready to implement auth feature
```

## 红旗

**绝不要：**
- 未验证被忽略就创建 worktree（项目本地）
- 跳过基线测试验证
- 未询问就带着失败测试继续
- 在含糊时假设目录位置
- 跳过 CLAUDE.md 检查

**始终：**
- 遵循目录优先级：现有 > CLAUDE.md > 询问
- 对项目本地目录验证其被忽略
- 自动检测并运行项目设置
- 验证干净测试基线

## 集成

**被以下技能调用：**
- **头脑风暴**（阶段 4）：设计获批并进入实现时必需
- **子代理驱动开发**：执行任何任务前必需
- **执行计划**：执行任何任务前必需
- 任何需要隔离工作区的技能

**配合：**
- **结束开发分支**：工作完成后必需，用于清理
