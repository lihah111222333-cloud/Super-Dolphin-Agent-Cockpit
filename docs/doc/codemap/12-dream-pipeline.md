# 12 Dream Pipeline 代码地图

> 拆卷说明：dream executor pipeline 横跨 `internal/provider/{unified,claudecli,codexapp,dreamexec}` 与 `pkg/dreammetrics` 多个包；它是 provider 侧的可选批处理能力，不代表已存在 memory 持久化 owner。
> 当前口径：以 2026-04-29 HEAD 为准。
> 与 `11-memory.md` 的边界：本卷只写 dispatcher → provider → binary 的 dream 执行链路；memory 触发、写盘和检索链已从当前生产树移除，见 `11-memory.md` §1–§4。

## 1. 这卷回答什么

- provider 或显式调用方如何使用 dream executor：dispatcher 选 provider → 调真 LLM → 解 JSON；本卷不声称后续会自动写入 memory。
- dispatcher 层 4 项底盘保障：5min timeout / 256KB prompt cap / DREAM_PROVIDER_ORDER env / 5 outcome + 3 token atomic metrics counters。
- 双 provider failover 真链路：claudecli `claude -p` + codexapp `codex exec` 各自 thin wrapper + 共享 `dreamexec` 子进程层。
- 测试金字塔：单元 → provider 单 e2e → dispatcher failover e2e；旧 stop-hook → write 场景仅属历史材料。

## 2. 当前源码结论

1. **dispatcher 是单例 fan-in，不是 fx group 工厂**：`unified.NewDreamExecutor(providers, logger)` 字母序聚合 `[]contract.DreamExecutorProvider`，按 `DREAM_PROVIDER_ORDER` env 解析后逐个尝试。
2. **failover 语义清晰二分**：单 provider 返回 `ErrDreamExecutorNotConfigured` → 跳过；返回真错误 → 立即短路。`AllNotConfiguredTotal` metric 区分两条失败路径。
3. **provider 是 thin wrapper**：claudecli/codexapp 各 ~85 行，核心子进程边界（exec / stdin / stdout cap / stderr 收集 / fence strip / JSON 提取 / retry）抽到 `dreamexec` 公共包。
4. **binary not available 哨兵 error 集中**：`dreamexec.ErrBinaryNotAvailable` 覆盖 PATH 查找失败 + 绝对路径不存在 + 字符串 fallback 三种场景，provider 层用 `errors.Is` 简化判断。
5. **timeout 集中管理**：`DreamConsolidationTimeout = 5 * time.Minute` 在 `platform/config/timeouts.go`（TimeoutLocality 规范），dispatcher 通过 `platformconfig.WithTimeout` wrapper 注入。
6. **metrics in-process**：`pkg/dreammetrics` 现在是 8 个 `atomic.Uint64` counter（5 个 outcome + 3 个 token usage），未接 Prometheus exporter（C 类长期路线）。

## 3. 真实包图与职责

| 路径 | 职责 | 核心锚点 |
|---|---|---|
| `internal/provider/unified/dream_executor.go` | dispatcher: failover / timeout / size cap / metrics / 日志 | `:138 ExecuteDream` `:143 ExecuteDreamWithOptions` `:161 preflight` `:178 runFailover` |
| `internal/provider/dreamexec/dreamexec.go` | 公共子进程层 + Commander 接口 + Run 整合 | `:38 Commander` `:52 NewRealCommander` `:153 Run` |
| `internal/provider/dreamexec/parse.go` | JSON fence 移除 + balanced object 提取 | `:30 StripJSONFences` `:54 ExtractFirstJSONObject` `:69 findBalancedJSONEnd` |
| `internal/provider/claudecli/dream_executor.go` | claude `-p` batch 真实现 | `:55 ExecuteDream` `:32 newDreamExecutor` |
| `internal/provider/codexapp/dream_executor.go` | codex `exec` batch 真实现 | `:60 ExecuteDream` `:30 newDreamExecutor` |
| `pkg/dreammetrics/dreammetrics.go` | 8 atomic counter（5 outcome + 3 token）+ Snapshot/Read + ResetForTesting | `:17 outcome atomic` `:27 token atomic` `:83 Snapshot` `:95 Read` |
| `internal/platform/config/timeouts.go` | timeout 常量集中 | `:21 DreamConsolidationTimeout` |
| `internal/contract/dream.go` | DreamExecutor 接口 + ErrDreamExecutorNotConfigured 哨兵 | `:8 sentinel` `:10 接口` |

## 4. 完整调用链

```
explicit dream caller
  → unified.dreamExecutor.ExecuteDream / ExecuteDreamWithOptions
  → preflight + timeout + provider-order resolution
  ↓
unified.dreamExecutor.ExecuteDream (dispatcher 入口)
  ├─ preflight: 空 prompt 校验 + 256KB cap + IncPromptOversize
  ├─ ctx.Err() check
  ├─ platformconfig.WithTimeout(5min) wrap ctx
  └─ runFailover: 按字母序/env order 试 providers
       ├─ provider.ExecuteDream(ctx, prompt) ─→ claudecli or codexapp thin wrapper
       │    └─ dreamexec.Run(ctx, commander, opts)
       │         ├─ realCommander.Run(binary, args, stdin → cmd.Stdin)
       │         │    ├─ exec.CommandContext (ctx 取消即 Kill)
       │         │    ├─ stdout limitedWriter (256KB cap)
       │         │    ├─ stderr 4KB preview
       │         │    └─ binary not available → ErrBinaryNotAvailable
       │         ├─ StripJSONFences (raw 输出)
       │         ├─ ExtractFirstJSONObject (brace balance + 字符串词法)
       │         └─ retry MaxRetries 次（仅 parse 失败 retry，cmd 错不 retry）
       │
       ├─ NotConfigured (binary not avail / auth) → IncProviderSkipped + 试下一个
       ├─ 真错误 (rate limit / network) → IncProviderFailed + 立即短路
       └─ 全链路 NotConfigured → IncAllNotConfigured + 返回 last error

返回 provider raw JSON (string, error) ──→ 显式调用方；当前树不再提供 memory extract/write/index 后处理链
```

## 5. dispatcher 4 项底盘

| 项 | 实现 | 锚点 |
|---|---|---|
| 5min timeout | `platformconfig.WithTimeout(ctx, defaultDreamTimeout)`，0 时跳过（测试可注入） | `internal/provider/unified/dream_executor.go:143-157` |
| 256KB prompt cap | `len(prompt) > maxPromptBytes` fail-fast，`Warn + IncPromptOversize` | `internal/provider/unified/dream_executor.go:159-174` |
| DREAM_PROVIDER_ORDER env | `resolveProviderOrder(registered, override)` 纯函数：列出已注册的按 CSV 顺序在前，剩余字母序补后 | `internal/provider/unified/dream_executor.go:101-135` |
| 8 metrics counter | outcome: `Success / ProviderSkipped / ProviderFailed / AllNotConfigured / PromptOversize`；token: `TokensInput / TokensOutput / TokensCacheRead` | `pkg/dreammetrics/dreammetrics.go:17-29` |

## 6. Provider 真实现矩阵

| Provider | binary 默认 | env override | model env | args |
|---|---|---|---|---|
| claudecli | `claude` (PATH) | `CLAUDE_CLI_BIN` | `DREAM_CLAUDE_MODEL` | `-p` (+ `--model X` 可选) |
| codexapp | `codex` (PATH) | `DREAM_CODEX_BIN` | `DREAM_CODEX_MODEL` | `exec` (+ `--model X` 可选) |

两 provider 都通过 stdin 写 prompt（256KB 远超 OS ARG_MAX），通过 stdout 读真 LLM 响应；继承父进程 OAuth 凭据（`~/.{claude,codex}`）；prompt 自带 JSON 契约（`consolidation_prompt.go:76`），无需附加 system prompt。

## 7. 错误映射策略

| 来源 | dispatcher 行为 | metric |
|---|---|---|
| `dreamexec.ErrBinaryNotAvailable` | provider wrapper 包成 `ErrDreamExecutorNotConfigured` → 跳过 failover | `IncProviderSkipped` |
| `cmd.Run` 退出码非 0 + 非 binary not avail | 透传给 dispatcher → 真错误立即短路 | `IncProviderFailed` |
| `ctx.Err()` (timeout / cancel) | 透传给 dispatcher → 立即返回 | （不分类，看上层 metric） |
| JSON parse 失败 | dreamexec 内 retry MaxRetries 次，仍失败返回最后 parse error | （不分类） |
| 全链路 NotConfigured | `IncAllNotConfigured` + 返回最后一个 NotConfigured 错误 | `IncAllNotConfigured` |

## 8. 端到端 manual test 矩阵（build tag manual）

| Test | 范围 | 实测延迟 |
|---|---|---|
| `claudecli/dream_executor_manual_test.go` | claude 单 provider e2e | 3.62s |
| `codexapp/dream_executor_manual_test.go` | codex 单 provider e2e | 5.21s |
| `codexapp/dream_failover_external_manual_test.go` | dispatcher failover (claude 不可用 → codex 成功) | 3.61s |
| （已移除的旧 memory stop-hook e2e） | 历史 stop hook → write 场景，仅保留在迁移材料 | — |

跑法：`go test -tags=manual -run TestManual<Name> -v ./<package>/`

## 9. 关键架构事实速查

- failover order：默认字母序 `claude` < `codex`；env override 通过 `resolveProviderOrder` 解析；未识别 provider 会立即返回错误并阻断启动（`internal/provider/unified/dream_executor.go:101-135`）
- 子进程隔离：dream 不复用 unified.SessionManager，避免污染 UI 事件流 + 历史；每次 dream 付 ~1-2s 子进程启动开销，由 SafeGo 后台触发对用户不可见
- prompt 已自带 JSON 契约：`consolidation_prompt.go:69 buildConsolidationPrompt` 在 Phase 1 区块显式要求模型返回严格 `{"memories":[{"content","type","tags"}]}`，无需附加 system prompt
- parse 容忍：`parseExtractedMemories` (`extract.go:67`) 容忍 envelope/list/single 三种 schema，dreamexec.ExtractFirstJSONObject 处理 prose preamble + fence + 嵌套 + 字符串内 brace
- panic 隔离：路径选择 ephemeral spawn（非 in-process SessionManager）天然把外部 LLM 子进程 panic 隔离在进程边界，不需要 Go 层 recover

## 10. 与其他卷的边界

- 旧 memory 触发/写盘段已退役：见 `11-memory.md` §1–§4 的移除结论
- prompt 输入与 provider-neutral assembly：见 `11-prompt-thread.md`
- thread / session 绑定（codex_home / instance_key）：见 `11-prompt-thread.md`
- 配置真值：order / timeout / cap / fail-fast 见 `internal/provider/unified/dream_executor.go:19-30`、`:50-84`、`:101-178`；provider binary/model 见 `internal/provider/claudecli/dream_executor.go:15-51` 与 `internal/provider/codexapp/dream_executor.go:16-55`

## 11. 历史阶段对照（session p25）

| Phase | 内容 | 当前落点 |
|---|---|---|
| B-2 | dispatcher 单元测试 + 5 结构化日志点 + capturing logger 测试 | `unified/dream_executor_test.go` 16+ case |
| B-3.1 | 5min dispatcher timeout 注入 | `defaultDreamTimeout` + `platformconfig.WithTimeout` |
| B-3.2 | 5 outcome metrics counter（仿 skillmetrics）；后续已增 3 个 token counter | `pkg/dreammetrics` |
| B-3.3 | DREAM_PROVIDER_ORDER env 解析（最小版本） | `resolveProviderOrder` 纯函数 |
| B-3.4 | 256KB prompt size cap | `defaultMaxPromptBytes` |
| B-4.1 | dreamexec 公共子进程层 | `internal/provider/dreamexec/` |
| B-4.2 / B-4.3 | claudecli + codexapp thin wrapper | `*/dream_executor.go` |
| B-4.4 | binary detection bug + failover e2e | `dreamexec.ErrBinaryNotAvailable` 哨兵 |
| B-4.5 | stop hook 完整 e2e | 历史场景；当前不再有对应 production caller |
| B-4.6 | archtest 3 项违规修复 | `ExecuteDream` 拆 3 函数 + `ExtractFirstJSONObject` 拆 3 函数 + timeout 迁移 |
| B-4.7 / B-4.9 | binding fixture 字段对齐（latent fix） | `capturingBindingStore` + `stubBindingStore` |
| B-4.8 / B-4.9 | dream provider 配置真值 | `internal/provider/unified/dream_executor.go:19-178`、`internal/provider/dreamexec/dreamexec.go:77-145` |
