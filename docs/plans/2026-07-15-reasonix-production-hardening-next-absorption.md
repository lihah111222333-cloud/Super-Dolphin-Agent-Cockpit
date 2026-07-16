# Reasonix 生产硬化能力下一批吸收计划

> 日期：2026-07-15
> 类型：docs-only、clean-room 行为吸收决策与实施计划
> 目标仓库：`super-agent-v3`
> 参考仓库：`deepseek-reasonix`
> 吸收决策内容状态：`absorption_decision_content_complete=true`
> 实施设计状态：`implementation_design_complete=false`
> P0 执行状态：`p0_executable=false`
> P1/P2 执行状态：`followup_design_required`
> 初稿复核状态：`initial_three_agent_review=completed_with_findings`
> 首次修订稿复核状态：`second_two_agent_review=completed_with_findings`
> 本次对抗复核状态：`third_two_agent_adversarial_review=completed_with_findings`
> Root 对抗复核状态：`fourth_root_adversarial_review=completed_with_findings`
> 第五次双 Agent 对抗复核状态：`fifth_two_agent_adversarial_review=completed_with_findings`
> 第六次双 Agent 对抗复核状态：`sixth_two_agent_adversarial_review=completed_with_findings`
> 第七次双 Agent 对抗复核状态：`seventh_two_agent_adversarial_review=completed_with_findings`
> 第八次双 Agent 对抗复核状态：`eighth_two_agent_adversarial_review=completed_with_findings`
> 第九次双 Agent 对抗复核状态：`ninth_two_agent_adversarial_review=completed_with_findings`
> 当前对象复核判定：`external_exact_sha_review_evidence`（文档自身不内嵌 PASS）

## 0. 结论

本轮不再扩写泛化的“吸收 Reasonix”清单。对当前生产代码复核后，只把两项列为 P0：

1. 独立恢复入口、更新观察期、完整 release-unit 回滚与 Safe Mode。
2. 外部 MCP 工具 Schema 的真实编译、逐工具隔离与稳定诊断。

必须先固定产品事实：v3 的模型执行层依赖第三方 CLI。当前 desktop 主 provider 通过 OpenAI Codex CLI/app-server 执行，另有 Anthropic Claude CLI provider；v3 自研范围是控制面、编排、provider adapter、toolbridge/MCP、状态与恢复、UI、测试和工程治理。集成、随包分发、托管安装或以 v3 package signer 重签第三方二进制，只证明 v3 发行物完整性，不会把第三方 CLI、协议、模型服务、商标、凭据体系或上游知识产权变成 v3 自有资产。任何“原创/自研/知识产权”表述必须明确只覆盖 v3-owned 仓库代码、测试、文档和自有组件，禁止扩大到完整运行栈或发行物。

以下能力保留为后续 tranche，不能与 P0 一起无边界展开：

- P1：统一只读能力诊断、语义进度租约、薄层 Subagent Profile。
- P2：Claude/Codex 插件包兼容预检、first-party writer 写后回执。
- P3：选中文本加入上下文。

`p0_executable=false` 不是否定 P0。Task 0 已在 `origin/main@b40867229af8e17916c00393639ccb0fcb4bf6fc` 建立隔离执行 worktree 并取得 locate、inspect、xref、read、diagnostics 五类新鲜 LSP 证据；Task 0-D0 已清零两个既有 owner 文件的 12 条 LSP diagnostics，并通过聚焦测试、完整影响包测试和仓库门禁。Task 0-Design 已把 release、provider、update、MCP authority、字段守卫和 compiler 冻结到 exact owner/path/schema/test/fixture/budget，但当前仍有两类执行前阻断：

- 当前设计对象尚未提交为 immutable SHA，无法形成可绑定 exact path/line/bytes 的外部 review object。
- 历史九轮 findings 已写回上一提交基线，但任何旧 hash 都不能证明当前对象。当前对象是否通过只由冻结后的 exact path/line/bytes/SHA 外部双 Reviewer 证据判定：P0/P1 必须为零；不影响正确性的 P2/P3 记录后续 tranche 后不阻断。

上述 blocker 全部闭合，并由 Task 0 把本文已固定的 v3-owned/third-party product IP boundary、`ObservedArtifactInventory`、release-member primary/embedded component graph、含release-unit节点的`ReleaseDigestGraph`、`ReleaseAttributionBundleDescriptor`/`ReleaseAttributionTrustPolicy`、`ResolvedProviderExecutable`/`ProviderExecutableLaunchPlan`/`PreparedProviderExec`/`ProviderProcessIdentity`、`ProviderExecutionComponentPolicy`、判别式provider capability evidence与`ProviderCLICompatibilityMatrix`/`ProviderCLICompatibilityTrustPolicy`、`ProviderExecutableAttestation`/`ExecutionLayerFailure`脱敏投影、最早`ProviderIngressAuthority`/`ProviderIngressEnvelope`/`ProviderProtocolGate`/`ValidatedProviderEvent`/`ProviderProtocolDrift`、generation-bound `MCPReadinessAttestation`、`ProviderMCPCapabilityStatus`、provider observation/peer report/`MCPPeerRuntimeAuthority`分离、bundled-only release compatibility preflight、package-owned update trust keyring + package signer policy、transaction-bound trust generation、签名 update-source 引导、probation/普通启动失败分支、normal re-exec environment、pre-healthy write-set transaction、supervisor 有界状态机、Recovery-only backend graph、config-generation-bound MCP trusted carrier、workspace aggregate snapshot、精确`AdmissionGrant`、prepared takeover、authenticated RPC principal/workspace handshake、Codex-only quarantine scope 和逐producer跨 P0 字段守卫冻结到 exact landing files 后，才允许同时切换为 `implementation_design_complete=true` 和 `p0_executable=true`。Task 0 要求冻结具名 RED/GREEN 测试、命令和预期失败断言；真实 fail-first RED、实现 GREEN、最终签名/公证产物和三代 provenance 证据在对应 P0 实现任务中产生并由 P0 Definition of Done 裁决，不能反向作为 Task 0 的前置输入。除 Task 0-D0 外，任何 Agent 不得跳过当前对象复核或 Task 0 直接修改生产代码。

## 1. Review object 与事实基线

### 1.1 对比基线

| Repository | Review object | 状态 | 用途 |
| --- | --- | --- | --- |
| `deepseek-reasonix` | `main-v2@ad9c3fc138b3e7b953405d94b96027b3275c4a50` | 与 `origin/main-v2` 一致、clean | 行为和测试参考 |
| `super-agent-v3` | `origin/main@5482a52cfc256e1ee386dd3ce4e125b01e7dbc85` | 与远端 `refs/heads/main` 一致 | v3 生产事实基线；相对第七轮审查起点只在 package script 更新 SQL LSP bundle 项，第三方 CLI 证据路径未变 |
| docs authoring checkout | `codex/go-ai-gap-push-guards-20260715@8cd4509994f316834a2f974a912cac855566f96a` | 有用户 dirty 文件 | 只新增本文档，不作为实现基线 |

本文不审、不回退、不改写 docs authoring checkout 中既有 dirty 文件。实现者不得把当前分支、当前 index 或用户未提交文件混入未来 P0 worktree。

### 1.2 Reasonix 只作为 clean-room 行为参考

允许参考：

- 可观察行为、失败模式、状态机、不变量、测试场景和用户结果。
- “一个坏 MCP 工具不能拖垮正常同级工具”之类的通用产品约束。
- “更新成功不能等同于新版本健康”之类的发布可靠性约束。

禁止：

- 复制 Reasonix 源文件、函数体、注释、测试文本、用户文案或目录布局。
- 引入对 Reasonix 包、二进制、配置目录、数据库或运行时的依赖。
- 把 Reasonix 的全局 Agent/Todo/Host 所有权搬进 v3，形成第二控制面。
- 因参考实现允许降级，就绕过 v3 的 fail-fast、边界 registry、字段守卫或本地 Git hook。

实现提交必须能从本文的不变量、v3 当前源码和新写的 RED 测试独立推导。

### 1.3 组件所有权与产品表述边界

| Component/能力 | Ownership | 本计划允许声明 | 禁止声明 |
| --- | --- | --- | --- |
| v3 control plane、orchestration、provider adapters、toolbridge/MCP、state/recovery、UI、guards/tests/docs | v3-owned | v3 自研的集成、控制、治理和产品实现 | 不得借此宣称拥有下游 vendor runtime 或模型服务 |
| OpenAI Codex CLI/app-server、CLI protocol/schema、OpenAI service/auth/trademark | third-party | v3 集成或按审定策略分发/调用 OpenAI Codex CLI | 不得称为 v3 自研 CLI、v3 自有协议或 v3 自有模型执行引擎 |
| Anthropic Claude CLI、stream-json/CLI flags、Anthropic service/auth/trademark | third-party | v3 提供 Claude CLI adapter；仅在显式 provider scope 中声明兼容能力 | 不得把 Claude CLI 原生 MCP、事件或能力归入 v3 自研实现 |
| external MCP、LSP server、compiler/dependency | third-party unless separately owned | 仅按来源、许可证、版本和验证边界声明集成 | 不得因 bundling、hash、签名或 adapter 包装改变作者/许可证归属 |

统一 `ProductIPBoundary`/component registry 是产品文案、release manifest、signed SBOM、NOTICE/attribution bundle、About 页面和 evidence 的唯一来源。每项至少包含 `component_id`、`ownership_scope`、`claim_scope`、`origin`、`vendor`、`distribution_mode` 和 `credential_owner`；中英文 README、发布说明或 UI 任一处出现未限定的“完整发行物 100% 自研/知识产权”都必须由动态 product-claim guard 阻断。

### 1.4 已具备能力，禁止重复建设

当前 `origin/main` 已经具备：

- `frontend-app/src/app/commands/appCommandRegistry.js`
- `frontend-app/src/shared/keyboard/shortcutModel.js`
- `frontend-app/src/features/prompt-history/model/promptHistoryController.js`
- `frontend-app/src/shared/diagnostics/frontendPerformancePressure.js`
- `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js`
- MCP per-tool lifecycle 和 Codex dynamic tool `DeferLoading` 路径
- `launch_agent`、持久 Agent、selected skills、恢复和报告控制面

本计划不得重开全局命令、Prompt History、性能压力监控、长历史 materialization，也不得新增第二套 `use_capability` 代理、Todo SSOT 或 Agent scheduler。

## 2. 当前差距的生产证据

### 2.1 更新器只有安装步骤内回滚

v3 当前 `cmd/super-dolphin-updater/install.go` 会在替换前建立 backup，并在复制、结构检查、签名、quarantine 清理或 rename 失败时恢复；但替换成功后立即删除 backup。它能处理“安装过程失败”，不能处理：

- 新版本替换成功但启动前崩溃。
- WebView 首屏出现后很快崩溃。
- 主程序、updater、未来 Guard/launcher 处于不同版本。
- 崩溃循环后需要不加载高风险扩展面的 Safe Mode。

`internal/module/appupdate` 当前拥有 manifest、下载、stage、helper 启动和 RPC；`cmd/super-dolphin-updater` 当前是独立安装进程。新设计必须延续这两个 owner，不得把更新事务塞进前端或 provider。

当前 `internal/module/appupdate/service.go` 从已安装应用自身的 `Resources/bin` 选择 helper，而不是从 staged DMG 选择新 helper；`internal/module/appupdate/manifest.go` 又使用 unknown-field strict decode。于是旧版本既没有 probation supervisor，也会拒绝直接增加 descriptor/protocol 字段的新 manifest。首个事务化版本不能假设旧 updater 已理解新协议，必须显式设计 bootstrap release。

当前 feed 选择也没有代际隔离：appupdate 只读取固定 manifest URL 或 GitHub `/releases/latest`，发布脚本又会把新 release 变成唯一 latest；请求在 strict decode 前不携带可用于路由的 client protocol/version。仅写“channel rollout”无法保证一台从未升级的 pre-P0 客户端在 v2 已发布后仍能找到 bootstrap。legacy/bootstrap feed 必须永久可发现并保持旧 schema，bootstrap 后客户端才切换到物理隔离的 transactional feed；若继续使用 GitHub latest，两代必须位于不同仓库或不同不可互相覆盖的 feed。

即使物理拆成两个 feed，当前 production selector 仍由环境变量决定：package 把 repo/manifest 写进 bundle `.env`，但 `LoadVideoEnv` 能先写入任意环境变量，`loadDotEnv` 又保留已存在值，最终 `appupdate.ProvideConfig` 直接读取进程环境。旧进程环境、用户 `video.env` 或部署脚本中的 legacy repo 因此可以覆盖 bootstrap bundle 的 transactional selector。计划必须冻结 packaged production 的签名 `UpdateSourceDescriptor` 和优先级；环境源不能成为第二个可写 SSOT。

仅把 feed endpoint 移进 descriptor 仍不够：当前 `appupdate.Config.PublicKey` 与 manifest `VerifyOptions.PublicKey` 需要实际 Ed25519 key bytes，package 又把 `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` 写进 bundle `.env`。`public_key_identity` 如果没有 package-owned trust keyring/resolver，只能继续从可覆盖环境取 key，或失去可执行的 manifest 验签输入。descriptor/keyring 必须共同绑定已验证 package/release-unit trust chain，并显式覆盖 bootstrap -> transactional key rotation；identity 不能自己验证自己。

keyring 的上层 package signer authority 仍可被降级：当前 service 从环境读取 `AllowUnsigned`、Windows publisher/thumbprint，helper 又接受裸 `-allow-unsigned`，macOS updater 还允许 `SUPER_DOLPHIN_EXPECTED_TEAM_ID` 覆盖已安装 app identity。manifest 验签即使正确，packaged 环境或直接 CLI 仍可跳过 Team ID/Gatekeeper 或替换 Windows signer 约束。production artifact 必须携 package/release-unit-bound `PackageTrustPolicy`，所有环境/CLI bypass 在 backup/replace 前失败；dev/test bypass 只能存在于物理隔离、不可冒充发布 GREEN 的非发布产物。

keyring anti-rollback 也不能在 candidate 安装时直接提升永久 floor。若 N+1 在 healthy 前成为 committed trust generation，probation 失败恢复 bundle/keyring N 后会被自身判为 downgrade；若完全不持久化，crash/restart 又失去 fencing。必须用 transaction-bound `TrustGenerationState{committed_generation,pending_generation,transaction_id}` 区分 probation 与 healthy：rollback 丢弃 pending，healthy 才以同一提交顺序提升 committed generation并删除 backup。

`cmd/super-dolphin-updater/install.go` 的 `open -n` 成功也不返回受监督 PID；当前前端 bootstrap promise 还可能永久等待。仅写“等待 PID/ACK”没有有界终态，无法处理未注册、错误/过期 ACK 或拒绝退出的卡死进程。

当前 `cmd/agent-terminal/main.go` 在进入 `RunDesktop` 前已调用 packaged runtime、video environment 和 frontend distribution解析；`RunDesktop`在Fx graph前只执行`PrimeProcessEnvironment`和packaged relay config bootstrap。当前`desktop_preflight_test.go`还明确要求desktop preflight不得调用`EnsureCLIAvailable`，真正的bundled/PATH/managed CLI availability探测留在provider start path。只要求selector位于`fx.New`前仍然太晚：sidecar/LSP bundle、`.env`、`video.env`、frontend assets或Codex config bootstrap损坏时，Recovery/Guard仍不可达。修复顺序必须把最小state-root/transaction selector放到这些normal-only preflight之前；另由本计划新增的bundled-only release compatibility preflight在probation healthy ACK前运行，它不得复用会查PATH、自动managed install、读取用户认证/provider-home或访问vendor network的现有`EnsureCLIAvailable`。

但仅把 selector 前移仍不能覆盖“第一次失败”：如果尚无 pending recovery，selector 会选择 normal，随后任一 normal preflight 失败仍可能直接退出；当前 RED 只描述已有 pending recovery 的损坏场景，并禁止 preflight 失败后切换 Recovery。必须在 normal eligibility/preflight 前先持久化 startup attempt，把 typed preflight failure 作为 selector 状态机的一条 fail-closed 转移，而不是依赖第二次偶然生成的 pending 状态。

`failed_preflight` 的动作还必须按 transaction context 分流：active probation candidate 不能留在 Recovery UI 等待 bootstrap-ACK deadline，它必须立即把 exact failure 通知 supervisor/Guard、退出并触发 exact-transaction rollback；只有无 pending probation 的普通首次失败才进入 Recovery/Guard。否则“首次失败可恢复”会与“probation 失败即时回滚”互相冲突。

当前 normal env loader 还是逐行写进进程：packaged derived environment、bundle `.env` 或 `video.env` 的前几项成功、后续失败时，先前的 `os.Setenv` 不会回滚；Go process environment 也没有多键原子 publish。即使文件完整合法，较晚的 frontend/Codex/Fx preflight 失败后，同进程 Recovery 仍会继承 normal PATH、provider home、token 和 update 配置。normal env 必须先形成单一 `NormalProcessEnvPlan`，selector 自身不发布它；验证成功后只能用 transaction-bound re-exec/child `Cmd.Env` 启动 normal candidate，Recovery/Guard 必须保持独立进程与 frozen allowlist。若选择单进程，就必须彻底取消 `os.Getenv/os.Setenv` 作为运行时 contract，而不能继续宣称“一次性 apply”。

环境之外还有 pre-healthy 持久写风险。当前 `runDesktopPreflight -> EnsureCodexBootstrap` 在 healthy 前直接覆盖 app-managed `providers/codex/config.toml`，`os.WriteFile` 成功后若 chmod、Fx start、ACK 或观察期失败，bundle rollback 不会恢复旧 bytes/mode/absence，旧 release 可能继续读取新格式或半写文件。计划必须动态枚举 `PreHealthyWriteSet`，默认禁止 candidate 在 healthy 前改写共享 user/provider/store state；Codex bootstrap 等 writer 必须写 transaction staging/versioned home，healthy 后才 atomic publish，rollback 恢复旧内容或删除升级前不存在的文件。

现有打包脚本读取 previous DMG 主要用于旧公钥，三代包仍可能由当前 checkout/current helper 构建，并受 phase cache 或 smoke retry 影响。因此“旧版 -> bootstrap -> 下一版”只有在三个独立 commit 和不可变 artifact SHA、真实旧 launcher/bundled helper、干净安装边界都被记录时才是跨代证据；同一 HEAD、当前测试 binary、缓存命中或重试后才成功都必须使产物级 E2E 失败。

### 2.2 第三方 CLI 的所有权、发行与兼容边界缺失

当前 README 明确要求已安装并认证的 OpenAI Codex CLI；生产 provider 又会启动 `codex app-server`，Claude provider则直接启动 `claude` 子进程。macOS package还能把 `@openai/codex-*` 原生二进制复制为 `Resources/bin/codex`，非 packaged/managed路径可从 `openai/codex` release安装。以上是第三方执行依赖，不是 v3 自研模型引擎；当前计划此前只约束 Reasonix clean-room和新增schema compiler依赖，没有把CLI ownership写进产品口径、release member或验收证据。

release归属也不能只点名Codex。当前macOS package还整树或动态复制Git/helper、LSP bundle和FFmpeg等第三方member；声明侧`ReleaseUnitDescriptor`、component registry和手写verifier即使彼此一致，也不能证明最终签名、公证、封装后的DMG/ZIP/installer没有多出构建机helper、DLL、dylib或symlink。必须从最终产物独立解包生成canonical `ObservedArtifactInventory{artifact_digest,entries[{path,type,mode,link_target,content_digest}]}`，再与`ReleaseUnitDescriptor.members`做双向等集；签名envelope、平台metadata等排除项必须具名、带owner与reason，禁止声明清单自行证明实物清单。

物理member与软件component也不是一对一。`agent-terminal`可内嵌前端bundle和Go module，Git/FFmpeg/LSP bundle内部还可能含多个上游许可主体；一个primary `component_ref`不能替代依赖/SBOM闭包。`ReleaseUnitDescriptor`每个member除唯一primary `component_ref`外，还必须关联由Go build info、`go.mod/go.sum`、npm lock/bundle metadata、native dependency scan和上游bundle manifest动态派生的`embedded_component_refs[]`/dependency relationships；canonical `ReleaseComponentGraph`计算actual dependency graph与registry/SBOM之间的missing、stale、duplicate和ownership mismatch，registry只允许承载政策/归属补充，不能手写复制派生依赖图。

“有signed SBOM”也不是绑定证据。当前NOTICE只是通用归属提示，仓库还没有release-specific SBOM；若没有无环subject定义、component-registry digest、NOTICE/SBOM digest、generator和可信signer binding，N-1合法签名SBOM可以被重放到N包，签名后NOTICE也可能被替换。必须先冻结canonical `ReleaseDigestGraph`，分别命名payload tree、signed app/bundle、notarized/stapled或平台签名完成后的final artifact，以及置于前序subject外部的release-unit、attribution和update envelope；单向链固定为`payload_tree -> signed_bundle -> final_artifact -> ReleaseUnitDescriptor -> ReleaseAttributionBundleDescriptor -> signed_update_manifest`，箭头只表示后节点绑定前节点exact digest，每个节点明确算法、canonical bytes、排除项、父节点和生成阶段，禁止任何下游/自身digest回写前序subject。`ReleaseAttributionBundleDescriptor`同时绑定payload-tree、final-artifact与`release_unit_digest`，并由package-anchored `ReleaseAttributionTrustPolicy`的独立`release_attribution` key usage验签；payload中的`signer_identity`不是authority，key algorithm/material hash/generation/validity/revocation/predicate type/canonical encoding必须来自可信policy。package verifier必须在平台签名、公证或staple完成后验证整个DAG，而不是只检查文件存在或签名格式。

现有 `codex-manifest.json` 只记录 path、version、asset/source/package hash；运行时 bundled校验不消费version或协议区间，`validCodexCLI` 只运行 `app-server --help`。Codex initialize结果也没有形成上游version/protocol/negotiated capabilities证据，Codex/Claude capability set由v3静态声明。于是一个能打印help、但已改变initialize、`thread/start`、dynamic tools、stream-json或terminal event schema的CLI仍可能进入session后才失败。

即使新增compatibility probe，探测结果也必须与真实exec原子绑定。当前PATH路径先`exec.LookPath`并对absolute path运行help，Codex spawn稍后可能重新构造命令；Claude auth、start、restart又各自解析`CLAUDE_CLI_BIN`/PATH，Codex pool复用键也没有绑定已解析binary和matrix generation，都会形成“descriptor描述B、进程仍执行A”。单个absolute path加post-start校验仍无法保证替代binary零执行；Unix launcher/shebang与Windows npm `.cmd/.bat` shim还可能实际启动解释器或嵌套target。probe必须先生成不可序列化`ResolvedProviderExecutable`和`ProviderExecutableLaunchPlan{entrypoint,chain_nodes[{role,path,file_identity,hash,signature,argv_prefix}],platform_semantics,source_generation}`；平台owner再建立持有已验证OS handle或不可变content-addressed staging的`PreparedProviderExec`，spawn只能从该对象启动，post-start identity仅作纵深防御。spawn返回的`ProviderProcessIdentity`必须贯穿auth/start/restart、Codex pool key/复用判断、session和runtime report；任一chain node、matrix/source/process generation漂移都在第三方代码执行前返回`provider_binary_identity_stale`。若平台无法提供零替代执行的pre-exec primitive，则该模式必须`blocked`，不能把“启动后发现并kill”写成零调用。

capability evidence还必须允许provider差异，不能把所有provider写成同一种negotiation。Codex initialize当前未提供可信CLI version/protocol/capability消费链；Claude静态复制`claudeCapabilities`，且`system:init`只在首条用户消息之后出现，而session此前已ready。canonical evidence必须是`HandshakeEvidence | MatrixEvidence | BlockedEvidence`判别联合，每一variant冻结必填与禁填字段，禁止平面descriptor出现混态。`ProviderCLICompatibilityMatrix`至少绑定provider/component/source kind、OS/arch、launcher class、CLI version range、transport/protocol/schema dialect、required/optional capabilities、trust-policy identity、release/key generation、canonical digest与signature；`MatrixEvidence`只能引用该exact subject，N-1、跨provider/arch/source重放均失败。matrix签名authority只能来自package-anchored `ProviderCLICompatibilityTrustPolicy{usage=provider_cli_matrix,algorithm,key_identity,key_material_hash,generation,valid_from,valid_until,revoked_at,canonical_encoding,allowed_subject_constraints}`，payload自报issuer/signer不能成为authority；bundled subject还必须绑定exact release unit。没有pre-turn handshake的provider只能以该policy验证的immutable matrix证明，version未知、matrix缺项或required capability无证据时session/turn调用数为零。静态map只能是expected set，不能称为negotiated truth。

distribution mode同样混杂：bundled CLI属于v3发行事务的第三方member；user-provided PATH CLI属于用户外部依赖；managed auto-install是独立vendor供应链和独立rollback owner。三者不能共享“找到一个codex就继续”的成功语义，也不能由managed `/latest`、PATH或环境值静默替换packaged truth。必须分别冻结source、精确/允许version、binary/vendor signature、protocol/schema、required/optional capabilities、license/NOTICE/SBOM、credential owner、consent和rollback policy。

release health也不能把责任域压成一个“Codex preflight失败”。bundled member hash/signature/protocol不兼容是candidate-attributable，可阻断active probation并回滚；无pending probation的同类app故障只进入独立Recovery。用户CLI缺失/过旧、登录过期、vendor outage或用户provider-home问题只应typed阻断对应provider，不得增加release crash count或无意义回滚app。反过来，随包CLI损坏不能伪装成用户认证问题。内部不可序列化`ResolvedProviderExecutable`/launch plan可以持绝对路径，但日志、RPC、frontend与可上传evidence只能消费可序列化`ProviderExecutableAttestation{provider,component_id,source_kind,path_class,file_volume_identity_digest,binary_hash,source_generation,process_generation}`；不得泄露HOME、用户名、provider-home路径分段、token、credential store、vendor auth文件或第三方stderr原文，payload/event fingerprint必须先按canonical redacted structural shape生成，或使用受控的非持久keyed digest，禁止对低熵secret/path原文直接做可重放hash。

### 2.3 外部 MCP Schema 只做外形校验

v3 当前 `internal/platform/toolbridge/types.go` 对 tool schema 只验证：

- 空值是否允许。
- 是否为合法 JSON。
- 非空时是否为 JSON object。

`validateMCPTools` 在第一个坏工具上返回错误，整个 `tools/list` 失败；`handler_peer_decode.go` 随后把原始 schema 直接转换成 Codex dynamic tools。当前调用前严格校验只覆盖显式 `additionalProperties:false` 下的未知字段，不等于 JSON Schema 编译。

缺口是：

- 非法嵌套 keyword、错误类型组合和无效 tuple schema 可能穿透外形检查。
- 外部 `$ref` 的资源边界没有被 compiler 级禁止。
- 一个第三方坏工具会使正常同级工具一起消失。
- 状态面没有稳定的逐工具 quarantine code、schema path 和安全摘要。

此外，`internal/module/turn/manifest.go` 当前从 `MCPSnapshot.ServerConfigs` 生成 `MCPBinary` 时没有写入 `TrustedServerID`，而 `internal/provider/shared/config_helpers.go` 的邻近 producer 已写入该字段。若直接启用严格分类，合法 external HTTP/stdio 会因生产 mapper 漂移被误阻断。

即使补成 `map key == Name == TrustedServerID`，字符串相等也不能证明来源：`ServerConfigs` 是无 provenance 的普通 map，managed set 包含 lsp/orch/ida，而现有部分 external 冲突检查只覆盖 lsp/orch。外部配置可以用 `ida` 等保留名进入 producer，再被按 Name 提升为 managed。分类必须消费由 registry/config owner 受控签发的 opaque resolved identity，并在所有外部配置入口拒绝动态完整 managed reserved set，不能只比较三个字符串。

现有逐项 lifecycle backfill 在后续坏工具出现前可能已写入前序项；重复工具名又到 surface projection 才被发现。quarantine identity 目前也没有冻结 workspace/surface scope，无法证明同名 server/tool 在不同工作区不会互相覆盖，或旧 surface release 不会删除新 generation。

当前 toolbridge quarantine generation/lease 只存在于进程内 map，MCP lifecycle store 又以逐行 sqlc 写入；`platform_no_store` 边界禁止 toolbridge 直接依赖 store。SQLite batch 已提交而进程在内存 generation publish 前崩溃时，所谓“同一 lease 收敛”没有 durable identity，重启还会产生 ABA。CWD-only RPC 与 optional SurfaceID 也无法唯一选择 workspace generation，尤其在 symlink、nested workspace、multi-root 或同 CWD 多 surface 场景下。

durable coordinator 还必须遵循另一条现有边界：`platform_no_module` 同样禁止 toolbridge 直接导入 `internal/module/mcp_server`。当前生产链已经通过 `internal/app/runtimeadapter/toolbridge/adapter.go` 把 module Service 投影为 toolbridge 窄端口；若新计划不把 coordinator consumer port、app adapter、module store port 和 production dependency profile 一起冻结，Task 0 仍可能选择一个 archtest 必然拒绝的调用方向。

把 `Origin` 加到普通 JSON DTO 也不能形成 provenance。当前 `MCPBinary` 是可由调用方构造的导出字段集合；若 authoritative origin 随 payload 序列化，external payload 可以直接伪造 managed 值，而分类器从相同字段无法证明构造路径。权威 identity 必须是 registry owner 返回的不可序列化 opaque validated handle，raw manifest 只能作为不可信描述输入。

但 opaque value 本身还需要可信 carrier：当前 turn/provider 链只传 JSON `MCPManifest`，`CodexToolSurfaceScope` 也只持 raw manifest。若把 handle 塞进 DTO，会违反不可序列化约束；若在 toolbridge 终点按 Name/URL/`TrustedServerID` 重建，又重新打开 provenance 伪造。必须由 app 注入的 `ResolveMCPManifest` port 在构造 surface scope 前，依据 authoritative config-owner snapshot + trusted agent/thread reference 生成内存态 `ResolvedMCPManifest`，并让 raw DTO 只参与 snapshot/digest 比对。

签发时 association 仍不足以保证后续 authority。当前配置 owner 可删除/禁用 server，`MCPSnapshot` 和 config provider 又没有 generation/digest；snapshot N 签发的旧 carrier 在 N+1 或 server 删除后仍可能保持内部自洽，并被重挂到另一 workspace/thread。resolved carrier 必须绑定不可序列化的 owner generation/digest、workspace roots digest、agent/thread/session scope；所有 surface/lifecycle 写入和真实 client call 前都必须对 current config-owner membership 做 CAS，配置更新不能只靠 notification 撤销旧 authority。

`prepared/committed/applied` 的现有文字还存在先后矛盾：所有权表要求 coordinator ACK 后 swap，Task 0 却要求先发布 surface 再写 `applied`。SQLite truth 与进程内 map 无法做单次原子提交；计划必须只把 `committed` 作为 durable truth，把 projection generation 定义为可重建的进程内派生状态，并覆盖 reload/swap/notification 失败，而不是用 durable `applied` 假装跨边界原子性。

仅声明 notification failure 后撤下旧 surface 也不够：notification 完全丢失时，进程内 generation N surface 不知道 SQLite 已提交 N+1，在 reconciler 运行前仍可被 catalog/provider/proxy/call 使用。所有可见和可调用入口必须经过 coordinator read port 的 committed-generation barrier；读取失败、`prepared`、generation/digest mismatch 都稳定 fail-closed，不能依赖通知最终到达。

一次 read barrier 还有 TOCTOU：call 可以先读到 committed N并通过校验，在 schema/policy 调度阶段暂停；并发 refresh 提交 N+1 后，它仍可能恢复并执行旧 client N。真实调用需要线性化、单次消费且绑定exact principal/call/tool/schema/lifecycle/peer/client/process/canonical args的`AdmissionGrant`；N+1 commit必须明确等待已经消费并进入旧exact client的grant有界drain，或撤销尚未消费的旧grant。第二次裸read或server级通用lease不能冒充原子性。

generation 的粒度也不一致：durable truth 按 workspace/server 分区，但一个 Codex surface 可以聚合多个 MCP binaries，workspace resolve/token 和 surface 却只有单数 generation/digest。A@N、B@M 的 surface 无法用一个 server generation 表达 B@M+1 或 server membership 增删。必须定义 canonical `CommittedMCPWorkspaceSnapshot{workspace_generation, sorted server_states[], aggregate_digest}`，并让 token、surface、catalog 与目标 call 绑定同一 aggregate/member truth。

`prepared` 目前只有阻断语义，没有恢复终态。若进程在 durable prepared N+1 后、SQL commit 前崩溃，重启后的 read barrier 会永久 not-ready，而文档只定义从 committed 重建。journal 必须支持 `prepared -> committed | aborted | superseded`，绑定 previous committed、owner lease、deadline、plan digest 和 fencing；startup coordinator 先幂等接管/裁决 dangling prepared，不可判定时进入 typed `recovery_blocked`，禁止重跑会覆盖人工 lifecycle 的 backfill。

workspace scope 同样缺少首次握手。当前 RPC 只有 CWD 请求和 tools 响应，新客户端既没有 backend-issued workspace ID，也没有 expected generation；要求第一次请求就携带二者会使合法 fresh client 无法调用，允许客户端自由填写又会破坏 scope trust。必须先由 backend 使用已注册 workspace roots 解析并签发短期 opaque scope token。

握手 token 还缺少不可伪造的调用 session principal：当前 Wails/WS 调用只传 trace context，local `Server.Dispatch` 每次创建嵌套 local server，业务 handler 看不到稳定连接身份。仅在通用 `WSHandler` 内生成 principal 也不安全：Wails origin/cookie guard 位于它的外层，仓库另有 raw `/ws` mount；control transport 又只有 `ctl/register` 成功后才认证。必须由已认证入口注入不可由 params/普通 context helper 构造的 `AuthenticatedRPCConnection` proof，transport 只从 proof 生成 principal；control 只在 register 成功后激活。Wails native path 必须明确 per-window owner + create/destroy revoke，或明确 app-lifetime principal且不宣称窗口隔离，禁止用全局 `Window.Current()` 猜身份。

provider scope必须显式：Codex把v3编译后的schema投影为第三方app-server `dynamicTools`；Claude当前把raw `MCPManifest`写成临时 `--mcp-config`，由第三方Claude CLI直接连接MCP，不经过拟议的toolbridge compiler/quarantine。v3写文件时既不执行`tools/list`/schema validation，也没有在首条用户payload前消费稳定MCP ready/failure信号，因此不能把“server-level fail-fast”写成当前保证。本P0-B只验收Codex toolbridge/proxy/direct route；Claude必须以typed `ProviderMCPCapabilityStatus{per_tool_quarantine,provider_owned_mcp_status,state,readiness_evidence_id,manifest_authority_digest,provider_process_identity,stable_code,sequence}`贯穿provider adapter -> contract/runtime DTO -> orchestration/uistate/eventsurface/RPC -> frontend，报告`unsupported|unproven|checking|failed|stale|blocked`时不得被普通runtime report清除startup overlay或显示ready。Task 0只有在固定CLI subject的真实E2E形成`MCPReadinessAttestation`，并把它绑定exact process/transport identity、target host turn、MCP manifest/authority generation+digest、workspace roots、sorted expected/observed server vector与compatibility matrix digest后，才能开放该exact tuple；start、restart、resume、config/manifest refresh、server增删或任一generation/turn变化都撤销旧attestation，每次MCP-enabled用户payload前必须CAS current tuple。否则user payload调用数为零。该provider observation不能转换为`MCPPeerRuntimeAuthority`。除非另立设计把Claude接入v3-owned proxy/compiled projection，否则不得用Codex GREEN或第三方CLI偶然行为冒充provider-neutral GREEN。

第三方CLI事件协议也属于不可信边界。当前Codex未知raw event和Claude未知stream event可能在decoder/translator前只告警后丢弃，Claude/Codex还会在统一gate之前更新identity/tool/turn状态、执行approval action或发布raw bus；若上游重命名terminal/approval/tool-call事件、删除必填identity或伪造与v3 synthetic相同的事件名，active turn可能挂起、被错误完成或触发内部动作。唯一顺序必须是bounded transport frame/line read -> 保留未知type与坏JSON诊断的最小wire decode -> verified spawn/session owner用私有构造器签发不可序列化`ProviderIngressAuthority{origin_kind,provider,component_id,resolved_executable_identity,os_process_identity,transport_instance_id,process_generation,host_session_binding,issued_epoch}` -> 组装只读`ProviderIngressEnvelope{authority,raw_method_or_type,raw_shape_digest,decoded_payload}` -> `ProviderProtocolGate` -> `ValidatedProviderEvent` -> state/action/raw-bus/translator。只有gate可构造成功路径`ValidatedProviderEvent`，并把ingress authority、host binding、process/transport/ingress generation、protocol、event kind/sequence和唯一typed payload variant带到所有终止consumer；consumer不得从原始payload重建这些字段。authority内部字段不导出，JSON/text marshal/unmarshal失败，raw payload、DTO、普通context helper、public `Dispatch(raw)`和测试手写字段都不能签发或覆盖origin/host identity。任何decoder、`applyRaw`、approval handler、raw bus或translator都不得在gate前丢弃、改状态或执行动作。v3 synthetic/recovery/lifecycle事件使用不同私有issuer的typed internal path，vendor bytes不能声明该origin或进入保留namespace。

未知provider事件默认critical，只有versioned noncritical registry中带event/reason/owner/fixture/provider+protocol range的条目可忽略。`ProviderProtocolDrift`由gate context注入host-owned `agent_id`、public thread、session-manager/process/transport/ingress generation，并按phase区分vendor session/turn字段何时必填；至少支持`pre_session_failure|active_turn_failure|session_fatal`，冻结exactly-once terminal、pending approval/tool清理、transport cancellation、late-generation fencing、late-event拒绝和session移除/拒绝新turn。pre-init没有vendor session ID仍必须精确终止当前host agent一次；旧generation drift不得终止replacement session。证据只记录redacted method/type、`ProviderExecutableAttestation`和redaction-before-digest后的结构摘要，禁止原始stderr、payload、token或绝对provider-home路径。

provider attestation与MCP peer runtime report是不同authority。`ctl/report runtime`只允许peer-owned process/port/provider-presence字段，不能写CLI path/hash/version、capability evidence、MCP readiness/status、execution failure或protocol drift；这些provider-owned字段只能由in-process provider adapter，或另行认证并绑定exact process identity的remote-launcher issuer产生。禁止把两类producer合并到同一可写DTO/mapper后再依赖字段名区分来源。

## 3. 总体不变量

### 3.1 Release Guard 不变量

1. 恢复入口必须在 packaged sidecar/LSP bundle、`.env`、`video.env`、normal frontend assets、Codex bootstrap、Wails、WebView、Fx graph、provider、MCP sidecar、Skill mirror 和用户插件无法启动时仍可运行。入口顺序固定为 minimal executable/state-root resolution -> recovery transaction/startup-state lock/read -> durable `StartupAttempt{attempt_id, process_identity, release_identity, transaction_identity, started_at}` registration -> normal/recovery/Guard selector。selector 自身只能构造 `NormalProcessEnvPlan`，不得向自身 process environment 发布 normal PATH/home/token/source；完整 parse/validate packaged derived env、bundle `.env`、`video.env` 与 inherited env 后，才允许用 transaction-bound re-exec/child 的显式 `Cmd.Env` 启动 normal candidate。任一 preflight 失败都写 stable `failed_preflight`：active probation 立即通知 exact supervisor/Guard、退出 candidate并触发 rollback/reopen old release；无 pending probation 只可由未继承 normal env 的独立 Recovery process/Guard接管。两条分支都不能在已发布 normal env 的同一进程切换 Recovery graph；Recovery 静态资产与 frozen allowlist 不依赖 normal asset/provider/env preflight。
2. 每个平台必须只有一个不可绕过的正常启动裁决链。macOS 固定为 `Info.plist/open/updater restart -> agent-terminal minimal selector -> normal-only preflight -> desktop`；Guard 可以提前拒绝或请求启动，但最终不能绕过 `agent-terminal` selector。direct binary 也必须执行同一 selector。
3. macOS 首次 probation 启动由 detached updater 作为 transaction supervisor：它在 `open -n` 后不得立即退出，必须等待 `agent-terminal` 写入 exact transaction/version/PID ownership，持续观察到 `healthy` commit 或进程异常退出；异常退出触发该 pending transaction 的即时完整回滚并重启旧 release。updater 自身崩溃、主机掉电或监督状态 stale 时，下一次 `agent-terminal` preflight 必须在加载 desktop 前启动 detached Guard recovery worker、传入 exact transaction ID 后退出；worker 完成 restore/parity verification 后才允许重开旧 release。`agent-terminal` 不得在自身仍运行时直接替换其所在 bundle。
4. supervisor 必须持有 exact transaction ID、release version、launch nonce、supervisor lease/identity 和不可仅靠 PID 数字表达的目标进程 identity；至少绑定 PID + process start/create time + expected executable/bundle identity，并按平台冻结可取得字段。它在 `open -n` 后使用独立 registration deadline，注册成功后使用独立 bootstrap-ACK deadline；前者到期进入稳定 `failed_registration`，后者到期进入稳定 `failed_bootstrap`，二者都不能被健康观察窗口替代。wrong/stale nonce、旧 lease、PID reuse 或 executable identity mismatch 必须 fail-closed。
5. deadline 或显式失败后只允许终止该 transaction 登记的 exact PID：先发 graceful stop，再执行有界平台终止策略；确认目标 PID 停止后才允许 rollback。若不能证明身份或无法在期限内停止，进入稳定 `rollback_blocked`，保留 backup 和事务，阻断 desktop 并只开放 Guard CLI/status；禁止按名称、路径猜测或杀死无关进程。
6. updater 协议升级必须显式分两代并物理隔离 feed：pre-P0 永久只访问不可被 v2 latest 覆盖的 legacy/bootstrap feed，该 feed 永远保持旧 schema并只指向 old-manifest-compatible bootstrap release；bootstrap release 引入 transaction-capable helper、`updater_protocol_version`、`minimum_transactional_updater_version`，以及由已验证 `PackageTrustPolicy{platform, signer_identity, team_id_or_publisher, thumbprint, allow_unsigned=false}` + release-unit trust chain 绑定的 canonical `UpdateSourceDescriptor{generation, source_kind, endpoint_or_repo, channel, public_key_identity}` 与 package-owned `UpdateTrustKeyring{generation, keys[{identity, algorithm, public_key, valid_from, valid_until, usage}], signer_identity}`。packaged verifier 先以不可由 env/CLI 覆盖的 OS/package signer policy + release descriptor hash 验证 keyring/descriptor 来源，再按 identity/usage/generation/validity 解析 manifest key并强制 hash parity；descriptor/keyring 不能自签自证。`SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED`、`SUPER_DOLPHIN_EXPECTED_TEAM_ID`、Windows signer env 与 production helper 裸 bypass flag 出现即在 backup/replace 前失败；dev/test bypass 只允许物理隔离非发布 artifact。bootstrap -> transactional key rotation 必须有旧/新 overlap，unknown/revoked/expired/mismatch 和 environment key在网络前失败。其余 feed/protocol/minimum-version/`manual_upgrade_required` 语义保持永久 legacy 与独立 transactional 两代，staged helper 也必须通过同一 signer/descriptor/keyring trust chain。 pre-P0 -> bootstrap 首跳无法由新包反向加固：只有经 OS/platform 独立验签的手工或系统安装可作为安全链起点；旧 helper 自动首跳只计 compatibility/legacy-risk，不计安全自动升级 GREEN。上述新 trust policy 自 bootstrap -> transactional 起生效。
7. 更新替换成功只进入 `probationary`，不能立即删除 backup 或标记提交完成。
8. release unit 是共同提交/共同回滚的整体；不得只恢复主 desktop 而保留不同版本 helper。
9. `ReleaseUnitDescriptor` 必须是声明成员、相对路径、hash、版本、唯一primary `component_ref`、`embedded_component_refs[]`和descriptor identity的唯一权威schema；`ReleaseComponentGraph`从真实Go/npm/native/bundle依赖证据单向生成embedded relationships。最终平台签名、公证、staple/封装完成后，独立解包生成`ObservedArtifactInventory`，并以规范化path/type/mode/link-target/content digest与descriptor做双向等集；`actual - declared`返回`unexpected_release_member`，`declared - actual`返回`missing_release_member`。签名envelope或平台metadata豁免必须具名、带direction/reason/evidence/owner。package、verifier、updater、Guard和transaction只能消费这些canonical producer或其只读投影，禁止共享手写required-list制造声明对声明的假闭环；member/component/dependency missing、stale、duplicate和ownership mismatch全部fail-first。
10. 每个备份成员必须记录 target、backup、预更新存在性和 descriptor identity；恢复后按 descriptor 逐项复验。
11. pending update、rollback、commit 与 `TrustGenerationState{committed_generation,pending_generation,transaction_id}` 必须跨进程序列化。candidate N+1 只写 transaction-scoped pending trust；probation 期间禁止用 pending trust开放新的 Check/Install authority。rollback 丢弃 pending并恢复 committed N；只有 healthy 证据完整后，才按冻结的 file fsync -> durable prepared marker -> journaled atomic publish of release-unit state、write-set parity 与 pending-to-committed trust -> 唯一 final commit marker -> backup deletion 顺序收敛。锁失败、事务损坏、hash mismatch、trust floor冲突或补偿失败都 fail-closed，不能把 rename/ACK 单独当作 durable。 prepared marker 永不表示提交；任一中间 crash 只能恢复旧版或幂等完成同一 transaction。
12. 启动状态至少区分 `starting`、`failed_preflight`、`rollback_requested`、`recovery_required`、`retry_authorized`、`ready`、`healthy`、`failed_registration`、`failed_bootstrap`、`rollback_blocked`、`failed`、`clean_exit`；`failed_preflight` 必须绑定 startup attempt、preflight stage、transaction context、normal env plan hash、pre-healthy write-set journal 和安全错误码，active probation 只能转 `rollback_requested`，无 pending probation 只能转 `recovery_required`。失败 evidence append-only，不能被 clean exit 或下一次 Begin 覆盖；显式 retry 创建带 parent 的新 attempt且不能绕过 pending/mixed/corrupt transaction。只有 exact release/transaction/nonce、PID ownership、supervisor lease、release-unit parity、pre-healthy write-set parity 与观察窗口都通过，才允许进入 `healthy`。
13. registration deadline、bootstrap-ACK deadline、健康观察时间、崩溃窗口和失败阈值是集中、可测试的不可变默认值；不得散落 magic number，也不得由前端静默改写。elapsed timer 或“进程仍活着”不能单独作为健康证明。
14. pending probation crash 的自动回滚属于安装事务创建时已预授权的补偿，只能恢复该 pending transaction 中的精确 release unit；任意历史 snapshot restore、跨 transaction restore 或用户配置修改仍需显式 ID 和确认。
15. Safe Mode 的禁用项必须是 typed allow/deny contract，不能用“尽量少加载”自由解释。minimal selector 包住 normal preflight，并只在独立 process boundary 选择 normal candidate、`RecoveryDesktop` 或 detached Guard；已发布 `NormalProcessEnvPlan` 的 process 不得 fallback 到 Recovery。Recovery/Guard 只消费 executable/state-root/release descriptor/keyring/transaction 和 frozen OS allowlist，禁止 normal `.env`/`video.env`、provider home、token 与 update source。Recovery graph 只允许 logger、recovery transaction/startup-status read model、最小只读 RPC/Wails binding 和独立静态 assets，禁止完整 `app.Module`、normal `BindRuntime`、DB/store、Hook、MCP control、appupdate、Skill、provider 和 toolbridge。前端保持 `recovery_bootstrap -> recovery_ready` 与动作禁用；`recovery_ready` 只来自无 active probation 的独立 recovery process，永远不能提交 backup。
16. 状态文件使用 app-owned state root、原子替换和 owner-only permission；路径、symlink、权限和跨设备 rename 必须有失败测试。
17. 第一版 Guard 不执行模型生成的修复计划，不读取 transcript，不自动改写用户 Skill/Hook/MCP 配置。
18. 所有 candidate preflight 和 normal graph 在 healthy 前必须受动态 `PreHealthyWriteSet` guard 约束，默认对共享 user/provider/store/config 零写入；新增 `WriteFile`、`MkdirAll`、`Rename`、migration、Skill mirror 或 store mutation 未登记即 fail-first。Codex bootstrap 使用 transaction staging/versioned provider home，journal 保存旧 bytes、mode、hash 与升级前不存在性；healthy 后才 atomic publish，rollback 精确恢复或删除。无法证明兼容且可回滚的 pre-healthy writer 必须移到 healthy 后。
19. canonical `ComponentRegistry`是所有ownership/claim/component identity的唯一可写SSOT；`ProductIPBoundary`和`ThirdPartyComponentDescriptor`都是它的只读生成投影，不得成为第二owner。第三方descriptor至少包含`component_id, origin, vendor, ownership_scope, upstream_version, upstream_url, upstream_digest, upstream_signature, package_signer, license_expression, license_text_hash, notice_paths, distribution_mode, protocol_compatibility, credential_owner, rollback_policy`。canonical `ReleaseDigestGraph`以versioned algorithm/canonical encoding定义`payload_tree -> signed_bundle -> final_artifact -> ReleaseUnitDescriptor -> ReleaseAttributionBundleDescriptor -> signed_update_manifest`的无环单向父子关系、排除项与生成阶段；箭头表示后节点绑定前节点exact digest，release-unit/attribution/update envelope位于前序subject外部，不得把自身或下游digest纳入前序subject。`ReleaseAttributionBundleDescriptor{payload_tree_digest,final_artifact_digest,release_unit_digest,component_registry_digest,release_component_graph_digest,observed_inventory_digest,notice_digest,sbom_digest,sbom_format_version,generator_identity,predicate_type,signature}`与release manifest绑定同一exact graph；可信signer只来自package-anchored `ReleaseAttributionTrustPolicy{usage=release_attribution,algorithm,key_identity,key_material_hash,generation,valid_from,valid_until,revoked_at,canonical_encoding}`，payload自报identity不能自签自证。v3 package signer只证明发行完整性；SBOM、NOTICE/attribution、About和中英文产品文案由registry和derived component graph单向生成并做drift/replay guard。origin/license/notice/compatibility任一未知，digest graph有环/阶段错绑，release-unit edge缺失，或N-1/wrong-key attribution replay时release保持BLOCKED。
20. `ProviderExecutionComponentPolicy` 按provider和distribution mode冻结唯一选择：packaged只接受release-unit绑定的bundled CLI且禁止vendor网络/PATH fallback；user-provided只接受显式外部binary并验证identity/version/protocol，不进入v3 release unit；managed默认关闭，只有显式用户同意、不可变vendor release identity、checksum/signature/license/SBOM和独立transaction/rollback齐备时才可安装，禁止启动时跟随`/latest`静默升级或切换来源。Claude默认user-provided，未建立独立managed policy前不得托管安装。 bundled/managed 还必须有 policy owner 针对 exact upstream version、目标平台和 distribution mode 批准的再分发依据；license/NOTICE 存在但不授权该再分发时保持 BLOCKED 或退回 user-provided。
21. compatibility resolver必须产生不可序列化`ResolvedProviderExecutable{provider,component_id,source_kind,canonical_absolute_path,file_identity,volume_identity,binary_hash,upstream_signature,cli_version,source_generation}`和完整`ProviderExecutableLaunchPlan{entrypoint,chain_nodes[{role,canonical_path,file_identity,volume_identity,hash,signature,argv_prefix}],platform_semantics,source_generation}`。平台owner只能据此创建持有已验证OS executable handle或不可变content-addressed staging的单次`PreparedProviderExec`；spawn不得重新按leaf name/PATH/shim解析，post-start check只作纵深防御。spawn返回`ProviderProcessIdentity{resolved_executable_identity,launch_plan_digest,process_generation,os_process_identity,matrix_generation}`并贯穿Claude auth/start/restart、Codex pool key/复用、session与runtime report；复用identity不等或任一launcher/interpreter/target漂移都在第三方代码执行前返回`provider_binary_identity_stale`。无法提供pre-exec零替代执行保证的平台/模式必须`blocked`，不能用启动后kill满足替代binary零调用。 同一 launch operation 的 prepare/spawn/reconcile/close 只有一个 owner；spawn 错误不等于未执行，结果使用 strict `NoEntry | MayHaveEntered | EffectResolved`，无法证明 `NoEntry` 一律按 `MayHaveEntered` 保留 authority/handle fence 并阻断 re-prepare/retry，直到按 OS process identity 完成 reconcile；只有 `NoEntry` 或确认终态才能原子 release。
22. 每个normal provider session必须在任何能力声明或用户turn前形成canonical `ProviderCLIRuntimeDescriptor{provider_process_identity,transport,protocol_schema_version,capability_evidence,compatibility_matrix_identity,auth_state_class,provider_home_identity_class}`。`capability_evidence`是`HandshakeEvidence | MatrixEvidence | BlockedEvidence`判别联合，每种variant有互斥tag及必填/禁填字段；`ProviderCLICompatibilityMatrix`必须绑定provider/component/source kind、OS/arch、launcher class、CLI version range、transport/protocol/schema dialect、required/optional capabilities、trust-policy identity、release/key generation、canonical digest与signature。唯一验证authority是package-anchored `ProviderCLICompatibilityTrustPolicy{usage=provider_cli_matrix,algorithm,key_identity,key_material_hash,generation,valid_from,valid_until,revoked_at,canonical_encoding,allowed_subject_constraints}`；matrix payload中的issuer/signer只作声明，不能选择key或policy。bundled descriptor还绑定exact release transaction/release unit并参与probation/rollback；PATH/managed漂移只阻断对应provider，不能静默换源。Claude在首条用户消息后才出现的`system:init`不能冒充pre-turn negotiation；混态/zero evidence、version未知、matrix exact-subject/policy mismatch、unknown/expired/revoked/wrong-usage signer、N-1/跨provider重放或required capability无证据时session/turn调用数为零。日志、RPC、frontend和可上传evidence只消费不含原始path/provider-home的`ProviderExecutableAttestation`。
23. `ExecutionLayerFailure{component_origin, provider, binary_version, protocol_version, distribution_mode, failure_domain, credential_owner, rollback_eligible, stable_code}` 是startup、provider status、diagnostic和supervisor decision的唯一归因。只有v3-owned candidate缺陷、bundled member integrity failure或bundled protocol incompatibility可令probation release unhealthy；`unauthenticated`、vendor outage、user-provided missing/out-of-range和外部provider-home错误只进入typed provider unavailable，不回滚app、不增加release crash count。credential/token/path只允许脱敏状态，不得进入journal、backup、日志、SBOM或evidence。
24. release compatibility preflight是独立bundled-only静态只读校验，位于probation healthy ACK之前、normal provider启动之前；只校验release-unit绑定binary/launch-chain digest、vendor signature和由package-anchored `ProviderCLICompatibilityTrustPolicy`验证的immutable compatibility matrix，不执行第三方CLI，禁止PATH lookup、managed install、用户auth/provider-home写入和vendor network。当前provider-start `EnsureCLIAvailable`不是该preflight，不能前移复用。只有active probation中的bundled candidate failure进入`failed_preflight -> rollback_requested`；无pending probation的同类app failure进入`recovery_required`并由独立Recovery/Guard接管，PATH/managed/auth/vendor failure不进入release health。

### 3.2 MCP Schema 隔离不变量

1. v3 自带的一方 MCP 仍 fail-fast；只有显式通过v3-owned toolbridge compiled projection的trusted external MCP才允许逐工具quarantine，本P0当前provider范围仅为Codex。Claude raw `--mcp-config`路径报告`per_tool_quarantine=unsupported`和`provider_owned_mcp_status=unsupported|unproven`；没有version-bound稳定ready/failure证据时，MCP-enabled turn在用户payload前阻断，不得宣称server-level fail-fast。canonical app-owned `ResolveMCPManifest(trusted owner reference, current MCPManifestAuthoritySnapshot, raw MCPManifest)` 是唯一签发入口。`MCPManifestAuthoritySnapshot` 至少含 owner generation/digest、canonical workspace/roots digest、agent/thread/session binding 和 server membership；私有构造器一次性生成 scope + `ResolvedMCPManifest`，caller 不能分别拼接 carrier 与 scope。identity 内部字段不导出，JSON/text marshal/unmarshal 报错，handle 不进入 DTO/config/log/store/session。所有 lifecycle/quarantine/surface 写入与 client factory/call 前都通过 config-owner current-snapshot port 对 generation/digest/membership/scope 做 CAS；删除、禁用或 N->N+1 更新原子撤销旧 carrier并返回 `manifest_authority_stale/revoked`。managed reserved set、raw origin、stdio allowlist和 HTTP egress继续独立 fail-closed。
2. 处理顺序固定为 app resolve authority-bound manifest/scope -> config-owner freshness CAS -> transport wire envelope decode only -> canonical class/trust resolution -> batch identity validation -> per-tool schema compile -> class policy barrier -> durable refresh transaction -> workspace aggregate committed snapshot -> candidate reconcile/swap -> lifecycle filter -> catalog/provider projection -> current peer runtime authority -> exact single-use `AdmissionGrant` -> client call。HTTP/stdio `ListTools` 只返回 `[]json.RawMessage`；identity invalid 对所有 class 在写入前整批失败，managed schema invalid 零写入，trusted external 只在身份全批稳定后逐工具 quarantine。任何入口不得跳过 authority CAS、aggregate barrier、peer authority或grant。
3. schema 必须先 canonicalize，再 compile；空 schema 只能规范化为严格的空对象 schema。compiler 输出唯一 `CompiledToolSchema`，至少包含 canonical JSON、draft、digest 和 typed diagnostic；catalog、provider dynamic tools、proxy 和 call validator 只能消费同一 digest 对象，禁止重新序列化或各算一份 schema hash。
4. provider-visible 根节点必须为 object；union/non-object root 不得被静默改写成 object。
5. compiler 必须禁止 file/network/自定义 loader；外部 `$ref` 一律拒绝。
6. 编译预算必须在 Task 0 冻结单 schema pre-parse bytes、decoded node/depth/internal-ref count、单 server tool count、全批 schema bytes、diagnostic count/summary bytes、compile isolation/cancellation 和 elapsed 上限；不得用返回后仍运行的 goroutine 伪装 timeout。
7. quarantine 不能改变合法同级工具的 name、description、schema 或 lifecycle 状态。
8. 被隔离工具继续出现在本地只读诊断投影中，但绝不能进入 catalog 的 enabled surface、provider-visible dynamic tools、proxy 或 call schema。
9. 本地 quarantine 不写 peer DTO。durable identity 为 workspace + trusted server + normalized tool，per-server state 进入 canonical `CommittedMCPWorkspaceSnapshot{workspace_generation, sorted server_states[{server_id,generation,digest,state}], aggregate_digest}`；SurfaceID 不入 durable identity，surface release不删 diagnosis。workspace roots按 realpath/case/volume/symlink/nested/multi-root规则规范化。首次 principal-bound resolve 返回 `{workspace_id, workspace_generation, aggregate_digest, opaque_scope_token, expires_at}`，token绑定 principal + aggregate snapshot；后续 RPC只接受 token + expected workspace generation/digest。server新增/删除/refresh必须推进 aggregate truth，map顺序不影响 digest；host/skill-only entry显式 N/A，不能伪造 MCP generation。
10. 诊断只保存稳定 code、server/tool identity、安全 schema path 和受限摘要；不得回传完整 schema、secret、绝对路径或第三方 stderr 原文。
11. refresh 后 schema 修复必须恢复工具；schema 重新损坏必须撤下工具，不能依赖进程重启。
12. lifecycle owner保持唯一；handler经 consumer port/app adapter调用 module coordinator。journal 固定 `prepared -> committed | aborted | superseded`，prepared 绑定 previous committed generation、owner lease/deadline、plan digest和 fencing。单一 SQLite transaction/outbox同时提交 manual-preserving batch、quarantine、per-server state、workspace aggregate snapshot和 committed transition。startup coordinator必须先接管 dangling prepared：证明未开始则 CAS aborted恢复 previous committed pointer，证明已提交则读取 committed，不可判定转 typed `recovery_blocked`；两进程 takeover中旧 lease/token失败，禁止重跑 backfill。toolbridge只持 committed 派生 projection，notification只加速，reload/swap/crash后从 aggregate committed truth幂等重建。
13. catalog/provider/proxy/call 使用 surface 前验证 target server member + workspace aggregate snapshot。参数必须按Task 0冻结的canonical JSON规则深拷贝、schema validate并计算digest；真实副作用call再由同一线性化owner签发不可序列化、原子单次消费的`AdmissionGrant{grant_id,host_call_id,principal_id,workspace_id,server_id,normalized_tool_id,compiled_schema_digest,lifecycle_version,peer_runtime_authority_id,client_instance_id,process_generation,member_generation,aggregate_digest,canonical_arguments_digest,admission_epoch,expiry,fencing_token}`。exact client入口重新核对同一不可变canonical args bytes/digest及grant中的tool/schema/peer/client/process/current epoch后才执行，不能换call、tool、args、schema、alias或client。N+1 commit必须有界drain已经消费并进入旧exact client的grant，或撤销尚未消费的旧grant；不能用第二次裸read或server-level lease替代原子性。旧refresh token/process/lease/generation/grant CAS失败，grant replay/复制/重复消费失败；surface release不删durable diagnosis，rollback只从old aggregate committed snapshot重建。 admission owner 还必须以 `(principal_id, host_call_id)` 维护唯一 active-or-terminal reservation，不得为同一逻辑调用签发第二个 grant；client 结果使用 strict `NoEntry | MayHaveEntered | EffectResolved`，只有 `NoEntry` 可新签，`MayHaveEntered` 必须保留 reservation 并先按 exact client/call reconciliation。
14. `RPCSessionPrincipal` 只能由 server-only `AuthenticatedRPCConnection` proof签发。Wails `http_server` 完成 origin/cookie token guard后注入 proof；raw `/ws` 无同等级 auth时不得暴露 scope方法。control transport仅在 `ctl/register` 成功后激活 principal。Wails native local path必须冻结 per-window create/destroy/revoke，或明确 app-lifetime principal且不声称窗口隔离；generic local Dispatch、params或普通 context helper不能造 proof/principal。close/reconnect/auth失效撤销 token，cross-principal使用失败。
15. P0-B的per-tool compile/quarantine只覆盖Codex app-server dynamic-tools及其v3-owned toolbridge/proxy/direct route。Claude CLI的`--mcp-config`直连路径必须生成typed `ProviderMCPCapabilityStatus{per_tool_quarantine,provider_owned_mcp_status,readiness_evidence_id,manifest_authority_digest,provider_process_identity,state,stable_code,sequence}`并贯穿provider -> contract/runtime DTO -> orchestration/uistate/eventsurface/RPC -> frontend；state只允许`unsupported|unproven|checking|ready|failed|stale|blocked`，非current `ready`不能被普通runtime report清除startup overlay或显示ready。只有固定CLI subject的真实E2E生成`MCPReadinessAttestation{provider_process_identity,process_generation,transport_generation,resolved_executable_identity,target_host_turn_id,manifest_authority_generation,manifest_authority_digest,workspace_roots_digest,sorted_server_statuses[{server_id,config_digest,state,evidence_digest}],compatibility_matrix_digest,event_sequence,evidence_id,issued_at,expiry}`，expected server集合与current authority snapshot完全一致，且每次MCP-enabled payload前CAS exact current tuple，才可放行。start/restart/resume/config或manifest refresh、server增删、target turn或process/transport/matrix换代立即撤销旧attestation；否则user payload调用数为零。产品UI、capability report、测试矩阵和DoD不得把Codex结果或第三方CLI偶然行为泛化为Claude或全部provider。若未来选择Claude v3-owned proxy，必须另立review object、compatibility contract和真实CLI E2E。
16. Codex/Claude第三方事件只能按bounded transport frame/line read -> minimal wire decode -> verified spawn/session owner私有签发不可序列化`ProviderIngressAuthority` -> `ProviderIngressEnvelope` -> `ProviderProtocolGate` -> `ValidatedProviderEvent` -> state/action/raw-bus/translator顺序流动；minimal decode必须把坏JSON和未知type作为gate输入，不得先warn/drop。authority绑定不可由payload覆盖的origin、provider/component/executable、OS process/transport instance、process generation和host session binding，内部字段不导出且JSON/text roundtrip失败；raw payload、普通DTO/context/public dispatch不能构造。只有gate可构造canonical `ValidatedProviderEvent{ingress_authority_digest,origin_kind,provider,component_id,host_session_binding_id,host_turn_id,process_generation,transport_generation,ingress_generation,protocol_version,event_kind,event_sequence,typed_payload_variant,redacted_shape_digest,noncritical_registry_generation}`；每个event kind固定一个typed payload variant及其必填/禁填字段，state/action/raw-bus/translator只能消费该结构化输出，不得回读raw payload补字段。v3 synthetic/recovery/lifecycle事件使用不同私有issuer和独立internal publish，内部保留event namespace不能由第三方wire声明。gate之前禁止identity/tool/turn mutation、approval/action执行、pending resolve、raw bus或translator发布。
17. `ProviderProtocolGate`把validated第三方事件分为critical terminal/approval/tool-call/auth/capability与versioned non-critical registry；未知事件默认critical，非critical exemption必须绑定provider+protocol range、event、reason、owner和fixture。canonical schema固定为`ProviderProtocolDrift{provider_executable_attestation,ingress_authority_digest,origin_kind,host_agent_id,public_thread_id,host_session_binding_id,host_turn_id,session_manager_generation,process_generation,transport_generation,ingress_generation,protocol_version,event_class,event_digest,event_sequence,phase,provider_session_digest,provider_turn_digest,decision,termination_scope,stable_code,redaction}`，至少支持`pre_session_failure|active_turn_failure|session_fatal`。`host_*`、public thread和四类generation只能由current ingress authority/binding owner注入，始终用于终止目标选择；provider session/turn只允许是redaction后的诊断digest，并按phase规定何时允许缺失，绝不能参与cleanup/terminal路由。坏JSON、未知/缺字段critical event、auth schema变化、required capability缺失必须以`host_session_binding_id + host_turn_id + session_manager/process/transport/ingress generation`为exactly-once key失败：清理pending approval/tool、取消transport、fence late generation、拒绝迟到success覆盖、移除或封禁session并拒绝新turn；pre-init无vendor identity仍按host identity精确终止一次，旧generation drift不得终止replacement session。结构摘要必须在redaction后生成，不记录原始payload/stderr/secret/path。
18. provider-owned CLI attestation/runtime/capability/MCP status/execution failure/protocol drift与MCP peer `ctl/report runtime`是两个独立producer和authority。peer wire DTO只允许process/port/provider-presence等peer-owned观测字段；任何CLI identity、version、matrix、readiness、status、failure或drift字段都必须拒绝或忽略，不能经通用mapper覆盖provider adapter产生的canonical状态。只有in-process provider adapter或另行认证且绑定exact `ProviderProcessIdentity`的remote launcher issuer可写provider-owned链。
19. `MCPReadinessAttestation`只是指定第三方provider process对指定manifest/turn的观测，不能签发、转换或替代v3 call authority。只有v3-owned peer/client registry与refresh coordinator可基于current committed member和真实client lease私有签发不可序列化`MCPPeerRuntimeAuthority{authority_id,workspace_id,server_id,manifest_generation,manifest_digest,workspace_generation,server_generation,server_digest,peer_instance_id,client_instance_id,transport_identity,process_generation,lease_epoch,fencing_token,expires_at}`；它是`AdmissionGrant`必需输入。Codex toolbridge client只在current v3-owned peer/client链取得authority；Claude direct `--mcp-config`固定只有provider observation，不能取得`MCPPeerRuntimeAuthority`或v3 call grant。

### 3.3 P1 控制面不变量

- 能力诊断只能聚合现有 Skill、provider mirror、MCP lifecycle/toolbridge、Hook 和 capability manifest owner；默认 static、无网络、无子进程、无写入。
- 语义进度必须以 v3 的 durable receipt、DAG node、有效 diff、唯一 host work 和 review/evidence 为事实，不得把 Todo 文本变化当作进展。
- Subagent Profile 只能是 canonical Skill 元数据到现有 `launch_agent` 的投影，不得新增 runner、scheduler、agent store 或并行生命周期。Profile-only 字段使用 namespaced typed block；现有 Skill `tools/allowed-tools` 白名单不能与 `launch_agent.disabled_tools` 暗中互相覆盖，唯一 projector 必须定义合成和冲突 fail-fast 规则。

## 4. 目标所有权与代码落点

### 4.1 P0-A：更新恢复与 Safe Mode

冻结落点。精确文件、schema v1、具名测试、命令与预计生成物见 `docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md`：

| Path | Owner/职责 | 约束 |
| --- | --- | --- |
| `internal/module/appupdate/releaseunit/` | canonical `ComponentRegistry`、`ReleaseUnitDescriptor` primary/embedded refs、`ReleaseComponentGraph`、`ObservedArtifactInventory`、`ReleaseDigestGraph`、生成式`ProductIPBoundary`/`ThirdPartyComponentDescriptor`、`ReleaseAttributionBundleDescriptor`/`ReleaseAttributionTrustPolicy`、`ProviderCLICompatibilityTrustPolicy`、`UpdateSourceDescriptor`、`UpdateTrustKeyring`与`PackageTrustPolicy` schema/version/identity和验证 | 最终产物实物与声明双向等集；derived dependency graph与SBOM闭包；digest DAG无环、含release-unit单向节点且阶段明确；attribution/matrix signer都来自package trust，不得self-declare；user-provided CLI不入release unit但其matrix仍须package-anchored policy授权；package signer不改变作者归属 |
| `internal/module/appupdate/recovery/` | startup attempt/status、update transaction、`TrustGenerationState`、`NormalProcessEnvPlan`/`PreHealthyWriteSet` journal、hash/restore、锁和路径 contract | attempt、pending trust、write-set staging在 normal candidate前持久化；不导入 Wails、Fx、provider、store、toolbridge |
| `internal/module/appupdate/service.go`、`manifest.go`、`github_release.go` 与 tests | packaged mode 只消费已验证 source/keyring/package policy；dev/test override只来自物理隔离 artifact | legacy feed保持旧 schema；环境 source/key/signer/unsigned全部冲突失败；unknown/revoked/expired/mismatch在网络前失败 |
| `cmd/super-dolphin-updater/` | 消费已准备 transaction + package trust policy；安装失败写 durable failure；成功保留 backup并监督 probation到 healthy trust promotion或 rollback | production helper拒绝裸 `-allow-unsigned`/env Team ID；healthy前不提升 committed trust、不删除 backup |
| `cmd/super-dolphin-guard/` | `check`、`launch`、`recover`、`status`、`restore` 的最小 CLI；承担 stale transaction 的 detached restore worker | 不加载 desktop graph，不执行 AI repair；restore 必须在 agent-terminal 退出后运行 |
| `cmd/agent-terminal/main.go`、`internal/app/app.go` 与窄 bootstrap adapter | minimal selector构造 env/write plans；用 transaction-bound re-exec/child启动 normal candidate；active probation rollback、无 pending启动独立 Recovery、显式 retry创建 parent attempt | 已发布 normal env的进程不得 fallback Recovery；parent selector environment不变；Recovery assets不依赖 normal frontend/provider |
| `internal/app/recovery_module.go`、`internal/ui/wails/recovery_module.go` 与 graph tests | 独立 `RecoveryDesktop` Fx/Wails composition，只装配恢复状态、只读 RPC、显式 retry authorization 和静态 assets | 只消费 selector 冻结参数与环境 allowlist；禁止 `app.Module`、normal `BindRuntime`、DB/store、provider、Skill、MCP/toolbridge、update check/install constructors |
| `internal/contract`、`internal/module/appupdate/rpc.go` | typed StartupAttempt/Safe Mode/status/health-ACK/retry contract 与严格 RPC | attempt/preflight failure/rollback/retry/ACK 必须绑定 release + process/transaction/parent-attempt identity；未知状态 fail-fast |
| `frontend-app/src/entities/client/model/runtimeSlice.js`、helpers、`useClientStore` tests | 在 normal bootstrap 前分流 typed `recovery_bootstrap -> recovery_ready`，不读取 provider/project/sidebar/thread 状态 | `recovery_ready` 不得复用 normal `ready` 或发送 healthy commit ACK |
| `frontend-app/src/features/update-recovery/` 与 `frontend-app/src/App.jsx` composition/tests | 持久 Safe Mode banner、恢复状态、禁用正常产品动作；Safe Mode 跳过 update check/install | typed projection 缺失时不得回退成正常 UI |
| `internal/archtest/backend_boundary_registry*.go` | 登记新 command surface 和允许的单一 recovery seam | 禁止 broad `internal/module` allowlist |
| `internal/platform/runtimeenv/runtimeenv.go`、`internal/platform/config/config.go` 与 tests | 生成覆盖 packaged derived env、`.env`、`video.env`、inherited env 的 immutable `NormalProcessEnvPlan`；只投影给 child `Cmd.Env` | parent selector零 normal env写入；并发 observer无部分状态；Recovery/Guard只继承 frozen allowlist |
| `internal/provider/shared/`、`internal/contract/runtime_reporter.go`、`internal/dto/agent/runtime.go`、`internal/app/runtime_reporter_adapter.go`、orchestration/uistate/eventsurface/RPC/frontend provider-status consumers 与 tests | provider-owned `ProviderProcessIdentity`、判别式capability evidence、`ProviderCLIRuntimeDescriptor`、`ProviderMCPCapabilityStatus`、`ExecutionLayerFailure`、`ProviderProtocolDrift`唯一投影 | in-process/认证launcher issuer-only；所有provider字段动态透传；unknown/missing/stale阻断；公开投影只用`ProviderExecutableAttestation`和脱敏class |
| `internal/dto/mcp/protocol.go`、`internal/platform/mcpcontrol/report_handlers.go`、`cmd/mcp-orch/orchestration/runtime.go` 与 peer report tests | MCP peer-owned runtime report，只描述peer process/port/provider-presence | peer携带CLI identity/version/matrix/readiness/status/failure/drift字段必须拒绝或忽略；不得成为provider attestation第二owner |
| `internal/provider/shared/` launch resolver、compatibility verifier、`internal/provider/codexapp/{codex_autoinstall.go,codexmanifest/,pool_spawn_cmd.go,transport_helpers.go,server_pool.go,driver_pool_routing.go}`、`internal/app/app.go` 与 pre-healthy/compatibility guard/tests | Codex bootstrap写transaction staging/versioned home；产生`ResolvedProviderExecutable`/launch plan/prepared exec；以package-anchored `ProviderCLICompatibilityTrustPolicy`验证exact matrix；bundled-only release静态preflight与provider-start分离；spawn返回process identity并把它纳入pool key/reuse | packaged禁网络/PATH fallback；preflight不执行第三方CLI；self-declared/wrong-usage/expired/revoked matrix signer失败；pool process identity/matrix generation mismatch必须drain+respawn；healthy/rollback验证exact v3+CLI pair；旧config parity |
| `internal/provider/claudecli/{config.go,auth_preflight.go,transport.go,transport_unix.go,transport_windows.go,session_log_watcher_integration.go,driver.go}` 与 auth/start/restart/真实CLI tests | Claude auth、start、restart只消费同一`PreparedProviderExec`/`ProviderProcessIdentity`，完整绑定Unix launcher/shebang与Windows npm shim/target chain | 禁止每阶段重解PATH或shim；auth A/start B、restart generation漂移、未登记解释器/target均在执行前fail-close；默认仍是user-provided third-party CLI |
| `README*.md`、`NOTICE`、signed SBOM/attribution、About/release-note projection 与 product-claim guard | 从canonical `ComponentRegistry`单向生成`ProductIPBoundary`、第三方descriptor和许可证据 | 删除ownership/claim scope、把third-party写成v3自研、缺vendor/license/notice均fail-first；多语言drift失败；生成投影不能反写registry |
| `cmd/super-dolphin-release-manifest/`、package/publish/verify scripts 与 guard tests | 在平台签名/公证/staple完成后独立解包生成`ObservedArtifactInventory`，派生component graph并生成含release-unit节点的无环digest DAG、可信attribution envelope、source/keyring/package/matrix trust policy；构建校验Guard与物理隔离feed | actual/declared双向差集；update manifest按单向链绑定payload-tree/final-artifact/release-unit/registry/component-graph/inventory/NOTICE/SBOM和trusted attribution key；覆盖额外member、内嵌依赖、self-cycle、缺release-unit edge、阶段错绑、N-1/wrong-key/tamper、rotation/revocation、unsigned bypass |

Windows 当前已有直接执行 installer 的生产更新入口，不能只标记文档 `unsupported`。Task 0 必须形成平台能力矩阵：仍能发布/检查/安装更新的平台必须实现同等 probation/commit/rollback 契约；做不到的平台必须同时禁用配置、Check/Install 和 manifest artifact 生成，并返回稳定 unsupported code。Linux 同样不得用 macOS package-smoke 冒充支持。

### 4.2 P0-B：MCP Schema 编译和隔离

冻结落点。为遵守现有文件/包预算，新增 shared contract 使用 `internal/contract/mcpcontrol/` 子包，schema/admission 使用 `internal/platform/toolbridge/{schema,admission}/` 子包；精确文件和测试见 Task 0 design-freeze evidence：

| Path | Owner/职责 | 约束 |
| --- | --- | --- |
| `internal/platform/toolbridge/schema/{compile.go,policy.go,quarantine.go}` | canonicalize、resource limits、compile、stable error classification和quarantine plan | 纯 schema contract，不决定 lifecycle；root toolbridge负责应用 lifecycle |
| `internal/platform/toolbridge/types.go` | 将 server-level decode 与 tool-level validation 分开 | 一方/第三方策略必须显式输入 |
| `internal/module/turn/manifest.go`、`internal/module/turn/service_helpers.go`、`internal/module/thread/mcp_server_config.go`、`internal/module/thread/start_session_helpers.go`、`internal/provider/shared/config_helpers.go`、`internal/contract/manifest.go`、`internal/dto/provider/manifest.go` | raw `MCPBinary` 只传不可信描述和 trusted config-owner/agent/thread reference；不跨 DTO“保持” opaque handle | DTO 不得出现 authoritative Origin/handle；`TrustedServerID` 只可派生；JSON/runtime-config roundtrip 后不得凭 raw 值恢复 authority |
| `internal/contract/mcpcontrol/{authority.go,refresh.go,admission.go,rpc.go}` | opaque identity/resolved manifest、`MCPManifestAuthoritySnapshot`、current authority read/CAS port、refresh/admission/RPC proof contract | owner generation/digest/workspace/agent/thread/session binding不可序列化；config删除/禁用/换代撤销旧 carrier |
| `internal/app/runtimeadapter/toolbridge/adapter.go`、app manifest resolver、Codex session/support/toolsurface/contract 与 start/resume tests | 私有构造器用 current authority snapshot一次性生成 resolved manifest + scope；pre-write/pre-call CAS current membership | raw/snapshot mismatch、scope重挂、post-sign update/delete、resolver bypass均零写入零 client；handle不进 wire/config/store/log |
| `internal/platform/toolbridge/http_mcp_client.go`、`stdio_mcp_client.go`、`types.go` 与 tests | `ListTools` 只做 wire envelope decode，返回含 `[]json.RawMessage` 的 typed result；移除 class resolution 前的整表 unmarshal/validation seam | transport 不决定 quarantine；畸形 envelope 仍 server-level fail-fast |
| `internal/platform/toolbridge/handler_peer_decode*.go`、`proxy.go`、`handler.go`、`module.go`、`admission/{grant.go,arguments.go}` 与 surface/dependency/concurrency tests | authority CAS、schema plan、aggregate snapshot barrier和统一call admission；schema validation后以current `MCPPeerRuntimeAuthority`取得绑定exact principal/call/tool/schema/lifecycle/peer/client/process/canonical args的单次`AdmissionGrant`并跨越到client call | barrier后/CallTool前并发commit只能drain exact旧client调用或撤销；换绑、args mutation与grant replay/重复消费全部RED；failure零写入零client |
| `internal/platform/toolbridge/protocol_contract.go` | 保留调用参数的 strict-object 检查 | 不把 unknown-field 检查误写成完整 schema validator |
| `internal/module/mcp_server/lifecycle.go`、refresh coordinator、store/migration/sqlc 与 restart tests | durable refresh唯一 owner；journal terminal state、per-server state、workspace aggregate snapshot、call admission owner | 单事务提交 batch/quarantine/server+aggregate/committed；dangling prepared takeover/abort/supersede幂等；无 durable applied |
| `internal/contract/mcpcontrol/admission.go`、v3-owned peer/client registry、refresh coordinator、app authority adapter 与 tests | 基于current committed member+exact peer/client/process lease私有签发不可序列化`MCPPeerRuntimeAuthority` | provider readiness不能转换；Claude direct路径无authority；peer/client/process/lease/fence换代立即revoked |
| `internal/app/runtimeadapter/toolbridge/adapter.go`、tests 与 Fx dependency tests | 适配 refresh、authority current read、aggregate snapshot read和 admission ports | platform不导入 module/store；nil port、route bypass和 production dependency fail-first |
| `internal/platform/toolbridge/schema_quarantine.go`、surface store/route/catalog/provider/proxy/call tests | 从 `CommittedMCPWorkspaceSnapshot` 重建 candidate；每个入口验证 aggregate/member，call取得并消费exact `AdmissionGrant` | notification只加速；多server变化、TOCTOU、tool/schema/client/args漂移、notification loss期间零 stale/错client call |
| `internal/contract/toolbridge.go`、`internal/contract/mcpcontrol/rpc.go`、app RPC、rpc server/WS/control/module、`internal/ui/wails/http_server.go`/binding/window lifecycle 与 tests | `AuthenticatedRPCConnection` proof、`RPCSessionPrincipal`、workspace aggregate resolve/token和 committed projection | Wails guard/control register后才签发；raw `/ws`/generic Dispatch失败；冻结 per-window owner 并在 close 时 revoke |
| `internal/provider/shared/`、unified dispatcher、Codex/Claude transport decoder/session ingress/approval/tool cleanup 与 protocol fixtures | verified spawn/session owner私有签发不可序列化`ProviderIngressAuthority`并组装`ProviderIngressEnvelope`；唯一`ProviderProtocolGate`在任何drop/state/action/raw-bus/translator前产出`ValidatedProviderEvent`；内部synthetic event走独立私有issuer | provider wire/DTO/context/public dispatch不能伪造origin/host binding/internal namespace；未知type与坏JSON必须到gate；删除/后移gate或gate前副作用均RED |
| `internal/provider/codexapp/{driver,event_map,session*,support,transport*,pool_spawn_cmd}.go` 与 compatibility/protocol fixtures | 只把v3已编译surface投影为第三方app-server dynamic tools；消费`ProviderProcessIdentity`和判别式handshake/matrix evidence；host generation路由critical drift | unknown terminal/approval/tool event不能warn/drop；pre-init与late generation精确失败；Codex GREEN只代表Codex scope |
| `internal/provider/claudecli/{driver,transport_config,session_events,auth_preflight,session_turn,session_log_watcher_integration}.go` 与 capability/protocol/真实CLI tests | Claude直连typed投影`ProviderMCPCapabilityStatus`；每个start/restart/resume/config generation生成并CAS exact `MCPReadinessAttestation`；host generation路由critical stream/auth drift | 不接入P0 quarantine；unsupported/unproven不显示ready；旧attestation或无new tuple ready时MCP-enabled user payload为零；post-first-message `system:init`不能冒充pre-turn negotiation |
| `internal/module/turn/mcp_server_config_test.go`、thread/start/resume、app resolver、registry/field guard/roundtrip tests | 锁定 config owner snapshot/ref -> app resolver -> opaque handle/binary association -> surface/classifier 链 | 动态枚举 managed registry/producer 字段，校验 resolver bypass、JSON serialization failure、snapshot mismatch、reserved set、zero/tampered handle 和 raw origin claim fail-first |
| `go.mod`、`go.sum`、NOTICE/SBOM 相关 owner | 如引入 compiler 依赖，记录版本、license、漏洞和供应链证据 | 当前 hooks 没有该 gate；Task 0 未新增 versioned fail-first gate 前保持 BLOCKED |

禁止把 schema compiler 放进 provider-specific adapter；Codex和未来显式接入v3-owned toolbridge projection的provider必须消费同一过滤结果。Claude当前直连路径不在此集合，保持明确`unsupported|unproven`；没有稳定ready/failure屏障时必须阻断MCP-enabled turn，不能静默宣称兼容或server-level fail-fast。

### 4.3 跨 P0 结构化字段守卫

下列每一行只允许一个具体producer truth或一个明确方向的registry producer；不得把多个DTO、descriptor、journal、proof或token合成一条。producer数量不是固定验收目标，Task 0通过LSP/AST/schema发现新producer时必须新增独立行。每行都动态枚举生产字段或registry value；消费registry只用于登记，必须由mapper AST、类型约束、SQL/schema、roundtrip/one-hot和真实mutation RED证明实现覆盖。没有单一全仓scanner时逐行建立versioned guard，不能用任一行GREEN或“字段守卫已通过”覆盖其它producer。

| Producer truth | 消费链终止边界 | 必须的 guard |
| --- | --- | --- |
| `ComponentRegistry` | registry owner -> release/component/product projections | 反射/schema枚举；missing/stale component、duplicate ID与无效ownership mutation |
| `ProductIPBoundary` | generated projection -> README/release note/About/product claim | generator-only、registry反向写禁止、多语言字段差集与claim-scope mutation |
| `ThirdPartyComponentDescriptor` | generated projection -> NOTICE/SBOM/About/release evidence | vendor/version/license/notice/protocol动态字段、unknown与stale component RED |
| `ReleaseUnitDescriptor` | generator -> package/verifier/updater/Guard/transaction | member字段动态枚举、primary/embedded refs、path/type/hash roundtrip及消费者逐个mutation |
| `ObservedArtifactInventory` | final artifact extractor -> release verifier | actual/declaration双向差集、type/mode/link/hash mutation、具名豁免反查stale |
| `ReleaseComponentGraph` | buildinfo/lock/native/bundle derivation -> SBOM/component verifier | dependency missing/stale/duplicate、derived graph禁止手写、真实新增/删除依赖RED |
| `ReleaseDigestGraph` | platform package stages -> attribution/update verifier | node/edge/phase/algorithm动态枚举、cycle/self-inclusion、错阶段与final artifact mutation |
| `ReleaseAttributionBundleDescriptor` | attribution generator -> update manifest/package verifier | payload/final/release-unit/registry/graph/inventory/NOTICE/SBOM digest、predicate/signature字段、缺edge/N-1/cross-artifact/tamper roundtrip |
| `ReleaseAttributionTrustPolicy` | package trust owner -> attribution verifier | algorithm/key/usage/generation/validity/revocation字段、self-declared/wrong-key mutation |
| `ProviderExecutionComponentPolicy` | policy registry -> source selector/install owner/provider status | provider/source动态枚举、distribution one-hot、consent/rollback owner、silent switch/latest RED |
| `ResolvedProviderExecutable` | resolver -> launch-plan builder | 字段动态枚举、不可序列化、path/file-volume/hash/signature/version/generation mutation |
| `ProviderExecutableLaunchPlan` | launch-plan builder -> prepared-exec owner | chain node字段和顺序、launcher/interpreter/target完整性、未登记node/stale node RED |
| `PreparedProviderExec` | platform pre-exec owner -> spawn | opaque handle/staging identity、single-use与generation CAS；禁止path roundtrip恢复authority |
| `ProviderProcessIdentity` | spawn -> auth/session/restart/pool/runtime report | process/launch/matrix generation动态字段、pool reuse mismatch与deep-copy/zero mutation |
| `ProviderExecutableAttestation` | process identity redactor -> log/RPC/frontend/evidence | allowlisted公开字段、原始path/HOME/provider-home/secret排除与snapshot mutation |
| `ProviderCapabilityEvidence` | handshake/matrix/blocked producer -> runtime decision | variant tag one-hot、各variant required/forbidden字段、混态/zero mutation |
| `ProviderCLICompatibilityMatrix` | signed matrix owner -> provider compatibility decision | subject/provider/source/OS/arch/launcher/version/protocol/capability/trust-policy/signature字段、N-1/cross-subject RED |
| `ProviderCLICompatibilityTrustPolicy` | package trust owner -> matrix verifier/provider compatibility decision | usage/algorithm/key identity-material hash/generation/validity/revocation/subject-constraint字段、payload self-declare与wrong-key mutation |
| `ProviderCLIRuntimeDescriptor` | provider adapter -> contract/agent DTO/status | producer/mapper字段差集、process/evidence/matrix identity、unknown/missing阻断 |
| `ProviderMCPCapabilityStatus` | provider adapter -> orchestration/uistate/eventsurface/RPC/frontend | quarantine/readiness/status字段动态透传；enum外输入必须fail-closed为`unproven|blocked`，不得映成ready |
| `MCPReadinessAttestation` | provider-specific readiness owner -> pre-payload CAS | executable/process/transport/manifest/matrix tuple、restart/refresh revoke、replay RED |
| MCP peer runtime report DTO | authenticated peer report -> peer runtime projection | 仅peer process/port/provider-presence字段；CLI attestation/readiness/status/failure/drift注入与stale mapper RED |
| `ExecutionLayerFailure` | provider/bootstrap -> supervisor/status/frontend/evidence | failure-domain/rollback-eligibility差集、bundled-vs-user/auth/outage mutation、secret排除 |
| `ProviderIngressAuthority` | verified spawn/session owner -> ingress envelope builder | opaque origin/executable/process/transport/host binding字段、私有issuer、serialization/context/public-dispatch伪造RED |
| `ProviderIngressEnvelope` | bounded frame/minimal decode -> protocol gate | authority/raw type/shape/payload字段、unknown/bad JSON保留、vendor synthetic namespace与gate-bypass RED |
| `ValidatedProviderEvent` | protocol gate -> state/action/raw-bus/translator | authority/host binding/generation/protocol/event-kind/sequence/variant字段动态枚举、raw重建/missing-stale/variant混态与终止consumer mutation |
| versioned noncritical-event registry | registry owner -> protocol gate | provider/protocol/event/reason/owner/fixture动态枚举、unknown与stale exemption RED |
| `ProviderProtocolDrift` | protocol gate -> session cleanup/fence/runtime/RPC/frontend | ingress-authority/event-sequence、host session/turn与provider diagnostic digest分域、四类generation/phase/termination字段、pre-init与late-generation/exactly-once mutation |
| `PackageTrustPolicy` | package owner -> verifier/updater/Guard | signer/platform/allow-unsigned字段、env/CLI bypass与wrong-signer artifact RED |
| `UpdateSourceDescriptor` | package owner -> appupdate/updater | source/generation/key identity字段、env override、tamper与network-before-fail mutation |
| `UpdateTrustKeyring` | package owner -> manifest verifier | key material/hash/algorithm/usage/validity/revocation字段、rotation/replay RED |
| `TrustGenerationState` | update transaction -> selector/updater/Guard | pending/committed/transaction字段、healthy promotion/rollback/crash roundtrip |
| `StartupAttempt` | selector -> supervisor/Guard/RPC/frontend | identity/parent/stage/state字段、active-probation/no-pending branch与append-only mutation |
| `NormalProcessEnvPlan` | runtimeenv owner -> normal child `Cmd.Env` | source/allowlist/hash字段、parent zero-write、partial parse与deep-copy mutation |
| `PreHealthyWriteSet` journal | writer registry -> transaction/healthy publish/rollback | writer/path/old bytes-mode-hash-absence字段、unregistered writer与crash roundtrip |
| Recovery projection | recovery owner -> RPC/frontend | recovery state/reason/capability字段、normal-ready混用与missing projection RED |
| health ACK | normal frontend -> supervisor transaction | release/transaction/nonce/PID/parity字段、missing/stale/wrong-owner mutation |
| `MCPManifestAuthoritySnapshot` | config owner -> app resolver/current CAS | owner generation/digest/membership/scope字段、post-sign revoke/cross-scope replay |
| `ResolvedMCPManifest` | app private resolver -> Codex tool-surface scope | opaque carrier/binary association、serialization/roundtrip/bypass mutation |
| dynamic managed-server registry | registry owner -> external-entry guards/classifier | reserved value动态枚举、Unicode/case collision与stale guard RED |
| refresh journal | module coordinator -> store/sqlc/startup takeover | terminal/previous/lease/deadline/plan/fencing字段、crash/two-owner CAS mutation |
| per-server committed state | coordinator -> store/sqlc/aggregate builder | server/generation/digest/state字段、sorted vector membership mutation |
| `CommittedMCPWorkspaceSnapshot` | aggregate builder -> app read ports/surface/token | workspace/vector/generation/digest字段、order/membership/roundtrip mutation |
| `MCPPeerRuntimeAuthority` | peer/client registry + coordinator -> admission issuer | current member/peer/client/process/lease/fence字段、provider-readiness conversion与Claude-direct no-authority RED |
| `AdmissionGrant` | admission owner -> exact client call | principal/call/tool/schema/lifecycle/peer/client/process/canonical-args/expiry/fence字段、deep-copy、single-use/replay与换绑RED |
| quarantine row | coordinator -> store/diagnostic/reconcile | workspace/server/tool/generation/code字段、manual lifecycle preservation与stale row RED |
| `AuthenticatedRPCConnection` | Wails/control auth boundary -> principal issuer | proof字段不可由params/context伪造，raw WS/register-before-auth/local Dispatch RED |
| `RPCSessionPrincipal` | authenticated connection -> workspace resolver/token issuer | connection/owner/lifecycle字段、close/reconnect/cross-principal mutation |
| workspace scope token claims | token issuer -> RPC validation/admission | principal/workspace/aggregate/expiry字段、tamper/stale/replay mutation |
| catalog DTO | committed projection -> RPC/frontend catalog | enabled tool/schema/generation字段、quarantine/stale surface omission mutation |
| diagnostic DTO | durable diagnosis -> RPC/frontend diagnostic | stable code/safe path/summary/generation字段、secret/raw schema exclusion mutation |
| `CompiledToolSchema` | compiler -> lifecycle/catalog/provider/proxy/call validator | canonical JSON/draft/digest字段、consumer registry、recompute/stale/deep-copy mutation |

每项豁免都必须登记 `Field | Direction | Reason | Evidence/Owner`；无法动态枚举、missing、stale、无效豁免或 mutation 不红时，Task 0 保持 `BLOCKED_PLAN_REVISION`。

## 5. 执行拓扑

Task 0 只能由 Integrator 串行完成。Task 0 闭合后允许两个隔离实现 lane：

| Lane | Scope | 可并行条件 | 禁止修改 |
| --- | --- | --- | --- |
| A Release Guard | appupdate/recovery、updater、guard、desktop bootstrap、packaging、third-party component/CLI distribution policy | Task 0 已冻结 ownership、release unit、compatibility和failure attribution | toolbridge、MCP lifecycle、provider session/event实现 |
| B MCP Schema | toolbridge schema compile/quarantine、MCP lifecycle batch/store、DTO、Codex-only projection/Claude scope tests、依赖 | Task 0 已冻结compiler/字段/budget/refresh transaction和provider scope | appupdate、packaging、orchestration、CLI distribution owner |
| Integrator | boundary registry、shared generated artifacts、最终 hooks、证据和文档 | A/B 都提交独立 commit 后串行 | 不重写 lane 内已验证实现 |

`go.mod/go.sum`、`internal/archtest/backend_boundary_registry*.go`、`internal/contract/provider*.go`、`internal/provider/{codexapp,claudecli,shared}` compatibility/protocol seams、生成 codemap/project-map 和本文档是 shared seams，只允许 Integrator 串行修改。若 Lane B 需要新增 dependency或触碰provider seam，先由Integrator提交compatibility/protocol contract并在Lane A后串行；不得并行制造go.sum、runtime descriptor或provider event冲突。

P1/P2 不与 P0 并行实施。它们必须在 P0 合并并重放门禁后各自建立新的 review object 和执行文档。

## 6. P0 逐任务清单

### Task 0：执行基线、LSP、边界和依赖冻结

Task 0 分为设计冻结和一个受限前置 lane：

- `Task 0-Design` 只冻结 owner、exact landing path、schema/version、producer、消费者、具名 RED/GREEN 测试、命令、预期失败断言、fixture 和预计生成物，不实现 P0 行为。Task 1 至 Task 4 开始时必须先运行对应具名 RED 并保存真实失败证据，再进入最小实现和 GREEN。
- `Task 0-D0` 仅允许修改 `cmd/super-dolphin-updater/install.go`、`internal/module/appupdate/service.go` 及必要的同包测试，用于清零 Task 0 基线记录的 12 条既有 LSP diagnostics。每个 unused symbol 必须先做 xref；修改不得新增 schema、改变外部行为或提前实现 P0；完成条件是两个目标文件 diagnostics 为零、聚焦测试通过并通过 `make guard`。D0 完成不自动切换任何 executable 状态。

`Task 0-D0 status: COMPLETE`。逐符号 xref、修改、零 diagnostics、聚焦/影响包测试和门禁证据记录在 `docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md`。`Task 0-Design status: DESIGN_FROZEN_PENDING_REVIEW`，精确 contract 记录在同目录 `01-task0-design-freeze.md`；在 immutable SHA 与双 fresh review 闭环前仍保持 `implementation_design_complete=false`、`p0_executable=false`。

- [ ] 从最新 `origin/main` 创建隔离 worktree，记录 base/head SHA、branch、status、index 和已有 worktree。
- [ ] 证明 current origin/main 中本文 §1.4 的能力仍存在，防止重复实现。
- [ ] 冻结canonical `ComponentRegistry`和生成式`ProductIPBoundary`/component ownership table：v3-owned只覆盖本仓库控制面、编排、adapter、toolbridge/MCP、状态恢复、UI、测试与治理资产；OpenAI Codex CLI/app-server、Anthropic Claude CLI及其它外部组件保持各自第三方ownership。打包、签名、安装和品牌呈现不得改变归属；README/NOTICE/SBOM/About/release note/多语言声明只可由registry单向生成，投影不得反写。
- [ ] 冻结从 final signed/notarized/stapled DMG/ZIP/installer 独立解包生成 subject-external `ObservedArtifactInventory` 的 owner、输入、输出、schema/version、verifier、具名 fixture 和双向等集测试；冻结从 Go build info、`go.mod/go.sum`、npm lock/bundle metadata、native scan 和 bundle manifest 生成 `ReleaseComponentGraph` 并与 registry/SBOM 做 node+relationship 双向差集的同类契约。具名排除项必须冻结 schema version、direction、owner、reason 与 fixture；不得用 package 声明或手写 required list 生成 observed truth。真实最终产物闭包证据在 P0 实现完成条件中产生。
- [ ] 冻结无环`ReleaseDigestGraph`及每个subject、canonical bytes、algorithm、排除项、父节点、生成阶段和signature-excluded signing input；固定`payload_tree -> signed_bundle -> final_artifact -> ReleaseUnitDescriptor -> ReleaseAttributionBundleDescriptor -> signed_update_manifest`单向链及`release_unit_digest`，冻结package-anchored `ReleaseAttributionTrustPolicy`及`release_attribution` usage。禁止self/downstream reference、双向digest互绑和payload自报key成为authority；final verifier在平台签名/公证完成后运行，缺release-unit edge、N-1 replay、NOTICE/SBOM tamper、wrong/expired/revoked/wrong-usage signer均RED。
- [ ] 冻结`ThirdPartyComponentDescriptor`、`ProviderExecutionComponentPolicy`、`ResolvedProviderExecutable -> ProviderExecutableLaunchPlan -> PreparedProviderExec -> ProviderProcessIdentity`、严格`HandshakeEvidence | MatrixEvidence | BlockedEvidence`、`ProviderCLICompatibilityMatrix`/`ProviderCLICompatibilityTrustPolicy`、`ProviderCLIRuntimeDescriptor`、`ProviderExecutableAttestation`、`ProviderMCPCapabilityStatus`、`ExecutionLayerFailure`、`ProviderIngressAuthority`/`ProviderIngressEnvelope`/`ValidatedProviderEvent`、`MCPReadinessAttestation`、`MCPPeerRuntimeAuthority`、`ProviderProtocolDrift`的唯一owner、landing path、schema/version和消费者。
- [ ] 冻结第三方 CLI 兼容矩阵的具名 RED、命令和预期失败断言：Codex bundled/PATH/managed 的 compatible、too-old、too-new、unknown、protocol drift 和 silent-latest/source-switch；Claude missing、out-of-range、auth expired、vendor outage、pre-turn evidence missing 与 critical event schema drift。每个 evidence variant 严格校验必填/禁填字段；matrix 绑定 provider/component/source/OS/arch/launcher class/version/protocol/schema/capability/trust-policy exact subject，并只接受 package-anchored `provider_cli_matrix` usage 授权的 algorithm/key material/generation/validity/revocation；payload self-declared、wrong/expired/revoked/wrong-usage signer和跨subject重放均须由 RED 覆盖。禁止用`--help`、硬编码capability或post-first-message `system:init`冒充兼容；managed install必须显式同意并固定不可变上游版本。
- [ ] 冻结pre-exec identity：resolver生成完整launcher/interpreter/target chain，platform owner建立已验证handle或immutable staging的`PreparedProviderExec`；Claude auth/start/restart和Codex pool spawn/reuse只消费同一`ProviderProcessIdentity`。prepare/spawn之间替换任一chain node、PATH、shim target、file ID、source/matrix/managed generation必须在第三方代码执行前返回`provider_binary_identity_stale`且替代binary零调用；无法提供该primitive的平台模式保持`blocked`。
- [ ] 对 appupdate、updater、agent-terminal bootstrap、packaging、toolbridge tools/list、dynamic tool projection 和 lifecycle filter 完成 LSP locate、inspect、xref、read、diagnostics 五类证据。
- [ ] 检查 `internal/module/appupdate`、`internal/platform/toolbridge`、`cmd/*` 的文件/函数预算；不得用新增大文件绕过既有预算。
- [ ] 验证每个平台 canonical launch authority；macOS package guard 锁定 `Info.plist -> agent-terminal preflight`，updater restart 和 direct binary 都必须进入同一 gate。
- [ ] 冻结 updater protocol、feed rollout、keyring 与 package signer SSOT：记录 strict decoder、固定 latest、env source/key/signer/unsigned 和 current helper行为；定义 `PackageTrustPolicy`/source/keyring/release schema、OS/package/release anchor、key rotation/revocation、signer Team ID或 publisher/thumbprint、与 descriptor hash绑定。packaged env/CLI bypass全部失败；dev/test bypass只在物理隔离 artifact。永久 legacy/bootstrap与独立 transactional feed、minimum version和 `manual_upgrade_required` 保持两代协议。
- [ ] 用状态图及具名 RED、命令和预期失败断言冻结 macOS 双触发 supervisor：detached updater 监督首次 probation 并在硬崩溃后即时 exact-transaction rollback；updater/主机中断时，下次 `agent-terminal` preflight 只负责启动 detached Guard worker 并退出，worker restore/parity/reopen。不得把“下次用户启动才恢复”误写成首次 crash 的即时自动回滚。
- [ ] 冻结 supervisor identity 与有界失败：transaction/version/launch nonce/lease、PID + process start/create time + executable/bundle identity、registration deadline、bootstrap-ACK deadline、graceful-stop 与 bounded termination policy，以及 `failed_registration`、`failed_bootstrap`、`rollback_blocked` 的稳定 code/状态迁移；逐平台证明 PID reuse 和不可取得 identity 时 fail-closed。
- [ ] 冻结`PackageTrustPolicy`/`ComponentRegistry`/`ReleaseUnitDescriptor`/`ReleaseAttributionBundleDescriptor`/`UpdateSourceDescriptor`/`UpdateTrustKeyring`/`TrustGenerationState`owner/path/schema/version、生成时点和package/verifier/appupdate/updater/Guard消费链。pending generation只绑定transaction，healthy才提升committed，rollback丢弃pending；各producer按§4.3独立动态守卫。
- [ ] 冻结 app state root、lock/fsync、StartupAttempt/parent、failure branch、`NormalProcessEnvPlan`、Recovery allowlist、`PreHealthyWriteSet`、health ACK和transaction identity。minimal selector不改 normal env；验证后以 re-exec/child显式 Env启动 candidate。active probation rollback、无 pending独立 Recovery、retry lineage保持；动态枚举 Codex bootstrap/migration/Skill mirror/provider/store等所有 pre-healthy writer，未登记即阻断。
- [ ] 冻结平台能力矩阵；Windows 当前更新入口若不能满足 P0，先用测试阻断 config、Check/Install 和 manifest artifact 发布。
- [ ] 冻结新增 `cmd/super-dolphin-guard` 未登记时失败的 fail-first archtest 名称、路径、命令和预期失败断言；真实 RED 在引入 command 的实现任务开始时运行，再最小扩展 canonical registry；不得 broad allowlist。
- [ ] 实证 MCP authority/carrier SSOT：config owner提供带 generation/digest/membership的 current `MCPManifestAuthoritySnapshot`；app私有 resolver一次性生成 scope + resolved manifest并绑定 workspace roots digest、agent/thread/session。所有 pre-write/pre-call 路径CAS current owner；签发后 config删除/禁用/N->N+1、跨 scope重挂、resolver bypass、serialization恢复authority、reserved collision均 RED。
- [ ] 实证最早 decode seam：HTTP/stdio `ListTools` 在 class resolution 前只解 wire envelope，`tools` 保存为 `[]json.RawMessage`，既不整表 unmarshal 成 `[]MCPTool`，也不调用整表 `validateMCPTools`；landing-file 清单必须包含两个 client、`types.go`、client interface、handler pipeline 和同包测试。
- [ ] 冻结provider scope与typed状态链：P0 per-tool quarantine只承诺Codex app-server消费的v3-owned toolbridge projection；Claude当前`--mcp-config`直连产生`ProviderMCPCapabilityStatus`并贯穿provider/contract/agent DTO/orchestration/uistate/eventsurface/RPC/frontend，`unsupported|unproven`不得映成ready，enum外输入必须fail-closed为`unproven|blocked`。`MCPReadinessAttestation`绑定exact process/transport/executable/manifest-authority/matrix tuple，start/restart/resume/refresh换代即撤销，每个MCP-enabled payload前CAS；无exact current attestation时user payload为零。
- [ ] 冻结最早`ProviderProtocolGate`、成功路径`ValidatedProviderEvent`与`ProviderProtocolDrift`：unknown type与坏JSON必须在minimal decode后由verified spawn/session owner私有签发不可序列化`ProviderIngressAuthority`并进入`ProviderIngressEnvelope`，任何drop/state/action/raw-bus/translator都在gate之后；raw payload/DTO/context/public dispatch不能签发origin/host binding，vendor不能伪造internal synthetic namespace。gate输出固定authority/host binding/generation/protocol/event kind/sequence/typed variant并由全部终止consumer动态守卫，consumer不得从raw补字段。drift绑定host agent/public thread/session-manager/process/transport/ingress generation，pre-init无vendor ID仍exactly-once终止当前agent，旧generation不得误伤replacement；删/后移gate、删任一validated mapper字段或cleanup/fence分支都RED。
- [ ] 冻结provider observation、peer report与v3 call authority三域：`ctl/report runtime`只允许peer process/port/provider-presence；CLI identity/version/matrix/readiness/status/failure/drift字段必须拒绝或忽略，不能覆盖in-process provider adapter状态。`MCPReadinessAttestation`不能转换为call authority；`MCPPeerRuntimeAuthority`只由v3-owned peer/client registry+coordinator基于current committed member、exact client/process lease私有签发，Claude direct路径签发计数为零。三条producer/mapper/DTO/consumer字段守卫分别建立。
- [ ] 冻结 batch identity、durable refresh、aggregate snapshot和精确call admission：journal为 `prepared -> committed|aborted|superseded`，prepared带 previous committed/lease/deadline/plan digest/fencing；startup takeover幂等裁决 dangling prepared。单事务提交 lifecycle、quarantine、server state、sorted workspace aggregate snapshot和 committed。catalog/provider验证 aggregate/member；副作用call持绑定call/tool/compiled-schema/lifecycle/client/args的单次`AdmissionGrant`，commit有界drain exact已admitted旧client调用或撤销旧grant。notification只加速；换tool/args/schema/client、grant replay、多server membership、barrier后/CallTool前TOCTOU、旧token/process全部RED。
- [ ] 用 LSP 定位 store/sqlc/fixture/app adapter/surface/call route与dependency profile。新增 authority current read、journal terminal/takeover、server+workspace aggregate snapshot和 admission API位于 module/store boundary，经app adapter注入；nil port、route bypass及 platform/module/store archtest全部RED。
- [ ] 冻结 workspace ownership、`AuthenticatedRPCConnection` proof、principal和首次握手：Wails http guard后注入 proof，raw `/ws`无同级 auth不得resolve；control register成功后激活；native path明确 per-window revoke或 app-lifetime语义。resolve token绑定 workspace aggregate generation/digest + principal。两连接、register前、raw WS、generic Dispatch、owner close/reconnect、multi-server stale均RED。
- [ ] 按 §4.3 为全部 P0 producer 分别冻结字段守卫 owner、动态 producer/registry 枚举源、mapper/SQL/RPC/frontend/schema consumer、missing/stale/roundtrip/deep-copy 测试及每条链至少一个真实 mapper/consumer mutation RED 的具名测试、命令和预期失败断言。真实 mutation RED 在对应字段首次实现前运行。禁止只守 opaque MCP identity 就声称 D17 全覆盖；无效 exemption 必须阻断并接入对应 versioned gate。
- [ ] 对候选 JSON Schema compiler 做 license、维护状态、external loader 禁用能力、Draft 支持、资源边界和依赖体积裁决；冻结单 schema bytes/node/depth/ref、单 server tool count、全批 schema bytes、diagnostic count/summary bytes、elapsed 数值和 cancellation/isolation 方案；冻结 `CompiledToolSchema` canonical JSON/draft/digest 的唯一 producer 与 catalog/provider/proxy/call 消费 guard。
- [ ] 当前 hooks 不含 license/SBOM/vulnerability gate；若新增 dependency，必须先批准 exact pinned tool/version/command/output owner 并新增 versioned fail-first gate，否则保持 `BLOCKED_PLAN_REVISION`。
- [ ] 若任一LSP类别、final artifact observed/declared闭包、actual dependency graph/SBOM闭包、无环release digest/可信attribution、product/IP ownership、third-party license/NOTICE/SBOM、pre-exec launch-chain/process identity、严格capability union/matrix subject、公开attestation脱敏、bundled-only release preflight、Claude generation-bound MCP readiness/typed状态、最早provider ingress gate/host drift routing、provider-peer authority分离、package signer/keyring/trust generation、permanent feed、normal re-exec environment、pre-healthy writer transaction、三代provenance、config-current-bound carrier、aggregate snapshot、prepared takeover、精确`AdmissionGrant`、authenticated principal/token、§4.3逐producer动态guard、release-unit或compiler loader政策无法冻结，记录`BLOCKED_PLAN_REVISION`并停止生产修改。

Task 0 交付：

- 独立 evidence 文件，绑定 exact worktree/base/head。
- landing-file 清单、owner、字段清单、focused test 命令和预计生成物。
- 每条实现链的具名 RED/GREEN 测试、命令、fixture 和预期失败断言；真实 RED/GREEN 运行记录由对应 P0 实现任务追加，不得用尚不存在的最终产物阻断设计冻结。
- `implementation_design_complete=true` + `p0_executable=true`，或保留 blocker 的明确裁决；不接受自由文本“基本可做”。

### Task 1：Release transaction RED 与纯状态包

- [ ] 先写 RED：成功替换后 backup 仍存在且 transaction 为 probationary。
- [ ] 先写 RED：release unit 缺少一个成员、hash 错误、路径越界、symlink、权限错误、跨设备替换或锁失败时 fail-closed。
- [ ] 先写 RED：rollback 必须恢复全部旧成员，并删除升级前不存在的新成员。
- [ ] 先写 RED：rollback 任一成员失败后执行补偿；补偿也失败时写 mixed-install 状态并阻断 launch。
- [ ] 先写 RED：descriptor 新增/删除/换 hash 任一成员时，package、签名 manifest verification、install prepare 和 verifier 必须共同 fail-first；禁止第二份成员清单漂移。
- [ ] 先写RED：descriptor固定后在final artifact额外复制未声明helper/dylib/DLL/symlink时，`ObservedArtifactInventory - ReleaseUnitDescriptor`报告exact path；声明但实物缺失，或type/mode/link-target/content digest变化时反向差集失败。verifier必须实际解包平台签名/公证完成后的exact artifact，不能读取声明清单冒充observed truth。
- [ ] 先写RED：最终Go binary、frontend bundle或native/helper tree新增生产依赖，但`ReleaseComponentGraph`/registry/SBOM缺node或relationship时失败；移除实际依赖后stale release-scoped关系也失败。同一物理member内两个第三方component必须都进入SBOM。
- [ ] 先写RED：动态增加任一第三方fixture member但不增加`component_ref`/component descriptor时，package/release guard报告exact member；补登记后GREEN，删除、stale、duplicate或ownership mismatch均RED。Codex、Git、LSP bundle、FFmpeg等真实member都从release descriptor枚举，禁止手写“已知第三方”数组。
- [ ] 先写RED：任一third-party member缺`origin`、`vendor`、`license_expression`、`license_text_hash`、`notice_paths`、`distribution_mode`或`protocol_compatibility`时，release manifest、package、NOTICE/SBOM和About共同fail-first；package signer、v3签名或复制进bundle都不得改写ownership。 即使这些字段齐全，bundled/managed 仍须证明 policy owner 已批准 exact version/platform/distribution mode 的再分发；许可证不允许或依据缺失时必须 BLOCKED/user-provided。
- [ ] 先写RED：`ReleaseDigestGraph`节点换序、canonicalization/algorithm变化、descriptor把自身/下游digest纳入subject、signature字段进入自身签名输入、payload-tree/final-artifact/release-unit阶段错绑或`release_unit_digest`缺失、N-1 envelope、跨平台artifact replay、self-declared/unknown/expired/revoked/wrong-usage attribution key均在发布前失败；只有package-anchored trust policy验证且沿exact单向链绑定release unit的N graph GREEN。
- [ ] 先写 RED：旧 strict decoder 对新 descriptor/protocol 字段的实际拒绝被锁定；从未升级的 pre-P0 client 在 transactional v2 已成为 latest 后仍只能从永久 legacy feed 获得 old-compatible bootstrap，bootstrap 之后才切到 transactional feed；单一 latest、legacy feed 出现新字段或低版本误取 v2 必须失败，transactional 低版本返回稳定 `manual_upgrade_required`。
- [ ] 先写 RED：bootstrap artifact 的签名 `UpdateSourceDescriptor` 指向 transactional feed；同时注入 legacy process env、bundle `.env`、app-data `video.env` 和部署变量后，packaged app仍只使用 descriptor，产生稳定 conflict diagnostic 且不回退 legacy。descriptor hash/signature/source kind/endpoint 任一 tamper 必须在网络请求前失败；trusted dev/test override 独立测试。
- [ ] 先写 RED：`UpdateTrustKeyring` 的 key material、identity、algorithm、usage、validity、generation、signer 任一 missing/stale/tamper 都在网络前失败；unknown/revoked/expired key、`hash(public_key) != public_key_identity`、环境 key覆盖、descriptor/keyring generation rollback和不满足重叠窗口的 bootstrap -> transactional rotation 均失败。真实旧/新 key overlap内轮换成功，且 descriptor/keyring不能自签自证。
- [ ] 先写 RED：packaged env注入 unsigned/Team ID/publisher/thumbprint或直接向 production helper传 bypass flag均在 backup前失败；wrong signer/ad-hoc DMG和 Windows signer mismatch在 manifest GREEN时仍失败，只有独立 dev artifact可 bypass。
- [ ] 先写 RED：N -> pending N+1 -> pre-healthy rollback后 committed仍为N且可重试N+1；N+1 healthy后 replay N/N-1网络前失败。pending write、replace、ACK、promotion、backup deletion各 crash point唯一收敛。
- [ ] 实现canonical release/source/keyring/package-policy/`ComponentRegistry`、`ObservedArtifactInventory`、`ReleaseComponentGraph`、无环`ReleaseDigestGraph`和attribution schema/verifier；从registry+actual dependency graph单向生成third-party descriptor、NOTICE、signed SBOM和产品归属。`ReleaseAttributionBundleDescriptor`绑定payload-tree/final-artifact/`release_unit_digest`/registry/component-graph/inventory/NOTICE/SBOM，update manifest单向绑定其digest；禁止“双向互绑”形成循环。实现package-anchored `ProviderCLICompatibilityTrustPolicy`并让provider compatibility verifier只接受`provider_cli_matrix` usage授权的exact matrix subject，payload signer不得选择authority。transaction只引用已验证identity，release unit绑定exact v3 artifact + bundled CLI版本/哈希/协议组合；用户PATH CLI不进入release unit，token、credential store和provider-home auth文件永不成为成员。
- [ ] recovery实现 versioned transaction、`TrustGenerationState`、env/write journals、atomic persistence和严格路径 scope；故障注入覆盖 fsync/rename/marker/trust promotion顺序。
- [ ] 先写 RED 并实现：`service.Install` 在 helper 启动前以 sole-writer operation ID durable prepare；helper 不得自行猜测 from/to version 或 release-unit 成员。helper/OS installer 调用错误不等于未执行；`MayHaveEntered` 保留 backup/operation fence、禁止 retry/commit/release，直到 process identity 与 release-unit 实况 reconciliation 得到 `NoEntry` 或 `EffectResolved`。
- [ ] updater 安装步骤失败继续立即 rollback；安装步骤成功只记录 installed/probationary，不删除 backup。

GREEN 至少覆盖 `internal/module/appupdate/recovery`、`internal/module/appupdate` 和 `cmd/super-dolphin-updater` focused tests。每个修改过的 Go 文件运行仓库单文件 guard；任一 Hint/Information/Warning/Error diagnostics 都必须处理或记录 blocker。

### Task 2：Startup tracker、Safe Mode 与 Guard CLI

- [ ] 先写 RED：活着的 PID 不能被重复启动误判为崩溃。
- [ ] 先写 RED：观察窗口内达到失败阈值进入 Safe Mode；窗口外失败计数重置。
- [ ] 先写 RED：`ready` 不能提交 update；只有持续通过观察期的 `healthy` 才提交并删除 backup。
- [ ] 先写 RED：pending rollback 未完成、mixed install、损坏事务或 hash 不一致时 Guard 拒绝启动 desktop。
- [ ] 先写 RED：detached updater 在 `open -n` 后保持监督；probation PID 硬退出时无需再次点击即可立即恢复 exact backup。updater 被杀或主机重启时，下一次 preflight 必须交接 detached Guard、退出当前 agent-terminal，再恢复并重开旧 release。
- [ ] 先写 RED：`open -n` 成功但 registration deadline 内没有 PID 时进入 `failed_registration`；PID 已注册但 bootstrap deadline 内没有 ACK、ACK nonce/lease/version 错误或显式 bootstrap failure 时进入 `failed_bootstrap`，均不得落入无限等待或误提交。
- [ ] 先写 RED：目标 PID 拒绝 graceful stop 时只对 exact PID 执行有界终止；身份不可证明或仍无法停止时进入 `rollback_blocked`，保留 backup、阻断 desktop 且不杀无关进程。
- [ ] 先写 RED：Safe Mode 不启动自动 provider、MCP sidecar、Hook/Skill 执行和用户扩展面，但仍允许本地恢复 UI/CLI 所需的最小只读状态。
- [ ] 先写RED：真实packaged `agent-terminal`在pending recovery下分别注入sidecar缺失、LSP manifest/server缺失、`.env`/`video.env`错误、normal frontend assets损坏和bundled CLI descriptor损坏，仍必须进入Recovery UI/CLI或detached Guard handoff；`ConfigurePackagedApp` full validation、`LoadVideoEnv`、normal `frontendDistFS`、新增bundled-only release compatibility preflight和normal graph调用计数均为零。
- [ ] 先写 RED：在没有 pending recovery 的干净首次启动中，durable `StartupAttempt` 必须先于 normal preflight 落盘；分别注入 sidecar/LSP、`.env`/`video.env`、normal assets和bundled Codex CLI integrity/protocol preflight首次失败，断言同一attempt写入stage-bound `failed_preflight`并立即进入Recovery/Guard。用户CLI/auth/vendor failure则只进入provider-unavailable。第二次启动仍读取app failure进入Recovery，不能把旧attempt覆盖成新的`starting`、`clean_exit`或健康状态。
- [ ] 先写RED：active probation candidate在sidecar/LSP、`.env`/`video.env`、normal assets或新增bundled-only release compatibility preflight失败时，写`failed_preflight -> rollback_requested`，立即通知exact supervisor/Guard并退出；无需等bootstrap-ACK deadline就恢复/reopen旧release，candidate不得发送`recovery_ready`或进入Recovery UI。provider-start PATH/managed/auth/vendor failure不进入该链；同类app故障在无pending probation时才进入`recovery_required -> Recovery/Guard`。
- [ ] 先写 RED：第 N 个 packaged derived/env setter失败时 parent selector逐键环境与启动前完全一致；完整 normal env后注入 frontend/Codex/Fx失败，独立 Recovery/Guard环境严格等于 allowlist；并发 observer永远看不到部分 normal env。
- [ ] 先写 RED：预置旧 Codex config bytes/mode或不存在状态，candidate staging后分别注入 chmod/Fx/ACK/crash；rollback后 canonical状态精确恢复，healthy后新config才可见。新增未登记 pre-healthy file/store writer时 guard自动RED。
- [ ] 先写 RED：bundled CLI hash/signature/protocol mismatch归因为candidate-attributable并触发probation rollback；用户PATH CLI缺失/过旧、登录过期、vendor outage或用户provider-home错误只产生typed provider-unavailable，不增加release crash count、不删除健康app也不触发rollback。任一token、credential path、第三方stderr secret进入journal/log/evidence都必须RED。
- [ ] 先写 RED：修复普通 `failed_preflight` 后，未经授权的 reopen仍进入 Recovery；Guard/Recovery 对 exact attempt显式 retry后创建 `parent_attempt_id` 新 attempt并可进入 normal。重复失败保留两代 evidence并继续受崩溃阈值约束；retry不能绕过 pending/mixed/corrupt transaction。
- [ ] 先写 RED：Safe Mode 进入 `recovery_bootstrap -> recovery_ready`，且 `getPreference(provider)`、`loadProviderConfig`、projects/sidebar/thread bootstrap、`checkAppUpdate/installLatestAppUpdate` 调用计数都为零；composer/正常导航禁用，并持续展示 stable reason、disabled capabilities 和 recovery state；typed projection 缺失时 desktop 不启动。
- [ ] 先写 RED：`fx.ValidateApp` 对独立 Recovery graph 成功，完整 `app.Module`、normal `BindRuntime`、DB/store、Hook、MCP control、appupdate、Skill、provider、toolbridge 构造器调用计数为零；Recovery RPC surface 不暴露 check/install update 或正常 runtime mutation。
- [ ] 先写 RED：missing/stale/wrong-version bootstrap ACK、前端 bootstrap failed/stuck、PID owner 不匹配或 release-unit parity 失败都不能提交 backup。
- [ ] 实现 `super-dolphin-guard check|status|launch|recover|restore` 及 detached restore worker。pending probation crash 的 exact-transaction rollback 是预授权补偿；历史 snapshot/cross-transaction restore 需要显式 ID 和确认，用户拒绝时不得写盘。
- [ ] `agent-terminal` minimal selector只构造 `NormalProcessEnvPlan`和write journal，以 transaction-bound re-exec/child显式 Env启动 candidate；normal child失败后退出并由parent/supervisor选择rollback或独立Recovery。禁止已发布normal env的进程fallback Safe Mode。
- [ ] desktop normal bootstrap复用 selector attempt，在高风险模块前进入 `starting`；前端 bootstrap后发送 version+transaction ACK；ACK、PID、release-unit parity、bundled CLI compatibility和观察期全通过才 `healthy`。candidate-attributable preflight failure不是 clean exit；用户CLI/auth/vendor故障只降级对应provider。普通app失败必须由 exact-attempt retry authorization创建 parent-linked新 attempt。仅无 active probation的 Safe Mode可发送不提交 backup的 `recovery_ready`；normal异常退出和 clean exit分别落盘。
- [ ] 通过 typed RPC 把 Safe Mode/recovery 状态投影给前端；`runtimeSlice` 必须在任何 provider/project/sidebar/thread bootstrap 前分支，`App.jsx` 必须让 update banner/check/install 服从 recovery mode；正常模式不得回归，Safe Mode 不得呈现可误操作的正常产品状态。
- [ ] Guard 输出稳定 code 和安全摘要；不得打印 HOME、token、完整环境、用户 prompt 或第三方 stderr。

第一版不实现 `assist/apply-plan`、自动配置修复或模型调用。若恢复 UI 依赖 Wails/WebView，则它不是 Guard 的唯一入口，CLI 必须保持完整可用。

### Task 3：打包、更新后恢复与平台验收

- [ ] macOS package构建并复制Guard；package generator、updater和Guard不得用手写数组猜成员，final verifier必须在签名/公证/staple完成后独立解包exact artifact生成subject-external `ObservedArtifactInventory`，与release descriptor做双向entry/attribute差集，并验证primary/embedded component refs、derived dependency graph、third-party descriptor、license/NOTICE/signed SBOM和产品归属；missing/unexpected/stale/duplicate/ownership、多语言drift均fail-first。observed inventory不得反写声明或成为artifact自身member。
- [ ] 更新签名manifest/installer参数携带payload-tree/final-artifact/release-unit/component-registry/component-graph/inventory/`ReleaseAttributionBundleDescriptor`/source/keyring hash和完整release identity；package/verifier按无环`ReleaseDigestGraph`生成、签名、验证，attribution只接受package-anchored `release_attribution` key usage。任何组件不得维护第二份成员/key/component清单。
- [ ] package guard 锁定 macOS `CFBundleExecutable=agent-terminal`、updater `open -n target.app` 和 direct agent-terminal 都进入同一 shared preflight。
- [ ] 在任何跨代E2E前生成provenance matrix：pre-P0、bootstrap、transactional next分别绑定独立commit、final artifact SHA、package signer、release digest graph/component-registry/component-graph/inventory/attribution/source/keyring hash、NOTICE/SBOM digest、manifest/attribution key identity-material hash/usage/validity、installed launcher/helper/Guard artifact-relative identity和PID start identity；对每个第三方CLI只公开记录vendor/origin/upstream version+URL/asset digest+signature/package signer/license/notice/distribution/protocol/source/consent、path class和identity digest，不记录用户绝对路径。任意同一HEAD、current test binary/helper、旧attribution replay、key或CLI provenance缺失、phase cache或自动retry都fail-fast。
- [ ] 建立旧版 → 新版成功启动 → healthy commit E2E。
- [ ] 在 disposable macOS VM 或 clean user/profile 中安装真实 pre-P0 DMG，从实际旧 `agent-terminal` 和其 bundled helper 启动；先发布 bootstrap，再让 transactional v2 成为其独立 feed 的 latest，最后让一台从未检查过更新的 pre-P0 安装首次检查。断言它仍从永久 legacy feed取得 bootstrap，bootstrap 再从 transactional feed取得下一版；第一次迁移不宣称 probation rollback，第二次才启用 descriptor/ACK/rollback。覆盖旧 strict decoder、minimum version、feed 隔离和 `manual_upgrade_required`。 该 old-helper 首跳只计兼容性/legacy-risk，不计安全自动升级 GREEN；安全链必须从经 OS/platform 独立验签的手工/系统 bootstrap 安装开始，新 package trust 只从 bootstrap -> transactional 生效。
- [ ] 在上述 bootstrap artifact 中验证签名 `UpdateSourceDescriptor` 确实指向 transactional feed；分别和同时注入旧 process env、bundle `.env`、app-data `video.env` 与部署脚本 repo/manifest 值，断言 packaged v2 仍请求 descriptor 指定 feed，旧值只产生稳定冲突诊断。descriptor hash/signature/source identity 任一篡改必须在首个网络请求前 fail-closed；显式 trusted dev/test override 使用独立非发布产物证明，不能与 packaged GREEN 混用。
- [ ] 在真实 bootstrap -> transactional next E2E 中执行 key rotation：bootstrap package keyring按明确 overlap信任旧/新 manifest key，next撤销旧 key；unknown/revoked/expired/mismatched environment key都在网络前失败，且实际请求使用 descriptor identity解析出的 key。记录 package/release trust anchor与 keyring generation，禁止用测试内直接注入 `PublicKey` 冒充。
- [ ] 对 packaged artifact 注入 unsigned/Team ID/publisher/thumbprint env并直接调用 helper bypass，全部在backup前失败；ad-hoc/wrong signer产物即使manifest正确也失败，独立dev artifact不能计入GREEN。
- [ ] 建立 trust generation E2E：N -> pending N+1 -> rollback N -> 可再次升级；healthy N+1后旧generation永久拒绝，并覆盖promotion/backup deletion掉电顺序。
- [ ] 建立 pre-healthy writer E2E：旧Codex config bytes/mode/absence在candidate crash/ACK timeout/rollback后精确恢复；healthy才发布新config，并枚举其它writer。
- [ ] 建立bundled/PATH/managed及Codex/Claude分平台CLI E2E：packaged只使用descriptor绑定的bundled chain且禁vendor network/PATH fallback；PATH只消费用户明确选择且兼容的现有chain；managed默认关闭、显式consent、固定版本与digest。三种模式的matrix都必须由package-anchored `ProviderCLICompatibilityTrustPolicy`授权；payload self-declared、wrong/expired/revoked/wrong-usage key与跨provider/source/arch/release-unit重放均在任何第三方CLI执行前失败。resolver生成完整launcher/interpreter/target plan，platform prepare后probe与spawn共用同一handle/staging；交换launcher、shebang interpreter、npm shim target、symlink、PATH或managed generation时返回`provider_binary_identity_stale`且替代sentinel严格零调用。Claude auth A/launch或restart B、Codex pool process A/descriptor或matrix B均失败并drain旧复用。healthy/rollback恢复exact v3+bundled CLI pair。 另模拟 spawn 返回错误但子进程已创建：同一 operation 不得再次 prepare/spawn，必须先按 OS process identity reconcile 并原子关闭 effect fence。
- [ ] 建立packaged probation bundled-only release compatibility E2E：该静态preflight位于healthy ACK前，只消费release-unit chain digest/vendor signature+由package-anchored policy验证的immutable matrix且不执行第三方CLI；PATH lookup、managed installer、auth、provider-home mutation和vendor network调用数均为零。active probation bundled integrity/protocol/launch-chain/matrix-trust failure触发exact rollback；无pending同类app failure进入Recovery，用户PATH/auth/vendor failure不影响app healthy。
- [ ] 建立 active probation 新版在 packaged runtime/video env/assets或candidate-attributable bundled CLI integrity/protocol preflight 首次失败 -> typed supervisor notification -> candidate退出 -> 无 ACK deadline等待 -> exact rollback/reopen旧版 E2E；与无 pending probation同故障进入 Recovery的 E2E 分开。用户PATH CLI缺失/过旧、auth expired、vendor outage则只投影provider-unavailable并保持app healthy，不能进入该rollback链。
- [ ] 建立旧版 → 新版替换成功 → detached updater 监督启动 → 新版硬崩溃 → 无第二次用户操作即自动回滚并重启旧版 E2E。
- [ ] 建立旧版 → 新版 probation → updater/主机中断 → 下一次 open 由 agent-terminal preflight 交接 detached Guard 并退出 → restore/parity/reopen 旧版 E2E。
- [ ] 建立 helper/Guard 缺失或版本混装 → fail-closed E2E。
- [ ] Finder/open、updater restart 和 direct binary 三条路径在 pending/mixed/corrupt transaction 下行为一致。
- [ ] Windows 若保留更新入口，必须使用同样的独立 commit/artifact provenance、真实旧 installer/launcher/helper、clean machine/profile、禁缓存/禁 retry 完成同等 bootstrap/probation/healthy/rollback E2E；否则 Check/Install/config/manifest artifact 一起 fail-closed。Linux 用稳定 unsupported 测试证明没有半开放入口。
- [ ] 检查 backup retention、磁盘不足、只读 volume、跨设备 rename 和重复双击启动恢复的竞态。

### Task 4：MCP Schema compiler 与 quarantine

- [ ] 先写 RED：合法同级工具 + 非法嵌套 schema 时，当前实现会让整个 list 失败。
- [ ] 先写 RED：external `$ref`、non-object root、超预算 schema、错误 keyword 类型和无效 tuple schema 被拒绝。
- [ ] 先写 RED：一方 MCP 任一 schema 失败仍让 server/list fail-fast；Codex消费的trusted external MCP只隔离坏工具。
- [ ] 先写RED：同一个“合法工具 + 坏schema同级工具”走Codex时隔离坏项并保留好项；Claude`--mcp-config`直连产生typed `ProviderMCPCapabilityStatus`。在manifest N/process P形成ready后，分别推进manifest/authority N+1、增删server、修改config digest、restart/resume为P+1、重放旧event sequence或把attestation重挂另一turn，全部转`stale|blocked`且user payload零发送；只有固定真实CLI为exact current process/transport/target-turn/manifest/matrix/server-vector tuple生成的`MCPReadinessAttestation`可开放。不得用Codex、bare ready bool、日志文本或post-user-message `system:init`声称provider-neutral能力。
- [ ] 先写RED：`ProviderMCPCapabilityStatus`覆盖`unsupported|unproven|checking|ready|failed|stale|blocked`，provider contract、agent DTO、runtime/uistate snapshot/patch、RPC和frontend runtime slice字段完全一致；旧generation/sequence不能覆盖新状态，删除任一mapper字段或用generic session ready/Codex状态/自由文本伪造Claude ready均RED。非current-ready时composer禁用，绕过UI调用backend仍失败。
- [ ] 先写RED：直接构造`RawProviderEvent`或payload伪造`origin_kind`、host agent/thread/session、process/transport generation和内部synthetic event名，旧transport A在replacement B启动后发送terminal，以及坏JSON/未知critical frame，都必须在任何state/action/raw-bus/translator前由host-issued opaque ingress authority和`ProviderProtocolGate`裁决；gate前UI token/status、readiness、tool/approval tracking和terminal调用计数为零。删除authority检查、后移gate或恢复public裸Dispatch必须RED。
- [ ] 先写RED：对gate成功输出的`ValidatedProviderEvent`动态枚举全部字段和typed payload variant，分别删除/陈旧化authority digest、host binding、process/transport/ingress generation、protocol、event kind/sequence，混填variant，或让state/action/raw-bus/translator任一mapper回读raw payload补值；每个真实终止consumer都必须在副作用前以exact `FIELD_CHAIN_ID`失败，不能由Drift失败链或Ingress Envelope GREEN替代。
- [ ] 先写RED：critical payload伪造或缺失全部provider session/turn ID时，drift仍只作用于ingress authority绑定的current host scope并exactly-once失败；旧process/transport generation的critical或success不得终止/覆盖replacement。把host identity改回payload来源、删除session-manager/process/transport generation或删除fence必须RED。
- [ ] 先写RED：MCP peer `ctl/report runtime`携带CLI identity/version/matrix/readiness/status/failure/drift字段时必须拒绝或忽略，不能覆盖provider-owned状态；Claude direct provider readiness不能转换成v3-owned peer/client authority或`AdmissionGrant`。Codex只有current committed member + exact v3-owned peer/client lease可取得call authority。
- [ ] 先写 RED：refresh 修复后工具恢复，重新损坏后撤下，lifecycle disabled 的工具不会因 schema pass 重现。
- [ ] 先写 RED：managed lsp/orch/ida 都 fail-fast；trusted external 才 quarantine，分类只接受 canonical registry/config owner 签发的 opaque `ResolvedMCPServerIdentity` handle。Name/URL/`TrustedServerID` 相等、raw DTO 的 `origin`/managed claim、zero value 或调用方仿造值都不能提升来源。
- [ ] 先写 RED：所有 external HTTP/stdio producer 对 canonical managed registry 动态枚举出的完整 reserved set 及大小写/Unicode/规范化碰撞统一 fail-closed；registry 新增 managed ID 时测试必须自动覆盖。zero/tampered/expired handle、handle 与 binary canonical ID 不匹配、managed payload 携带 external ID、raw payload 伪造 authority、当前 HTTP URL early-return 和 Name-only classifier mutation 都必须被捕获，同时 stdio command allowlist 与 HTTP egress 旧拒绝场景保持 GREEN。
- [ ] 先写 RED：合法 external经 current authority snapshot生成scope+carrier；签发后删除/禁用server或推进N+1、把未改 carrier从workspace/thread A重挂B、移除pre-write/pre-call CAS均返回 `manifest_authority_stale/revoked`，零client/零写入。start/resume/turn分别覆盖。
- [ ] 先写 RED：HTTP/stdio `ListTools` 收到“一个合法工具 + 一个字段类型无法反序列化的坏工具”时仍返回包含两项 `json.RawMessage` 的 typed result；只有畸形 JSON-RPC/tools array envelope 才在 transport 层整表失败。后续一方 class 对坏项 fail-fast，trusted external class 隔离坏项并保留合法项。
- [ ] 先写 RED：valid 第一项 + missing/non-string/blank/duplicate JSON `name` key/normalized-duplicate identity 的后项时，所有 class 都整表失败，durable refresh transaction 调用计数为零，quarantine generation/surface 不变；不得用 array index 建 quarantine。
- [ ] 先写 RED：真实 SQLite故障覆盖 prepared前后、commit、projection；prepared后kill必须由唯一coordinator takeover为 committed/aborted/superseded，双takeover旧lease失败且不重复backfill。两server surface仅B更新/新增/删除时aggregate digest变化且顺序稳定。取得`AdmissionGrant`后分别替换host call ID、canonical tool、compiled schema digest、lifecycle version、client/process identity、canonical arguments，及重复消费、过期、N+1 revoke、validate后原args buffer mutation，均在`CallTool`前失败且真实client为零；合法grant只能消费一次。并发commit若先线性化则撤销未消费grant，否则有界等待已进入exact旧client的grant；proxy/Codex/direct route同一RED。 同一 `(principal_id,host_call_id)` 并发/重复申请只能得到一个 active-or-terminal reservation；client 返回错误但可能已进入副作用时不得新签 grant，reconcile 前真实 client 重试计数为零。
- [ ] 先写 RED：同一 invalid external tool 在只读状态中可见且有 typed code，但 catalog enabled surface、dynamic tools、proxy 和 call schema 都不可见/不可调用；修复后恢复、再损坏撤下，manual lifecycle state 不被 backfill 覆盖。
- [ ] 先写 RED：raw `WSHandler`/默认 `/ws`、control register前、generic Dispatch无 auth proof均不能resolve；Wails guard与control register后可用。两 Wails owner token不可交叉，关闭一个只撤销它；若选择app-lifetime则测试明确不宣称window隔离。token绑定workspace aggregate digest，multi-server stale失败。
- [ ] 实现唯一 canonicalize/compile seam 和 `CompiledToolSchema` canonical JSON/draft/digest owner；catalog、provider、proxy 和 call validator 的 digest/内容必须一致，任一 consumer 重算或消费 raw schema 的 mutation 都应 RED。
- [ ] authority-bound resolver/scope、current CAS、wire decode、identity/schema、class policy、journal terminal/takeover、server+workspace aggregate commit、candidate reconcile、lifecycle、catalog/provider、current peer runtime authority、exact single-use `AdmissionGrant`、client call顺序必须固定；任何绕序RED。
- [ ] 遍历§4.3按真实producer动态枚举的全部独立字段链，对每一行分别执行至少一个真实producer、mapper和终止consumer mutation；错误必须报告exact `FIELD_CHAIN_ID`、producer和field。任一行合并两个独立producer、任一字段只存在于测试手写清单、或豁免未登记/原因无效都必须RED；一行GREEN不得替代其它行。
- [ ] quarantine 状态使用 typed code；schema path 和摘要经过长度限制、路径/secret 清理。
- [ ] 单 schema 与 aggregate tool-count/schema-bytes/diagnostic-count/summary-bytes 超限使用稳定 typed code；重复超限输入不得增加 goroutine/heap，必须有 benchmark/allocation cap。compiler 不支持安全取消/隔离时停止，禁止 timeout goroutine 遗留后台工作。
- [ ] 如新增依赖，先证明 versioned dependency gate 会被 `go.mod/go.sum` 变更选中，再记录 exact compiler version、license、vulnerability 和 SBOM evidence；当前 hooks 不提供这项证明。

不得为“兼容更多坏 schema”做全局静默修复。只允许规范化空 schema、缺失 root type 等 Task 0 明确批准且有双向测试的兼容形态。

### Task 5：P0 集成、独立复核与交付

- [ ] A/B lane 各自保持单一 owner 和独立提交；Integrator 对 shared seams 串行合并。
- [ ] 从 staged snapshot 生成当前 hook gate plan，不在本文复制可能漂移的全量静态命令清单。
- [ ] 运行 focused RED/GREEN、每文件 guard、archtest、package smoke、依赖检查、生成物 drift 和 staged pre-commit/pre-push 对应门禁。
- [ ] 每个 gate 记录 command、exit、pass/fail count、首次失败、最终重跑和适用 review object。
- [ ] 对 Guard、MCP Schema 和 Integrator 三个 surface 分别做独立 D01-D19 coverage；lane PASS 不能写成 repo PASS。
- [ ] 冻结本文 exact path/line/bytes/SHA 后启动两条 fresh、只读 Reviewer lane；P0/P1 requirement set 来自独立审查策略而非 actual findings，两 lane 均记录 start/end identity。只有 `0 P0 / 0 P1` 可通过；不影响正确性的 P2/P3 明确处置后不阻断，任何旧 hash 不得复用。
- [ ] 只有真实远端 SHA parity 后才能声称 pushed；本计划本身不授权 commit、merge、push 或 release。

## 7. P1 后续 tranche 进入条件

### 7.1 统一只读能力诊断

进入条件：

- P0 Schema quarantine 已提供稳定 typed diagnostic projection。
- LSP 证明现有 Skill inventory、provider mirror、MCP lifecycle、Hook 和 capability manifest 的 owner/port。
- 独立 ADR 决定聚合 owner；不得让 `internal/module/dashboard` 深导入其它 module 实现。

第一 tranche 只做 static read model 和 JSON/RPC；默认不联网、不启动 MCP、不写配置、不做自动修复。CLI/live probe 必须另行授权。

### 7.2 语义进度租约

进入条件：

- 列出 Codex/Claude/local/remote orchestration 实际可见的 durable progress receipt。
- 明确 wall-clock stall 与 semantic stall 是两个 detector，不互相覆盖。
- 通过 replay fixture 证明重复 event、相同 read、Todo 文案 churn 不续租，而真实 DAG/diff/evidence 可以续租。

首版只告警和 nudge；自动暂停/恢复必须在误报率有基准后另开开关，不直接照搬 Reasonix 的轮次常数。

### 7.3 薄层 Subagent Profile

进入条件：

- Profile schema 复用 canonical Skill owner，并使用 namespaced `subagent_profile` typed block；`runAs`、model、effort、allowed_tools/disabled_tools 等字段走字段守卫。
- Profile invocation 只投影到现有 `launch_agent`；profile delete 不删除 Agent、Thread 或运行记录。
- 唯一 projector 定义 Skill `AllowedTools` 白名单与 `launch_agent.disabled_tools` 的 precedence、交集和冲突 fail-fast；provider mirror 对 profile-only metadata 的保留/剥离必须有 roundtrip/golden 证据。
- Claude child-agent 不支持仍是显式 capability，不得由 Profile UI 伪装成支持。

## 8. P2/P3 明确延期

| 能力 | 决策 | 重新打开条件 |
| --- | --- | --- |
| Claude/Codex 插件包兼容 | `DEFER` | 先完成静态 manifest/路径安全/语义 gap ADR；禁止 install script auto-run |
| First-party writer 回执 | `DEFER` | 复用现有 writer-preview ADR，只覆盖 v3-owned writer，带 hash/range/size bound |
| 选中文本加入上下文 | `DEFER` | 前端独立计划，接入现有 command registry，并有注入/截断/切 thread 测试 |

## 9. 复核与返修历史

三个 Reviewer 只读同一初稿 review object，不修改文件、不共享结论、不按票数裁决。初稿为当时未跟踪的本文档，R3 记录 SHA-256 `bb16799a87f14895631e9abbcfe3c375e4435f3b2b3b45abe35964e3628ab322`；v3 和 Reasonix 事实分别绑定 §1.1 的固定 commit。

| Reviewer | 主审维度 | 结果 | 状态 |
| --- | --- | --- | --- |
| R1 Architecture/Release | D01、D02、D10、D13、D16、D19 | P1×3、P3×1，无 P0 | `COMPLETED_WITH_FINDINGS` |
| R2 MCP/Schema/Test | D03、D05、D10、D12、D14、D18 | P1×3、P2×1，无 P0 | `COMPLETED_WITH_FINDINGS` |
| R3 Product/Operations | D06、D08、D09、D11、D15、D19 | P1×3、P2×1、重复 whitespace finding×1，无 P0 | `COMPLETED_WITH_FINDINGS` |

Root agent 使用固定 SHA 源码独立复核可达性后裁决如下：

| ID | Priority/Dimension | Finding | Disposition | 文档修订 |
| --- | --- | --- | --- | --- |
| R1-F1 | P1 D13/D19 | Finder/open、updater restart 与 direct binary 没有唯一不可绕过 launch gate | `ACCEPTED` | §3.1、§4.1、Task 0/3 固定 macOS `agent-terminal` shared preflight |
| R1-F2 | P1 D01/D19 | release-unit 成员可能在 signed manifest、runtime manifest、package/verifier 各自成为 SSOT | `ACCEPTED` | 新增 canonical `ReleaseUnitDescriptor`、descriptor hash 签名绑定和 drift RED |
| R1-F3 | P1 D02/D13 | Windows 已有 `/S` 安装入口，不能只用 package-smoke 或文档 unsupported 关闭 P0 | `ACCEPTED` | 新增平台能力矩阵；不满足事务语义时同步阻断 Check/Install/config/manifest |
| R1-F4 / R3-F5 | P3/P1 D16 | 初稿 metadata 行尾空格使 no-index whitespace check 失败 | `ACCEPTED_FIXED` | 删除行尾双空格并重跑 no-index/diff gate |
| R2-F1 | P1 D03/D05/D18 | first-party/external 分类没有 compiler 可见的权威输入，IDA 可能被旧 helper 错分 | `ACCEPTED` | 分类只取 RuntimeMCPPolicy/managed name/MCPBinary trust facts，并补 lsp/orch/ida 测试 |
| R2-F2 | P1 D03/D05/D18 | quarantine read model、RPC、identity、refresh 和 backfill/catalog 顺序未定义 | `ACCEPTED` | 唯一可写 owner 固定为 toolbridge `schema_quarantine.go`，App 只读投影；禁止污染 peer DTO 并固定完整 pipeline |
| R2-F3 | P1 D10/D12 | 当前 versioned hooks 不存在 license/SBOM/vulnerability gate | `ACCEPTED` | 不再宣称现有 gate；新增 dependency 前先建立 pinned versioned fail-first gate，否则 BLOCKED |
| R2-F4 | P2 D10/D12/D14 | schema resource budget 没有数值和 cancellation-safe 验收 | `ACCEPTED` | Task 0 冻结 bytes/node/depth/ref/time/cancellation；Task 4 增加 leak/benchmark/allocation 证据 |
| R3-F1 | P1 D06/D15 | 自动 rollback 与“所有 destructive action 显式确认”相互冲突 | `ACCEPTED` | 区分 pending transaction 预授权补偿与历史 snapshot 显式恢复 |
| R3-F2 | P1 D09/D15 | Safe Mode 启动正常 desktop 会继续自动检查更新并展示正常产品动作 | `ACCEPTED` | 增加 typed Safe Mode projection、持久 banner 和 update/composer/navigation 禁用；缺 projection 时 CLI-only |
| R3-F3 | P1 D11/D15 | `healthy` 没有 version-scoped frontend bootstrap ACK，elapsed timer 可能误提交 backup | `ACCEPTED` | healthy 固定为 ACK + PID + descriptor parity + observation window |
| R3-F4 | P2 D08/D19 | Skill AllowedTools 与 launch_agent disabled_tools 可能产生双权限语义 | `ACCEPTED` | Profile 使用 namespaced typed block 和唯一 projector，冲突 fail-fast |

没有 finding 被按票数自动接受，也没有 finding 被隐藏。所有接受项已写回对应不变量、landing files、任务和测试；`p0_executable=false` 保持不变，因为 LSP、clean worktree、平台/descriptor/compiler 决策仍需 Task 0 实证。

### 9.1 修订稿 Root 复核返修

Root 对第一次整合后的 440 行修订稿做了新的只读复核，返修前 review object SHA-256 为 `f6b7bd18d6a63bde7d8a67264f1c547ddf67236172517736084f9cdad58ada46`。该轮没有 P0，确认 4 个 P1 和 1 个 P2；本次文档修改逐项闭环如下：

| ID | Priority/Dimension | Finding | 修订结果 |
| --- | --- | --- | --- |
| RR-F1 | P1 D01/D02/D11/D13/D19 | canonical launch gate 没有定义 hard crash 后由谁触发自动回滚 | 固定 detached updater 首次 probation supervisor，以及 updater/主机中断后的 next-launch detached Guard handoff；补状态、RED 和两条 E2E |
| RR-F2 | P1 D02/D09/D11/D15 | Safe Mode 与当前强制 provider bootstrap 冲突 | 固定独立 `recovery_bootstrap -> recovery_ready`；列入 runtimeSlice/helpers/store/App landing files，并断言 provider/project/sidebar/thread/update 调用为零 |
| RR-F3 | P1 D03/D05/D12/D18 | HTTP/stdio client 在 class resolution 前整表校验，handler 已失去逐工具隔离机会 | client `ListTools` 改为 wire-envelope-only typed result；两个 client、types、interface、handler 和 tests 全部进入 landing files/pipeline RED |
| RR-F4 | P1 D03/D10/D19 | HTTP `ValidateManifestBinary` 可在未验证 `TrustedServerID` 时提前成功 | 固定 `ClassifyManifestBinary` typed class SSOT，external HTTP/stdio 缺失或错配 trusted ID 全部 fail-closed 并补 RED |
| RR-F5 | P2 D16/D19 | `decision_complete`/DoD 与尚未冻结的 implementation owner、路径和复核对象冲突 | 拆分 absorption decision、implementation design、P0 executable 和初稿/修订稿复核状态；当前修订稿明确等待独立 recheck |

以上是计划文档返修，不是生产代码 GREEN。该轮返修随后进入 §9.2 的双 Reviewer 独立复核；旧对象状态不能冒充当前对象 PASS。

### 9.2 两 Agent 修订稿独立复核与本次返修

两个 Reviewer 只读同一个 476 行修订稿，review object SHA-256 为 `19e79b559e3286bc3fcee970adc24d762ce4659daa9b87ca6f19abb828c668f1`；二者均未修改文件。该轮无 P0，共确认 6 个不重复 P1，Root 重新核对源码可达性后全部接受：

| ID | Priority/Dimension | Finding | 本次文档修订 |
| --- | --- | --- | --- |
| SR-F1 | P1 D11/D13/D15 | 旧版本从当前 bundle 启动无 supervisor 的 updater，strict manifest decoder 又会拒绝新增 descriptor 字段，首个事务化版本没有可执行 bootstrap 路径 | §2.1、§3.1、§4.1、Task 0/1/3 增加 two-generation protocol rollout、old-compatible bootstrap、minimum version、`manual_upgrade_required` 与跨两代 E2E |
| SR-F2 | P1 D01/D02/D06/D09/D15/D19 | Safe Mode 只约束前端调用，当前 desktop 仍装配完整 `app.Module` 和 normal Wails/runtime graph | §3.1、§4.1、Task 0/2 增加 `fx.New` 前 mode selector、独立 `RecoveryDesktop` graph、forbidden constructor/RPC surface RED |
| SR-F3 | P1 D06/D11/D13/D15 | supervisor 对 `open` 后无 PID、PID 后无 ACK、stale ACK 和卡死进程没有有界终态 | §3.1、Task 0/2 增加 registration/ACK 双 deadline、transaction/version/nonce/lease/PID identity、bounded termination 与 `rollback_blocked` |
| SR-F4 | P1 D03/D05/D10/D12/D17/D18 | `turn/manifest.go` 的生产 mapper 未从 server-config key 写入 `TrustedServerID`，严格分类会误阻合法 external MCP | §2.2、§3.2、§4.2、Task 0/4 固定 producer/mapper/contract/classifier 链，并加入动态字段 guard、roundtrip、mutation/tamper RED |
| SR-F5 | P1 D03/D05/D10/D12/D19 | per-tool identity 错误和 duplicate 发现过晚，逐项 lifecycle backfill 可留下部分写入 | §3.2、§4.2、Task 0/4 固定 batch identity 零写入门禁、class policy barrier、事务化 `BackfillBatch` 与 quarantine generation commit barrier |
| SR-F6 | P1 D05/D10/D11/D12/D19 | quarantine identity 未包含 workspace/surface ownership，旧 generation/release 可能覆盖或删除其它 scope | §3.2、§4.2、Task 0/4 固定 workspace/server/tool[/surface]/generation key、严格 RPC scope 和 replace/release CAS 隔离测试 |

这些修订只把 finding 写成可执行契约，不是生产代码 GREEN。该对象后来演进为本次 §9.3 对抗审查绑定的 528 行对象；旧 `19e79...` 结论不能冒充后续对象 PASS。

### 9.3 两 Agent 对抗审查与第三次返修

两个 Reviewer 分别从 Release/Recovery 和 MCP/Field Guard 方向，只读同一个 528 行返修稿；review object SHA-256 为 `750680b1e2b1db3e52aa0c9366fb0e0f25057708974b2139794abb5cba82f2d8`，二者均未修改文件。该轮无 P0，共确认 6 个互不重复的 P1；Root 以 `origin/main@4093b7537d29b141fcc79ceb2e380641a31e3ec8` 源码逐项复核后全部接受：

| ID | Priority/Dimension | Finding | 本次文档修订 |
| --- | --- | --- | --- |
| AR-F1 | P1 D02/D11/D13/D15/D19 | 单一固定 manifest/GitHub latest 无法保证从未升级的 legacy client 在 v2 发布后仍找到 bootstrap | §2.1、§3.1、§4.1、Task 0/1/3 固定永久旧 schema legacy feed、独立 transactional feed、物理隔离发布 guard 和“v2 latest 后首次检查”真实旧产物 E2E |
| AR-F2 | P1 D01/D02/D05/D08/D09/D15/D19 | selector 虽在 `fx.New` 前，但仍晚于 packaged runtime/video/frontend/Codex normal preflight | §2.1、§3.1、§4.1、Task 0/2 把 minimal state-root/transaction selector 提到所有 normal-only preflight 前，并增加 sidecar/LSP/env/assets/Codex 故障下 Recovery 可达性 RED |
| AR-F3 | P1 D12/D13/D16/D19 | 三代 E2E 未绑定独立 commit/不可变旧 artifact/helper，当前 HEAD、cache 和 retry 可制造假绿 | §2.1、§4.1、Task 3 固定 provenance matrix、真实旧 DMG/launcher/bundled helper、clean VM/user、禁 current binary/cache/retry 和逐平台失败条件 |
| AR-F4 | P1 D01/D06/D07/D11/D12/D19 | 进程内 quarantine lease/generation 与逐行 SQLite lifecycle 无法在 crash 后原子收敛，platform 又不能直连 store | §2.2、§3.2、§4.2、Task 0/4 固定 module/store durable refresh coordinator、同一 SQL transaction/transactional outbox、`prepared/committed/applied` journal、fencing/ABA 与真实 SQLite restart matrix |
| AR-F5 | P1 D05/D10/D11/D12/D19 | optional SurfaceID、CWD-only RPC 和 surface release 对 workspace quarantine 所有权互相矛盾 | §2.2、§3.2、§4.2、Task 0/4 明确选择 workspace/server durable diagnosis，SurfaceID 不入 identity且 release 不删除；RPC 必填 backend-issued workspace ID/generation，并冻结 realpath/symlink/nested/multi-root 规则 |
| AR-F6 | P1 D03/D05/D10/D12/D17/D19 | `map key == Name == TrustedServerID` 只能证明相等，不能证明 managed/external 来源，外部 `ida` 可碰撞 | §2.2、§3.2、§4.2、Task 0/4 改为受控 typed `MCPServerIdentity{CanonicalID, Origin}`、完整 managed reserved set 输入拒绝、start/resume/turn/provider mapper 字段守卫和 Name-only mutation RED |

Reviewer 还指出 schema digest、aggregate budgets、duplicate JSON `name`、stdio allowlist/HTTP egress，以及 release fsync/manifest 字段守卫/PID reuse identity 等残余风险；该次返修一并写入 §3、Task 0–4 和停止条件，形成后来 Root 对抗审查绑定的 562 行对象 `42e4cc5b...`。以上仍只是计划契约修复，不是生产代码 GREEN；该轮旧对象结论不能冒充当前对象 PASS。

### 9.4 Root 对抗审查与第四次返修

Root 对第三次返修形成的同一 562 行对象做只读对抗审查，review object SHA-256 为 `42e4cc5ba9d0ca5b058f81bf94700a608feb48437e89cddab1e88e8ac33e70e2`；源码证据重新绑定 `origin/main@4093b7537d29b141fcc79ceb2e380641a31e3ec8`。该轮无 P0，共确认 6 个 P1，本次文档返修全部接受：

| ID | Priority/Dimension | Finding | 第四次文档修订 |
| --- | --- | --- | --- |
| FR-F1 | P1 D01/D02/D09/D11/D15/D19 | 没有 pending recovery 的第一次 normal preflight 失败仍会直接退出，既有 RED 只覆盖已有 pending 状态 | §2.1、§3.1、§4.1、Task 0/2 增加 durable `StartupAttempt`、stage-bound `failed_preflight` 和 selector-envelope 内即时 Recovery/Guard 转移 |
| FR-F2 | P1 D02/D10/D13/D17/D19 | process env、bundle `.env` 或 `video.env` 可覆盖 bootstrap 选择的 transactional feed | §2.1、§3.1、§4.1/§4.3、Task 0/1/3 固定签名 `UpdateSourceDescriptor`、packaged 优先级和 stale-env 跨代 E2E |
| FR-F3 | P1 D01/D06/D07/D18/D19 | durable coordinator 没有冻结符合 `platform_no_module`/`platform_no_store` 的 app adapter 注入方向 | §2.2、§3.2、§4.2、Task 0/4 固定 toolbridge consumer port -> app runtime adapter -> module Service -> contract store port，并增加 production dependency/archtest RED |
| FR-F4 | P1 D03/D05/D10/D17/D19 | 把 `Origin` 放进普通导出 DTO 仍可由 external payload 伪造 managed provenance | §2.2、§3.2、§4.2/§4.3、Task 0/4 改为 registry/config owner 签发的不可序列化 opaque `ResolvedMCPServerIdentity`，reserved set 动态派生 |
| FR-F5 | P1 D06/D07/D11/D12/D19 | durable `applied` ACK 与 surface swap 顺序互相矛盾，无法跨 SQLite 与进程内 map 原子提交 | §2.2、§3.2、§4.2、Task 0/4 只保留 durable `prepared -> committed`；projection 为可重建派生状态，覆盖 reload/swap/notification 失败与 reconcile |
| FR-F6 | P1 D05/D09/D10/D12/D17/D19 | workspace RPC 要求首次请求携带 backend-issued ID/generation，却没有签发它们的第一跳；D17 也只覆盖部分 P0 字段链 | §2.2、§3.2、§4.2/§4.3、Task 0/4 增加 `toolbridge/workspace/resolve(CWD)`、session-bound token 和六条跨 P0 动态字段守卫 |

该轮只有 Root 单一审查者，不满足当时对象的独立多 Reviewer PASS 条件。第四次写回后形成的对象已进入 §9.5 第五次双 Agent 对抗审查；`42e4cc5b...` 的旧结论不能冒充后续对象 PASS。

### 9.5 两 Agent 第五次对抗审查与第五次返修

两个 Reviewer 分别从 Release/Recovery 和 MCP/Refresh/RPC 方向，只读同一个 617 行返修稿；review object SHA-256 为 `29924f348553881106046db95ea372561d2eed8befdf634fd82b7b667b8720bd`，源码证据仍绑定 `origin/main@4093b7537d29b141fcc79ceb2e380641a31e3ec8`，二者均未修改文件。该轮无 P0，共确认 6 个互不重复的 P1；Root 重新核对生产源码可达性后全部接受：

| ID | Priority/Dimension | Finding | 第五次文档修订 |
| --- | --- | --- | --- |
| F5-R1 | P1 D01/D02/D10/D13/D19 | `UpdateSourceDescriptor` 只有 key identity/hash，没有实际验签公钥的权威来源、轮换和吊销链，运行时仍可能从环境读取 key | §2.1、§3.1、§4.1/§4.3、Task 0/1/3 增加 package/OS/release-bound `UpdateTrustKeyring`、实际 key material、用途/有效期/generation/signer、hash 匹配、轮换吊销与 network-before fail；禁止 env/self-sign authority |
| F5-R2 | P1 D02/D06/D11/D13/D15 | active probation 的 preflight 失败若进入 Recovery，会与“立即自动回滚”契约冲突 | §2.1、§3.1、§4.1、Task 0/2/3 把 active probation 固定为通知原 supervisor/Guard、退出、`rollback_requested -> rollback` 并重开旧 release；只有无 pending 分支进入 `recovery_required -> Recovery` |
| F5-R3 | P1 D02/D09/D10/D15/D19 | `.env`/`video.env` 逐行 `os.Setenv` 可在解析失败前留下半应用环境，污染同进程 Recovery/Guard | §2.1、§3.1、§4.1/§4.3、Task 0/2 固定 immutable parse-validate snapshot、成功后一次性 apply、失败零写入，以及 Recovery/Guard allowlisted frozen environment |
| F5-M1 | P1 D03/D05/D10/D17/D19 | opaque MCP identity 虽不可序列化，但 raw turn/provider 链没有可信 carrier，handle 无法安全到达 tool surface | §2.2、§3.2、§4.2/§4.3、Task 0/4 增加 app-owned `ResolveMCPManifest`，以权威 registry snapshot + trusted agent/thread ref 生成 `ResolvedMCPManifest` 并只放入 `CodexToolSurfaceScope`；DTO/wire/config/store/log 中出现 handle 一律失败 |
| F5-M2 | P1 D05/D06/D07/D11/D12/D19 | refresh notification 丢失时，进程内旧 surface 仍可调用；notification 不能充当 durable freshness barrier | §2.2、§3.2、§4.2/§4.3、Task 0/4 增加 committed generation/digest read port；catalog/provider/proxy/call 每个入口都校验，prepared/read failure/mismatch 返回 typed stale/not-ready 且 client call 为零，notification 仅作加速 |
| F5-M3 | P1 D05/D09/D10/D12/D17/D19 | workspace token 没有可信 RPC connection principal，首次 resolve 后仍可能跨连接重放或伪造 local ownership | §2.2、§3.2、§4.2/§4.3、Task 0/4 增加 transport-issued、context-only `RPCSessionPrincipal`，Wails 使用 owner-local principal；禁止通用 local dispatch 签发 token，并覆盖 reconnect/close/revoke/cross-principal RED |

两个 Reviewer 均明确报告 LSP namespace 不可用，未把 shell/git-object 阅读冒充 LSP PASS。以上写回只表示 `fifth_review_findings=applied_pending_independent_recheck`；写回后的当前对象必须重新计算 SHA，并由新的独立 Reviewer 复核，不能复用 `29924f34...` 的结论。

### 9.6 两 Agent 第六次对抗审查与第六次返修

两个新的 Reviewer 分别从 Release/Recovery 与 MCP/Refresh/RPC 方向，只读同一个 658 行返修稿；review object SHA-256 为 `c02d8d09ce5494ae5cae8a51f35efc3e7bfc22d854e4338664ea0b36bd7f28b6`，源码基线固定为 `origin/main@4093b7537d29b141fcc79ceb2e380641a31e3ec8`，二者均未修改文件。该轮无 P0，共确认 9 个互不重复 P1；Root 逐项核对 Git-object 源码可达性后全部接受：

| ID | Priority/Dimension | Finding | 第六次文档修订 |
| --- | --- | --- | --- |
| F6-R1 | P1 D02/D10/D13/D17/D19 | manifest keyring 上层 package signer仍可被 env或 helper `-allow-unsigned` 降级 | 增加不可覆盖 `PackageTrustPolicy`、production bypass拒绝、signer字段链和 wrong-signer artifact E2E |
| F6-R2 | P1 D06/D10/D11/D13/D19 | anti-rollback floor与 probation rollback缺少 pending/committed激活事务 | 增加 `TrustGenerationState`、healthy promotion/rollback语义及逐 crash-point E2E |
| F6-R3 | P1 D01/D02/D09/D10/D12/D19 | 多键 `os.Setenv` 不可原子，同进程 Recovery仍继承已发布 normal env | 改为 `NormalProcessEnvPlan` + transaction re-exec/child `Cmd.Env`；Recovery/Guard独立进程与 frozen allowlist |
| F6-R4 | P1 D01/D02/D05/D06/D12/D13/D17/D19 | candidate healthy前覆盖共享 Codex `config.toml`，bundle rollback不恢复持久副作用 | 增加动态 `PreHealthyWriteSet`、staging/versioned home、old bytes/mode/absence journal与 healthy publish/rollback guard |
| F6-M1 | P1 D01/D03/D05/D10/D12/D17/D19 | resolver签发后 config删除/换代或跨scope重挂仍可重放旧 carrier | 增加 `MCPManifestAuthoritySnapshot`、owner generation/digest/scope binding及 pre-write/pre-call current membership CAS |
| F6-M2 | P1 D02/D05/D06/D07/D10/D12/D19 | read barrier通过后到 client call之间存在并发 commit TOCTOU | 增加线性化 call admission lease/fence、commit drain/cancel语义及精确暂停并发 RED |
| F6-M3 | P1 D05/D06/D07/D11/D12/D17/D19 | per-server generation与聚合 surface/token单数generation矛盾 | 增加 sorted server vector + `CommittedMCPWorkspaceSnapshot` aggregate digest/token/barrier |
| F6-M4 | P1 D02/D06/D07/D11/D12/D19 | crash遗留 prepared没有终止或接管，workspace可永久not-ready | journal扩为 committed/aborted/superseded，增加 lease/deadline/previous state、startup takeover与双进程CAS RED |
| F6-M5 | P1 D01/D02/D09/D10/D12/D17/D19 | principal没有绑定认证成功证明，Wails local owner生命周期不明确 | 增加 server-only `AuthenticatedRPCConnection` proof、Wails guard/control register签发边界、raw WS阻断和 owner revoke语义 |

F5-R2 的 probation/no-pending文字分支本身闭合；F5-R1/R3与F5-M1/M2/M3均因更深的 authority、transaction或linearization缺口判为 partial。以上写回只表示 `sixth_review_findings=applied_pending_independent_recheck`；新对象必须重新计算 SHA并由新的独立 Reviewer复核，不能复用 `c02d8d09...` 结论。

### 9.7 两 Agent 第七次对抗审查与第七次返修

两个新 Reviewer 分别从 Release/Packaging 与 Provider/MCP 方向，只读同一个 707 行返修稿；review object SHA-256 为 `2c704f5229c5db06968e329d4e5f46d753b01ec790a27dc9785774d01687566d`，二者均未修改文件。两个 Reviewer 原始报告分别含 4 个和 5 个 P1；Root 按共同根因去重为 6 个 P1，全部接受：

| ID | Priority/Dimension | Finding | 第七次文档修订 |
| --- | --- | --- | --- |
| F7-C1 | P1 D01/D10/D13/D15/D19 | 文档没有划清 v3-owned 控制面与 OpenAI Codex CLI/Anthropic Claude CLI 的第三方 ownership，可能把整套产品或发行包误述为全自研/全自有 IP | §0、§1.3、§3.1、§4.1、Task 0/1/3 增加 `ProductIPBoundary`、component ownership table和多语言 product-claim guard；打包/签名不改变归属 |
| F7-C2 | P1 D10/D13/D17/D19 | bundled CLI作为release member却缺vendor/origin/version/license/NOTICE/SBOM和上游签名链，无法审计发行义务 | §2.2、§3.1、§4.1/§4.3、Task 0/1/3 增加 `ThirdPartyComponentDescriptor`、生成式NOTICE/signed SBOM与third-party provenance |
| F7-C3 | P1 D02/D05/D10/D12/D13/D19 | bundled/PATH/managed来源混合，现有manifest version未进入消费链，`app-server --help`也不能证明版本/协议兼容；managed可能silent latest | §2.2、§3.1、§4.1、Task 0/3 增加 `ProviderExecutionComponentPolicy`、`ProviderCLIRuntimeDescriptor`、`ProviderCLICompatibilityMatrix`及三模式矩阵/不可变版本/显式consent |
| F7-C4 | P1 D02/D06/D09/D11/D15 | release health把CLI integrity、用户auth、vendor outage和用户PATH问题混成“Codex preflight失败”，会误回滚或漏回滚 | §2.2、§3.1、Task 0/2/3 增加 `ExecutionLayerFailure`，区分candidate-attributable rollback与provider-unavailable，并排除credential材料 |
| F7-C5 | P1 D03/D05/D12/D15 | P0逐工具quarantine只覆盖Codex toolbridge projection；Claude以raw `--mcp-config`直连，不能宣称provider-neutral | §2.3、§3.2、§4.2、Task 0/4 增加Codex-only范围、Claude `per_tool_quarantine=unsupported`与server-level fail-fast对照测试 |
| F7-C6 | P1 D02/D05/D11/D12 | Codex/Claude未知critical terminal/approval/tool/auth事件可能warn/drop继续执行，版本协议漂移缺少 fail-first | §2.2/§2.3、§3.2、§4.2、Task 0/4 增加 `ProviderProtocolDrift`、critical event fail-turn/session与兼容fixture |

该轮源码核对最初绑定 `origin/main@4093b7537d29b141fcc79ceb2e380641a31e3ec8`；审查期间本地与远端 `main` 前进到 `5482a52cfc256e1ee386dd3ce4e125b01e7dbc85`。Root 对两点做了重绑定：新增差异只改 package script 的 SQL LSP bundle项，Codex/Claude CLI启动、打包和manifest证据路径未变；本文当前事实基线统一更新为 `origin/main@5482a52cfc256e1ee386dd3ce4e125b01e7dbc85`。以上写回只表示 `seventh_review_findings=applied_pending_independent_recheck`；当前新对象仍须重新计算SHA并由新的独立Reviewer复核。

### 9.8 两 Agent 第八次对抗审查与第八次返修

两个新 Reviewer 分别从 Release/Packaging/CLI 与 Provider/MCP 方向，只读同一个第七次返修后的 785 行对象；review object SHA-256 为 `87ad8f023ec81a9756e626f4cc799d8ff6cb666a28c78c0b33db14df4dcc83d1`，authoring HEAD 为 `8cd4509994f316834a2f974a912cac855566f96a`，事实源码基线为本地与远端一致的 `origin/main@5482a52cfc256e1ee386dd3ce4e125b01e7dbc85`，二者均未修改文件。两个 Reviewer 各报告 4 个 P1；Root 按独立根因核对后接受全部 8 项，无 P0：

| ID | Priority/Dimension | Finding | 第八次文档修订 |
| --- | --- | --- | --- |
| F8-R1 | P1 D01/D10/D13/D17/D19 | `ReleaseUnitDescriptor.members` 与 component registry 只是并列清单，无法证明每个动态package member都有唯一、当前且归属匹配的component | 增加member `component_ref`、canonical `ComponentRegistry`与动态missing/stale/duplicate/ownership mismatch差集 |
| F8-R2 | P1 D10/D13/D17/D19 | signed SBOM/NOTICE没有绑定exact release，N-1合法签名材料可重放到N包 | 当时增加`ReleaseAttributionBundleDescriptor`双向绑定；该处置后由F9-03 superseded，当前契约改为含`release_unit_digest`的无环单向DAG |
| F8-R3 | P1 D02/D05/D10/D12/D19 | compatibility probe验证absolute path，真实spawn却可能再次按PATH解析叶名称，存在probe-to-exec TOCTOU | 当时增加absolute-path spawn与post-start identity；该处置后由F9-04 superseded，当前契约使用完整launch chain与`PreparedProviderExec` pre-exec primitive |
| F8-R4 | P1 D01/D02/D06/D12/D13 | 文档误称当前`RunDesktop`执行Codex CLI preflight，且把provider-start检查混入release health | 纠正当前事实；增加独立bundled-only只读release preflight，禁止PATH/managed/auth/provider-home/vendor network |
| F8-P1 | P1 D02/D05/D11/D12/D18 | provider-neutral pre-turn capability字段对Claude不可证明；静态map或首条用户消息后的`system:init`不能作为协商证据 | 将runtime evidence拆为`handshake|immutable_version_matrix|blocked`，固定matrix generation/hash、probe method、evidence source和required decision |
| F8-P2 | P1 D03/D05/D12/D15/D18 | Claude当前只写`--mcp-config`，没有v3-owned `tools/list`/schema验证或首条用户payload前的稳定MCP ready barrier | 报告`per_tool_quarantine=unsupported`与`provider_owned_mcp_status=unsupported|unproven`；无version-bound ready/failure证据时阻断MCP-enabled turn |
| F8-P3 | P1 D02/D05/D06/D11/D12/D19 | `ProviderProtocolDrift`缺session-local termination状态机，unknown critical event可能悬挂或迟到覆盖 | 增加translator前`ProviderProtocolGate`、three-scope failure、exactly-once terminal、pending清理、transport cancel、generation fence与late-event拒绝 |
| F8-P4 | P1 D11/D12/D17/D19 | 字段守卫漏掉protocol drift，并把多个独立producer压成单链，不能证明完整消费者覆盖 | 将§4.3拆成十二条独立producer chain，纳入runtime report DTO/contract/status/UI消费者和真实mutation RED |

两个 Reviewer 均明确报告 LSP namespace 不可用，没有把shell/git-object阅读冒充 LSP PASS。以上写回只表示 `eighth_review_findings=applied_pending_independent_recheck`；写回后的当前对象必须重新计算SHA并由新的独立Reviewer复核，不能复用 `87ad8f02...` 的结论。

### 9.9 两 Agent 第九次对抗审查与第九次返修

两个新 Reviewer 分别从 Release/Packaging/第三方CLI 与 Provider/MCP/Protocol/Runtime 方向，只读同一个第八次返修后的832行对象；review object SHA-256 为 `c425bb5ccd45a663c9a78e7496c8c63a613cff7125f9c4404964cebc8d976965`，authoring HEAD 为 `8cd4509994f316834a2f974a912cac855566f96a`，事实源码基线为本地与远端一致的 `origin/main@5482a52cfc256e1ee386dd3ce4e125b01e7dbc85`。两个 Reviewer 原始报告共17个P1；Root合并字段链重复、attribution subject/authority、pre-exec primitive/launcher-chain三个共同根因，并补充`failed_preflight`分支直接矛盾后，接受15个P1，无P0：

| ID | Priority/Dimension | Finding | 第九次文档修订 |
| --- | --- | --- | --- |
| F9-01 | P1 D01/D12/D13/D17/D19 | 声明member与最终产物实物没有独立双向闭包 | 增加final-artifact `ObservedArtifactInventory`、actual/declared双向差集、具名排除项和额外helper/DLL/dylib/symlink RED |
| F9-02 | P1 D10/D13/D17/D19 | 单个`component_ref`不能表示binary/bundle内嵌Go/npm/native依赖 | 增加primary/embedded refs、derived `ReleaseComponentGraph`与registry/SBOM node-edge双向闭包 |
| F9-03 | P1 D02/D10/D12/D13/D17/D19 | exact release digest/signing关系可能自引用或错阶段，attribution signer没有可信key owner | 增加含`release_unit_digest`的无环单向`ReleaseDigestGraph`、signature-excluded input、final-artifact/release-unit阶段和package-anchored `ReleaseAttributionTrustPolicy` |
| F9-04 | P1 D01/D02/D05/D10/D12/D13/D18/D19 | absolute path+post-start check不能保证替代binary零执行，也不能表达launcher/interpreter/shim chain | 增加`ProviderExecutableLaunchPlan`、platform `PreparedProviderExec`；无pre-exec primitive的模式保持blocked |
| F9-05 | P1 D01/D02/D05/D10/D12/D13/D19 | Claude auth/start/restart重复解析binary，Codex pool复用不绑定已解析process identity | 增加spawn-owned `ProviderProcessIdentity`并贯穿Claude生命周期、Codex pool key/reuse/session/runtime |
| F9-06 | P1 D02/D05/D12/D17/D19 | capability evidence平面混态，matrix未绑定exact provider/platform/source/signing subject | 改为strict `HandshakeEvidence | MatrixEvidence | BlockedEvidence`、exact-subject signed matrix及package-anchored `ProviderCLICompatibilityTrustPolicy`，payload signer不构成authority |
| F9-07 | P1 D01/D02/D03/D05/D10/D11/D12/D17/D19 | gate只在translator前仍太晚，raw event缺不可伪造third-party/synthetic来源域 | 固定bounded frame/minimal decode后最早host-issued opaque ingress authority -> gate -> `ValidatedProviderEvent`顺序，成功输出独立字段链且gate前零副作用 |
| F9-08 | P1 D02/D03/D05/D06/D09/D12/D15/D19 | Claude MCP ready未绑定process/config/manifest/turn代际 | 增加exact tuple `MCPReadinessAttestation`，start/restart/resume/refresh撤销旧证明，每次payload前CAS |
| F9-09 | P1 D01/D03/D05/D10/D11/D17/D18/D19 | provider attestation与MCP peer runtime report混为同一authority | 拆分provider-owned与peer-owned DTO/mapper/write authority，禁止peer覆盖CLI attestation或Claude取得v3 call authority |
| F9-10 | P1 D05/D09/D11/D12/D15/D17 | Claude MCP `unsupported|unproven`只存在文字，未进入canonical UI链 | 增加typed `ProviderMCPCapabilityStatus`贯穿provider/contract/DTO/uistate/RPC/frontend/composer |
| F9-11 | P1 D02/D05/D06/D11/D12/D17/D19 | ProtocolDrift缺host identity及session-manager/process/transport generation | drift由ingress authority注入host binding/generation；pre-init可路由，旧generation不误伤replacement |
| F9-12 | P1 D03/D05/D06/D10/D12/D17/D19 | call admission只绑定server generation，可换tool/schema/client/args或重放 | 增加single-use `AdmissionGrant`绑定host call/tool/schema/lifecycle/peer/client/process/canonical args和epoch |
| F9-13 | P1 D12/D17/D19 | “十二条独立链”实际仍按领域合并多个producer，可假绿 | §4.3按真实producer逐行拆分并补入`ValidatedProviderEvent`成功路径，不设固定数量；每行独立动态枚举、consumer mutation和`FIELD_CHAIN_ID` |
| F9-14 | P1 D05/D10/D11/D17 | evidence要求绝对CLI路径，与路径/secret脱敏不变量冲突 | 分离内部不可序列化identity与公开`ProviderExecutableAttestation`，规定redaction-before-digest |
| F9-15 | P1 D02/D06/D11/D13/D15/D19 | bundled failure无条件rollback与无pending必须Recovery直接矛盾 | release preflight/Task/DoD统一为active probation rollback、无pending独立Recovery、用户外部故障仅provider unavailable |

两个 Reviewer 均明确报告 LSP namespace不可用，没有把shell/git-object阅读冒充LSP PASS。第八轮F8-P4“拆成十二条”的处置在第九轮被F9-13重新判为不独立，现已按真实producer重拆。以上写回只表示`ninth_review_findings=applied_pending_independent_recheck`；写回后的当前对象必须重新计算SHA并由新的独立Reviewer复核，不能复用`c425bb5c...`的结论。

### 9.10 D01-D19 coverage ledger

| Dimension | Coverage | 证据/理由 |
| --- | --- | --- |
| D01 Architecture | `Applied` | observed artifact/dependency/signing owner、v3-owned/third-party CLI ownership、prepared process identity、provider-vs-peer runtime authority、最早ingress gate、selector/Recovery边界、aggregate/admission和authenticated principal boundary |
| D02 Fail-fast | `Applied` | actual/declared与dependency双向差集、digest cycle/trusted signer、pre-exec zero-substitute、strict evidence union、bundled-only static preflight、generation readiness、host-bound protocol gate、stale grant/prepared/auth proof失败 |
| D03 MCP protocol | `Applied` | Codex-only quarantine、Claude direct MCP typed unsupported/readiness、bounded frame -> opaque ingress -> gate、provider/peer authority分离、wire-only decode、authority snapshot、aggregate snapshot、exact admission与compiled schema |
| D04 LSP implementation | `N/A + BLOCKED` | 本 review object 不修改 mcp-lsp；强制 LSP 证据缺失另列 blocker，不写成 PASS |
| D05 Provider/runtime | `Applied` | complete launch chain/prepared exec/process identity、Codex pool与Claude auth/start/restart、strict handshake/matrix/blocked evidence、generation-bound MCP readiness/status、host-bound drift与exact call grant |
| D06 Orchestration | `Applied` | active-probation/no-pending分支、provider protocol generation termination、supervisor状态机、trust promotion/rollback、pre-healthy transaction、journal takeover、aggregate commit和grant drain/revoke |
| D07 Store/sqlc | `Applied` | 单事务提交 lifecycle/quarantine/per-server/aggregate/committed，prepared previous/lease/deadline/terminal状态与app read/admission ports；exact migration/sqlc由Task 0 LSP冻结 |
| D08 Skill/Prompt/Thread | `Applied` | Subagent Profile投影、pre-healthy Skill mirror writer guard，以及 start/resume/turn trusted owner ref + scope-bound authority |
| D09 Frontend | `Applied` | typed Claude MCP status全链、旧patch拒绝、app health/provider unavailable、protocol fatal、Recovery/frozen env/principal/Safe Mode状态与动作禁用 |
| D10 Security | `Applied` | final artifact/component graph、trusted attribution、pre-exec chain identity、公开attestation脱敏、CLI source/consent/package trust、opaque ingress origin、provider-peer authority、stale grant/replay与stdio/HTTP |
| D11 Observability | `Applied` | public executable attestation、redaction-before-digest、typed execution/startup branch、host-bound protocol drift、MCP readiness/status、attempt/write-set/trust、aggregate/prepared/principal lifecycle |
| D12 Testing | `Applied` | rogue artifact/dependency、digest cycle/signer、launcher swap/pool mismatch、evidence union、Claude generation readiness/UI、gate-before-side-effect、peer spoof、grant mix/replay及既有rollback/takeover/auth RED |
| D13 Release/Install | `Applied` | observed final artifact、embedded component/SBOM closure、digest DAG/trusted attribution、exact v3+third-party CLI chain、物理隔离feed、source/keyring/package policy、三代provenance、trust activation、rollback和Windows installer |
| D14 Performance | `Applied` | 单 schema 与 aggregate tool/schema/diagnostic budget、goroutine/heap 和 allocation cap |
| D15 UX/Product | `Applied` | 产品声明明确v3-owned与Codex/Claude third-party边界，app health/provider auth-outage分层，typed capability/readiness/unsupported、Recovery和stale/not-ready稳定呈现 |
| D16 Git/Workflow | `Applied` | 初稿/九轮返修对象分离、origin/main漂移重绑定、dirty隔离、artifact provenance、shared seams和当前对象pending independent recheck |
| D17 Field guard | `Applied` | §4.3已按真实producer逐行拆分并包含`ValidatedProviderEvent`成功链，不设固定数量；每行要求动态字段源、exact consumers、missing/stale/roundtrip/deep-copy、真实mutation与具名exemption |
| D18 DRY | `Applied` | shared resolver/gate/status/admission owner保留Codex/Claude、provider/peer、Unix/Windows的必要差异；不以错误统一模型隐藏launcher或authority差异 |
| D19 SSOT | `Applied` | component/observed inventory/dependency graph/release与matrix trust、prepared process identity、provider evidence/peer runtime、ingress gate/validated output、readiness/status、aggregate/grant、principal/token均有分离的单一可写owner |

### 9.11 Review blocker 与 residual

- 历次 Reviewer、Root 与本次返修会话都没有 `mcp__lsp.*` namespace；locate/inspect/xref/read/diagnostics 五类证据无法取得。shell/git-object 阅读不是 LSP PASS。
- 历史九轮对象和 findings 只证明审查沿革，不能证明当前文件。当前判定必须引用冻结后的 exact path/line/bytes/SHA、两条 fresh lane 的 start/end identity 与 `0 P0 / 0 P1`；非正确性 P2/P3 只登记后续处置。
- Reviewers 没有在用户脏 checkout 上把源码测试结果绑定成 `origin/main` GREEN；本轮实际门禁只证明 docs authoring checkout 的现有 gate plan，不替代未来隔离 P0 worktree。
- final artifact inventory与actual dependency/SBOM graph、含release-unit节点的release digest DAG与attribution trust、third-party CLI launch plan/prepared exec/process identity、strict capability union/matrix及package-anchored matrix trust、bundled-only static preflight、公开attestation/failure redaction、Claude generation-bound readiness/typed状态、最早ingress gate/`ValidatedProviderEvent`成功链/host drift、provider-peer runtime authority、§4.3全部真实producer字段链、legacy/transactional endpoint、package signer/source/keyring/trust-generation schema与激活顺序、updater数值、supervisor identity/deadline、StartupAttempt、normal process re-exec env、pre-healthy writer registry/journal、Recovery capability、MCP authority snapshot/current CAS、journal terminal/takeover、server vector/aggregate digest、exact `AdmissionGrant`、authenticated proof/Wails owner、workspace token、compiler budget、dependency gate与跨平台lock/fsync仍由Task 0以LSP/RED冻结。
- macOS 签名产物、Finder/open、Windows installer 和 Linux unsupported 尚无本轮真实外部边界证据。

## 10. 验收证据格式

每个实现 task 的 evidence 至少包含：

```text
TASK_ID
REVIEW_OBJECT: worktree / base SHA / head SHA / staged tree
OWNER_AND_PATHS
PRODUCT_IP_BOUNDARY: component ID / v3-owned or third-party ownership scope / claim scope / source registry / README-NOTICE-SBOM-About-release-note consumers / multi-language drift result
THIRD_PARTY_COMPONENT: component ID / origin / vendor / upstream version-URL-asset digest-signature / package signer / license expression-text hash / notice paths / distribution mode / protocol compatibility / credential owner / rollback policy
RELEASE_COMPONENT_COMPLETENESS: declared release member set / primary and embedded component refs / registry generation-digest / missing-stale-duplicate-ownership diff / package generator decision
OBSERVED_ARTIFACT_INVENTORY: schema and exact final artifact digest / platform signing-notarization-staple stage / normalized observed path-type-mode-link-target-content digest entries / observed-declared and declared-observed diff / attribute mismatch / named exclusions with direction-owner-reason-fixture
RELEASE_COMPONENT_GRAPH: scanner identities-versions / Go-npm-native-bundle actual nodes and relationships / primary-embedded refs / registry-SBOM node-edge bidirectional diff / unresolved-stale decision
RELEASE_DIGEST_GRAPH: ordered subject nodes-parent edges / algorithms-canonicalization ID / canonical bytes-exclusions-generation stage / signature-excluded payload / cycle-self-downstream-reference check / payload-tree-signed-bundle-final-artifact-release-unit-attribution-update one-way chain
RELEASE_ATTRIBUTION_BUNDLE: payload-tree-final-artifact-release-unit-registry-component-graph-inventory-NOTICE-SBOM digests / release_unit_digest presence and exact edge / generator-predicate / signer key identity-generation-usage-validity-revocation / package trust anchor / update-manifest one-way binding / replay-tamper-wrong-signer result
PREPARED_PROVIDER_EXEC: provider-component-source / complete launcher-interpreter-target chain identity digests / platform primitive and prepared handle-or-staging identity / probe-spawn same-object proof / pre-exec swap and zero-substitute result
PROVIDER_PROCESS_IDENTITY: prepared launch digest / matrix-source-process generation / PID start identity / effective chain identity / Claude auth-start-restart binding / Codex pool key-reuse-drain decision
PROVIDER_EXECUTABLE_ATTESTATION: provider-component-source / path class / file-volume identity digest / binary hash-version-source-process generation / raw path-HOME-provider-home exclusion
PROVIDER_CLI_RUNTIME: public executable attestation / CLI-transport-protocol / exact handshake-matrix-blocked variant / variant required-forbidden field validation / required decision / process generation / no raw path-provider-home-secret
PROVIDER_CLI_COMPATIBILITY: provider-component-source-OS-arch-launcher class / CLI range / transport-protocol-schema / required-optional capabilities / exact matrix subject-signature and replay decision
PROVIDER_CLI_COMPATIBILITY_TRUST: package anchor / policy identity / usage=provider_cli_matrix / algorithm-key identity-material hash / generation-validity-revocation / canonicalization-subject constraints / payload self-declare-wrong-key decision
EXECUTION_LAYER_FAILURE: public origin-provider-version-mode / failure domain / rollback eligibility / stable code / active-probation or no-pending decision / redaction-before-digest proof / raw HOME-path-secret-stderr exclusion
PROVIDER_INGRESS_AUTHORITY: private issuer / origin kind / provider-component-source / executable-process-transport identity and generation / host agent-thread-session binding / bounded-frame-before-gate evidence / raw origin-public Dispatch-context issuer rejection
VALIDATED_PROVIDER_EVENT: gate-only producer / ingress authority digest / host binding / process-transport-ingress generation / protocol-event kind-sequence / exactly-one typed payload variant / state-action-rawbus-translator mapper and terminal-consumer mutation / raw reconstruction rejection
PROVIDER_PROTOCOL_DRIFT: ingress authority digest / session-manager-transport-process generation / CLI-protocol / event class-digest-sequence-phase / host agent-thread-session-turn from binding owner / provider identity digest / decision-scope-code / exactly-once cleanup / stale-generation late event / redaction
PROVIDER_MCP_READINESS: provider-process-transport identity / target host turn / manifest-authority generation-digest / roots digest / sorted expected-observed server vector / matrix digest / sequence-expiry-revoke / current-ready-stale decision / pre-user-payload zero-call evidence
PROVIDER_MCP_STATUS_UI: typed state-code-generation-sequence / provider-contract-agent DTO-runtime-uistate-RPC-frontend mapper parity / stale patch rejection / composer state / backend send-gate / no log-string-generic-ready inference
MCP_PEER_RUNTIME_REPORT: authenticated peer issuer / allowed process-port-provider-presence fields / provider-owned CLI-matrix-readiness-status-failure-drift rejection / mapper-consumer parity
MCP_PEER_RUNTIME_AUTHORITY: private v3 issuer / current committed workspace-server member / peer-client-transport-process identity / lease-fence-expiry / provider-readiness conversion rejection / Claude direct no-authority result
ARTIFACT_PROVENANCE: generation / source commit / final artifact SHA / signer / release digest graph-registry-component graph-inventory-attribution hash / NOTICE-SBOM digest / source-keyring hash / manifest-attribution key identity-material hash-usage-validity / rotation-revocation / installed launcher-helper-Guard artifact-relative identity and CLI path class / PID start identity / cache-retry policy
PACKAGE_TRUST_POLICY: platform / ReleaseUnitDescriptor binding / signer identity / macOS Team ID or Windows publisher-thumbprint / allow_unsigned=false / production env-CLI-helper bypass rejection / dev-test artifact separation
TRUST_KEYRING: package-OS-release binding / authoritative public-key bytes hash / descriptor key hash match / current-previous generation / validity-usage / signer chain / rotation-revocation / env-self-sign rejection / network-before-fail
TRUST_GENERATION_STATE: committed generation / pending generation / transaction ID / healthy promotion or rollback-discard decision / keyring-marker-state fsync order / crash replay result
STARTUP_ATTEMPT: attempt ID / release-process-transaction identity / parent attempt-retry authorization / active probation or no-pending branch / preflight stage / transition code / persisted state / supervisor-Guard handoff / second-launch decision
NORMAL_PROCESS_ENV_PLAN: source hashes / parsed allowlist / inherited-protected-key conflicts / validation result / selector parent zero-write / transaction child explicit Cmd.Env / concurrent observer partial-state check / independent Recovery-Guard frozen allowlist
PRE_HEALTHY_WRITE_SET: dynamically registered writer-path-owner set / staging or versioned-home paths / old bytes-mode-hash-absence journal / healthy publish decision / rollback parity / unregistered-writer mutation result
LSP_LOCATE
LSP_INSPECT
LSP_XREF
LSP_READ
LSP_DIAGNOSTICS
FIELD_CHAIN_ID / PRODUCER / MAPPER_SQL_RPC_FRONTEND_SCHEMA_CONSUMERS / DYNAMIC_DIFF / ROUNDTRIP / MUTATION_RED / EXEMPTIONS
ADAPTER_PORT_DIRECTION: toolbridge consumer port / app runtime adapter / module Service / contract store port / archtest and production dependency evidence
MCP_AUTHORITY_SNAPSHOT: registry-config owner generation-digest-membership / canonical workspace-roots-agent-thread-session binding / ResolveMCPManifest decision / private ResolvedMCPManifest-CodexToolSurfaceScope association / current pre-write-pre-call membership CAS / serialization-persistence-log rejection / post-sign revoke-cross-scope mutation result
DURABLE_REFRESH: prepared terminal state / previous committed snapshot / lease-deadline-plan digest / fencing token / startup takeover or supersede decision / SQL transaction evidence / sorted server state vector / committed aggregate workspace generation-digest / candidate rebuild-swap / projection generation / notification-loss reconcile / restart point
MCP_CALL_ADMISSION_GRANT: grant-host call / workspace aggregate-server member / canonical tool / compiled schema-lifecycle / exact peer-client-process / canonical immutable args digest / epoch-expiry / single-use consume / mix-replay-revoke result / concurrent commit drain-cancel / zero stale or wrong-client call
RPC_AUTH_PROOF: entry guard or ctl-register proof / server-only AuthenticatedRPCConnection issuance / context-only RPCSessionPrincipal / raw WS and generic local Dispatch rejection / connection-window-app owner lifecycle / close-reconnect-revoke-cross-principal result
WORKSPACE_IDENTITY: authenticated principal / canonical registered roots / resolve(CWD) decision / CommittedMCPWorkspaceSnapshot aggregate generation-digest-server vector / opaque token binding-expiry-principal / subsequent RPC and admission validation
RED_COMMAND / EXPECTED_FAILURE / EXIT
GREEN_COMMAND / PASS_COUNT / EXIT
GUARD_COMMAND / EXIT
GENERATED_PRECHECK / REFRESH / POSTCHECK
REVIEW_FINDINGS_AND_DISPOSITIONS
RESIDUAL_RISKS
COMMIT_SHA
REMOTE_SHA
```

空日志、`[no tests to run]`、只写 PASS、旧 binary、一次绿色重跑、Reviewer 自报 DONE、clean local branch 或本地 commit 都不是完成证据。

## 11. 停止条件

出现以下任一情况立即停止对应生产 lane。Task 0-Design 可继续补齐冻结证据；Task 0-D0 只享有本节明示的两个目标文件诊断清零权限，不得扩展为 P0 行为实现：

- LSP namespace 不可用，或五类证据任一缺失。
- worktree/base/head 与 Task 0 review object 不一致。
- 发现用户未知 dirty、shared seam 并发写入或 generated artifact 无 owner。
- canonical `ComponentRegistry`、`ProductIPBoundary`、`ThirdPartyComponentDescriptor`、`ReleaseUnitDescriptor` primary/embedded refs、`ObservedArtifactInventory`、`ReleaseComponentGraph`、含release-unit节点的无环`ReleaseDigestGraph`、`ReleaseAttributionBundleDescriptor`/`ReleaseAttributionTrustPolicy`、`ProviderExecutionComponentPolicy`、`ResolvedProviderExecutable`/`ProviderExecutableLaunchPlan`/`PreparedProviderExec`/`ProviderProcessIdentity`、严格capability evidence union与`ProviderCLICompatibilityMatrix`/`ProviderCLICompatibilityTrustPolicy`、`ProviderCLIRuntimeDescriptor`/`ProviderExecutableAttestation`/`ExecutionLayerFailure`、`ProviderIngressAuthority`/`ProviderIngressEnvelope`/最早`ProviderProtocolGate`/`ValidatedProviderEvent`/host-bound `ProviderProtocolDrift`、`MCPReadinessAttestation`/`ProviderMCPCapabilityStatus`、provider observation/peer report/`MCPPeerRuntimeAuthority`三域分离、bundled-only release preflight、permanent legacy/transactional feed、签名 `UpdateSourceDescriptor`、不可覆盖 `PackageTrustPolicy`、package/OS/release-bound `UpdateTrustKeyring` 与 `TrustGenerationState`、key material/rotation/revocation/healthy activation、updater bootstrap/protocol/minimum-version、三代不可变 artifact provenance、supervisor OS identity/deadline/termination policy、StartupAttempt transaction branch/retry、`NormalProcessEnvPlan`/transaction child re-exec/独立 Recovery allowlist、动态 `PreHealthyWriteSet`、最早 Recovery selector/graph 禁用项、`MCPManifestAuthoritySnapshot`/scope-bound carrier/current membership CAS、app adapter/coordinator `CommittedMCPWorkspaceSnapshot`、prepared terminal/takeover/fencing/projection reconcile、精确single-use `AdmissionGrant`、`AuthenticatedRPCConnection`/`RPCSessionPrincipal`/Wails owner/workspace resolve/token、opaque MCP identity/dynamic reserved set 或 compiler loader/digest 策略未冻结。
- recovery lock、transaction/hash、schema resource budget 只能靠静默降级维持运行。
- §4.3 任一真实producer没有独立`FIELD_CHAIN_ID`，任一行合并多个独立producer，动态枚举、missing/stale/roundtrip/deep-copy/真实consumer mutation未覆盖，或exemption无direction/reason/evidence/owner；一行GREEN被用来替代其它producer，或新增command未进入canonical backend boundary registry。
- 产品声明把第三方CLI/依赖写成v3自研或全自有IP，或以v3打包、签名、安装行为改变上游ownership；任一第三方release member缺origin/vendor/version/digest/signature/license/NOTICE/SBOM/protocol/distribution字段。 bundled/managed 若没有针对 exact version/platform/distribution mode 的批准再分发依据也必须停止，不能用 license/NOTICE/SBOM 存在替代授权。
- final signed/notarized/stapled artifact未独立生成`ObservedArtifactInventory`并与声明双向等集，actual dependency graph与registry/SBOM node+relationship未双向闭合，排除项无version/direction/owner/reason/fixture，或`ReleaseDigestGraph`存在self/downstream reference、缺`final_artifact -> ReleaseUnitDescriptor -> ReleaseAttributionBundleDescriptor -> signed_update_manifest`单向edge、subject/canonicalization/algorithm/生成阶段不明确、attribution signer由payload自报或缺package-anchored usage/generation/validity/revocation。
- packaged模式访问vendor network或回退PATH，PATH模式静默转managed，managed默认开启、未显式consent、使用silent latest或未固定上游asset；runtime版本/协议未知、越界或只凭`--help`/硬编码capability仍继续启动。launch chain任一launcher/interpreter/shim target未进入`ProviderExecutableLaunchPlan`/`PreparedProviderExec`，spawn/auth/restart重新解析PATH或shim，平台只能post-start发现替代执行却仍宣称zero-call，Claude重复解析binary，或Codex pool在prepared/matrix/source/process identity不一致时复用旧process。
- bundled-only release preflight执行第三方CLI、调用PATH/managed install、用户auth/provider-home或vendor network，复用provider-start `EnsureCLIAvailable`；active probation candidate failure未rollback，无pending同类app failure却触发release rollback而非Recovery，或用户CLI/auth/outage进入release health。
- 内部absolute path、HOME/provider-home、原始file identity、token、credential/auth材料、原始payload/stderr或其低熵稳定hash进入公开attestation、runtime DTO、RPC、frontend、日志或evidence；redaction未先于event/payload digest。
- capability evidence不是严格`HandshakeEvidence | MatrixEvidence | BlockedEvidence`判别联合、variant可混填/缺必填字段，matrix未绑定provider/component/source/OS/arch/launcher/version/protocol/schema/capability/trust-policy exact subject，或`ProviderCLICompatibilityTrustPolicy`不是package-anchored `provider_cli_matrix` usage、key material/generation/validity/revocation/subject constraint缺失、payload signer可自选authority，或静态map、Codex结果、Claude post-first-message `system:init`被冒充pre-turn evidence。
- Claude raw `--mcp-config`被描述为逐工具quarantine；`MCPReadinessAttestation`未绑定exact process/transport/target turn/manifest-authority/matrix/server vector，旧ready、partial/extra server、config/process换代仍可发送user payload；typed `ProviderMCPCapabilityStatus`任一mapper缺失、`unsupported|unproven`或enum外输入被显示ready，或UI从日志/generic session/Codex状态推断Claude ready。
- provider raw frame在host-issued opaque ingress authority和`ProviderProtocolGate`之前发生drop、identity/tool/turn mutation、approval/action、raw-bus或translator副作用；origin/host identity可由payload/DTO/public Dispatch伪造，unknown critical可warn/drop；成功路径`ValidatedProviderEvent`缺authority/host binding/generation/protocol/event-kind/sequence字段、variant可混态、mapper/consumer回读raw补值，或Drift缺host/session-manager/process/transport generation导致旧event误伤replacement或provider ID缺失时无法exactly-once终止current host scope。
- MCP peer `ctl/report runtime`可写或覆盖CLI identity/version/matrix/readiness/status/failure/drift，provider readiness可转换成v3 peer/client call authority，或Claude direct路径可取得v3-owned admission。
- focused test 显示 `[no tests to run]`、平台测试被错误跳过或 package smoke 只验证文件存在而未验证运行行为。
- 单一 latest 覆盖 legacy bootstrap、env/self-signed key 成为验签 authority、descriptor key hash 与 keyring 不匹配、revoked/expired/wrong-usage key 可验签、production package signer/Team ID/publisher-thumbprint 可被 env、CLI 或 helper `allow-unsigned` 降级，或 trust failure 发生在网络请求之后。
- pending trust generation 在 candidate healthy 前成为 committed floor、rollback 后未丢弃 pending generation、旧 release 被 anti-rollback floor拒绝、keyring/state/marker fsync顺序无法逐 crash point恢复，或 healthy commit早于 trust generation与release-unit原子发布。
- stale process/bundle/video/deploy env 能覆盖 packaged `UpdateSourceDescriptor`，selector parent 改写 normal env、transaction child 未使用完整显式 `Cmd.Env`、同进程 Recovery/Guard 继承 normal env、并发 observer 能看见多键部分状态，跨代 E2E 使用同一 HEAD/current helper/cache/retry，或任一平台混装后仍能启动 desktop。
- candidate healthy 前任一持久写入者未动态登记进 `PreHealthyWriteSet`、共享 Codex `config.toml`/Skill mirror 等未 staging 或未记录旧 bytes/mode/hash/absence、rollback 后文件与旧 release 不一致，或 healthy publish 不能与 transaction journal收敛。
- active probation 的 packaged runtime/LSP/env/assets或bundled CLI integrity/protocol等candidate-attributable preflight failure进入 Recovery/等待用户而没有通知原 supervisor/Guard并立即回滚旧release；无 pending的同类首次失败未写 `failed_preflight`/`recovery_required`就退出或被下次Begin覆盖；retry没有显式authorization/parent lineage。
- Recovery graph 仍创建 forbidden normal constructors，或 quarantine 工具仍能进入 provider projection。
- toolbridge 直接导入 module/store、production dependency 未注入 coordinator/read/admission port，raw DTO/Name/URL/`TrustedServerID` 能伪造 managed identity，或 raw turn/provider/config/store/log/wire 可携带 opaque handle；app resolver 未以 registry/config owner authority generation+digest+membership 和 canonical roots/agent/thread/session 生成 `MCPManifestAuthoritySnapshot`/`ResolvedMCPManifest`，签发后删除、换代或跨 scope重挂未在 pre-write/pre-call CAS阻断。
- opaque identity/reserved-name/identity failure 后存在任意 lifecycle/quarantine/surface 写入，refresh journal/lifecycle/quarantine 不能同事务提交，出现 durable `applied`，prepared 没有 `committed|aborted|superseded` 终态、previous committed/lease/deadline/plan digest 或 startup takeover，旧 fencing token/进程发生 ABA，或单数 workspace generation无法表达 sorted per-server state vector与aggregate digest。
- catalog/provider/proxy/call 任一入口在prepared/current-read failure/aggregate generation-digest mismatch后仍调用client；`AdmissionGrant`未绑定host call/canonical tool/compiled schema/lifecycle/exact peer-client-process/canonical immutable args，能够序列化、换绑、重放、重复消费、过期继续使用，validate后args可变，或concurrent N+1不能撤销未消费grant/有界drain已进入exact旧client的grant；notification丢失时旧surface仍可调用或不能reconcile，surface release删除durable diagnosis。 同一 `(principal_id,host_call_id)` 存在第二个 active/terminal grant，或 client 结果为 `MayHaveEntered` 时释放 reservation/重签/重试，也必须停止。
- `AuthenticatedRPCConnection` 可由请求字段或业务 handler 自造，HTTP/WS entry guard 或 control `ctl/register` 未完成就能签发 `RPCSessionPrincipal`，raw `/ws` 或通用 local Dispatch 能获得 workspace scope，token 不绑定 transport/Wails owner，Wails per-window owner 未在关闭时撤销且未明确选择 app-lifetime owner，fresh client无法经 principal-bound `toolbridge/workspace/resolve(CWD)` 获得 scope，客户端可自填 workspace ID/roots，或 missing/tampered/expired/cross-principal/reconnected/revoked token、旧 aggregate generation 能覆盖/删除新状态。

## 12. Definition of Done

吸收决策内容完成条件（对应 `absorption_decision_content_complete=true`）：

- 初稿三个独立 Reviewer 都返回 coverage、finding 和 residual risk，且绑定初稿 review object。
- Root agent 已裁决并写回历史九轮 finding；历史 hash 仅作沿革，不得冒充当前对象 PASS。当前对象的 review PASS 只存在于绑定 exact SHA 的外部双 Reviewer 证据中，不回写文档制造自证循环。
- 吸收优先级、clean-room 边界、事实基线、禁止范围和 P0/P1/P2/P3 分层经当前仓库验证。
- docs-only diff 通过 whitespace/path/文档相关校验；未改写用户 dirty 文件。

实施设计完成条件（切换 `implementation_design_complete=true` 前全部满足）：

- Task 0 evidence 绑定隔离 worktree、最新 `origin/main` 和五类 LSP 证据。
- canonical `ComponentRegistry`、`ProductIPBoundary`、`ThirdPartyComponentDescriptor`、`ReleaseUnitDescriptor` primary/embedded refs、`ObservedArtifactInventory`、`ReleaseComponentGraph`、含release-unit节点的无环`ReleaseDigestGraph`、`ReleaseAttributionBundleDescriptor`/`ReleaseAttributionTrustPolicy`、`ProviderExecutionComponentPolicy`、不可序列化`ResolvedProviderExecutable`/`ProviderExecutableLaunchPlan`/`PreparedProviderExec`/`ProviderProcessIdentity`、严格`HandshakeEvidence | MatrixEvidence | BlockedEvidence`与exact-subject `ProviderCLICompatibilityMatrix`/package-anchored `ProviderCLICompatibilityTrustPolicy`、`ProviderCLIRuntimeDescriptor`/公开`ProviderExecutableAttestation`/`ExecutionLayerFailure`、Codex/Claude MCP capability scope、最早opaque `ProviderIngressAuthority`/`ProviderIngressEnvelope`/`ProviderProtocolGate`/`ValidatedProviderEvent`/host-bound `ProviderProtocolDrift`、generation-bound `MCPReadinessAttestation`、typed `ProviderMCPCapabilityStatus`、provider observation/peer report/`MCPPeerRuntimeAuthority`三域分离和bundled-only release preflight，以及permanent legacy/transactional feed、签名 `UpdateSourceDescriptor` + `PackageTrustPolicy` + `UpdateTrustKeyring`/`TrustGenerationState`/packaged signer precedence/key rotation-revocation/healthy activation、updater protocol/bootstrap rollout、supervisor/Guard handoff 与有界 OS process identity、StartupAttempt transaction branch/retry/fsync、`NormalProcessEnvPlan`/transaction child re-exec/独立 Recovery allowlist、动态 `PreHealthyWriteSet`/healthy publish/rollback parity、首次失败 selector 和独立 Recovery graph、app-owned `MCPManifestAuthoritySnapshot`/scope-bound carrier/current membership CAS、opaque MCP identity/dynamic reserved-name guard、app-adapter durable coordinator/prepared terminal takeover/fencing/SQLite transaction/`CommittedMCPWorkspaceSnapshot`/精确single-use `AdmissionGrant`/projection reconcile、server-only `AuthenticatedRPCConnection`/transport-issued RPC principal/Wails owner/workspace diagnosis owner/resolve token、transport decode seam、canonical compiled-schema digest、单项/aggregate compiler budget和§4.3按真实producer动态枚举的全部独立字段链都有唯一owner、exact landing files、动态字段源、全部消费者、具名 RED/GREEN 测试、命令、fixture 与预期失败断言。真实三代 artifact provenance 和最终产物验证结果属于 P0 实现完成证据，不作为 Task 0 设计冻结的前置输入。
- 当前对象冻结 exact path/line/bytes/SHA，并由两条 fresh Reviewer lane 复核到 `0 P0 / 0 P1`；不影响正确性的 P2/P3 有明确后续处置即可，不要求为此扩写内联 schema。
- Task 0 同时裁决 `implementation_design_complete=true` 与 `p0_executable=true`；任一 blocker 存在时两个状态都保持 false。

P0 实现完成条件：

- README/NOTICE/signed SBOM/About/release note由canonical `ComponentRegistry`证明v3-owned范围和Codex/Claude等第三方CLI归属；final signed/notarized artifact的observed/declared entry集合及属性完全一致，actual Go/npm/native/bundle dependency graph与registry/SBOM nodes+relationships闭合。release digest DAG按`payload_tree -> signed_bundle -> final_artifact -> ReleaseUnitDescriptor -> ReleaseAttributionBundleDescriptor -> signed_update_manifest`单向绑定、无环且阶段/canonical bytes明确，attribution含exact `release_unit_digest`并由package-anchored `release_attribution` usage验签；缺edge、N-1/cross-artifact replay、self-cycle、签名后篡改、unknown/expired/revoked/wrong-usage signer均RED。任何产品声明都不把打包或签名写成ownership/IP转移。 bundled/managed 的每个第三方 exact version/platform/distribution mode 还必须有批准的再分发依据；否则保持 BLOCKED 或 user-provided。
- packaged/PATH/managed CLI模式互斥且来源可观测。Codex和Claude所有auth/start/restart/pool reuse只消费同一`PreparedProviderExec`/`ProviderProcessIdentity`；完整launcher/interpreter/shim target任一节点替换时第三方替代代码零执行，无法提供平台pre-exec primitive的模式保持blocked。bundled-only release preflight在healthy ACK前只做release-bound chain digest/vendor signature+由package-anchored policy验证的immutable matrix静态校验，不执行第三方CLI，也不访问PATH/managed/auth/provider-home/vendor network。capability evidence严格属于一个`HandshakeEvidence | MatrixEvidence | BlockedEvidence` variant，matrix绑定exact provider/component/source/OS/arch/launcher/version/protocol/schema/capability/trust-policy subject；`ProviderCLICompatibilityTrustPolicy`固定`provider_cli_matrix` usage、algorithm/key material/generation/validity/revocation/canonicalization/subject constraint，payload signer不能自选authority，wrong/expired/revoked/wrong-usage与跨subject重放均RED。静态map或post-user-message init不算pre-turn evidence。 provider spawn 的同一 operation 在 prepare/spawn/reconcile/close 期间保持 sole owner；错误但可能已启动时保留 fence、禁止第二次 spawn，直至 exact process reconciliation。
- `ExecutionLayerFailure`正确区分active probation bundled candidate integrity/protocol/launch-chain故障、无pending同类app故障与用户CLI/auth/vendor outage：前者只走exact rollback，第二类只走独立Recovery，用户外部故障只降级provider且不增加release crash count。公开runtime/failure/RPC/frontend/evidence只消费`ProviderExecutableAttestation`和redaction-before-digest结构摘要，不含绝对path、HOME/provider-home、credential、原始payload/stderr或其低熵稳定hash。
- Codex trusted external MCP坏工具逐项隔离；Claude raw `--mcp-config`明确`unsupported|unproven`，只有current generation-bound `MCPReadinessAttestation`与exact process/transport/target-turn/manifest-authority/matrix/server-vector完全匹配才开放MCP-enabled payload，否则user payload为零。typed `ProviderMCPCapabilityStatus`从backend贯穿contract/agent DTO/runtime/uistate/RPC/frontend/composer，旧patch与自由文本不能伪造ready。每个真实第三方frame都在bounded framing/minimal decode后、任何drop/state/action/raw-bus/translator前由verified spawn/session owner私有签发不可序列化`ProviderIngressAuthority`并进入唯一gate；gate-only `ValidatedProviderEvent`把authority/host binding/generation/protocol/event kind/sequence/exact typed variant完整传到state/action/raw-bus/translator，任一mapper/终止consumer漏字段、混variant或回读raw都RED。raw payload/DTO/context/public dispatch不能造origin或host binding。critical drift只使用authority绑定的host identity与session-manager/process/transport generation做exactly-once终止，旧generation late event不能影响current session。provider attestation不能成为peer authority；Claude direct无`MCPPeerRuntimeAuthority`或v3-owned admission，Codex也只能从current committed v3 peer/client lease取得authority。
- 更新成功后 backup 保留到 healthy commit；启动崩溃能恢复完整旧 release unit。
- 永久 legacy feed 在 transactional v2 成为 latest 后仍只向任意从未升级的 pre-P0 旧版提供 old-compatible bootstrap；bootstrap 切换到物理隔离的 transactional feed后，下一版才启用 transaction manifest，低于最低 updater protocol 的 transactional client 稳定返回 `manual_upgrade_required`。
- packaged production 只消费签名并与 release unit 绑定的 `UpdateSourceDescriptor`、不可被 env/CLI/helper降级的 `PackageTrustPolicy` 和 package/OS/release-bound `UpdateTrustKeyring`；macOS Team ID、Windows publisher/thumbprint、实际验签 key bytes、identity/hash、algorithm、usage、validity、generation 和 signer chain 都来自该权威链，production `allow_unsigned` 恒为 false，旧 process env、bundle `.env`、`video.env` 和部署变量均不能覆盖 feed/key/signer。descriptor/keyring/package signer tamper、unknown/revoked/expired/wrong-usage key 与 committed generation rollback 在网络请求前失败，bootstrap -> transactional rotation 有受测 overlap/revocation，dev/test override 与发布产物物理隔离。`TrustGenerationState` 只在 candidate healthy 后将 pending generation提交为committed；rollback丢弃pending并允许旧release启动，所有keyring/state/marker crash point可恢复。
- mixed install、损坏事务、hash/lock/restore 失败全部 fail-closed。
- detached updater 能在首次 probation 硬崩溃后立即恢复；无 PID、无/错 ACK 和卡死 PID 都在有界 deadline 内进入稳定状态，绝不杀无关进程；监督中断时 next-launch preflight 能交接 detached Guard，在 desktop stack 不可用时恢复完整旧 release unit。
- 每次 normal candidate 都在 preflight 前持久化带 transaction/parent lineage 的 `StartupAttempt`；active probation 的 packaged runtime/video/frontend或bundled CLI integrity/protocol任一candidate-attributable首次失败写入 stage-bound `failed_preflight -> rollback_requested`，通知 exact supervisor/Guard、退出并立即恢复/reopen旧 release，绝不进入 Recovery UI 或等待 ACK deadline。用户CLI/auth/vendor故障不进入该链。无 pending probation 的同类app失败才写 `recovery_required` 并选择独立 Recovery process/graph/Guard，第二次启动不能覆盖证据，只有显式 authorized retry 可创建 parent-linked attempt。normal `.env`/`video.env` 必须先形成完整 `NormalProcessEnvPlan`，selector parent保持零normal-env写入，再以transaction child re-exec的显式 `Cmd.Env` 发布；并发observer看不到部分多键状态，Recovery/Guard独立进程只消费frozen allowlist。所有candidate healthy前持久写入者动态登记进 `PreHealthyWriteSet`，只写staging/versioned home并记录旧bytes/mode/hash/absence；healthy后统一publish，失败/rollback完整恢复，未登记写入者由guard阻断。高风险 normal constructor 和 provider/project/sidebar/thread/update 调用均为零，且 `recovery_ready` 不提交 backup。
- pre-P0/bootstrap/transactional next 的真实安装产物分别绑定独立 commit/artifact/helper SHA，在 clean VM/user 下禁 current binary、cache 和 retry 完成跨代 E2E；任一 provenance 缺失不得计为 GREEN。
- external HTTP/stdio provenance 只由 app-owned `ResolveMCPManifest` 使用 registry/config owner签发的 `MCPManifestAuthoritySnapshot` + trusted agent/thread/session ref + canonical workspace roots签发；opaque、不可序列化 `ResolvedMCPServerIdentity` 只通过内存态 `ResolvedMCPManifest` 进入对应 `CodexToolSurfaceScope`，pre-write/pre-call current membership CAS阻断签发后删除、owner generation/digest换代和跨scope重放，raw DTO/wire/config/store/log 不携 handle，也不能由 Name/URL/字符串相等重建。动态完整 managed reserved set、zero/forged/handle-binary/snapshot mismatch 与 turn/thread/start/provider resolver-bypass mutation 都 fail-first；Codex路径第三方坏MCP tool被隔离、正常同级工具继续可用，一方MCP保持fail-fast，Claude直连明确不在该quarantine集合，stdio allowlist/HTTP egress不退化。
- identity 缺失/空白/duplicate-key/重复和 managed payload/schema 失败均零 lifecycle/quarantine/surface 写入；toolbridge 只经 consumer port + app adapter 调用 module coordinator。coordinator 先持久化含previous committed、lease/deadline、plan digest和fencing的 `prepared` journal，再以单一 commit transaction/transactional outbox 原子提交 manual-preserving lifecycle batch、quarantine generation、sorted per-server states、`CommittedMCPWorkspaceSnapshot` aggregate generation/digest 和 `prepared -> committed`；crash遗留prepared由startup CAS takeover后转为`committed|aborted|superseded`，committed aggregate snapshot是唯一durable truth。每次真实MCP副作用调用都使用不可序列化、一次消费的`AdmissionGrant`，完整绑定principal/host call、workspace/server committed generation-digest、canonical tool、同一`CompiledToolSchema` digest、lifecycle、current `MCPPeerRuntimeAuthority`、exact peer/client/process generation和canonical immutable args digest；换绑、深拷贝后args mutation、replay、重复消费、expiry与concurrent N+1 revoke均在client前失败。notification只作加速；candidate reload/digest/swap/notification任一失败都撤下旧surface并可reconcile，旧fencing token/process无ABA，不存在durable `applied`。 admission owner 对 `(principal_id,host_call_id)` 保证唯一 active-or-terminal reservation；不确定 call 结果不得释放、重签或重试，必须先 reconcile 到 `NoEntry` 或 `EffectResolved`。
- external `$ref` 和单项/aggregate 超预算 schema 被阻断，canonical compiled-schema digest、quarantine 与 lifecycle 各只有单一事实链；HTTP/WS entry guard 或 control `ctl/register` 成功后由server-only `AuthenticatedRPCConnection` 为每个连接签发context-only `RPCSessionPrincipal`，raw `/ws` 与generic local Dispatch不能签发或取得scope。Wails明确选择per-window owner并在关闭时revoke，或记录并测试app-lifetime owner，不能使用未定义的local等价身份。fresh client以principal-bound `toolbridge/workspace/resolve(CWD)` 获得workspace/aggregate-generation/expiry-bound token，后续token + `CommittedMCPWorkspaceSnapshot` CAS/admission阻止客户端自填principal/scope、跨principal/workspace和stale refresh污染；close/reconnect/revoke使旧token失效。SurfaceID不入durable identity且surface release不删除diagnosis。
- §4.3按真实producer动态枚举的全部独立P0字段链都以动态差集、missing/stale、roundtrip/deep-copy和真实producer/mapper/终止consumer mutation RED覆盖；每行只有一个producer truth或明确方向的registry producer。无效exemption、把多个producer合成一条、固定数量冒充覆盖或只在测试手写字段清单不得计为GREEN。
- focused、archtest、package smoke、generated drift、hook gate 和独立复核证据全部绑定 exact review object。
- merge/push/release 状态按本地和远端 SHA 如实报告，不把 docs plan 或局部绿色误报成生产完成。
