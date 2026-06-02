# 第 41 轮审查结论

## 审查范围

- `internal/platform/shared/ctxutil.go`（NonNilContext、CheckCtx）
- `internal/platform/shared/retry.go`（Retry、RetryWithPolicy、normalizeRetryPolicy、retryDelay、exponentialDelay、applyJitter、waitRetry）
- `internal/platform/config/timeouts.go`（13 个 timeout 常量、6 个 With* helper）
- `internal/platform/shared/jsonutil.go`（DecodeInput、CloneSelector、CloneHookPayload、CloneStrings、CloneStringMap、FilterKeys、CloneRawMessage、CloneJSONMap、CloneRuntimeConfigMap、NormalizeAbsolutePath）
- `internal/platform/shared/safe_go.go`（SafeGo —— 已 deprecated）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `ctxutil.go:12-17` CheckCtx | 静默 | `if ctx == nil { return nil }` | nil ctx 不报错 → caller 误以为 ctx 有效，下游 ctx.Err() 调用都返 nil 让 cancel 失效 | nil ctx 改 panic 或返 ErrNilContext |
| `retry.go:25-27` RetryWithPolicy | 静默 fallback | `ctx == nil` 时 fallback Background | 与 ctxutil.CheckCtx 同问题：nil ctx 被掩盖；retry 永不被取消（因为 Background 永不 Done） | nil ctx 改 panic |
| `retry.go:54-71` normalizeRetryPolicy | 静默 | MaxAttempts<0 → 0；Jitter>1 → 1；BaseDelay<0 → 0 | caller 传错配置（如 MaxAttempts=-1）被静默修正为「立即返回 nil 错误」（loop 不进入），无重试也无错 | 非法值返 error |
| `retry.go:31-52` 主循环 | 弱契约 | MaxAttempts==0 时 loop 不进入，直接 `return lastErr`（lastErr 是 nil） | 调用方期望「至少跑一次」时传 MaxAttempts=0 会得到 nil 错误而 fn 从未执行 | MaxAttempts<1 panic 或文档明确「0=不执行」 |
| `retry.go:32-36` if/else 形式 | 风格 | `if err := fn(); err == nil { return nil } else { lastErr = err }` | 可改 `lastErr = fn(); if lastErr == nil { return nil }` 更清晰 | 重写（minor） |
| `safe_go.go:8-26` SafeGo | 双协程入口 | 与 `internal/util/safego.Go` 重复实现，已标记 deprecated 但仍存在 | 调用方误用 deprecated API；archtest 限制了新增使用，但旧代码可能未迁移 | 标记可见的 deprecation Warning（如 `//lint:ignore SA1019` 或 build tag）；时机成熟后删除 |
| `jsonutil.go:13-19` DecodeInput | 静默 | 空输入或 `null` 时把 input 改成 `{}` 静默走 Unmarshal | caller 传 JSON null 时期望 nil 反序列化，但被静默替换为空对象——dst 字段保留零值看似正常 | 显式语义：null/empty → 返 ErrEmptyInput；caller 决定是否兜底 |
| `jsonutil.go:42-57` FilterKeys | 静默 | 空 keys 时返回原 payload —— 与「过滤」语义相反 | caller 传空白名单期望返回空 map（filter 严格语义），却拿到全部 payload | 改为 keys==nil 返原值，keys==[]string{} 返空 map；或始终返空 |
| `jsonutil.go:48-50` FilterKeys | 静默 | 空字符串 key trim 后跳过 | 调用方传 `[" "]` 期望「无任何字段匹配」，但被静默忽略 → 与传空 keys 行为不同（一个返全量，一个返空） | 直接对每个 key trim 后处理；空 key 视为不匹配（返空） |
| `safe_go.go:18` SafeGo | 静默 | recover 时 `r != nil && logger != nil` —— logger 为 nil 时 panic 完全吞掉 | deprecated API 但仍可能被旧代码用；nil logger 路径吞 panic 无任何痕迹 | logger nil 时 fallback global logger（与新 safego.Go 一致） |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `retry.go:24-52` RetryWithPolicy | 默认 OnRetry==nil 时无任何 retry 可观测性 | 提供默认 OnRetry：每次 retry 输出 Debug 日志带 attempt/delay/error |
| `retry.go:73-83` retryDelay | 指数退避，但 MaxDelay 缺省 0 时无上限 | 文档明确「MaxDelay==0 表示无上限」；运行时 attempt > 10 打 Warn |
| `retry.go:85-98` exponentialDelay | 溢出保护到 maxDuration（约 292 年）—— 实际正常值 | 正确实现，无问题 |
| `timeouts.go:1-56` | 13 个常量、6 个 With* helper —— 但常量值在 ctxutil 包定义，本文件仅 re-export | 当前文件是适配层，无运行时逻辑——无需监控 |
| `jsonutil.go:13-19` DecodeInput | json.Unmarshal 大对象慢；当前无 size 限制 | 与第 33 轮 codec.go 同问题；建议在更上层（HTTP/MCP transport）限制 input size |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `ctxutil.go:13-15` CheckCtx | nil ctx 静默返 nil |
| `retry.go:25-27` | nil ctx fallback Background |
| `retry.go:55-69` normalizeRetryPolicy | 5 个非法配置静默修正 |
| `jsonutil.go:14-17` DecodeInput | 空/null 输入静默替换 `{}` |
| `jsonutil.go:43-45` FilterKeys | 空 keys 静默返原 payload |
| `jsonutil.go:48-51` | 空 key 字符串静默跳过 |
| `safe_go.go:18` | logger nil 时 panic 完全吞掉 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `ctxutil.go:12-17` CheckCtx | nil ctx 视为「无错误」与 ctx.Err() 真实语义冲突 |
| `retry.go:9-15` Policy | 5 字段全部允许零值，零值语义全靠 normalizeRetryPolicy 兜底 |
| `retry.go:17-22` Retry | 简化版接受 maxAttempts/base，不暴露 Jitter/MaxDelay/OnRetry |
| `jsonutil.go:13-19` DecodeInput | input 是否预期可为空靠调用约定 |
| `jsonutil.go:42-57` FilterKeys | nil keys vs empty keys 语义混淆 |
| `safe_go.go:8-26` SafeGo | deprecated 但未在编译期阻止使用 |

## 修复优先级

### P0（必须本周修）
1. **`ctxutil.go:12-17` CheckCtx nil ctx 返 nil 错误**——这是项目内 ctx 检查的统一 helper。如果 caller 传 nil ctx，`CheckCtx` 返 nil 让上层认为 ctx 健康，但所有后续 ctx 操作（cancel、deadline）都失效。这是分布式系统中 cancellation 失效的隐藏根因。改为 nil → panic 或 return 显式错误。
2. **`retry.go:55-71` normalizeRetryPolicy MaxAttempts<0 → 0**——caller 传 -1（错误代码可能从配置 parse 出）会被修正为 0，loop 不进入，retry 直接返回 nil 错误且 fn 从未执行。caller 误以为「retry 成功」实则 fn 没跑。改为非法值返 error。
3. **`retry.go:31-52` MaxAttempts==0 时 fn 从未执行**——配合上面 P0：在「fail-fast 100%」要求下，MaxAttempts=0 应该 panic 或 fn 至少跑一次（重试 0 次但执行 1 次）。当前 lazy loop 让 caller 拿到 nil 错误以为成功，是隐藏故障。

### P1（本月）
4. `retry.go:25-27` RetryWithPolicy nil ctx 改 panic
5. `jsonutil.go:13-19` DecodeInput null/empty 显式语义
6. `jsonutil.go:42-57` FilterKeys nil vs empty keys 语义统一
7. `safe_go.go:8-26` SafeGo logger nil 改 fallback global logger
8. `retry.go:73-83` retryDelay 缺省 MaxDelay==0 行为文档化

### P2（下个 sprint）
9. `safe_go.go` 添加构建期检查阻止新增使用（已有 archtest，但 deprecated 注释可强化）
10. `retry.go` 统一 OnRetry 默认提供 debug 日志
11. `retry.go:32-36` if-else 重写为顺序赋值

## 边界条件

1. **`retry.go` 是项目内 retry 的核心实现**——指数退避 + jitter + 可配置 MaxDelay + OnRetry callback，覆盖了大多数 retry 需求。`exponentialDelay` 的溢出保护（line 92-94）是细致的边界处理，**正面案例**。但整体「错误配置静默修正」哲学与全局 fail-fast 张力——尤其 MaxAttempts<0 → 0 的等效语义可能导致沉默故障。
2. **`jsonutil.go:13-19` DecodeInput 的 null→{} 修补**：很可能是为兼容 LLM 偶发返回 `null` 的实践。但 LLM 返 null 应该是「明确空意图」而非「等同空对象」——比如 tool call 的 args 是 null 时 caller 应该用默认参数，而非空对象。当前修补可能掩盖 LLM 返回错误。建议加 caller-side 区分：DecodeOptional（接受 null/empty）vs DecodeStrict（拒绝 null/empty）。
3. **`jsonutil.go:42-57` FilterKeys 的设计意图**：从函数名看是「从 payload 中过滤出 keys 列表中的字段」。但 line 43-45 「空 keys 返原 payload」是相反语义。可能的解释：「未指定过滤条件 = 返回全部」是 SQL `WHERE` 的语义。若如此，应改名为 `MaybeFilterKeys` 或在文档明确「empty keys = no filter applied」。当前命名误导。
4. **`config/timeouts.go` 是 facade 模式正面案例**：13 个 timeout 常量 + 6 个 helper 全部 re-export 自 `internal/util/ctxutil`。这是良好的依赖管理——`platform/config` 提供稳定 API 给上层（mcp-orch / mcp-lsp），底层实现细节（util/ctxutil）可以独立演化。**正面架构案例**。
5. **`shared/safe_go.go` 已 deprecated 但仍在文件树中**：注释明确说「as of 2026-04-18 no in-tree call sites remain and the TestSafeGoUsageCentralized archtest blocks regressions」。在 archtest 守护下保留 deprecated API 是合理的（外部 import 可能依赖），但应加 build tag `//go:build legacy` 或标注计划删除日期。当前注释只说「kept for backward compatibility」无明确生命周期。
6. **`ctxutil.go` 仅 18 行 + 2 个函数**：与名字暗示的「ctx 工具集」不匹配——大部分 ctx helper 在 `config/timeouts.go` 或 `util/ctxutil`。建议合并：`shared/ctxutil.go` + `config/timeouts.go` → 统一到一个包。当前分散让 reviewer 难以定位 ctx 处理逻辑。
7. **`retry.go` 与 `internal/util/safego/safego.go` 都不会汇报失败**：retry 失败返回 lastErr，但调用方（如 `wakeup_reclaim.go:95`，第28轮发现 `_, _ = r.ReclaimOnce(ctx)`）可能丢弃错误。retry 自身正确，但调用链下游可能消化错误。这是组合 anti-pattern——单个 helper fail-fast 但调用方 fail-soft 让效果归零。建议在 OnRetry 中加 metrics counter，让运维至少能看到 retry 频率。

---

**本轮总结**：发现 3 个 P0 问题集中在 ctx/retry 基础设施：①CheckCtx 把 nil ctx 视为无错误，导致 cancellation 失效；②normalizeRetryPolicy 把非法 MaxAttempts 静默修正为 0；③MaxAttempts=0 时 fn 从未执行但返 nil 错误的隐藏故障。`config/timeouts.go` 是 facade 模式正面案例。`safe_go.go` deprecated API 应加生命周期标注。retry 与 ctxutil 模块应合并以集中 ctx 处理。

**累计进度**：41 轮完成。cron `fd4b4728` 继续推进。
