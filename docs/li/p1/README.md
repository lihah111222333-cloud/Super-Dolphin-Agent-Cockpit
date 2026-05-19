# P1: 代码质量指标清零执行总览

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 彻底清零 `baseline.json` 中由于零容忍代码质量规则（limitZero）被冻结的 117 个文件，实现全代码库解冻与基线收缩。

**Architecture:** 按违规类别将修复任务拆分为 6 个可并行/渐进执行的子计划。优先处理低复杂度的语法级修复，最后集中处理需要架构层重构的全局变量和显式 panic。

**Tech Stack:** Go 1.25, `make guard`, `internal/archtest`

---

## 执行范围

本次执行范围严格限定在 `internal/archtest/baseline.json` 和 `baseline_test.json` 中存在以下违规项的 117 个文件：
- `global_vars`: 46 个文件
- `todo_count`: 34 个文件
- `empty_funcs`: 20 个文件
- `panic_count`: 15 个文件
- `has_init`: 5 个文件
- `naked_returns`: 3 个文件

## 子计划一览

| 文档 | 违规项 | 覆盖文件 | 执行人分配 |
| --- | --- | --- | --- |
| [01-naked-returns-empty-funcs.md](01-naked-returns-empty-funcs.md) | `naked_returns`, `empty_funcs` | 23 | 并行 Agent x1 |
| [02-init-functions.md](02-init-functions.md) | `has_init` | 5 | 并行 Agent x1 |
| [03-explicit-panics.md](03-explicit-panics.md) | `panic_count` | 15 | 并行 Agent x1 |
| [04-todo-cleanups.md](04-todo-cleanups.md) | `todo_count` | 34 | 并行 Agent x2 |
| [05-global-vars-refactor.md](05-global-vars-refactor.md) | `global_vars` | 46 | 并行 Agent x2 |
| [06-integration-and-shrink.md](06-integration-and-shrink.md) | 验证全量清零 | 全局 | 主 Agent |

## 守卫预检清单 (Checklist)

1. **不可回退（Ratchet）**: 在修复某个文件时，必须完全消灭该文件被 `make guard` 标记的所有零容忍项，禁止留下残余。
2. **测试一致性**: 消除 `global_vars` 和 `has_init` 时，需同步修改其 `_test.go` 中的依赖注入，禁止引发测试编译失败。
3. **接口完整性**: 处理 `empty_funcs` 时，如果是未使用的废弃接口应当连同 interface 声明一起重构。
4. **Panic 兜底**: 处理 `panic` 时，如果是必现的开发期异常，应替换为返回带有堆栈的强类型 error。
5. **Shrink 同步**: 每一波修改后必须且只能通过执行 `./scripts/test_with_guard.sh ./internal/archtest -count=1` 或 `make guard` 来自动更新 `baseline.json`。
