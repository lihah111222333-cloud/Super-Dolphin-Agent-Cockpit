# 调研：Google Gemini CLI 的 Context Window 与 Token 使用量

> 调研日期：2026-05-16
> 对象仓库：[google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli)
> 目的：为本项目「去掉 context window 硬编码表」提供业界参考。

## 0. 结论速览（TL;DR）

- Gemini CLI **同样使用「模型 → 数字」的硬编码常量表**，并非完全动态获取。源码位于 `packages/core/src/core/tokenLimits.ts`。
- 它的硬编码相对简单：几乎所有 Gemini 模型都返回同一个 `DEFAULT_TOKEN_LIMIT = 1_048_576`，只有 Gemma 系列单独返回 `256_000`；未知模型也回退到 `DEFAULT_TOKEN_LIMIT`。
- Gemini API 其实**提供了动态来源**：`models.get` / `models.list` 返回 `inputTokenLimit` / `outputTokenLimit`；但 Gemini CLI 当前并未用它来填充 context window 上限。
- Token 使用量靠两条路：远程 `countTokens`（精确）与本地字符估算（约 4 字符/token）。
- 自动压缩阈值是 context window 上限的一个比例（当前默认 `0.5`，历史上是 `0.7` / `0.95`），即压缩阈值 = `tokenLimit(model) * threshold`。

---

## 1. 底部状态栏：context 剩余百分比 / token 使用量

### 展示方式
- Footer 默认展示「**剩余 context window 百分比**」（context left），以及高占用告警。
- 配置项 `ui.footer.hideContextPercentage`（`settings.json`）可隐藏该百分比。
- 社区正在把显示逻辑「反转」为展示**已用百分比**（"X% context used"），并做颜色分级（阈值处黄色、100% 红色）—— 见 Issue [#20070](https://github.com/google-gemini/gemini-cli/issues/20070)。
- `/stats` 命令展示当前会话的详细 token 统计（包括缓存 token 节省、会话时长）。
- 新增提案 `/context` 命令（Issue [#23165](https://github.com/google-gemini/gemini-cli/issues/23165)）：用一条彩色水平条展示 context 占用，按 system prompt / tool schemas / memory files / conversation history 四类拆分，并在条上标记压缩阈值位置；数据全部来自内存中已加载状态，不做磁盘 IO。

### 百分比怎么算
- 核心公式（见 Issue [#15426](https://github.com/google-gemini/gemini-cli/issues/15426)）：

  ```ts
  const remainingTokenCount =
    tokenLimit(modelForLimitCheck) - this.getChat().getLastPromptTokenCount();
  ```

  即：**剩余 = 模型上限(硬编码) − 上一次请求的 prompt token 数**。
- 已知缺陷：该计算只减去 `promptTokenCount`，漏掉了 `candidatesTokenCount`（模型回复 token），导致百分比偏高 —— Issue [#15426](https://github.com/google-gemini/gemini-cli/issues/15426)、[#6106](https://github.com/google-gemini/gemini-cli/issues/6106)。
- `--resume` 恢复历史会话时百分比错误显示 100% —— Issue [#16973](https://github.com/google-gemini/gemini-cli/issues/16973)。

> 借鉴点：百分比的「分母」（context 上限）来自硬编码 `tokenLimit()`，「分子」（已用）来自最近一次真实请求的 token 计数。分母静态、分子动态。

---

## 2. Context window 上限的来源（核心）

### 源码文件
`packages/core/src/core/tokenLimits.ts`（DeepWiki 标注约 15-30 行处定义映射）。

### 常量
| 常量 | 值 |
|------|-----|
| `DEFAULT_TOKEN_LIMIT` | `1_048_576`（1M） |
| `GEMMA_4_TOKEN_LIMIT` | `256_000` |

### 模型 → token 上限映射（`tokenLimit(model)` 函数）
该文件从 `'../config/models.js'` 导入模型名常量：
`DEFAULT_GEMINI_MODEL`、`DEFAULT_GEMINI_FLASH_MODEL`、`DEFAULT_GEMINI_FLASH_LITE_MODEL`、
`PREVIEW_GEMINI_MODEL`、`PREVIEW_GEMINI_FLASH_MODEL`、`GEMMA_4_31B_IT_MODEL`、`GEMMA_4_26B_A4B_IT_MODEL`。

`tokenLimit()` 是一个 `switch`：

| 模型 | 上限 |
|------|------|
| `GEMMA_4_31B_IT_MODEL` / `GEMMA_4_26B_A4B_IT_MODEL` | 256,000 |
| `PREVIEW_GEMINI_MODEL` / `PREVIEW_GEMINI_FLASH_MODEL` | 1,048,576 |
| `DEFAULT_GEMINI_MODEL` / `DEFAULT_GEMINI_FLASH_MODEL` / `DEFAULT_GEMINI_FLASH_LITE_MODEL` | 1,048,576 |
| 其它（`default:`）未知模型 | `DEFAULT_TOKEN_LIMIT` = 1,048,576 |

### 关键判断
- **是硬编码常量表，不是动态获取。** 与本项目「模型名 → 数字」表本质相同。
- 但 Gemini CLI 的表非常「扁平」——除 Gemma 外几乎全部 1M，未知模型也安全回退到 1M，**没有按模型名做正则/子串匹配，也没有单独的 output token limit 函数**。
- 1M 上限被社区诟病为瓶颈（Gemini 3 Flash 实际只有 200K，Gemini 3 Pro 才 1M），出现负数「剩余 token」等 bug —— Issue [#26188](https://github.com/google-gemini/gemini-cli/issues/26188)、[#11947](https://github.com/google-gemini/gemini-cli/issues/11947)、[#14333](https://github.com/google-gemini/gemini-cli/issues/14333)、Discussion [#16067](https://github.com/google-gemini/gemini-cli/discussions/16067)。即「硬编码表跟不上新模型」正是本项目要规避的问题。
- 模型默认配置另见 `packages/core/src/config/defaultModelConfigs.ts`（含 `maxOutputTokens` 等；Issue [#23081](https://github.com/google-gemini/gemini-cli/issues/23081) 指出 gemini-2.5-pro 漏配 `maxOutputTokens` 导致输出在 ~8K 被截断）。

---

## 3. Gemini API 的动态来源

### `models.get` / `models.list`（REST：`GET /v1beta/models/{model}`）
返回的 Model 资源字段（[官方文档](https://ai.google.dev/api/models)）：

| 字段 | 含义 |
|------|------|
| `name` | 资源名，形如 `models/{model}` |
| `displayName` | 人类可读名，如 "Gemini 1.5 Flash" |
| `inputTokenLimit` | **该模型允许的最大输入 token 数** |
| `outputTokenLimit` | **该模型可生成的最大输出 token 数** |
| `supportedGenerationMethods` | 支持的方法数组，如 `generateContent` |

> `inputTokenLimit` 就是真正意义上的 context window 上限。**这个接口就是「去硬编码」的标准动态来源**：启动时调用 `models.get` 即可拿到准确上限，无需维护常量表。Gemini CLI 当前没有用它，属于可改进点。

### `countTokens`（REST：`POST /v1beta/models/{model}:countTokens`）
返回字段（[官方文档](https://ai.google.dev/api/tokens)）：

| 字段 | 含义 |
|------|------|
| `totalTokens` | prompt 被 tokenize 后的 token 总数（非负） |
| `cachedContentTokenCount` | prompt 中命中缓存部分的 token 数 |
| `promptTokensDetails` | 输入各模态的处理明细 |
| `cacheTokensDetails` | 缓存内容各模态明细 |

Gemini CLI 用它做**精确**的「已用 token」计算（`calculateRequestTokenCount`）。

---

## 4. 自动压缩（chat compression）与 context window 的关系

- 服务：`ChatCompressionService`，由 `GeminiClient`（`packages/core/src/core/client.ts` 的 `tryCompressChat`）调用。
- 阈值常量（DeepWiki [Chat Compression](https://deepwiki.com/google-gemini/gemini-cli/4.12-chat-compression-and-context-management)）：
  - `DEFAULT_COMPRESSION_TOKEN_THRESHOLD = 0.5`：history token 达到 **context 上限的 50%** 时触发自动压缩。
  - `COMPRESSION_PRESERVE_THRESHOLD = 0.3`：最近 30% 的历史保留不压缩，保证对话连续性。
- 历史变化：早期为 `0.95`，PR [#2898](https://github.com/google-gemini/gemini-cli/pull/2898) 降到 `0.7`，后续进一步降到 `0.5`（Issue [#12068](https://github.com/google-gemini/gemini-cli/issues/12068) 讨论 0.7 是否过于保守）。
- 触发判定本质：`tokenUsed >= tokenLimit(model) * threshold`。**压缩阈值直接乘在 context window 上限上**——所以上限值准不准会直接影响压缩时机。
- 可配置：`settings.json` 中压缩阈值是 0~1 的比例，同时作用于自动压缩和手动 `/compress`。
- 相关缺陷：`tryCompressChat` 把 `maxOutputTokens` 设成整个历史的 `originalTokenCount`，可能超出 API 上限导致请求失败 —— Issue [#7578](https://github.com/google-gemini/gemini-cli/issues/7578)。

---

## 5. 对本项目「去硬编码」的可借鉴做法

1. **保留硬编码表，但仅作为兜底（fallback），不作为唯一真值。** Gemini CLI 的 `default:` 分支回退思路值得学：未知模型不报错、给一个安全默认值。
2. **优先用 API 动态获取上限。** Gemini 提供 `models.get` 的 `inputTokenLimit`/`outputTokenLimit`；本项目可在 provider 适配层启动时拉取并缓存，缺失再回退到硬编码表。这正好补上「只有 provider 事件带 `context_window` 时才用动态值」的空窗——可以主动查询而非被动等事件。
3. **优先级链建议**：`provider 事件 context_window` → `provider API 查询(models.get)` → `硬编码表` → `全局默认值`。
4. **硬编码表保持扁平 + 安全默认。** Gemini CLI 几乎所有模型同值、未知回退 1M，避免「漏一个新模型就崩」。但反例教训：1M 写死后跟不上 Gemini 3 Flash 的 200K，产生负数剩余 token——说明**默认值宁可偏小或必须能被动态值覆盖**。
5. **分子（已用）和分母（上限）分开治理**：上限可静态/半动态，已用量用真实 token 计数（精确 `countTokens` 或本地估算）。
6. **百分比展示注意把输出 token 也计入**（Gemini CLI 漏算 `candidatesTokenCount` 是已知 bug），避免剩余量虚高。
7. **压缩/告警阈值用比例而非绝对值**，依赖准确的 context 上限；上限不准会连锁影响压缩时机。

---

## 6. 来源链接汇总

### 源码 / 文档
- 仓库：https://github.com/google-gemini/gemini-cli
- `packages/core/src/core/tokenLimits.ts`：https://raw.githubusercontent.com/google-gemini/gemini-cli/main/packages/core/src/core/tokenLimits.ts
- `packages/core/src/core/client.ts`（`tryCompressChat`）、`packages/core/src/config/models.ts`、`packages/core/src/config/defaultModelConfigs.ts`
- DeepWiki - Chat Compression and Context Management：https://deepwiki.com/google-gemini/gemini-cli/4.12-chat-compression-and-context-management
- Gemini API - countTokens：https://ai.google.dev/api/tokens
- Gemini API - models（inputTokenLimit/outputTokenLimit）：https://ai.google.dev/api/models
- 配置文档：https://google-gemini.github.io/gemini-cli/docs/get-started/configuration.html

### 相关 Issue / PR / Discussion
- #20070 反转 context 百分比显示：https://github.com/google-gemini/gemini-cli/issues/20070
- #23165 新增 /context 命令：https://github.com/google-gemini/gemini-cli/issues/23165
- #15426 RemainingTokenCount 漏算 candidatesTokenCount：https://github.com/google-gemini/gemini-cli/issues/15426
- #6106 Context Window percent 计算错误：https://github.com/google-gemini/gemini-cli/issues/6106
- #16973 --resume 后显示 100%：https://github.com/google-gemini/gemini-cli/issues/16973
- #12788 持久 token 使用量显示：https://github.com/google-gemini/gemini-cli/issues/12788
- #14864 hideContextPercentage 不生效：https://github.com/google-gemini/gemini-cli/issues/14864
- #16754 Footer 显示模型用量百分比：https://github.com/google-gemini/gemini-cli/issues/16754
- #12068 为何 COMPRESSION_TOKEN_THRESHOLD 设为 0.7：https://github.com/google-gemini/gemini-cli/issues/12068
- PR #2898 压缩阈值下调：https://github.com/google-gemini/gemini-cli/pull/2898
- #7578 压缩时 maxOutputTokens 超限：https://github.com/google-gemini/gemini-cli/issues/7578
- #26188 / #11947 / #14333 token 估算与剩余量 bug
- #23081 gemini-2.5-pro 漏配 maxOutputTokens：https://github.com/google-gemini/gemini-cli/issues/23081
- Discussion #16067 请求突破 1M 上限：https://github.com/google-gemini/gemini-cli/discussions/16067
