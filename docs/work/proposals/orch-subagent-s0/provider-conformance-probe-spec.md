# Provider Conformance Probe 子规格
Spec-ID: PROBE
Status: Draft
PROBE-Production: NO-GO
Depends-On: VERIFY_S, ROLLOUT_S
S0-Owner: Provider conformance owner with independent per-provider evidence scope
S0-Scope: four official CLI probes, capability profiles, bounded fixtures, and attested evidence
S0-Dependencies: VERIFY_S, ROLLOUT_S
S0-Inputs: immutable executable identity, provider profile, fixture revision, workspace, and resource limits
S0-Outputs: bounded observation, capability result, evidence digest, and signed attestation candidate
S0-Failure: unsupported, stale, tampered, or uncertain probes quarantine and issue no capability lease
S0-Recovery: query the original run id and request digest; reconcile before any stateful rerun
S0-Field-Chain: requirements -> probe admission -> fixture observation -> attestation -> capability repository -> bridge
S0-Named-Acceptance: PROBE-P01 PassiveRequirements; PROBE-P06 ForkCallerResourceMatrix; PROBE-P07 PerProviderCapabilityJoin; PROBE-N01 OfficialCLIMustNotBeImpersonated

负责人：provider conformance owner；审核对象是四家官方 CLI 的独立、可复查能力证据。
本文件是 Gate S0 子规格草案，不是 capability lease、生产启用或总规格 Accepted 的声明。

## 1. 范围与边界

PROBE 只回答“某个版本固定的官方 CLI 是否实际提供某项能力，以及证据属于哪一类”。
它不启动统一控制面、不写 Agent Graph current binding，也不替代 BRIDGE、CTL 或 OUT owner。
每个 provider 必须单独建立 profile、tested tree、fixture、证据和结论，禁止同厂商或同模型横向推导。
`execution_surface` 固定为 `official_cli`；SDK、Desktop App、Web App、独立 API、TUI 和未声明日志格式排除。
orch 通过 `launch_agent(kind=managed_agent)` 创建的 CLI 进程是 managed agent，不是 provider 原生 subagent。
provider hook 只看到 child 名称或 transcript 时最多形成 `observed`，不能填充 `fully_controlled`。

## 2. 依赖与 owner 职责

PROBE 依赖 `VERIFY_S` 的 machine-DAG、requirements registry、artifact/partition 证据骨架。
PROBE 依赖 `ROLLOUT_S` 的 release lineage、测试树摘要、签名和 anti-rollback 约束。
probe evidence head 的恢复域外 authority 仍由独立外部单实例服务持有私有 SQLite；PROBE 不在本地打开、复制或替代该 authority DB，普通本地 SQLite/WAL 只能作为未验证候选。
Probe runner owner 生成并冻结 `provider-evidence-requirements.json`，不得让 provider adapter 自改要求。
Fixture owner 负责隔离 workspace、凭据引用、资源预算、进程树和清理证明；凭据正文不可进证据。
Evidence verifier 在 checkout 外校验签名、digest、版本、scope、完整 fixture 覆盖和失败分支。
Capability repository owner 只在 verifier 成功后提交 scoped lease；PROBE 不能直接签 lease。
审计 owner 保存原始证据索引、运行 cursor、负测结果和 missing 列表，不能按最后一次成功覆盖失败史。

## 3. 输入合同

requirements 文件至少固定 `schema_version`、`provider_id`、`provider_version`、CLI canonical path/digest。
它还固定 `tested_tree_digest`、runtime closure、machine surface、transport、profile kind 和 fixture revision。
每一项 capability 是 `{name,scope,subject,evidence_mode,fixture_id,phase_owner,required_fields}` 判别联合。
`evidence_mode` 仅允许 `passive|isolated_profile|stateful`，未知值、重复 capability 或空 fixture 立即拒绝。
运行输入包含显式 executable、home、model allowlist、工作区、临时 state namespace 和资源/时限上限。
启动前必须记录 executable file identity、digest、interpreter/script/package closure 与平台签名或等价证明。
凭据只作为受控引用消费；日志、attestation、report、ready metadata 和命令行均不得出现 credential value。
VERIFY_S/ROLLOUT_S 的 current digest、release target 与测试 artifact 作为输入，缺失不得以本地默认值补齐。

### 3.1 Stateful capability evidence matrix（硬字段）

stateful capability 不得只以一句 `pass` 表示；attestation 必须携带下表对应的完整字段集合。缺任一字段、字段为空、跨 provider 复用或由另一个 capability 推导，结果只能是 `unsupported|quarantined`，不得进入 lease candidate。

| capability family | `evidence_mode` | required evidence fields（canonical names） | 正例必须证明 | 负例必须证明 |
| --- | --- | --- | --- | --- |
| `session.fork` / `child.fork` | `stateful` | `fork_operation_id`, `fork_request_digest`, `parent_execution_incarnation_id`, `parent_runtime_instance_id`, `child_execution_incarnation_id`, `child_runtime_instance_id`, `source_session_epoch`, `process_tree_generation_before`, `process_tree_generation_after`, `authority_inventory_root_before`, `authority_inventory_root_after`, `planned_child_id`, `actual_post_fork_receipt`, `child_caller_attribution_ref`, `resource_reservation_ref`, `cleanup_receipt_ref` | 同一官方 CLI runtime 中 parent→child 的真实 fork；child 先 suspended/quarantined，取得 process/authority post receipt 后才可 resume；重启后按原 operation 查询得到同一 child | clone/exec/attach 或 provider hook 冒充 fork；旧 epoch、重复 operation、未取得 post receipt、跨 runtime 或 parent authority 未冻结时 provider 调用数为 0 |
| `caller.attribution` | `isolated_profile` + `stateful` | `caller_principal_ref`, `caller_scope_ref`, `credential_carrier_profile`, `issuer_id`, `issuer_generation`, `verifier_boot_epoch`, `connection_or_session_nonce`, `token_digest`, `target_execution_incarnation_id`, `attribution_receipt`, `replay_guard_key`, `replay_rejection_receipt` | transport/auth middleware 产生不可伪造 principal，并将 child token 单次绑定到目标 incarnation；同 nonce 重放被拒绝且有 receipt | 普通 metadata、`agent_id`/thread id、共享 connection、过期/跨 scope token 或重连后 token 试图建立 caller；授权/副作用调用数为 0 |
| `resource.admission`（launch/fork/recover/respawn） | `isolated_profile` + `stateful` | `resource_reservation_id`, `resource_policy_revision`, `resource_policy_digest`, `reservation_generation`, `principal_ref`, `workspace_run_ref`, `provider_runtime_ref`, `parent_incarnation_ref`, `depth`, `active_agent_count`, `in_flight_count`, `fan_out`, `concurrency`, `wall_time_deadline`, `token_budget`, `cost_budget`, `admission_receipt`, `usage_receipt_or_absent`, `release_receipt`, `quarantine_receipt` | durable reservation 在实际 spawn/fork 前单赢家提交；usage/release 与 command/cleanup fence 对齐；超额 child 进入 quarantine 并可重放收口 | 纯内存计数、缺任一维度、stale policy、跨 workspace 借额、重复 fan-out 或 reservation response unknown；provider/resource/command side effect 均为 0 |
| `message.output.receipt.reconcile` | `stateful` | `operation_id`, `attempt_id`, `request_digest`, `source_cursor`, `provider_receipt_digest`, `outcome_state`, `reconcile_query_receipt`, `content_ref`, `resource_reservation_ref`, `caller_principal_ref` | provider receipt、output cursor 和 CONTENT ref 在 crash/restart 后按原 id 查询，结果唯一且不重复派发 | 只有 transport ACK、缺 receipt/query、换 key 重放或跨 provider 查询；保持 `outcome_unknown` 且调用数为 0 |

`fork`、`caller.attribution`、`resource.admission` 三个 family 必须分别取得 required fixture；一个 family 的 receipt、principal 或 reservation 不能填充另一个 family 的字段。`fork` 若 provider 仅支持 root/session fork，必须显式返回 `scope=root_only`，不得升级为 `child.fork` 或 `fully_controlled`。

## 4. 四 provider 独立探针

Codex probe 以指定 `codex` 与其 CLI-owned `codex app-server` 机器协议为唯一候选。
Codex 的根 session、thread、turn 和 MCP tool-call 证据不自动成为 native-child spawn/control 证据。
Claude probe 以指定 `claude` 的官方机器接口、`-p`/stream-json 和 hooks 为唯一候选。
Claude Agent-tool hook 可提交 observation/boundary candidate；background session 要另按 provider_session 或 managed_agent 探针。
Antigravity probe 以指定 `agy` 官方 CLI；`/agents`、statusline 或模型内工具只是产品行为观测。
若没有 CLI-owned、同 runtime identity 的 child transport，Antigravity native child 必须记录 unsupported。
Grok probe 以指定 `grok` 和 `grok agent stdio`/ACP 为唯一候选，root ACP 证据不外推 native child。
四项 probe 的版本、path、runtime instance、session epoch 和 evidence hash 必须逐项不同并独立验签。

## 5. 输出合同与能力等级

runner 生成 out-of-tree `provider-evidence-run-attestation.json`，其中保存 requirements digest、run id。
attestation 还保存 provider/CLI identity、fixture phase、input/output digest、cursor、result、observed time。
签名 envelope 必须绑定 security namespace、release lineage、tested tree、artifact partition 和 signer scope。
可重建的原始记录只以 content reference 或加密对象保存；attestation 不内联秘密和无界 transcript。
输出按 capability 产生 `pass|fail|unsupported|not_run|outcome_unknown|quarantined`，不使用模糊的“基本支持”。
`passive` 只能输出版本、schema、工具和可发现性候选，不能输出任何 stateful 行为 lease。
`isolated_profile` 可证明 profile wiring、schema、边界和 exact-set，但不能证明真实 child 生命周期。
`stateful` 才能证明 session/child/output/receipt/message/interrupt/reconcile 等行为，并须有正负夹具。
能力等级由 profile requirement AST 计算：`none|root_only|observed|managed|fully_controlled`。
`observed` 只表示身份/事件可见；`managed` 只表示 orch owner 可管理该 execution；两者均不是 fully controlled。
`fully_controlled` 需要其 profile 的所有 required stateful capability、caller attribution、receipt 和 recovery proof。
任一 required evidence missing、过期、跨 scope 或结果不确定时，结论为 `unsupported` 或 `outcome_unknown`。

## 6. 探针状态机

运行状态为 `draft -> admitted -> passive_running -> passive_recorded`。
满足 isolated profile 要求后进入 `isolated_running -> isolated_recorded`，再按要求进入 `stateful_running`。
stateful 成功进入 `attested`；验证成功后只生成 `lease_candidate`，需由 capability owner 另行 commit。
失败状态为 `rejected|quarantined|expired|outcome_unknown|not_run`，均不可当作通过。
`outcome_unknown` 只能 query 原 `run_id+request_digest` 并进入 reconcile，禁止换 key 重跑副作用 fixture。
runner 崩溃、超预算、清理不明、receipt 不明或 process identity 变化均先 quarantine，再由 owner 收口。
新 provider/version/path/runtime digest 使旧证据失效；旧证据可审计重放，不可续发新 lease。
同一 operation id 同 digest 幂等返回原 attestation；同 id 异 digest、不同 fixture 复用或跨 provider 引用均冲突。

## 7. 字段消费链

requirements canonical bytes 经 VERIFY_S/ROLLOUT_S 校验后进入 probe admission，不允许 runner 省略字段。
runner 读取实际 CLI identity 与 fixture input，产出 bounded observation、negative vector 和 output digest。
verifier 对 attestation 重算 requirements/result roots，校验签名、cursor 连续性、scope 和时间有效性。
verifier 通过的 capability candidate 进入 sidecar capability repository，提交时绑定 provider/session/runtime generation。
bridge handshake 只消费已提交的 scoped lease 和当前 CLI identity，不读取 runner 内存状态。
identity、resource、content、outbox、trust owner 分别消费其自己的引用；PROBE 不代写这些事实。
TOOLS 只把 probe/lease 作为 per-provider join 输入，不能把 probe hash 代替 tool manifest 或 policy hash。

### 7.1 Per-provider capability join（禁止横向推导）

capability repository 的唯一 join key 为：`{provider_id, provider_version, execution_surface=official_cli, cli_canonical_path_digest, tested_tree_digest, runtime_instance_id, session_epoch, profile_id, capability_name, capability_scope, capability_subject, evidence_mode, fixture_revision, requirements_digest, attestation_digest}`。提交 scoped lease candidate 时还必须逐字段 join `caller_principal_ref|resource_reservation_ref|source_fence|content_ref_or_absent|outbox_operation_ref_or_absent|lease_expiry`；任一 join 缺失、过期、scope 不符或来自不同 provider，结果为 `unsupported`。

四个 provider 必须分别保留独立的 capability set、requirements digest、attestation root、negative head 和 lease-candidate root。允许的 join 关系如下，表内 `—` 表示该 provider 不能借用另一 provider 的证据：

| provider profile | root/session evidence | child/fork evidence | caller attribution | resource admission | 可提交的 tier |
| --- | --- | --- | --- | --- | --- |
| `codex_official_cli` | `codex_root_session` | `codex_child_fork`（必须有 fork matrix 全字段） | `codex_caller_vN` | `codex_resource_vN` | 按本行四项实际交集计算 |
| `claude_official_cli` | `claude_root_session` | `claude_child_fork`（缺失则 `unsupported`） | `claude_caller_vN` | `claude_resource_vN` | 按本行四项实际交集计算 |
| `antigravity_official_cli` | `antigravity_root_session` | `antigravity_child_fork`（无 CLI-owned child transport 必须 `unsupported`） | `antigravity_caller_vN` | `antigravity_resource_vN` | 按本行四项实际交集计算 |
| `grok_official_cli` | `grok_root_session` | `grok_child_fork`（root ACP 不得外推） | `grok_caller_vN` | `grok_resource_vN` | 按本行四项实际交集计算 |

repository 在写入任一 provider candidate 前必须执行 exact join：`provider_id/version/path/tree/runtime/profile/fixture` 全相等，且该 capability 所需的 fork、caller、resource evidence 均为 `pass`；`observed`、`root_only`、`outcome_unknown`、其他 provider 的 `pass` 或不同 runtime 的成功均不能满足 join。bridge 只读取这个已提交的 per-provider candidate，不读取 aggregate/last-success projection。

## 8. 权限、隔离与恢复

probe principal 仅拥有指定 provider、workspace、fixture namespace 和预算；不得读取其他 run 或 workspace。
stateful fixture 必须先完成 resource admission，再创建 reservation、process boundary 和 cleanup plan。
provider-originated child 若已经运行，必须先登记并进入 `already_running_quarantine`，不能假设可安全接管。
无法证明 `zero_physical_effect`、process start identity、authority revoke 或 cleanup 完整时，不得继续控制。
CLI path 必须由已验证 immutable handle 启动；shell、PATH 可替换 symlink、SDK/App fallback 一律拒绝。
凭据、provider receipt、原始命令和输出正文按 content-owner 加密保存，错误和日志只保留脱敏摘要。
重启恢复先读取恢复域外 evidence head 与同 operation，验证 lineage/anti-rollback，再恢复可审计状态。
恢复过程中 capability lease 为 stale，bridge 必须停止新的 observation/command admission；不得以旧 observed 证据续期。

## 9. 当前 standalone 证据与缺口

ST-05 已有真实 Codex app-server MCP `list_agents` 调用，证明工具连接，不证明模型 turn 或 native child。
ST-06 已有真实 Claude MCP 配置/工具调用证据，但发布环境和完整工具矩阵仍未验证。
ST-07 已有 standalone binary 下 Codex/Claude managed task 双轮本地证据，仍缺官方 binary provenance、发布和远程环境。
ST-08 已有 Codex↔Claude direct-peer 本地双向 evidence，但使用 trusted scope，不是第三方 bearer agent 身份完整路径。
ST-13 有部分 Codex fork/resume/archive/interrupt 证据；Claude archive/interrupt 保持 typed unsupported，不是成功能力。
上述结果均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不得直接签 PROBE stateful lease。
当前缺少四 provider 版本钉死的 requirements/attestation 产物、完整 native-child stateful fixture 和 out-of-tree signer。
还缺 caller attribution、fork、resource admission、failure/reconcile、process cleanup 及跨重启证据；ST-09/11/14/16～18 也未闭合。

## 10. 具名验收与退出条件

PROBE-P01：四 provider 各自以固定 CLI path/version/tree 通过 passive requirements，证据无跨 provider 引用。
PROBE-P02：四 provider 各自完成 isolated fixture，校验 exact profile、runtime identity、negative vectors 和资源清理。
PROBE-P03：声明 stateful 的 capability 必须以真实 session/child/receipt/cursor/reconcile 正例及对应负例证明。
PROBE-P04：attestation 可在 checkout 外独立验签、重算 digest，并被 repository 以 scoped lease candidate 接收。
PROBE-P05：同 key 丢响应 query 返回同一结果；过期、unknown、stale、异 digest 和重放都 fail-fast。
PROBE-P06 `ForkCallerResourceMatrix`：四 provider 各自运行 fork、caller attribution、resource admission 的 stateful fixtures；attestation 逐字段匹配 §3.1，缺字段/跨 scope/跨 provider 均无 lease。
PROBE-P07 `PerProviderCapabilityJoin`：repository 仅接受同一 provider 的 `{path,version,tree,runtime,profile,fixture}` exact join，且 required capability family 全为 `pass`；aggregate/last-success/另 provider 证据被拒绝。
PROBE-N01：用 SDK、Desktop、TUI、共享模型或另一 runtime 冒充 official CLI，必须零 lease、零副作用。
PROBE-N02：orch 创建 managed CLI 后标成 native_subagent 或 fully_controlled，必须被拒绝并记录 topology mismatch。
PROBE-N03：只有 hook/name/transcript、只有 root MCP tool-call 或只有文档描述时，最高为 observed/unsupported。
PROBE-N04：缺 executable digest、process start proof、caller attribution、cleanup receipt 或跨 provider scope 时，必须 fail closed。
PROBE-N05：伪造、重复、过期签名、回滚 artifact、跨 tested tree 或修改后正文，必须拒绝 attestation。
PROBE-N06 `ForkWithoutAuthoritySuccessor`：缺 process-tree/authority successor、post-fork receipt 或 parent freeze 的 fork 只能 quarantine，provider call count=0。
PROBE-N07 `CallerOrResourceBorrowing`：metadata/共享连接伪造 caller，或纯内存/跨 workspace 借用 resource reservation，必须零副作用并保留 typed failure。
退出条件：requirements、四套 fixture、正负向量、签名 attestation、missing ledger、逐 provider capability join 和 verifier 报告全部可复查；恢复域外 evidence head 不可读时保持 `NO-GO`。
任一 provider 尚未达到其声明 tier 时保留 Draft/unsupported；用户批准推进计划不改变此退出判定。
