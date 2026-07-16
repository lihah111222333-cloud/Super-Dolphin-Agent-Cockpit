# Reasonix Production Hardening - Task 0 Design Freeze

TASK_ID: `TASK_0_DESIGN`

STATUS: `RELEASE_OPEN_MCP_DECISION_PENDING`

SOURCE_HEAD: `1ea371f4e39279703dd2023a94add2dccafbcfa8`

HISTORICAL_BASE_HEAD: `b40867229af8e17916c00393639ccb0fcb4bf6fc`

`release_design_complete=true`

`mcp_design_complete=false`

`implementation_design_complete=derived(release_design_complete && mcp_design_complete)=false`

`p0_release_executable=true`

`p0_mcp_executable=false`

`p0_executable=derived(p0_release_executable && p0_mcp_executable)=false`

本文件冻结两个独立 P0 lane 的最小设计、源码 owner、landing package、fail-first 测试和开门 blocker。它不实现 P0，不把预计 RED 写成已运行 RED，也不要求 Task 0 手写所有未来字段。

## 1. 独立门禁

| Lane | 独立状态 | 开放任务 | 不受其阻塞 |
| --- | --- | --- | --- |
| Release transaction/recovery | `p0_release_executable` | Task 1-3 | MCP compiler 选型、MCP review finding |
| Codex schema compile/quarantine | `p0_mcp_executable` | Task 4 | Release supervisor、Recovery、packaging finding |

`p0_executable` 只按 AND 派生总状态，不能反向成为 Task 1-4 的 all-or-nothing gate。共享 finding 只有在同一缺陷真实影响两个 lane 时才能同时登记；不得按“同属 P0”跨 lane 阻断。

## 2. 当前源码 owner 冻结

本次在 `main@1ea371f4...` 上取得 `grep/structure`、`inspect`、`xref`、`file(read_file)`、`file(diagnostics)` 五类 LSP 证据。

| 事实 | LSP 结果 | Design Freeze 裁决 |
| --- | --- | --- |
| agent runtime 事件投影 | `internal/platform/eventsurface/bind.go:bindAgentLifecycle` 订阅；`bind_payloads.go:agentRuntimeReportedPayload` 组 payload | 正确 owner 是 `internal/platform/eventsurface`；不存在的 `internal/ui/eventsurface/agent_runtime.go` 不得作为 landing path |
| `turn/start` | `internal/module/turn/rpc.go` 注册 `turnStartHandler`；`rpc_helpers.go:turnStartHandler` 调用 `Service.PrepareTurn` 与 `Service.StartTurn`；实现为 `service.go:(*service).StartTurn` | backend send/turn gate 首落 `rpc_helpers.go`，不是 `internal/app/turn_rpc.go` |
| turn MCP manifest | `internal/module/turn/manifest.go:mcpServerConfigBinary` 构造 HTTP/stdio `dto.MCPBinary` | `dto.MCPBinary.TrustedServerID` 已存在，但当前构造未赋值；这是 MCP lane 的 P0 gap |

这些 P0 owner 源码文件本轮 diagnostics 均为零。旧 evidence 点名的 `internal/module/uistate/overlay_test.go` 当前也为零，但依赖不在本写集的并发未提交修复，不能写成 HEAD 证据；provider-wide status 已移入 follow-up，因此它不再阻断 Release 或 MCP lane。diagnostics 不是测试，后续仍需具名 fail-first/GREEN。

## 3. Release Lane Freeze

### 3.1 In scope

- durable update transaction、backup/staging parity、状态 journal、锁与 crash replay；
- probation supervisor、exact health ACK、healthy commit、timeout/crash rollback；
- detached Guard 接管 stale transaction；
- normal-only preflight 前的 selector 与 Recovery-only graph；
- package-owned update source/manifest key/package signer policy，production env/CLI bypass fail-fast；
- transaction-bound pending/committed trust generation；
- 实际新增 producer 的动态 changed-field guard；
- macOS 当前更新入口的产物级 E2E；其它平台在无同等证据时同步禁用 check/install/publication。

### 3.2 Out of scope

完整 ProductIP、ObservedArtifactInventory、SBOM/component graph、release attribution/digest graph、provider CLI distribution/compatibility/ingress 均移入 follow-up。Release lane 只记录 transaction 所需的 exact release/helper digest 和 signer identity，不构建全产品 component graph。

### 3.3 Landing packages

| Path | Frozen responsibility |
| --- | --- |
| `internal/module/appupdate/recovery/` | transaction/attempt/trust/ACK state、journal、lock、rollback/commit |
| `internal/module/appupdate/{service.go,manifest.go,github_release.go,rpc.go}` | package-owned update trust inputs、transaction start、typed status/RPC |
| `cmd/super-dolphin-updater/` | install、backup retention、probation supervision、healthy/rollback |
| `cmd/super-dolphin-guard/` | detached check/status/restore；不加载 desktop/provider/toolbridge/store graph |
| `cmd/agent-terminal/main.go`、`internal/app/recovery_*.go` | early selector、normal child/re-exec boundary、Recovery-only composition |
| `internal/platform/runtimeenv/` | normal environment plan 与 Recovery/Guard allowlist |
| `frontend-app/src/features/update-recovery/` 与最小 bootstrap owner | typed recovery state、Safe Mode banner/action disable；不复用 normal ready |
| package/verify scripts 与 tests | protocol/helper/release parity、平台 capability fail-closed、artifact E2E |

### 3.4 Frozen fail-first tests

测试名可在实现前因 package 预算作等价重命名，但 Task 0 review object 必须先更新，不能实现后补写计划。

| Test | Initial command | Expected RED / zero side effect |
| --- | --- | --- |
| `TestUpdateTransactionRetainsBackupUntilHealthy` | `./scripts/test_with_guard.sh ./internal/module/appupdate/recovery ./cmd/super-dolphin-updater -run '^TestUpdateTransactionRetainsBackupUntilHealthy$' -count=1` | candidate 安装成功即删 backup；healthy commit 前 delete count 必须为零 |
| `TestProbationFailureRollsBackExactTransaction` | 同上，`-run '^TestProbationFailureRollsBackExactTransaction$'` | crash/ACK timeout 未恢复 exact old release；wrong transaction restore/restart count 为零 |
| `TestTrustGenerationCommitsOnlyAfterHealthy` | `./scripts/test_with_guard.sh ./internal/module/appupdate/recovery -run '^TestTrustGenerationCommitsOnlyAfterHealthy$' -count=1` | pending generation 提前成为 committed；active trust write count 为零 |
| `TestPackagedUpdateTrustRejectsEnvironmentAndCLIOverride` | `./scripts/test_with_guard.sh ./internal/module/appupdate ./cmd/super-dolphin-updater -run '^TestPackagedUpdateTrustRejectsEnvironmentAndCLIOverride$' -count=1` | env key/source、allow-unsigned、signer override 生效；network/replace count 为零 |
| `TestAgentTerminalSelectsRecoveryBeforeNormalPreflight` | `./scripts/test_with_guard.sh ./cmd/agent-terminal ./internal/app -run '^TestAgentTerminalSelectsRecoveryBeforeNormalPreflight$' -count=1` | pending/corrupt transaction 进入 normal graph；provider/toolbridge/store constructor count 为零 |
| `TestGuardTakesOverStaleProbationOnce` | `./scripts/test_with_guard.sh ./cmd/super-dolphin-guard -run '^TestGuardTakesOverStaleProbationOnce$' -count=1` | stale/wrong lease 重复 rollback；第二次 restore/restart count 为零 |
| `TestRecoveryGraphContainsOnlyAllowedConstructors` | `./scripts/test_with_guard.sh ./internal/app -run '^TestRecoveryGraphContainsOnlyAllowedConstructors$' -count=1` | normal constructor 出现在 Recovery graph；构造失败并报告 exact owner |
| `TestUnsupportedUpdatePlatformHasNoHalfOpenRoute` | `./scripts/test_with_guard.sh ./internal/module/appupdate ./scripts -run '^TestUnsupportedUpdatePlatformHasNoHalfOpenRoute$' -count=1` | 无 transaction 证据的平台仍能 check/install/publish；网络/写入 count 为零 |

Release focused GREEN command 由 staged gate plan 生成；最低覆盖上述 packages、对应 frontend lint/test/build、archtest、package smoke 和 artifact E2E。

## 4. MCP Lane Freeze

### 4.1 In scope

- HTTP/stdio tools/list envelope decode 与逐项 `json.RawMessage`；
- tool identity/classification 后的统一 schema canonicalize/compile；
- managed/first-party fail-fast 与 trusted external per-tool quarantine；
- stable diagnostics、resource budgets、cancellation/isolation；
- config-owner authority、generation/digest/membership current-CAS；
- start/resume/turn `TrustedServerID` carrier parity；
- quarantine/surface/catalog/proxy/call 的一致结果；
- Codex dynamic tool projection；
- 实际新增 producer 的动态 changed-field guard。

### 4.2 Out of scope

Claude provider-wide readiness、provider executable/compatibility/ingress、通用 RPC principal/workspace token、通用 single-use admission/effect reconciliation 不属于本 lane。当前 P0 不修改 `internal/platform/eventsurface` 或 `turnStartHandler` 来实现 provider-wide status/send gate；它们只作为 follow-up 的正确 owner 记录。

### 4.3 Landing packages

| Path | Frozen responsibility |
| --- | --- |
| `internal/platform/toolbridge/{http_mcp_client.go,stdio_mcp_client.go,types.go}` | envelope decode、raw tool items、server-level identity errors |
| `internal/platform/toolbridge/schema/` | canonicalization、prewalk budgets、compile、digest、diagnostic、quarantine plan |
| `internal/platform/toolbridge/{handler.go,proxy.go,module.go,schema_quarantine.go}` | class policy、current-CAS、surface/catalog/proxy/call result consistency |
| `internal/module/mcp_server/` 与现有 store/sqlc owner | config-owner current generation/membership 与 durable quarantine/refresh 最小状态 |
| `internal/contract/mcp_control.go` 或审查后确认的窄子包 | authority snapshot/current read/CAS ports；不得顺带引入 generic RPC/admission |
| `internal/module/thread/{mcp_server_config.go,start_session_helpers.go}` | trusted config-owner reference 的 start/resume producer |
| `internal/module/turn/{service_helpers.go,manifest.go}` | turn carrier 与 `mcpServerConfigBinary` |
| `internal/dto/provider/manifest.go`、`internal/provider/shared/config_helpers.go` | existing `TrustedServerID` 字段与 provider conversion validation |
| `go.mod`、`go.sum` 与现有 dependency/NOTICE owner | 仅在 compiler decision gate 通过后修改 |

### 4.4 Compiler isolation decision gate

Task 0 不再预选 `github.com/santhosh-tekuri/jsonschema/v6`，也不预建 `cmd/mcp-schema-compiler-worker` 或 `process_pool.go`。候选库必须先形成可复核 evidence：

1. 版本、license、module sums、已知漏洞与 Draft 支持；
2. deny external loader/reference 的可执行 proof；
3. adversarial schema 下的预解析 bytes/node/depth/ref/regex bounds；
4. compile 阶段能被 `context` 或库自身机制安全取消，并证明超时后无后台 goroutine/heap/cache 写入；
5. 若第 4 项不能证明，则冻结有界进程协议、输入/输出上限、kill/reap deadline、并发上限、平台支持矩阵和 stale-result fencing 后才能实现；
6. 若进程内 hard bound 与进程隔离两者都没有可执行证据，`p0_mcp_executable` 保持 false。

只有 decision 选择进程隔离后，才允许给 helper binary 和 package 命名；不得用“可能需要隔离”提前扩大写集。

### 4.5 Frozen fail-first tests

| Test | Initial command | Expected RED / zero side effect |
| --- | --- | --- |
| `TestToolsListWireDecodeDefersPerToolSchema` | `./scripts/test_with_guard.sh ./internal/platform/toolbridge -run '^TestToolsListWireDecodeDefersPerToolSchema$' -count=1` | mixed good/bad item 在 transport 层整表失败；lifecycle/quarantine write count 为零 |
| `TestTrustedExternalQuarantinesOnlyInvalidTool` | 同上，`-run '^TestTrustedExternalQuarantinesOnlyInvalidTool$'` | 坏项拖垮好项或仍进入 surface；坏项 client count 为零、好项保持一 |
| `TestManagedSchemaFailureRemainsFailFast` | 同上，`-run '^TestManagedSchemaFailureRemainsFailFast$'` | managed server 被静默 quarantine；surface/client/quarantine write count 为零 |
| `TestSchemaCompilerRejectsReferencesAndBudgets` | `./scripts/test_with_guard.sh ./internal/platform/toolbridge/schema -run '^TestSchemaCompilerRejectsReferencesAndBudgets$' -count=1` | external ref、非 object root、keyword type、bytes/node/depth/ref 超限未返回稳定 code |
| `TestSchemaCompilerCancellationOrIsolationIsBounded` | 同上，`-run '^TestSchemaCompilerCancellationOrIsolationIsBounded$'` | cancel/timeout 后仍有 goroutine/process/cache/lifecycle/quarantine 增量；MCP lane 必须保持 blocked |
| `TestMCPAuthorityCurrentCASBlocksStaleSurfaceAndCall` | `./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/module/mcp_server -run '^TestMCPAuthorityCurrentCASBlocksStaleSurfaceAndCall$' -count=1` | config delete/disable/N+1 后旧 surface/grant 仍可调用；真实 client count 为零 |
| `TestMCPServerConfigTrustedIDStartResumeTurnParity` | `./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/turn ./internal/provider/shared -run '^TestMCPServerConfigTrustedIDStartResumeTurnParity$' -count=1` | `mcpServerConfigBinary` 丢失/篡改 `TrustedServerID` 或由 raw name 恢复 authority；provider/toolbridge count 为零 |
| `TestQuarantineRepairAndRegressionReconcilesSurface` | `./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/module/mcp_server -run '^TestQuarantineRepairAndRegressionReconcilesSurface$' -count=1` | 修复不恢复、再损坏不撤下或覆盖 manual lifecycle state |
| `TestCompiledSchemaDigestIsSingleSourceAcrossConsumers` | `./scripts/test_with_guard.sh ./internal/platform/toolbridge -run '^TestCompiledSchemaDigestIsSingleSourceAcrossConsumers$' -count=1` | catalog/provider/proxy/call 重编译或 digest 不一致；client count 为零 |

MCP focused GREEN command 由 staged gate plan 生成；最低覆盖上述 packages、Codex dynamic tool tests、archtest、dependency policy、race/resource tests。

## 5. Dynamic Changed-Field Guard Freeze

不存在也不新增声称覆盖全仓字段的 scanner。Task 0 不登记尚未出现的几十个 future producer/field。

每个实际实现 commit 按以下规则建立 package-local guard：

1. 以本 commit 新增/修改的 struct、schema、SQLC row、registry 或 DTO 为 producer truth。
2. 用 reflection、AST、schema parser、SQLC metadata 或类型系统动态枚举字段；测试不得复制字段名清单。
3. 显式登记已发现 mapper 与 terminal consumers；计算 missing/stale，unknown/parse failure 直接阻断。
4. 为真实 mapper 或 terminal consumer 做至少一个 mutation RED；报告 chain ID、producer 和 field。
5. exemption 必须有 `Field | Direction | Reason | Evidence/Owner`，并反查 stale。
6. guard 只声明该 producer 的已发现链路，不得写“全仓扫描通过”。

最低 guard roots 随实际实现产生：

- Release：transaction、trust generation、health ACK、Recovery projection 中实际新增/修改的 producer；
- MCP：compiled schema、quarantine/current authority 中实际新增/修改的 producer；
- 既有链修复：config-owner trusted reference -> thread start/resume mapper -> turn `mcpServerConfigBinary` -> `dto.MCPBinary.TrustedServerID` -> provider/toolbridge validation。

## 6. Follow-up Ledger

| ID | Deferred but not cancelled |
| --- | --- |
| `FOLLOWUP_RELEASE_COMPOSITION_IP` | ProductIP、final artifact inventory、SBOM/component graph、release attribution/digest graph 与产品声明生成 |
| `FOLLOWUP_PROVIDER_EXECUTION_INGRESS` | provider-wide executable prepare identity、compatibility matrix、ingress/protocol drift 与 failure attribution |
| `FOLLOWUP_CLAUDE_READINESS` | Claude readiness/status、真实 CLI generation E2E、frontend/backend send gate；事件 owner 为 `internal/platform/eventsurface`，send owner 为 `turnStartHandler -> StartTurn` |
| `FOLLOWUP_RPC_PRINCIPAL_ADMISSION` | generic authenticated connection/principal/workspace token/single-use admission/effect reconciliation |

这些条目不得被描述为已取消，也不得作为当前 Release/MCP lane 的开门 blocker。

## 7. Current Blockers And Review Closure

### Release blockers

- none; main Agent review completed and Task 1-3 are open.

### MCP blockers

- `MCP_COMPILER_CANCELLATION_OR_ISOLATION_DECISION_REQUIRED`

Required sequence:

1. 每个实现任务由独立实现 Agent 完成，主 Agent 初审后立即合并 `codex/integration-reasonix-p0`；逐任务不增加 reviewer Agent。
2. Release Task 1-3 已开放，按顺序落地并保持 task branch/worktree 与 integration 串行合并。
3. MCP compiler decision evidence 先作为独立小任务落地；主 Agent 初审通过后设置 `mcp_design_complete=true`、`p0_mcp_executable=true` 并开放 Task 4。
4. Task 1-4 和 Task 5 全部完成后，冻结 integration exact commit，启动三个全新、无继承上下文的总审 Agent。
5. 最终总审按受影响 lane 登记 finding；任何 P0/P1 返修后重跑受影响门禁与三 Agent 总审。
6. 总状态始终重新派生，不得手工单独翻转。

Current verdict: `RELEASE_EXECUTABLE`。Task 1-3 开放；Task 4 仅由 compiler decision blocker 关闭。
