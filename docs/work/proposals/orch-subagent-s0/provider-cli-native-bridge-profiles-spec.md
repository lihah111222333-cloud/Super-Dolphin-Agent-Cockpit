# Provider CLI-Native Bridge Profiles 子规格
Spec-ID: BRIDGE
Status: Draft
Depends-On: CTL, PROBE, ID, CONTENT, RESOURCE, OUT, TRUST
S0-Owner: per-provider bridge owner with an independently fenced profile for each CLI
S0-Scope: official CLI identity, profile handshake, topology classification, launch/adoption, stop, and replacement
S0-Dependencies: CTL, PROBE, ID, CONTENT, RESOURCE, OUT, TRUST
S0-Inputs: CLI binary identity, provider capability lease, command fence, content ref, and resource reservation
S0-Outputs: bridge session, topology/capability result, provider receipt, and terminal/reconcile evidence
S0-Failure: identity/profile/topology mismatch is unsupported or quarantined before process or provider side effects
S0-Recovery: query the original bridge operation and incarnation fence; replacement uses a new generation only after reconcile
S0-Field-Chain: probe attestation -> bridge profile -> command fence -> CLI boundary -> provider receipt -> ID/OUT event
S0-Named-Acceptance: BRIDGE-P01 OfficialCLIIdentity; BRIDGE-N01 ManagedCannotBecomeNative

负责人：per-provider bridge owner；本文件规定四个官方 CLI profile 的接入、执行、观察和收口边界。
本草案不把 managed CLI 创建、MCP 连接成功或用户批准开始执行写成 native control 已 Accepted。

## 1. 范围与硬边界

BRIDGE 只连接官方 CLI 暴露的 CLI-owned machine surface，并把已持久化 command 转成 provider request。
bridge 不拥有 Agent Graph、current binding、command intent、room、tool manifest 或统一等待逻辑。
provider session、native_subagent、managed_agent 和 a2a_remote_agent 是不同 profile；能力不得跨 kind 继承。
orch 创建的 `managed_agent` 即使使用同一 provider binary，也绝不等于 provider 在 session 内创建的 `native_subagent`。
provider 自发 child 必须先有稳定 identity observation；无安全 adoption 时只能 `observed`、quarantine 或 unsupported。
SDK、Desktop App、Web App、TUI、alternate-screen、键盘模拟、shell/PATH fallback 和未声明日志均排除。

## 2. 依赖与 owner 职责

BRIDGE 依赖 CTL 的 authenticated `ctl/native-agent/v1`、LeaseKey、cancel、session 和 reconnect contract。
BRIDGE 依赖 PROBE 的 version-pinned evidence、capability/evidence-mode 矩阵和 provider-specific missing 结论。
BRIDGE 依赖 ID、CONTENT、RESOURCE、OUT、TRUST 各自 owner；bridge 只消费它们的 reservation/ref/fence。
per-provider bridge owner 维护 profile、CLI executable/runtime closure、transport lane 和 provider error mapper。
identity owner 写 reservation/incarnation/current binding；bridge 只能提交 `ProviderIdentityObserved` candidate。
outbox owner 发行/claim command；bridge 只能执行已 claim 的 command，并回传 provider request/receipt evidence。
content/resource/trust owner 分别控制正文、预算、caller attribution、egress 和 prompt boundary；bridge 不得旁路。

## 3. 共同输入合同

输入必须包含 PROBE attestation digest、provider/version、CLI canonical path/hash、runtime instance 和 machine surface。
还需包含 transport、provider session epoch、bridge lease generation、CTL connection nonce、MCP profile binding。
每次 launch 绑定 executable file identity、代码签名或等价证明、interpreter/script/package closure、cwd 和 sandbox。
实际 spawn 前必须复验 PID start token、executable identity、argv/env/cwd、继承 fd/socket/handle 和 CLOEXEC 状态。
command 输入必须携带 reservation/command id、full execution fence、turn fence、resource reservation、content refs 和 deadline。
provider-originated 输入必须携带稳定 provider identity tuple、observation id、origin、adoption branch 和 evidence cursor。
profile registry 另固定 transcript root；读取必须 directory-fd anchored、`NOFOLLOW`、beneath traversal 和 bounded size。

## 4. 四 provider 独立 profile

**Codex CLI**：候选 executable 是 `codex`，machine surface 是其 CLI-owned `codex app-server`。
root thread/session 的 list/start/resume/fork/turn stream 可进入 provider_session 候选，但 native child 仍需单独 stateful probe。
app-server 发现 subAgent source kind、parent 或 nickname 只算 discovery，不能自发签 native child stop/send/interrupt lease。

**Claude Code CLI**：候选 executable 是 `claude`，使用官方 `-p`/stream-json、hooks 或 documented session commands。
独立 background session 可评为 provider_session 或 managed_agent；session 内 Agent-tool child 的 hook observation 不等于外部 child control。
没有 direct list/send/interrupt/recover/reconcile machine contract 时，native_subagent 固定 observed/unsupported。

**Antigravity CLI**：候选 executable 是 `agy`，只接 CLI-owned documented machine surface。
`/agents`、statusline、`invoke_subagent`、`manage_subagents` 等产品/TUI行为不能填充 bridge transport 或 receipt。
未发现稳定外部 child control surface 时，profile 只可 root discovery 或 unsupported，禁止 fallback 到 UI。

**Grok Build CLI**：候选 executable 是 `grok`，root 候选为 `grok agent stdio`/ACP。
ACP `session/update` 和 root session resume/fork 不外推 native child identity、kill、reconcile 或 caller attribution。
四 profile 的 version/path/runtime/session epoch/evidence hash 必须单独记录、单独失效、单独跑 fixture。

## 5. 连接、能力和拓扑判定

bridge 必须先通过 `same_cli_runtime_identity` 资格门，再为 identity、lifecycle、control、output、receipt/reconcile 各 lane 选 transport。
资格门要求 provider CLI 实际启动或 attach、回显同一 runtime/session/child identity，并在 stateful fixture 命中同一 incarnation。
共享模型、共享 harness、共享 config 或厂商签名不构成同一 CLI runtime 证明；不合格 surface 不得进入排序。
capability lease 固定 scope/subject/constraint、provider/runtime/version/transport/epoch/generation、probe evidence 和 expiry。
provider session lease 不自动下沉 child；session capability 与 target operation capability 必须显式 join。
`control_level` 只是 derived display：`none|root_only|observed|managed|fully_controlled`，不是授权 shortcut。
只有 identity、lifecycle、new turn、output、mid-turn、interrupt、receipt 和 recovery 等 required stateful proof 全齐才可 fully_controlled。
任一 provisional、identity_unverified、过期 lease、missing requirement 或不明 receipt 都使写操作 fail-fast。

## 6. Launch/adoption 状态机

bridge 连接状态为 `disabled -> probed -> ctl_connected -> lease_candidate -> active`，失败为 `unsupported|stale|reconcile_required`。
新 managed launch 先经 resource admission 和 OUT reservation，再进入 `process_start_pending`，不提前写 active binding。
实际进程 receipt 成功后依次为 `incarnation_prepared -> conversion_pending -> measurement_pending`。
随后必须完成 `active_process_prepared -> binding_prepared -> bind_current -> identity_finalized -> resumed`。
任何 prepared 间隙不得修改 current truth；response unknown 保留原 operation 并进入 reconcile/stop。
provider-originated child 只能走 `pre_spawn_blocked` 或 `already_running_quarantine` 判别联合。
后者必须有 `proof_kind=zero_physical_effect`、freeze/revoke 全 authority 和 cleanup evidence，之后才能 adoption。
无法完成 safe adoption 时返回 `agent_id + observation/adoption handle`，不得返回 execution incarnation 或 fully controlled。

## 7. Replacement、stop、recover 与恢复

P1→P2 替换固定为 reservation→stable replacement head→独立 add-P2 phase/start/authority receipt→prepared。
再独立完成 measurement→remove-P1 phase/kill/revoke/read-back receipt→actual-generation renewal ancestry→final replacement。
add/remove 两 phase 的 authorization、issuance、nonce、cleanup、receipt 和 operation id 不得复用。
stop 使用独立 revoke operation，必须先关闭 runtime-effect 和 active-process head，再 finalize identity binding/event。
recover 从任一 nonterminal phase 读取原 head，按 source-specific cleanup 或 same-family replacement 继续，不能 fresh genesis。
provider 无法证明 session epoch 连续、child identity 不复用或 receipt 可读时，旧 lease 立即 stale，停止新的 external effect。
仅查询原 operation/idempotency key；禁止 response unknown 后补发 token、重新 claim 或双重 spawn。

## 8. 字段消费链

PROBE attestation → capability repository → CTL connection/LeaseKey → bridge handshake/transport lane。
launch command → OUT claim + RESOURCE reservation + CONTENT refs + TRUST caller/effect fence → actual CLI boundary。
process-start receipt → ID incarnation prepared → measurement/active-process → binding preparation/finalization。
provider event/output → bounded content ingest → event owner/source cursor → node/report projection；这是 bridge 的输出消费链，bridge 不造第二 transcript SSOT。
provider receipt/ACK → OUT attempt/terminal/reconcile；tool surface 只读取 capability/probe projection，不能反向授予 control。
字段 mapper 必须保留 provider/session/child/turn discriminator、origin、epoch、generation、receipt kind 和 failure reason。
缺字段、unknown enum、foreign fence、duplicate event、cursor gap、跨 workspace/run 或 replay digest 冲突必须拒绝。

### 8.1 Relay dense allocator 与 source-sequence owner

provider relay 的 `source_sequence` 只能由 EventStore/relay 的单一 durable `SourceSequenceOwner` 为 `(provider_identity, session_epoch, lane)` 分配和提交；bridge、CLI callback、provider event id、内存计数器及重连连接都不得自分配或重编号。owner 持有 `next_sequence/materialized_sequence/dedup_floor/source_epoch`、envelope hash tombstone 和 writer/storage commit fence，且沿用 ID 的有界 rows/bytes/wall-time 事务政策。

输入是带完整 provider/session/child/turn discriminator、source epoch/lane/sequence（或由 owner 分配的 reservation）、canonical envelope hash、可选 provider event id、content ref、execution fence、bridge LeaseKey、operation id 和 source cursor 的 `RelayFrame`。输出只能是可按原 operation 查询的 `staged|materialized|replayed|quarantined|outcome_unknown` receipt，包含 pre/post source watermark、dedup decision、commit-fence digest 和 recovery-custody receipt；provider ACK 不得替代 relay receipt。

owner 先把高序帧保存为 staged/gap，不推进 materialized cursor；收到连续前缀时在同一有界事务内 drain。相同 `(source,epoch,lane,sequence)` 与相同 hash 只返回原 receipt/event，不新增行；同序异 hash、foreign/回退 epoch、低于 dedup floor 的异 hash、provider event id 与 canonical tuple 冲突立即 quarantine，原事件不可覆写。crash 位于 allocator、receipt、canonical event、watermark 或 cursor 任一边界时，恢复按原 tuple/operation 查询并从 durable checkpoint 重放，禁止 sequence hole、重编号、双 relay 或再次 external effect。无法证明 storage commit fence 或恢复域外 custody 时保持 `outcome_unknown`，不得 ACK 或释放唯一 spool。

## 9. 权限与运行安全

bridge principal 绑定 CTL LeaseKey、provider identity、connection/session epoch、audience、scope 和 expiry。
child 没有独立 authenticated connection 或签名 token 时只能 `controller_only`，不得自称 caller attribution/peer capable。
prompt/output/transcript/artifact 都是不可信内容；provider adapter 只能按官方 user/data role 传递 quoted envelope。
CLI 工作区必须真实解析并保持 containment；transcript 读取拒绝 symlink swap、TOCTOU、越界路径和无界分配。
所有 launch/fork/recover/respawn 先做 durable resource admission；预算不足、process tree 不明或 cleanup 超时都 deny-first。
凭据不进入 argv、日志、event、report、content metadata 或 attestation；错误只返回 typed code 和脱敏 digest。

## 10. 当前 standalone 证据与缺口

ST-05 证明真实 Codex app-server MCP `list_agents` 连通，不能证明 Codex native child bridge 或 ctl mutation。
ST-06 证明 Claude MCP 连接和工具调用；未证明官方 child control、SSE/reconnect 或发布环境。
ST-07 证明独立 binary 可启动 Codex/Claude managed task 并完成双轮，但未证明 provenance、孤儿清理或远程/Windows。
ST-08 证明同 daemon 的 Codex↔Claude direct-peer 双向本地路径；trusted scope 不是 bearer child attribution，完整 receipts 未闭合。
ST-13 有 Codex fork/resume/archive/interrupt 局部证据；Claude archive/interrupt 保持 typed unsupported。
以上均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不得作为四 provider full control 结论。
尚缺四套 version-pinned probe→spawn 同 runtime 证据、provider-originated adoption、caller/resource fence、transcript race 与 crash/recovery。
ST-09/11/14/16～18 的 delivery、unknown、Room、bounded cleanup、旧 adapter 和 stdio proxy 仍未闭合。

## 11. 具名验收与退出条件

BRIDGE-P01：四家分别通过 CLI identity、runtime closure、process-start receipt 和 same-runtime stateful fixture。
BRIDGE-P02：managed launch 明确落 `managed_agent`；native child 只有真实 child identity/lifecycle/control 全链才能登记。
BRIDGE-P03：每个 supported lane 都能回放 event/output/receipt cursor，并在 reconnect 后用新 LeaseKey 防旧写入。
BRIDGE-P04：P1→P2、stop、recover 和 provider adoption 的所有 phase 可查询、可中止、可清理且不双重 external effect。
BRIDGE-P05：caller attribution、resource admission、content/trust boundary 和 transcript path guard 均有正负 evidence。
BRIDGE-P06：每个 provider/session epoch/lane 只有一个 relay `SourceSequenceOwner`；连续 source sequence、storage commit fence、recovery custody 和 dedup tombstone 可在重启后逐字段重建。
BRIDGE-N01：SDK/App/API/TUI、共享模型、hook 名称或 root tool-call 冒充 native fully_controlled，必须零 lease。
BRIDGE-N02：foreign LeaseKey、旧 runtime、过期 probe、symlink swap、missing receipt、unknown outcome 或跨 kind 继承必须 fail closed。
BRIDGE-N03：provider child 先运行但没有 `zero_physical_effect` adoption proof，必须 quarantine/stop-reconcile，不得 bind current。
BRIDGE-N04：bridge 直接写 identity/outbox、绕过 CTL/RESOURCE/CONTENT/TRUST 或把 provider ACK 当完成，必须被 guard 拒绝。
BRIDGE-N05 `RelayHoleRejected`：seq=2 先到、seq=1 未到时只能 staged/gap，不能推进 materialized cursor、发布 output 或 ACK 连续交付。
BRIDGE-N06 `RelayDedupHashConflict`：同 tuple+同 hash 只返回原 receipt；同 tuple+异 hash、foreign/回退 epoch 或 provider-id 冲突进入 quarantine，绝不覆写原 event。
BRIDGE-N07 `RelayCrashBeforeCommit`：allocator/receipt/event/watermark 任一 crash window 只能保留完整 durable record 或全部回滚；恢复不产生 hole、重编号或第二 relay。
BRIDGE-N08 `RelayReplayAfterRestart`：重启/重连按原 source tuple、operation 和 commit fence 查询，合法 replay 幂等，未知结果保持 `outcome_unknown` 且不触发第二 external effect。

Relay dense allocator 的具名 RED/GREEN fixture（必须真实到达 SourceSequenceOwner，不能只测 DTO/check constraint）：

- RED-BRIDGE-01 `RelayHoleCannotAdvance`：seq=2 先到只能 staged/gap，materialized cursor、下游 output 和 custody ACK 均不前进。
- RED-BRIDGE-02 `RelayDuplicateCanonicalConflict`：同 tuple+异 hash、foreign/回退 epoch 或 provider-id 冲突返回 typed quarantine，原 receipt/event 不变。
- RED-BRIDGE-03 `RelayCrashRollbackBeforeCommit`：allocator、receipt、event、watermark 任一暂停/杀死窗口都不能留下半提交或 sequence hole；若 COMMIT 结果不明只允许原 operation query。
- RED-BRIDGE-04 `RelayReplayCannotRedeliver`：重启后重复 frame 不新增 event/receipt，不重新调用 provider，旧 generation frame 只能审计。
- GREEN-BRIDGE-01 `RelayDenseAllocatorSingleOwner`：四 provider 在各 source epoch/lane 由唯一 durable owner 分配连续 sequence，bounded transaction、commit fence 和 recovery custody 与 cursor 同批落盘。
- GREEN-BRIDGE-02 `RelayCrashReplayAndDedup`：覆盖 seq=2→seq=1 drain、同 hash replay、dedup floor tombstone、进程重启与合法 reconnect，最终 receipt/cursor/hash 与首次提交逐字一致。
退出条件：四 profile 的 registry、fixture、lease、状态机、field chain、recovery 和 signed evidence 全部可审评。
任一 provider 缺少强证明则保留 observed/unsupported；本草案继续 Draft，TOOLS 只可读取其 per-provider join 结果。
