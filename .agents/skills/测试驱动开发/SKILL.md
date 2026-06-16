---
name: 测试驱动开发
description: 实现任何功能或 bug 修复时，在编写实现代码前使用
---

# 测试驱动开发

## 节索引（按需读，勿全文加载）

- 概览 — 先写测试。
  详见 references/01-概览.md
- 何时使用 — 如果你在想“这次就跳过 TDD”，停下。
  详见 references/02-何时使用.md
- 铁律 — ``` NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST ```。
  详见 references/03-铁律.md
- 红-绿-重构 — ```dot digraph tdd_cycle { rankdir=LR; red [label="RED\nWrite failing test", sh…
  详见 references/04-红-绿-重构.md
- 好测试 — | 质量 | 好 | 坏 | |---------|------|-----| | **最小** | 一件事。
  详见 references/05-好测试.md
- 为什么顺序重要 — 代码之后写的测试会立即通过。
  详见 references/06-为什么顺序重要.md
- 常见合理化借口 — | 借口 | 现实 | |--------|---------| | “太简单，不用测” | 简单代码也会坏。
  详见 references/07-常见合理化借口.md
- 红旗：停止并重新开始 — 
  详见 references/08-红旗-停止并重新开始.md
- 示例：Bug 修复 — ```typescript test('rejects empty email', async () => { const result = await su…
  详见 references/09-示例-Bug 修复.md
- 验证检查清单 — 标记工作完成前：。
  详见 references/10-验证检查清单.md
- 卡住时 — | 问题 | 解决方案 | |---------|----------| | 不知道如何测试 | 写理想 API。
  详见 references/11-卡住时.md
- 调试集成 — 发现 bug？
  详见 references/12-调试集成.md
- 测试反模式 — 添加 mock 或测试工具时，读取 @testing-anti-patterns.
  详见 references/13-测试反模式.md
- 最终规则 — ``` Production code → test exists and failed first Otherwise → not TDD ```。
  详见 references/14-最终规则.md

> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。
