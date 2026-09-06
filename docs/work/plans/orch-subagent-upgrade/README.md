# Orch 子 agent 升级：Gate S0 工作包

Status: Active

创建日期：2026-09-01。

用户于 2026-09-01 批准按“先 S0、再可靠命令/身份/事件、随后消息等待与跨任务授权、Room、验收”的顺序执行。
这批准的是推进工作，不把尚未写出的精确子规格、历史 blocker、运行证据或发布资格标成已获批准。

## 范围与事实源

- 继续使用隔离工作树 `.worktrees/20260831-orch-subagent-upgrade`，不自动提交、合并或推送。
- [原始总规格](../../../superpowers/specs/2026-07-11-unified-native-subagent-control-plane-design.md)给出待满足的整体要求；源码/同包测试和 Accepted 契约仍是当前运行行为的事实来源。
- [独立单实例执行计划](../2026-08-31-orch-standalone-upgrade.md)继续追踪 ST-01～ST-18，不因本工作包结构通过而关闭。
- 本目录是可执行的规划清单，不是运行时 policy owner，也不是另一套 agent 调度器、数据库或发布许可签发器。
- `release-gate-dag.json` 是本工作包依赖关系的单一可写清单；子规格顶部依赖是必须同步的设计约束，派生图只由生成器写入。
- `current-blocker-registry.json` 登记总规格 B01～B18 的历史发现、关闭要求和负责规格。初始状态全部是 `needs_revalidation`，不是“当前代码仍然存在十八个已复现漏洞”的断言。
- 编号与规格文件集合由总规格的明确表/清单动态枚举核对；漏项、重复、未知引用、循环、漏依赖、类型或生成物漂移均失败。

## 十四份独立子规格

| Spec ID | 文档 | 状态 |
| --- | --- | --- |
| TRUST | [输出信任与提示词边界](../../proposals/orch-subagent-s0/agent-output-trust-and-prompt-boundary-spec.md) | Draft / 待内容评审 |
| ID | [任务身份与事件存储](../../proposals/orch-subagent-s0/agent-identity-and-event-store-spec.md) | Draft / 待内容评审 |
| CONTENT | [内容生命周期与加密擦除](../../proposals/orch-subagent-s0/content-lifecycle-and-crypto-erasure-spec.md) | Draft；CONTENT-1方向已批准、本地候选已实现 |
| RESOURCE | [持久资源准入与预算](../../proposals/orch-subagent-s0/durable-resource-admission-and-budget-spec.md) | Draft；RESOURCE-1方向已批准、本地reserved候选已实现 |
| OUT | [持久命令与事务发件箱](../../proposals/orch-subagent-s0/durable-native-command-outbox-spec.md) | Draft / 待内容评审 |
| PROBE | [官方 CLI 能力探测](../../proposals/orch-subagent-s0/provider-conformance-probe-spec.md) | Draft / 待内容评审 |
| CTL | [控制通道与原生任务 RPC](../../proposals/orch-subagent-s0/mcp-control-channel-and-native-agent-rpc-spec.md) | Draft / 待内容评审 |
| BRIDGE | [各提供方 CLI 原生桥接](../../proposals/orch-subagent-s0/provider-cli-native-bridge-profiles-spec.md) | Draft / 待内容评审 |
| TOOLS | [版本化工具面迁移](../../proposals/orch-subagent-s0/versioned-tool-surface-rollout-spec.md) | Draft / 待内容评审 |
| ROOM | [协作房间授权与流控](../../proposals/orch-subagent-s0/agent-room-authz-and-flow-control-spec.md) | Draft / 待内容评审 |
| UI | [任务与事件 UI 投影](../../proposals/orch-subagent-s0/agent-ui-projection-spec.md) | Draft / 待内容评审 |
| A2A | [跨实例 A2A 网关](../../proposals/orch-subagent-s0/a2a-gateway-spec.md) | Draft / 待内容评审 |
| VERIFY | [机器验证与发布证据](../../proposals/orch-subagent-s0/machine-verification-and-release-evidence-spec.md) | Draft / 待内容评审 |
| ROLLOUT | [跨进程升级与回滚](../../proposals/orch-subagent-s0/cross-process-rollout-and-rollback-spec.md) | Draft / 待内容评审 |

所有子规格均为内容评审草案。复杂字段的最终版本化 schema、数据库迁移和外部协议修订必须按各份草案的未决项裁决；不得从概览段落直接发布 strict `unified_v2`。

### S0 spec completeness checklist (planning-only)

每个被 Gate S0 枚举的 spec 文档必须在 `Spec-ID`/`Status`/`Depends-On` 之后（且不在 fenced code block 中）提供且仅提供一组以下摘要字段：`S0-Owner`、`S0-Scope`、`S0-Dependencies`、`S0-Inputs`、`S0-Outputs`、`S0-Failure`、`S0-Recovery`、`S0-Field-Chain`、`S0-Named-Acceptance`。这些字段只登记文档的 owner、边界、契约和可复查验收名称；它们不是内容批准、证据签名、release decision、capability lease 或 runtime authorization。`S0-Dependencies` 必须与 `Depends-On` 及 DAG 普通入边一致；`S0-Named-Acceptance` 必须列出具名 `Test...`、`RED-*`、`GREEN-*` 或当前 Spec-ID 的 `P/N` 编号，并且每个 token 都要在正文再次出现。离线 validator 逐文件 fail-fast 检查缺失、重复、未知字段、空值、占位值、错误前缀、不可追踪 token 和依赖漂移。

`acceptance-inventory.json` 是 S0 token 到具体规划 fixture/guard 路径的机器索引；`scripts/orch_acceptance_inventory.py` 严格校验 51 个 token 与 B01–B18 blocker 的路径、符号、行号和状态。它保持 `planning_only`、`NO-GO`、`planned`/`unverified` 语义，任何索引命中都不能被解释为测试通过、GREEN attestation 或发布授权。

`spec-execution-index.json` 是十四份子规格的可执行复核索引。每项固定 `owner`、直接 `prerequisite_ids`、传递 `ancestor_gate_ids`、`entry_criteria`、`exit_criteria`、可运行的规划检查 `commands`、`evidence_classification` 和 `blocker_ids`；`blocker_ids: []` 明确表示当前没有直接历史 owner，不表示该规格已无阻断。该索引只允许 `planning_only`/`NO-GO` 语义，命令结果必须按 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED` 解释。

`current-blocker-registry.json.evidence_slots` 为每个 B01–B18 提供独立 `reproduction_ref`/`reproduction_anchor` 与 `closure_ref`/`closure_anchor` 槽位，状态固定为 `unverified`。槽位是后续执行器写入具体复现/关闭证据的地址，不是当前复现成功、修复完成或 GO 的声明；registry 的 `status` 仍必须保持 `needs_revalidation`。

### Cross-task requirements registry (planning-only)

`release-gate-dag.json` 的 `cross_task_requirements` 是跨任务能力的机器可检查规划集合，共八项：direct-peer message delivery union、strict discover/read/send grant separation、message/reply association、Room 不扩权、cursor resume、idempotency、unknown outcome 和 unauthorized zero-side-effect。每项都绑定 `owner_spec_ids`、设计依赖 `depends_on`、`ancestor_gates` 以及独立的 `red_evidence_refs`、`green_evidence_refs`、`guard_evidence_refs`；状态只能是 `pending` 或 `needs_revalidation`。

八项能力的统一 ancestor gate 集合为 `VERIFY_S`、`ROLLOUT_S`、`DOMAIN_DONE`、`VERIFY_DONE`、`PRECANARY_DONE`；能力级 GREEN 规划不得脱离升级兼容性与混合版本回滚证明。

其中 `message_reply_association` 由 ID、CONTENT、OUT、TOOLS、UI 共同负责，覆盖 delivery union、authenticated source 与 target execution fence、每条 reply 独立的幂等 command/receipt 链，以及禁止裸 `correlation_id`/`reply_to` 作为关联事实。该条目仍是 planning-only 约束，不冻结 runtime wire 字段或启用资格。

该集合只描述后续验收的 owner/祖先/证据定位，不注册工具、不签发 grant、不接 runtime。`canonical_surface` 只允许 `unified_v2` 或 `room`；legacy `send_message` 保留为兼容的 operator 管理路径，禁止作为 canonical cross-task send。所有八项与十四份子规格继续保持 Draft，跨任务 send、Room 和发布资格继续 NO-GO。生成的 `dependency-map.generated.md` 会列出条目、依赖、祖先 gate、三类 planned refs 及计数；引用即使指向现有测试或守卫，也只能按 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED` 解读。

### RecoveryCustody 外部单实例 / SQLite 专项 gate（planning-only）

`[RecoveryCustody 专项 gate 矩阵](recovery-custody-gate.md)`是对已冻结“外部单实例服务独占私有 SQLite、PostgreSQL 排除”方向的逐项输入/输出/失败/恢复与 RED/GREEN/Guard 规划。只读审计确认现有 `release-gate-dag.json` 没有等价 singleton、RecoveryCustody 或 SQLite authority gate；为避免破坏总规格 Mermaid 与离线 validator 的 exact 集合，本矩阵不新增 DAG node、edge、cross-task capability 或第三份 machine JSON。它从属于 `VERIFY_S`、`ROLLOUT_S`、`VERIFY_DONE`、`PRECANARY_DONE` 祖先门，六项检查及专项总决策均保持 `pending`/`needs_revalidation` 与 `NO-GO`；矩阵引用的本机测试和守卫不构成发布证据。

## 机器检查与派生物

在项目根执行：

```text
make orch-upgrade-check
make orch-upgrade-render
```

- `orch-upgrade-check` 运行离线单元测试与只读一致性检查；不读取个人 provider home，不发模型调用，也不运行远程负载。
- `orch-upgrade-render` 只重生成本目录的 `dependency-map.generated.md`；不能回写或改变输入状态。
- 派生物列出依赖图、blocker 关闭要求和由输入计算的计数/digest。digest 仅为确定性 JSON 的 SHA-256，不是 JCS、数字签名或授权证明。
- 通过只表示规划输入结构一致，不能替代业务测试、字段实现闭环、子规格批准、blocker 修复、安装验收或 GA。
- 当前 schema 刻意只允许 spec=`draft`、gate=`pending`、blocker=`needs_revalidation`；不提供“把 JSON 改成 closed 即获准发布”的开关。
- 下一阶段需要单独实现内容批准与精确 revision/digest 绑定、当前 blocker revalidation、RED/GREEN/guard receipt、适用祖先闭合和实际验收裁决模型；在此之前不签发任何运行许可。
- 每个 blocker 必须提供非空且唯一的 `red_evidence_refs`、`green_evidence_refs`、`guard_evidence_refs`；它们只是 workspace 内已存在的 planned/observed 文档引用，供后续证据运行器定位，不是测试通过、签名 attestation 或发布授权。
- 当前引用即使指向验证记录或 guard 源码，也必须按 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED` 解读；缺失、越界、重复或把引用当作 receipt 的行为由离线校验拒绝或保持未验证。
- 引用先解析为 workspace 内 canonical path，再在各字段内去重；`path`、`./path` 和内部 symlink alias 不能通过计数膨胀，跨 category 或跨 blocker 共享同一 guard 仍允许。registry 与生成的 dependency map 禁止作为自身证据。
- 派生图额外记录 canonical in-root document/evidence snapshot digest；source、spec/README 文档或引用文件内容漂移会令只读 `--check` 失败，registry 与生成物本身排除在该 digest 外以避免循环。

## Gate 依赖合同

下列行用于动态对照 JSON 的 gate 输入边。它们描述整体 umbrella 路径，不代表每个局部 standalone 开发步骤都依赖远程 A2A。
按 provider 独立启用的 join 是 `BRIDGE -> TOOLS`，仅对明确的 `codex/claude/agy/grok` 分支生效；不能用一家成功替代其他家。

Gate-Dependencies: VERIFY_S <- VERIFY

Gate-Dependencies: ROLLOUT_S <- ROLLOUT

Gate-Dependencies: DOMAIN_DONE <- BRIDGE, UI, A2A

Gate-Dependencies: VERIFY_DONE <- DOMAIN_DONE, VERIFY

Gate-Dependencies: PRECANARY_DONE <- DOMAIN_DONE, ROLLOUT

Gate-Dependencies: CANARY_5_LEASE <- VERIFY_DONE, PRECANARY_DONE

Gate-Dependencies: CANARY_5_ATTEST <- CANARY_5_LEASE

Gate-Dependencies: CANARY_25_LEASE <- CANARY_5_ATTEST

Gate-Dependencies: CANARY_25_ATTEST <- CANARY_25_LEASE

Gate-Dependencies: CANARY_100_LEASE <- CANARY_25_ATTEST

Gate-Dependencies: CANARY_100_ATTEST <- CANARY_100_LEASE

Gate-Dependencies: FINAL_DECISION <- CANARY_100_ATTEST

Gate-Dependencies: FINAL_COMMIT <- FINAL_DECISION

Gate-Dependencies: ENABLE <- FINAL_COMMIT

## 本轮工作包退出条件

1. 十四份草案都必须有机器可检查的 S0 摘要（owner、范围、依赖、输入输出、失败/恢复、字段消费链和具名验收），并在正文中给出可审查的字段与故障合同；摘要/正文均不等于内容批准，未决项必须可见。
2. 两份 JSON 可严格加载，动态集合与原始总规格一致，派生图/表可重复生成且只读检查不漂移。
3. 负测覆盖删掉真实字段/依赖/编号、duplicate JSON key、unknown/missing/type/null、cycle、provider join、路径逃逸和文档错配。
4. 校验器本身通过后仍记录“内容待评审、运行时未启用”，不能将本工作包退出条件当整份升级完成条件。
5. 后续最先推进 OUT/ID 的适用祖先：先完成 TRUST 与 VERIFY_S/ROLLOUT_S 精确骨架评审，再进行可靠命令、资源、内容和身份事件的独立实现批次；migration 130 的 AccessGrant/EventWaitHandle 已进入 standalone/legacy Fx 构造图，但仍只是 ID-1/ID-2 之后的内部候选，尚未发布或启用 wait。

## 验证记录

实际执行记录与残余边界见[验证记录](verification-notes.md)、[B01–B18 blocker revalidation](blocker-revalidation-2026-09-02.md)和[本轮候选补强复核](candidate-revalidation-2026-09-02.md)。下一批 ST-11 的当前源码风险、唯一 command owner 边界与具名 RED 目标见[可靠命令实现切片](command-reliability-next-slice.md)。

> 当前状态锚点（2026-09-04）：[current snapshot](current-snapshot-2026-09-04.md)。本快照优先说明本次复核的命令结果、候选实现、candidate-only/NO-GO 边界和未提交/未推送状态；本文件及 `verification-notes.md` 中更早的耗时仅作历史观测。

## 当前执行检查点（2026-09-04）

- migration 136 wait-operation head/target journal 已完成 strict candidate schema；Plan/Get/AppendTarget 与 `ProvisionTarget` 均保持内部、未注册、未接生产 Fx/provider。
- AccessWait transaction-bound create/read/cleanup seam 已通过 ordinary/race/vet；ProvisionTarget 在同一 `BEGIN IMMEDIATE` 内完成 grant+wait+journal，支持并发单赢家、deadline fail-fast、rollback 和 SQLite reopen replay。
- wait-operation `AdvanceState`、`ResumeTarget`、`CleanupTarget` 已作为未注册 candidate 补齐；状态聚合、未知 read-back、cleanup revision 连续性和 SQLite reopen replay 有 targeted ordinary/race/vet/code-size/LSP 证据。`CommandAttemptGateway.Execute` 亦只提供 typed-provider candidate，未接真实 provider/Fx/dispatcher。
- wait-operation marker 现在有成功 COMMIT 后的一次性 owner-level `ErrCommitUnknown` 注入回归，以及 read-side phase/identity/resource continuity 守卫；这两项仍是 candidate 证据，不替代驱动层 acknowledgement、跨进程 receipt 或 production wiring。
- EventStore post-commit watcher、expiry candidate/runner、waitaggregate clock/panic/result guards、terminal retry/rehydrate/outbox identity 和 cross-generator `agent_*` uniqueness 均为本地候选证据。
- EventStore `RetentionPinVerifier`/`retentioncandidate.Verifier` 与 AccessWait `CreateWaitTx`
  source-watermark admission 已补齐 transaction-bound candidate；pending wait/target pin、floor
  equality、overflow/malformed fail-closed 和 stale-source zero-effect 回归通过，仍未接生产 Fx。
- socket-enabled `GOWORK=off go test ./cmd/mcp-orch/... -count=1 -p=1` 与完整 race 均已通过；项目地图、codemap 和 Gate S0 派生物已刷新并通过 check。wait-operation 与 retention candidate 文件已无本包尺寸/注释发现，full light guard 当前仍有 31 项其他候选棘轮项；SQLC exact verify 仍受 generated tracking/宿主 GitRunner 边界阻断。
- 生产 OUT/receipt/custody、真实 provider→EventStore caller、跨进程 commit-unknown recovery、Resume/Cleanup aggregate owner、`wait_agents`/Room/SSE/public unified_v2 继续 NO-GO；当前 Resume/Cleanup 仅是内部 candidate。
- wait-operation 的依赖与下一阶段边界见[Wait Operation 下一切片](wait-operation-next-slice.md)；该文档只描述 candidate seam，不构成 public wait 或 release 授权。

## 当前执行检查点（2026-09-03）

- 已落地的本地候选包括：S0机器规划骨架、命令意图/生命周期候选owner、migration131 `send_message_v1`完整principal/authorization/双端binding profile（仍是non-authorizing draft）、ID-1 reservation/incarnation/current binding、ID-2 canonical dense EventStore、migration130 `AccessGrant/EventWaitHandle`、内部多目标等待收口器、[task attribution/current task principal/canonical task event source](task-principal-and-event-source.md)、direct-peer accepted replay的fail-closed保护、migration132 [CONTENT-1候选](content-1-encrypted-single.md)、migration133 [RESOURCE-1 reserved候选](resource-1-reserved-skeleton.md)、[B08 command/attempt候选](B08-wakeup-command-candidate.md)、[B09严格唤醒载荷与 migration134 边界](candidate-revalidation-2026-09-02.md)、[B09历史载荷迁移规划器](B09-legacy-wakeup-migration-candidate.md)、[B10 writer-epoch候选](../../proposals/orch-subagent-s0/b10-taskdag-writer-epoch-candidate.md)、[B10 M01–M42 writer inventory](../../../../cmd/mcp-orch/store/taskdag/writer_inventory.json)、[B12 security-time候选](security-time-owner-candidate.md)和[RecoveryCustody SQLite authority/transport candidates](recovery-custody-sqlite-authority.md)，以及独立服务 CLI 的构造/生命周期候选与 durable reconcile coordinator 候选。
- 已补齐的等待边界包括未知 source、cursor watermark、同事务 identity fence、事务身份读者强制、CAS 竞争重放和统一 owner clock；production standalone HTTP 已由必填配置建立 local operator issuer，并按每个 initialize 签发 CallerPrincipal session 快照。
- 这些组件仍未获得public tool/manifest启用资格。task-only HTTP认证与内存credential owner候选已存在；唯一 identity.Store→taskprincipal resolver 与 eventstore.Store→taskeventsource producer 窄口现已进入 legacy/standalone Fx 构造图，但不自动 append 事件，也不替代第二 task listener、child issuer、managed CLI credential注入或 identity pending→active 生命周期。`unified_v2` list/wait/send、childhandoff 与 Room 仍未注册；managedlaunch→managedruntime 只有显式投影校验候选。production CONTENT key/provider adapters、RESOURCE claim/permit/usage/release、OUT command/receipt/reconcile、跨daemon recovery、Room event bridge和远程/发布证据仍是Active计划的阻断项。
- 本轮已修复并补回归的边界包括：reservation/incarnation 独立代际、EventStore canonical source、AccessWait selector source、RecoveryCustody 幂等冲突分类、receipt proof digest v2、child handoff 私有目录/安全清理、RecoveryCustody 配置有界私有读取、managedlaunch prepared-request 绑定、unified_v2 严格 schema 与 SQL 显式列。它们只提高候选的 fail-closed 质量，不改变生产启用门。
- 本轮还形成了 B08 command/attempt、B09 唤醒判别联合/入队事务/dispatcher 前置校验与 migration134 version/claim/quarantine 边界、B10 writer-epoch 与 B12 security-time 隔离候选；历史 legacy payload 仍只有操作员审核 planner，没有生产 applier/receipt，RecoveryCustody 外部服务只接受显式 private-only SQLite 模式。上述候选均不关闭对应 blocker，也不改变生产启用门。
- 2026-09-03 继续执行补齐了四个 fail-closed 边界：managedlaunch COMMIT/ABORT projection 逐字绑定 prepared-request digest；`writer_inventory.json` 与 archtest 双向冻结 taskdag M01–M42；外部 RecoveryCustody 在 canonical authority path 上拒绝已知网络/未知文件系统并统一后续 open/bootstrap 路径；独立服务配置递归拒绝嵌套重复 JSON key。上述均为本地候选/守卫，不改变 B01–B18 `needs_revalidation`、managed provider/Room/release NO-GO 或总计划 Active。
- 额度恢复后又补齐了运行时 fail-closed 边界：CLI Wait/reader 异常退出通知带真实 provider session 与 launch fence，并异步进入 exactly-once lifecycle；managed Claude 不会回退旧 local command；legacy stdio/peer HTTP direct-peer 全面禁用直到有 server-owned attribution；legacy/standalone 各自只运行一个 direct-peer outbox projector；standalone lock 与产品 SQLite 使用同一 canonical state root；B09 target/payload identity 与 managedlaunch query receipt one-hot 继续由 Go/SQL 守卫。上述仍是本地候选和防护，不授予 production/release 资格。
- 随后继续收口了 task-only transport 与 identity/event 输入：已验证 attribution 的 `TaskID` 才会投影到内部 `ToolScope.AgentID`，operator profile 仍为空且客户端 metadata 被拒绝；`taskeventsource.Producer` 在 appender 前拒绝未知 reservation/incarnation schema；authority 与底层 SQLite `Open` 同时拒绝路径或叶子名首尾空格，防止 lock/DB 身份分裂。上述均为候选 fail-closed 修复，不注册 unified_v2/Room、不接 legacy cross-task send。
- 额度恢复后的继续批次还新增了未注册的 `orchestration/tasktransport` Binder candidate、task HTTP 多 session 删除隔离回归，以及 [RecoveryCustody 外部单实例 / SQLite 专项 gate](recovery-custody-gate.md)。这些只把服务端归因绑定、断线/删除隔离和 private-SQLite/unknown-receipt 的验收边界写成可复核候选；仍不接 runtime、不中止 NO-GO，也不把本机测试写成发布证据。
- 随后又把唯一 `orchestrationcommand` intent/journal owner 以永不放行的 receipt-verifier seam 接入 legacy/standalone canonical Fx 图，并增加 `taskeventsource.RuntimeEventCandidate`、`identity.GetActiveSnapshot` 和 taskruntime listener/credential 生命周期的候选收口。它们只提供 `recovery_custody_pending` 的 test-owned genesis、完整身份快照/水位映射和 listener race 防护；production verifier、identity/event 原子写链、child credential、provider effect 与 public tool 仍未接通，继续保持 NO-GO。
- [生产决策门](content-resource-custody-decision-gate.md)的CONTENT/RESOURCE方向已获批准；RecoveryCustody已冻结为“外部单实例服务独占私有SQLite DB”，PostgreSQL继续排除。当前已有数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、三层digest、SQLite完整性join、`PinnedDirectoryLock`+`KernelAuthorityLease`、typed finalization observation/同key `ReconcileResolution`、带 server-side `VerifiedClient` receipt gate 的独立服务 CLI 构造/生命周期候选与 durable reconcile coordinator，以及非MCP `rpcwire`+`authorityhttp`+`httpclient`候选；仍缺真实外部EvidenceFinalizer/ReceiptVerifier/trust root、production supervisor/Fx/OUT接线、发布配置、network FS证明、签名trust root和发布验收。
- 缺owner时的行为已由[pre-admission proof与custody合同](pre-admission-proof-and-custody-contract.md)机器化：pre-profile candidate ref不能直写，post-profile必须有两owner proof，local custody永远indeterminate/reject-all。CONTENT-1 store虽已实现，但其proof adapters/Fx/tool仍未装配，production seam继续fail-closed。

### 额度恢复后的新增候选（仍未注册）

- `orchestration/tasktransport` 现在同时包含 Binder 与 discover/read/send 授权 claim 候选；它们只消费服务端身份快照，所有拒绝返回 zero-effect 判定，不写 grant、cursor、EventStore、OUT 或 provider。
- `taskeventsource.RuntimeEventTxCandidate` 将 identity snapshot、source watermark 和 canonical append 收窄到同一 transaction-bound DBTX；它与旧 split-transaction candidate 都未进入 runtime caller/Fx。
- identity/event store 同时提供 additive Tx seam（`RequireTransactionDBTX`、`GetActiveSnapshotTx`、`GetSourceStateTx`、`AppendTx`、`WithTransaction`），仅供后续原子 caller 评审，不分配 sequence、不持有 custody、未接生产生命周期。
- `accesswait.Store` 现在兼容 unified wait 的 target-source selector（仅 active read grant + 完整 target fence），同时保留 caller/source fence 重验；这只消除内部 owner 语义冲突，public `wait_agents` 仍未注册。
- `orchestration/childissuer` 只校验已验证 parent 与 command-backed `managed_agent` child 链，拒绝 operator/native-adoption/共享 issuer/同 child 重放；它不签发 bearer 或认证 token。详见 [child issuer candidate](child-issuer-candidate.md)。`standalone/managedruntime/issuer` 明确是 endpoint/process `BindingCandidate`，不是 child issuer，详见 [managed-runtime binding candidate](managed-runtime-issuer-candidate.md)。
- `orchestration/trustcandidate` 提供 bounded trust envelope、CONTENT ticket、quoted/inert renderer 和 zero-effect sink decision；详见 [trust envelope candidate](trust-envelope-candidate.md)。`tools` registry 对显式 output schema 在构造期编译并包裹唯一生产 handler 链。二者都不接真实 trust root、CONTENT key/object、OUT、EventStore、provider 或 UI sink。
- process-exit terminal projection 现在保留 session generation 至 outbox ACK，exitmonitor 在事件未成功写入 channel 时释放 claim 供重试；channel enqueue 后崩溃的内存事件仍需后续 durable consumer/reconcile owner。
- RecoveryCustody 外部服务继续固定为独立单实例私有 SQLite（不是 PostgreSQL）：主库 inode 与 kernel lease 绑定，WAL/SHM 按合法 SQLite 生命周期只在锁内重绑；replacement/appearance race 均 fail-closed。
- RecoveryCustody 读路径现在也复核 WAL/SHM 对象身份，并拒绝 authority path 与固定 lock sentinel 同名或同 inode；独立 coordinator wiring 和 candidate binary packaging 仍为 test/opt-in、disabled，不改变 production composition。
- `standalone/managedruntime` 增加 `CandidateSupervisor` 内存状态机候选（严格 PREPARE/COMMIT/ABORT/QUERY replay 与 unknown fence），只消费外部已验证 receipt，不启动 helper/provider；详细边界见 [managed runtime binding candidate](managed-runtime-issuer-candidate.md)。

以上新增项均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 候选或局部防护，不改变 B01–B18
`needs_revalidation`、十四份 spec 的 `Draft`、strict `unified_v2`/Room/OUT 的 NO-GO，
也不把 legacy `send_message` 或 direct-peer 变成 canonical cross-task surface。当前源码冻结
后的 Go matrix、full archtest、地图刷新和 `make orch-upgrade-render && make orch-upgrade-check`
均已完成；后续任何源码/文档变更都必须重新执行相应验证。

codemap 当前为 393 files/1474 refs（`make codemap-check` 可复核）；最终
`make project-map-refresh PROJECT_MAP_ARGS=--full && make project-map-check` 已通过：5516
files、8 domains、`drift=OK`。这些生成物只反映目录索引，不代表运行时能力或发布资格。

production orch composition继续使用unavailable/reject-all，不装配local SQLite authority或HTTP client candidate。legacy send/directpeer也不能因task principal/event source候选存在而升级为cross-task能力；managed provider、Room与release仍保持NO-GO。总计划保持Active，十四份完整规格保持Draft，cross-task send与Room保持NO-GO。

内部组件进度分别记录于[不可变命令意图](command-intent-storage.md)、[send_message_v1 Profile](send-message-intent-profile.md)、[CONTENT-1](content-1-encrypted-single.md)、[RESOURCE-1](resource-1-reserved-skeleton.md)、[B08 wakeup command candidate](B08-wakeup-command-candidate.md)、[RecoveryCustody SQLite authority](recovery-custody-sqlite-authority.md)、[RecoveryCustody service candidate](recovery-custody-service-candidate.md)、[Task Principal/Event Source](task-principal-and-event-source.md)、[managed runtime mapping candidate](managed-runtime-mapping-candidate.md)、[managed runtime binding candidate](managed-runtime-issuer-candidate.md)、[child issuer candidate](child-issuer-candidate.md)、[runtime event producer candidate](runtime-event-producer-candidate.md)、[trust envelope candidate](trust-envelope-candidate.md)、[B09 legacy wakeup migration](B09-legacy-wakeup-migration-candidate.md)、[B10 writer-epoch candidate](../../proposals/orch-subagent-s0/b10-taskdag-writer-epoch-candidate.md)与[下一切片 writer session 规格](../../proposals/orch-subagent-s0/b10-taskdag-writer-session-next-slice.md)、[B12 security-time candidate](security-time-owner-candidate.md)、[命令生命周期日志](command-lifecycle-journal.md)、[身份/事件下一切片](identity-event-next-slice.md)、[Canonical EventStore](event-store-next-slice.md)、[授权与可恢复等待下一切片](access-wait-next-slice.md)和[Room contract candidate](room-contract-candidate.md)。`tasktransport`、`messageassociation` 等仅代码候选，验证细节见本目录的验证记录。组件验证不关闭旧生产投递风险，也不改变总计划Active或发布门状态。
