---
name: 编写计划
description: "仅当用户明确点名 `编写计划` 技能时使用。"
disable_model_invocation: true
aliases: ["@编写计划", "@writing-plans"]
---

# 编写计划

## 概览

编写全面的实现计划，假设执行工程师对我们的代码库完全没有上下文，而且品味可疑。记录他们需要知道的一切：每个任务要改哪些文件、代码、测试、可能需要查看的文档、如何测试。把整个计划拆成小任务。DRY。YAGNI。TDD。频繁提交。

假设他们是有技能的开发者，但几乎不了解我们的工具链或问题领域。假设他们不太懂好的测试设计。

**开始时声明：** “我正在使用 编写计划 技能来创建实现计划。”

**上下文：** 这应在专用 worktree 中运行（由 头脑风暴 或 使用git工作区 技能创建）。在 super-agent-v3 中，默认分支名前缀为 `codex/`。

**计划保存到：** `docs/plans/YYYY-MM-DD-<feature-name>.md`
- 用户偏好的计划位置优先于这个默认位置

## 范围检查

如果规格覆盖多个独立子系统，本应在 头脑风暴 期间拆成子项目规格。如果没有，建议拆成独立计划：每个子系统一个。每个计划都应该能独立产出可工作、可测试的软件。

## 文件结构

定义任务前，先绘制将要创建或修改哪些文件，以及每个文件负责什么。这里会锁定分解决策。

- 设计边界清晰、接口明确的单元。每个文件应有一个明确职责。
- 你最擅长推理能一次放进上下文的代码；文件聚焦时编辑也更可靠。优先使用更小、更聚焦的文件，而不是承担过多职责的大文件。
- 会一起变更的文件应放在一起。按职责拆分，而不是按技术层拆分。
- 在现有代码库中遵循既有模式。如果代码库使用大文件，不要单方面重构；但如果你要修改的文件已经变得笨重，把拆分纳入计划是合理的。

这个结构会影响任务分解。每个任务都应产生自包含、独立看也有意义的变更。

## 小粒度任务

**每一步都是一个动作（2-5 分钟）：**
- “编写失败测试”：一步
- “运行它以确认失败”：一步
- “实现最小代码让测试通过”：一步
- “运行测试并确认通过”：一步
- “提交”：一步

## 计划文档头部

**每个计划必须以此头部开始：**

```markdown
# [Feature Name] Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** [One sentence describing what this builds]

**Architecture:** [2-3 sentences about approach]

**Tech Stack:** [Key technologies/libraries]

**Verification Surface:** [Go packages, frontend-app, SQL/store, codemap, docs/skills]

---
```

## 任务结构

````markdown
### Task N: [Component Name]

**Files:**
- Create: `exact/path/to/file.py`
- Modify: `exact/path/to/existing.py:123-145`
- Test: `tests/exact/path/to/test.py`

- [ ] **Step 1: Write the failing test**

```python
def test_specific_behavior():
    result = function(input)
    assert result == expected
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pytest tests/path/test.py::test_name -v`
Expected: FAIL with "function not defined"

- [ ] **Step 3: Write minimal implementation**

```python
def function(input):
    return expected
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pytest tests/path/test.py::test_name -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tests/path/test.py src/path/file.py
git commit -m "feat: add specific feature"
```
````

## 不允许占位符

每一步都必须包含工程师需要的实际内容。下面都是**计划失败**，绝不要写：
- “TBD”、“TODO”、“implement later”、“fill in details”
- “Add appropriate error handling” / “add validation” / “handle edge cases”
- “Write tests for the above”（没有实际测试代码）
- “Similar to Task N”（重复代码：工程师可能会乱序阅读任务）
- 只描述做什么但不展示怎么做的步骤（代码步骤需要代码块）
- 引用任何任务中都未定义的类型、函数或方法

## 记住
- 始终使用精确文件路径
- 每一步都包含完整代码：如果该步骤会改代码，就展示代码
- 精确命令和预期输出
- DRY、YAGNI、TDD、频繁提交

## 自审

写完整个计划后，用新的视角看规格，并对照规格检查计划。这是你自己运行的检查清单，不是派发子代理。

**1. 规格覆盖：** 略读规格中的每个章节/需求。你能指出哪个任务实现它吗？列出任何缺口。

**2. 占位符扫描：** 在计划中搜索红旗：上方“不允许占位符”章节里的任何模式。修复它们。

**3. 类型一致性：** 后续任务中使用的类型、方法签名和属性名，是否与你在早期任务定义的内容一致？任务 3 中叫 `clearLayers()`，任务 7 中叫 `clearFullLayers()`，这就是 bug。

如果发现问题，内联修复。不需要重新审阅，修好后继续。如果发现某个规格需求没有对应任务，添加该任务。

## 执行交接

保存计划后，提供执行选择：

**“计划已完成并保存到 `docs/plans/<filename>.md`。有两个执行选项：**

**1. 子代理驱动（推荐）** - 我为每个任务派发新的子代理，在任务之间审查，迭代更快

**2. 当前会话内执行** - 使用 执行计划 在当前会话执行任务，按批次执行并设置检查点

**你想用哪种方式？”**

**如果选择 Subagent-Driven：**
- **必需子技能：** 使用 superpowers:子代理驱动开发
- 每个任务使用新子代理 + 两阶段审查

**如果选择 Inline Execution：**
- **必需子技能：** 使用 superpowers:执行计划
- 批量执行并设置审查检查点
