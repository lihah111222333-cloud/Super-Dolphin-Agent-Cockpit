# orch 消息交付覆盖映射

更新日期：2026-09-08。范围：direct-peer 工具、SQLite owner、native receipt、orchestration/standalone 提交链。本文对应当前源码与测试断言，不是行覆盖率报告，也不宣称穷尽所有并发时序。

当前登记有 **12 个真实 provider 交付场景、12 个非模型交付 control 场景，另有 1 个原生记录解析守卫**。包含既有测试在内共 60 个验收场景，本轮单次完整重跑 **60/60 通过、0 skip、0 未验证**。机器可读逐项证据见 [结果记录](orch-message-delivery-results.json)。

最新结果：`/tmp/orch-full60-final-20260908.jsonl` 单次命令退出码 0，60/60 PASS，0 FAIL、0 SKIP，554.897 秒；control 30、clients 9、lifecycle 9、delivery 12 全部通过。真实用户配置另从新建会话开始复验，应用模型→注册 MCP→运行二进制→原生子 agent→orch 工具调用及停止/恢复三轮流程首次通过，详见机器可读记录。

此前分组复验历史：结果口径：完整运行 `/tmp/orch-delivery-frozen-56.jsonl` 中客户端 9/9、生命周期 9/9、交付 12/12 通过，control 的原有 outbox 测试存在通知先于 ACK 落盘的竞态断言。修复后该用例重复 30 次通过，整个 control 26/26 重新通过（`/tmp/orch-delivery-verified-control26.jsonl`）。本次新增两个 SIGKILL 恢复场景后，整个 control 28/28 再次通过（`/tmp/orch-coverage-enhanced-control28-final.jsonl`，10.283 秒）。继续补齐 sink 后 ACK 前崩溃场景后，control 29/29 通过（`/tmp/orch-outbox-enhanced-control29.jsonl`，9.104 秒）；其余 30 项沿用上述上一轮证据。本次再加入双 worker 接管场景，control 30/30 通过（`/tmp/orch-claim-takeover-control30.jsonl`，9.202 秒）。因此，这是明确记录修复的分组复验，不声称首次单次完整命令退出码为 0。

## 证据层与语义

- 真实交付场景使用 Registry handler、进程内 Fx、真实 Codex/Claude、SQLite 和原生 history；不等同 binary HTTP 成功发信，也不等同模型自主调用发信工具。binary operator 场景验证外部权限拒绝。
- 非模型 control 使用真实 Registry/SQLite；并发及持久失败场景使用明确 gated native stub。它们能确定性验证事务与调用次数，不能冒充真实模型崩溃。
- `accepted` 只证明意图持久化；`submitted` 要求原生确认与消息 owner 事务写入；报告成功是独立的完成事实。
- `delivery_outcome_unknown` 不授权再次投递，包括换 key 重发同一业务消息。busy 保守返回 unknown，没有自动排队承诺；恢复验证使用独立的新任务、新正文与新 key。
- `ProviderSubmissionReceipt` 无 schema_version 字段，围栏字段为 provider、agent_id、thread_id、turn_id、receipt_id、launch_seq。grant_revision 是授权代际，不能混称 receipt schema version。
- Claude 原生计数只打开本次 thread UUID 对应的 `.claude/projects/*/<threadID>.jsonl`，匹配 user 文本与 receipt UUID，排除 assistant、tool_result 和其他会话。

## 真实 provider 场景（12）

入口均在 `cmd/mcp-orch/direct_peer_delivery*_e2e_test.go`。

| 顶层测试 | 直接证据 |
|---|---|
| `TestStandaloneDirectPeerSubmitsReceiverTurn` | Codex 原生确认、随机报告、report_seq 与成功 outcome |
| `TestStandaloneDirectPeerSubmitsClaudeReceiverTurn` | Claude session/UUID 确认及报告完成 |
| `TestStandaloneDirectPeerCrossProviderConversation` | Codex/Claude 三轮双向交付（六次新报告） |
| `TestStandaloneDirectPeerDeliveryBoundaries` | 未授权、撤权、停止 receiver 的拒绝；重新授权后的交付；拒绝路径无原生输入及持久副作用 |
| `TestStandaloneDirectPeerCodexOrderedDelivery` | Codex 三轮顺序、消息 cursor 与报告对应 |
| `TestStandaloneDirectPeerClaudeOrderedDelivery` | Claude 三轮顺序、消息 cursor 与报告对应 |
| `TestStandaloneDirectPeerCodexIdempotency` | 相同 key/body 保持 receipt、原生输入一次；异 body 冲突且输入零、报告不变 |
| `TestStandaloneDirectPeerCodexBusyDelivery` | 活跃 turn 前置条件、busy unknown 不排队、原消息输入零、独立后续任务成功 |
| `TestStandaloneDirectPeerCodexRestartReplay` | 真实提交后关闭并重建 app/owner，重放原 receipt，原生输入仍一次 |
| `TestStandaloneDirectPeerClaudeIdempotency` | Claude UUID、SQLite 唯一 submitted 事件、原生输入一次及冲突输入零 |
| `TestStandaloneDirectPeerClaudeBusyDelivery` | 原生 receipt 后同 thread `turn_running`、unknown 输入零、独立新任务完成；不要求工具执行权限 |
| `TestStandaloneDirectPeerClaudeRestartReplay` | 重启后旧 UUID receipt 安全重放且输入一次；当前 Claude 不支持持久线程重新激活，明确验证能力拒绝 |

## 非模型交付 control 场景（12）

| 顶层测试 | 直接证据与边界 |
|---|---|
| `TestStandaloneDirectPeerOutboxSinkProcessCrash` | 真实子进程在 sink 文件副作用 Sync 后、ACK 前被强杀；重开 SQLite 并用测试 SQL 到期租约，同 EventID 通知两次，测试幂等消费者业务效果一次，ACK 后不再领取 |
| `TestStandaloneDirectPeerOutboxWorkerTakeover` | 真实 SQLite 双生产 projector：有效租约不偷领；旧 sink 未返回时到期接管，新 worker ACK 后旧 ACK 返回 ErrEventOutboxFence；通知同 ID 一次/两次，后续不再投影；race 重复 3 轮通过 |
| `TestStandaloneDirectPeerInputContract` | 四工具缺 scope/空 CWD，send/read 缺 agent；未知 action、缺失 agent、自发自收、空 key、无权限、相对 workspace；空正文与 UTF-8 正文恰好 1 MiB/多一字节，拒绝无额外消息事件 |
| `TestStandaloneDirectPeerCursorContract` | 消息和事件 exclusive cursor、limit=1 分页无重复/丢失/乱序、空尾页、200/201、负 cursor/limit；公开 limit=0 对应 50 |
| `TestStandaloneDirectPeerScopeAndPermissions` | send-only/read-only、有向授权、agent/operator scope、receiver/workspace 隔离 |
| `TestStandaloneDirectPeerUnknownRevisionContract` | accepted 重放 unknown；撤权/重授 revision 递增；旧 key 不重新投递、旧消息不可见；事件审计保留明确旧 revision |
| `TestStandaloneDirectPeerConcurrentSubmissionFault` | gated 原生 ack 前同 key 重叠调用；一次 native submit、竞争者 unknown；释放后最终 receipt 重放、唯一 submitted event/outbox |
| `TestStandaloneDirectPeerReceiptCommitFailureRestartFault` | 原生 ack 成功后 SQL trigger 分别拒绝 event/outbox，事务回滚保留 accepted；重建 owner/Registry 后原 key 仍 unknown、新 stub 调用零，无假 submitted/outbox |
| `TestStandaloneDirectPeerReceiverIdentityFault` | controlled native fixture 返回线程漂移或代际拒绝，unknown/无submitted；重建后原 key 不提交 |
| `TestStandaloneDirectPeerFailedReplayRecoveryFault` | 缺 port 产生 failed，恢复 port 后原 key 不再提交；独立新任务可提交 |

既有 `TestStandaloneDirectPeerPersistsAcrossRestart`、`TestStandaloneDirectPeerSupportsBidirectionalConversation`、`TestStandaloneDirectPeerOutboxProjectsEvent` 继续覆盖持久状态重建、双向授权和事件投影；它们不计入上述新增 12 项，也不替代真实 submitted receipt 重启场景。

新增进程故障入口 `TestStandaloneDirectPeerAcceptedProcessCrash` 与 `TestStandaloneDirectPeerSubmittedProcessCrash`：通过握手定位 accepted 已提交/receipt 事务已提交两个窗口，父进程 SIGKILL 自己启动的子进程并等待退出，重新打开同一 SQLite。前者原 key 两次重放仍 unknown，后者保持完整原 receipt；两者 native 再提交次数为零，message/event/outbox 无重复或部分提交。原生端口是受控替身，不是实际 provider ACK。

## 分支与补充测试映射

| 契约分支 | 测试证据 | 当前边界 |
|---|---|---|
| grant 同 key 重放、参数冲突、活跃 grant 新 key 冲突 | `TestDirectPeerStoreContract` | SQLite 契约 |
| revoke/regrant 代际与并发意图围栏 | `TestRegrantRevisionIsolation`、`TestRegrantConcurrentIntentFence`、UnknownRevision control、真实 DeliveryBoundaries | 本轮修复重授路径；旧 revision 消息与当前授权隔离，事件历史仍明确标注代际 |
| native ack 成功、错误/缺失 receipt、accepted 重放 | `TestDirectPeerNativeReceipts`、`TestDirectPeerSubmitErrorIsOutcomeUnknown` | error-only SubmitTurn 不构成 ack；fresh=false 不再提交 |
| 缺失 port/typed nil、owner delivery_failed | tools DeliveryPortFailure/MissingStore/TypedNil；`TestDeliveryFailedReplayRemainsFinal` | failed 在重建 store 后保持原记录、Fresh=false；完整 Registry 补齐 port 后重放由 FailedReplayRecoveryFault 覆盖 |
| legacy submitted 无 receipt | tools/store LegacySubmitted 测试 | 返回 unknown，不伪造原生确认 |
| owner capability、跨 store capability、message ID 冒充 turn/receipt | `TestNativeReceiptRejectsInvalidIdentity`、owner capability 测试 | 非授权写入被拒绝 |
| provider/agent/thread/turn/launch_seq 围栏 | `TestNativeReceiptExecutionFence`、tools native failure、standalone native identity | 包含旧 generation 与身份错配；不等同全部损坏组合穷举 |
| 所有 receipt 字段往返与 mapper | `TestNativeReceiptRoundTripFields`、`TestNativeReceiptFieldGuard` | 动态字段守卫 |
| 持久 receipt 内外身份闭包与未知字段 | `TestNativeReceiptReadRejectsCorruptPayload`、`TestNativeReceiptReadRejectsEnvelopeCorruption` | workspace/sender/receiver/revision、未知 envelope/receipt 字段等污染，重建 owner 后拒绝 |
| receipt 全字段持久化单点变异 | `TestNativeReceiptPersistedFieldMutations` | 反射枚举 6 字段 × 12 非法值，共 72 子用例；逐一缺失/null/空白/错误类型/零/负数/小数/溢出，经真实 SQL 写入和新 Store 读取均拒绝；不是多字段组合穷举 |
| 尾随第二 JSON 文档 | `TestNativeReceiptSQLRejectsTrailingJSON` | 生产 SQL json_valid 约束拒绝注入，原 receipt 保持不变；未绕过约束假造可达路径 |
| 同消息并发 receipt 持久写入 | `TestNativeReceiptConcurrentCommit` | 真实 SQLite 两并发 RecordNativeReceipt 单赢家，另一方 sql.ErrNoRows，重复写拒绝，无重复 event/outbox；本轮 -race -count=10 通过，18.118 秒 |
| 状态、event、outbox 原子性 | `TestNativeReceiptTransactionRollback`、ReceiptCommitFailureRestartFault | event/outbox 持久失败均无部分成功 |
| 原生写入不等于 ack、退出前无 ack | `TestNativeAckIdentityAndWait`、`TestNativeClaudeWriteIsNotAck` | 单元可控时序与真实正常提交互补 |
| Claude 合法 system/message 字符串 | `TestClaudeSystemTextMessage`、`TestClaudeMessageDiscriminatorRejectsInvalidTypes` | 本轮修复 wire discriminator；system 元数据不能冒充模型报告或 user receipt；非法类型/尾随帧继续拒绝 |
| Claude 原生输入解析 | `TestClaudePeerNativeInputParser`、真实 Claude 幂等/忙碌 | text block/string；assistant/tool_result 回显及 marker 前缀碰撞不计数 |
| outbox claim 到期接管与旧 token ACK | `TestDirectPeerEventOutboxClaimLeaseAndFence` | SQLite 租约围栏 |
| outbox renew 正常/错误 worker/错误 token/过期 | `TestDirectPeerOutboxRenewalBoundaries` | 续期结果与持久值一致，过期 renew/ACK 拒绝 |
| pending初领/expired接管 × 首/中/尾claim写失败 | `TestDirectPeerClaimBatchRollback` | 6 组合核对整批 status/worker/token/lease/attempt_count 原样回滚、sink零调用；重建后原事件各投影一次，失败事务不增加attempt；最终 race 6/6 通过 |
| 批次首/中/尾 × sink/ACK/取消故障 | `TestDirectPeerProjectorBatchRecoveryMatrix` | 9 个组合逐事件断言状态与调用次数；已 ACK 前缀不重放、失败点重投同 ID、未调用后缀继续；重建 Store/projector 后完成且再次 Process 无副作用 |
| sink 失败不 ACK、成功不重复领取 | `TestDirectPeerEventProjectorAcksOnlyAfterSink`、既有 standalone outbox 场景 | 投影确认边界 |
| 损坏事件 poison 与健康事件继续 | `TestDirectPeerOutboxPoisonContinuesHealthyEvents` | 独占测试连接模拟磁盘损坏后恢复约束；带诊断隔离，健康事件继续，poison/projected 不再领取 |

## 仍需明确保留的证据边界

1. **真实进程精确崩溃窗口**：已覆盖真实子进程在 accepted 已提交及 receipt/event/outbox 已提交后的 SIGKILL 与 SQLite 重开恢复；仍未覆盖真实 provider ACK 精确指令点、事务执行中任意位置、机器断电和全部调度组合。
2. **真实执行身份替换窗口**：controlled native fixture 已覆盖线程漂移与 owner 拒绝代际变化；它不是在实际 provider 指令中插入调度闸门。Registry 不具有独立的 snapshot launch generation 字段，代际有效性由 native owner 保证。
3. **完整故障组合**：receipt 单字段污染已覆盖 72 项，但多字段污染的所有排列、历史 legacy 状态跨真实进程重启，以及 outbox poison/renew/进程退出的全部交叉时序（sink 后 ACK 前真实进程退出已覆盖），不能由有限代表测试推导全部覆盖。
4. **能力边界**：Codex 支持持久线程恢复；Claude 的既有 receipt 可重放，但当前产品明确不支持持久线程重激活。覆盖拒绝路径不等于实现该能力。

此前草稿列出的同 key 并发、ack 后持久失败并重建 owner、输入/scope/权限/cursor、receipt 外层污染与尾随 JSON、failed store replay、outbox renewal/poison、Claude 幂等/忙碌、Codex 真实 receipt 重启重放、同消息并发 receipt 持久写入，现均已有上表明确测试入口。关闭的是这些具体证据缺口，不是对消息交付作无边界的“100%”承诺。

本次 outbox 扩展未修改生产代码。重复通知是 EventSink 契约允许的恢复行为，消费端必须按 EventID 幂等；测试消费者的单次业务效果不能外推为所有产品消费者均已满足 exactly-once。
