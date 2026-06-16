---
name: 编写技能
description: 创建新技能、编辑现有技能，或部署前验证技能是否有效时使用
---

# 编写技能

## 节索引（按需读，勿全文加载）

- 概览 — 你编写测试用例（用子代理制造压力场景），看它们失败（基线行为），编写技能（文档），看测试通过（代理遵循），然后重构（堵住漏洞）。
  详见 references/01-概览.md
- 什么是技能？ — 
  详见 references/02-什么是技能？.md
- 技能的 TDD 映射 — | TDD 概念 | 技能创建 | |-------------|----------------| | **测试用例** | 使用子代理的压力场景 | | …
  详见 references/03-技能的 TDD 映射.md
- 何时创建技能 — 
  详见 references/04-何时创建技能.md
- 技能类型 — 有具体步骤的方法（condition-based-waiting、root-cause-tracing）。
  详见 references/05-技能类型.md
- 目录结构 — ``` skills/ skill-name/ SKILL.
  详见 references/06-目录结构.md
- SKILL.md 结构 — ```markdown。
  详见 references/07-SKILL.md 结构.md
- Claude 搜索优化（CSO） — description 只应描述触发条件。
  详见 references/08-Claude 搜索优化（CSO）.md
- 流程图使用 — ```dot digraph when_flowchart { "Need to show information?
  详见 references/09-流程图使用.md
- 代码示例 — 选择最相关的语言：。
  详见 references/10-代码示例.md
- 文件组织 — ``` defense-in-depth/ SKILL.
  详见 references/11-文件组织.md
- 铁律（与 TDD 相同） — ``` NO SKILL WITHOUT A FAILING TEST FIRST ```。
  详见 references/12-铁律（与 TDD 相同）.md
- 测试所有技能类型 — 不同技能类型需要不同测试方法：。
  详见 references/13-测试所有技能类型.md
- 跳过测试的常见合理化 — | 借口 | 现实 | |--------|---------| | “技能显然很清楚” | 对你清楚 ≠ 对其他代理清楚。
  详见 references/14-跳过测试的常见合理化.md
- 让技能抵抗合理化 — 强制纪律的技能（如 TDD）需要抵抗合理化。
  详见 references/15-让技能抵抗合理化.md
- 技能的 RED-GREEN-REFACTOR — 遵循 TDD 循环：。
  详见 references/16-技能的 RED-GREEN-REFACTOR.md
- 反模式 — “在 2025-10-03 的会话中，我们发现空 projectDir 导致……”。
  详见 references/17-反模式.md
- 停止：进入下一个技能前 — 部署未测试技能 = 部署未测试代码。
  详见 references/18-停止-进入下一个技能前.md
- 技能创建检查清单（TDD 适配） — 
  详见 references/19-技能创建检查清单（TDD 适配）.md
- 发现工作流 — 未来 Claude 如何找到你的技能：。
  详见 references/20-发现工作流.md
- 底线 — 同一条铁律：没有失败测试，就没有技能。
  详见 references/21-底线.md

> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。
