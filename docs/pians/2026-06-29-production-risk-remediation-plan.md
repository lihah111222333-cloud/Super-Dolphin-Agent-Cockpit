# Global Production Risk Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-06-29 全域生产风险审查中经交叉裁决保留的生产可达风险和发布门禁风险，禁止把配置错误、权限错误、协议漂移、发布缺口和诊断缺失静默降级为成功。

**Architecture:** 使用一次性并行 fanout：每个保留风险簇在独立 `codex/` 分支 worktree 中同时修复、验证、本地提交，主控只负责审查、去重、冲突裁决和最终合并。每个生产修复都必须配一个上层防御点：DTO/facade/schema/app graph/CI/pre-push/UI 阻断，确保同类漂移不能再次穿透到生产路径；被裁定为 guard-only / evidence-only 的条目只进入对应门禁或证据队列，不得伪装成 runtime 缺陷。

**Tech Stack:** Go 1.25.7, SQLite/sqlc, fx, jrpc2/MCP, Wails, React/Vite, Node 20, shell/PowerShell packaging scripts.

**Verification Surface:** `./scripts/test_with_guard.sh`, affected Go packages, `make guard`, `make sqlc-verify`, `make codemap-check`, `make frontend-embed-verify`, `python3 scripts/validate_super_agent_skills.py`, `go test ./scripts -run 'Guard|Package|Release|Frontend|Evidence' -count=1`, `./scripts/ci_commit_guard.sh`, `.githooks/pre-push` dry-run coverage where supported, `(cd frontend-app && npm run lint && npm test && npm run build)`, and a validator-backed evidence ledger that maps every active production/release queue ID to red/green commands and lane commits, while recording adjusted readiness, guard-only, and evidence-only IDs separately.

---

## Scope And Rules

- 本文档按用户指定路径写入 `docs/pians/`；不要顺手移动到 `docs/plans/`。
- `cmd/agent-terminal/web-dist/` 是生成 embed 输出，不直接手改；修复前端从 `frontend-app/` 入手，再用既有 build/sync 生成。
- P0/P1、生产可达 P2、发布/CI 门禁项和明确保留的 P3 支撑项进入同一次并行修复队列；不得按严重级别延期处理。交叉裁决已降级的 guard-only / evidence-only 条目必须在对应门禁或证据 lane 中关闭，不得派成 runtime 修复。
- 每个并行 lane 必须在独立 worktree 和独立分支中执行，分支名使用 `codex/risk-<lane>-20260629`，路径使用 `.worktrees/risk-<lane>-20260629`。
- 主工作区只做调度和审查，不直接实现；子 worktree 可以本地提交，最终合并权留给主控。
- 每个修复必须先写失败测试或门禁验证，再改实现，再跑对应命令。禁止靠“应该不会”关闭条目。
- fanout 前必须让本计划对所有 worktree 可见：优先提交到协调分支；若不能提交，必须把同一份只读快照复制到每个 worktree 并记录 `sha256`。未满足时不得派发 agent。
- 当前主工作区 dirty 文件一律先记录并隔离；若 dirty 文件落在某个 lane 的 owns 内，主控必须先决定“纳入该 lane 基线”或“等待该变更提交/撤离”，不得让 worker 覆盖未知改动。

## Cross-Adjudication Corrections

2026-06-29 三代理只读交叉裁决后，本计划以下列结论为执行事实源：

| Category | IDs | Execution Decision |
|---|---|---|
| Production runtime | 60 items, including adjusted readiness item `P1-07` | 保留在 runtime、app graph、provider、store、frontend、observability 或 orchestration lane 中修复；每项需要代码或上层门禁证据。`P1-07` 不按原始缺服务描述盲修，只补 production readiness/diagnostic probe。 |
| Release / CI gate | 9 items | 保留在 release、package、CI、pre-push、embed、DTO parity 或 skill mirror lane 中修复；每项需要门禁证据。 |
| Guard / test governance only | `P1-32`, `P2-01`, `P2-24`, `P2-27`, `P2-28`, `P3-04` | 不作为生产 runtime 缺陷派给业务修复 lane。`P1-32`、`P2-24`、`P2-27`、`P2-28`、`P3-04` 属 guard/test governance；`P2-01` 只保留 constructor/app graph guard 或回归测试。 |
| Evidence-only | `P3-07` | 只作为执行证据索引和 validator 规则，不创建生产代码修复任务。 |

上层防护裁决：三代理未确认任何 ID 已具备完整 `PRESENT` 防护。`P0-01`, `P1-04`, `P1-06`, `P1-07`, `P1-09`, `P1-24`, `P1-26`, `P1-31`, `P1-32`, `P2-08`, `P2-25`, `P2-26`, `P2-28` 只有局部防护，仍需补齐对应 readiness、CI、guard 或 runtime 阻断；`P2-01` 和 `P3-07` 的上层防护不适用。其余大多数 P0-P2 条目按无上层防护处理。普通 controller/worktree fanout 即可，只有真实 `cmd/mcp-orch` wakeup/lease/timeout/shutdown/index 项需要 DAG/lease 语义。

## Severity Queue

### P0 Queue

| ID | Risk | Best Fix | Upper-Layer Defense | Validation |
|---|---|---|---|---|
| P0-01 | `internal/platform/toolbridge/stdio_mcp_client.go:64` runtime/dynamic MCP stdio 可启动任意本地命令，且继承宿主 secret env。 | 禁止 runtime/turn snapshot 直接创建任意 stdio MCP；所有 stdio MCP 统一复用 `internal/module/mcp_server/service.go` 的 allowlist/approval 规则；stdio 子进程使用最小 env allowlist，只传必要 PATH/HOME/TMPDIR 和明确允许的 provider 变量。 | 需要。`internal/provider/shared/config_helpers.go` 在解析 `mcpConfig` 时先产出 untrusted marker，`toolbridge` 启动前强制校验；frontend/API 不允许直接提交 stdio command，必须引用已登记 server id。 | 新增任意 command、npx 追加参数、secret env 继承的负向测试；运行 `./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/shared ./internal/module/mcp_server -count=1`。 |

### P1 Queue

| ID | Risk | Best Fix | Upper-Layer Defense | Validation |
|---|---|---|---|---|
| P1-01 | `internal/provider/shared/config_helpers.go:281` runtime HTTP MCP 未校验公网 URL/header，可请求 localhost/内网/元数据地址。 | HTTP MCP 配置复用 `internal/platform/httpegress` URL、DNS/IP、redirect、unsafe header 校验；managed local peer 与用户配置 HTTP MCP 分类型处理。 | 需要。provider config DTO 加 `trustedManagedPeer`/`externalRuntimePeer` 区分，外部配置默认 deny private network。 | `./scripts/test_with_guard.sh ./internal/provider/shared ./internal/platform/toolbridge ./internal/platform/httpegress -count=1`。 |
| P1-02 | `internal/provider/claudecli/transport_config.go:416` 未知 `approval_policy/approvals` 默认映射到 `bypassPermissions`。 | provider 启动前严格枚举 approval/sandbox；未知值返回错误，不进入 provider launch。 | 需要。`thread/start.config` 层禁止安全敏感 alias 绕过规范化字段。 | Claude config table test 覆盖 unknown approval、unknown sandbox、known values。 |
| P1-03 | `internal/provider/dreamexec/dreamexec.go:73` dream runner 直接启动 provider CLI，未强制 no-tools、只读 sandbox、env allowlist。 | 优先改为 tool-disabled model API；若继续 CLI，必须使用临时 cwd、no-tools/readonly sandbox、最小 env、拒绝提权。 | 需要。memory/dream 调度入口对 provider runtime 做 deny-tools capability check，不能只靠 CLI 默认。 | 恶意 memory/transcript 注入工具调用的回归测试。 |
| P1-04 | `internal/module/thread/stop.go:290` Stop/Archive/Delete 在终态落库前解除 resume 阻断，并发 Resume 可复活 session。 | `stopThreadRuntime` 返回 release func，由 Stop/Archive/Delete 在状态、binding、turn cleanup、事件发布全部完成后释放。 | 需要。Resume/backgroundResume 在读取状态后再次确认 blocker generation。 | Stop/Archive/Delete racing Resume 并发测试。 |
| P1-05 | `internal/provider/unified/session_resolver_auto_resume.go:74` auto-resume 构造 `ResumeSessionRequest` 时缺 `PromptSnapshot`。 | 复用 thread resume snapshot hydration；缺失、hash 不匹配或版本非法时 fail-fast。 | 需要。provider `ResumeSession` adapter 要拒绝空 snapshot，不能只信上游。 | unified auto-resume 测试断言 provider 收到非空 snapshot。 |
| P1-06 | `internal/module/thread/lifecycle.go:420` provider resume 成功后持久化失败不清理 runtime，形成 ghost session。 | `persistResumedSession` 任意持久化失败都调用 `stopAgent`/`RemoveSession`/close session 后返回原错误。 | 需要。unified client 记录 pending-resume，未 durable commit 前不对外宣告 active。 | store 写失败、binding 写失败的 ghost-session 测试。 |
| P1-07 | ADJUSTED: `internal/app/thread_orchestration_adapter.go:31` 缺 `OrchestrationService` 时会注入 missing facade，但该 facade 在 `LaunchAgent`/`StopAgent`/`Recover` 调用期返回显式错误，不是静默成功。剩余风险是生产启动期 readiness 未把该能力缺口提前暴露。 | 保留 missing facade 的调用期 fail-fast；只在生产 app graph / provider lifecycle readiness 上补显式 probe 或测试，证明合法 no-orchestration/test 模式不会被误伤。不得无证据改成全局 app 启动硬失败。 | 已有调用期防护；需要补启动期/诊断防护。 | `go test ./internal/app ./internal/contract -count=1`，新增测试断言 missing facade 显式报错，并验证生产 readiness 对真实缺口可见。 |
| P1-08 | `internal/app/toolbridge_adapters.go:259` Codex/toolbridge 关键依赖 optional，坏装配延迟到 tool call 才失败。 | 生产 app 图中 `ServerManager`、`DriverFactory`、`Handler` 必需；测试/no-Codex 模式用显式 stub module。 | 需要。provider 启动前执行 toolbridge readiness check。 | app fx graph tests 覆盖缺依赖 fail-fast。 |
| P1-09 | `cmd/mcp-orch/orchestration/wakeup_dispatcher.go:199` wakeup lease 不 heartbeat，长任务可能被 reclaimer 重派。 | Route/automation 执行期间 lease heartbeat 到 terminal write；或持久化 running fence 让 reclaimer 跳过活跃节点。 | 需要。node execution 层记录 idempotency key，重复 dispatch 必须拒绝。 | 慢命令超过 lease 的单执行测试。 |
| P1-10 | `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:104` timeout 配置持久化但未作用到 command context。 | 合并 DAG metadata 默认值与 node config 覆盖值，用 `context.WithTimeout` 包住 automation/agent 执行。 | 需要。UI/API 保存 execution config 时展示实际 timeout 来源，禁止“配置已保存但不生效”。 | 慢命令被 kill 的回归测试。 |
| P1-11 | `internal/platform/toolbridge/stdio_mcp_client.go:113` 和 `http_mcp_client.go:125` 把 malformed `tools/list` 当空工具集。 | 抽出严格 decoder：`tools` 必须存在且为数组，tool name 非空，schema 类型合法。 | 需要。provider-facing tools/list 遇到 peer malformed 要阻断 turn，而不是空工具继续。 | missing/null/empty-name 负向测试。 |
| P1-12 | `internal/platform/toolbridge/proxy.go:311` orch peer 失败但 host tools 非空时返回部分工具成功。 | provider-facing `tools/list` 对 peer down 返回 JSON-RPC error，或显式 degraded schema 并阻断启动/turn。 | 需要。frontend/diagnostics 显示 tool surface degraded，不能隐藏成工具少。 | 修改 `handler_shard13_tools_list_test.go` 期望。 |
| P1-13 | `internal/provider/claudecli/session_events.go:322`、`internal/provider/codexapp/event_map.go:75` 记录 raw provider event/payload。 | 不记录 raw line/payload；只记录 event type、session、size、hash、安全字段名。 | 需要。统一 logger redaction 测试覆盖 provider event。 | secret payload 负向测试。 |
| P1-14 | `internal/platform/toolbridge/proxy.go:397`、`internal/platform/bus/sink.go:163` tool/event preview 可完整落参数、结果、用户输入。 | 引入 bounded/redacted preview helper；超限内容只给 hash/size 和安全持久化引用。 | 需要。bus/log sink 对 `slog.Any` struct 做递归脱敏或禁止记录完整 event。 | bus/toolbridge secret 不落日志测试。 |
| P1-15 | `internal/store/sharedfile/store.go:124`、`cmd/mcp-orch/store/sharedfile/store.go:77`、`cmd/mcp-orch/store/sharedfile/importer.go:48` 吞掉 `.gitignore` 保护失败。 | `sharedfilegitignore.Ensure` 失败必须在写盘/DB 前返回错误；失败不得留下文件或 index。 | 需要。sharedfile service 对 `_internal` 写入前执行 explicit protected-root check。 | `.gitignore` 不可读/不可写失败注入测试。 |
| P1-16 | `internal/platform/db/tx.go:32` `WithImmediateTx` 不是真正 `BEGIN IMMEDIATE`。 | 使用单独 `*sql.Conn` 显式执行 `BEGIN IMMEDIATE`/`COMMIT`/`ROLLBACK`，或 DSN `_txlock=immediate` 并测试锁语义。 | 需要。prompt 并发提交入口保留 idempotency/fence，不只依赖 DB 锁。 | 并发锁语义测试，不只跑现有 prompt test。 |
| P1-17 | `internal/platform/db/sqlite/migrations/001_baseline.sql:128` `prompt_templates.match_when NOT NULL DEFAULT '{}'` 把 nil/null 变成永远匹配。 | 允许 NULL，sqlc 参数 nullable，store 保留 nil；`{}` 只表示显式 match-all。 | 需要。prompt write API 对 `match_when` 非 object/null 直接拒绝。 | SQLite 集成测试覆盖 `match_when:null` 不进入 auto route。 |
| P1-18 | `internal/module/thread/router_resolve.go:56` 找不到 `main/default` 或可用模板时继续启动空 system prompt。 | 无显式 `BaseInstructions` 且无模板时返回错误；反转现有“provider bundled prompt”测试。 | 需要。thread start API 在响应里暴露 routing failure，不进入 provider launch。 | router resolve 缺默认模板 fail-fast 测试。 |
| P1-19 | `internal/module/uistate/builtin_tools.go:178` 读取 builtin tool 偏好失败回退默认禁用集。 | resolver 返回 `([]string, error)`，区分 not-found 与读/解码错误；读失败 fail closed。 | 需要。StartAssembly 汇总 suppressed tools 时保留 blocking diagnostic。 | `prefs.GetValue` 非 not-found 错误测试。 |
| P1-20 | `internal/module/memory/extract_metadata.go:80`、`internal/store/thread/session_adapter.go:49` `config_override.runtime` 解码失败当 nil runtime。 | decode 返回 `(runtime, error)`；extract/resume/auto-dream 遇坏 JSON fail closed。 | 需要。thread/session config 写入层加 JSON schema/known-field guard。 | malformed config_override 测试覆盖 extract 和 resume。 |
| P1-21 | `internal/platform/hooks/resolver.go:126` hook resolve readback 失败仍返回 Accepted=true。 | readback 失败传播错误，或同事务返回 canonical row。 | 需要。审批 UI/store 只接受 canonical resolved id/time，不接受请求兜底。 | 反转 `TestResolve_ReadbackFallback`。 |
| P1-22 | `internal/module/turn/rpc_types.go:48` turn start/steer 未做 unknown field guard。 | 从 struct json tag 反射生成允许字段；compat alias 用 Field/Direction/Reason 登记。 | 需要。frontend facade 同步 allowed keys，未知字段本地阻断。 | start/steer unknown-field fail-first 测试。 |
| P1-23 | `frontend-app/src/shared/api/backendApi.js:1633` `turnStartPayload` 透传 `...rest`。 | turn/start facade 加 allowed-key/canonicalization，未知字段 throw。 | 需要。后端仍必须 strict decode，前端只是第一层防御。 | `api.startTurn` unknown/camel/snake alias 测试。 |
| P1-24 | `frontend-app/src/shared/api/backendApi.js:207` `THREAD_START_ALLOWED_KEYS` 手写且漏 skill 字段。 | 从后端 DTO/contract registry 派生，或增加 reflect 生成检查；先补漏 `selectedSkills`、`selectedSkillRefs`、`manualSkillSelection`。 | 需要。CI 加 frontend/backend surface parity test。 | backendApi surface tests。 |
| P1-25 | `frontend-app/src/entities/client/model/useClientStore.js:2426` thread patch sequence 重启后从 1 开始会被前端丢弃。 | 在 `thread/stopped`/`agent/stopped` 清理 `sequencesByThread`，或后端给 patch generation/epoch。 | 需要。后端 patch envelope 增加 generation 是长期方案；前端清理是短期修复。 | 前端 `1,2,1` 重启序列测试。 |
| P1-26 | `internal/ui/wails/http_server.go:183` WebSocket guard 只校验 loopback，不校验 Host/Origin 同源。 | Origin 存在时必须与 request Host scheme/host/port 同源；或改用 URL/subprotocol nonce。 | 需要。frontend dev server/Wails host 显式注入 expected origin。 | localhost-vs-127.0.0.1、跨端口拒绝测试。 |
| P1-27 | `frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx:49` 前端拼 `file://<cwd>/.agnet/shared/...` 预览最终产物。 | 媒体预览也走后端校验后的 blob/asset endpoint；拒绝绝对路径、`..`、盘符路径。 | 需要。后端 read/open shared file API 做路径 canonicalization，前端不拼本地路径。 | path traversal/absolute path 前后端测试。 |
| P1-28 | `scripts/package_windows.ps1:1055` 请求 installer 时缺 Inno Setup 仍成功。 | artifact 包含 `installer` 时缺 `iscc` 必须 fail-fast；显式 skip 只能通过独立 flag。 | 需要。release wrapper 验证请求 artifact 全部存在后才输出 ready。 | packaging script unit/dry-run test。 |
| P1-29 | `scripts/package_macos.sh:1814` DMG 安装脚本先删旧 app 再复制，无 rollback。 | staging 目录复制并校验，成功后原子替换；失败回滚旧 app。 | 需要。发布脚本对 install script 做 shellcheck/fixture 验证。 | macOS package fixture test。 |
| P1-30 | `scripts/publish_github_release.sh:19` 发布资产矩阵只有 `darwin-arm64`。 | 明确平台矩阵，纳入 Windows 产物和 update manifest。 | 需要。appupdate 检查必须有对应 asset manifest gate。 | release asset matrix guard。 |
| P1-31 | `scripts/validate_super_agent_skills.py:403` 当前 `.agents/skills` mirror 与 canonical 漂移，CI 会红。 | 从 canonical 重新生成 mirror；统一 EOL/尾随空白比较规则。 | 需要。pre-push 对 skill/canonical/mirror 路径触发 validation。 | `python3 scripts/validate_super_agent_skills.py`。 |
| P1-32 | ADJUSTED: `scripts/code_size_guard.go:131` 单文件模式已启用函数中文注释守卫；默认/strict/CI 路径仍需确认并补齐 `EnforceFuncComments` 覆盖，避免只在单文件检查生效。 | 默认/strict/TestCodeSizeGuard 路径启用 `EnforceFuncComments` 或用测试证明当前等价覆盖；豁免必须显式登记。 | 局部已有防护；需要 CI/默认路径防护。 | `make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1`，并加 guard fixture 证明默认路径会拦截缺失函数级中文注释。 |
| P1-33 | `internal/provider/codexapp/server_pool.go:34` Codex app-server pool 无 live process/FD 上限。 | 增加 `MaxLive`、per-home cap、acquire queue/backpressure。 | 需要。UI/start/resume 显示 capacity exhausted，而不是无限拉起。 | pool capacity 并发测试。 |
| P1-34 | `cmd/mcp-orch/orchestration/process_lifecycle.go:247` shutdown drain 无总 deadline，StopAllAgents 用 background。 | StopAllAgents 接收 shutdown ctx；有界并发停止；总预算耗尽强制清理。 | 需要。fx shutdown timeout 和 orch drain status 进入 health/metrics。 | shutdown 多 agent 超时测试。 |
| P1-35 | `cmd/mcp-lsp/tools/tool_edit_rename.go:88` 跨文件 edit rollback 只恢复磁盘，不同步 LSP buffer。 | rollback 也走 LSP 同步恢复路径，记录 manager/version/original 并反序恢复。 | 需要。edit API 响应带 rollback status；上层不得继续使用可能脏的 LSP session。 | 第二文件失败后第一文件 LSP 已回滚测试。 |
| P1-36 | `internal/module/turn/tool_result_storage.go:56` 截断 tool result 完整原文落盘失败时静默丢证据。 | `CaptureToolResult` 返回 persist error 或 `PersistFailed/PersistError` 字段并写 trace/log/event。 | 需要。UI/observability 对 truncated+persist_failed 给可见诊断。 | cache dir 不可写测试。 |

### P2 Queue

| ID | Risk | Best Fix | Upper-Layer Defense | Validation |
|---|---|---|---|---|
| P2-01 | GUARD-ONLY: `internal/mcpserver/common/server.go:403` nil `ToolProvider` 会让 `tools/list` 返回空列表，但当前生产构造点 `cmd/mcp-lsp/fx.go` 和 `cmd/mcp-orch/runtime.go` 均传入具体 provider，未找到生产 nil provider 调用链。 | 不派 runtime 修复。保留 constructor/graph 回归测试，防止未来生产构造传 nil；如发现真实生产 nil 注入点，再升级回 runtime 修复。 | 不适用生产上层防护；需要 app graph guard。 | nil-provider guard test；生产构造点测试覆盖 concrete provider。 |
| P2-02 | `internal/platform/toolbridge/handler_peer_decode.go:576` tools/list 多 peer 选择与 tools/call 歧义规则不一致。 | list/call 复用同一 scoped peer selection。 | 需要。provider manifest 绑定 peer identity。 | two active peers 测试。 |
| P2-03 | `cmd/mcp-lsp/schema.go:64` schema 未暴露 `language_id`，但 handler 使用它。 | schema 加 `language_id` 或删除 handler override。 | 需要。schema-vs-param parity CI。 | mcp-lsp schema parity test。 |
| P2-04 | `cmd/mcp-lsp/schema.go:118` structure schema 漏 `folding_range/semantic_tokens`。 | enum 与 handler action 同步。 | 需要。manifest/schema/action parity test。 | mcp-lsp action parity test。 |
| P2-05 | `cmd/mcp-lsp/middleware/timeout.go:34` timeout 后 handler goroutine 可继续挂住。 | 请求取消、有界 worker/semaphore、outstanding request cleanup。 | 需要。LSP manager 暴露 timed-out request metrics。 | goroutine/request leak test。 |
| P2-06 | `internal/ui/wails/binding_native.go:16` clipboard image 不校验 MIME/header/大小。 | data URL MIME、PNG sniff、size cap、nosniff。 | 需要。前端 paste 也限制 MIME，但后端为事实边界。 | non-image payload 拒绝测试。 |
| P2-07 | `internal/ui/wails/code_scope.go:42` 显式 project 解析失败时继续用其他 root。 | 任一显式 project 无效即 fail-fast。 | 需要。frontend 选择器显示无效 root。 | one invalid + one valid 失败测试。 |
| P2-08 | `internal/ui/wails/code_preview.go:145` 图片预览绕过大小和内容 sniff。 | 图片也先 size cap + sniff；SVG 默认拒绝或安全渲染。 | 需要。前端只消费 tokenized preview，不消费裸 `file://`。 | fake image/oversize image 测试。 |
| P2-09 | `frontend-app/src/pages/workflows/WorkflowPage.jsx:474` workflow node config 畸形时 objectValue 变 `{}`。 | 严格解析 config，失败进入 blocking diagnostic 并禁用保存。 | 需要。后端 workflow API 对 config schema 做同样校验。 | malformed config 前端测试。 |
| P2-10 | `frontend-app/src/pages/workflows/adapters/workflowDisplayAdapter.js:15` workflow config JSON 解析失败显示无 IO。 | adapter 返回 parse error，诊断面显示损坏配置。 | 需要。WorkflowDiagnostics 显示错误而不是空态。 | adapter/diagnostics test。 |
| P2-11 | `frontend-app/src/pages/chat/components/chatUiActions.js:5` 空 catch 吞掉 UI action 错误。 | 删除局部 helper，统一用 visible action feedback/logger。 | 需要。store action 全部返回 typed error。 | send/stop/fork failure UI tests。 |
| P2-12 | `ChatApprovalMessage.jsx:25` 审批提交超时不释放 busy。 | AbortController 或超时后失败态释放 busy 并允许重试。 | 需要。后端审批 API 支持 idempotency/retry。 | never-resolve approval test。 |
| P2-13 | `ChatPage.jsx:481` approval notice 只有 sr-only。 | 接入可见 toast/action feedback。 | 需要。统一 error surface。 | visual/DOM test。 |
| P2-14 | `sql/queries/system_log.sql:6`、`sql/queries/ai_log.sql:2` list 查询固定丢 raw/extra。 | list/detail 分层，列表返回 preview/truncated，详情返回 sanitized raw/extra。 | 需要。dashboard 调详情 API。 | SQL/store/UI tests。 |
| P2-15 | `internal/platform/bus/sink.go:228`、`internal/module/uistate/patch.go:173` trace write error 被 `_ =` 吞掉。 | 限频 warn，带 event/thread/trace/status。 | 需要。observability health 计数。 | trace sink failure tests。 |
| P2-16 | provider/toolbridge trace 把错误泛化为 `operation failed`。 | 写 bounded sanitized `err.Error()`、type/code/exit。 | 需要。UI trace detail 显示 error preview。 | trace assertion tests。 |
| P2-17 | `internal/module/prompt/service.go:575` 非 object `match_when` 可落库，runtime 静默 no-match。 | 写入/导入/seed/schema 校验 NULL 或 object。 | 需要。frontend prompt editor 阻断非 object。 | prompt write/import tests。 |
| P2-18 | `internal/module/memory/config.go:136` auto-dream intent 文件读/解析失败后继续用 env/default。 | intent 损坏 fail closed，直到重写配置。 | 需要。settings UI 显示 intent 文件损坏。 | malformed intent tests。 |
| P2-19 | `internal/module/memory/auto_dream.go:306` queue 满直接丢 stopped enqueue。 | coalescing/durable retry，暴露 dropped/processed/scheduled。 | 需要。memory health 指标。 | queue full tests。 |
| P2-20 | `internal/module/memory/auto_dream_task.go:308` consolidation 失败后 idle 无 last error。 | snapshot 暴露 last status/error/time/thread。 | 需要。UI/RPC 显示 last failure。 | snapshot tests。 |
| P2-21 | `internal/module/memory/factory.go:227` memory index 读取失败与 miss 混淆。 | 返回 index status/error。 | 需要。memory_read response 带 warning/error。 | unreadable MEMORY.md test。 |
| P2-22 | `internal/platform/runtimeenv/runtimeenv.go:415` malformed video.env 行被跳过。 | 严格解析并带行号报错。 | 需要。startup/settings 显示配置文件损坏。 | malformed video.env test。 |
| P2-23 | `internal/platform/db/module.go:33` schema floor 仍 107，runtime migration 到 109。 | floor 绑定最新 runtime migration 109，并 gate 关键表/列。 | 需要。release smoke 检查关键 query 可运行。 | migration/version tests。 |
| P2-24 | GUARD-ONLY: `scripts/test_with_guard.sh:29` guard-only 只跑 code-size，`make guard` 名称高估。 | 不派 runtime 修复。让 `make guard` 跑完整 archtest，或拆出明确命名的 `code-size-guard` 快速子目标。 | 需要 guard 覆盖面防护。CI job、Makefile target 和脚本行为必须一致。 | `make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1`，并用 fixture 证明完整 guard 覆盖面。 |
| P2-25 | `frontend-app/scripts/sync-frontend-dist.mjs:27` build/sync 后 CI 不检查 web-dist 生成边界。 | 增加 `frontend-embed-verify`：若 `cmd/agent-terminal/web-dist/` 继续保持 ignored，则校验 build manifest/sync output/smoke hash；若改为 tracked artifact，则显式 unignore 并检查 diff。禁止手改 web-dist。 | 需要。pre-push/CI 对 frontend 变更运行同一 embed verifier，不能只看 `git status`。 | frontend embed verifier red/green test。 |
| P2-26 | `.githooks/pre-push:156` 未按路径触发 sqlc/codemap/skill validation。 | 增加 path-based gates。 | 需要。CI 与 pre-push gate matrix 对齐。 | hook dry-run tests。 |
| P2-27 | GUARD-ONLY: `frontend-app/src/App.test.jsx:4801` provider metadata 用例 skip，属于关键测试覆盖缺口，不是生产 runtime 缺陷。 | 取消 skip 或拆成稳定单测；同时加 no-critical-skip guard 阻止 provider/thread/turn/workflow 关键路径新增 `.skip`。 | 需要 CI 测试治理防护。 | `(cd frontend-app && npm test -- App.test.jsx useClientStore.test.js)` 和 `node frontend-app/scripts/no-critical-skip.mjs`。 |
| P2-28 | GUARD-ONLY: `internal/archtest/fx_invoke_guard_test.go:61` matcher skeleton skip，属于架构守卫可信度缺口，不是生产 runtime 缺陷。 | 落地 AST matcher 正/反 fixture，或把 skeleton 移出可信守卫输出并重命名 guard 覆盖面。 | 需要 guard 覆盖面防护。 | `./scripts/test_with_guard.sh ./internal/archtest -count=1`，包含 red/green fixture。 |
| P2-29 | `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1` reclaimer 缺 dispatching lease partial index。 | 加 partial index 并分批 reclaim。 | 需要。query-plan guard。 | sqlc + query plan test。 |
| P2-30 | `internal/module/cron/progress_subscriber.go:122` progress 队列无界。 | 有界队列，progress coalesce，terminal 保留。 | 需要。cron health backlog。 | queue pressure tests。 |
| P2-31 | `internal/module/memory/team/team_sync_watcher.go:300` watcher 递归无目录/文件/字节上限。 | hard cap，超过 fail-fast；增量 dirty scan。 | 需要。settings UI/health 显示 root oversized。 | large tree tests。 |
| P2-32 | `scripts/package_macos_github_release.sh:142` release wrapper 无 clean worktree/source revision gate。 | 发布入口强制 clean git status，记录 build commit。 | 需要。manual override 必须列 dirty whitelist。 | wrapper dry-run tests。 |

### P3 Queue

| ID | Cleanup | Best Fix | Upper-Layer Defense | Validation |
|---|---|---|---|---|
| P3-01 | MCP/runtime config trust boundary 分散在 provider/toolbridge/module。 | 写一页 runtime MCP 信任边界说明，并在代码中集中成 `RuntimeMCPPolicy` helper。 | 需要。新 helper 的测试名和错误码成为上层防御入口。 | `rg "RuntimeMCPPolicy"` 和相关包测试。 |
| P3-02 | 多处日志/preview 各自实现截断脱敏。 | 收敛到 shared `SafePreview`/`RedactedFields` helper。 | 需要。logger、toolbridge、bus、mcpcontrol 全部只用 shared helper。 | secret corpus table tests。 |
| P3-03 | release/package gate 分散在 shell/PowerShell。 | 建立 release preflight 脚本，统一检查 clean tree、asset matrix、installer dependency、embed diff。 | 需要。发布 wrapper 调 preflight，不能绕过。 | preflight fixture tests。 |
| P3-04 | GUARD-ONLY: guard 名称与覆盖面容易误读。 | 不派 runtime 修复。重命名快速目标或补齐 `make guard` 覆盖面：`guard` 必须表示完整门禁，`code-size-guard` 才是快速子集。 | 需要 guard/CI 命名防护。CI job 名称、Makefile target 和实际脚本覆盖面必须一致。 | `make guard`、Makefile/CI guard tests if present。 |
| P3-05 | trace/observability error 字段命名不统一。 | 定义 `error_preview`、`error_code`、`provider_exit_code`、`peer_id` 等统一字段。 | 需要。UI trace detail 只消费统一字段。 | trace golden tests。 |
| P3-06 | frontend/backend DTO parity 依赖手写 Set。 | 逐步迁移到 generated registry 或 reflective schema export。 | 需要。新增 DTO 字段未登记时 CI fail。 | surface parity tests。 |
| P3-07 | EVIDENCE-ONLY: P0/P1/P2/P3 裁决、修复和验证证据容易散落。 | 新建执行证据索引，记录每个 active queue ID 的红/绿命令、commit、残留风险；另用单独字段记录 adjusted/guard-only/evidence-only ID。 | 不需要生产上层防御；需要流程防御。 | validator 检查 active queue ID 完整匹配，并允许 `P3-07` 作为 evidence-only 元数据存在。 |

## Parallel Isolated Worktree Execution

All lanes below start at the same time from the same refreshed `main` baseline. Do not sequence active runtime/release fixes by severity. Adjusted guard-only and evidence-only items stay in their named lanes, but they do not authorize unrelated production-code edits.

### Controller Setup

- [ ] Step 1: Record controller baseline and make this plan visible to workers.

```bash
plan=docs/pians/2026-06-29-production-risk-remediation-plan.md
test -f "$plan"
git status --short
git branch --show-current
git worktree list --porcelain
if git ls-files --error-unmatch "$plan" >/dev/null 2>&1; then
  base_sha=$(git rev-parse HEAD)
else
  git switch -c codex/risk-20260629-coordination
  git add "$plan"
  git commit -m "docs: 固化生产风险修复计划快照"
  base_sha=$(git rev-parse HEAD)
fi
printf '%s\n' "$base_sha" > .risk-20260629-base-sha
sha256sum "$plan" 2>/dev/null || shasum -a 256 "$plan"
```

Expected: the plan is tracked in the recorded baseline commit. Unrelated dirty files are listed and not touched. If the controller cannot create the coordination commit, it must stop and use the snapshot-copy fallback below before fanout.

- [ ] Step 2: Create all isolated worktrees from the same baseline SHA.

```bash
plan=docs/pians/2026-06-29-production-risk-remediation-plan.md
base_sha=$(cat .risk-20260629-base-sha)
test -n "$base_sha"
for lane in \
  mcp-runtime-security provider-security-logging app-graph-readiness \
  thread-session-lifecycle mcp-orch-protocol store-schema-config frontend-wails release-ci-guard \
  lsp-perf-observability p2-p3-hardening
do
  git worktree add ".worktrees/risk-${lane}-20260629" -b "codex/risk-${lane}-20260629" "$base_sha"
  test -f ".worktrees/risk-${lane}-20260629/$plan"
done
```

Expected: ten clean linked worktrees, each on its own `codex/risk-*` branch.

- [ ] Step 2b: If Step 1 used snapshot-copy fallback instead of a coordination commit, copy the plan into every worktree and verify the same hash.

```bash
plan=docs/pians/2026-06-29-production-risk-remediation-plan.md
plan_sha=$(sha256sum "$plan" 2>/dev/null | awk '{print $1}')
test -n "$plan_sha" || plan_sha=$(shasum -a 256 "$plan" | awk '{print $1}')
for wt in .worktrees/risk-*-20260629
do
  mkdir -p "$wt/docs/pians"
  cp "$plan" "$wt/$plan"
  got_sha=$(sha256sum "$wt/$plan" 2>/dev/null | awk '{print $1}')
  test -n "$got_sha" || got_sha=$(shasum -a 256 "$wt/$plan" | awk '{print $1}')
  test "$got_sha" = "$plan_sha"
done
```

Expected: every worktree has the same immutable plan snapshot. Workers must not edit this snapshot.

- [ ] Step 3: Dispatch one no-context worker per lane with this document path and the lane section only.

Expected: each worker reports branch, worktree path, owned files, planned tests, risk IDs, plan `sha256`, and confirms it will not edit files outside its lane unless it returns `NEEDS_APPROVAL`.

### Lane A: MCP Runtime Security

**Worktree:** `.worktrees/risk-mcp-runtime-security-20260629`
**Branch:** `codex/risk-mcp-runtime-security-20260629`

**Owns:**
- `internal/module/thread/rpc_types.go`
- `internal/module/thread/start_session_helpers.go`
- `internal/module/thread/mcp_server_config.go`
- `internal/dto/provider/manifest.go`
- `internal/contract/mcp_control.go`
- `internal/contract/manifest.go`
- `internal/platform/toolbridge/stdio_mcp_client.go`
- `internal/platform/toolbridge/http_mcp_client.go`
- `internal/platform/toolbridge/handler_peer_decode.go`
- `internal/platform/toolbridge/types.go`
- `internal/provider/shared/config_helpers.go`
- `internal/provider/codexapp/support.go`
- `internal/module/mcp_server/service.go`
- `internal/mcpserver/common/server.go`
- `internal/mcpserver/common/server_test.go`
- related tests under the same packages

**Queue IDs:** P0-01, P1-01, P1-11, P2-02, P3-01

**Adjusted Out:** `P2-01` is guard-only after cross-adjudication. Lane A must not implement nil-provider runtime behavior unless the controller finds a production nil-provider injection path.

**External Boundary:** Lane A may import and test `internal/platform/httpegress` for `P1-01`, but it must not edit `internal/platform/httpegress` policy code without returning `NEEDS_APPROVAL`; the expected minimal change is to consume the existing egress checks from provider/toolbridge ingress.

- [ ] Step 1: Add failing tests for arbitrary stdio command rejection, secret env non-inheritance, HTTP MCP private URL rejection, malformed tools/list rejection, and multi-peer list/call parity.
- [ ] Step 2: Implement `RuntimeMCPPolicy` as the shared trust-boundary helper used by thread/start MCP config parsing, provider manifest DTO parsing, MCP control/manifest contracts, provider config parsing, and toolbridge launch.
- [ ] Step 2b: Reject raw thread/start `config.mcpConfig` command/url/header/env input unless it references a trusted server id produced by `internal/module/mcp_server`; open runtime config must not be able to create arbitrary stdio/http MCP peers.
- [ ] Step 3: Implement strict tools/list decoder used by stdio, HTTP, and peer decode paths; `tools` must exist as an array, tool names must be non-empty, and schemas must be structurally valid.
- [ ] Step 4: Add the runtime MCP trust-boundary notes in code comments/tests next to `RuntimeMCPPolicy`; do not create a separate docs-only closure.
- [ ] Step 5: Run `./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/shared ./internal/provider/codexapp ./internal/provider/claudecli ./internal/module/thread ./internal/module/mcp_server ./internal/mcpserver/common ./internal/contract ./internal/dto/provider -count=1`.
- [ ] Step 6: Commit only owned files.

### Lane B: Provider Security And Logging

**Worktree:** `.worktrees/risk-provider-security-logging-20260629`
**Branch:** `codex/risk-provider-security-logging-20260629`

**Owns:**
- `internal/contract/dream.go`
- `internal/module/thread/startconfig/sandbox.go`
- `internal/module/thread/factory_config.go`
- `internal/module/thread/factory_config_failfast_test.go`
- `internal/module/memory/module.go`
- `internal/module/memory/auto_dream.go`
- `internal/module/memory/auto_dream_task.go`
- `internal/module/memory/health.go` (create if absent)
- `internal/module/memory/ui_rpc.go`
- `internal/provider/claudecli/transport_config.go`
- `internal/provider/claudecli/config.go`
- `internal/provider/claudecli/dream_executor.go`
- `internal/provider/dreamexec/dreamexec.go`
- `internal/provider/unified/dream_executor.go`
- `internal/provider/unified/event_map.go`
- `internal/provider/codexapp/dream_executor.go`
- `internal/provider/claudecli/session_events.go`
- `internal/provider/codexapp/event_map.go`
- `internal/dto/provider/event.go`

**Queue IDs:** P1-02, P1-03, P1-13, P2-19, P2-20

- [ ] Step 1: Add failing tests for unknown approval/sandbox, open thread/start security alias bypass, dream tool invocation, memory/dream scheduling without deny-tools capability, auto-dream queue overflow durable retry, auto-dream last error snapshot, and raw provider event secret.
- [ ] Step 2: Implement strict provider security config validation in both provider config parsing and thread/start ingress; unknown approval/sandbox must fail before provider launch.
- [ ] Step 3: Add `DreamRuntimePolicy{ToolsDisabled, ReadOnlySandbox, MinEnv}` to the dream contract and enforce it in provider wrappers, unified executor, automatic memory dreams, manual memory dreams, and similarity-triggered dreams.
- [ ] Step 4: Add auto-dream durable retry/coalescing and health snapshot fields for dropped/processed/scheduled, last error, last time, and thread id.
- [ ] Step 5: Stop recording raw provider line/payload; provider events may only keep type, session, size, hash, and safe field names.
- [ ] Step 6: Run `./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/provider/claudecli ./internal/provider/codexapp ./internal/provider/unified ./internal/provider/dreamexec ./internal/contract ./internal/dto/provider -count=1`.
- [ ] Step 7: Commit only owned files.

### Lane C: App Graph Readiness

**Worktree:** `.worktrees/risk-app-graph-readiness-20260629`
**Branch:** `codex/risk-app-graph-readiness-20260629`

**Owns:**
- `internal/app/thread_orchestration_adapter.go`
- `internal/app/thread_orchestration_adapter_test.go`
- `internal/app/toolbridge_adapters.go`
- `internal/app/toolbridge_adapters_test.go`
- `internal/contract/toolbridge.go`
- app/fx graph tests under `internal/app`

**Queue IDs:** P1-07 (adjusted readiness/diagnostic only), P1-08

- [ ] Step 1: Add tests that preserve `P1-07` calling-path fail-fast for missing `OrchestrationService`, and add failing fx/readiness tests for missing `ServerManager`, missing `DriverFactory`, and missing toolbridge handler in production app wiring.
- [ ] Step 2: Do not remove the existing missing orchestration facade unless a production graph test proves it is unsafe. Replace Codex/toolbridge optional critical dependencies with explicit fail-fast readiness in production wiring; test/no-provider mode must use explicit stub modules, not implicit nil adapters.
- [ ] Step 3: Add provider/toolbridge readiness probe before thread lifecycle can launch a provider turn.
- [ ] Step 4: Run `./scripts/test_with_guard.sh ./internal/app ./internal/contract -count=1`.
- [ ] Step 5: Commit only owned files.

### Lane D: Thread And Session Lifecycle

**Worktree:** `.worktrees/risk-thread-session-lifecycle-20260629`
**Branch:** `codex/risk-thread-session-lifecycle-20260629`

**Owns:**
- `internal/module/thread/stop.go`
- `internal/module/thread/archive.go`
- `internal/module/thread/service.go`
- `internal/module/thread/events.go`
- `internal/module/thread/lifecycle.go`
- `internal/module/thread/lifecycle_fork.go`
- `internal/module/thread/prompt_snapshot.go`
- `internal/module/thread/router_resolve.go`
- `internal/contract/session.go`
- `internal/store/thread/session_adapter.go`
- `internal/module/memory/extract_metadata.go`
- `internal/provider/unified/session_resolver_auto_resume.go`
- `internal/provider/unified/client.go`
- `internal/provider/unified/session.go`
- `internal/provider/unified/session_adapter.go`
- `internal/dto/provider/session.go`
- `internal/provider/claudecli/driver.go`
- `internal/provider/codexapp/driver.go`
- related tests

**Queue IDs:** P1-04, P1-05, P1-06, P1-18, P1-20

- [ ] Step 1: Add failing tests for Stop/Archive/Delete racing Resume, auto-resume missing snapshot, provider adapter rejecting empty snapshot, resume persist failure cleanup, missing default prompt fail-fast, and malformed `config_override.runtime`.
- [ ] Step 2: Delay resume unblock until durable terminal state is written.
- [ ] Step 3: Add shared `ValidateResumePromptSnapshot` behavior for thread auto-resume and provider adapters; `internal/contract/session.go` and `internal/store/thread/session_adapter.go` must carry the snapshot required to build a valid resume request.
- [ ] Step 4: Clean provider runtime on every resume persistence failure and keep unified client state `pending` until the durable commit succeeds.
- [ ] Step 5: Decode `config_override.runtime` with fail-closed errors in both metadata extraction and resume/session recovery paths.
- [ ] Step 6: Run `./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/store/thread ./internal/provider/unified ./internal/provider/claudecli ./internal/provider/codexapp ./internal/contract ./internal/dto/provider -count=1`.
- [ ] Step 7: Commit only owned files.

### Lane E: MCP Orchestration And Shutdown

**Worktree:** `.worktrees/risk-mcp-orch-protocol-20260629`
**Branch:** `codex/risk-mcp-orch-protocol-20260629`

**Owns:**
- `cmd/mcp-orch/orchestration/node_router.go`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- `cmd/mcp-orch/orchestration/wakeupreclaim/reclaimer.go`
- `cmd/mcp-orch/orchestration/service.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation_command.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go`
- `cmd/mcp-orch/orchestration/nodeexec/config_test.go`
- `cmd/mcp-orch/orchestration/nodeexec/plan.go`
- `cmd/mcp-orch/orchestration/nodeexec/plan_update_test.go`
- `cmd/mcp-orch/orchestration/process_lifecycle.go`
- `cmd/mcp-orch/store/taskdag/contract.go`
- `cmd/mcp-orch/store/taskdag/store_lease.go`
- `cmd/mcp-orch/store/taskdag/store_dispatch_guard.go`
- `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `cmd/mcp-orch/sql/queries/task_dag_worker_lease.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- `internal/platform/db/sqlite/migrations/110_task_dag_wakeup_dispatch_index.sql` (create with the next available migration number if 110 is taken)
- `internal/platform/db/sqlite/query_plan_test.go` (create if absent)
- related sqlc/generated files if query changes require regeneration

**Queue IDs:** P1-09, P1-10, P1-34, P2-29

- [ ] Step 0: Before creating any migration file, ask the controller to reserve a migration number for Lane E and Lane F. Do not independently create `110_*` in both lanes; if `110` is already taken, use the controller-assigned next number and record it in the lane evidence.
- [ ] Step 1: Add failing tests for duplicate wakeup after lease expiry, command timeout enforcement, shutdown drain total deadline, and reclaimer query plan.
- [ ] Step 2: Add store-backed running fence: CAS `pending/ready -> running`, persist `active_wakeup_id` and attempt before `RunCommandCard`, renew lease while output is active, and reject stale CompleteNode/failure by attempt.
- [ ] Step 3: Add timeout config to nodeexec config/plan persistence and apply the effective DAG metadata plus node config timeout to `executor_automation_command.go`; saved config that does not affect execution is a failure.
- [ ] Step 4: Pass shutdown context through StopAllAgents and add bounded stop behavior.
- [ ] Step 5: Add the wakeup dispatch partial index through a runtime migration and lock it with a query-plan test, not only a query-file edit. If the shared baseline also needs the index, return `NEEDS_APPROVAL` for controller-level baseline reconciliation instead of editing Lane F's prompt-schema baseline scope.
- [ ] Step 6: Run `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1` plus `make sqlc-verify` when SQL changes.
- [ ] Step 7: Commit only owned files.

### Lane F: Store, Schema, And Config Fail-Fast

**Worktree:** `.worktrees/risk-store-schema-config-20260629`
**Branch:** `codex/risk-store-schema-config-20260629`

**Owns:**
- `internal/store/sharedfile/store.go`
- `cmd/mcp-orch/store/sharedfile/store.go`
- `cmd/mcp-orch/store/sharedfile/importer.go`
- `internal/platform/sharedfilepath/policy.go`
- `internal/platform/sharedfilepath/policy_test.go`
- `internal/platform/sharedfilegitignore/gitignore.go`
- `internal/platform/db/tx.go`
- `internal/platform/db/module.go`
- `internal/platform/db/sqlite/migrations/001_baseline.sql`
- `internal/platform/db/sqlite/migrations/110_prompt_templates_nullable_match_when.sql` (create with the next available migration number if 110 is taken)
- `sql/queries/prompt_template.sql`
- `sql/queries/prompt_template_sections.sql`
- `sql/queries/prompt_routing_test.sql`
- `internal/store/prompt/store.go`
- `internal/store/prompt/contract.go`
- `internal/store/sqlc/prompt_template.sql.go`
- `internal/store/sqlc/prompt_template_sections.sql.go`
- `internal/store/sqlc/prompt_routing_test.sql.go`
- `internal/module/uistate/builtin_tools.go`
- `internal/module/memory/config.go`
- `internal/module/memory/factory.go`
- `internal/module/prompt/service.go`
- `internal/platform/hooks/resolver.go`
- `internal/platform/runtimeenv/runtimeenv.go`

**Queue IDs:** P1-15, P1-16, P1-17, P1-19, P1-21, P2-17, P2-18, P2-21, P2-22, P2-23

- [ ] Step 0: Before creating any migration file, ask the controller to reserve a migration number for Lane E and Lane F. Do not independently create `110_*` in both lanes; if `110` is already taken, use the controller-assigned next number and record it in the lane evidence.
- [ ] Step 1: Add failing tests for `.gitignore` Ensure failures, `_internal` protected-root writes, true `BEGIN IMMEDIATE`, `match_when:null`, builtin tool preference read error, non-object `match_when`, hook resolve readback failure, malformed intent, unreadable memory index, malformed `video.env`, and schema floor.
- [ ] Step 2: Implement fail-fast behavior before disk/DB writes and before runtime launch.
- [ ] Step 3: Preserve nullable `match_when` semantics through a runtime migration and baseline update: existing DBs and new DBs must allow `NULL` or JSON object, sqlc uses nullable params, store preserves nil, and `{}` is only explicit match-all.
- [ ] Step 4: Propagate hook readback failures or return the canonical row from the same transaction; approval UI/store must not accept request-time fallback IDs.
- [ ] Step 5: Add a shared protected-root policy in `internal/platform/sharedfilepath` or `internal/platform/sharedfilegitignore` and call it from both sharedfile stores and importer before disk or DB writes.
- [ ] Step 6: Run `make sqlc-verify` and `./scripts/test_with_guard.sh ./internal/store/sharedfile ./cmd/mcp-orch/store/sharedfile ./internal/platform/sharedfilepath ./internal/platform/sharedfilegitignore ./internal/platform/db ./internal/store/prompt ./internal/module/prompt ./internal/module/uistate ./internal/module/memory ./internal/platform/hooks ./internal/platform/runtimeenv -count=1`.
- [ ] Step 7: Commit only owned files.

### Lane G: Frontend And Wails Boundaries

**Worktree:** `.worktrees/risk-frontend-wails-20260629`
**Branch:** `codex/risk-frontend-wails-20260629`

**Owns:**
- `internal/module/turn/rpc_types.go`
- `internal/dto/ui/event.go`
- `cmd/mcp-orch/tools/workflow_workbench.go`
- `cmd/mcp-orch/orchestration/nodeexec/ops.go`
- `cmd/mcp-orch/orchestration/nodeexec/ops_update_node_test.go`
- `internal/platform/rpc/approval.go`
- `internal/platform/rpc/approval_support.go`
- `internal/platform/rpc/approval_test.go`
- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/shared/api/backendApi.surface.test.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/shared/api/wailsBridge.test.js`
- `frontend-app/scripts/rpc-contract-audit.mjs`
- `frontend-app/scripts/rpc-contract-audit.test.mjs`
- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- `frontend-app/src/entities/client/model/composerAttachments.js`
- `frontend-app/src/entities/client/model/composerAttachments.test.js`
- `frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx`
- `frontend-app/src/pages/workflows/components/WorkflowDiagnostics.jsx`
- `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`
- `frontend-app/src/pages/workflows/adapters/workflowDisplayAdapter.js`
- `frontend-app/src/pages/workflows/adapters/workflowDisplayAdapter.test.js`
- `frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx`
- `frontend-app/src/pages/chat/components/chatUiActions.js`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `internal/ui/wails/http_server.go`
- `internal/ui/wails/binding_native.go`
- `internal/ui/wails/code_scope.go`
- `internal/ui/wails/code_preview.go`
- `internal/ui/wails/sharedfile_open.go`

**Queue IDs:** P1-22, P1-23, P1-24, P1-25, P1-26, P1-27, P2-06, P2-07, P2-08, P2-09, P2-10, P2-11, P2-12, P2-13, P3-06

- [ ] Step 0: Keep `P1-27` path canonicalization inside the owned Wails/shared-file preview boundary. If the fix requires editing `internal/platform/sharedfilepath`, sharedfile stores, or other Lane F files, return `NEEDS_APPROVAL` instead of widening the write set.
- [ ] Step 1: Add failing backend tests for turn start/steer unknown-field rejection, DTO parity, `UIThreadPatch` generation/epoch semantics in `internal/dto/ui/event.go`, workflow node config schema validation, and approval idempotency/retry.
- [ ] Step 2: Add failing frontend tests for unknown fields, generated contract registry drift, launch skill facade keys, patch sequence restart, malformed workflow config diagnostics, visible approval errors, and approval timeout retry.
- [ ] Step 3: Add Wails tests for same-origin websocket, clipboard MIME/header/size, invalid project root, image preview size/sniff, and tokenized shared-file preview rejecting absolute/path-traversal input.
- [ ] Step 4: Generate the frontend allowed-key registry from Go DTO JSON tags and explicit compatibility aliases for thread/start, turn/start, and turn/steer; `frontend-app/scripts/rpc-contract-audit.mjs` must compare Go DTOs, generated registry, and `backendApi.js`.
- [ ] Step 5: Remove frontend `file://` synthesis and route local previews through a backend-validated shared-file preview blob/token endpoint that uses canonical path, no-symlink, size cap, MIME sniff, and traversal rejection.
- [ ] Step 6: Apply workflow config schema at the create/update/apply boundary; frontend parse errors must enter diagnostics and block save/start, while backend `cmd/mcp-orch` create/update/apply ops must reject invalid persisted config.
- [ ] Step 7: Add approval respond idempotency or abort semantics through `backendApi.js`, `internal/module/turn/rpc_types.go`, and `internal/platform/rpc/approval.go`; timeout retry must not double-submit. Hook resolver readback remains owned by Lane F.
- [ ] Step 8: Run `./scripts/test_with_guard.sh ./internal/module/turn ./internal/module/dashboard ./internal/platform/rpc ./internal/ui/wails ./cmd/mcp-orch/tools ./cmd/mcp-orch/orchestration/nodeexec -count=1`.
- [ ] Step 9: Run `(cd frontend-app && npm run lint && npm test && npm run build)`.
- [ ] Step 10: Commit only owned source files; generated embed output is committed only if Lane H explicitly switches the repository policy to tracked artifacts.

### Lane H: Release, CI, And Guard

**Worktree:** `.worktrees/risk-release-ci-guard-20260629`
**Branch:** `codex/risk-release-ci-guard-20260629`

**Owns:**
- `scripts/publish_github_release.sh`
- `scripts/package_windows.ps1`
- `scripts/package_windows_github_release.ps1`
- `scripts/package_macos.sh`
- `scripts/package_macos_github_release.sh`
- `scripts/package_guard_helpers_test.go`
- `scripts/package_windows_guard_test.go`
- `scripts/package_macos_guard_test.go`
- `scripts/package_macos_release_guard_test.go`
- `scripts/github_release_updater_guard_test.go`
- `scripts/frontend_build_guard_test.go`
- `scripts/frontend_embed_verify.sh` (create if absent)
- `scripts/frontend_embed_verify_guard_test.go` (create if absent)
- `scripts/validate_risk_evidence.py` (create if absent)
- `scripts/validate_super_agent_skills.py`
- `scripts/code_size_guard.go`
- `scripts/test_with_guard.sh`
- `scripts/ci_commit_guard.sh`
- `Makefile`
- `.githooks/pre-push`
- `.github/workflows/ci.yml`
- `.github/workflows/sqlite-release-gates.yml`
- `docs/pians/2026-06-29-production-risk-remediation-evidence.schema.md` (create during execution)
- generated skill mirror files if validation requires regeneration
- canonical skill files only with explicit controller approval; mirror regeneration must not silently rewrite canonical skill content

**Queue IDs:** P1-28, P1-29, P1-30, P1-31, P1-32 (partial guard), P2-24 (guard-only), P2-25, P2-26, P2-32, P3-03, P3-04 (guard-only), P3-07 (evidence-only)

- [ ] Step 1: Add failing script/fixture tests for installer dependency, macOS atomic install, asset matrix, release wrapper clean tree/source revision gate, skill mirror drift, guard coverage, frontend embed verifier policy, path-based pre-push gates, and guard-only/evidence-only ID accounting.
- [ ] Step 2: Regenerate skill mirrors from canonical and make validation pass.
- [ ] Step 3: Add release preflight that blocks dirty worktree unless a manual override lists exact dirty whitelist entries and records the build commit.
- [ ] Step 4: Implement `frontend-embed-verify`: default mode validates build/sync manifest and smoke hash for ignored `cmd/agent-terminal/web-dist/`. Tracked-artifact mode is out of scope for this lane unless the controller explicitly approves a repository policy switch. Add `make frontend-embed-verify` and wire it into CI, pre-push, and final acceptance.
- [ ] Step 5: Implement `scripts/validate_risk_evidence.py` so it extracts every active queue ID from this plan and fails on missing or extra evidence rows, while accepting adjusted readiness/guard/evidence IDs (`P1-07`, `P1-32`, `P2-01`, `P2-24`, `P2-27`, `P2-28`, `P3-04`, `P3-07`) only in their declared disposition sections.
- [ ] Step 6: Define the evidence schema only; the controller writes final evidence rows after lane commits are known.
- [ ] Step 7: Run `python3 scripts/validate_super_agent_skills.py`, `make guard`, `make frontend-embed-verify`, `go test ./scripts -run 'Package|Release|Frontend|Guard|Commit|Evidence' -count=1`, `./scripts/ci_commit_guard.sh`, and `git diff --check`.
- [ ] Step 8: Commit only owned files.

### Lane I: LSP, Performance, And Observability

**Worktree:** `.worktrees/risk-lsp-perf-observability-20260629`
**Branch:** `codex/risk-lsp-perf-observability-20260629`

**Owns:**
- `cmd/mcp-lsp/schema.go`
- `cmd/mcp-lsp/tools/tool_edit_rename.go`
- `cmd/mcp-lsp/tools/tool_edit_recovery.go`
- `cmd/mcp-lsp/tools/tool_edit_support.go`
- `cmd/mcp-lsp/manager/manager.go`
- `cmd/mcp-lsp/multilsp/manager.go`
- `cmd/mcp-lsp/multilsp/manager_lifecycle.go`
- `cmd/mcp-lsp/middleware/timeout.go`
- `internal/provider/codexapp/server_pool.go`
- `internal/provider/shared/observability_trace.go`
- `internal/provider/claudecli/observability_trace.go`
- `internal/provider/unified/observability_trace.go`
- `internal/module/turn/tool_result_storage.go`
- `internal/module/uistate/patch.go`
- `internal/platform/bus/sink.go`
- `internal/platform/toolbridge/proxy.go`
- `internal/platform/toolbridge/observability_trace.go`
- `internal/platform/mcpcontrol/handlers.go`
- `sql/queries/system_log.sql`
- `sql/queries/ai_log.sql`
- `internal/store/sqlc/system_log.sql.go`
- `internal/store/sqlc/ai_log.sql.go`
- `internal/module/dashboard/contract.go`
- `internal/module/dashboard/rpc.go`
- `internal/module/dashboard/logs.go`
- `internal/module/dashboard/service.go`
- `internal/module/dashboard/wire_dto.go`
- `internal/module/dashboard/log_detail_rpc.go` (create if absent)
- `internal/module/cron/progress_subscriber.go`
- `internal/module/memory/team/team_sync_watcher.go`
- `internal/platform/observability/record_error.go`
- `internal/platform/observability/event.go`
- `internal/platform/observability/query.go`
- `internal/platform/observability/service.go`
- `internal/platform/observability/sanitizer.go`
- `internal/platform/observability/safe_preview.go` (create if absent)

**Queue IDs:** P1-12, P1-14, P1-33, P1-35, P1-36, P2-03, P2-04, P2-05, P2-14, P2-15, P2-16, P2-30, P2-31, P3-02, P3-05

- [ ] Step 0: Keep `P2-05` timeout cleanup and `P1-35` rollback-buffer synchronization inside the owned mcp-lsp tool/manager files above. If the real fix needs additional mcp-lsp files, return `NEEDS_APPROVAL` with the exact paths before editing them.
- [ ] Step 1: Add failing tests for peer-down `tools/list` behavior, bus/toolbridge/mcpcontrol secret preview, generic provider/toolbridge trace errors, trace write failure visibility, server pool capacity, LSP rollback buffer state, persisted tool result failure, schema/action parity, timeout leak, sanitized log detail projection, bounded cron queue/coalescing, and watcher caps.
- [ ] Step 2: Implement bounded capacity/backpressure and deterministic rollback/cancellation.
- [ ] Step 3: Change provider-facing `tools/list` for peer-down from partial success to JSON-RPC error or explicit degraded envelope that blocks provider start/turn.
- [ ] Step 4: Add shared `SafePreview`/`SafeErrorPreview` and standard fields `error_preview`, `error_code`, `provider_exit_code`, `peer_id`; bus, toolbridge, mcpcontrol, provider trace producers, and UI state patch trace writes must call the shared helpers.
- [ ] Step 5: Add sanitized list/detail log projection: list returns preview/truncated fields, detail returns sanitized raw/extra through concrete dashboard RPC/service/DTO handlers.
- [ ] Step 6: Add cron/team-sync health snapshots with backlog, dropped/coalesced counts, last error/time/thread, and root-size cap violations.
- [ ] Step 7: Run `./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./internal/provider/codexapp ./internal/provider/shared ./internal/provider/claudecli ./internal/provider/unified ./internal/module/turn ./internal/module/uistate ./internal/module/dashboard ./internal/module/cron ./internal/module/memory/team ./internal/platform/bus ./internal/platform/toolbridge ./internal/platform/mcpcontrol ./internal/platform/observability -count=1` and `make sqlc-verify` when SQL changes.
- [ ] Step 8: Commit only owned files.

### Lane J: Test Guard Hardening

**Worktree:** `.worktrees/risk-p2-p3-hardening-20260629`
**Branch:** `codex/risk-p2-p3-hardening-20260629`

**Owns:**
- `frontend-app/src/App.test.jsx`
- `frontend-app/scripts/no-critical-skip.mjs` (create if absent)
- `frontend-app/package.json`
- `internal/archtest/fx_invoke_guard_test.go`
- `internal/archtest/fx_invoke_guard_fixture_test.go` (create if absent)

**Queue IDs:** P2-27 (guard-only), P2-28 (guard-only)

**Execution Boundary:** Lane J is a test/guard governance lane. It must not claim to fix production runtime behavior.

- [ ] Step 1: Remove or replace skipped provider metadata tests in `frontend-app/src/App.test.jsx` and add a critical-skip guard that fails on `.skip` in provider/thread/turn/workflow contract tests unless the test name is explicitly allowlisted with a dated reason. If the scanner finds skipped tests in files owned by another lane, such as `useClientStore.test.js` or Settings tests, this lane reports `NEEDS_APPROVAL` instead of editing outside its owns.
- [ ] Step 2: Land real fx.Invoke AST matcher fixtures for positive and negative cases, or remove the skeleton from trusted guard output and rename the guard to reflect its reduced scope.
- [ ] Step 3: Run `(cd frontend-app && npm test -- App.test.jsx useClientStore.test.js)` and `node frontend-app/scripts/no-critical-skip.mjs`.
- [ ] Step 4: Run `./scripts/test_with_guard.sh ./internal/archtest -count=1`.
- [ ] Step 5: Commit only owned files.

### Controller Merge And Conflict Rules

- [ ] Step 1: Wait for all ten lanes to return local commits and validation output.
- [ ] Step 2: Reject any lane with dirty worktree, skipped validation, docs-only closure, or expanded write-set without `NEEDS_APPROVAL`.
- [ ] Step 3: Create a temporary integration worktree from the recorded base SHA.

```bash
base_sha=$(cat .risk-20260629-base-sha)
test -n "$base_sha"
git worktree add .worktrees/risk-integration-20260629 -b codex/risk-integration-20260629 "$base_sha"
```

Expected: integration worktree is clean and starts from the same base SHA recorded in Controller Setup.

- [ ] Step 4: Record lane commit SHAs before merging.

```bash
for branch in \
  codex/risk-mcp-runtime-security-20260629 \
  codex/risk-provider-security-logging-20260629 \
  codex/risk-app-graph-readiness-20260629 \
  codex/risk-thread-session-lifecycle-20260629 \
  codex/risk-mcp-orch-protocol-20260629 \
  codex/risk-store-schema-config-20260629 \
  codex/risk-frontend-wails-20260629 \
  codex/risk-release-ci-guard-20260629 \
  codex/risk-lsp-perf-observability-20260629 \
  codex/risk-p2-p3-hardening-20260629
do
  git -C .worktrees/risk-integration-20260629 rev-parse "$branch"
  git -C .worktrees/risk-integration-20260629 merge-base --is-ancestor "$base_sha" "$branch"
done
```

Expected: every lane branch resolves to exactly one reviewed local commit or a reviewed stack with a documented top SHA, and every lane descends from the recorded fanout base SHA.

- [ ] Step 5: Merge lanes in the fixed order above, resolving conflicts only after reading both lane diffs and rerunning affected tests.
- [ ] Step 6: Update `docs/pians/2026-06-29-production-risk-remediation-evidence.md` with every queue ID, lane commit, red command, green command, and residual risk, then run the evidence validator before final acceptance.
- [ ] Step 7: Run the final acceptance gate after all lanes are integrated.
- [ ] Step 8: If any lane fails final integration, send it back to its isolated worktree; do not move it out of this parallel repair run.

## Global Upper-Layer Defense Matrix

| Boundary | Defense To Add |
|---|---|
| Runtime MCP config | `RuntimeMCPPolicy` in thread/start DTO, contract, provider manifest, provider config, and toolbridge launch paths rejects untrusted stdio/HTTP twice: before manifest materialization and before process/network launch. |
| App graph | Production readiness exposes missing orchestration/toolbridge capabilities before user-visible lifecycle work. The existing missing orchestration facade remains acceptable only as a calling-path fail-fast adapter; optional Codex/toolbridge critical dependencies need explicit production readiness or stub modules. |
| Provider security | Enumerated approval/sandbox config, no unknown defaults, no inherited secret env, DreamExecutor no-tools/read-only/min-env enforced at contract, provider wrappers, and memory dream scheduling entries. |
| Thread/session lifecycle | Durable lifecycle blockers with generation, provider cleanup on partial failure, provider resume adapters reject empty or stale prompt snapshots. |
| MCP orch execution | Store-backed running fence and attempt/lease idempotency prevent duplicate wakeup dispatch and stale completion. |
| DTO/API facade | Backend reflective/generated allowed-field registry is the fact source; frontend facade consumes it and CI fails on field-level drift, not only RPC method drift. |
| Store/schema | Nullable semantics preserved, migration floor current, prompt write/import/seed reject invalid `match_when`, query-plan guards cover hot paths. |
| Logging/observability | Shared redaction/preview helper, trace persist failure warnings, sanitized log list/detail split, memory/cron health snapshots for backlog and last errors. |
| Frontend/Wails | No frontend local path synthesis; all local files go through backend canonicalization and tokenized blob/asset endpoints. |
| Release/CI | Clean tree, asset matrix, installer dependency, frontend embed verifier, skill mirror, sqlc/codemap gates in CI and pre-push. |

## Final Acceptance Gate

Run these after all parallel lanes have been integrated into the temporary integration worktree:

```bash
git status --short
python3 scripts/validate_super_agent_skills.py
make guard
make sqlc-verify
make codemap-check
make frontend-embed-verify
./scripts/test_with_guard.sh ./internal/app ./internal/module/thread ./internal/provider/... ./internal/platform/toolbridge ./internal/ui/wails ./cmd/mcp-orch/... ./cmd/mcp-lsp/... -count=1
go test ./scripts -run 'Package|Release|Frontend|Guard|Commit|Evidence' -count=1
./scripts/ci_commit_guard.sh
(cd frontend-app && npm run lint && npm test && npm run build)
git diff --check
test -f docs/pians/2026-06-29-production-risk-remediation-evidence.md
python3 scripts/validate_risk_evidence.py --plan docs/pians/2026-06-29-production-risk-remediation-plan.md --evidence docs/pians/2026-06-29-production-risk-remediation-evidence.md
```

Expected result: no active production/release queue item remains open; every active risk ID has code or gate evidence, red/green validation where applicable, and a final integration command result. Adjusted readiness item `P1-07` must have a production readiness/diagnostic record. Guard-only and evidence-only IDs (`P1-32`, `P2-01`, `P2-24`, `P2-27`, `P2-28`, `P3-04`, `P3-07`) must have explicit disposition records instead of fabricated runtime fixes.
