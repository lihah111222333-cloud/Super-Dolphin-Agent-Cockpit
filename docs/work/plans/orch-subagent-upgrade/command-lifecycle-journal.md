# ST-11 命令生命周期日志：内部实现切片

Status: Active

日期：2026-09-01。基线 HEAD `d95c6379812d9d9b95e8831c4713d219752e3eaa` 加隔离工作树候选改动。
本切片承接[不可变意图存储](command-intent-storage.md)与[可靠命令顺序](command-reliability-next-slice.md)，不替代 OUT/ID/CONTENT/RESOURCE 的完整合同。

## 已实现范围与边界

`store/orchestrationcommand` 同时拥有不可变 intent 与 append-only lifecycle journal；没有另建 direct-peer 或 wakeup command owner。
新增 `Coordinator` 只校验已有权威回执并追加本地事实，不调用 provider、不签发 permit/token、不消费 nonce。
构造必须显式提供 `ReceiptVerifier`，nil/typed-nil 立即失败；没有生产默认成功实现，也没有 Fx/runtime 注册。
测试使用显式 fake verifier，只证明绑定数据和拒绝路径，不能证明真实授权、恢复域、资源或 provider receipt。
任何 `Snapshot` 都是某次操作的历史事实，不是执行许可、accepted command fence 或完整 submission fence。

真实 provider 投递目前仍走旧路径；direct-peer accepted replay 已有不授权的止重放分支，但真实 OUT/receipt/unknown 与 B08 wakeup 仍未修复，ST-11/B08 不关闭。

## 唯一事实源与原子写入

- migration 127 新增 `orchestration_command_journal STRICT`；通过 `command_id` 引用 migration 126 的 intent。
- `(command_id, version)` 是主键；同一 command 版本连续递增，不能覆盖或删除历史。
- `(command_id, operation_id)` 唯一；同操作同摘要返回原始记录，异摘要返回 `ErrOperationConflict`。
- 最新状态由 journal 的最大 version 派生，不建立可独立漂移的 mutable head 表。
- `Prepare` 显式写 version 1 的 `recovery_custody_pending/pending`；没有记录时返回 not-found，不从空值推断 pending。
- `Apply` 先读取原操作，再校验预期版本、唯一状态机、attempt fence 和回执，最后由单条 SQLC `INSERT SELECT RETURNING` 裁决 CAS。
- CAS 条件包含旧 version、phase、attempt、accept resolution，并确认没有更新的版本；并发预读后发现 winner 时再次查询原 operation。
- 新 attempt 的 partial UNIQUE 索引只覆盖 `begin_dispatch/reclaim_unsent`；同 attempt 的后续阶段可追加，但历史 attempt 不能再次开立。
- SQL 的零行与精确 SQLite UNIQUE 冲突才进入原 operation 查询；其它数据库故障原样失败，不重试、不吞错。

固定 sqlc v1.30.0 在嵌套子查询中留下未替换参数的候选写法已移除。
正式查询没有手写位置参数，也不靠全表聚合查 attempt；生成物必须无残留 `sqlc.arg`。
数据库新 attempt 唯一约束取代嵌套参数查重，不降低幂等要求。

## 状态与重放语义

规范正常链为：

```text
recovery_custody_pending -> accepted -> dispatch_custody_pending
 -> dispatched -> effect_authorization_pending -> effect_token_issued
 -> external_effect_started -> provider_acknowledged -> completed
```

上述箭头记录经过 verifier 验证的事实，不执行箭头名称所代表的外部动作。
唯一转换表位于 `lifecycle_machine.go`，由 stateless 驱动；这仍是内部显式注册表，不是总规格所需的完整生成式 effect/terminal registry。

| 分支 | 当前内部保证 |
| --- | --- |
| accept abort | 仅 pending 可转 rejected，`accept_resolution=aborted`，无 attempt |
| 未送出 reclaim | 仅 dispatch-custody-pending、verifier 证明未送出时允许新且未使用过的 attempt |
| dispatch abort | 保留原 attempt，经明确 reject 后终止；不伪装成 acceptance abort |
| redeem 后 abort | before-issue / before-start 都必须经过 cleanup-pending，再由独立证据转 rejected |
| outcome unknown | token-issued / effect-started / provider-acknowledged 可记录 unknown，保留来源阶段和原 attempt |
| unknown reconcile | 只接受原 attempt 的 ack/completed/failed 证明；不能 begin/reclaim/issue/start |
| 证明未启动 | 仅 token-issued 来源的 unknown 可转 before-start，随后仍必须完成 cleanup |
| 终态 | completed/rejected/failed 没有出边，不能复活或创建新 attempt |

同 operation 重放故意返回它首次写入的 version/time/phase，而不是当前最大 version；查询当前状态使用 `GetState`。
重放不再次调用 verifier，也不能被调用方用于绕过当下权限检查或重做外部动作。
公共访问授权仍必须在未来 transport/command API 层实施，当前组件没有公开工具入口。

在 token-issued 之前，外部恢复/permit 操作的未决结果仍需按原 owner operation 查询；本切片未实现该恢复协议。
不得因本地 pending、超时或租约到期自行构造“未送出”证据。

## 字段生产与消费

| 字段组 | 生产者 | 消费者与约束 |
| --- | --- | --- |
| command ID / immutable intent（含显式 caller principal/scope ref） | 原 intent Store | Coordinator / verifier / journal FK；引用非空不等于 transport 认证、授权或 CONTENT custody，不复制原始正文 |
| operation ID / expected version / trigger / attempt / evidence | 显式 TransitionRequest | 输入校验、digest、状态机、verifier、SQL mapper |
| operation digest | 带 `orch.command.transition/v1` 域的 SHA-256 | operation replay 比较；不是签名或权限证明 |
| next phase / resolution / uncertainty / starts-attempt | 唯一状态机 | 私有 plan 全字段复验、SQL 参数、回读 shape 校验 |
| command/schema version | SQL 固定值 1 | mapper 拒绝未知版本；不是缺失字段默认值 |
| journal version / recorded_at | 原子 SQL / SQLite UTC 时钟 | 历史排序、精确重放；不充当 security-time authority |

`History` 要求显式 limit 1..200、非负 cursor；无 genesis 或未来 cursor 报错，空的合法下一页才返回空集合。
row mapper 验证时间、版本、phase/trigger、accept resolution、genesis、evidence、uncertainty 和 attempt shape。
公开读取进一步从持久字段重算 operation digest，并精确查询 version-1 校验直接前驱和派生字段；History 页内逐条校验连接，仅第一页边界另查前驱，避免每行追加查询。
损坏行使整页读取失败；`accepted` 不能因随后有人插入一个形状合法的 `completed/reconcile_complete` 行而被读成完成。
这是相邻记录的一致性检查，不是全库扫描、签名验证或抗恶意重写整段历史的证明；真实恢复域/receipt verifier 仍需独立实现。

## 验证状态

最终候选的生命周期业务验证已执行；迁移与启动门结果另列，不从生成或静态检查推断通过。
使用项目宿主资源准入入口：

```text
./scripts/test_with_guard.sh --host-test light ./cmd/mcp-orch/store/orchestrationcommand -run '^TestCommandLifecycleContract$' -timeout=120s -count=1
./scripts/test_with_guard.sh --host-test light ./internal/platform/db/sqlite -run '^TestCommandJournalMigrationContract$' -timeout=120s -count=1
./scripts/test_with_guard.sh --host-test light ./internal/platform/db -run '^TestMinRequiredSchemaVersion$' -timeout=120s -count=1
```

| 验证 | 实际结果 |
| --- | --- |
| TestCommandLifecycleContract | 历史 host-light 观测曾 exit 0（archtest219.235s、业务5.762s）；当前本机包测试通过；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。覆盖并发操作/历史重放/attempt唯一性/unknown/cleanup/重开/分页/损坏读取/字段守卫及完整 intent regression |
| TestCommandJournalMigrationContract | 历史 host-light 观测曾 exit 0（archtest126.185s、迁移业务1.594s）；当前本机迁移测试通过；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。覆盖 migration 127 DDL/partial unique/append-only/旧版升级/embedded fresh+legacy+disk一致性及前序 intent 回归；其嵌套的 `TestCommandJournalMigrationReaches133AndConstrainsSchema` 保留历史函数名，但当前断言已要求最高迁移版本 134 |
| TestMinRequiredSchemaVersion | 当前 schema gate 的本机定向测试 exit 0；确认最低 schema floor 为 134（早期 v127 combined-migration 记录仅属历史切片） |
| SQLC | `make sqlc-generate-worktree`、`make sqlc-verify-worktree` exit 0；新增前驱查询后再生成无漂移 |
| LSP | Coordinator definition/references/call hierarchy 与生产/生命周期/迁移/启动门 diagnostics exit 0；构造器当前仅测试消费者，无生产 Fx 接线 |
| 结构与规划 | `git diff --check` 与定向 code/entry guard 当前通过；`make orch-upgrade-check` 当前 exit 0（Python 29 tests 与 dependency-map check）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段（早期17/22/25/26组仅为历史快照） |

第一次 host 执行在架构前置阶段仍有审查修正，虽然其业务3.626s通过，未用作最终冻结候选证据；最终完整入口已独立重跑。
最终局部候选校验和为 `f1f58dbc4fb5b91742f79bd8ca1da51036717a6e03bdfe36d44cc8112b50c4c7`，重跑前后相同；该摘要对应历史 v127 journal slice，不是当前全仓 schema digest。
它由 `orchestrationcommand/*.go`、journal query/generated Go、migration127 和 journal migration test 的 `shasum -a 256` 清单再做 SHA-256 得到，只标识这组本地文件，不是整个仓库、签名或 release attestation。
所有本机证据仍是 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；未执行完整 make guard/项目地图 closure、race、真实进程 crash/reclaim、模型 E2E 或发布安装验收。

状态机测试使用独立的27边契约期望表，与实际注册表比较缺边、多边、错误边和重复边；不能从被测表本身生成“正确答案”。
读完整性测试必须先证明损坏 fixture 可被 DDL 插入，再验证 GetState/GetOperation/History 拒绝，避免把插入约束失败当读取守卫生效。

## 有限范围审查记录

Review object 为上述 HEAD 加本切片候选文件，不是整个 dirty worktree 或仓库发布资格。
schema 车道负责 migration/SQLC/baseline；store 车道负责私有写入与映射；测试车道负责状态、并发与字段；根任务负责协调器、集成与最终 host 验证。
独立只读审查提出的“前驱关系漏检”和“同源矩阵自证”已纳入本切片修正及具名测试；子车道静态检查不等于根候选验证。
审查车道曾使用不符合本任务入口要求的单文件裸测试，其输出不纳入这里的验证证据。

| 维度 | 覆盖 | 导航入口与证据/边界 |
| --- | --- | --- |
| D01 架构边界 | Applied | `docs/doc/codemap/02-mcp-orch.md`；唯一 orchestrationcommand owner，项目 archtest 前置 |
| D02 Fail-fast | Applied | Coordinator / journal_read；nil verifier、错误证据、错误摘要和损坏前驱负例 |
| D03 MCP | N/A | 本切片不修改工具 wire、session、transport 或 registry |
| D04 LSP | Applied | Coordinator/Store 的 definition、references、call hierarchy 和 diagnostics；源码及测试精读 |
| D05 Provider/runtime | N/A | 新协调器未绑定生产 provider，不声明原生子任务或进程恢复通过 |
| D06 编排 | Applied | lifecycle_machine；独立契约矩阵、unknown 禁止重发、terminal 无出边测试 |
| D07 Store/sqlc | Applied | migration127/query/生成物；CAS、partial unique、migration 与重开测试 |
| D08 Skill/Memory/Prompt/Thread | N/A | 未修改运行时技能、记忆、提示词或 thread 写链 |
| D09 Frontend | N/A | 没有 frontend-app 变更 |
| D10 Security | Applied | 非授权 Snapshot 与显式 verifier 边界；真实权限、nonce、恢复域完整性仍缺失 |
| D11 Observability | Applied | lifecycle_types 错误类型；冲突、损坏、证据拒绝均不改写为成功 |
| D12 Testing | Applied | 精确 host suites、mapper omission RED/GREEN、真实 SQLite 负例；不计不合规入口结果 |
| D13 Release/Install | Applied | 当前 schema floor 134；migration 127 的 legacy/fresh/embedded 覆盖属于该历史切片，另有启动门测试；未验证发布制品或安装 |
| D14 Performance | Applied | journal_read/query；按主键读取直接前驱，History limit1..200；无性能 SLA 声明 |
| D15 UX/Product | N/A | 本切片不暴露用户工具结果或修改 UI；计划仍 Active |
| D16 Git/Workflow | Applied | 已核对 worktree/common-dir；保留其它 dirty，生成稳定性检查，不提交推送 |
| D17 字段守卫 | Applied | request/plan/SQLC/row/JSON 动态比较、verifier 完整输入、真实 mapper 源码副本删字段检测 |
| D18 DRY | Applied | 生产单一转换表与 plan 复验；测试独立期望故意不复用实现真值 |
| D19 SSOT | Applied | immutable intent + append-only journal，最新状态从日志派生；无第二 mutable head/dispatcher |

## 下一接线条件

1. 明确真实 receipt verifier 的身份、内容、资源、恢复与安全时间依赖；不能把非空 evidence ref 当通过。
2. 补生成式 operation/effect registry、完整 fence、attempt generation/lease、permit issuance/nonce 和 cleanup owner；本 journal 不代替它们。
3. 定义保留真实 command/attempt/provider receipt/outcome 的提交窄端口，替换旧 error-only 路径的猜测。
4. direct-peer/wakeup/DAG 仅创建或找回 canonical command，唯一 dispatcher 执行外部副作用；随后将已有 duplicate RED 转为 GREEN。
5. 完成真实进程崩溃、恢复、晚到回执、provider conformance 与发布证据，才评估 ST-11 关闭。
