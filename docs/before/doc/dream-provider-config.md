# Dream Provider 配置与运维

记忆系统 auto-dream consolidation 的 provider 配置、启用流程、调试方法。

## 默认 OFF 安全说明

记忆系统**默认完全关闭** — 用户必须显式启用至少一个 env var 才会触发任何 LLM 调用。

```
Config.Enabled                = false  (config.go:108)
Config.ExtractOnStop          = false  (config.go:115)
ResolveMemoryGate.AutoEnabled = 受 settings + CLAUDE_CODE_DISABLE_AUTO_MEMORY 控制 (gate.go:74,102)
```

3 重守门 + 全 false → 不会有「用户被静默扣 tokens」风险。

## 启用流程（最小可用）

```bash
# 1. 总开关
export ENABLE_MEMORY_SYSTEM=1

# 2. thread.stopped 触发提取
export MULTI_AGENT_MEMORY_EXTRACT_ON_STOP=1

# 3. 确保至少一个 provider binary 在 PATH 且已登录
which claude  # 默认 PATH 查找
which codex
# 或自定义路径：
export CLAUDE_CLI_BIN=/usr/local/bin/claude
export DREAM_CODEX_BIN=/usr/local/bin/codex
```

## 完整 env vars 矩阵

### 启用守门（4 项）

| Env | 默认 | 含义 |
|---|---|---|
| `ENABLE_MEMORY_SYSTEM` | `0` | 记忆系统总开关 |
| `ENABLE_MEMORY_TOOLS` | `0` | 暴露 memory tools 给 LLM（独立于总开关，config.go:109） |
| `MULTI_AGENT_MEMORY_EXTRACT_ON_STOP` | `0` | thread.stopped 触发自动 dream consolidation |
| `CLAUDE_CODE_DISABLE_AUTO_MEMORY` | `unset` | 设为任意值后第 3 重守门强制关闭 AutoEnabled |

### Dream provider 配置（5 项）

| Env | 默认 | 含义 |
|---|---|---|
| `DREAM_PROVIDER_ORDER` | 字母序 (`claude` < `codex`) | failover 顺序覆盖，CSV 格式如 `"codex,claude"` |
| `CLAUDE_CLI_BIN` | `claude` (PATH 查找) | claude binary 路径 |
| `DREAM_CLAUDE_MODEL` | binary 默认 | claude `--model` 参数（可选） |
| `DREAM_CODEX_BIN` | `codex` (PATH 查找) | codex binary 路径 |
| `DREAM_CODEX_MODEL` | binary 默认 | codex `--model` 参数（可选） |

### 凭据要求

- **claude**: `~/.claude` OAuth（`claude login`）
- **codex**: `~/.codex` OAuth（`codex login`）或 `OPENAI_API_KEY` env
- **codex multi-profile**: `CODEX_HOME` env override 路径；binding 层记录 `CodexInstanceKey` + `CodexModelProvider` 区分多 profile (GLM / Kimi / OpenAI)
- dream 子进程继承启动用户环境变量 + home 目录，无额外 dream-specific auth

## 触发条件

dream consolidation 同时满足以下条件才真正触发：

1. `Config.Enabled = true`（`ENABLE_MEMORY_SYSTEM=1`）
2. `Config.ExtractOnStop = true`（`MULTI_AGENT_MEMORY_EXTRACT_ON_STOP=1`）
3. `ResolveMemoryGate.AutoEnabled = true`（settings 层级未关）
4. thread.stopped 事件 + `sessionCount ≥ 5` + 距上次 consolidation `≥ 24h`（避免频繁调用）

代码引用：`internal/module/memory/auto_dream_task.go:60` `startDreamTask`

## Dispatcher 行为

dispatcher (`internal/provider/unified/dream_executor.go`) 按 `DREAM_PROVIDER_ORDER` 列出顺序逐个尝试 provider：

- **timeout**: 5min 兜底（`platform/config/timeouts.go DreamConsolidationTimeout`）
- **prompt size cap**: 256KB（fail-fast 防爆）
- **failover 语义**: 单 provider 返回 `ErrDreamExecutorNotConfigured` (binary 不存在/未登录) → 跳过试下一个；返回真错误 (auth/network/rate-limit) → 立即短路不再尝试
- **retry**: parse 失败 retry 1 次，dispatcher 层不重试 LLM 调用

## 可观测性 — Dispatcher Metrics

`pkg/dreammetrics` 5 个 atomic counter（in-process，未接 Prometheus）：

| Counter | 含义 |
|---|---|
| `SuccessTotal` | 单次 dream 成功（dispatcher 命中某 provider 返回非 nil） |
| `ProviderSkippedTotal` | 单 provider NotConfigured 跳过（failover 路径累加） |
| `ProviderFailedTotal` | 单 provider 真错误（dispatcher 立即短路） |
| `AllNotConfiguredTotal` | failover 链路全部 NotConfigured，整轮失败 |
| `PromptOversizeTotal` | prompt 超 256KB cap 被 fail-fast 拒绝 |

读取（in-process）：`dreammetrics.Read() Snapshot`

## 结构化日志

dispatcher 5 日志点（`*slog.Logger`）：

| Level | Message | 字段 |
|---|---|---|
| Debug | `dream executor registered` | `providers=[claude codex]` |
| Info | `dream executor succeeded` | `provider`, `size_bytes` |
| Debug | `dream executor skipped (not configured)` | `provider` |
| Warn | `dream executor failed` | `provider`, `error` |
| Warn | `all dream executors not configured` | `providers` |
| Warn | `dream prompt exceeds size limit` | `size_bytes`, `max_bytes` |
| Info | `dream provider order overridden` | `env`, `resolved` |

## 故障排查

| 症状 | 原因 | 处理 |
|---|---|---|
| 永远不触发 dream | 任一默认 false env 未开 / `CLAUDE_CODE_DISABLE_AUTO_MEMORY` 被设 | 检查 4 项启用守门 |
| 日志全部 `(not configured)` | binary 不在 PATH 或未登录 | `claude login` / `codex login` 或设 `CLAUDE_CLI_BIN` / `DREAM_CODEX_BIN` |
| `binary not available: <path>` | `*_CLI_BIN` env 写错 / binary 不存在 | 检查 env 路径 + `which` 验证 |
| 日志 `dream prompt exceeds size limit` | memory 累积超 256KB silent fail (PromptOversizeTotal++) | 调 `dreammetrics.PromptOversize` / 清理 topic 文件 |
| `DREAM_PROVIDER_ORDER` 不生效 | 未识别名被静默忽略 | 检查启动时 `dream provider order overridden` Info 日志的 `resolved` 字段 |
| dream 跑超 5min | LLM 真 hang / 网络 | dispatcher timeout 自动 cancel；检查网络 / quota |
| metrics `ProviderFailedTotal` 上升 | rate limit / quota / auth / network | 检查 `~/.claude` / `~/.codex` 凭据 + logger Warn 错误内容 |

## 引用代码

- 接口：`internal/contract/dream.go:10-12`
- Dispatcher：`internal/provider/unified/dream_executor.go`
- Provider 实现：`internal/provider/claudecli/dream_executor.go` + `internal/provider/codexapp/dream_executor.go`
- 公共子进程层：`internal/provider/dreamexec/`
- 触发链路：`internal/module/memory/auto_dream_task.go` + `auto_dream_scheduler.go`
- 配置：`internal/module/memory/config.go`
- 端到端 manual test：
  - `internal/provider/claudecli/dream_executor_manual_test.go`
  - `internal/provider/codexapp/dream_executor_manual_test.go`
  - `internal/provider/codexapp/dream_failover_external_manual_test.go`（dispatcher failover）
  - `internal/module/memory/auto_dream_e2e_manual_test.go`（stop hook → write 完整链路）
