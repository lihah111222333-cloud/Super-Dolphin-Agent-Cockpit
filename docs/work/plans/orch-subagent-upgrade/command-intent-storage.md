# ST-11 首批实现：不可变命令意图

Status: Active

日期：2026-09-01；上级：[可靠命令实现切片](command-reliability-next-slice.md)。

## 本批实现合同

本批建立唯一 command owner 的最早持久化原语：`orchestration_command_intents` 与 `cmd/mcp-orch/store/orchestrationcommand`。
它不是第二套 direct-peer/wakeup 调度器，也不是完整 command state machine；后续 attempt、receipt、accept resolution 仍归同一 owner。
本批意图原语仅提供 Reserve、Get、GetByKey，不暴露 accepted、created-can-execute、dispatch、lease 或 provider 已执行字段。
后续同 owner 的内部生命周期组件与其独立验证状态见[生命周期日志切片](command-lifecycle-journal.md)；不能把后续组件的存在回填为本批运行证据。
生产 direct-peer/wakeup/Fx 路径尚未接入 OUT；accepted replay 的应急分支已 fail-closed 止重放，但 provider receipt、unknown reconcile 和 wakeup 重放风险没有因这个组件存在而关闭。

## 持久字段与来源

| 字段 | 含义与约束 |
| --- | --- |
| command_id | 调用方预分配的候选命令 ID，最多128字节；幂等重放返回首个胜出 ID |
| command_kind | 明确的 launch/fork/send/interrupt/stop/recover/room-broadcast/room-round 操作标识，不允许未知动作 |
| caller_principal_ref / scope_ref | 由可信上游显式传入的主体与作用域引用，各最多512字节；本 owner 不从 namespace、agent/thread ID 或 metadata 反推身份，也不把非空值当授权通过 |
| idempotency_namespace | 可信上游计算的 kind-specific 幂等作用域标识，最多256字节；它不再承载 caller/scope，也不建立权限 |
| idempotency_key_digest | 原幂等键的规范256-bit小写十六进制摘要，不存原始秘密键 |
| request_digest | 原请求的规范256-bit摘要；本组件检查格式，不冒充密钥或内容真实性验证 |
| digest_key_id | 固定摘要密钥版本标识，最多128字节，重放不能静默换 key |
| request_content_ref | 最多512字节且不内联prompt；generic base行中的非空值仍不自带authority。只有migration132校验过active/live CONTENT relation并创建held deletion fence的`send_message_v1` profile，才可把它解释为owner-issued canonical ref；仍不等于custody或可派发 |
| source_fence_ref / target_fence_ref | 完整身份/代际材料的引用，各最多512字节；本组件不解析或授予其权限 |
| schema_version / command_version | 此版本固定为1；未知版本在读取时拒绝 |
| recorded_at | SQLite 生成的 UTC 审计时间，不用于租约或授权裁决 |

字符串不进行 trim、默认值或身份别名归一化；不完整、空白别名、NUL、非法 UTF-8 或超限输入在 SQL 前失败。
基础表不持有完整 CallerPrincipal、authorization decision 或 typed ID-1 selector；内容/身份 ref 的真实性、keyring 与 canonical digest 也不能从本表非空字段推断通过。
这里只保存准备阶段的引用；migration132的CONTENT metadata/local object receipt也不将本机SQLite/file commit当恢复域外custody receipt或RPO=0。

上述principal/authorization/typed-binding缺口由migration131的一对一[send_message_v1身份/授权Profile](send-message-intent-profile.md)以kind-specific扩展补齐；migration132又要求[CONTENT-1](content-1-encrypted-single.md) relation/fence。generic base表仍不新增自由组合的authorization/selector列。完整profile仍只是non-authorizing draft，RESOURCE与外部custody依赖未具备。

## 原子性与错误

- 数据库 UNIQUE(namespace, key_digest) 通过 INSERT ON CONFLICT DO NOTHING 裁决并发。
- 同键重放比较除候选 command_id 外的全部意图字段，任何不同均为 ErrIdempotencyConflict，不覆盖原行。
- 同 command_id 已属于另一键时明确报 ErrCommandIDConflict，不把它当正确重放。
- INSERT 返回错误不自动重试；调用方可按原 Key 查询。查询不到不允许据此推断外部副作用状态，因为本组件根本未执行外部调用。
- UPDATE/DELETE 被不可变 trigger 拒绝；未来 retention/crypto erasure 必须单独设计带证明的 tombstone 流程，不能删除旧键后重建命令。
- Get/GetByKey 不创建记录；损坏的版本、引用或时间不能返回成功。

## 字段消费链

Go Intent -> Validate -> intentParams -> sqlc CreateParams -> migration126/SQLite -> sqlc Row -> recordFromRow -> Record/JSON。
Key -> GetByKeyParams -> SQL 唯一键查询同一记录。
字段守卫需要动态枚举 Intent/Record/Key、SQLite PRAGMA 和 sqlc tags，配合 mapper AST、fresh-write one-hot 及固定时间的读取映射测试。
AST 字段名称出现仅作定位证据，不能代替实际值往返；运行时未知结果/receipt 字段仍未实现。

## 验证边界

本批验收是同键重放、全字段冲突、不同作用域、取消/坏输入零写入、跨连接并发单一意图、关闭重开和 schema/SQLC/mapper 一致。
它不证明 provider exactly-once、daemon硬崩溃后任务恢复或 B08 修复；这些仍需 attempt/receipt/query/dispatcher 接线后的真实故障矩阵。
以下验证已由根任务通过仓库精确 host-test 入口完成，测试未执行或入口资源门拒绝不计为通过。

| 验证 | 实际结果 |
| --- | --- |
| TestCommandIntentStoreContract | 早期 host light wrapper exit 0；本轮加入 principal/scope 后本机定向包测试 exit 0，覆盖新增字段的空白/NUL/超限验证、冲突、schema/sqlc/mapper 与值往返；未把早期 host 结果外推为新字段 host 验证 |
| TestCommandIntentMigrationReaches133AndConstrainsImmutableSchema | 本轮本机定向测试 exit 0；该测试函数名保留早期“Reaches133”历史命名，但实际嵌入迁移已校验最高版本 134，覆盖 principal/scope 列的 trim-equality/NUL/上限约束、trigger 与版本检查；migration 131 profile 的独立真实测试名是 `TestCommandIntentProfileMigrationContract`（见 [send_message_v1 身份/授权 Profile](send-message-intent-profile.md)），新增 stale-v130 缺列启动门禁负测通过 |
| TestCommandIntentFieldGuardRejectsRealMapperOmission | 本轮从真实读写 mapper 各派生一个隔离夹具；删除 `CallerPrincipalRef` 后两个子进程均精确 RED，恢复同源码后均 GREEN，测试 exit 0 |
| sqlc生成稳定性 | 固定 sqlc v1.30.0 两次生成后相关三份生成物 SHA-256 完全一致 |
| LSP | 首次快照对生成物未刷新而报告旧类型；显式纳入生成文件后 `store.go` 与 sqlc 文件 diagnostics 重新检查为0诊断，Go编译也通过 |
| 结构与差异 | 定向 code/entry guard、vet、`go build -p=1 ./cmd/mcp-orch` 与 `git diff --check` 当前通过；`make orch-upgrade-check` 当前 exit 0（Python 29 tests 与 dependency-map check）；统一 wrapper 当前在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段（早期 17/22/25/26 组属历史快照） |

初始测试曾因复杂度、函数长度、裸goroutine入口被拒绝；拆分helper并改用受控WaitGroup.Go后才取得上述业务通过，未删除断言或放宽基线。
全部结果是 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED 的开发证据，不是正式发布或远程运行证明；完整make guard/closure、race和第三方真实CLI E2E本批未取得证据。
`make sqlc-verify-worktree` 当前 exit 0；生成稳定性仍只是本地候选证据，不能称作可提交/可发布。
本批完成时共享schema最低版本与必需表集先更新为126；生命周期、ID、Event、AccessWait、send profile、CONTENT-1、RESOURCE-1与wakeup payload后续分别推进至127～134（含 migration 133/134）。各次变化及其两份sqlc manifest均是明确的数据库变更，不是仅注释调整。

## 后续必需工作

1. CONTENT-1方向与本机owner候选已具备；仍需完成TRUST、RESOURCE、生产key/provider adapters与发布证据。
2. migration131 已以 `send_message_v1` 一对一 profile 保存完整 CallerPrincipal、authorization decision 与双端 typed ID-1 current binding，并删除旧 accesswait fake accepted seam；该 profile store/assembler仍未装配，不把 launch/fork/recover/Room 强塞进 send discriminator。其它 command kind 必须各有独立 profile 与授权 producer。
3. 先补CONTENT production adapters并实现RESOURCE与recovery custody真实owner/receipt，再为同一command owner增加accept-pending/custody resolution、attempt、receipt、outcome_unknown和只查询原操作的reconcile；依赖未齐时 production 只保留 non-authorizing draft，不得写 accepted/dispatch。当前新增的 `orchestrationcommand` Module 仅以永不放行的 verifier 做 test-owned 生命周期 genesis（`Prepare` 可记录 `recovery_custody_pending`，但 Apply 必须拒绝且不推进 journal），不能当作 production custody admission。
4. 将 direct-peer、wakeup、DAG 和 standalone 的真实返回身份接到该 owner；旧 error-only 提交口不能猜测 provider 是否已受理。
5. 在完整 current-fence/授权/资源/回执合同通过后启用派发；重跑成功后 mark 前崩溃与跨重启重复请求场景，才关闭 ST-11。
