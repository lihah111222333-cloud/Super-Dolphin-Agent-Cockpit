# Release 最终总审修复证据

日期：2026-07-17

基线：`6d6b8996b871015908bc26be8a647e950eb17f48`

分支：`codex/reasonix-p0-final-release-fixes`

## 1. Finding RED / GREEN

| Finding | RED | GREEN 实现 | 锁定测试 |
| --- | --- | --- | --- |
| 1. pending replay 与 rollback restart receipt | 文件恢复完成后到旧版本 restart/receipt 之间无 durable 协议；进程崩溃可重复启动或永久不启动。 | rollback 请求前持久化 256-bit launch token intent；`rollback_pending` replay 完成 rename 后进入 `rolled_back`；Guard 用 exact token 重发现已启动进程，校验 PID/start token/executable identity+digest 后持久 ACK。这里只宣称可重放收敛，不宣称 exactly-once。 | `TestGuardReplaysCommitPendingToCommitted`、`TestGuardReplaysRollbackPendingAndConvergesRestart`、`TestCrashReplayCompletesRollbackIntent`、`TestRollbackRestartIntentSurvivesRenameAndConvergesOnce`、`TestRollbackRestartRecoversLaunchBeforeACKWindowByToken`、Guard 两类 process E2E。 |
| 2. 正式 release 密钥连续性 | 本地 APP/DMG 可满足正式 gate；初版修复仍把前一 tag 与 `releases/latest` 资产查询拆开，并以旧 app 自身 TeamIdentifier 作为 trust signer 期望值。 | publish 先从 `releases/latest` 固定前一正式 tag；verify-existing 从非 draft/非 prerelease 列表排除候选 tag 后固定前一正式 tag。URL/digest/size 随后只查询 `releases/tags/$formal_previous_tag`。本次 staging 的 canonical Darwin DMG 必须只有一个顶层 app，并先通过 `codesign --verify --deep --strict`；其 TeamIdentifier 是独立 signer 锚。上一 exact release app 的有效 codesign signer 与 package-trust signer 均必须匹配该锚，同时继续校验版本、repo provenance、digest/size 与 key continuity。本地 override 仅允许显式 dry-run/test。 | `TestGitHubReleaseContinuityUsesExactPreviousTagEndpoint` 记录并断言三项资产字段均来自 `v9.9.8` exact-tag endpoint，且 latest 不提供资产；`TestGitHubReleaseContinuityRejectsSelfSignedPreviousSigner` 与 `self signed attacker` case 锁定旧 codesign/trust 均为 `TEAM-ATTACKER`、当前可信 signer 为 `TEAM-EXACT` 时失败；原伪造 app、多 app、version/signer mismatch 覆盖保留。 |
| 3. exact process termination PID reuse TOCTOU | 核验 stable identity 后仍按 PID 发 TERM/KILL，核验与 signal 之间可复用 PID。 | Darwin arm64 candidate 启动 0600 Unix socket，以随机 token 认证；receiver 仅在 constant-time token 匹配后返回 ACK 并自行退出。调用方 ACK 后只观察原 stable identity 消失，不再发送 PID signal。非 Darwin API 返回 unsupported，capability 不扩大。 | `TestCooperativeTerminationAuthenticatesBeforeACK`、`TestTerminateExactProcessUsesAuthenticatedCooperativeExit`；旧 signal-based exact 测试与实现已删除。 |
| 4. wall-clock terminal selection | `SelectForTarget` 以更新时间选择终态，时钟回拨可恢复旧 transaction/trust。 | 每 target 在独立 generation lock 下扫描 durable journal 并分配严格递增 `target_generation`；journal 原子写入+目录 fsync 后才发布。active transaction 仍唯一，终态只按 generation 选择并拒绝零值/重复值。 | `TestSelectForTargetUsesGenerationAcrossClockRollback`、`TestCreateAllocatesMonotonicTargetGeneration`。 |
| 5. package path trust | executable/bundle/resources/helper/trust/transaction path 可经 symlink 或系统外 alias tree 绕到非 canonical 内容。 | package layout 从 clean absolute executable 推导，逐层要求 canonical existing path；trust 文件/helper 也必须 canonical；暂时不存在的 rename target 要求最深现存祖先 canonical。 | `TestPackageLayoutRejectsExecutableAlias`、`TestPackageLayoutRejectsResourcesAlias`、`TestVerifiedPackageTrustRejectsHelperAlias`、`TestPackageTrustRejectsTrustFileAlias`、`TestPackageTrustRejectsTransactionTargetAlias`。 |

## 2. 验证结果

| 门禁 | 结果 |
| --- | --- |
| focused Go：updater/Guard/release-manifest/agent-terminal/appupdate/recovery/pidregistry/runtimeenv/scripts | PASS |
| race：updater/Guard/appupdate/recovery/pidregistry/app | PASS；Darwin linker 仅有既有非致命 `LC_DYSYMTAB` warning |
| Guard process E2E 两场景 `-count=10` | PASS，20 次 |
| independent artifact rollback/healthy E2E `-count=3` | PASS，6 次 |
| full `scripts` | PASS |
| full `internal/archtest` | PASS；初次 RED 的 8 个生产函数和 4 个测试函数复杂度均已拆分并清零 |
| frontend embed build/verify 与 host `agent-terminal` test | PASS；embed smoke hash `a89d4c965d9d8b69b6ab7c3ad1e29cb0f0bf130a47be4ee3c32e5f4154010334` |
| `CGO_ENABLED=0` affected package 六目标 cross-build | PASS：darwin/linux/windows × amd64/arm64；只证明 capability 包可编译，不宣称非 Darwin 原生更新 E2E |
| codemap/project-map/capcontract refresh + check | PASS；project-map 已同步 10 个新增源码/测试文件，其他 generated artifacts 无 drift |
| Bash syntax、`git diff --check` | PASS |

追加初审修复聚焦门禁：exact-tag continuity、self-signed signer attack、previous DMG proof、verify-existing、latest download/inspect 均 PASS。

## 3. LSP 证据

- 完整链：`grep` 定位、`structure` 大纲、`inspect(definition)` 理解、`xref(references)` 影响面、`file(read_file)` 精读、`patch_edit` 修改、`file(diagnostics)` 复核。
- 34 个 changed Go source/test 文件分两批 diagnostics：Error/Warning/Information/Hint 均为 0。
- 2 个 changed shell source 文件以 `language_id=shellscript` diagnostics：全部 severity 为 0。
- `.env.packaging.example` 无语言 adapter，以 scripts guard test 锁定字段用途。

## 4. 残余边界

- rollback restart 是 token-bound at-least-once convergence：若进程在 ACK 前已死，重放会再次启动；不声称不可实现的跨进程 exactly-once。
- Darwin arm64 是唯一开启 check/install/publish 的生产目标。Linux、Windows 与 Darwin amd64 仅验证 fail-closed capability 和 cross-build，不声称原生终止/安装 E2E。
- 正式 continuity gate 依赖 exact-tag GitHub release API 提供 asset digest/size，下载后做本地复核；signer 锚来自本次 canonical staging package，旧包自身字段不能建立信任；本地 APP/DMG 不能满足 publish 或 verify-existing。
