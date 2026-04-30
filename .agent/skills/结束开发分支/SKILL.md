---
name: 结束开发分支
description: 当实现已完成、所有测试通过，并且需要决定如何集成工作时使用；通过为合并、PR 或清理提供结构化选项来指导开发收尾
aliases: ["@结束开发分支", "@完成开发分支", "@finish-branch"]
---

# 完成开发分支

## 概览

通过呈现清晰选项并处理所选工作流，指导开发工作的完成。

**核心原则：** 验证测试 → 呈现选项 → 执行选择 → 清理。

**开始时声明：** “我正在使用 结束开发分支 技能来完成这项工作。”

## 流程

### 第 1 步：验证测试

**呈现选项前，先验证测试通过：**

```bash
# Run project's test suite
npm test / cargo test / pytest / go test ./...
```

**如果测试失败：**
```
测试失败（<N> 个失败）。完成前必须先修复：

[显示失败内容]

测试通过前，不能继续合并或创建 PR。
```

停止。不要进入第 2 步。

**如果测试通过：** 继续第 2 步。

### 第 2 步：确定基准分支

```bash
# Try common base branches
git merge-base HEAD main 2>/dev/null || git merge-base HEAD master 2>/dev/null
```

或者询问：“这个分支是从 main 分出来的，对吗？”

### 第 3 步：呈现选项

准确呈现下面 4 个选项：

```
实现已完成。你想怎么处理？

1. 本地合并回 <base-branch>
2. 推送并创建 Pull Request
3. 保持分支现状（我之后处理）
4. 丢弃这项工作

请选择一个选项。
```

**不要添加解释**：保持选项简洁。

### 第 4 步：执行选择

#### 选项 1：本地合并

```bash
# Switch to base branch
git checkout <base-branch>

# Pull latest
git pull

# Merge feature branch
git merge <feature-branch>

# Verify tests on merged result
<test command>

# If tests pass
git branch -d <feature-branch>
```

然后：清理 worktree（第 5 步）

#### 选项 2：推送并创建 PR

```bash
# Push branch
git push -u origin <feature-branch>

# Create PR
gh pr create --title "<title>" --body "$(cat <<'EOF'
## Summary
<2-3 bullets of what changed>

## Test Plan
- [ ] <verification steps>
EOF
)"
```

然后：清理 worktree（第 5 步）

#### 选项 3：保持现状

报告：“保留分支 <name>。Worktree 已保留在 <path>。”

**不要清理 worktree。**

#### 选项 4：丢弃

**先确认：**
```
这会永久删除：
- 分支 <name>
- 所有提交：<commit-list>
- 位于 <path> 的 worktree

输入 'discard' 以确认。
```

等待精确确认。

如果确认：
```bash
git checkout <base-branch>
git branch -D <feature-branch>
```

然后：清理 worktree（第 5 步）

### 第 5 步：清理 Worktree

**适用于选项 1、2、4：**

检查是否在 worktree 中：
```bash
git worktree list | grep $(git branch --show-current)
```

如果是：
```bash
git worktree remove <worktree-path>
```

**选项 3：** 保留 worktree。

## 快速参考

| 选项 | 合并 | 推送 | 保留 Worktree | 清理分支 |
|--------|-------|------|---------------|----------------|
| 1. 本地合并 | ✓ | - | - | ✓ |
| 2. 创建 PR | - | ✓ | ✓ | - |
| 3. 保持现状 | - | - | ✓ | - |
| 4. 丢弃 | - | - | - | ✓（强制） |

## 常见错误

**跳过测试验证**
- **问题：** 合并损坏代码，创建失败 PR
- **修复：** 提供选项前始终验证测试

**开放式提问**
- **问题：** “接下来我该做什么？”→ 含糊
- **修复：** 准确呈现 4 个结构化选项

**自动清理 worktree**
- **问题：** 在可能还需要时删除 worktree（选项 2、3）
- **修复：** 只为选项 1 和 4 清理

**丢弃前不确认**
- **问题：** 意外删除工作
- **修复：** 要求输入 "discard" 确认

## 红旗

**绝不要：**
- 在测试失败时继续
- 不验证合并结果就合并
- 不确认就删除工作
- 未明确请求就 force-push

**始终：**
- 提供选项前验证测试
- 准确呈现 4 个选项
- 对选项 4 获取输入确认
- 只为选项 1 和 4 清理 worktree

## 集成

**被以下技能调用：**
- **子代理驱动开发**（第 7 步）：所有任务完成后
- **执行计划**（第 5 步）：所有批次完成后

**配合：**
- **使用git工作区**：清理该技能创建的 worktree
