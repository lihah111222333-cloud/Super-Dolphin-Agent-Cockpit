# Orch 独立单实例升级与 E2E 执行计划

Status: Active

负责人：当前 Codex 根任务；用户负责产品目标与架构选择。

创建日期：2026-08-31

关联设计：[统一原生 Subagent 控制面总规格](../../superpowers/specs/2026-07-11-unified-native-subagent-control-plane-design.md)、[独立单实例部署设计](../proposals/2026-08-31-orch-singleton-runtime.md)。

2026-09-01：用户批准继续执行剩余升级，已启动 [Gate S0 工作包](orch-subagent-upgrade/README.md)。该工作包建立十四份子规格草案、typed dependency DAG、历史 blocker registry 与离线一致性守卫；它不改变 ST-01～ST-18 的验收状态，也不把子规格草案当已批准运行协议。

S0 首批结构已落盘：十四份 Draft 子规格、28 节点/63 边规划图、B01～B18 历史台账。[验证记录](orch-subagent-upgrade/verification-notes.md)已更新本轮可复现的本地测试与静态证据，但所有结果仍标记为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不能继承或替代发布/远程验收。ST-11/B08风险与后续切片不关闭总计划。

## 目标与完成条件

完整目标：升级 orch 子 agent 系统，完成 E2E；orch 脱离 V3 主程序，作为单实例第三方编排服务组合接入 Claude Code 与 Codex CLI，保留跨任务对话能力。V3 是可选客户端，不是启动、执行、存储或授权前置依赖。单实例约束是同一规范化配置/数据目录唯一 owner；实现没有显式逻辑 agent-count cap，但实际并行仍受连接、session、工具和 provider busy 限制。

单实例专项验收还须覆盖：不同配置文件指向同一状态目录、路径别名指向同一目录时仍互斥；不同隔离状态目录可以独立运行；两个客户端共用一个业务 owner 且断开一个不停止任务；客户端重新连接与 daemon 崩溃接管不能混同于任务恢复。跨任务授权不因共用 daemon 或 Room 而扩大。详见部署设计的“单实例的准确边界”。

开始执行的授权不等于既有 NO-GO 门已经通过。先推进子规格、前置缺陷及测试；新能力必须取得其适用祖先规格与实际运行证据后才能启用。完整目标不能用“能 initialize/list”“mock provider 回答”“仅 Codex 成功”替代。

## 当前事实与工作区

下文各“轮次进展”保留当时的本机观测，属于历史记录；其中出现的 wrapper/full-archtest
耗时或 PASS 不自动代表当前工作树状态。当前稳定快照以文末“额度恢复续作”及
[验证记录](orch-subagent-upgrade/verification-notes.md)为准，仍受本地非权威和发布 NO-GO 边界约束。

- 基线：`d95c63798`；工作树：`.worktrees/20260831-orch-subagent-upgrade`；分支：`codex/20260831-orch-subagent-upgrade`。
- 主工作区上一轮总规格修改原样保留，已复制到隔离工作树作为输入；不自动合并、提交或推送。
- legacy main/sidecar 入口仍要求 V3 parent runtime env；本轮新增的 `serve --config --ready-file` standalone 入口不读取该环境，使用显式配置、嵌入迁移、单实例锁和同一 orchestration service。
- standalone 已不再依赖项目资源目录来启动数据库和模型 registry；独立 HTTP 是本地 bearer profile，仍不能宣称满足 strict Streamable HTTP 全量合同（HTTP SSE replay/连接事件恢复尚未完成；direct-peer outbox 是独立的最小闭环）。
- 真实 Claude CLI、Codex CLI 客户端连接和受管 provider task 均已有本地 wrapper 通过证据；同域 Codex↔Claude 双向 direct-peer 第二 turn 也已通过，但证据等级仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- Codex 执行权限现由 standalone 配置显式声明并传入 `thread/start`：`workspace-write` 用于 coding-agent 修改，`read-only` 用于审阅/fixture，`danger-full-access` 仅允许显式高风险选择；缺失、非法或 Claude 携带该字段均 fail-fast，不再静默固定为只读。
- 当前会话没有 MCP LSP 工具，已使用本机 `gopls check` 对新增 standalone 原语、工具 mapper/registry、嵌入迁移、direct-peer 及其测试检查（均 exit 0 无诊断）；未覆盖部分不能据此标 PASS。

## 交付顺序

1. 修正总规格 §10.5 中的 delivery/receipt/grant/Room/A2A 冲突，将独立单实例部署纳入 Gate S0；建立十四份子规格 backlog、机器 DAG、B01–B18 registry 与验收追踪。
2. 打通独立 composition root：严格配置、单实例内核锁、嵌入同源迁移/必需资源、日志、ready metadata、信号关闭；不导入 V3 app/UI 或伪造 ctl host。
3. 统一身份、持久事件、content/outbox/receipt、授权及资源 owner；直接管理官方 CLI machine protocol，完成 launch/fork/send/interrupt/stop/recover。
4. 实现单实例多会话 MCP：独立客户端请求 ID、身份、取消、权限；事件读取、等待、跨任务对话与 Room；stdio 如提供只代理连接，不拥有数据库或执行器。
5. 用隔离配置运行 Claude Code 和 Codex CLI 的接入 E2E；单独区分真实 CLI discovery、真实模型执行、deterministic fault fixture。
6. 按下表及总规格全量要求审计完成证据；未运行、失败、依赖缺失均保留未完成，不能由普通单测通过自动关闭。

## 必需 E2E 追踪

| ID | 场景与验收 | 当前证据状态 |
| --- | --- | --- |
| ST-01 | 在非仓库 cwd、无 V3 环境/进程/资源目录下启动真实 orch binary，迁移私有状态并完成 MCP 初始化 | 部分完成：`TestMCPOrchServeBlackBoxSingleInstance` 已通过真实 binary/HTTP 初始化/迁移；仍需发布制品和远程环境证据 |
| ST-02 | 同数据目录两个并发启动只有一个 owner；存活期间拒绝第二实例，崩溃后允许接管；锁文件不删除重建；不同隔离状态目录可并行独立运行 | 部分完成：`TestMCPOrchServeRejectsSecondInstance` 已在真实 binary 下覆盖原路径、另一配置文件和符号链接别名指向同一状态目录，第二 owner 均拒绝且第一实例继续可用；`TestMCPOrchServeReclaimsAfterOwnerKilled` 已验证 owner 被强制杀掉后第二 binary 可接管并重新提供 MCP；新增 standalone deterministic `TestInstanceLockIndependentDirectoriesCanAcquireInParallel`、锁文件删除/替换回归覆盖内核锁边界（ordinary/race PASS），但不同隔离目录的真实 serve binary 并发、原生 Windows 删除/替换和发布文件系统仍未验证 |
| ST-03 | 两个独立客户端共享同一 daemon/任务事件；相同 request ID 互不干扰；断开或 DELETE 一个 session 不停止他人任务 | 部分完成：真实 binary 已验证双 session、两个 session 复用相同 request ID 仍分别得到正确响应、删除 A 后 A=404 且 B 仍可调用；运行中受管任务断线存活仍未验证 |
| ST-04 | 未知参数、坏配置、空凭据、宽权限、错误 manifest/schema 失败且无 agent 副作用 | 部分完成：`TestMCPOrchServeRejectsInvalidCLIAndConfig` 已通过未知参数、缺失/相对/损坏配置的真实 binary fail-fast；空凭据、权限、manifest/schema 副作用审计仍未完成 |
| ST-05 | 官方 Codex CLI 作为 MCP 客户端实际连接服务并调用工具 | 已通过真实 `codex app-server` MCP `list_agents` 调用；模型 turn 和发布环境仍未纳入本地证据 |
| ST-06 | 官方 Claude Code 作为 MCP 客户端实际连接同一服务并调用工具 | 已通过本地真实 `claude mcp add/get` 连接状态检查，以及 print/stream-json 模型会话实际调用 `mcp__orch__list_agents`；发布环境和更广工具矩阵仍未验证 |
| ST-07 | 独立服务直接启动官方 CLI task，取得真实输出并执行后续 turn；启动环境与命令行不带 V3 runtime | 部分完成：`TestStandaloneBinaryLaunchesManagedClaudeThroughMCP` 与 `TestStandaloneCodexClientLaunchesManagedCodexThroughMCP` 已分别通过真实 standalone binary、HTTP MCP/官方 Codex app-server MCP、`launch_agent`、首轮报告和同一客户端第二轮 `send_message(wait_report=true)`；测试 harness 提供剥离 V3 变量的环境、检查命令行不含禁用 runtime token，并做一次有界后代快照，不读取进程环境，也不构成正式孤儿清理证明。官方 binary provenance、发布制品和 Windows/远程环境证据仍未完成 |
| ST-08 | 两个独立 root task 双向消息、同域跨 provider 协作；完整 receipt/事件链 | 部分完成：唯一 identity/event resolver/producer 窄口已进入 Fx 构造图，但没有transport auth binder、真实 runtime event producer 或`unified_v2` public list/wait/send。既有direct-peer/legacy send缺CONTENT/RESOURCE/custody/OUT chain，不能升级为canonical cross-task能力；本机结果仍为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` |
| ST-09 | delivery 全联合、busy 策略、单目标/多目标 any/all/quorum/first_success 与取消 | 部分完成：内部 `cmd/mcp-orch/orchestration/waitaggregate` 已实现固定最多32目标的 any/all/quorum/first_success、typed predicate、timeout/indeterminate 区分及兄弟 context 取消；尚未接 durable `wait_agents` selector/fence、HTTP/stdio watcher、delivery/busy 全联合或 public manifest，不能关闭 ST-09 |
| ST-10 | discover/read/send 独立授权、伪造身份/跨 run/过期 fence 拒绝、共同 Room 不授予私有历史访问 | 部分完成：CallerAttribution、task-only HTTP session认证、内存credential owner、TaskPrincipal resolver与固定incarnation event source候选可逐字段重验principal/reservation/incarnation/current binding；但第二task listener、managed CLI注入与identity/Fx激活链尚未实现，local operator仍不是child/task principal。public discover/read/send、CONTENT/OUT约束和Room私有历史隔离未完成 |
| ST-11 | 同 key 重试/丢响应不重复创建命令；provider outcome unknown 不猜测或自动重发 | 待实现/验证：不可变intent、send profile/lifecycle、CONTENT-1、RESOURCE-1及[local SQLite authority/kernel lease/HTTP transport candidates](orch-subagent-upgrade/recovery-custody-sqlite-authority.md)均未完整装配。typed observation、revision-3（含external-service mode）DB writer_epoch/takeover与同key reconcile候选已落地；production仍是custody unavailable/reject-all，独立服务/外部EvidenceFinalizer/anchor/签名、durable coordinator job/journal/startup scan、OUT accepted/attempt/permit/nonce/cleanup及orch client/Fx/runtime仍缺失 |
| ST-12 | 杀掉并重启 daemon，恢复任务身份/消息/事件 cursor；旧 incarnation follow 不跳新任务 | 部分完成：`TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss` 已通过真实首轮、受控执行器句柄丢失、第二个 standalone Fx app 的 `thread/resume` 和第二轮；migration 130 的 durable `EventWaitHandle` 设计了重启后以同一 `PollRequest` 继续、并在当前 binding fence 失配时拒绝，identity/event resolver/producer owner 也已进入 Fx 构造图，但尚未接入独立 serve 的 SIGKILL/锁接管或 canonical event producer 调用。该测试不是硬崩溃接管证据；跨 incarnation fence、canonical event custody/cursor 恢复和 provider receipt 仍未验收，已有本机证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` |
| ST-13 | fork、interrupt、stop、recover 的真实行为与进程/存储清理；stop 与 interrupt 不混同 | 部分完成：Codex `thread/fork`、child 持久 parent 关系、child 后续真实 turn、Codex resume、真实 `thread/archive` 和真实 `turn/interrupt` 已通过；Claude 归档未支持时 `stop_agent` 的真实 Stop fallback 也已通过且不声明 archived。Claude interrupt 明确返回 typed capability error，真实 provider recover、硬崩溃清理与完整 receipts 仍待验证 |
| ST-14 | Room broadcast/round 的逐目标 receipt、撤权、partial、关闭和恢复 | 待实现/验证：当前仅有 concrete `DisabledRoomCapabilityGate` 的 fail-closed 接缝，Room tools/mutation 未注册；ROOM/CONTENT/RESOURCE/OUT/TRUST、canonical room event bridge 和 drain/recovery 仍未完成 |
| ST-15 | 不支持的 capability 返回 typed error；fake/mock、不可用凭据或缺 CLI 不能写成真实执行 PASS | 部分完成：Claude 的 archive/interrupt、非 Codex 的 fork/resume 现在返回 `CapabilityError`（同时保留 `ErrExecutionNotReady`），typed MCP classifier 已经把它们稳定映射为 `capability_unsupported`；fake/mock、不可用凭据或缺 CLI 的真实执行证据门仍待补齐 |
| ST-16 | 有界连接/消息/等待/内容、负载下 interrupt/cancel 可达、退出无孤儿进程 | 待实现/验证 |
| ST-17 | 旧 V3 adapter 回归，同工具业务 owner；不存在 desktop-only 特权旁路 | 待实现/验证 |
| ST-18 | 若提供 stdio connect，代理不落库/迁移/执行；daemon 不可达时明确失败，不自动另起 core | 待实现/验证 |

## 验证纪律与退出条件

Claude 受管执行 readiness RED→GREEN：前三次在 CPU 96.0%/84.3%/96.0% 的入口门拒绝，均未发模型请求。源码检查发现 launch 后尚无 `system/init` 能力证据；扩展 claudewire Event 的 tools/mcp_servers，并增加根 `system/init` fence：foreign init 不激活、init 前 result 进入一槽 pending buffer、根 init 后只回放一次，冲突结果 fail-closed。最终 exec session 61206：前置 archtest 211.315s，真实 Claude provider 业务测试 13.617s，包装 exit 0，CPU 前/中/后 34.1%/50.7%/40.2% 放行。实际覆盖 system/init、thread/binding 持久化、模型 turn、success terminal 和清理；证据仍为 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED。

真实受管 provider 测试推进：新增 `TestStandaloneManagedCodexTurn` 与 `TestStandaloneManagedClaudeTurn`，要求显式 CLI/home/model，直接经过 standalone.Execution 启动 provider、原子检查 thread/binding、提交一次无工具短 turn 并等待 canonical terminal；缺输入不跳过。Codex 首次业务执行实际完成 9.925s，thread、持久绑定和 success terminal 均成立，但模型近似复述测试标记导致断言失败、包装 exit 1；已把基础设施断言改为 success+非空正文，不把随机模型措辞当协议。最终重跑 exec session 25176：前置 archtest 219.706s，真实 Codex provider 业务测试 13.955s，包装 exit 0，CPU 前/中/后 73.3%/77.1%/48.6% 放行。两者都读取显式 provider home 的现有认证，但不复制或输出凭据。

跨任务能力事实审计（direct-peer 落地前）：当时只有同一 orchestration registry 已知 agent 的 follow-up `send_message`，没有持久 discover/read/send 独立授权、event cursor 或 idempotency receipt。本轮已补上 direct-peer 的最小授权、持久消息、submitted/delivery_failed receipt 和 exclusive cursor，并取得双向 Codex↔Claude 真实 E2E；跨 run/incarnation fence、Room、完整事件 outbox 仍未完成，不能把该最小闭环扩写成完整跨任务协议。

Claude 官方客户端连接 GREEN：`TestStandaloneOfficialClaudeConnection` 使用隔离 CLAUDE_CONFIG_DIR，以真实 `claude mcp add/get` 连接带 bearer 的同一独立 orch；活体时必须显示 Connected，停止 daemon 后必须显示 Failed to connect，排除纯配置缓存假阳性。exec session 80426：前置 archtest 123.643s，业务测试 1.610s，包装 exit 0，CPU 前/中/后 8.8%/12.6%/12.6% 放行。证明真实 Claude CLI 接入和鉴权，不证明工具实际调用或模型/受管任务执行。证据仍为 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED。

Codex 接入 RED→GREEN：`TestStandaloneOfficialCodexClient` 初次本体执行 0.986s，唯一失败为 daemon cleanup `signal: killed`，包装 exit 1。原因是测试 defer cancel（以及 t.Context 在 Cleanup 前取消）先于 daemon 清理杀死进程。改独立超时 context 并将 cancel 最先注册到 Cleanup，保证客户端和 daemon 先清理。重跑 exec session 53869 完成：前置 archtest 171.530s，真实 Codex CLI 接入测试 0.863s，包装 exit 0，CPU 前/中/后 28.7%/19.2%/24.2% 放行。证明隔离配置下真实 Codex app-server 可连接独立 orch 并调用 list_agents，退出清理成功；没有模型 turn，不证明受管任务执行或 Claude 接入。证据仍为 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED。

官方 CLI 接入测试推进：新增 `TestStandaloneOfficialCodexClient`，显式指定 `ORCH_E2E_CODEX_BIN`，隔离 CODEX_HOME，以官方 app-server 的 thread/start 与 mcpServer/tool/call 调用同一真实 orch daemon 的 list_agents；不发模型 turn，不复制用户凭据，缺 CLI 不跳过。参数依据官方 MCP/app-server 文档与本机 generate-json-schema 输出，精确源码守卫、复杂度和差异检查通过。实际运行被 CPU 82.2% 入口门拒绝，尚无官方 CLI 接入通过证据。同期 `TestMCPOrchServeRejectsSecondInstance` 的前置 archtest 204.158s 通过，但中间 CPU 97.6% 拒绝，业务测试未执行，包装 exit 2。

真实 binary RED→GREEN：`TestMCPOrchServeBlackBoxSingleInstance` 先以 0.765s 失败、包装 exit 1，服务在 ready 发布前拒绝测试夹具的 0755 目录。DAG runners 已启动并在该错误后退出，非 V3 parent-env 失败。修正测试 ready 父目录为 owner-only 0700，保留生产权限拒绝规则。重跑 exec session 13844 已完成：前置 archtest 214.557s，真实 binary E2E 0.666s，包装 exit 0，CPU 前/中/后 25.2%/34.2%/20.7% 放行。覆盖隔离 cwd/无 V3 env 启动、两个 MCP session、工具调用、删除一个会话后的隔离与 daemon 正常关闭；不覆盖第二 owner 竞争专项、官方 CLI 或模型执行。仍是 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED 本地开发证据。

独立启动首次运行通过：`TestStandaloneGraphStartsWithoutHost` 包装命令 exit 0，前置 archtest 150.218s，业务测试 0.080s，CPU 前/中/后 31.8%/35.0%/36.2% 均放行。实际覆盖生产 Fx 图、嵌入迁移、DAG 依赖装配、ready、真实 TCP MCP initialize 及停止清理；不覆盖官方 CLI 模型执行，亦不等于第三方发行 binary 验收。证据等级仍为 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED。后续真实 binary `TestMCPOrchServeBlackBoxSingleInstance` 已在第十轮通过。

启动取消链修订：每个 CLI session 持有本次启动的 cancel，Stop/Close 标记停止后在锁外取消正在进行的握手和持久化，再等待 launch 结束；先停止后绑定也立即取消。新增 `TestCLILaunchCancellationOwnership`。编译、精确源码守卫、复杂度及差异检查通过。启动测试被 CPU 92.5% 入口拒绝；取消链测试 exec session 4816 已完成：前置 archtest 193.403s、测试本体 0.019s、包装命令 exit 0，前/中/后 CPU 68.3%/67.6%/69.9% 均放行。脚本仍标记 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED，仅是本地精确回归通过，不替代发布证明。此修改不能证明 provider 管道写入、回调阻塞和强杀等待全部有界。

Claude 归档语义修订：移除把 Stop 当作 Archive 成功的实现，未接通真实归档时返回明确能力错误且不停止当前任务。新增 `TestClaudeArchiveDoesNotImpersonateStop`。编译、精确源码守卫与差异检查通过；启动和此回归分别被 CPU 99.9%/100.0% 入口门拒绝（exit 2），测试本体未执行。真实归档仍为未完成项，不能把拒绝行为当成功能交付。

多客户端 E2E 补强：真实 binary 测试在两个 session 调用工具后 DELETE 会话 A，要求 A 后续 ping 返回 404、B 仍能 list_agents。源码守卫、复杂度与差异检查通过；本轮 E2E 尝试在 CPU 99.7% 的入口门拒绝，测试本体未执行。该断言证明范围仅为连接会话隔离，不代替运行中受管 CLI 任务在客户端断线后的存活及恢复证据。

CLI 工作目录修订：launchProfile 由词法包含关系改为解析工作区和 cwd 的真实路径，拒绝已有符号链接逃逸、不存在路径及非目录；同一工作区的合法别名仍允许。新增 Unix `TestCLIWorkspaceResolvesAliases` 通过实际执行器入口验证。该启动前检查不是 OS sandbox，也不证明消除了并发恶意目录替换。编译、精确源码守卫、复杂度和差异检查通过；启动及路径测试均被 CPU 100.0% 入口门拒绝（exit 2），业务测试未执行。未收到停止其他工作树 gopls 的授权，没有停止该进程。

单实例 E2E 补强（早期尝试）：`TestMCPOrchServeRejectsSecondInstance` 同时覆盖原配置与另一份配置指向同一状态目录，第二实例必须拒绝且不发布 ready；该增强当时曾因 CPU 入口门未运行，后续已在 medium wrapper 中通过并记录于第十轮。只读诊断期间未擅自停止其他工作树的 gopls 进程或绕过资源门。

CLI 结果真实性修订：Codex 会话发生输出超限等已记录错误后，不再追加正文；随后的 provider completed 被投影为失败，不能把部分正文当成功摘要。新增 `TestCLIOutputOverflowCannotReportSuccess`。编译、精确源码守卫、复杂度和差异检查通过；启动测试与超限回归分别被 CPU 99.2%/100.0% 入口门拒绝，测试本体未执行。完整输出持久化与可恢复事件流仍未实现，该错误传播修订不替代这些交付要求。

会话握手屏障修订：独立 profile 的每个 HTTP session 分别记录 initialized；工具操作必须在合法 `notifications/initialized` 通知之后，携带请求 ID 的 initialized 拒绝，重复合法通知幂等，关闭会话不复活。legacy dispatch 不变，独立 profile 不把旧 shutdown/exit 方法冒充标准 MCP 方法。新增 `TestLocalSessionInitializationBarrier` 覆盖实际 dispatcher 的提前调用、非法通知、重复通知、两会话隔离和关闭后拒绝。编译、精确源码守卫、复杂度和差异检查通过；启动和屏障测试分别被 CPU 100.0%/99.2% 资源门拒绝，测试本体未执行。字段状态读写在 session mutex 内，但 race 与字段守卫 fail-first 尚无运行证据。

初始化输入修订：独立 profile 在分配 session 前要求 capabilities/clientInfo 为对象、clientInfo.name/version 为非空字符串、可选 _meta 为对象；客户端名称不作为授权身份。新增 `TestLocalInitializeRejectsIncompleteIdentity`，保留旧 HTTP 行为。编译、精确源码守卫、复杂度和差异检查通过；启动测试和新增负例分别被 CPU 99.8%/99.3% 的资源入口拒绝（exit 2，测试本体未执行）。此处仅覆盖必填字段形状，尚未完成 capabilities 深层 schema、重复键和 initialized 状态屏障，不能据此宣称完整协议验证或字段守卫闭环。

版本协商修订：独立 HTTP profile 不再因客户端提出其他格式安全的协议版本而直接拒绝 initialize，而是明确回复本 profile 唯一支持的 `2025-11-25`，后续请求仍必须携带协商后的版本。旧 HTTP 的既有多版本协商保持不变；未知版本不被假称为受支持。依据 [MCP 生命周期规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle) 的版本协商要求，新增 `TestLocalHTTPNegotiatesSupportedVersion` 覆盖当前、较早和未来请求版本。源码守卫、复杂度和差异检查通过；初始化必填 clientInfo/capabilities 深校验与 initialized 屏障仍未完成。

启动测试证据补强：`TestStandaloneGraphStartsWithoutHost` 不再在 `app.Start` 后立即停止，而是等待真实 runner 的 ready，实际 TCP 调用 MCP initialize，核对 session header、协议版本、响应 ID 和 server name，停止后检查 ready 清理；失败断言亦注册 app.Stop 清理。精确源码守卫、复杂度和差异检查通过。修改前后两次运行分别被 CPU 98.2%/99.2% 的入口资源门拒绝（exit 2），尚无测试本体通过证据。

DAG 装配修订：发现 standalone 已注册 DAG 工具但未装配 dispatcher/scheduler/node executor。现将原执行配置中的节点构造拆为 `orchestrationNodeExecutionOptions`，由旧 sidecar 和 standalone 共用；standalone 加入 `orchestrationDAGOptions`，仍只使用显式 CLI launcher，不引入宿主 RPC。新增 `TestStandaloneGraphIncludesDAGExecution` 静态接线回归；它不是业务执行证据，后续仍需真实图启动及 DAG 推进 E2E。编译、精确源码守卫、复杂度和差异检查通过；启动测试两次分别被 CPU 96.2%/99.7% 资源入口拒绝，测试本体均未执行。shell command 的既有授权 fail-closed 不因该装配修改而放宽。

CLI 清理修订：Claude `exec.Cmd.Wait` 已关闭管道后，重复清理只对 `os.ErrClosed` 视为幂等，其余 I/O 错误保留。新增 `TestClaudeProcessCloseAfterWait` 与 `TestClaudePipeClosePreservesFailures`；源码守卫、编译、差异检查通过。前者尝试运行时被 CPU 96.8% 的资源入口拒绝（exit 2），后者尚未运行；不能把该修改记录为测试通过。同期独立启动测试在 CPU 100.0% 时被拒绝，业务测试本体未执行。

最新启动生命周期修订：数据库由 `serve` 根入口显式打开、迁移和持有，Fx 图只消费该句柄；初始化失败关闭数据库，正常退出在 app.Stop 后关库，实例锁最后释放。新增 `TestServeMigrationFailureReleasesResources`，验证损坏数据库连续启动失败后可重新取得实例锁、不发布 ready，并在原生 Windows 上用删除文件检查未遗留打开句柄。该测试尚未运行，不作为修复通过证据。

本轮精确源码守卫通过；`TestStandaloneGraphStartsWithoutHost` 在 CPU 90.0%、空闲内存 34.0% 时被资源门拒绝（exit 2），测试本体未运行。诊断确认高占用 gopls PID 8346 的 cwd 属于另一个 wjboot-v2 工作树，没有停止该进程。脚本中的“stage or clean”是资源拒绝后的候选指导，不应误报为本次测试的独立前置要求；没有为此暂存用户改动或绕过资源门。

本地精确测试经 `scripts/test_with_guard.sh --host-test light|medium`；共享工作树编辑完成后再运行，避免 SSA 加载半成品。测试脚本的资源、代码守卫与最终权威验证要求不绕过。测试本身失败、守卫失败和环境不可用分开记录。不设置伪造的 local-authority/remote receipt 环境绕过门禁。

退出条件：上表与总规格适用要求逐项有与实际产物绑定的证据，真实两 CLI 客户端和独立执行闭环均通过；发布协议、权限与回滚门完成，第三方接入说明可复现。原目标未达到之前，本计划不归档、不将 goal 标 complete。

## 2026-08-31 首轮执行记录（未完成）

已编写的前置代码：

- `cmd/mcp-orch/standalone`：严格 JSON 配置和按数据目录的内核单实例锁。配置解码只证明声明形状，credential 内容、CLI identity/probe 与实际启动仍未接通。
- `internal/platform/db/sqlite/RunEmbeddedMigrations`：从同一规范 SQL 源 embed，复用 FS 注入的既有迁移算法；尚未被 standalone composition root 消费。
- `cmd/mcp-orch/runtime.go`、`tools/registry.go`：现有 outputSchema wire mapper 与 duplicate-name fail-fast；仅 B14 的一部分，不代表完整 generated manifest/hash 门已关闭。
- `cmd/mcp-orch/standalone_daemon_e2e_test.go`：真实 build/exec、无 V3 env 的 daemon 启动、两个 HTTP session、重复实例拒绝和配置负例。当前只是黑盒启动测试，不是完整业务 E2E；provider 路径 fixture 不执行模型，不计为真实 CLI 证据。

实测记录（均为本地开发证据，脚本标记 `LOCAL_NON_AUTHORITATIVE`，不代替发布门）：

| 检查 | 实际结果 | 证明边界 |
| --- | --- | --- |
| `TestInstanceLockContract` | exit 0，测试包 0.009s | 修改前基础互斥/崩溃释放；不能覆盖随后发现的锁文件替换问题 |
| `TestInstanceLockFileReplacementDoesNotCreateSecondOwner` | RED：`replacing a lock file created a second owner: <nil>`，exit 1；修改后 GREEN：wrapper exit 0，测试包 0.007s | 真实文件替换绕过旧 inode 锁已复现并在 macOS 回归通过；Unix 改为目录 inode 锁，Windows 改为禁止 delete-share 句柄但仍待原生验证 |
| `TestConfigStrictContract` | 测试本体 `ok` 0.008s；后置 CPU 91.1% 资源门拒绝，wrapper exit 2 | 不记为整次 gate PASS；需低负载重跑 |
| `TestMapRegistryToolDefinitionOneHotWireFieldCoverage` | wrapper exit 0，测试包 0.016s | 当前 wire producer→mapper 逐字段变更验证；尚需 mapper 删除 fail-first 和其余回归 |
| `TestRunEmbeddedMigrationsFreshDatabaseIsIdempotent` | wrapper exit 0，测试包 0.059s | fresh SQLite 的同源嵌入迁移与重复运行；legacy120/集合一致性用例尚未跑 |
| `GOOS=windows GOARCH=amd64 go build -p=1 ./cmd/mcp-orch/standalone` | exit 0 | 仅交叉编译；不证明 Windows 锁、删除共享或运行时 E2E |
| 本机 `gopls check` | 已检查的新原语、mapper、迁移及测试 exit 0 无诊断 | 无 MCP LSP；未检查的 daemon/provider 链不算通过 |
| `TestMCPOrchServeBlackBoxSingleInstance` | 真实 binary RED：`parent launch contract missing: sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`，wrapper exit 1，测试包 0.727s | 已证明脱离 V3 的独立启动缺口；不是 mock、编译失败或隐藏 skip。`serve` 尚未接入 |

审查裁决：采纳锁文件替换、跨任务 receipt/delivery/grant 冲突与 pipeline 未接通发现；“`instance_windows.go` 缺显式 build tag 导致非 Windows 重复编译”不成立，`_windows.go` 文件名本身就是 Go 平台约束。Windows `root.Name()` 重开路径需保留 root/reference 文件身份比对；目录整体替换、旧 binary rollout、原生 Windows删除/rename负测仍须后续验证。

下一步：接入 singleton `serve` composition root、正式 HTTP profile 与直接官方 CLI 执行器；补齐配置凭据/路径安全，重跑后置资源门失败的配置测试、锁全部回归、migration legacy120/集合一致性、duplicate-name 与 mapper fail-first。真实 binary 的 startup RED 已取得，不通过设置 V3 parent env 或隐藏失败来变绿。单实例锁、配置及嵌入迁移尚无 daemon 生产消费者，不能把前置单测通过表述为独立服务完成。

## 第二轮进展（仍未完成）

- 新增显式 `serve --config --ready-file` 入口，参数不再进入 V3 parent runtime 解析；未知/重复参数和相对路径拒绝。
- `fx_standalone.go` 将配置、内核锁、私有 credential、内嵌 migration、同一业务 registry/service、HTTP runner 与信号退出接通；没有注入宿主 RPC 地址或启动 stdio 第二核心。
- `common.NewLocalHTTPServer` 是显式本机 operator profile，不是 strict `unified_v2`。它复用现有 dispatcher/session/result 代码，增加 bearer 必填、Origin/标量 header 校验、固定版本、Accept q 值、body/并发上限和服务端 scope。GET SSE 当前明确返回 405，不声称 SSE replay 已完成；原 legacy profile 保持原行为。
- 新增 `TestLocalHTTPProfileContract`、`TestStandaloneBootstrapFiles`、`TestServeOptionsContract`。Ready 文件在完整写入后用 hard-link 原子发布且不覆盖既有文件；凭据内容不进入 ready/log。
- 历史候选快照曾通过：更新后的 `go build ./cmd/mcp-orch`、相关 `gopls check`、`test_with_guard.sh --light-guard-only`（生产/测试/priority SSA 全通过）与 `git diff --check`；当前 wrapper 结果须以最新工作树为准，且仍可能被未提交冻结基线 drift 阻断。
- 尚未验证：本轮多次尝试正式 `TestMCPOrchServeBlackBoxSingleInstance`，均在入口 CPU 约 99%～100% 时被宿主资源门拒绝，exit 2，测试本体未执行。不得把旧 RED 改成 GREEN，也不通过裸跑或伪造 local-authority 绕过资源门。
- 重要未完成项：`standalone.Execution` 目前是明确拒绝的能力门，不会递归执行 orch 自身、生成假 turn 或回退 V3；真正 Claude/Codex CLI 执行器必须继续实现。当前 serve 源码接线不等于整个独立服务功能完成。新 profile 的完整初始化屏障、请求断线语义、权限分离、输出 schema 验证、资源 reserve、DAG runners 和真实 CLI 任务闭环仍待补齐/验证。
- 下一步：资源允许后先运行本轮 startup E2E 与 profile/config/ready 测试，按实际错误收敛装配；然后实现直接 CLI backend 和持久事件/消息/等待链，继续完成 ST-01～ST-18，不缩小完成标准。

## 第三轮进展（仍未完成）

- Codex 分支已从全量拒绝的 Execution 门推进到实际 `codex app-server --listen stdio://` 代码路径：有界 JSONL↔jrpc2 适配、initialize/initialized、thread/start、turn/start、turn/interrupt、thread/archive、进程组关闭与等待。
- `cliLauncher` 绑定既有编排终态 sink，沿用已有 early-terminal fence 路径；新增了提交中的互斥标记和早到 completion 标记，避免 turn/start 响应到达后重新标 busy。
- 新增 `TestCodexJSONLChannelContract`、`TestCodexJSONLRejectsInvalidFrames`、`TestCLIExecutionInputAndTerminalContract`。这些测试尚未执行，不计为真实 CLI 或模型通过证据。
- 编译、相关 `gopls check`、light code guard 和 `git diff --check` 通过。正式 E2E 再次在 CPU 约 99.7%～99.8% 时被资源门拒绝（exit 2，测试本体未运行），没有绕过门禁。
- 仍需修复/完成（第三轮当时）：真实 CLI 资格探测与 executable identity、持久命令/事件/content/outbox、完整输出保存、source/incarnation fencing、退出/启动竞态、provider 回调处理、Claude 分支、fork/recovery、完整输入字段守卫和 sandbox/权限配置。当时 Codex 路径固定 read-only，不能作为最终可写 coding-agent 能力交付；其他未支持输入明确拒绝。不得以此宣布 ST-05～ST-18 完成。
- 当前下一步是验证并修复实际 `serve` 装配及 Codex 任务闭环，再推进 Claude 与跨任务持久协作。新增代码不改变 NO-GO/未验证结论；用户完整目标保持 active。

## 第四轮进展（仍未完成）

- 针对执行器 shutdown/launch 竞争增加 launch wait group 和 stopped fence：停止发生在进程创建或 thread/start 途中时，后续激活拒绝；执行器关闭等待已登记的启动操作收口。
- 将 turn 提交的 busy/reset 放入单一锁区，保留上一 completed turn 的去重标记。通知现在检查 thread/turn，不允许旧 turn 清空新 turn 的 busy 状态或追加输出；重复完成通知不重复投递。
- provider 自发中断和本地 interrupt request 分开构造 canonical terminal，投递前验证 schema，校验失败记录 session fault 而不是吞掉。
- 新增 `TestCLIExecutionLifecycleFences`，覆盖旧 turn、重复终态、停止 pending launch 和提交期间旧完成通知。该测试尚未运行；尝试被 CPU 93.9% 的入口门拒绝。独立启动 E2E 两次重试分别被 CPU 99.2% 与 91.9% 门拒绝，均 exit 2、没有测试本体结果。
- 本轮编译、light code guard、相关 `gopls check` 和差异检查通过。完整执行语义仍未验证；还有任意更旧 turn 在新 submission 窗口的隔离、持久 event/command fence、完整输出、当时固定为 read-only 的 sandbox、Claude/fork/recovery 等未完成项。
- 资源拒绝不是实现完成，也不作为缩小目标理由；后续必须取得真实 daemon 和两 CLI 的实际 E2E 证据。

## 第五轮进展（仍未完成）

- 新增 `standalone/claudewire`：官方 `claude -p --input-format stream-json --output-format stream-json` 的用户消息、结果身份校验、单帧上限、进程组与关闭/等待管理。根 session 和 `parent_tool_use_id` 分开判断，子 agent 输出不冒充根任务结果；不引入 Python/TypeScript SDK runtime。
- 新增 `TestClaudeStreamProtocolContract`，覆盖用户消息字段、坏 JSON、错误结果、foreign session 和 child result。协议包编译、gopls 诊断和代码守卫通过；测试本体尚未运行。
- Claude process 仍未接入 `Execution` 的 provider 分支，不能报告 Claude 任务已可执行。还需发送取消/超时、官方 capability probe、实际 session 激活、输入权限与输出持久化。
- 本轮 `TestMCPOrchServeBlackBoxSingleInstance` 首次重新通过资源入口门（CPU 67.4%）；前置 archtest 实际通过（449.467s），随后再次采样 CPU 100%、空闲内存 51%，在业务测试启动前被资源门拒绝，wrapper exit 2。exec session `81764` 已结束，不能再描述为运行中；E2E 本体仍没有本轮结果。

## 第六轮进展（仍未完成）

- Claude provider 分支已接入同一 `Execution`：创建 CLI 进程、固定 session UUID、发送文本、校验根结果身份、回传 canonical terminal、关闭受管进程。`CLAUDE_CONFIG_DIR` 从显式 provider home 注入，V3/数据库控制变量继续剥离。
- 共享逻辑编排 owner 未变；Codex 保留自己的 app-server 控制路径，Claude 不支持的 interrupt/fork/recovery 继续明确拒绝。Claude 关闭并不证明 provider 级 archive 或持久恢复已实现。
- 新增 `TestClaudeExecutionRoutingContract`，覆盖 provider 选择、foreign/nested result 不污染根任务、成功结果到规范终态的映射；尚未运行。
- 代码守卫、编译、相关 gopls 诊断和差异检查通过。两次独立启动 E2E 重试分别在 CPU 99.7% 和 100% 时被入口资源门拒绝，exit 2，未产生业务测试结果。
- 仍待真实验证与补齐：Claude system/init 激活证明、结果与确切输入 turn 的持久关联、输入/输出全字段守卫、取消、完整 provider 权限配置、持久任务/事件/消息与恢复、两个 CLI 客户端真实 MCP 调用。不能把本次 provider 分支接线当成可独立交付。

## 第七轮进展（仍未完成）

- 从 canonical terminal store 反查发现：CLI launcher 成功后没有绑定 `sessionGeneration`，后续 turn head activation 会拒绝零代际。本轮在 CLI launch 完成后、首 turn 提交前接入既有 `BindSessionGeneration`，保持现有终态 CAS 校验，不删除安全要求。
- 新增 `TestCLILauncherBindsSessionGeneration`。测试尚未执行：独立启动 E2E 在 CPU 100% 时被入口门拒绝，代际回归在 CPU 99.6% 时同样被拒绝（均 exit 2）。
- 编译、代码守卫和差异检查通过。持久 thread/binding、完整 event/content/outbox、恢复和两 CLI 真实 E2E 仍未完成；本次代际绑定使用运行内 launch generation，不能冒充跨重启持久 incarnation 证明。

## 第八轮进展（仍未完成）

- 为独立执行器增加 `SessionStore` 窄口，在首轮提交前把 thread 与 provider binding 原子保存到既有表；store 实现在 `cmd/mcp-orch/store/agent/session.go`，沿用该包已有的 `database/sql` 边界，未新增表或 migration。
- 新增 `TestStandaloneSessionPersistenceAtomicity`：真实内嵌 SQLite migration 后写入身份，重复 binding 冲突必须回滚同批 thread，避免孤立行。该测试尚未执行。
- 编译、相关 gopls 诊断、代码守卫和差异检查通过；独立启动 E2E 和持久化测试分别被 CPU 99.2% 与 100% 的入口资源门拒绝，均 exit 2、未运行测试本体。
- 当前持久身份仅覆盖创建结果，不具备 launch intent/receipt、跨重启 incarnation、完整输出/event/outbox、恢复与多端权限保证。store 字段动态覆盖、生成查询收敛与实际 SQL 验证仍待补齐，不能把两行原子插入当完整可恢复执行。

## 第九轮进展（仍未完成）

- 新增 `TestStandaloneGraphStartsWithoutHost`，直接启动生产独立 Fx 图并停止，避免只检查局部组件或编译结果。该测试和独立 binary E2E 分别被 CPU 99.9% 与 100% 的入口门拒绝，exit 2，未执行测试本体。
- 把 SessionStore 缺失检查提前到 CLI 外部进程启动前，并补入口检查测试；防止创建进程后才发现不能保存身份。
- 编译、相关静态诊断、代码守卫和差异检查通过。仍未取得 standalone startup GREEN，完整业务 E2E、两 CLI 实际接入、持久事件/消息/恢复和权限未完成。

## 第十轮进展（仍未完成）

- direct-peer 最小跨任务闭环已落地：migration 124 新增有向 grant、消息幂等键/请求摘要、submitted/delivery_failed 状态和 receiver exclusive cursor；store 事务测试覆盖未授权写、重复请求、冲突、撤权隐藏读取。
- 新增 `manage_direct_peer`、`send_direct_peer`、`read_direct_peer` 工具。grant/revoke 仅 operator scope；send/read 从可信 agent scope 取发送者/接收者身份，并在明确的 provider 前置失败时记录 `delivery_failed`，成功只记录 `submitted`，不冒充 provider 已观察或完成；后续第十二轮进一步把 error-only 提交错误收紧为 `delivery_outcome_unknown`。
- 新增跨 app 重启持久化 E2E 与真实活体 receiver 提交 E2E；早期 medium gate 曾因瞬时 CPU/admission 退出 2，随后 Codex、Claude 及跨 provider 双向链路均取得完整 wrapper 通过。direct-peer 仍不具备完整双向 receipt、事件 outbox、Room、跨 incarnation fence 或 provider 已读证明。
- standalone/CLI 单元回归在修正 canonical diagnostic/recovery 字段和 schema 测试表示后通过；`go build -p=1 -tags=e2e ./cmd/mcp-orch`、`git diff --check` 通过。`make sqlc-verify-worktree` 生成稳定性通过，但最终 Git 状态检查被既有 `internal/store/sqlc/sqlite_runtime_sql_test.go` 的 `operation not permitted` 阻断，未绕过。
- 双 provider、官方 Claude/Codex 客户端和独立 binary 的本地证据已覆盖各自接入边界，新增跨 provider 双向真实 E2E 进一步覆盖 ST-08 的运行时往返；ST-08 的完整 receipt/event outbox 以及 ST-09～ST-14、ST-16～ST-18 仍未完成，计划保持 Active。
- 复核发现 standalone Fx 图此前没有把 `Execution` 的 provider terminal sink 显式接到 orchestration service；该接线尝试已回退，因为 `serviceResult` 是 Fx 输出结构而不是可注入的独立接口，不能用未验证的隐式绑定冒充完成。当前真实 provider 直连测试仍直接消费 execution sink，独立 daemon 的完整 provider→service 终态 E2E 仍需以正式可注入窄端口重新设计并验证。
- 本轮修正了 CLI 失败终态的 diagnostic ID 与 recovery action，使其符合 canonical turn schema；standalone、direct-peer store、direct-peer tools 单测通过，standalone DAG 装配回归通过。该回归不等于真实 daemon/provider 终态闭环完成。
- 进一步验证 standalone Fx 接线：`cliLauncher` 的 provider sink 目前仍由 launcher/service 内部绑定路径拥有，不能把 `serviceResult` 或未注册的 sink interface 作为 Fx 参数；错误试验已撤回，避免启动时出现 missing dependency。完整独立 daemon provider→service terminal E2E 仍是待补项，必须新增正式 composition seam 后再验收。
- provider terminal composition seam 已正式收口：`AgentTerminalHandler` 由 `ProvideTerminalHandler` 以 Fx 接口显式导出，standalone `bindStandaloneExecutionSink` 在图构造时绑定 `Execution`→service。此前草拟的 terminal-handler 路由测试当前源码中不存在；实际接线回归为 `TestDirectPeerProjectorFxProviderProjectsOutbox`、`TestProvideExecutorsWireLifecycleHooks` 与 standalone graph startup/DAG 回归，均以这些真实测试名记录。随后真实 daemon + Codex/Claude provider 状态推进 E2E 已通过并在本轮追加记录；这些隔离 worktree 的本机证据仍不覆盖完整 Room/outbox/recovery 或发布验收。
- 新增第三方接入说明 [`docs/reference/mcp-orch-standalone-clients.md`](../../reference/mcp-orch-standalone-clients.md)，给出严格 JSON 配置、`serve --config --ready-file`、Claude Code HTTP MCP 和 Codex CLI `config.toml` 的可复现形态，并明确 token/状态目录/单实例边界及当前未支持能力。文档只描述源码已实现的入口，不替代真实客户端和发布环境验收。
- 真实 provider→service 终态 E2E 收口：`TestStandaloneDirectPeerSubmitsReceiverTurn` 使用调用方显式提供的 `ORCH_E2E_CODEX_BIN`、真实 `launch_agent` receiver、`send_direct_peer`，检查 `submitted` receipt、receiver idle 及非空 service report；完整 medium wrapper 通过（archtest 146.634s，业务 9.075s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。
- 同一闭环新增 Claude 版本 `TestStandaloneDirectPeerSubmitsClaudeReceiverTurn`，使用调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、真实 stream-json receiver 和 service report；完整 medium wrapper 通过（archtest 123.084s，业务 7.590s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。这两条证据证明双 provider 终态已进入独立 daemon 的 orchestration owner，不证明完整 Room/outbox/recovery 能力。
- standalone direct-peer E2E 另新增 `TestStandaloneDirectPeerSupportsBidirectionalConversation`，完整 medium wrapper 通过（archtest 174.375s，业务 0.097s，admission low/medium/low），证明双向授权下两个独立 root task 可往返发送并按 exclusive cursor 读取；消息仍只覆盖 direct-peer 最小闭环。
- 本轮补强 `TestStandaloneDirectPeerSubmitsReceiverTurn`：等待 receiver idle 后通过 `AgentReportPort` 检查 service report sequence 和正文，避免只凭 `submitted` receipt 判定投递完成。该 Codex 测试完整 medium wrapper 通过（archtest 146.634s，业务 9.075s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。同一骨架新增 Claude 版本 `TestStandaloneDirectPeerSubmitsClaudeReceiverTurn`，完整 medium wrapper 通过（archtest 123.084s，业务 7.590s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。这两条真实测试证明 provider terminal 已经由 Fx sink 回到 orchestration service report。
- `TestDirectPeerToolInputFieldCoverage` 和 `TestDirectPeerStoreContract` 分别经 light wrapper 通过（业务 0.016s/0.048s，最终 admission low），schema 字段、授权、幂等、cursor、撤权回归保持绿色；wrapper 仍标记 LOCAL_NON_AUTHORITATIVE NOT_VERIFIED。
- 修复 direct-peer 撤权后的幂等重试边界：`sendInTransaction` 现在先验证当前 active `can_send` grant，再读取同 key 旧消息；新增回归确认撤权 sender 不能借旧幂等键重新访问发送路径。`TestDirectPeerStoreContract` 经 light wrapper 重跑通过（archtest 123.027s，业务 0.045s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。
- 同域跨 provider 双向真实 E2E 已通过：`TestStandaloneDirectPeerCrossProviderConversation` 在同一个 standalone daemon 内启动显式配置的 Codex 与 Claude root task，分别完成 Codex→Claude、Claude→Codex 的 direct-peer 第二 turn，并通过 `AgentReportPort` 检查两端 service report。完整 medium wrapper 通过（archtest 120.849s，业务 31.571s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。该结果证明跨任务/跨 provider 的运行时往返链路，不等于完整事件 outbox、跨 incarnation 恢复或 Room 语义完成。
- 当前代码回归复跑保持绿色：`TestStandaloneDirectPeerPersistsAcrossRestart` medium wrapper 通过（archtest 119.509s，业务 0.132s），覆盖 app 重启后的消息幂等、exclusive cursor、撤权隐藏读取；`TestStandaloneGraphStartsWithoutHost` light wrapper 通过（archtest 122.667s，业务 0.064s），覆盖正式 Fx sink 接线下的独立图启动与停止。两次均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不替代远程/发布环境证据。
- direct-peer 投递错误边界补强：依赖缺失或 receiver snapshot 失败时，已持久化的 `accepted` 消息必须写入 `delivery_failed` receipt，并把 receipt 写失败与原始错误一起返回；`TestDirectPeerDeliveryPortFailureMarksReceipt` light wrapper 通过（archtest 123.553s，业务 0.015s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED）。
- 独立 binary 配置/参数负例已通过：`TestMCPOrchServeRejectsInvalidCLIAndConfig` medium wrapper 通过（archtest 118.564s，业务 0.553s，前/中/后 admission low，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED），未知 flag、缺失/相对/损坏配置均在 agent 副作用前失败。
- direct-peer 事件历史已接入：migration 125 新增 append-only `orchestration_direct_peer_events` 与同事务 `orchestration_direct_peer_event_outbox`，grant/message 状态变化写入对应事件，`read_direct_peer_events` 以 active read grant 和 exclusive cursor 对 agent 暴露可见历史；`TestDirectPeerEventHistoryAndOutbox` light wrapper 通过（archtest 121.837s，业务 0.044s），`TestStandaloneDirectPeerSupportsBidirectionalConversation` medium wrapper 通过（archtest 145.125s，业务 0.066s）。随后补齐 outbox claim/lease/expired takeover/projected ACK 与 standalone event-bus projector；完整 receipt/Room/content/incarnation 恢复仍未完成。
- Codex sandbox 不再由执行器硬编码为 `read-only`：standalone 配置现在要求显式 `read-only`、`workspace-write` 或 `danger-full-access`，并原样传入 `thread/start`；受管 coding-agent E2E 使用 `workspace-write`，审阅/fixture 测试使用 `read-only`。缺失、未知或 Claude 携带 Codex sandbox 均 fail-fast，避免把只读成功误报为可写编码能力；变更后的配置契约和真实 provider E2E 已在本轮末尾重跑。
- 事件路径接入真实 Claude 后复跑保持通过：`TestStandaloneDirectPeerSubmitsClaudeReceiverTurn` medium wrapper exit 0（archtest 126.241s，业务 7.599s，前/中/后 admission low/low/medium，LOCAL_NON_AUTHORITATIVE NOT_VERIFIED），证明事件事务写入没有破坏 Claude provider terminal→service report 链路。
- 最终静态收口保持通过：`make sqlc-verify-worktree` 报告生成输出在重生成前后稳定，`GOWORK=off go build -p=1 -tags=e2e ./cmd/mcp-orch`、`git diff --check` 与 `./scripts/test_with_guard.sh --light-guard-only` 均通过。以上仍是隔离工作树本地证据，不替代远程/发布环境授权；未提交、未推送。
- 事件历史测试在文档更新后再次经精确 light wrapper 通过：`TestDirectPeerEventHistoryAndOutbox` 业务 0.046s、archtest 155.300s，前/中/后资源门均放行；wrapper 仍标记 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- 新增 `TestStandaloneOfficialClaudeToolCall`，以官方 Claude print/stream-json 会话和临时 `--mcp-config` 实际调用 `mcp__orch__list_agents`，并要求显式认证目录，避免修改用户 MCP 配置。首次缺少 `ORCH_E2E_CLAUDE_BIN` 时按预期在 daemon 副作用前 fail-fast；随后一次尝试被后置资源门拒绝，最终显式 auth 目录重跑通过，详见本轮末尾证据。
- direct-peer outbox 已补齐 durable claim/lease/expired takeover/token fence/ACK API，并由 standalone runner 投影到进程内事件总线；`EventProjector` 在 sink 失败时保留 claimed 项、成功后才标记 `projected`。新增 store/projector 回归与 standalone 投影 E2E；此前一次尝试受宿主后置资源门拒绝，随后 store 与 standalone 投影测试均取得 wrapper GREEN。
- 官方 Claude 工具调用 E2E 已取得 wrapper GREEN：`TestStandaloneOfficialClaudeToolCall` 使用调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、已认证 `ORCH_E2E_CLAUDE_AUTH_DIR` 和临时 `--mcp-config`，真实调用 `mcp__orch__list_agents` 并校验 `tool_use` 事件；业务 28.470s、archtest 164.658s，前/中/后资源门 low/medium/low，wrapper exit 0，仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- 在 outbox projector runner 接入后的当前代码上复跑同域跨 provider 双向真实 E2E：`TestStandaloneDirectPeerCrossProviderConversation` wrapper exit 0，业务 49.624s、archtest 178.164s，前/中/后资源门 low/medium/low，Codex→Claude 与 Claude→Codex 两个第二 turn 均收到 service report；证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- projector、Claude 官方工具调用和跨 provider E2E 之后的最终收口再次通过：`make sqlc-verify-worktree` 输出稳定，`GOWORK=off go build -p=1 -tags=e2e ./cmd/mcp-orch`、`git diff --check`、`./scripts/test_with_guard.sh --light-guard-only` 均通过。
- outbox claim 接管回归在重构后取得 wrapper GREEN：`TestDirectPeerEventOutboxClaimLeaseAndFence` 业务 0.097s、archtest 167.023s，前/中/后资源门 low/low/medium，验证过期租约接管、token 变更、旧 worker ACK 拒绝和 projected 不重放；证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- projector 与事件类型接入后的官方 Codex 客户端回归保持 GREEN：`TestStandaloneOfficialCodexClient` 使用调用方显式提供的 `ORCH_E2E_CODEX_BIN`、隔离 `CODEX_HOME` 和真实 app-server MCP `mcpServer/tool/call`；业务 1.269s、archtest 161.592s，前/中/后资源门 low/low/medium，wrapper exit 0，仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- sandbox 契约收紧后的回归保持 GREEN：`TestConfigStrictContract` light wrapper 通过（业务 0.020s，archtest 217.056s）；`TestStandaloneManagedCodexTurn` 以 `workspace-write` 完成真实 Codex turn（业务 13.708s，archtest 178.683s）；`TestStandaloneManagedClaudeTurn` 完成真实 Claude stream-json turn（业务 9.289s，archtest 181.111s）；`TestStandaloneDirectPeerCrossProviderConversation` 在同一 daemon 内完成 Codex↔Claude 双向第二 turn（业务 51.593s，archtest 147.034s）。以上均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不替代发布/远程环境证据。
- sandbox 回归后的静态收口再次通过：`git diff --check`、`GOWORK=off go build -p=1 -tags=e2e ./cmd/mcp-orch`、`./scripts/test_with_guard.sh --light-guard-only` 和 `make sqlc-verify-worktree` 均通过；工作树仍未提交、未推送。
- sandbox 回归后官方 Claude 工具调用继续 GREEN：`TestStandaloneOfficialClaudeToolCall` 使用调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、显式已认证 `ORCH_E2E_CLAUDE_AUTH_DIR` 和临时 MCP 配置，真实发起 `mcp__orch__list_agents` 并收到 `ORCH_TOOL_CALL_OK`；业务 32.637s、archtest 147.148s，前/中/后资源门 low/low/low，wrapper exit 0，证据为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- provider 可发现性与实际执行器已对齐：standalone 的 `newStandaloneModels` 为配置中的 Codex/Claude 显式写入 `available=true`，`list_models` 保留该能力覆盖；legacy registry 仍把未经 standalone 接入的 provider 标为 packaged build 不支持。该标记只代表 standalone provider 路由存在，仍不替代凭据、配额和模型可用性检查。`TestHandleListModels_MarksCodexAvailable`（业务 0.019s、archtest 165.885s）与 `TestHandleListModels_PreservesExplicitAvailability`（业务 0.021s、archtest 241.849s）以及 `TestStandaloneModelsExposeConfiguredProviders`（业务 0.017s、archtest 172.872s）均经 wrapper 通过，证据为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。

## 第十一轮进展（仍未完成）

- Codex fork 已接入 standalone 执行器：读取可信 parent session 的真实 thread，调用官方 `thread/fork`，要求返回非空且来源一致的 child thread，并把 child 的 `ParentID` 与 provider thread 原子持久化；child 随后可继续提交真实 turn。`TestStandaloneManagedCodexFork` 已经 medium wrapper 通过（业务 30.467s、archtest 141.490s、资源门 low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。这只证明 Codex fork 的核心路径，不证明跨 daemon 恢复或完整 fork receipt。
- 新增 `PersistedRuntimeActivation` 接口和 Codex `thread/resume` 实现：service 从可信 binding/thread 重建缺失 runtime，再由 standalone 执行器恢复真实 app-server session；不接受客户端自带 thread、cwd 或 provider 身份。`TestStandaloneManagedCodexResume` 已经 medium wrapper 通过（业务 18.891s、archtest 263.747s、前/中/后资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`），并与 orchestration 代际绑定回归共同锁定恢复身份。
- 新增 `TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss`，覆盖第一 standalone Fx app 的真实 turn、执行器句柄丢失、同 SQLite 创建第二个 standalone Fx app、恢复原 thread 后发送第二条消息。调整为先恢复 provider session、再绑定 terminal generation 后，medium wrapper 已通过（业务 27.199s、archtest 313.056s，前/中/后资源门 medium/medium/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。该结果证明受控执行器损失后的重建闭环，不是独立 `serve` binary 的 SIGKILL/锁接管，也不等于硬崩溃接管或跨 incarnation fence。
- 为诊断恢复窗口，terminal head 缺失身份时现在返回受控的 thread/turn/session/generation/launch-seq 字段；不输出凭据。`TestSubmitTurnRehydratesPersistedAgentBindsTerminalGeneration` 已经 light wrapper 通过（业务 0.019s、archtest 248.187s、资源门 medium/medium/medium，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。
- standalone 未接通的 CLI 能力现在统一返回 typed `CapabilityError`：Claude 的 `thread_archive`/`turn_interrupt`，以及非 Codex 的 `thread_fork`/`thread_resume` 均明确拒绝，不会把 stop、空会话或新任务冒充成功；`TestClaudeArchiveDoesNotImpersonateStop` light wrapper 已通过（业务 0.021s、archtest 215.083s，资源门 medium/medium/medium，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。
- 同一能力集合新增 `TestClaudeUnsupportedLifecycleCapabilitiesAreTyped`，覆盖四个拒绝入口的统一 typed error 形状；本轮两次尝试因宿主 CPU 95.6%/97.5% 被 light 资源门拒绝，测试本体未执行，不计为通过证据。
- MCP 错误 envelope 的默认分类器现在识别 `CapabilityError` 和 `capability_not_supported`，稳定映射到既有 `capability_unsupported` code；`TestClassifyToolErrorRecognizesTypedCapabilityError` light wrapper GREEN（archtest 151.767s，业务 0.004s，前/中/后资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。
- 修正 stop/archive 语义：`stop_agent` 只有在归档成功时才返回 `archived=true`；Claude 的 `thread_archive` typed capability error 会触发真实 `StopAgent` 回退，不能把 Stop 冒充 Archive。`TestStandaloneManagedClaudeStop` 已用调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、认证 home 和显式 model 取得 medium wrapper GREEN（archtest 207.955s，业务 0.142s，前/中/后资源门 low/medium/medium），真实进程退出后状态从 `stopping` 收口，不再卡住；证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- `TestStandaloneManagedClaudeStop` 首次真实运行暴露了 process-exit terminal 使用当前 `stopping` 状态与 durable head 的 pre-stop `idle` fence 不一致，导致 `ErrTerminalOutcomeConflict` 和 stop 等待超时。修复为 process-stop 从真实 active current head 对齐 pre-stop `ExpectedActiveState`，投影阶段允许合法 `stopping` 中间态；新增 `TestProcessExitStopUsesPreStopHeadFence`（业务 0.026s，archtest 213.016s）锁定该回归，禁止吞掉其它 terminal CAS 冲突。
- 新增 `TestStandaloneManagedCodexArchive`，真实 `codex 0.151.0`/`gpt-5.6-sol` 先完成一轮 rollout，再调用官方 `thread/archive`，验证归档后执行器确实停止；medium wrapper GREEN（archtest 160.511s，业务 11.614s，前/中/后资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。Codex 在尚无 rollout 的新 thread 上会明确返回 `[-32600] no rollout found for thread id ...`，E2E 保留该 provider 前置条件，不将错误降级成成功。
- 新增 `TestStandaloneManagedCodexInterrupt`，真实 Codex turn 先请求受控长操作，再在 active-turn 启动竞态下以 5 秒有界窗口重试 `turn/interrupt`；最终收到 `interrupted`、`TerminationCause=user_request` 和非空 `TerminationRequestID`。medium wrapper GREEN（archtest 149.365s，业务 0.354s，前/中/后资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）；仅重试明确的 `no active turn` 启动竞态，其它错误和无终态均失败。
- `TestMCPOrchServeRejectsSecondInstance` 增加真实状态目录符号链接别名配置，和原配置/另一份配置一起验证第二 owner 拒绝；medium wrapper GREEN（archtest 147.183s，业务 0.967s，前/中/后资源门 low/low/medium，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`），第一实例在三次竞争后仍可正常 MCP 调用。该结果仍不替代崩溃接管、锁文件删除重建和原生 Windows 证据。
- 新增 `TestMCPOrchServeReclaimsAfterOwnerKilled`，真实 binary 的第一 owner 被 SIGKILL 后，第二实例使用同一状态目录重新取得锁、发布 ready、完成 MCP initialize/list_agents 并正常关闭；medium wrapper GREEN（archtest 149.687s，业务 0.722s，前/中/后资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。该结果证明内核锁在异常退出后可接管，不证明锁文件删除/替换和原生 Windows 句柄语义。
- 多会话请求路由回归继续收紧：`TestMCPOrchServeBlackBoxSingleInstance` 让两个独立 HTTP session 复用相同的 `tools/list`/`tools/call` request ID，并分别校验返回 ID、工具结果、删除 A 后 B 仍可用；这把 request-ID 隔离纳入真实 binary E2E，而运行中受管任务断线存活仍未验证。
- Claude 正常对话回归保持 GREEN：`TestStandaloneManagedClaudeTurn` 以调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、认证 home 和显式 model 完成真实 stream-json turn，业务 8.010s、archtest 139.896s，前/中/后资源门 low（`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）；证明 stop/pre-stop fence 修复没有破坏普通对话链路。
- 本轮静态检查在恢复时序修改后保持通过：`git diff --check`、`GOWORK=off go build -p=1 -tags=e2e ./cmd/mcp-orch`、`gopls check`、`./scripts/test_with_guard.sh --light-guard-only` 和 `make sqlc-verify-worktree`；生成代码在重生成前后稳定。工作树继续未提交、未推送。
- Claude 的真实 standalone binary 链路取得首个完整双轮证据：`TestStandaloneBinaryLaunchesManagedClaudeThroughMCP` 通过 `serve --config --ready-file` 启动独立二进制，在同一 HTTP MCP session 调用 `launch_agent`，等待首轮 `DAEMON_CLAUDE_TURN_OK`，确认 runtime active turn 已收口，再调用 `send_message(wait_report=true, timeout_ms=60000)`，收到 `DAEMON_CLAUDE_SECOND_OK` 且 `report_seq` 递增、状态为 idle；随后执行一次有限进程后代快照。测试使用调用方显式提供的 `ORCH_E2E_CLAUDE_BIN`、`ORCH_E2E_CLAUDE_HOME` 和 `ORCH_E2E_CLAUDE_MODEL`，业务测试 20.435s，wrapper exit 0；因使用本机认证/网络，证据等级仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不替代发布或远程环境验收。
- Codex 的独立客户端组合链路也取得真实双轮证据：`TestStandaloneCodexClientLaunchesManagedCodexThroughMCP` 启动独立 `mcp-orch` binary，再由官方 Codex app-server 客户端通过 `mcpServer/tool/call` 调 `launch_agent`，等待受管 Codex 首轮非空 idle report，确认 active turn 收口后在同一 Codex MCP session 调 `send_message(wait_report=true, timeout_ms=60000)`，校验 `previous_report_seq` 与第二轮 `report_seq` 严格递增、报告非空且 idle，并执行一次有限进程后代快照。测试使用调用方显式提供的 `ORCH_E2E_CODEX_BIN`、`ORCH_E2E_CODEX_HOME` 和 `ORCH_E2E_CODEX_MODEL`，业务测试 13.953s，wrapper exit 0；Codex 模型正文不作固定 marker 断言，避免自然语言复述误差掩盖协议 fence，证据同样为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- 为使第二轮等待真正可用，`publicTerminalReportResult` 不再把 durable terminal outcome 的 `report_seq` 固定为 1，而是暴露 canonical terminal head version；`TestTerminalPublicReportUsesHeadVersionFence` 锁定该递增围栏。Claude 配置可省略 Codex 专用 `sandbox`，Codex 仍必须显式声明三种 sandbox 策略；对应 `TestParseConfigAllowsOmittedClaudeSandbox`、`TestParseConfigStillRequiresCodexSandbox` 已通过 standalone 包回归。
- 新增真实 daemon E2E 现已覆盖 Claude 与 Codex：`TestStandaloneBinaryLaunchesManagedClaudeThroughMCP` 和 `TestStandaloneCodexClientLaunchesManagedCodexThroughMCP` 均完成 binary→MCP `launch_agent`→首轮报告→同一客户端后续 turn；测试 harness 构造不带 V3 变量的环境，另做命令行禁用词过滤和一次性后代快照，不证明 provider 进程环境隔离、正式 provenance 或所有失败路径的进程组回收。ST-07 仍保持“部分完成”，因为官方 binary provenance、发布制品、Windows/远程环境证据尚未取得。ST-09、ST-11、ST-14、ST-16～ST-18 等总规格门仍未实现，计划保持 Active。
- Claude 事件时序修订：受管 CLI 可能先发根 `result`、后发 `system/init`；standalone 现在保留一个有界 pending root result，在匹配 init 到达后只回放一次，冲突结果 fail-closed。`TestClaudeResultBeforeInitIsBufferedAndReplayedOnce` 与 `TestClaudeConflictingPreInitResultsFailClosed` 锁定该行为，避免首轮结果丢失或重复终结。
- 本轮收口复核：本轮实际运行的 standalone/task HTTP、mcp-orch 根包、字段守卫、E2E compile-only 与轻量守卫均通过，相关 race/vet 也按具体包/selector 复核；不把“standalone 全包”概括成所有变更的统一 race 结论。真实 Claude binary→MCP→双轮重跑需要调用方再次明确授权使用认证 home/网络配额，当前未把审批拒绝误记为新的 GREEN 证据。计划保持 Active。

## 第十二轮进展（仍未完成）

- 新增内部 `cmd/mcp-orch/orchestration/waitaggregate` 收口器：固定最多 32 个 target，支持 `any/all/quorum/first_success`、typed event/content metadata predicate、稳定 item 顺序，以及 `satisfied|timeout|aggregate_failure|indeterminate` 判别；只依赖 `Waiter` 窄口，不写 durable handle、grant、EventStore、direct-peer 或 provider，也未注册 public `wait_agents`。最终实现的本机固定缓存、race 和代码守卫通过；此前 host light 结果发生在最后一次 runtime-safe 启动入口微调前，因此不把 272.443s/0.114s 旧结果写成当前版本 host PASS，证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- 强化 migration 130 `accesswait` owner：缺失 source 不再静默变成 pending；after cursor 越过 materialized watermark 或低于 dedup floor 直接返回 typed cursor error；满足事件和取消 CAS 在同一写连接重验 grant/current identity fence；不支持 transactional identity reader 的构造直接失败；CAS 竞争重放已返回胜出终态；deadline timeout 使用 `timed_out` owner 状态并统一安全时钟，SQLite 毫秒时间戳解析固定兼容。固定缓存 package race/重复运行通过，host accesswait 本轮在架构前置后因宿主 CPU 后置门升至 high 而未执行业务本体，未写成 host PASS。
- 增加 `CallerPrincipal` 的严格 context 快照和 local HTTP session 接缝：显式配置的 profile principal 会在 initialize→initialized→tools/call 期间保持不可由 `_meta` 改写，非法/过期 fail-closed；零值仍是直接构造 local profile 的 legacy 兼容模式。随后 production `serve` 已强制配置 `principal_ref/scope_ref`，使用可失败的加密随机 server-boot auth context并按每个 initialize 签发有限期 session 快照；这仍是 operator issuer，不冒充 child identity。临时移除 `principal_ref` 校验时精确测试 RED，恢复后 GREEN；当前 `TestConfigStrictContract` host light wrapper exit 0（archtest 189.280s、业务 0.016s、资源门 low/low/low，`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。
- direct-peer accepted replay 增加一次性 `Fresh` 事务元数据和不授权的 `delivery_outcome_unknown` 分支：`MarkSubmitted` 失败或 error-only 提交端口返回错误后，同 key 重试不再调用 `SubmitTurn`，也不写伪造的 definitive `delivery_failed`。真实 provider submission ID、OUT command/attempt/receipt 和 wakeup/DAG recovery 仍未接入。定向 store/tools 测试与 race 通过；host gate 因后置宿主 CPU 门拒绝，未把本机结果提升为发布证据。
- canonical command intent 已把 configured caller principal/scope 拆为独立字段；migration131 再以 `send_message_v1` 一对一 profile保存完整 profile/auth-context/expiry、authorization request/grant/revision/time和双端最新 ID-1 binding，并用 HMAC、IMMEDIATE transaction、关系 trigger和全字段启动门禁保护。旧 accesswait fake Accepted handoff 已删除。profile/assembler没有 Fx/tool接线，CONTENT/RESOURCE/custody owner也未具备，所以尚不能进入 `recovery_custody_pending`、返回 accepted 或接 dispatcher。
- v131 当前快照精确 host-light 三入口均 wrapper exit 0：profile store archtest137.930s/业务0.662s，assembler archtest154.719s/业务0.125s，migration contract archtest132.001s/业务0.064s；全部仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。完整相关本机包与 sqlc 重生成稳定性通过；race按策略未执行，不能写成race PASS。
- RecoveryCustody本地候选现包含三层digest（含 proof digest v2）、revision-3（含external-service mode）DB writer_epoch/takeover、SQLite scalar/JSON/DDL/terminal join、`PinnedDirectoryLock`+`KernelAuthorityLease`、typed observation/同key reconcile、非MCP `rpcwire`+`authorityhttp`+`httpclient`及禁止普通orch import/open authority DB的架构守卫。独立服务 CLI 目前只有构造/生命周期候选，仍缺 production supervisor/CLI 依赖注入与配置发布、network FS证明、真实外部EvidenceFinalizer/anchor/签名trust root、durable coordinator 的受信接线和 production orch client/Fx/OUT 接线。
- 新增[task attribution/current task principal/canonical task event source候选](orch-subagent-upgrade/task-principal-and-event-source.md)、[Room contract candidate](orch-subagent-upgrade/room-contract-candidate.md)与[managed runtime mapping candidate](orch-subagent-upgrade/managed-runtime-mapping-candidate.md)；事件 source、reservation/incarnation 独立代际、AccessWait source、child handoff 私有目录、unified_v2 schema、managedlaunch prepared-request 绑定均有本机回归。尚无transport auth binder、真实event producer、provider/managed runtime supervisor 或`unified_v2` public list/wait/send接线。legacy send/directpeer不能升级为cross-task能力。
- 历史快照中的证据（`1108.097s`、`1066.951s`、`924.253s`）仅用于记录当时的 archtest 变体；当前 `make orch-upgrade-check` 已为 29 tests PASS（早期 22/25/26 为历史快照），且 dependency-map check 当前 exit 0。`1018.025s`（外层 elapsed 1019s，含 tasktransport/messageassociation candidate guard 及 parity 集合 281/67）同样是新增候选前的 full `internal/archtest` 历史观测；随后源码冻结后的当前 full archtest 已返回 `ARCHTEST_EXIT=0`（包内 1019.957s、外层 real 1075.50s，当前 parity 集合 284/69）。B09 trigger/claim branch hardening、planner source/digest/null guard、终态 compensation consume-only claim 及 B10 writer-session planning doc 已由定向普通/race/vet/LSP 复核。sqliteauthority/authorityhttp 旧 host-light 记录仍仅作本机候选证据；Windows仅test-binary compile。官方 wrapper 因未提交 `freeze_baseline.json` generated-file drift 按契约阻断。全部仍为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。总计划保持Active，十四份完整规格Draft，production send/Room NO-GO。

## 2026-09-03 继续执行（仍未完成）

- managedlaunch 的 COMMIT/ABORT projection 现在强制 `PreparedRequestDigest` 与 source
  operation 摘要逐字一致，并有合法格式但错误摘要的负测；该改动只收紧 projection
  fail-closed 绑定，未接 managedruntime supervisor、provider 或 Fx。
- B10 先落地不接线的 [`writer_inventory.json`](orch-subagent-upgrade/../../../../cmd/mcp-orch/store/taskdag/writer_inventory.json)
  与 `TestTaskDAGWriterInventoryIsExactAndCandidateOnly`，双向冻结 taskdag M01–M42
  的 production receiver/file/symbol 集合。清单状态是 `candidate_not_wired`，不表示
  writer session、BEGIN IMMEDIATE、epoch CAS 或旧 writer 硬隔离已经完成。随后新增
  隔离 `taskdagwriterfence.WriterSession` candidate，复用现有 `sqlctx.WithImmediateTx`
  的 tx-bound callback、stale CAS 与 commit-unknown replay barrier；仍未接入任何
  M01–M42 生产 entrypoint/Fx/shared DB。
- 外部 RecoveryCustody 继续是独立单实例私有 SQLite：authority path 先 canonicalize，
  外部模式调用 `securefs.CheckLocalFilesystem` 拒绝 UNC、已知网络/未知文件系统和检测
  失败；Bootstrap/OpenAuthority/Takeover 使用同一 canonical path。服务配置的重复键
  检查递归覆盖嵌套对象/数组并受深度、成员数和字节上限约束。PostgreSQL、产品 DB
  和共享文件仍明确排除；真实外部 finalizer/verifier 缺失时 CLI 仍 fail-closed。
- 本轮受影响包 ordinary/race/vet/LSP、SQLC 稳定性、Windows/Darwin/Linux test-binary
  cross-compile 通过；完整 direct code-size guard 通过。官方 `test_with_guard.sh
  --light-guard-only` 仍仅因工作树中未提交的 `internal/archtest/freeze_baseline.json`
  generated-file drift 阻断，未修改基线或绕过门禁。

本轮不改变总计划 `Status: Active`、十四份 S0 规格 `Draft`、B01–B18
`needs_revalidation`，也不改变 B08/B09/B10/B12、managed provider、cross-task send、
Room 与 release 的 NO-GO。生产 RecoveryCustody、OUT/receipt、managed supervisor、
历史 wakeup applier、全量 taskdag writer session 和发布/远程证据仍是下一阶段阻断项。

## 2026-09-03 额度恢复后继续执行（仍未完成）

- 重新拉起子 agent 复核后，完成 standalone CLI 异常退出边界：Codex/Claude 的
  `Wait`/reader 回调改为受控异步通知，绑定 provider 真实 thread/session、宿主
  `launchSeq`，并以 exactly-once、stale-session、explicit-close 和 late-launch
  success fence 收口；避免 callback 反向 `Close` 时 Wait worker 自等待。直接调用
  `Execution` 时缺失 owner fence 只分配实例内单调序列，不再固定填 `1`。
- managed Claude 的异常退出、手动恢复和 stall recovery 均不再落入旧的
  `startProcessLocked`/`LaunchRequest.Command` 路径；在 durable resume supervisor
  尚未实现前返回 typed `thread_resume` capability error。Codex managed recovery
  和 legacy local-agent recovery 保持原边界。
- legacy stdio/peer HTTP/bootstrap 的 direct-peer 工具在 scope 注入前整体隐藏并拒绝，
  防止 `_agentId/_cwd` metadata 冒充 operator 或 task 身份；standalone HTTP 的
  task-scoped direct-peer 工具只有完整 server-bound principal+attribution 才可见，
  不从 operator principal 推导 agent。
- legacy 与 standalone Fx 图各接入一个 direct-peer outbox projector，事件 sink 成功
  后才 ACK；这只修复本地通知投影，不构成 canonical EventStore、OUT receipt 或完整
  cross-task authorization。
- 生产 registry/legacy/standalone provider 增加了统一的 surface guard：strict
  `unified_v2`（`list_agents(view=route)`、`wait_agents`、strict `send_message`）以及
  Room tool/capability 均不公开，缺少 canonical owner 时候选构造器返回稳定 disabled
  错误；legacy `list_agents`/`send_message` schema 仍保持兼容，未把候选误接入生产图。
- task-only HTTP/runtime 的 allowlist 现在在构造期拒绝未绑定 capability manifest 的
  orchestration 控制/读取、direct-peer 和 unified 工具，并在触达底层 provider 前失败；
  安全本地工具仍须显式 allowlist。该 guard 不改变当前 operator/legacy endpoint，也不
  授予 task principal、grant 或跨任务权限。
- task-only HTTP session 在已有服务端归因绑定时，将经过验证的 `TaskID` 投影到内部
  `ToolScope.AgentID`，使 tools/list 与 tools/call 的 task-scoped direct-peer 判定一致；
  operator profile 继续保持空 AgentID，客户端 metadata 仍不能提供身份。该候选不注册
  unified_v2，也不把 legacy `send_message` 变成 canonical cross-task。
- standalone operator HTTP 仍保留 legacy `send_message(agent_id/pos, ...)` 以兼容现有
  官方 CLI 双轮 E2E；它是 service-level 的旧管理路径，不消费 CallerAttribution/AccessGrant，
  不能表述为 canonical cross-task send。严格跨任务发送必须使用未来独立的
  `unified_v2` grant target，并在 identity/event、CONTENT/RESOURCE、OUT/receipt/custody
  链完整后才允许注册；不能在 legacy handler 上局部改名或静默扩权。
- identity/event owner wiring 现在由 `canonicalOwnerModules` 在 legacy 与 standalone 图中
  只构造一次：identity Store→taskprincipal resolver、eventstore Store→task event producer。
  这只是可供后续 task consumer 使用的窄接口；没有 transport binder、child credential
  issuer、runtime event call 或 public tool 注册，不能把它写成跨任务能力。
- RecoveryCustody authority HTTP 现在要求带私有 marker 的 `VerifiedClient`；服务用真实
  注入的 `ReceiptVerifier` 包装 SQLite Authority，伪造/拒绝/不确定 terminal receipt
  会折叠为 same-handle `indeterminate`。缺真实外部 verifier 时，CLI 仍在依赖检查阶段
  fail-closed 且不会创建 authority DB/lock/WAL/SHM/ready。
- RecoveryCustody 独立 binary 现在有显式 opt-in 的 `build-recovery-custody-candidate` 构建目标；
  Linux/macOS package 默认不构建/复制它，只有 `RECOVERY_CUSTODY_CANDIDATE_PACKAGE=1` 才由
  package 资产和 `runtime-manifest.json` 明确记录为 `candidate`/`disabled`。它不进入 launcher
  `bundled_execs`，不会改变默认 orch runtime 或自动启动。真实外部
  EvidenceFinalizer/ReceiptVerifier 尚未注入，故 binary 即使配置完整仍在依赖检查阶段 fail-closed，
  不产生 startup side effect、terminal receipt 或 release 授权；旧的、没有候选字段的 package
  manifest 仍可由验证器读取，候选字段一旦出现则必须四项完整且保持 disabled。
- Gate S0 blocker registry 现在为 B01–B18 分别保存非空且唯一的 RED/GREEN/Guard
  planned/observed 文档引用，并由离线生成器校验 workspace 边界、输出三类计数；引用
  明确标为 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED`，不是 receipt、通过或 release 授权。
  生成器进一步把 §2.2 的 blocker 五字段作为 registry 的逐字段 SSOT 对照，并把
  source/spec/README/evidence 内容纳入一次读取的 canonical snapshot digest；表头、
  反引号/转义分隔符、别名重复和自指都会 fail-closed。
- RecoveryCustody 入口新增真实 `run(--config ...)` 负测：即使配置与私有凭据均完整，
  缺少外部 EvidenceFinalizer/ReceiptVerifier 时也必须在 Bootstrap/OpenAuthority、监听、
  lock/WAL/SHM/ready 之前 fail-closed 且零目录副作用；生产依赖仍未伪造或注入测试替身。
- standalone `AcquireInstance` 将 lock 与数据库路径绑定同一 canonical state root；
  RecoveryCustody 外部 authority 继续只使用独立私有 SQLite，并在 canonical parent
  上执行本地文件系统类型 fail-closed 检查。B09 wakeup target/payload agent identity
  的首尾空白在 Go 与 migration 134 SQL 两侧均拒绝；authority/SQLite path 也拒绝
  叶子名或完整路径首尾空格，避免 lock 身份与 SQLite `Open` 的路径归一化分裂；
  managedlaunch query projection 的 receipt 分支按状态严格 one-hot。
- `taskeventsource.Producer` 在调用 canonical appender 前拒绝 reservation/incarnation
  快照的未知 schema（当前只接受 schema 1），并以负测确认不会触达 appender；这只是
  identity/event candidate 的输入守卫，不提供迁移、序列分配或 runtime event 接线。
- 新增未注册的 transport-neutral task Binder candidate：只有服务端验证的
  `CallerAttribution` 与非 operator `CallerPrincipal` 才能将 `TaskID` 投影到内部
  `ToolScope.AgentID`，客户端 metadata、预绑定 scope、缺失身份和非法模板均
  fail-closed；同时补充 task HTTP 删除一个 session
  时另一个正在运行的 session 继续完成并可 ping 的 ST-03 回归。新增单实例专项矩阵
  记录 duplicate-owner、migration/bootstrap、unknown receipt、private SQLite/no-PG、
  network-FS 与 trust-root/rollback 的待复核门，均不注册 unified_v2/Room。
- 新增未注册的 `messageassociation` DTO/strict JSON 候选，固定 typed
  object/scope/cardinality、source principal 与双端 execution/turn fence，要求
  parent/message/reply 使用独立 command 与幂等 tuple，拒绝自引用、重复/未知/null/
  非规范字段和裸 `correlation_id`/`reply_to`；该候选不拥有 delivery、cursor、receipt，
  不接 OUT/DB/provider/Fx，不能替代 production cross-task evidence。
- canonical owner 图随后加入唯一 `orchestrationcommand` intent/journal Store、
  默认永不放行的 receipt verifier 与 Coordinator；`Reserve→Prepare` 只用于
  test-owned `recovery_custody_pending` genesis，`Apply` 保持拒绝且不推进 journal。
  同时新增 `identity.GetActiveSnapshot` 与未注册的 `RuntimeEventCandidate`，从
  identity-owner 事务快照和已有 source watermark 构造事件输入；由于 append 仍跨
  两个事务，不能接生产 lifecycle。taskruntime 的 listener/credential lifecycle
  race 也已补强，但仍未接 standalone Fx 或 child/provider 启动链。
- 历史批次曾记录 ordinary/race/vet/LSP、SQLC、代码守卫、地图和 full archtest
  通过；其中 `1018.025s`（以及后续 1066.951s、1120.714s）均早于本轮新增候选，
  不再作为当前耗时。当前官方 wrapper 仍会因未提交
  `internal/archtest/freeze_baseline.json` generated-file drift 阻断，未修改基线或使用
  `--no-verify`。

以上均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 本地证据；总计划继续 Active，十四份
完整规格继续 Draft，B01–B18 继续 `needs_revalidation`，production send/Room、
managed provider、RecoveryCustody supervisor、OUT/receipt、B08/B09/B10/B12 完整
owner 和发布/远程验收继续 NO-GO。

## 2026-09-03 额度恢复续作（当前状态）

在用户要求继续并通知子 agent 后，本轮并行完成了以下可验证窄切片：

- B08：`wakeupcommandcandidate` 对已有 `effect_pending`/`outcome_unknown` attempt
  不再重复交出 provider permit；command/profile/assembler 对 nil context fail-fast。
- ID-2：新增 transaction-bound `RuntimeEventTxCandidate`，要求 identity、source
  watermark 和 canonical appender 共用一个 `DBTX`，gap/commit-unknown 禁止重放；旧
  split-transaction candidate 仍 disabled。
- ID-2 相关 store 另提供 additive Tx seam：`RequireTransactionDBTX`、
  `identity.GetActiveSnapshotTx`、`eventstore.GetSourceStateTx/AppendTx/WithTransaction`，
  只允许真实事务对象并保持旧 `Append` 行为不变；该 seam 尚未接 runtime caller、序列
  分配、custody 或 public wait。
- `accesswait.Store` 已补 target-source selector 适配：仅 active `CanRead` grant、
  完整 target fence 和 canonical `task/<target>`/incarnation 可读取目标事件；caller/source
  fence 仍独立复核，伪造、撤权和 rollover 继续 fail-closed。该内部修复不注册 public
  `wait_agents`，也不替代 child/task auth、CONTENT/OUT/custody。
- 任务身份与授权：`tasktransport` 新增 Binder + discover/read/send authorization
  claim（parent/grant/revocation/cursor/zero-effect）；`childissuer` 只验证 parent→child
  identity（详见 [child issuer candidate](orch-subagent-upgrade/child-issuer-candidate.md)），
  `managedruntime/issuer` 明确是 endpoint/process `BindingCandidate`（详见
  [managed runtime binding candidate](orch-subagent-upgrade/managed-runtime-issuer-candidate.md)），两者均
  未注册、无 bearer/DB/provider。
- TRUST/TOOLS：新增 stdlib-only bounded trust envelope、CONTENT ticket、quoted/inert
  renderer 与 zero-effect sink decision（详见 [trust envelope candidate](orch-subagent-upgrade/trust-envelope-candidate.md)）；Registry 对显式 output schema 构造期编译并
  包裹唯一生产 handler，空 schema 不猜测，错误 envelope 仍由 common writer 负责。
- managedruntime 增加 `CandidateSupervisor` 内存状态机候选：仅接收外部已验证
  prepared receipt，严格处理 PREPARE/COMMIT/ABORT/QUERY 的重放、冲突与
  `outcome_unknown` sticky fence；不启动 helper/provider、不持久化、未接 Fx。
- 生命周期与外部 SQLite：process terminal projection 将 session cleanup 延后到
  durable outbox ACK；exitmonitor 未入 channel 的满缓冲 claim 可释放重试。RecoveryCustody
  外部模式继续独立单实例私有 SQLite（不是 PostgreSQL），主库 inode 与 kernel lease
  绑定，WAL/SHM 仅按合法 SQLite 生命周期在锁内重绑，replacement/appearance race
  fail-closed；读路径也复核 sidecar 身份，并拒绝 authority path 与固定 lock sentinel
  同名或同 inode。独立 coordinator wiring 只在 test-owned candidate 中打开已 bootstrap
  的独立 SQLite，candidate binary 仅 opt-in 打包为 disabled 资产。

当前稳定本机 Go 结果：ordinary EXIT=0（约 65s）；`GOWORK=off go test -race ./cmd/mcp-orch/...`
直接命令 EXIT=0（所有包 `ok`/`[no test files]`，无 race 报告；此前含 recovery-custody 的
739.401s 为历史快照）；vet（6.97s）、build（7.32s）均 EXIT=0；新增 Tx/supervisor/sidecar/sentinel/package 候选的定向
ordinary/race/vet 也通过。直接 code-size/entry checks 通过，但官方
`./scripts/test_with_guard.sh --light-guard-only` 当前 exit=1，首个失败是 25 项
production-ratchet findings（冻结基线 drift 尚未进入该阶段），不是单独的 drift 通过/失败结论。最终
`make project-map-refresh PROJECT_MAP_ARGS=--full && make project-map-check` 已 exit=0
（5461 files、8 domains、`drift=OK`）；Gate S0 `make orch-upgrade-check` 当前 exit=0
（29 tests 与 dependency-map check），两者都只表示规划/索引一致性。
当前源码冻结后的完整 `internal/archtest` 测试本体通过，`ARCHTEST_EXIT=0`，日志为
`ok ... 1019.957s`，外层 real 1075.50s；它覆盖 Tx/supervisor/sidecar/sentinel 及现有
候选守卫。更早的 `1101.495s`/`1157.59s` 仅保留为历史观测；当前结果仍只属于本机
静态证据。

上述实现不改变 production composition：strict `unified_v2` list/wait/send、Room、真实
child credential/nonce registry、CONTENT/RESOURCE/OUT/custody/provider receipt、跨进程
crash/reconcile、远程/Windows/发布验收继续 NO-GO；总计划保持 `Status: Active`。

本轮候选的详细边界见 [RecoveryCustody service candidate](orch-subagent-upgrade/recovery-custody-service-candidate.md)、
[RecoveryCustody SQLite authority](orch-subagent-upgrade/recovery-custody-sqlite-authority.md)、
[identity/event next slice](orch-subagent-upgrade/identity-event-next-slice.md)、
[event-store next slice](orch-subagent-upgrade/event-store-next-slice.md) 和
[managed-runtime binding/supervisor candidate](orch-subagent-upgrade/managed-runtime-issuer-candidate.md)；
这些文档与本段一样只记录本机候选和未闭合阻断，不改变 DAG 或发布状态。

## 2026-09-06 正式入口续作（完整目标继续 Active）

本轮主 agent 按真实 binary/官方 CLI 重新验证，而不是继承上述历史 PASS。
完整目标仍为 ST-01～ST-18 与适用总规格的正式调用链，不缩小成已有 operator profile 的演示。
详细命令、失败分类和后续依赖见
[正式服务运行进度](orch-subagent-upgrade/formal-service-progress-2026-09-06.md)，
操作者启动说明见[独立服务运行手册](../../reference/orch-standalone-runbook.md)。

- ST-02 新增 `TestMCPOrchServeIndependentStateDomains`：同一真实 binary 的两个进程/不同私有状态目录
  重叠存活，停止 A 后 B 的原 MCP session 继续工作，原受信 host-test 入口 PASS。
- 官方 Codex 0.153.3 + 显式 `gpt-6-astra` 已完成真实 standalone binary → 官方 Codex
  MCP client → managed Codex 首轮/后续 turn，并在增强进程身份与 Git 环境隔离后再次 PASS（约 18.58 秒）。
- Claude 正式首消息路径发现先等 `system/init`、却不允许发送触发 init 的首条 user 的循环前置。
  具名子进程 RED 后，仅移除该错误前置；first-input、wrong-session init、ready、busy 和
  terminal receipt 回归 GREEN。真实 daemon 已越过原拒绝；模型双 turn 当前仍被上游 HTTP 503
  的 `api_retry` 阻断，独立运行同一官方 CLI 也复现，不能把本轮 Claude 模型场景写成 PASS。
- 未接生产 caller 的 Claude probe 另有独立缺陷：非 UUID `orch-probe` 及空 user 并非有效
  无模型握手。现改为关联 request ID 的 `control_request/initialize`，真实官方 CLI 2.1.237
  握手与主动关停 PASS；143 只在本探针主动 TERM 后归一化，普通失败与 SIGSEGV 仍保留。
  RequestID 字段守卫完成真实临时破坏 RED、字节恢复与 GREEN；不把探针修复误归因为首消息修复。
- 旧关停检查只按退出后的 PPID 遍历，会漏掉 reparent 孤儿。现保存关停前 stable process
  identity，再按 PID/start token 复核；同 start token 换 exe 仍算存活，读取错误不是退出证明。
  窄证据测试与真实 Codex 场景均 PASS；这不覆盖未观测后代或尚未运行的 Windows 场景。
- 真实 Git 反例证明 `cwd=repoA` 仍可被父 `GIT_DIR/GIT_WORK_TREE` 改路由至 repoB。
  已仅剥离仓库局部上下文，保留认证/作者/home，两 provider 的动态变量及真实仓库回归 RED→GREEN。

基线文件、硬阈值、真实暂存内容和个人 CLI 配置未被本轮手动改写。主 agent 审查旧基线差异后
使用外置独立 index 保留原 drift/ratchet 检查；没有提交、合并、推送或签发全量发布 GO。
