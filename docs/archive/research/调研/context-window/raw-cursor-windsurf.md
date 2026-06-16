# Cursor / Windsurf 的 Context Window 与 Token 用量调研

> 调研目的：我们项目把 context window 硬编码为「模型名 → 数字」表，仅在 provider 事件携带 `context_window` 时用动态值。本文调研 Cursor / Windsurf 等多模型聚合 AI IDE 的业界做法，为「去硬编码」提供借鉴。
>
> 调研日期：2026-05-16。来源均为公开网页，链接见文末。

## 1. Cursor 的 context 使用指示器

- **历史形态**：旧版本 Cursor 在聊天侧边栏有一个**环形 token 计数器**，以 `已用k / 总量k`（如 `45k/200k`）形式展示当前对话占用了多少 context window，并配合百分比。这是很多重度用户的核心工作流参照——用来判断「还剩多少余量、何时该开新会话」。
- **被移除**：该指示器在近期版本（约 v2.0.63 起逐步弱化，到 v2.2.44 / v2.6 已「完全消失」）被移除或隐藏，社区出现大量「请求恢复 token counter circle / context usage indicator」的 feature request 与 feedback 帖。付费用户尤其不满：「买了 200k context 的 Pro，却没有任何可视化指示」。
- **超限提示**：Cursor 采用**自动 summarization（对话压缩）**机制——上下文接近上限时会「Summarizing chat context」。但社区报告该过程**没有进度指示**、有时卡死；并且压缩后 token 计数未必重新计算（见第 5 节 bug）。
- **现状结论**：Cursor 当前对 context 用量的可视化是**退化且不稳定**的，属于反面案例而非可直接抄的范本。

## 2. Cursor 不同模型的 context window 信息来源

- Cursor 代理 Claude / GPT / Gemini / 自研 Composer 等多家模型，可在同一会话切换（如 GPT-5.3-Codex、Claude Sonnet 4.5、Gemini 3 Pro）。
- **官方模型表**：`cursor.com/docs/models` 维护一张模型表，文档称「上面的模型表展示每个模型的最大 context size」。这是一张**由 Cursor 自己维护的清单**（含 context、定价、Max 是否可用等），本质仍是受控的「模型 → 能力」映射，并非纯动态从上游拉取。
- **Normal vs Max 模式**：context window 不只取决于模型，还取决于运行模式：
  - Normal 模式约 ~128K；Max 模式「扩展到模型支持的最大值」（如 200K，Claude 4.6 可达 1M）。
  - 关键事实：**同一模型的有效 context 由「模型变体 + 运行模式 + 订阅档位」三者共同决定**。例如 Claude 4.6「不开 MAX 默认 200k」，开 MAX 才解锁 1M，且 1M 需要 Ultra 套餐。
  - Cursor 内部处理还会进一步把可用窗口压到 ~70K–120K（codebase 检索/RAG 开销）。
- **借鉴点**：单一「模型名 → 数字」无法表达真实窗口，至少需要 `(模型, 模式, 套餐)` 维度；上游能力 + 产品侧策略叠加。

## 3. Windsurf（Cascade）的 context / credits 展示

Windsurf 在这方面比 Cursor 做得更完整、更新更积极（来自官方 changelog）：

- **Footer 实时 context window 用量条**：Cascade 在底部 footer 显示实时的 context window 使用 meter，帮助用户预判上限、决定何时开新会话。
- **prompt cache 计时器**：context window 指示器内集成了 prompt cache timer，显示缓存何时过期。
- **model picker 显示 token 定价**：模型选择器直接展示每个模型的 input / output / cache-read token 费率。
- **response card token 明细**：每条消息回复后的 response card 显示 token 计数，可看到 input / output / cached-input 的拆分，理解每条消息成本如何计算。
- **credits 用量入口**：Settings 面板（状态栏）查看剩余 credits；overflow 菜单 → 「Cascade Usage」；或在 `windsurf.com/plan` 查看。
- **借鉴点**：Windsurf 把「context 用量」和「计费用量」分成两套展示；context 条是**按当前模型窗口归一化**的，说明它内部知道当前所选模型的窗口大小并用于实时计算。

## 4. 多模型聚合产品如何维护「模型能力清单」

- **OpenRouter 是典型范式**：维护一个 **normalized model catalog**——数百个模型的统一元数据（ID、定价、支持参数、`context_length` 等），客户端可**动态浏览/选择模型而无需硬编码** provider 细节。
  - 提供 `/models` API，返回每个模型的 context length、pricing、supported parameters 等结构化字段。
  - OpenRouter 负责吸收各家差异（不同 sampler、context window、streaming 协议），对外暴露统一 schema（类 OpenAI Chat API）。
- **业界通行做法**：能力清单是「**上游声明 + 聚合层归一化 + 可远程刷新**」，而不是写死在客户端代码里。客户端把模型列表当作**数据**（可拉取、可缓存、可热更新），而非**代码常量**。
- **对我们的启示**：
  1. context window 应作为**模型元数据的一个字段**，与 pricing/能力一起管理，而非散落的硬编码 map。
  2. 优先信任 **provider 事件 / 上游 API 返回的真实值**；硬编码表只作为「上游未提供时的 fallback 默认值」，并明确标记为 fallback。
  3. fallback 表应可通过配置/远程下发更新，避免新模型上线必须改代码发版。
  4. 有效窗口需考虑「模式 / 档位」叠加，单一数字不足以描述。

## 5. 已知问题：context 指示器不准 / 窗口大小错误

来自 Cursor 社区论坛的真实 bug（对我们设计有警示意义）：

- **指示器硬编码上限，与实际配置脱节**：用户把 GPT-5.5 设为 272k，但 context 指示器「始终停在 1M」，不反映所选 limit；会话实际还冲到了 ~350k。社区分析：**display / enforcement / summarization 是三套不同步的系统**——这正是「UI 上限被写死」的典型恶果。
- **Claude 模型卡在 200k**：v2.6.22 中 Claude 4.6 显示 200k 而非预期的 1M。官方澄清：200k 是「未开 MAX 的默认值」，1M 需开 MAX + Ultra 套餐——即**窗口值随模式/档位变化**，单值表达必然出错或引起误解。
- **token 计数膨胀**：多份报告称 token counter 比实际高约 10x，dashboard 甚至高 30x+；IDE 与 dashboard 数字对不上。
- **summarization 无效**：压缩对话「连跑两次都不减少 context size」，疑似压缩后 token 未重算。
- **指示器被移除 / 透明度下降**：token counter 从聊天面板消失（v2.0.63、v2.6），prompt 编辑器里的 context 指示器也不再显示已应用的 rules。

**核心教训**：把 context 上限当作 UI 常量、与真实 enforcement 解耦，是 Cursor 反复出问题的根因。指示器必须从**单一可信来源**（实际计费/请求侧的 token 计数 + 当前生效窗口）派生。

## 6. 对我们「去硬编码」的可借鉴做法

1. **以上游为真值源**：provider 事件携带 `context_window` 时无条件采用；这应是默认路径，硬编码只是兜底。
2. **fallback 表数据化**：把「模型名 → 数字」从代码常量改为可配置/可远程刷新的数据（类似 OpenRouter catalog），新模型不必改代码。
3. **元数据聚合**：context window 与 pricing、能力标志放在同一份模型元数据里统一维护，单一 registry。
4. **建模模式维度**：若产品有类似 Normal/Max 的概念，有效窗口 = `f(模型, 模式, 档位)`，不要用单一标量。
5. **单一可信来源驱动 UI**：context 指示器和限额 enforcement 必须读同一份 token 计数与窗口值，避免 Cursor「UI 停在 1M」式脱节。
6. **fallback 要可见**：当用的是硬编码兜底值（而非上游真值）时，最好在日志/调试信息里可识别，便于发现「上游没传值」的情况。

## 来源链接

- [Context | Cursor Learn](https://cursor.com/learn/context)
- [Models | Cursor Docs](https://cursor.com/docs/models)
- [Bring back context usage indicator (token counter circle) — Cursor Forum](https://forum.cursor.com/t/bring-back-context-usage-indicator-token-counter-circle/147515)
- [Where did context-window fill indicator go? — Cursor Forum](https://forum.cursor.com/t/where-did-context-window-fill-indicator-go/156687)
- [The consumption indicator of the context window appears to have been removed — Cursor Forum](https://forum.cursor.com/t/the-consumption-indicator-of-the-context-window-appears-to-have-been-removed/139914)
- [Claude models stuck at 200k context window size — Cursor Forum](https://forum.cursor.com/t/claude-models-stuck-at-200k-context-window-size/156424)
- [Summarization does not reduce context, 272k limit ignored, indicator stays at 1M — Cursor Forum](https://forum.cursor.com/t/summarization-does-not-reduce-context-272k-limit-is-ignored-and-context-indicator-stays-at-1m/159570)
- [Massive discrepancy between IDE Context usage and Dashboard Activity logs — Cursor Forum](https://forum.cursor.com/t/massive-discrepancy-between-ide-context-usage-and-dashboard-activity-logs/146468)
- [Token count is inaccurate/broken in Cursor IDE v2.6 — Cursor Forum](https://forum.cursor.com/t/token-count-is-inaccurate-broken-in-cursor-ide-v2-6-team-plan/155002)
- [Cursor Token Counter Over Counting by x10 — Cursor Forum](https://forum.cursor.com/t/cursor-token-counter-over-counting-by-x10/128362)
- [Diminishing transparency in context usage indicator — Cursor Forum](https://forum.cursor.com/t/diminishing-transparency-in-context-usage-indicator/149973)
- [Windsurf Editor Changelog](https://windsurf.com/changelog)
- [Plans and Credit Usage — Windsurf Docs](https://docs.windsurf.com/windsurf/cascade/usage)
- [Context Awareness Overview — Windsurf Docs](https://docs.windsurf.com/context-awareness/overview)
- [OpenRouter Models — Documentation](https://openrouter.ai/docs/guides/overview/models)
- [Models | OpenRouter](https://openrouter.ai/models)
