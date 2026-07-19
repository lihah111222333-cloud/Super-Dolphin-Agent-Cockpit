# Reasonix 生产硬化能力下一批吸收计划

> 日期：2026-07-15
> 当前源码对象：`codex/reasonix-p0-mcp-decision@3d6fccfc58b904e2c9a6f358285cdee6d6ea7753`
> 类型：docs-only、clean-room 行为吸收决策与实施计划
> 吸收决策内容状态：`absorption_decision_content_complete=true`
> 实施设计状态：`implementation_design_complete=true`
> Release P0 执行状态：`p0_release_executable=true`
> MCP P0 执行状态：`p0_mcp_executable=true`
> P0 总状态：`p0_executable=derived(p0_release_executable && p0_mcp_executable)=true`
> 当前复核状态：`current_main_recheck=main_review_complete`

## 0. 结论与门禁

本轮 P0 只保留原始两项目标：

1. **Release transaction/recovery**：更新替换成功后仍能监督 probation、提交 healthy 或按 exact transaction 自动回滚，并在 normal 启动不可用时进入独立 Recovery/Safe Mode。
2. **Codex toolbridge schema compile/quarantine**：真实编译 MCP tool input schema；trusted external server 的坏工具逐项隔离，好工具继续可用；managed/first-party server 保持 fail-fast。

两条 P0 lane 独立开门，禁止因为另一条 lane 的 blocker 未清零而相互阻塞：

| 派生状态 | 只依赖 | 开放任务 |
| --- | --- | --- |
| `p0_release_executable` | Release owner/设计、LSP/diagnostics 与主 Agent 初审 blocker 全部清零 | Task 1-3 |
| `p0_mcp_executable` | MCP owner/设计、LSP/diagnostics、compiler 隔离裁决与主 Agent 初审 blocker 全部清零 | Task 4 |
| `p0_executable` | `p0_release_executable && p0_mcp_executable` | 仅表示两条 P0 lane 均已开放，不作为 Task 1-4 的共同前置门禁 |

`implementation_design_complete` 同样是两条 lane 设计完成状态的派生汇总；单 lane 可执行不要求另一 lane 已完成。Task 5 的联合收口才要求两条 lane 都完成。

Release lane 已由主 Agent 完成 owner、设计、LSP、diagnostics 和计划合理性初审，Task 1-3 开放。MCP lane 已冻结 one-shot local helper 进程、超时 kill/reap、协议预算、generation/digest fence、跨平台构建和稳定错误码，compiler decision blocker 已清零，Task 4 开放。逐任务不再追加独立 reviewer；全部实现合入 integration 后再启动三条全新总审 lane。

## 1. Review object 与源码 owner 事实

| 对象 | 状态 | 用途 |
| --- | --- | --- |
| `deepseek-reasonix main-v2@ad9c3fc138b3e7b953405d94b96027b3275c4a50` | 固定参考对象 | 只参考可观察行为、失败模式和测试场景 |
| `super-agent-v3 codex/reasonix-p0-mcp-decision@3d6fccfc58b904e2c9a6f358285cdee6d6ea7753` | 当前源码对象 | 本次 toolbridge LSP owner 与 compiler decision 核对对象 |
| `b40867229af8e17916c00393639ccb0fcb4bf6fc` | 历史 Task 0 对象 | 只保留历史 evidence，不能证明当前计划 |

Reasonix 只作为 clean-room 行为参考。禁止复制其源码、注释、测试文本、目录布局或引入运行时依赖；实现必须能从 v3 当前源码、本计划不变量和新写的 fail-first 测试独立推导。

### 1.1 本次五类 LSP 证据裁决

当前对象已通过 `grep/structure -> inspect -> xref -> file(read_file) -> file(diagnostics)` 核对以下 owner：

| 行为 | 当前真实 owner | 裁决 |
| --- | --- | --- |
| agent runtime 事件投影 | `internal/platform/eventsurface/bind.go` 的 `bindAgentLifecycle`；payload 在 `internal/platform/eventsurface/bind_payloads.go` | 删除错误落点 `internal/ui/eventsurface/agent_runtime.go` |
| `turn/start` 后端入口 | `internal/module/turn/rpc_helpers.go` 的 `turnStartHandler`，调用 `Service.PrepareTurn` 与 `Service.StartTurn`；实现位于 `internal/module/turn/service.go` | 删除错误落点 `internal/app/turn_rpc.go` |
| turn manifest MCP binary | `internal/module/turn/manifest.go` 的 `mcpServerConfigBinary` | `dto.MCPBinary` 有 `TrustedServerID`，但当前 HTTP/stdio 构造均未赋值，属于 MCP lane 的真实安全缺口 |

上述精确 P0 owner 文件本轮 diagnostics 均为零。旧 evidence 点名的 `internal/module/uistate/overlay_test.go` 已由并发外部修改清除两条 `unusedwrite`，当前工作树 diagnostics 为零，但该文件不在本 agent 写集且尚未进入 immutable Git object；它也因 provider-wide readiness 已移入 follow-up 而不再作为任一 P0 lane 的 owner blocker。详细记录见 `docs/plans/evidence/reasonix-production-hardening-next/02-current-main-recheck.md`。

## 2. P0 范围

### 2.1 Release lane：transaction/recovery

P0 只实现以下闭环：

1. **Durable transaction**：更新在替换前写入 transaction identity、旧/新 release identity、状态与 backup；所有状态转换可恢复、可重放且 fail-fast。
2. **Probation supervisor**：替换成功不删除 backup；新版本只有在 exact release/transaction/process health ACK 与观察期完成后才能提交。
3. **Exact rollback**：candidate crash、ACK timeout、候选 release 完整性失败或 supervisor 中断时，由 updater/Guard 对同一 transaction 有界接管、恢复旧 release 并重启。
4. **Early selector**：`agent-terminal` 在 normal-only environment、frontend/provider/Fx preflight 前读取 transaction 状态；active probation 失败走 rollback，无 active probation 的 normal 启动失败走独立 Recovery。
5. **Recovery-only graph**：Safe Mode 不装配 normal provider、store、toolbridge、skill 或 update install；只暴露恢复状态、检查、重试和显式历史恢复。
6. **Minimal update trust**：packaged production 的 update source、manifest key 与 package signer policy 来自 package-owned trust；环境变量或裸 CLI 参数不能降级；pending trust generation 只在 healthy 后提交，rollback 丢弃 pending。
7. **Changed-field guards**：实现实际新增或修改的 transaction、trust、ACK、projection producer 各自在所属包建立动态字段守卫。

P0 不要求建立完整软件供应链产品模型。最终 payload/release/helper 的 exact digest 与 signer 仍要进入 transaction 和验收证据，但不扩张成全仓 component graph。

### 2.2 MCP lane：Codex schema compile/quarantine

P0 只实现以下闭环：

1. HTTP/stdio `ListTools` 在 transport 层只校验 JSON-RPC envelope 与 tools array，逐项保留 `json.RawMessage`，不因一个工具 schema 坏掉而提前整表反序列化失败。
2. 在 tool identity 通过后统一 canonicalize/compile input schema，拒绝 external reference、非 object root、错误 keyword 类型和超预算输入，输出稳定 digest 与 typed diagnostic。
3. managed/first-party server 任一 schema 失败时整批 fail-fast；只有 config-owner 证明为 trusted external 的 server 才允许逐工具 quarantine。
4. quarantine 后的坏工具不进入 Codex dynamic tool surface、catalog、proxy 或 call validator；同级好工具保持可用；修复后恢复，再次损坏后撤下。
5. authority/current-CAS 是最小安全前置：config owner 签发的 current server identity、generation/digest/membership 必须在 surface publish 与 call 前复核；Name/URL/raw `trustedServerId` 不能自行提升 authority。
6. 修复 start/resume/turn 配置链：trusted server reference 必须从 config owner 一路传到 `mcpServerConfigBinary` 并赋给 `dto.MCPBinary.TrustedServerID`；当前缺失必须由动态 changed-field guard 和 parity test 锁定。
7. schema compiler 的隔离方式保持 decision gate：只有所选库能证明 cancellation 与 hard resource bound 时才可进程内执行；否则必须采用有界进程隔离并阻断 MCP lane。证据不足时不得预先指定或新增某个 helper binary。
8. P0 quarantine 只承诺 Codex 通过 v3-owned toolbridge 的路径；Claude raw `--mcp-config` 不在本 lane 的兼容声明中。
9. Task 4B 的 current/quarantine 采用 config-owner 对齐的进程内最小状态，不声明跨进程 durable：进程重启同时清空 surface 与 owner state，新 owner 拒绝旧 generation；只有重新 fetch、compile 并通过 exact current-CAS publish 后才恢复 surface/call，因此重启路径 fail-closed。

### 2.3 安全保留项

瘦身不得删除三项最小安全约束：

- Release：package-owned update trust、production bypass fail-fast、transaction-bound trust activation。
- MCP：config-owner authority + exact current-CAS，禁止 raw DTO/name/URL 自造信任。
- 两条 lane：每个实际 changed producer 的动态字段守卫、missing/stale/unknown fail-fast 与真实 mutation RED。

## 3. 明确 follow-up tranche

以下工作**没有取消**，但不是本轮 P0 的开放条件，不得重新混入 Task 0 或 Task 1-4：

| Follow-up | 范围 | 重新进入条件 |
| --- | --- | --- |
| F1 Release composition/IP | 完整 `ProductIPBoundary`、ObservedArtifactInventory、SBOM/component graph、release attribution/digest graph、README/NOTICE/About 多语言投影 | 独立产品与供应链目标、owner ADR、最终产物 scanner 设计和专门门禁 |
| F2 Provider executable hardening | provider-wide executable resolve/prepare identity、compatibility matrix、ingress authority/protocol gate、failure attribution | 独立 provider security review object；不得借 release transaction 顺带展开 |
| F3 Claude readiness | Claude provider-wide MCP readiness/status、start/restart/resume generation、frontend/backend send gate和真实 CLI E2E | 明确 Claude 产品承诺与真实 CLI fixture；Codex quarantine 结果不得外推 |
| F4 Generic RPC/admission | `AuthenticatedRPCConnection`、`RPCSessionPrincipal`、workspace token、通用 single-use admission/effect reconciliation | 独立 RPC threat model、transport owner ADR 与跨入口测试 |
| F5 原计划 P1/P2/P3 | 统一只读能力诊断、语义进度租约、Subagent Profile、插件兼容、writer 回执、选中文本 | 对应 P0 合并后各自新建 review object 和计划 |

F3 若新增 provider MCP 状态投影，真实事件投影 owner 是 `internal/platform/eventsurface/bind_payloads.go` + `bind.go`；若新增 backend send gate，入口是 `internal/module/turn/rpc_helpers.go:turnStartHandler`，随后才进入 `service.go:StartTurn`。这些正确路径只为 follow-up 定位，不代表本轮实现这些扩张。

## 4. P0 landing surface

### 4.1 Release lane

| Path | P0 职责 |
| --- | --- |
| `internal/module/appupdate/recovery/` | transaction、attempt、trust generation、ACK、journal、锁、rollback/commit 状态机 |
| `internal/module/appupdate/{service.go,manifest.go,github_release.go,rpc.go}` | package-owned update source/trust 输入、transaction 启动、typed status/RPC |
| `cmd/super-dolphin-updater/` | 保留 backup、监督 probation、durable failure、healthy commit 或 rollback |
| `cmd/super-dolphin-guard/` | detached stale-transaction check/restore/status；不加载 desktop graph |
| `cmd/agent-terminal/main.go`、`internal/app/recovery_*.go` | normal preflight 前 selector、独立 Recovery composition、retry |
| `internal/platform/runtimeenv/` | normal child environment plan；Recovery/Guard 使用 frozen allowlist |
| `frontend-app/src/features/update-recovery/` 与最小 runtime bootstrap | typed Recovery/Safe Mode 状态和动作禁用；不得复用 normal ready |
| package/verify scripts 与对应 tests | transaction protocol、helper/release parity、平台 capability fail-closed |

### 4.2 MCP lane

| Path | P0 职责 |
| --- | --- |
| `internal/platform/toolbridge/{http_mcp_client.go,stdio_mcp_client.go,types.go}` | envelope decode 与逐项 raw tool 保留 |
| `internal/platform/toolbridge/schema/` | canonicalize、budget、one-shot helper client、diagnostic、quarantine plan |
| `cmd/mcp-schema-compiler-helper/` | 单请求 compile/validate；严格 stdin/stdout 协议；无网络、文件、cache 或后台任务 |
| `internal/platform/toolbridge/{handler.go,proxy.go,module.go,schema_quarantine.go}` | class policy、surface/filter/call 一致性、current-CAS 和 quarantine 应用 |
| `internal/module/mcp_server/` | config-owner generation/current membership、owner-aligned 进程内 quarantine/current-CAS；重启时 surface/state 同时清空并重新 fetch/compile |
| `internal/contract/mcp_control.go` 或预算允许的窄子包 | current authority read/CAS port；不扩张通用 RPC principal/admission |
| `internal/module/thread/{mcp_server_config.go,start_session_helpers.go}` | config-owner trusted reference 的 start/resume 生产链 |
| `internal/module/turn/{service_helpers.go,manifest.go}` | turn 链与 `mcpServerConfigBinary` 的 `TrustedServerID` 透传 |
| `internal/dto/provider/manifest.go`、`internal/provider/shared/config_helpers.go` | existing `TrustedServerID` DTO 字段及 provider conversion 校验 |
| `go.mod`、`go.sum`、NOTICE/SBOM 的现有依赖登记 owner | 仅在 compiler 选型通过 decision gate 后变更 |

共享 seam 只允许 Integrator 串行修改；任一 lane 不得顺带实现 §3 follow-up。

## 5. 执行拓扑

### Task 0：双 lane 设计与证据冻结

- 记录 current HEAD/worktree、五类 LSP 证据、diagnostics 与 immutable review bytes。
- 分别维护 Release blocker ledger 与 MCP blocker ledger；finding 只阻断所属 lane，确属共享安全 seam 时才同时登记。
- 冻结每个 lane 的 exact landing package、具名 fail-first test、命令、稳定失败语义和零副作用断言。
- 不手写未来所有字段，不建立或声称全仓字段 scanner。
- 主 Agent 对返修后的设计、owner 与证据做初审；Release 与 MCP 设计 blocker 均已清零。

所有实现任务统一采用：实现 Agent 在独立 task branch/worktree 完成 -> 主 Agent 用 LSP、diff 和匹配门禁初审 -> 通过后立即合并 `codex/integration-reasonix-p0`。逐任务不再另派 reviewer Agent。Task 1-4 全部完成并完成 Task 5 收口后，才启动三条全新、无继承上下文的总审 lane；总审发现必须返修并重新执行受影响门禁。

### Task 1：Release transaction core

- 先写 transaction crash matrix、backup retention、trust pending/commit/rollback RED。
- 实现 durable state transitions、same-volume backup/staging、fsync/lock 与 exact transaction identity。
- 通过后只更新 Release blocker ledger。

### Task 2：Supervisor、Guard 与 Recovery

- 先写 candidate crash/ACK timeout、supervisor takeover、first normal preflight failure 和 Recovery graph allowlist RED。
- 实现 probation supervisor、detached Guard、early selector 与 Recovery-only graph。
- 不引入 provider/toolbridge/SBOM component graph。

### Task 3：Update trust 与产物级恢复 E2E

- 先写 production env/CLI bypass、wrong key/signer、pending trust rollback、mixed helper/release RED。
- 实现最小 package-owned source/key/signer policy和平台 capability矩阵。
- 用独立旧/新 artifact 验证 install -> crash -> rollback -> reopen 与 healthy commit。

Task 1-3 的唯一开门条件是 `p0_release_executable=true`。

### Task 4：Codex MCP schema compile/quarantine

- 按 `03-mcp-compiler-decision.md` 实现 one-shot local helper；禁止改回进程内 compiler、常驻 worker 或 cache。
- 先写 mixed good/bad tools、managed fail-fast、trusted external quarantine、authority stale、`TrustedServerID` 丢失、修复/再损坏和资源上限 RED。
- 实现 transport raw decode -> identity/class -> compile -> quarantine/current-CAS -> surface/call 的唯一顺序。
- 只声明 Codex toolbridge scope。

Task 4 的唯一开门条件是 `p0_mcp_executable=true`。

### Task 5：联合收口

- 两条 lane 各自可独立完成和复核；联合 P0 DoD 才要求两条 lane 都完成。
- Integrator 串行处理 shared seams、生成物与最终 hook gate plan。
- lane PASS 不是 repo PASS；每条 lane 独立记录 D01-D19 coverage、测试、门禁和残余风险。
- 全部任务合入 integration 后，由三个全新 Agent 分别执行 Release、MCP/security、repo-wide integration 总审；三者均达到 `0 P0 / 0 P1` 才允许宣告 P0 完成。

## 6. 动态 changed-field guard

Task 0 不再冻结几十个尚未存在的未来 struct，也不建立 `p0_field_chain_registry` 作为全仓 scanner。字段守卫在每个实际实现任务中按实际变更 producer 动态生成：

1. 以本次新增/修改的 struct、schema、SQL row、registry 或 DTO 为 producer truth。
2. 用 reflection、AST、schema parser、SQLC metadata 或类型系统动态枚举 producer 字段；禁止测试手写字段名数组冒充真值。
3. 显式登记 mapper 与 terminal consumers，检查 `missing`、`stale`、unknown 和无效 exemption。
4. 至少执行一个真实 producer/mapper/terminal mutation RED；错误必须指出 producer、field 与 chain ID。
5. 每个 exemption 包含 `Field | Direction | Reason | Evidence/Owner`；空原因或未来补齐无效。
6. 任一 producer guard 只证明该 producer 的已发现边界，禁止写成“全仓字段已扫描”。

本轮最低必有 guard：Release lane 实际新增的 transaction/trust/ACK/projection producer；MCP lane 实际新增的 compiled schema/quarantine/authority producer；以及 `MCPServerConfig/config-owner reference -> mcpServerConfigBinary -> dto.MCPBinary.TrustedServerID` 链。

## 7. Lane gate 与完成条件

### 7.1 `p0_release_executable=true`

仅在以下条件同时成立后设置：

- Release exact owner、landing package、RED/GREEN tests、命令和预期失败已冻结。
- Release 相关 LSP 五类证据与 diagnostics 无 blocker。
- 主 Agent 已完成计划合理性、owner、影响面与 diagnostics 初审并清零 blocker。

### 7.2 `p0_mcp_executable=true`

仅在以下条件同时成立后设置：

- MCP exact owner、landing package、RED/GREEN tests、命令和预期失败已冻结。
- MCP 相关 LSP 五类证据与 diagnostics 无 blocker。
- 已冻结可验证的 one-shot helper 进程 contract、kill/reap、输入输出预算、并发、deadline、六目标构建、generation/digest fence 与稳定错误码。
- compiler decision evidence 已在本 review object 完成，blocker ledger 为 none。

### 7.3 P0 Definition of Done

Release lane 必须证明：backup 在 probation 期间存在；candidate crash/timeout/中断可自动恢复 exact old release；healthy 才提交 trust 与删除 backup；Recovery graph 不加载 normal 高风险依赖；production trust 不能被 env/CLI 降级。

MCP lane 必须证明：mixed good/bad external tools 只隔离坏项；managed server 仍 fail-fast；compiled digest 在 catalog/provider/proxy/call 一致；stale authority 或丢失 `TrustedServerID` 时零 surface/零 client call；compiler 超预算/取消不泄漏后台工作；声明严格限于 Codex toolbridge。

最终完成还要求 integration exact commit 接受三条全新、无继承上下文的总审 lane，且每条均为 `0 P0 / 0 P1`；这是一轮总审，不是逐任务重复审查。

## 8. 当前残余 blocker

| Lane | Blocker | 当前状态 |
| --- | --- | --- |
| Release | none | `CLEARED` |
| MCP | none；one-shot local helper decision 已冻结 | `CLEARED` |

Release blocker 已清零并开放 Task 1-3；MCP compiler decision blocker 已清零并开放 Task 4。每个实现 Agent 完成后由主 Agent 初审，初审通过即合并 integration；最终三 Agent 总审只绑定全部实现完成后的 integration exact commit。

历史基线、Design Freeze 和当前主线重检分别记录在：

- `docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md`
- `docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md`
- `docs/plans/evidence/reasonix-production-hardening-next/02-current-main-recheck.md`
- `docs/plans/evidence/reasonix-production-hardening-next/03-mcp-compiler-decision.md`
