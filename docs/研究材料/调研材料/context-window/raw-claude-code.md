# 调研：Claude Code 如何确定与展示 Context Window

> 调研对象：Anthropic 官方 Claude Code CLI / Claude API
> 调研日期：2026-05-16
> 目的：为我们项目「去硬编码 context window 表」提供业界做法参考

## 数据来源

- Claude Code 官方文档 - Explore the context window: https://code.claude.com/docs/en/context-window
- Claude API 文档 - Context windows: https://platform.claude.com/docs/en/build-with-claude/context-windows
- Claude API 文档 - Models List (`GET /v1/models`): https://platform.claude.com/docs/en/api/models-list
- Claude API 文档 - Compaction: https://platform.claude.com/docs/en/build-with-claude/compaction
- /context 命令说明: https://claudelog.com/faqs/what-is-context-command-in-claude-code/
- 状态栏监控: https://pasqualepillitteri.it/en/news/162/claude-code-status-bar-context-monitor-guide
- 1M context beta 头说明: https://simonwillison.net/2025/Aug/12/claude-sonnet-4-1m/
- auto-compact 阈值讨论: https://github.com/anthropics/claude-code/issues/15719 , https://github.com/anthropics/claude-code/issues/28728
- 1M beta 退役公告: https://pasqualepillitteri.it/en/news/1451/anthropic-1m-context-beta-retirement-april-30-2026

---

## 1. `/context` 命令与状态栏如何展示用量

### `/context` 命令（v1.0.86 引入）
- 输入 `/context` 即时打印一份「按类别拆分」的 token 占用明细，类别包括：
  - System prompt（系统提示词）
  - System tools（内置工具）
  - MCP tools
  - Custom agents（子代理）
  - Memory files（CLAUDE.md / MEMORY.md）
  - Skills
  - Messages（对话历史）
  - **Free space**：真正剩余可用空间（留给后续消息、文件读取、命令输出）
  - **Autocompact buffer**：为自动压缩预留的保留区
- 输出形式是「token 数 + 占比」的可视化分解，并附带优化建议。

### 状态栏 / Claude Desktop 指示器
- 在 Claude Desktop 的会话中，会话顶部有 context window 指示器，点击后显示当前用量的「分数 + 百分比」（已用 / 总量）。
- CLI 终端底部有「Context left until auto-compact: N%」提示，表示距离触发自动压缩还剩多少缓冲。
- 状态栏可自定义脚本进一步显示精细 token 数。

### 关键点
展示口径是「已用 token / 模型总 context window」，并把「autocompact buffer」从可用空间里单独扣除——即 Free space = 总窗口 − 各固定占用 − autocompact 预留。

---

## 2. Claude Code 如何知道当前模型的 context window 上限

- 官方「Explore the context window」交互模拟页里用了硬编码常量 `const MAX = 200000`，但那只是教学用的模拟器，不代表 CLI 运行时逻辑。
- 真实 CLI 运行时：模型的 context window 上限来自模型元数据，而非写死的字符串映射表。Anthropic 自己提供了权威来源 —— Models API（见第 3 节）。
- 对 1M 窗口模型，CLI 需要结合 beta header 的启用状态来判断「实际生效」窗口是 200k 还是 1M（见第 4 节）。

---

## 3. Anthropic API 是否提供查询 context window 的接口 —— 有，这是关键发现

### `GET /v1/models`（Models List / Get）
返回的每个 `ModelInfo` 对象**直接带 context window 字段**：

| 字段 | 含义 |
|------|------|
| `id` | 模型唯一标识 |
| `display_name` | 人类可读名 |
| `max_input_tokens` | **最大输入 context window（token）** —— 即 context window 上限 |
| `max_tokens` | `max_tokens` 参数允许的最大值（最大输出） |
| `created_at` | 模型发布时间 |
| `capabilities` | 能力矩阵：batch / citations / code_execution / image_input / pdf_input / structured_outputs / thinking / effort / **context_management** |
| `capabilities.context_management` | 是否支持 compaction，含 `compact_20260112`、`clear_tool_uses_20250919`、`clear_thinking_20251015` 等策略 |

调用示例：
```
curl https://api.anthropic.com/v1/models \
  -H 'anthropic-version: 2023-06-01' \
  -H "X-Api-Key: $ANTHROPIC_API_KEY" # guard:allow-secret environment placeholder, not a literal secret.
```

**结论：`max_input_tokens` 就是「无需硬编码」的动态数据源。** 可以拉取 `/v1/models` 后按 `id` 建立运行时映射，而不是在代码里写死数字表。

### Token Counting API
- `POST /v1/messages/count_tokens`（beta `token-counting-2024-11-01`，现已 GA）可在发送前精确估算一次请求会占用多少 token，用于「已用量」侧的计算。
- 文档明确建议：用 token counting API 估算用量以避免溢出。

### 模型内的 Context Awareness（运行时上下文感知）
Claude Sonnet 4.6 / 4.5、Haiku 4.5 等支持「context awareness」，由 API 在对话中注入：
- 会话开始：`<budget:token_budget>1000000</budget:token_budget>`（1M 窗口为 1000000，小窗口为 200000）
- 每次工具调用后：`<system_warning>Token usage: 35000/1000000; 965000 remaining</system_warning>`

这意味着**总窗口与已用量是 API 主动下发的运行时信号**，harness 不必自己猜。这与我们项目里「provider 的 system:init 事件带 context_window 字段就用动态值」是同一思路，且 Anthropic 把它做成了模型协议的一部分。

---

## 4. 1M context（beta header）场景下窗口如何确定

- 1M token 窗口的模型：Claude Mythos Preview、Opus 4.7、Opus 4.6、Sonnet 4.6。其余（如 Sonnet 4.5、Sonnet 4）为 200k。
- 历史上 Sonnet 4 / Opus 4.x 需要 beta header `context-1m-2025-08-07` 才解锁 1M；**不发该 header 时 API 强制 200k 硬上限**。
- 在 Opus 4.6 / Sonnet 4.6 上，1M 已是默认值，`context-1m-2025-08-07` 变成「接受但冗余」。
- 该 1M beta 计划于 **2026-04-30 退役**。
- 因此「实际生效窗口」= 模型上限（`max_input_tokens`）× 是否启用了对应 beta header。判断逻辑必须把 beta 启用状态纳入，不能只看模型名。

---

## 5. auto-compact 触发阈值与 context window 的关系

### Claude Code 客户端侧 auto-compact
- CLI 当前为**硬编码约 95%** 使用率触发自动压缩。
- VSCode 扩展约在 **75% 使用率**（剩余约 25%）触发，并为压缩过程本身预留约 20%。
- `/context` 里的「Autocompact buffer」就是这块预留区——会从 Free space 中扣掉。
- 阈值可配置目前仍是社区 feature request（issue #15719 / #28728 / #41818），官方尚未开放。

### 服务端 Compaction（API 能力）
- beta header：`compact-2026-01-12`，支持模型：Mythos Preview、Opus 4.7、Opus 4.6、Sonnet 4.6。
- **触发基于输入 token 计数，而非 context window 比例**：默认 trigger = **150,000 input tokens**，最小 50,000，可通过 `context_management.edits[].trigger` 自定义。
- 机制：超阈值时生成 summary → 写入 `compaction` block → 后续请求自动丢弃该 block 之前的内容。

### 关系总结
客户端 auto-compact 用「百分比 × context window」；服务端 compaction 用「绝对 input token 阈值」。两者都依赖准确的 context window 总量来计算「还剩多少」。

---

## 6. 对我们「去硬编码」的可借鉴做法

当前问题：项目把 context window 写成「模型名 → 数字」表（如 `opus[1m] = 872000`），仅当 provider 的 `system:init` 带 `context_window` 时才用动态值。

可借鉴的改进方向：

1. **优先用权威动态源**：调用 `GET /v1/models` 读取 `max_input_tokens`，作为 context window 的真值来源；硬编码表只作为 API 不可达时的兜底缺省。
2. **窗口 = 模型上限 × beta 生效状态**：不要直接用模型名映射最终数字。`opus[1m]` 这种「带 1M 标记的别名」应拆成「基础模型 + 是否启用 `context-1m-*` header」，由 header 状态决定 200k / 1M。注意 1M beta 在 2026-04-30 退役后新模型默认即 1M。
3. **运行时信号优先级最高**：Anthropic 自己用 `<budget:token_budget>` 和 `<system_warning>Token usage: X/Y; Z remaining</system_warning>` 在协议层下发总量与已用量。我们已有的「`system:init` 带 `context_window` 就用动态值」是对的，应把该优先级显式定为：运行时事件 > Models API > 硬编码兜底。
4. **展示口径对齐 `/context`**：展示「已用 / 总量」时把「为压缩预留的 buffer」单独扣除，区分「物理总窗口」与「实际可用 Free space」，避免用户以为 95%~100% 还能用。
5. **已用量计算用 count_tokens**：发送前用 token counting API 精确估算，而不是粗略字符数估算。
6. **别再为 `872000` 这类「魔法数」纠结**：它本质是「1M 窗口扣掉输出预留 / buffer 后的可用值」。正确做法是存「物理窗口」(`max_input_tokens`)，可用值由 buffer 策略在运行时算出，而不是把扣减结果写死进表。
