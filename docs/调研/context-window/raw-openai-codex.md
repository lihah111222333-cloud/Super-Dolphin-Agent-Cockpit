# 调研：OpenAI Codex CLI 与 GitHub Copilot 的 Context Window 处理方式

> 调研日期：2026-05-16
> 调研目的：我们项目把 context window 硬编码成「模型名 → 数字」表，仅当 provider 事件带 `context_window` 时才用动态值。本调研对比 Codex CLI 与 GitHub Copilot 的业界做法，给「去硬编码」提供参考。

---

## 1. OpenAI Codex CLI

### 1.1 界面是否展示 context 使用率
- 是。Codex CLI 的 TUI 在状态行展示「XX% left」上下文剩余指示器，表示当前剩余可用 context 占总 `model_context_window` 的百分比。
- 数据来源是会话事件流：每轮 live session JSONL 事件里带 `token_count`（已用 token）和 `model_context_window`（窗口上限）。指示器即由这两者计算 `剩余% = 1 - token_count / model_context_window`。
- 已知 bug：Windows 平台该指示器一度不显示（属 TUI 渲染问题，后端事件数据仍存在）。见 issue #17618。

### 1.2 Codex 如何获取当前模型的窗口上限
Codex CLI（Rust 实现 `codex-rs`）采用**多来源 + 优先级**模型，而非纯 API 动态获取：

1. **配置覆盖（最高优先级）**：config 里的 `model_context_window` 键可直接指定窗口 token 数；`model_auto_compact_token_limit` 设定触发自动压缩历史的阈值。
2. **内置/外部模型目录（catalog）**：Codex 内置一份模型目录，编码各模型默认窗口（如 GPT-5.4 默认 272K）。可用 `model_catalog_json` 指向外部 `models.json` 在启动时覆盖（profile 级覆盖优先于顶层）。`codex debug models` 可查目录里的值。
3. **provider 能力**：`model_providers.<id>` 配置项暗示可按 provider 查能力，但文档未明确把它作为窗口来源。

> 关键缺陷案例（issue #19319）：Codex 把 GPT-5.5 报成 `258400` 而非官方 400K。根因：内置目录把窗口编码成 `272000`（实为「输入侧」拆分值，模型实际为 272K 输入 + 128K 输出），再乘 95% 有效窗口系数 → `272000 × 0.95 = 258400`。即便用户在本地 config 显式写 `model_context_window = 400000`，硬编码目录值仍可能压过预期。**这是「硬编码目录与真实模型能力脱节」的典型踩坑。**

### 1.3 小结
Codex 的窗口数据**本质上是硬编码目录 + 配置覆盖**，不是从 API 动态拉取。运行时的 `token_count` / `model_context_window` 事件只负责「用量展示」，窗口上限本身仍来自静态目录。

---

## 2. GitHub Copilot（VS Code / Copilot Chat / Agent 模式 / Copilot CLI）

### 2.1 窗口上限来源：动态 CAPI `/models` 端点
- Copilot 的权威模型能力来源是 **CAPI 端点 `GET https://api.githubcopilot.com/models`**。
- 该端点每个模型在 `capabilities.limits` 下返回多个 token 字段：
  - `max_context_window_tokens` —— 总上下文窗口
  - `max_prompt_tokens` —— 实际可用的输入预算（常远小于窗口）
  - `max_output_tokens`
  - `max_non_streaming_output_tokens`
- 例（claude-opus-4.6，Individual/试用 token）：`max_context_window_tokens: 144000`、`max_output_tokens: 64000`、`max_prompt_tokens: 128000`、`max_non_streaming_output_tokens: 16000`。
- **按账户/计划动态变化**：Individual vs Business/Enterprise 拿到的窗口值不同，端点返回的就是当前 token/会话实际可用值，而非固定表。

### 2.2 「窗口」与「可用预算」是两个数
重要区分：`max_context_window_tokens`（可能 400K）≠ `max_prompt_tokens`（可能仅 128K）。Copilot 的压缩/截断阈值（budgetThreshold）应按 `max_prompt_tokens` 算。若 CAPI 低报 `max_prompt_tokens`，会导致压缩过早触发、浪费可用上下文（issue #298900）。

### 2.3 VS Code 模型选择器是否展示 context 信息
- VS Code 的 **Language Models 编辑器**已列出每个模型的 capabilities、context size、计费、可见性等。
- 在「模型选择器」下拉里直接显示窗口大小仍是**进行中的功能请求**（issue #298121、#248860，参考 Cursor 的模型页做法）。
- Copilot Chat 有「Context Window」用量环/图：模型上限来自模型能力 metadata，用量来自补全响应的 usage（`prompt_tokens`/`completion_tokens`/`total_tokens`）。本地模型（ollama / customoai）的限额来自本地 provider 配置的 `maxInputTokens`/`maxOutputTokens`；存在「上限能显示但用量环灰着」的 bug（issue #313458），说明上限与用量是两条独立数据通路。

### 2.4 Copilot CLI
- `/model` 命令目前只显示 premium request 倍率，**不显示** context window 大小（功能请求 issue #1851）。
- 数据同样应来自 `api.githubcopilot.com/models` 的 `capabilities.limits`。

### 2.5 小结
Copilot 的窗口上限是**纯动态**：来自 CAPI `/models` 端点，按账户实时返回，区分「总窗口 / 可用输入预算 / 输出上限」多个字段。本地模型才退回到本地配置 metadata。

---

## 3. OpenAI 官方 API 的 models endpoint 是否返回 context_window

- **不返回。** 传统 `GET /v1/models` 端点的 model 对象只含 `id`、`object`、`created`、`owned_by`，没有 context window 字段。
- OpenAI **文档页面**（developers.openai.com 的模型页）以表格形式列出每个模型的 Context window（如 GPT-5.5 标 1M）、Max output、价格、knowledge cutoff 等，但这是**文档展示数据，不是 API 可程序化读取的字段**。
- 因此 GPT-5 / o 系列窗口大小来源 = 官方文档/模型卡 → 各客户端自行抄进内置目录（这正是 Codex 内置 catalog 的来历，也是它会和真实值脱节的原因）。

---

## 4. tiktoken 等本地 tokenizer 的角色

- tiktoken 是 OpenAI 官方 BPE tokenizer，与 GPT 模型实际编码一致，对 OpenAI 模型计数「100% 准确」。
- 角色定位：**发送前本地预估 token 数**，用于（a）避免超限报错、（b）在 UI 上实时显示「已用 / 剩余」上下文。它只解决「分子」（已用量），**不提供「分母」（窗口上限）**。
- 注意：tiktoken 只对 OpenAI 系模型准；Claude / Gemini 用各自 tokenizer，跨模型用 tiktoken 只是粗估。Codex/Copilot 的精确用量更倾向用 **API 响应回传的 usage**（`prompt_tokens` 等），本地 tokenizer 多用于「发送前预览」。

---

## 5. 关键发现汇总

| 维度 | Codex CLI | GitHub Copilot |
|------|-----------|----------------|
| 窗口上限来源 | 内置模型目录 + config 覆盖（静态） | CAPI `/models` 端点动态返回 |
| 是否区分窗口/可用预算 | 用 95% 有效窗口系数（隐式） | 显式三字段：context / prompt / output |
| 按账户变化 | 否 | 是（Individual vs Enterprise） |
| 用量展示 | TUI「XX% left」，来自会话事件 token_count | Context Window 用量环，来自响应 usage |
| 官方 API 提供窗口 | 否（`/v1/models` 不含） | 是（Copilot 私有 `/models` 含 limits） |
| 典型踩坑 | 硬编码目录值与真实模型脱节（258400 vs 400K） | CAPI 低报 max_prompt_tokens 致压缩过早 |

---

## 6. 对本项目「去硬编码」的可借鉴做法

1. **分层兜底而非单一硬编码表**（参考 Codex 的优先级）：
   `用户配置覆盖` > `provider 事件/API 动态值` > `内置目录默认值`。我们当前「只有 provider 事件带 context_window 才用动态值」其实已是雏形，可补上「显式配置覆盖」这一最高优先级层。

2. **把窗口拆成多个语义字段**（参考 Copilot `capabilities.limits`）：
   不要只存一个数。区分「总窗口 / 可用输入预算 / 输出上限」，压缩/截断阈值应按可用输入预算算，避免 Codex 那种「乘 95% 系数」的隐式 hack 和 Copilot 那种「压缩过早」的 bug。

3. **优先信任 provider 回传的 usage 做用量分子**，本地 tokenizer（tiktoken 类）仅作发送前预估；窗口分母优先用 provider/API 动态值。

4. **承认官方 OpenAI `/v1/models` 不返回窗口** —— 对 OpenAI 系模型，动态获取窗口上限并不现实，硬编码目录无法完全消除；可行目标是「目录可被配置/事件覆盖」而非「彻底去表」。对 Copilot 类有私有 `/models` 端点的 provider，则应直接拉端点而非维护表。

5. **UI 展示「XX% 剩余」是业界共识**，且应基于「动态拿到的窗口」而非硬编码值，否则会出现 Codex issue #19319 那种「显示数与官方不符」的用户困惑。

---

## 来源链接

- [Issue #17618 — Codex CLI Windows 缺失「XX% left」指示器](https://github.com/openai/codex/issues/17618)
- [Issue #19319 — GPT-5.5 在 Codex 报 258400 而非 400K](https://github.com/openai/codex/issues/19319)
- [Codex CLI Features](https://developers.openai.com/codex/cli/features)
- [Codex Configuration Reference](https://developers.openai.com/codex/config-reference)
- [Codex Advanced Configuration](https://developers.openai.com/codex/config-advanced)
- [Codex CLI 概览](https://developers.openai.com/codex/cli)
- [VS Code — Manage context for AI](https://code.visualstudio.com/docs/copilot/chat/copilot-chat-context)
- [VS Code — AI language models](https://code.visualstudio.com/docs/copilot/customization/language-models)
- [Issue #298121 — 在模型选择器展示 context window 大小](https://github.com/microsoft/vscode/issues/298121)
- [Issue #248860 — 展示每个 Copilot 模型的 context size](https://github.com/microsoft/vscode/issues/248860)
- [Issue #298900 — Copilot CAPI 低报 max_prompt_tokens 致压缩过早](https://github.com/microsoft/vscode/issues/298900)
- [Issue #313458 — 本地模型 Context Window 用量未填充](https://github.com/microsoft/vscode/issues/313458)
- [Discussion #186340 — 为何无法用满 context_window（Copilot API 数据）](https://github.com/orgs/community/discussions/186340)
- [copilot-cli Issue #1851 — /model 命令展示 context window 大小](https://github.com/github/copilot-cli/issues/1851)
- [OpenAI API — Models](https://developers.openai.com/api/docs/models)
- [tiktoken 生产环境指南 — Galileo](https://galileo.ai/blog/tiktoken-guide-production-ai)
- [Token Explorer（VS Code 扩展，展示 context-window 用量）](https://marketplace.visualstudio.com/items?itemName=MohitGhodke.token-explorer-copilot-ready)
