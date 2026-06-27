# 生产可达风险并行修复计划

> **对于智能体执行者:** 必需的子技能: 使用 @子代理开发 或 @执行计划 来逐个任务地执行此计划。每个修复 lane 必须使用 @测试驱动开发 和 @完成前验证。执行步骤使用复选框 (`- [ ]`) 语法进行进度追踪。

**目标:** 修复 20-agent 全域审查中已裁决为生产可达的风险；Agent19 范围无生产可达 findings，本计划覆盖其余 19 个 agent 的有效回报。

**架构:** 采用控制器/worker 分离。主 agent 只负责派发、审批写集扩展、审查 diff 和集成验证；worker 在独立 worktree 中按 lane 写失败测试、实现最小修复、运行本 lane 验证。每个 lane 写集互不重叠；确需越界时必须停下并输出 `NEEDS_APPROVAL`，由主 agent 批准后才能继续。

**技术栈:** Go 1.25.7、sqlc SQLite、React/Vite/Vitest、Wails、MCP stdio/http、Fx lifecycle。

---

## 全局执行规则

- [ ] 每个 lane 创建独立 worktree/分支，分支命名形如 `codex/risk-fix-lane-08-mcp-server-boundary`。
- [ ] 每个 worker 只允许修改本计划列出的 `写集`。新增测试文件也必须落在对应测试写集内。
- [ ] 超出写集必须停止并输出 `NEEDS_APPROVAL`。审批请求必须包含实际 lane id、逐行列出的真实文件路径、越界原因、拒绝后的不可修复风险；不得继续编辑越界文件。

- [ ] 禁止静默兜底。配置错误、路径越权、资源超限、存储错误必须返回错误或显式 degraded 状态。
- [ ] TDD 铁律：每个风险点先写失败测试，运行并保存 RED 证据，再写生产代码。
- [ ] 每改完一个 Go 文件，立即运行单文件守卫：

```bash
./scripts/test_with_guard.sh internal/module/mcp_server/service.go
```

- [ ] 完成前验证铁律：没有新鲜验证输出，不得声明 lane 完成。
- [ ] worker 最终报告必须包含：RED 命令和失败摘要、GREEN 命令和通过摘要、改动文件列表、未覆盖风险。

## 并行调度

第一波可并行启动全部 lane。主 agent 集成时按优先级合并：安全/文件写删 > 资源耗尽 > 状态一致性 > 可观测性/审计。

| Lane | 优先级 | 责任域 | 写集状态 |
|---|---|---|---|
| L01 | HIGH | MCP LSP transport/search/installer/diagnostics | 独立 |
| L02 | HIGH | mcp-orch DAG 终态、retry、node lease | 独立 |
| L03 | HIGH | mcp-orch workspace/sharedfile 文件边界 | 独立 |
| L04 | HIGH | Codex app provider pool/recovery/tool policy | 独立 |
| L05 | MED | Claude CLI provider fail-fast/force-complete | 独立 |
| L06 | MED | Unified provider dream/session resolver | 独立 |
| L07 | HIGH | Cron 与 appupdate | 独立 |
| L08 | HIGH | datasource 与 MCP server 安全边界 | 独立 |
| L09 | MED | dashboard/store/sql 查询与 limit | 独立 |
| L10 | HIGH | platform queues、stdio transport、runner | 独立 |
| L11 | MED | platform config/cache/pidregistry | 独立 |
| L12 | HIGH | Wails host bridge/proxy/clipboard | 独立 |
| L13 | MED | frontend-app runtime event/interrupt | 独立 |
| L14 | MED | thread lifecycle prompt/archive | 独立 |
| L15 | MED | memory context/prefetch/team sync | 独立 |
| L16 | MED | skill hash/mirror resource caps | 独立 |
| L17 | HIGH | logger relay 与 mcp-ida payload 脱敏 | 独立 |
| L18 | HIGH | updater helper 命令 timeout/rollback | 独立 |

## Lane L01: MCP LSP 工具链资源与超时

**写集:**
- 修改: `cmd/mcp-lsp/multilsp/transport.go`
- 修改: `cmd/mcp-lsp/multilsp/transport_conn.go`
- 修改: `cmd/mcp-lsp/installer/installer.go`
- 修改: `cmd/mcp-lsp/manager/registry.go`
- 修改: `cmd/mcp-lsp/search/searchutil.go`
- 测试: `cmd/mcp-lsp/multilsp/*_test.go`
- 测试: `cmd/mcp-lsp/installer/*_test.go`
- 测试: `cmd/mcp-lsp/manager/*_test.go`
- 测试: `cmd/mcp-lsp/search/*_test.go`

**唯一最优修复方案:** transport 写入必须受 request ctx 控制，ctx 超时后关闭 stdin 并 kill 对应 LSP 进程；installer 设置本层安装超时；diagnostics 对显式 unsupported language 返回逐文件错误；grep/AST search 在达到 `max_results` 时停止遍历并取消 ast-grep。

- [ ] RED: 添加 `TestTransportRequestWriteHonorsContext`, 构造阻塞 writer，运行 `./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp -run TestTransportRequestWriteHonorsContext -count=1`，预期因阻塞写无法按 ctx 返回而失败。
- [ ] RED: 添加 `TestSearchTextStopsAtMaxResults` 和 `TestSearchASTCancelsAtMaxResults`，运行 `./scripts/test_with_guard.sh ./cmd/mcp-lsp/search -run 'TestSearch(TextStopsAtMaxResults|ASTCancelsAtMaxResults)' -count=1`，预期返回超过上限或进程未取消。
- [ ] RED: 添加 installer timeout 与 diagnostics unsupported tests，运行对应 package 测试并确认失败。
- [ ] GREEN: 将 transport write 包成可取消操作；ctx 到期后调用连接关闭/进程终止路径。
- [ ] GREEN: installer 使用 `context.WithTimeout` 派生本地 deadline。
- [ ] GREEN: diagnostics 返回 `unsupported_files` 或 error envelope，不再空成功。
- [ ] GREEN: search 遍历和 AST decoder 统一使用 `resultLimiter`，超限立即停止。
- [ ] 验证: `./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1`。

## Lane L02: mcp-orch DAG 终态与 lease fencing

**写集:**
- 修改: `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- 修改: `cmd/mcp-orch/orchestration/hook_consumer_dag_fallback.go`
- 修改: `cmd/mcp-orch/orchestration/launcher.go`
- 修改: `cmd/mcp-orch/orchestration/dag.go`
- 修改: `cmd/mcp-orch/tools/task_tools.go`
- 修改: `cmd/mcp-orch/tools/task_tool_definitions.go`
- 修改: `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- 修改: `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`
- 测试: `cmd/mcp-orch/orchestration/*_test.go`
- 测试: `cmd/mcp-orch/tools/*_test.go`

**唯一最优修复方案:** DAG 终态消费失败写入 durable compensation queue；remote terminal event 必须保留并校验 `TurnID`；`task_update_node` 必须带 caller agent/turn/wakeup lease 并在 service/store 双层 fencing；retry SQL 的最大次数由策略传入。

- [ ] RED: 写 `TestRemoteTerminalRequiresTurnID`，运行 `./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run TestRemoteTerminalRequiresTurnID -count=1`，预期当前空 `TurnID` 会通过。
- [ ] RED: 写 `TestTaskUpdateNodeRejectsNonLeaseHolder`，运行 `./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run TestTaskUpdateNodeRejectsNonLeaseHolder -count=1`，预期当前可完成非持有节点。
- [ ] RED: 写 subscriber/fallback store error compensation tests，确认当前只 warn/return。
- [ ] GREEN: handler 注入 `_agentId/_turnId/_wakeupId`，service 校验 lease，SQL 更新加入 fence 条件。
- [ ] GREEN: compensation 表或现有 retry outbox 记录 lookup/fail-cascade/fallback 失败事件。
- [ ] GREEN: retry dispatch SQL 接收策略 max attempts 参数，不再硬编码 8。
- [ ] 验证: `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1`。

## Lane L03: mcp-orch workspace 与 shared file 边界

**写集:**
- 修改: `cmd/mcp-orch/tools/workspace_tools.go`
- 修改: `cmd/mcp-orch/workspace/service.go`
- 修改: `cmd/mcp-orch/workspace/service_merge.go`
- 修改: `cmd/mcp-orch/store/sharedfile/store.go`
- 修改: `internal/platform/sharedfilefs/disk.go`
- 测试: `cmd/mcp-orch/tools/*workspace*_test.go`
- 测试: `cmd/mcp-orch/workspace/*_test.go`
- 测试: `cmd/mcp-orch/store/sharedfile/*_test.go`

**唯一最优修复方案:** `workspace_create_run` 和 service 都强制 `source_root` 位于可信 `_workspaceRoots`；merge/delete 再次校验 `run.SourceRoot` containment；shared file schema/store 持久化 `content_location=disk|inline`，disk-only 文件缺失时 fail-fast。

- [ ] RED: 写 `TestWorkspaceCreateRunRejectsSourceRootOutsideScope`，当前应失败。
- [ ] RED: 写 `TestSharedFileDiskOnlyMissingFileFails`，当前应返回空内容导致失败。
- [ ] GREEN: 工具层从 `ToolScope` 读取 workspace roots，service request 增加 `AllowedRoots`，store 中保存规范化 root。
- [ ] GREEN: merge/write/delete 前统一调用 `ensureSourceRootAllowed`.
- [ ] GREEN: sharedfile 增加 location 字段和迁移；读路径缺磁盘正文返回错误。
- [ ] 验证: `./scripts/test_with_guard.sh ./cmd/mcp-orch/workspace ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-orch/tools -count=1`。

## Lane L04: Codex app provider pool/recovery/native policy

**写集:**
- 修改: `internal/provider/codexapp/server_pool.go`
- 修改: `internal/provider/codexapp/recovery.go`
- 修改: `internal/provider/codexapp/transport.go`
- 修改: `internal/provider/codexapp/driver_pool_routing.go`
- 修改: `internal/provider/codexapp/support.go`
- 测试: `internal/provider/codexapp/*_test.go`

**唯一最优修复方案:** server pool 用 in-flight entry 锁定 spawn 并累计 refCount；replay pending turn 前必须通过 provider 状态确认 turn 丢失； native tool config 类型错误直接返回启动错误；runtime report 使用 session transport URL。

- [ ] RED: 写并发 acquire 测试 `TestServerPoolConcurrentAcquireSharesInFlightSpawn`。
- [ ] RED: 写 recovery 测试 `TestRecoveryDoesNotReplayWhenProviderTurnStillActive`。
- [ ] RED: 写 config 测试 `TestNativeToolPolicyRejectsInvalidListTypes`。
- [ ] GREEN: pool entry 增加 `spawning`/`waiters`，store spawned 不重置已有 refCount。
- [ ] GREEN: recovery 先 query provider turn/session state；无法确认丢失则阻断 replay 并上报恢复待确认。
- [ ] GREEN: runtime report 从 active session transport URL 解析端口。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`。

## Lane L05: Claude CLI provider fail-fast

**写集:**
- 修改: `internal/provider/claudecli/auth_preflight.go`
- 修改: `internal/provider/claudecli/transport_config.go`
- 修改: `internal/provider/claudecli/event_map.go`
- 修改: `internal/provider/claudecli/session_log_watcher_integration.go`
- 修改: `internal/provider/claudecli/session_config.go`
- 测试: `internal/provider/claudecli/*_test.go`

**唯一最优修复方案:** auth status/manifest/timestamp 解析失败全部显式错误；ForceComplete 对 process-gone 归一化为幂等成功并继续收口。

- [ ] RED: 写 `TestAuthPreflightFailsOnInconclusiveStatus`, `TestManifestBuildFailsWhenRequestedServerRejected`, `TestEventTimeRejectsMissingTimestamp`, `TestForceCompleteTreatsProcessGoneAsIdempotent`。
- [ ] GREEN: preflight 返回 error；manifest builder 返回 rejected server error；event/log decode 缺 timestamp 产生 provider error；ForceComplete 使用 `normalizeSignalError`。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/provider/claudecli -count=1`。

## Lane L06: Unified provider dream/session resolver

**写集:**
- 修改: `internal/provider/unified/dream_executor.go`
- 修改: `internal/provider/unified/session_resolver.go`
- 修改: `internal/provider/unified/session_resolver_auto_resume.go`
- 测试: `internal/provider/unified/*_test.go`

**唯一最优修复方案:** `DREAM_PROVIDER_ORDER` 出现未知 provider 时阻断启动；auto-resume runtime config 读取只允许 NotFound 继续，DB/解码错误直接返回。

- [ ] RED: 写 `TestDreamProviderOrderRejectsUnknownProvider`。
- [ ] RED: 写 `TestAutoResumeRuntimeConfigFailsOnThreadStoreError`。
- [ ] GREEN: `resolveProviderOrder` 返回 `([]string, error)`；resolver helper 返回 `(map[string]any, error)`。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/provider/unified -count=1`。

## Lane L07: Cron 与 appupdate

**写集:**
- 修改: `internal/module/cron/scheduler_recovery.go`
- 修改: `internal/module/cron/scheduler.go`
- 修改: `internal/module/cron/turn_adapter.go`
- 修改: `internal/module/appupdate/service.go`
- 测试: `internal/module/cron/*_test.go`
- 测试: `internal/module/appupdate/*_test.go`

**唯一最优修复方案:** cron terminal handler 同时消化 submitted/running run，并让 observe 已终态时直接 finalize；appupdate 下载使用 manifest size hard cap、request timeout 和临时文件超限删除。

- [ ] RED: 写 `TestCronTerminalEventFinalizesSubmittedRun`。
- [ ] RED: 写 `TestAppUpdateDownloadRejectsBodyLargerThanManifestSize`。
- [ ] GREEN: cron completion 按 turnID 查询 unresolved run 并 CAS 终态；`CronTrackTurn` 返回 terminal state。
- [ ] GREEN: appupdate 用 `io.LimitReader(resp.Body, artifact.Size+1)` 和 counting writer，超过 size 删除 tmp。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/appupdate -count=1`。

## Lane L08: datasource 与 MCP server 安全边界

**写集:**
- 修改: `internal/module/datasource_v2/service.go`
- 修改: `internal/module/datasource_v2/pdf_extract.go`
- 修改: `internal/module/datasource/extract.go`
- 修改: `internal/module/datasource/pdf_extract.go`
- 修改: `internal/module/mcp_server/service.go`
- 修改: `internal/module/mcp_server/sqlite.go`
- 修改: `internal/module/mcp_server/http_tools.go`
- 修改: `internal/platform/httpegress/policy.go`
- 测试: `internal/module/datasource_v2/*_test.go`
- 测试: `internal/module/datasource/*_test.go`
- 测试: `internal/module/mcp_server/*_test.go`
- 测试: `internal/platform/httpegress/*_test.go`

**唯一最优修复方案:** datasource 本地导入只允许 workspace 内路径；PDF/text 解析统一 size/decompressed/text/chunk 上限；datasourceV2/list 在 service 层拒绝超大 limit；MCP stdio argv 使用精确模板；sqlite RPC 只允许产品 DB；HTTP egress 使用逐跳 redirect 校验和解析后 IP 拒绝。

- [ ] RED: 写 workspace 越权导入、PDF 解压超限、datasourceV2/list 超大 limit、npx 参数绕过、sqlite 任意 path、HTTP redirect-to-localhost 六组测试。
- [ ] GREEN: datasource 引入 `ImportLimits` 常量并在读/解压/chunk 前检查。
- [ ] GREEN: MCP stdio validator 解析 command family 和 argv exact shape，启动层复用同一 validator。
- [ ] GREEN: sqlite `StartSQLiteServer` 忽略请求 path 并固定到配置 DB。
- [ ] GREEN: httpegress client 使用 custom `CheckRedirect` 和 `DialContext`，每个 hop 校验最终 IP。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/module/datasource_v2 ./internal/module/datasource ./internal/module/mcp_server ./internal/platform/httpegress -count=1`。

## Lane L09: dashboard/store/sql 查询边界

**写集:**
- 修改: `sql/queries/audit_log.sql`
- 修改: `sql/queries/session_insight.sql`
- 修改: `internal/store/auditlog/store.go`
- 修改: `internal/store/dbquery/executor.go`
- 修改: `internal/store/insight/store.go`
- 修改: `internal/module/dashboard/logs.go`
- 修改: `internal/module/dashboard/insights_rpc.go`
- 测试: `internal/store/auditlog/*_test.go`
- 测试: `internal/store/dbquery/*_test.go`
- 测试: `internal/store/insight/*_test.go`

**唯一最优修复方案:** audit list 返回真实 `extra`；dbquery 执行规范化后的 SQL；dashboard insight 类 list limit 统一定义最大值，超过上限返回错误。datasourceV2/list 的 limit 修复由 L08 负责，避免并行写集冲突。

- [ ] RED: 写 audit extra roundtrip、dbquery actual SQL includes injected LIMIT、insight oversized limit rejected tests。
- [ ] GREEN: 更新 sqlc query 并运行 `make sqlc-verify`。
- [ ] GREEN: `prepareQueryContext` 返回 normalized SQL，执行层只使用 normalized SQL。
- [ ] GREEN: service/store limit 校验使用同一 `MaxListLimit`，超限返回 invalid argument。
- [ ] 验证: `make sqlc-verify`；`./scripts/test_with_guard.sh ./internal/store/auditlog ./internal/store/dbquery ./internal/store/insight ./internal/module/dashboard -count=1`。

## Lane L10: platform queues、stdio transport、runner shutdown

**写集:**
- 修改: `internal/platform/rpc/push_worker.go`
- 修改: `internal/platform/hooks/dispatch_worker.go`
- 修改: `internal/platform/mcpcontrol/config_fanout_worker.go`
- 修改: `internal/mcpserver/common/stdio.go`
- 修改: `internal/platform/toolbridge/stdio_mcp_client.go`
- 修改: `internal/platform/runner/contract.go`
- 测试: `internal/platform/rpc/*_test.go`
- 测试: `internal/platform/hooks/*_test.go`
- 测试: `internal/platform/mcpcontrol/*_test.go`
- 测试: `internal/mcpserver/common/*_test.go`
- 测试: `internal/platform/runner/*_test.go`

**唯一最优修复方案:** push/hooks/config fanout 改为有界队列和按语义 coalesce；stdio framed/raw 输入设最大消息大小；runner Stop 使用独立 shutdown ctx 并保留 drain 超时错误。

- [ ] RED: 写三组 queue overflow tests，确认当前无上限。
- [ ] RED: 写 stdio `Content-Length` 超限 test，确认当前分配大 body。
- [ ] RED: 写 `TestWorkerRunnerStopUsesFreshShutdownContext`。
- [ ] GREEN: queue worker 增加 capacity、latest-only key、dropped metric/degraded event。
- [ ] GREEN: stdio reader 在分配前拒绝超过 `MaxMCPMessageBytes` 的 framed body，raw decoder 包 capped reader。
- [ ] GREEN: workerRunner 使用 `context.WithTimeout(context.Background(), shutdownTimeout)` 调 Stop。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/platform/hooks ./internal/platform/mcpcontrol ./internal/mcpserver/common ./internal/platform/runner -count=1`。

## Lane L11: platform config/cache/pidregistry fail-fast

**写集:**
- 修改: `internal/platform/config/config.go`
- 修改: `internal/platform/cachekeepalive/module.go`
- 修改: `internal/platform/pidregistry/pidregistry.go`
- 修改: `internal/provider/codexapp/pool_spawner.go`
- 测试: `internal/platform/config/*_test.go`
- 测试: `internal/platform/cachekeepalive/*_test.go`
- 测试: `internal/platform/pidregistry/*_test.go`
- 测试: `internal/provider/codexapp/*pidregistry*_test.go`

**唯一最优修复方案:** env 变量存在但非法必须返回配置错误； cache keepalive shutdown 错误从 Fx hook 返回； pidregistry persist 返回 error，spawn 注册失败立即 kill 子进程并返回失败。

- [ ] RED: 写 invalid env fails startup、cache shutdown error propagates、pidregistry persist error aborts spawn tests。
- [ ] GREEN: env parser 区分 unset 与 invalid；OnStop 返回 shutdown error；`Register` 返回 error。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/platform/config ./internal/platform/cachekeepalive ./internal/platform/pidregistry ./internal/provider/codexapp -count=1`。

## Lane L12: Wails host bridge/proxy/clipboard

**写集:**
- 修改: `internal/ui/wails/assets.go`
- 修改: `internal/ui/wails/rpc.go`
- 修改: `run-new-ui-desktop.sh`
- 修改: `run-new-ui-desktop.ps1`
- 测试: `internal/ui/wails/*_test.go`
- 测试: `scripts/*new_ui_desktop*_test.go`

**唯一最优修复方案:** `VITE_DEV_URL` 只接受 `http/https` 且 host 为 loopback；脚本层同步拒绝非 loopback；clipboard RPC 保留原始文本，只用长度判空。

- [ ] RED: 写 `TestViteDevProxyRejectsNonLoopbackURL` 和 `TestCopyTextPreservesLeadingAndTrailingWhitespace`。
- [ ] GREEN: assets proxy 调用 `validateLoopbackDevURL`；shell/powershell 脚本复用同一规则；copyText 删除 `TrimSpace`。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/ui/wails -count=1`；`./scripts/test_with_guard.sh ./scripts -run 'TestNewUIDesktopDev' -count=1`。

## Lane L13: frontend-app runtime event 与 interrupt 语义

**写集:**
- 修改: `frontend-app/src/entities/client/model/runtimeSlice.js`
- 修改: `frontend-app/src/shared/api/wailsBridge.js`
- 修改: `frontend-app/src/entities/client/model/threadLifecycleRuntime.js`
- 测试: `frontend-app/src/entities/client/model/*runtime*.test.*`
- 测试: `frontend-app/src/shared/api/*wailsBridge*.test.*`

**唯一最优修复方案:** event subscribe 返回 `{ready, unsubscribe}`；只有 ready 后才记录 unsubscribe；`activeThreadRPC` 对 `ok:false` 显示 warning 并返回 false。

- [ ] RED: 写 Vitest：首次 runtime 不可用第二次可用必须重新注册 `bridge-event`；`turn/interrupt` 返回 `ok:false` 时不得显示成功。
- [ ] GREEN: `subscribeRuntimeEvent` 明确 ready 状态；runtimeSlice 失败时清空标记并可重试；thread lifecycle 读取 result.ok。
- [ ] 验证: `cd frontend-app && npm test -- src/entities/client/model src/shared/api`。
- [ ] 验证: `cd frontend-app && npm run lint && npm run build`。

## Lane L14: thread lifecycle prompt/archive

**写集:**
- 修改: `internal/module/thread/archive.go`
- 修改: `internal/module/thread/lifecycle_fork.go`
- 修改: `internal/module/thread/lifecycle_helpers.go`
- 修改: `internal/module/thread/prompt_snapshot.go`
- 测试: `internal/module/thread/*_test.go`

**唯一最优修复方案:** pending_launch unarchive 走专用事务路径并发布事件； fork 成功持久化时立即保存继承 prompt snapshot，保存失败则停止新 session 并回滚。

- [ ] RED: 写 `TestUnarchivePendingLaunchDoesNotRequireBinding`。
- [ ] RED: 写 `TestForkPersistsInheritedPromptSnapshotBeforeReturning`。
- [ ] GREEN: pending_launch 分支跳过 binding archived 标记并发 `unarchived_pending_launch` 投影。
- [ ] GREEN: `Fork` 在 `persistThreadState` 同事务或同失败边界保存 snapshot。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/module/thread -count=1`。

## Lane L15: memory context/prefetch/team sync

**写集:**
- 修改: `internal/module/memory/rules_provider.go`
- 修改: `internal/module/memory/service.go`
- 修改: `internal/module/memory/retrieval/prefetch.go`
- 修改: `internal/module/memory/team/thread_metadata.go`
- 修改: `internal/module/memory/module.go`
- 测试: `internal/module/memory/*_test.go`
- 测试: `internal/module/memory/team/*_test.go`
- 测试: `internal/module/memory/retrieval/*_test.go`

**唯一最优修复方案:** memory root 解析失败阻断 Fx 构造；prefetch 错误返回 turn context error；team metadata store/json 错误返回 error，不回退 CWD。

- [ ] RED: 写 root invalid、prefetch error surfaced、team metadata parse error tests。
- [ ] GREEN: `NewContextProvider` 返回 `(*ContextProvider, error)` 并调整 Fx provider；`ConsumeIfReady` 暴露错误；team builder 返回 error。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/module/memory/... -count=1`。

## Lane L16: skill hash/mirror resource caps

**写集:**
- 修改: `internal/module/skill/skillhash/hash.go`
- 修改: `internal/module/skill/mirror_manifest.go`
- 修改: `internal/module/skill/mirror_publisher.go`
- 修改: `internal/module/skill/skills_import.go`
- 测试: `internal/module/skill/**/*_test.go`

**唯一最优修复方案:** skill import/hash/mirror 统一使用 streaming hash/copy，设置单文件、总字节、文件数上限；超限返回错误并停止 provider mirror。

- [ ] RED: 写大文件 import hash 超限和 mirror copy 超限 tests。
- [ ] GREEN: 引入 `SkillContentLimits`，hash 使用 streaming `io.CopyN`，mirror copy 使用 bounded reader。
- [ ] 验证: `./scripts/test_with_guard.sh ./internal/module/skill/... -count=1`。

## Lane L17: logger relay 与 mcp-ida payload 脱敏

**写集:**
- 修改: `pkg/logger/relay.go`
- 修改: `pkg/logger/redact.go`
- 修改: `cmd/mcp-ida/fx.go`
- 测试: `pkg/logger/*_test.go`
- 测试: `cmd/mcp-ida/*_test.go`

**唯一最优修复方案:** relay 发送前复用同一脱敏函数；mcp-ida 不记录 raw payload，只记录 scope/version/selector/payload_size/payload_hash。

- [ ] RED: 写 relay token/password/api_key 脱敏测试；写 mcp-ida config changed log 不含 payload 测试。
- [ ] GREEN: relay attr walker 调用 `sanitizeLogAttr`；mcp-ida payload 字段替换为 hash/size。
- [ ] 验证: `./scripts/test_with_guard.sh ./pkg/logger ./cmd/mcp-ida -count=1`。

## Lane L18: updater helper timeout 与 rollback

**写集:**
- 修改: `cmd/super-dolphin-updater/install.go`
- 修改: `cmd/super-dolphin-updater/main.go`
- 测试: `cmd/super-dolphin-updater/*_test.go`

**唯一最优修复方案:** helper 所有外部命令使用 `exec.CommandContext` 和命令级 timeout；安装先复制到 staging app 并校验，再短窗口替换 target；任何 timeout/error 走统一 rollback。

- [ ] RED: 写 `TestRunCommandTimesOutAndKillsProcessGroup`。
- [ ] RED: 写 `TestInstallRollsBackWhenDittoTimesOutAfterBackup`。
- [ ] GREEN: `runCommand(ctx, timeout, name, args...)` 创建进程组并在 ctx done kill group。
- [ ] GREEN: `install` 改为 staged copy -> verify -> atomic replace -> cleanup，失败调用 rollback。
- [ ] 验证: `./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1`。

## 主 agent 集成与最终验证

- [ ] 收齐所有 lane 汇报后，主 agent 逐个检查 `git diff --name-only` 是否完全落在写集内。
- [ ] 对任何越界 diff，要求 worker 给出审批记录；没有审批记录则拒绝集成该 lane。
- [ ] 合并顺序：L08, L03, L02, L04, L07, L10, L18, L17, 其余 MED lane。
- [ ] 每合并一个 lane，运行该 lane 的验证命令。
- [ ] 所有 lane 合并后运行：

```bash
make sqlc-verify
make guard
make test
make build-plain
cd frontend-app && npm run lint && npm test && npm run build
```

- [ ] 最终报告必须列出每条命令的退出码和关键输出。任何失败命令都阻断“完成”声明。

## 子 agent 派发模板

```text
你负责主 agent 指派的单个 Lane ID。只允许修改计划中的写集；新增测试只能落在测试写集内。

必须使用 TDD：
1. 先写失败测试。
2. 运行指定 RED 命令，确认按预期失败。
3. 写最小生产代码。
4. 运行 GREEN 命令和单文件 guard。
5. 运行 lane 验证命令。

如果需要修改写集外文件，立即停止并输出 NEEDS_APPROVAL，不得自行越界。

最终只返回：
- RED 证据
- GREEN/验证证据
- 修改文件
- 是否有越界审批
- 剩余风险
```
