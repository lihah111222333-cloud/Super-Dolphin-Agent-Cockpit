# 开源 AI Coding Agent 如何确定 Context Window 与展示 Token 用量

> 调研日期：2026-05-16
> 目的：为我们项目「去硬编码 context window 表」提供业界参考，重点关注「模型元数据注册表」模式、OpenRouter `context_length` 动态返回、以及未知模型兜底策略。

## TL;DR / 核心结论

1. **几乎没有项目把 context window 硬编码成「模型名→数字」大表并长期维护**。主流做法是依赖一个**集中维护的模型元数据注册表**（LiteLLM 的 `model_prices_and_context_window.json`、models.dev、OpenRouter `/models` API），把「数字」的维护责任外包出去。
2. **聚合类 provider（OpenRouter）在运行时动态返回 `context_length`**，agent 直接消费 API 返回值，而不是自己维护表。
3. **静态注册表 + 动态 API + 默认兜底**三层结构是普遍范式：静态表覆盖常见模型，动态 API 覆盖长尾/新模型，兜底默认值兜住未知模型。
4. **兜底值通常是一个保守的常量**（如 contextWindow 128k / 200k），并且**只用于「展示/压缩触发」，不用于「强制拦截」**——真正的上限错误交给 API provider 报。

---

## 1. LiteLLM `model_prices_and_context_window.json` —— 业界事实标准注册表

- 仓库文件：[BerriAI/litellm · model_prices_and_context_window.json](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json)
- 托管 raw URL（很多 agent 直接拉取）：`https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`
- 文档：[Add Model Pricing & Context Window | LiteLLM](https://docs.litellm.ai/docs/provider_registration/add_model_pricing)、[Completion Token Usage & Cost](https://docs.litellm.ai/docs/completion/token_usage)

### 机制

- 一个**单一 JSON 文件**，key 为模型名（带 `provider/` 前缀），value 为该模型的能力/价格元数据。
- 关键 token 字段：
  - `max_input_tokens`：provider 明确给出的最大输入 token；若未给出则回退到 `max_tokens`。
  - `max_output_tokens`：最大输出 token；若未给出则回退到 `max_tokens`。
  - `max_tokens`：旧版字段（legacy），现在更多作为前两者的 fallback。
  - 另含 `input_cost_per_token` / `output_cost_per_token` 及各种 `supports_*` 能力布尔位。
- 示例：`claude-sonnet-4-5` 的 `max_input_tokens = 200000`。
- **维护方式**：社区通过 PR 持续往这个文件加模型（例如 [PR #11972](https://github.com/BerriAI/litellm/pull/11972)）。维护成本被「众包」到 LiteLLM 社区，下游 agent 免费搭便车。
- **程序化注册**：`register_model()` 可传入 dict 或直接传上面的 raw URL；运行时用 `get_max_tokens()` / `get_model_info()` 查询。
- LiteLLM 还有「context window fallback」概念：当主模型超出 context 限制时，自动切换到 context window 更大的备用模型（[Fallbacks 文档](https://docs.litellm.ai/docs/proxy/reliability)）。

### 借鉴点

这正是「模型元数据注册表」模式的范本：**把数字集中到一个文件/服务，下游只查询不维护**。我们若要去硬编码，最直接的做法就是引入（或镜像）LiteLLM 的这个 JSON，而不是自建一张表。

---

## 2. Aider —— 直接复用 LiteLLM metadata + 用户可覆盖文件

- 文档：[Advanced model settings](https://aider.chat/docs/config/adv-model-settings.html)、[Model warnings](https://aider.chat/docs/llms/warnings.html)

### 机制

- **默认数据源**：Aider 直接依赖 litellm 的 `model_prices_and_context_window.json` 获取模型元数据，自己不维护表。
- **未知模型兜底**：
  - 对 litellm 不认识的模型，Aider **不强制**任何限制，只打印一条「unknown context window size and model costs」的 warning。
  - 官方明确说：`Aider never enforces token limits, it only reports token limit errors from the API provider`（Aider 从不强制 token 上限，只转述 API provider 返回的上限错误）。
- **用户覆盖机制**：用户可创建 `.aider.model.metadata.json`（放在 home 目录 / git repo 根 / 当前目录，或用 `--model-metadata-file` 指定），为 litellm 不认识的模型补充 `max_tokens` / `max_input_tokens` / `max_output_tokens` / 价格。
  - 要求用全限定模型名（`deepseek/deepseek-chat` 而非 `deepseek-chat`）。
- 官方鼓励用户「给 litellm 提 PR 补模型」，把长尾维护推回上游注册表。

### 借鉴点

- 「上游注册表 + 本地覆盖文件 + 软 warning」三段式。
- **关键理念**：context window 主要用于提示和压缩决策，不要拿它做硬拦截；真正的越界让 provider 报错。这能消除「硬编码表过期导致误拦」的风险。

---

## 3. Cline —— ModelInfo 接口 + 动态拉取 + 磁盘缓存 + 默认兜底常量

- 参考：[Model Selection and Management | cline DeepWiki](https://deepwiki.com/cline/cline/4.3-model-selection-and-management)、[API Configuration | cline DeepWiki](https://deepwiki.com/cline/cline/4.1-api-configuration)、[OpenRouter - Cline 文档](https://docs.cline.bot/provider-config/openrouter)

### 机制

- **`ModelInfo` 接口**（定义于 `src/shared/api.ts`）描述所有模型能力/价格数据，关键字段：
  - `contextWindow`：总上下文大小（输入+输出）token 数。
  - `maxTokens`：最大输出 token。
  - `supportsPromptCache` / `supportsReasoning` / `thinkingConfig` / 各价格字段。
- **两类 provider**：
  - 有静态列表的 provider（如 Anthropic 直连）：在 `src/shared/api.ts` 里写死 `contextWindow: 200_000` 等常量。
  - 无静态列表的 provider（OpenRouter、Cline、Ollama、LiteLLM、Requesty 等）：**运行时动态拉取**模型列表。
- **动态拉取流程**（`refreshOpenRouterModels`）：
  1. 先查 `StateManager` 内存缓存（TTL = 1 小时，`MODEL_CACHE_TTL_MS = 60*60*1000`）。
  2. 未命中则调用 `https://openrouter.ai/api/v1/models`（axios）。
  3. 价格换算（原始价 ×1,000,000 转成 per-million）。
  4. 对 Claude 1M 变体等做特殊 override。
  5. 结果写磁盘缓存 `openrouter_models.json`。
  - 用 `pendingRefresh` promise 去重并发请求。
- **选中模型时**：模型 ID 字符串 + 完整 `ModelInfo` 对象一起写入 state。**内联存储 ModelInfo 让离线也能展示能力/价格**，无需重新请求 API。
- **未知模型兜底**：`openRouterDefaultModelId` / `openRouterDefaultModelInfo` 常量（`src/shared/api.ts` 约 771–787 行）作为 OpenRouter 无动态数据时的 fallback。
- **已知坑**：
  - OpenRouter 同一模型由不同底层 provider 服务，context window 会有差异（[Issue #3428 Roo / Cline 类似问题]）；[Cline Issue #9972](https://github.com/cline/cline/issues/9972) 报告过 contextWindow 值错误。
  - [Issue #9592](https://github.com/cline/cline/issues/9592)：max_tokens 回归 bug。修法是 `max_tokens = Math.min(rawMaxTokens, contextWindow - buffer)`，即用 contextWindow 反推安全的输出上限。

### 借鉴点

- **ModelInfo 作为统一结构体**，contextWindow 只是其中一个字段，和能力位/价格一起流转。
- **动态拉取 + 内存 TTL 缓存 + 磁盘缓存 + 并发去重**：网络不可用时优雅降级。
- 选中即把完整元数据快照进 state，保证离线展示。

---

## 4. Roo Code —— 与 Cline 同源，每个 provider 各自的默认 ModelInfo

- 参考：[Using OpenRouter With Roo Code](https://docs.roocode.com/providers/openrouter)、[Issue #7209 feat: context window support](https://github.com/RooCodeInc/Roo-Code/issues/7209)、[Issue #3428 错误 context window](https://github.com/RooCodeInc/Roo-Code/issues/3428)、[Issue #7797 Ollama num_ctx](https://github.com/RooCodeInc/Roo-Code/issues/7797)

### 机制

- Roo Code fork 自 Cline，沿用 `ModelInfo` + `contextWindow` 结构。
- 每个动态 provider 有自己的默认兜底对象：`openRouterDefaultModelInfo`、`ollamaDefaultModelInfo` 等，当拉不到 / 用户未提供具体信息时用作 fallback。
  - 例：Ollama 默认 fallback `ollamaDefaultModelInfo` 的 `contextWindow = 200000`。
- 已知坑：OpenRouter 特定底层 provider 上报的 context window 不对（[Issue #3428](https://github.com/RooCodeInc/Roo-Code/issues/3428)）；Roo 强制用 `modelInfo.contextWindow` 覆盖 Ollama Modelfile 的 `num_ctx`（[Issue #7797](https://github.com/RooCodeInc/Roo-Code/issues/7797)），说明「以注册表值为准 vs 以本地真实配置为准」存在张力。

### 借鉴点

- **「每个 provider 一个默认 ModelInfo 常量」**比「一张全局模型名→数字大表」更易维护：兜底粒度落在 provider 级别，新模型不至于完全没值。

---

## 5. OpenRouter `/models` API —— 动态返回 `context_length`

- 文档：[List all models and their properties](https://openrouter.ai/docs/api/api-reference/models/get-models)、[OpenRouter Models 概览](https://openrouter.ai/docs/guides/overview/models)
- 端点：`GET https://openrouter.ai/api/v1/models`，响应 `{"data": [ ...model ]}`

### 每个 model 对象的关键字段

- `context_length`（**Required**，整数）：模型整体的最大 context window（如 Claude v2.0 = 100000）。
- `top_provider.context_length`：**当前最优底层 provider 的 context length**，可能与模型整体值不同（这是 Cline/Roo 出现 contextWindow 不一致的根因）。
- `top_provider.max_completion_tokens`：最优 provider 的最大输出 token。
- `per_request_limits.max_completion_tokens`：每请求最大输出 token。
- 其它：`id` / `canonical_slug` / `architecture`（modalities、tokenizer）/ `pricing` / `supported_parameters` / `created` 等。

### OpenRouter 自身如何用 context_length

- 路由/压缩时：若没有模型满足 context 要求，OpenRouter 回退到「可用 context_length 最大」的模型。
- 「middle-out」transform：自动压缩/截断 prompt 中间部分以塞进所选模型的 context window。

### 借鉴点

- **聚合 provider 已经把 context window 做成 API 返回字段**——对接 OpenRouter 类 provider 时，应直接消费 `context_length`，而不是自己查表。
- 注意 `context_length` 与 `top_provider.context_length` 的区别：取保守值（两者取较小）更安全。

---

## 6. OpenCode (sst) —— 依赖 models.dev 注册表 + 保留缓冲

- 参考：[Provider and Model Configuration | sst/opencode DeepWiki](https://deepwiki.com/sst/opencode/3.3-provider-and-model-configuration)、[Context Management and Compaction](https://deepwiki.com/sst/opencode/2.4-context-management-and-compaction)、[Providers 文档](https://opencode.ai/docs/providers/)

### 机制

- OpenCode 集成 Vercel AI SDK + **[models.dev](https://models.dev) 注册表**，支持 75+ provider；标准 provider 的 `limit` 字段（context window、max output）自动从 models.dev 拉取。
- 用户可在配置里手动加 `limit` 字段，或用 `maxContext` 覆盖模型默认 context 上限。
- **压缩触发逻辑**：`isOverflow` 判断当前 token 数是否超过「可用上限」=「模型 context 上限 − 保留缓冲」。
  - 默认保留输出 token = 32,000。
  - 安全缓冲 `COMPACTION_BUFFER` = 20,000 token。
  - 超出则自动 summarize 历史会话继续。
- 已知坑：models.dev 对部分模型列的 context window 偏大（实际只有高价档才有），导致压缩触发不准（[Issue #3477](https://github.com/sst/opencode/issues/3477)、[opencode zen context limit issues]）。

### 借鉴点

- **models.dev 是 LiteLLM 注册表之外的另一个开放注册表选项**，专门做模型元数据，OpenCode 直接依赖它。
- **保留缓冲（reserved output + safety buffer）**是个好实践：context window 不是直接用满，而是预留输出空间，再触发压缩。

---

## 7. 各项目对「未知模型」兜底策略对比

| 项目 | 主数据源 | 兜底策略 | context window 用途 |
|------|----------|----------|--------------------|
| Aider | LiteLLM `model_prices_and_context_window.json` | 软 warning，不强制；用户可建 `.aider.model.metadata.json` 覆盖 | 仅提示，不拦截，越界交给 API 报错 |
| Cline | 静态 `ModelInfo` 表 + OpenRouter `/models` 动态拉取 | `openRouterDefaultModelInfo` 默认常量 | 反推 max_tokens、压缩、展示 |
| Roo Code | 同 Cline | 每 provider 一个默认 ModelInfo（如 `ollamaDefaultModelInfo` contextWindow=200000） | 同上 |
| OpenCode | models.dev 注册表 | 用户 `limit` / `maxContext` 配置；保留 32k 输出 + 20k 缓冲 | 触发自动压缩 |
| OpenRouter（provider 侧） | 自有模型目录 | 回退到 context_length 最大的模型；middle-out 截断 | 路由 + 压缩 |

---

## 8. 对我们项目「去硬编码」的可借鉴做法

当前问题：context window 硬编码成「模型名 → 数字」表，只有 provider 事件带 `context_window` 时才用动态值。

建议演进为**三层数据源 + 保守兜底**：

1. **动态优先（运行时来源）**
   - provider 事件携带的 `context_window` 永远最高优先级（已有）。
   - 对接 OpenRouter 类聚合 provider 时，主动消费 `/models` API 的 `context_length`（取 `min(context_length, top_provider.context_length)` 更安全），并做内存 TTL + 磁盘缓存（参考 Cline 的 1h TTL、`pendingRefresh` 去重、磁盘 fallback）。

2. **静态注册表替代手写表（去硬编码核心）**
   - 不再手工维护「模型名→数字」大表，改为引入/镜像一个**外部模型元数据注册表**：
     - LiteLLM `model_prices_and_context_window.json`（覆盖最广、社区 PR 活跃），或
     - models.dev（OpenCode 用的，专做模型元数据）。
   - 可定期同步该 JSON 到仓库内（vendored snapshot），既去掉手维护负担，又不引入运行时网络强依赖。
   - 把 contextWindow 放进一个统一的 `ModelInfo` 结构体（参考 Cline），和 maxOutputTokens、能力位、价格一起流转，选中模型时快照进状态，支持离线展示。

3. **保守兜底（未知模型）**
   - 未知模型用一个**保守默认常量**（建议偏小，如 128k），而不是乐观大值——宁可早压缩，不可越界。
   - 可按 provider 分别设默认（参考 Roo Code 每 provider 一个 `defaultModelInfo`），比单一全局默认更准。
   - **兜底值只用于「展示 token 用量 + 触发压缩」，不做硬拦截**（参考 Aider：never enforce, only report）。真正越界让 provider 报错后再处理。
   - 留输出缓冲：可用上限 = contextWindow − 预留输出 − 安全缓冲（参考 OpenCode 的 32k + 20k）。

4. **展示 token 用量**
   - UI 展示「已用 / 可用上限」，可用上限来自上述合并后的 contextWindow。
   - 对未知模型展示一个明确的「估算/未知」标记（参考 Aider 的 warning），避免用户误以为是精确值。

### 落地优先级建议

- 第一步（低风险、收益大）：用 vendored 的 LiteLLM/models.dev JSON 替换手写硬编码表，保留 provider 事件动态值优先级不变。
- 第二步：OpenRouter 类 provider 接入 `/models` API 动态 `context_length` + 缓存。
- 第三步：引入保守的 per-provider 兜底常量 + 输出缓冲，明确「不硬拦截」原则。

---

## 来源链接汇总

- [BerriAI/litellm · model_prices_and_context_window.json](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json)
- [LiteLLM · Add Model Pricing & Context Window](https://docs.litellm.ai/docs/provider_registration/add_model_pricing)
- [LiteLLM · Completion Token Usage & Cost](https://docs.litellm.ai/docs/completion/token_usage)
- [LiteLLM · Fallbacks](https://docs.litellm.ai/docs/proxy/reliability)
- [Aider · Advanced model settings](https://aider.chat/docs/config/adv-model-settings.html)
- [Aider · Model warnings](https://aider.chat/docs/llms/warnings.html)
- [Cline · Model Selection and Management (DeepWiki)](https://deepwiki.com/cline/cline/4.3-model-selection-and-management)
- [Cline · API Configuration (DeepWiki)](https://deepwiki.com/cline/cline/4.1-api-configuration)
- [Cline · OpenRouter provider docs](https://docs.cline.bot/provider-config/openrouter)
- [Cline · Issue #9592 max_tokens regression](https://github.com/cline/cline/issues/9592)
- [Cline · Issue #9972 wrong contextWindow](https://github.com/cline/cline/issues/9972)
- [Roo Code · OpenRouter docs](https://docs.roocode.com/providers/openrouter)
- [Roo Code · Issue #7209 context window support](https://github.com/RooCodeInc/Roo-Code/issues/7209)
- [Roo Code · Issue #3428 wrong context window](https://github.com/RooCodeInc/Roo-Code/issues/3428)
- [Roo Code · Issue #7797 Ollama num_ctx override](https://github.com/RooCodeInc/Roo-Code/issues/7797)
- [OpenRouter · List all models API](https://openrouter.ai/docs/api/api-reference/models/get-models)
- [OpenRouter · Models 概览](https://openrouter.ai/docs/guides/overview/models)
- [OpenCode · Provider and Model Configuration (DeepWiki)](https://deepwiki.com/sst/opencode/3.3-provider-and-model-configuration)
- [OpenCode · Context Management and Compaction (DeepWiki)](https://deepwiki.com/sst/opencode/2.4-context-management-and-compaction)
- [OpenCode · Providers docs](https://opencode.ai/docs/providers/)
- [models.dev](https://models.dev)
