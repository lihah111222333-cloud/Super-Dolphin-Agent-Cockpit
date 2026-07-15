# 前端可维护性与错误可发现性 90 分提升计划

> **状态：** PROPOSED / docs-only execution contract
>
> **写作快照：** `codex/go-ai-gap-push-guards-20260715@8cd4509994f316834a2f974a912cac855566f96a`
>
> **本地 `origin/main`：** `4093b7537d29b141fcc79ceb2e380641a31e3ec8`
>
> **核心原则：** 目标结构尚未落地。评分治理必须先于被评分实现独立落地并获批；只有从该 governance base 派生的 clean `SUBJECT_SHA` 上，生产根因修复、fail-first 回归、production-shape 失败注入、真实 package provenance、完整门禁和外部 composite attestation 全部闭环后，才允许声明达到 90 分。

**Goal：** 将写作快照中的前端可维护性估算分 `61.8/100` 先转换为可机械复算、由独立治理提交锁定的执行基线，再提升到至少 `90.0/100`；解除因“失败伪成功”和“关键用户动作错误不可见”触发的 G2 生产门禁，并把错误可发现性从调用方约定升级为不可绕过、可失败、可复评的工程契约。`61.8` 在 Task G0 scorer 复算前只作历史输入，不是可复用的当前事实。

**Architecture：** 后端和 bridge 仍是运行事实来源；前端新增单一状态判定入口，把中断请求映射为非终态 `interrupt_requested`，只把契约已归一的最终事实映射为互斥的 `success / failed / interrupted / cancelled` UI 终态。关键异步动作必须显式选择用户可见错误、持久 health/diagnostics 或受控升级出口之一。静态守卫、跨层行为测试和真实桌面失败注入共同证明错误不会被 console、空对象、默认值或成功提示吞掉。

**Tech Stack：** React 19、Zustand、TanStack Query、Vitest、Testing Library、Vite、现有 Wails/JSON-RPC bridge、Go turn/provider DTO、仓库内 MCP-LSP、现有 frontend contract/code-size/z-index/critical-skip guards。

**Verification Surface：** `frontend-app` 页面、实体状态、bridge、错误边界、Prompt History、测试与脚本；`internal/dto/turn`、`internal/provider/codexapp` 和 Wails event surface；`cmd/agent-terminal/web-dist` 仅作为规范构建同步生成物；pre-commit、pre-push、frontend embed、真实桌面 RPC/UX failure smoke 和独立复评。

---

## 0. 基线、范围与执行前提

### 0.1 写作快照不是执行基线

写作时当前分支相对本地 `origin/main` 为 behind 5 / ahead 0；`frontend-app`、`cmd/agent-terminal/web-dist`、`.githooks`、`scripts/ai_maintenance`、`Makefile` 和 `scripts/frontend_embed_verify.sh` 在 `HEAD..origin/main` 范围没有差异。全仓存在用户和其他任务的未提交改动，因此本文只能记录 planning snapshot，不能把当前工作树视为批准后的执行现场。

执行 Task 0 时必须重新记录：

```bash
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
git rev-list --left-right --count origin/main...HEAD
node --version
npm --version
```

若执行工作树不干净，不得清理、覆盖、暂存或顺带提交用户现场。应从用户确认的 base 建立隔离 worktree，分支名建议：

```text
codex/frontend-error-discoverability-90-20260715
```

### 0.2 权威源码与生成物边界

- 当前 React/Vite UI 的唯一权威源码位于 `frontend-app`。
- `cmd/agent-terminal/web-dist` 是 `frontend-app` 构建后的单向同步生成物，不得手工编辑。
- Wails/backend bridge 错误必须 fail-fast 且可见，不得静默吞掉或返回默认成功外观。
- 不引入 Tailwind、shadcn 或新的 UI 框架。
- 不新增第二套 terminal、error、loading、thread 或 page cache 真相源。
- 本计划不重写整个 Zustand store，不吸收新的 Reasonix 能力，不顺带修改 Command Palette、A2A、MCP/LSP 产品语义或后端数据库。
- 生成的 codemap、project-map 和 embed 只允许通过规范生成器刷新；生成器未证明需要变化时不得手改。

### 0.2.1 治理基线必须先于被评分实现

本计划使用一层外部信任锚和三个 Git 身份，禁止继续用一个可由调用方任意填写的 `APPROVED_BASE_SHA` 或 opaque attestation id 同时代表这些事实：

```text
ATTESTATION_ROOT_ID      = G0/subject 均不可修改的外部签发 policy + resolver 身份
IMPLEMENTATION_BASE_SHA = 启动本计划前、尚无本计划治理资产的产品基线
GOVERNANCE_BASE_SHA     = 只落 rubric/scorer/schema/baseline/taxonomy/gate-lock 的独立获批提交
SUBJECT_SHA             = 从 GOVERNANCE_BASE_SHA 派生、接受评分的产品实现提交
```

**Task R0（外部前置，不是 repo commit）：** 在 G0 前必须先由组织级 release service 或 `IMPLEMENTATION_BASE_SHA` 之外、内容 digest 已在组织设置中固定的受保护 reusable workflow 提供 trust anchor。固定记录 `ATTESTATION_ROOT_ID/ROOT_POLICY_SHA256/ROOT_VERIFIER_SHA256`，并把外部 `rootSignerWorkflowDigest`、`trustedEvaluatorWorkflowDigest` 与 repo 内产品 workflow 的 `approvedProductWorkflowSetSha256` 分成三个不同字段；禁止用一个模糊的 `workflowDigest` 兼任三种身份。前两个 external digest必须在R0预先固定；product workflow set此时尚不存在，只能由G0提交模板、经独立review后由外部root attestation首次批准，之后subject只读，绝不能作为caller input或由R0假装预知。policy 至少锁定 issuer、repository、外部 signer/evaluator workflow path + content digest、protected ref/environment、OIDC/signing identity、public key或透明日志、resolver endpoint、key rotation/revocation，以及 governance-attestation schema version + digest。repo 中的 `governance-attestation.v1.schema.json` 只是可审阅镜像，必须与 root policy批准 digest逐字节一致；ROOT_VERIFIER使用自己的固定 parser/schema语义，不执行 G0 提供的解析器。G0 与 subject都不能修改外部 policy/workflow、读取裸签名凭据或把本地 JSON注册成 attestation；当前仓库若没有该前置，执行状态必须是 `BLOCKED`，不得在 G0内自举信任根。

Task R0 还必须在组织权限层建立唯一发布边界：所有 repo workflow 默认只有只读权限，不持有 release/package 写 token；唯一外部发布服务持有写权限，并且只接受 `ROOT_VERIFIER` 已验签的 composite full object。OIDC/GitHub App policy 必须绑定 repository、protected tag/ref、environment、exact `job_workflow_ref` + content digest、aggregator job和 artifact digest；第二条 workflow、REST client、secret或 endpoint 能直接发布时立即 `BLOCKED`。仓库静态 workflow 扫描只是附加证据，不能代替组织权限证明。

Task G0 必须先在隔离 governance lane 中完成并提交以下固定资产，之后由不参与该提交编写的 reviewer 审批，外部 root signer 才能签发 `GOVERNANCE_ATTESTATION_ID`：

```text
docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md
scripts/ai_maintenance/frontend_maintainability_rubric.v1.json
scripts/ai_maintenance/frontend_score_attestation.v1.schema.json
scripts/ai_maintenance/frontend_score.go
scripts/ai_maintenance/frontend_score_test.go
scripts/ai_maintenance/frontend_required_commands.v1.json
scripts/ai_maintenance/frontend_trusted_build_recipe.v1.json
scripts/ai_maintenance/frontend_untrusted_sandbox_policy.v1.json
scripts/ai_maintenance/frontend_governance_verifier.go
scripts/ai_maintenance/frontend_governance_verifier_test.go
scripts/ai_maintenance/frontend_trusted_probe.mjs
scripts/ai_maintenance/frontend_trusted_probe.test.mjs
scripts/ai_maintenance/frontend_trusted_manifest_writer.go
scripts/ai_maintenance/frontend_trusted_manifest_writer_test.go
frontend-app/config/coverage-baseline.v1.json
frontend-app/src/shared/errors/frontendFailureTaxonomy.v1.json
internal/dto/turn/schema/turn_ref.v1.json
internal/dto/turn/schema/turn_ref_exemptions.v1.json
internal/dto/turn/schema/turn_terminal.v2.json
internal/dto/shared/schema/public_error.v1.json
internal/dto/tool/schema/public_tool_result.v1.json
frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.schema.json
frontend-app/src/shared/errors/noncriticalSyncExemptions.v1.schema.json
frontend-app/src/shared/errors/frontendErrorContractPolicy.v1.json
scripts/contracts/frontend-governance-assets.v1.schema.json
scripts/contracts/frontend-governance-assets.v1.json
scripts/contracts/governance-attestation.v1.schema.json
scripts/contracts/evidence-bundle-manifest.v1.schema.json
scripts/contracts/frontend-build-only-manifest.v1.schema.json
scripts/contracts/frontend-command-evidence.v1.schema.json
scripts/contracts/frontend-release-attestation.v1.schema.json
scripts/contracts/frontend-smoke-manifest.v1.schema.json
scripts/contracts/frontend-assets.v1.schema.json
scripts/contracts/frontend-runtime-build-info.v1.schema.json
scripts/contracts/codex-artifact-provenance.v1.schema.json
scripts/contracts/lsp-bundle-provenance.v1.schema.json
scripts/codex_artifact_trust_policy.v1.json
scripts/lsp_bundle.lock.v1.json
.github/workflows/frontend-desktop-release-gates.yml
.github/workflows/frontend-desktop-release.yml
```

`scripts/contracts/frontend-governance-assets.v1.json` 是治理成员集合的唯一 SSOT，内容必须与下列 exact groups 逐路径一致；manifest 本身由 root attestation 直接锁 digest，故不把自身递归收入自身。除它之外，列表中的每个治理资产必须且只能属于一组：

```text
scoreGovernance = [
  docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md,
  scripts/ai_maintenance/frontend_maintainability_rubric.v1.json,
  scripts/ai_maintenance/frontend_score_attestation.v1.schema.json,
  scripts/ai_maintenance/frontend_score.go,
  scripts/ai_maintenance/frontend_score_test.go,
  frontend-app/config/coverage-baseline.v1.json,
  frontend-app/src/shared/errors/frontendFailureTaxonomy.v1.json
]
runtimeIdentityContract = [
  internal/dto/turn/schema/turn_ref.v1.json,
  internal/dto/turn/schema/turn_ref_exemptions.v1.json
]
frontendErrorContract = [
  internal/dto/turn/schema/turn_terminal.v2.json,
  internal/dto/shared/schema/public_error.v1.json,
  internal/dto/tool/schema/public_tool_result.v1.json,
  frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.schema.json,
  frontend-app/src/shared/errors/noncriticalSyncExemptions.v1.schema.json,
  frontend-app/src/shared/errors/frontendErrorContractPolicy.v1.json
]
trustedExecutionContract = [
  scripts/ai_maintenance/frontend_required_commands.v1.json,
  scripts/ai_maintenance/frontend_trusted_build_recipe.v1.json,
  scripts/ai_maintenance/frontend_untrusted_sandbox_policy.v1.json,
  scripts/ai_maintenance/frontend_governance_verifier.go,
  scripts/ai_maintenance/frontend_governance_verifier_test.go,
  scripts/ai_maintenance/frontend_trusted_probe.mjs,
  scripts/ai_maintenance/frontend_trusted_probe.test.mjs,
  scripts/ai_maintenance/frontend_trusted_manifest_writer.go,
  scripts/ai_maintenance/frontend_trusted_manifest_writer_test.go,
  .github/workflows/frontend-desktop-release-gates.yml,
  .github/workflows/frontend-desktop-release.yml
]
evidenceContract = [
  scripts/contracts/frontend-governance-assets.v1.schema.json,
  scripts/contracts/governance-attestation.v1.schema.json,
  scripts/contracts/evidence-bundle-manifest.v1.schema.json,
  scripts/contracts/frontend-build-only-manifest.v1.schema.json,
  scripts/contracts/frontend-command-evidence.v1.schema.json,
  scripts/contracts/frontend-release-attestation.v1.schema.json
]
packageContract = [
  scripts/contracts/frontend-smoke-manifest.v1.schema.json,
  scripts/contracts/frontend-assets.v1.schema.json,
  scripts/contracts/frontend-runtime-build-info.v1.schema.json
]
supplyChainPolicy = [
  scripts/contracts/codex-artifact-provenance.v1.schema.json,
  scripts/contracts/lsp-bundle-provenance.v1.schema.json,
  scripts/codex_artifact_trust_policy.v1.json,
  scripts/lsp_bundle.lock.v1.json
]
```

`GOVERNANCE_BASE_SHA` 必须以 `IMPLEMENTATION_BASE_SHA` 为父代或可审计后继，且 governance diff 不得夹带产品行为修复。Task 0-9 从该提交重新建 clean detached worktree；`SUBJECT_SHA` 必须是其后继，且不得修改上述治理资产。Task G0 还要从 governance commit 构建不可变 `frontend-governance-verifier`、governance-owned trusted probe bundle与唯一 canonical manifest writer，并冻结 trusted build recipe、完整 toolchain/dependency/image lock 和 untrusted sandbox policy。strict `governance-attestation.v1` 必须绑定 attestation id + content digest、root policy digest、implementation/governance commit + tree、governance asset-manifest digest及各 group digest、reviewer identity/approval record、各 OS/arch verifier/probe/writer digest + build provenance、build recipe/toolchain/dependency/image/sandbox-policy digest、verifier/probe/writer stdout/result/mutant bundle digest，外部 signer/evaluator workflow digest与批准的 repo product-workflow-set digest，以及 issuer/repository/ref/run/job/artifact/result。每个可信 evaluator只能选择与自身 OS/arch exact match 的 verifier、probe、writer与build recipe。

验证顺序不可倒置：先由 `ROOT_VERIFIER` 从受信 resolver 取得并验签完整 governance attestation object，输出 immutable verified-lock；再由 lock 中批准的 governance verifier检查 subject、scorer、frontend-error/trusted-execution/package/evidence schema、command/probe和 bundle。任何步骤都不能只检查 ID非空，也不能执行可能已被 subject修改的 dispatcher。`frontend:governance-baseline-lock` 对 subject与governance base做逐字节比较，并拒绝 base不存在、非祖先、短SHA、self-base、未批准/不可解析attestation、同ID换内容、错误external signer/evaluator/repo workflow identity/ref、旧base replay、verifier/probe/build/stdout/result/bundle mismatch、权重/rounding/gate/`requiredFor90`变化、coverage阈值降低/include缩小/exclude扩大/分母缩小、failure taxonomy缩减及 runtime/frontend-error/trusted-execution/package/evidence/supply-chain contract drift。

root attestation 先锁 `approvedGovernanceAssetsManifestSha256`，governance verifier 再严格按该 manifest 的 named exact sets，对排序 `{repoRelativePath,contentSha256}` canonical manifest 计算 `approvedScoreGovernanceSha256/approvedRuntimeIdentityContractSha256/approvedFrontendErrorContractSha256/approvedTrustedExecutionContractSha256/approvedEvidenceContractSha256/approvedPackageContractSha256/approvedSupplyChainPolicySha256`。删除、增加、移动、大小写冲突、跨组重分类、重复或未分类治理资产均 fail-fast；不得由实现者解释“本节对应文件组”。合法治理更新必须单独开启下一轮 governance lane、重新签发 attestation 并让产品实现重新基于新 base 执行全套证据；被评分提交不得审批自己的 rubric、错误 schema、required-command/probe、coverage baseline 或 failure-class 分母。不存在 root trust、asset manifest 或 baseline 时必须 BLOCKED，禁止从 `SUBJECT_SHA` bootstrap、回填或自动接受当前值。

product/repo OS lane 一律属于不受信 build producer：它可以编译、运行 subject tests并上传内容寻址 `BUILD_ONLY` artifact/raw bundle，但这些字节永远不可被提升、签名或发布，也无权产生可签发的 control PASS。root-controlled trusted evaluator必须从受信 repository按40位 `SUBJECT_SHA`取得并复算 `SUBJECT_TREE_SHA`，由隔离 trusted builder controller使用 verified lock中的 exact build recipe、OS image、toolchain、dependency与输入 digest从该只读 source tree重新构建最终候选；subject build脚本只作为无权访问信任面的 untrusted child执行。若采用可复现重建替代，至少两份隔离构建的规范化 artifact/executable字节必须 exact-match，repo artifact仍只能作缓存提示且不匹配即拒绝。signer只接受 trusted builder内部认证通道产出的 digest，runtime build-info只作附加一致性检查，不能证明源码到字节关系。

所有 subject command、build child 与被启动 executable都必须进入独立 microVM或等价强 sandbox：独立 user/mount/network/PID/IPC namespace，无 host loopback、host Unix socket、共享内存、设备、cloud metadata、OIDC/runner credential、继承 fd或外部 egress；drop全部 capabilities，启用 `no_new_privs` 与平台等价的 seccomp/AppContainer/sandbox profile，禁止 ptrace。只允许 verified sandbox policy预声明的只读内容寻址缓存和单向认证 broker/vsock；app/child看不到 verifier、lock、command record、result、signer或publish channel，result broker使用不同主体且只接收绑定 nonce/digest的输出。trusted evaluator只读挂载 approved verifier/probe/writer，独立复算 canonical bundle/package/executable/runtime identity并运行唯一 scorer。替换 builder/recipe/toolchain/image/verifier/lock/probe/writer、伪造 subject runner、repo产物晋升、构建后改机器码、同权限 TOCTOU、localhost/Unix socket/metadata/OIDC/ptrace/继承fd逃逸、预造 result与symlink逃逸 mutants必须在计分前失败。

### 0.3 可机械复算的评分协议

写作快照的 `61.8` 来自此前人工评估，只用于说明为什么启动本计划。Task G0 必须先在独立 governance lane 建立以下单一评分真值源并重新生成 baseline；若复算结果不是 `61.8`，保留新结果和差异原因，不允许调分去追平旧数字：

```text
scripts/ai_maintenance/frontend_maintainability_rubric.v1.json
scripts/ai_maintenance/frontend_score_attestation.v1.schema.json
scripts/ai_maintenance/frontend_score.go
scripts/ai_maintenance/frontend_score_test.go
```

`frontend_maintainability_rubric.v1.json` 是 governance lane 中唯一可写 rubric。本文控制项表在 Task G0 后只能由 scorer 的 `render-rubric` 模式生成并通过 drift guard 校验，禁止同时手改 JSON 与 Markdown。进入 product lane 后两者都只读，并必须与 `approvedRubricSha256` 逐字节一致。rubric 顶层必须严格包含 `schemaVersion/rubricVersion/dimensions/gates/rounding`，每个 control 必须含 `requiredFor90`；未知字段、重复 control id、维度点数不等于 `100` tenths、权重和不等于 `100`、一个证据声明多个主要计分维度或 rubric 版本不匹配均 fail-fast。

每个 control 只有 `PASS / FAIL / NOT_VERIFIED / BLOCKED` 四态。只有同一 `SUBJECT_SHA` 上全部 required evidence 和 gate 均为 PASS 才获得完整 `pointsTenths`；其余状态一律获得 0 分且保留原状态，不给主观部分分。计分全程使用整数：

```text
dimensionTenths = Σ(PASS control.pointsTenths)，每维范围 0..100
rawBasisPoints = Σ(dimensionTenths × dimension.weight)，范围 0..10000
effectiveBasisPoints = G2_BLOCKED ? min(rawBasisPoints, 5990) : rawBasisPoints
门槛比较使用未取整的 basis points；仅展示时 half-up 到 1 位小数
```

原子控制项固定如下；每行 evidence key 必须在 attestation schema 中是一热、可定位且只有一个主要计分维度：

<!-- BEGIN GENERATED FRONTEND MAINTAINABILITY RUBRIC -->

| 维度/权重 | Control | pointsTenths | requiredFor90 | 必须同时满足的 evidence key |
|---|---|---:|---|---|
| 错误 35 | `E01-terminal-truth` | 20 | yes | strict raw decode、canonical outcome、互斥终态 parity |
| 错误 35 | `E02-visible-user-error` | 20 | yes | 全部 user action 的 visible DOM failure proof |
| 错误 35 | `E03-background-health` | 15 | yes | background/subscription/teardown persistent health proof |
| 错误 35 | `E04-action-matrix` | 15 | yes | `actionId×failureClass×sink` 全单元行为与 production-form proof |
| 错误 35 | `E05-desktop-failure` | 15 | yes | instrumented production-shape Wails failure matrix |
| 错误 35 | `E06-safe-recovery` | 15 | yes | safe error envelope、turn correlation、capability-backed recovery |
| 架构 20 | `A01-outcome-ssot` | 25 | yes | terminal schema 单一 owner 与 Go/JS 生成 drift guard |
| 架构 20 | `A02-turn-ownership` | 20 | yes | `(threadId,turnId,generation)` ledger 与 late-event isolation |
| 架构 20 | `A03-state-ssot` | 15 | score-dependent | page/store/cache owner 与窄 selector 证据 |
| 架构 20 | `A04-dependency-direction` | 15 | yes | modules graph/archtest 与 facade 边界 |
| 架构 20 | `A05-action-inventory-ssot` | 15 | yes | 动态 `U`、manifest、exemption missing/stale parity |
| 架构 20 | `A06-generated-boundary` | 10 | yes | frontend→dist→embed 单向生成与 drift guard |
| 契约 15 | `C01-presence-aware-wire` | 20 | yes | raw presence/type/unknown enum 一热反例 |
| 契约 15 | `C02-terminal-field-guard` | 20 | yes | TurnRef/terminal schema→Go/JS→全部 consumers 动态字段守卫 |
| 契约 15 | `C03-error-action-schema` | 20 | yes | governance safe-error/public-tool/visibility/action schema 与外部消费证明 |
| 契约 15 | `C04-critical-typecheck` | 15 | yes | `--listFiles` 覆盖且 strict/checkJs 生效 |
| 契约 15 | `C05-provider-rpc-contract` | 15 | yes | provider/eventsurface/RPC 合法与非法矩阵 |
| 契约 15 | `C06-package-schema` | 10 | yes | package/runtime-build-info strict schema、writer/reader field parity |
| 测试发布 20 | `T01-red-green-mutant` | 20 | yes | 每个 blocker deterministic RED、GREEN、mutant |
| 测试发布 20 | `T02-critical-cell-coverage` | 20 | yes | 错误矩阵全部 guard-derived cells 100% |
| 测试发布 20 | `T03-full-diff-routing` | 15 | yes | approved-base full diff 与 gate plan exact parity |
| 测试发布 20 | `T04-source-embed-smoke` | 15 | yes | dev、instrumented host、真实 embed 三类边界不冒充 |
| 测试发布 20 | `T05-package-provenance` | 15 | yes | trusted probe证明每个artifact/executable与实际runtime embed.FS/VCS identity一致 |
| 测试发布 20 | `T06-three-platform-release` | 15 | yes | 三平台trusted evaluator、root composite、唯一外部publish授权 |
| 性能 10 | `P01-narrow-selectors` | 25 | yes | streaming 对无关页面 render 不回归 |
| 性能 10 | `P02-profiler-ratchet` | 20 | yes | 同环境 React Profiler 预锁阈值 |
| 性能 10 | `P03-history-benchmark` | 20 | yes | 200/1000/5000 turns 可复现基准 |
| 性能 10 | `P04-feedback-budget` | 20 | yes | pre-commit/pre-push/全量测试反馈预算 |
| 性能 10 | `P05-resource-budget` | 15 | score-dependent | bundle/chunk/heap/scan 上限与不放宽守卫 |

<!-- END GENERATED FRONTEND MAINTAINABILITY RUBRIC -->

rubric v1 的固定 `requiredFor90=yes` profile 全部 PASS 时得到维度 `10.0/8.5/10.0/10.0/8.5`，即 `9550 basis points = 95.5`；这是离散、保守控制项下可复算的最小完成 profile，不得人为削分到恰好 90。`A03/P05` 可以继续把分数提升到 100；它们非 PASS 时必须按 P2/P3 残留记录，且 scorer 仍要满足维度/Raw 门槛。scorer fixture 还必须锁定：重复/未知 control、证据主要维度重复、`NOT_VERIFIED` 计 0、required control 非 PASS 阻断、G2=5990、8999 不达标、9000 达标、只在展示层取整。最终报告不得接受 reviewer 手填维度分。

### 0.4 写作快照中的已验证事实

已验证正向事实：

- `npm run lint` 通过。
- `npm test` 为 157 个测试文件、2322 个测试通过。
- `npm run build` 与 `make frontend-embed-verify` 通过。
- RPC audit 为 140/140，已审范围内 handler、validator、payload drift 缺口为 0。
- UI Test MCP acceptance 与 navigation scenario 通过，但它们没有覆盖 provider 失败、partial output、断连或 Prompt History RPC 拒绝。
- 根级 Error Boundary、bootstrap 可见错误、重试/reload、全局 crash report 和隐私清洗已有正向实现与测试。
- 当前 code-size guard 的 production freeze 为 0，不得通过新增 baseline 消化后续复杂度。

### 0.5 两条已确认生产阻断

#### Blocker A：失败 turn 被展示为成功

当前链路：

```text
provider turn/failed
  -> TurnCompleted{success:false, error, status, reason, result}
  -> event surface 发布 turn/completed
  -> clientStoreBridgeRuntime 不检查 payload.success
  -> runtimeAssistantCompletion 从 partial result 创建 done assistant item
  -> applyAssistantCompletion 无条件设置“已收到回复 / success”
```

证据落点：

- `internal/dto/turn/event.go`
- `internal/provider/codexapp/factory.go`
- `internal/provider/codexapp/session_approval.go`
- `internal/provider/codexapp/event_map.go`
- `internal/provider/codexapp/turn_completed_event_map_test.go`
- `internal/platform/eventsurface/bind.go`
- `frontend-app/src/entities/client/model/helpers/a1/clientStoreBridgeRuntime.js`
- `frontend-app/src/entities/client/model/runtimeAssistantTimeline.js`
- `frontend-app/src/entities/client/model/helpers/assistantEventRuntime.js`
- `frontend-app/src/pages/chat/ChatPage.jsx`

`success:false + error + partial result` 会形成正常完成消息和 success notice；没有 `result` 时则不会形成成功消息，但 `error` 仍未进入可见错误面。两种情况都违反终态真实性。

#### Blocker B：Prompt History 失败只进入 console

当前链路：

```text
当前草稿位于历史最新位置时按 ArrowUp
  -> promptHistory.previous
  -> RPC / stale retry / response contract 失败继续抛出
  -> runUIAction 无 onError 调用
  -> 默认 console.error 并消费 rejection
  -> 用户只看到草稿未变化
```

证据落点：

- `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- `frontend-app/src/features/prompt-history/hooks/usePromptHistory.js`
- `frontend-app/src/features/prompt-history/model/promptHistoryController.js`
- `frontend-app/src/shared/ui/runUIAction.js`

现有 Prompt History controller/hook 测试证明 Promise 会 reject，但没有 Composer/Chat 层可见错误、恢复动作或 `role=alert` 断言。

### 0.6 现有门禁盲区

- `frontend-app/scripts/no-silent-async-failure.mjs` 只扫描 `src`，只拒绝空 catch 或显式丢弃 error；console-only、返回 `{}`/`[]`、默认值和成功外观不属于现有违规类型。
- `runUIAction` 的 `onError` 可选，因此“工具函数能接收错误回调”不等于“调用方必须提供错误出口”。
- `frontend-app/public/wails/runtime.js` 是 Vite 本地开发 shim，不是 production Wails runtime；其中 build-info RPC 失败返回 `{version:'dev'}`，随后可能显示“构建信息已刷新”。该问题属于开发态 parity 与门禁盲区，不得误报为生产 bridge 缺陷。
- `tsconfig.contracts.json` 使用 `checkJs:false`、`strict:false`、`noImplicitAny:false`，并包含失效的 `src/entities/client/model/providerPreferences.js` 路径；当前 typecheck PASS 不能作为有效关键契约类型覆盖证据。
- 写作期间取得的局部 LSP 结果不能替代执行基线；Task 0 必须在 candidate `SUBJECT_SHA` 上重跑完整目标范围 diagnostics。仓库规定 Hint/Information 也必须处理，既不能把 targeted files 的局部 clean 写成全范围 PASS，也不能把已经消失的旧诊断继续列为当前债务。
- 本次文档返修在写作 HEAD 上重新取得 `internal/provider/codexapp/factory.go:99` 的 `Information/unusedfunc`（`decodeJSONMap` 未使用）；Task 0 必须在 subject SHA 复现，若仍存在则由 Task 1 删除或接入真实调用并补测试。它未清零前 final-90 diagnostics gate 为 FAIL，不能因 severity 不是 Error 而忽略。
- desktop RPC/UX smoke 仍是手工入口，没有进入当前 pre-commit/pre-push 的强制前端链。
- 没有 coverage threshold、变更覆盖率或关键错误 mutant 门禁。

---

## 1. 90 分目标、阶段出口与硬门禁

### 1.1 阶段出口是控制项门槛，不是预填分数

实施期间不得更换 rubric/权重、抬高证据上限或用其他维度高分抵消错误不可见。阶段出口由 scorer 读取 attestation 后决定，本文不再给第一波、第二波预填 `75.4/84.6`：

| 阶段 | 必须 PASS 的新增 controls | 阶段判定 |
|---|---|---|
| Task G0 governance | 独立 governance commit、rubric/scorer/baseline/taxonomy、外部批准 digest | 输出不可由 product subject 修改的机械 baseline；与历史 `61.8` 的差异必须解释 |
| Task 0 baseline | governance lock、现状 RED、LSP/浏览器/工具链 preflight | 证明执行环境和 candidate 均绑定获批 governance base |
| 第一波 | `E01/E02/E05/E06/A01/A02/C01/C02/C03/T01/T04` | G2 解除所需控制项均 PASS；scorer 输出实际 Raw/Effective |
| 第二波 | 再加 `E03/E04/A05/C04/C05/T02/T03` | FE-ERR-0..4 PASS；scorer 输出实际 Raw/Effective |
| 第三波 | 全部 `requiredFor90=yes` controls；`A03/P05` 是明确的 score-dependent uplift | `rawBasisPoints>=9000`、错误维度 `>=90` tenths、其他维度 `>=85` tenths |

任一 required control 为 `FAIL/NOT_VERIFIED/BLOCKED` 即不得跨越对应阶段；不沿用旧分，也不允许 reviewer 通过口头判断补分。

### 1.2 90 分不可补偿门槛

必须同时满足：

- `rawBasisPoints >= 9000`，展示值才是 Raw `>= 90.0`。
- 错误可发现性 `>= 9.0`，其他任何维度 `>= 8.5`。
- 已知 P0/P1 为 0，G2 已解除。
- clean `SUBJECT_SHA` 上通过 release gate，且外部 composite attestation 同时绑定 governance base、同一 subject 和三个 lane bundle。
- 关键用户动作错误矩阵覆盖率为 100%。
- production JS/JSX 及本次修改的 Go/脚本文件，LSP `Error / Warning / Information / Hint` 全部为 0。
- source、dist、Go embed 和真实桌面行为一致。

### 1.3 前端错误硬门禁

| Gate | 不变量 | 失败判定 | 解除证据 |
|---|---|---|---|
| FE-ERR-0 终态真实性 | canonical `outcome!=success` 永远不能产生 success notice；legacy malformed tuple 在 adapter fail-fast | failed/interrupted/cancelled/invalid 进入成功 UI | producer adapter、v2 outcome parity、fail-first 回归、production-shape 失败注入 |
| FE-ERR-1 用户可发现 | 关键用户动作失败必须有可见原因、上下文和 capability-backed recovery 或安全下一步 | console-only、草稿静默不变、通用“失败了”无上下文 | DOM 断言、错误原因、action identity、recovery/safe-next-step 测试 |
| FE-ERR-2 后台健康 | 后台/订阅错误至少进入持久 health/diagnostics | 只写 console/log、teardown 失败被忽略 | health 状态、重连或受控降级、sanitized trace 测试 |
| FE-ERR-3 严格边界 | 非法响应不得返回 `{}`、`[]` 或默认成功值 | fallback 被渲染成成功或正常空态 | 负载反例、typed failure、可见 degraded/error 状态 |
| FE-ERR-4 交付证明 | mock 单测不能替代 Wails/provider 失败路径 | 无 instrumented production-shape Wails 失败注入，或用它冒充 release artifact | failure-harness smoke、真实 embed smoke、无注入的 packaged identity/provenance smoke 三类独立证据 |

FE-ERR-0 失败继续触发 G2，Effective 最高为 59.9。统一产品评分没有规定的其他数值封顶不得由本文自行创造，但任一硬门禁 `NOT VERIFIED` 时禁止进入 90 分区间。

---

## 2. 错误可发现性契约

### 2.1 终态协议固定为 v2：请求事件与终态事件分离

本计划不再把 `TurnInterrupted` 是请求还是终态留给实施者决定。Task 1 必须原子迁移到以下协议；迁移完成前 G2 保持阻断：

```text
turn/interrupt_requested + TurnInterruptRequested -> 非终态 interrupt_requested
turn/stalled + TurnStalled                         -> 非终态 degraded/health
turn/completed + TurnCompletedV2.outcome           -> 唯一终态 success|failed|interrupted|cancelled
invalid                                             -> contract error；不写入成功/正常空态
```

唯一可写定义位于 G0 governance lane 的 `internal/dto/turn/schema/turn_terminal.v2.json`，product subject只读。`scripts/generate_turn_terminal_contract.go` 从该 schema 生成 `internal/dto/turn/terminal_contract_generated.go` 和 `frontend-app/src/shared/contracts/turnTerminal.generated.js`；生成 guard与approved verifier独立parser必须共同拒绝 schema/Go/JS drift。Go timeline、uistate、observation、notifier、cron/orchestration、hooks、bus、memory、insight、trajectory 和前端只消费生成的 `outcome`，不得再从 `success/status/reason/method` 各自推导。`internal/module/uistate/terminalstatus.Status`、provider 私有 terminal resolver 和拟新增 frontend mapper 中的平行判定必须删除或收缩为调用同一生成 validator；provider 特有 raw adapter 可以不同，但 adapter 输出后不得保留第二个终态真值字段。

旧 `turndto.TurnInterrupted`、`EventTypeTurnInterrupted`、canonical bus/eventsurface 的 `MethodTurnInterrupted` 必须从内部权威事件面删除；provider/remote 原始字符串 `turn:interrupted` 只允许存在于 legacy adapter 输入并立即归一为 `TurnCompletedV2{outcome:interrupted}`。全部原消费者分别迁移到 `TurnInterruptRequested` 或 `TurnCompletedV2`，不得保留双订阅兼容期后仍同时写状态。

canonical 状态固定如下：

| Event | 必填事实 | Canonical 结果 | UI/状态约束 |
|---|---|---|---|
| `turn/interrupt_requested` | `TurnRef`、agent identity、request id、timestamp | `interrupt_requested` | 不封口、不提示完成/失败/取消 |
| `turn/stalled` | `TurnRef`、typed stall reason、deadline | `degraded` | 只写 health；可恢复，不做 completion/drain/flush/cleanup |
| `turn/completed` | identity、`outcome=success` | `success` | 唯一允许 success notice 的组合 |
| `turn/completed` | identity、`outcome=failed`、safe error envelope | `failed` | partial 可保留但必须标失败 |
| `turn/completed` | identity、`outcome=interrupted` | `interrupted` | 终态；不得继续等待另一个 completed |
| `turn/completed` | identity、`outcome=cancelled`、typed cancellation evidence | `cancelled` | 仅真实用户取消或合法 supersede |
| 任一事件 | 缺字段、错类型、未知 outcome、method/schema 不匹配 | `invalid` | fail-fast 并进入 contract diagnostics |

`TurnRef = {threadId,turnId,generation}` 是全部 turn-scoped 事件的必填关联键，不只属于 terminal。唯一 schema 是 governance-owned `internal/dto/turn/schema/turn_ref.v1.json`；`turnId` 是本地 ledger 在接受用户 turn 时分配、跨 provider restart/replay 保持不变的逻辑 ID，provider remote turn id 只能作为可轮换 alias，绝不能反客为主；`generation` 是 canonical registry 在每次新逻辑 turn 分配的非空 128-bit instance nonce（32 位 lowercase hex），不是调用方计数器或可缺省零值。同一 turn contract generator 派生 reusable Go/JS 类型，所有 turn-scoped DTO/schema 必须 `$ref`/嵌入它并贯穿 assistant/thinking delta、plan、item started/completed、command output、approval/tool result、interrupt、stall 和 terminal。

单一生命周期 owner 固定为 `internal/module/turn/refregistry`，API 语义为 `Start(providerSessionEpoch,threadId,providerTurnAlias)->TurnRef`、`Resolve(providerSessionEpoch,threadId,providerTurnAlias)->TurnRef`、`PrepareAliasRebind(TurnRef,oldEpoch,recoveryHandle)->RebindToken`、`CommitAliasRebind(RebindToken,newEpoch,newAlias)`、`Close(TurnRef,outcome)`：Start 拒绝 live duplicate；Resolve 只能命中当前 alias 所属 active/closed identity；Close 原子写入 bounded tombstone。`RebindToken` 是 registry-private、不可序列化、single-use token，绑定原 TurnRef、旧epoch/alias、Start时保存的本地request identity、recovery handle digest、目标provider instance和deadline；caller JSON或仅同thread/同prompt不能构成 continuity proof。合法 provider restart/replay只能在旧 session已终止、目标 TurnRef仍active、token全字段匹配、new alias未被占用时，在registry锁内CAS rebind并先消费token；成功后旧alias立即进入tombstone，new alias继续指向原TurnRef，不生成第二个generation。无法证明 continuity、token过期/重放或目标冲突时必须先由 terminal ledger exactly-once 封口旧 TurnRef为 `failed/code=PROVIDER_SESSION_RESTARTED`，再为真正的新逻辑turn执行Start；禁止让旧active永远pending或静默拆成两个identity。closed identity至少保留 generated `TurnRefClosedRetention=10m`，之后的事件只允许 `UNKNOWN_CLOSED_TURN` fail-fast diagnostic，绝不能重绑到当前turn；进程重启必须产生新provider session epoch，未被显式rebind的旧session event直接拒绝。tombstone eviction/容量有性能测试，但容量压力只能使过期事件失败，不能回退为thread-wide/current-turn查找。

`turn_ref_exemptions.v1.json` 只允许列出真正的 agent/thread lifecycle 事件，逐项含 event type、owner、reason 与行为测试；turn/item/tool/approval/command payload 不得豁免。动态 guard 从 Go DTO embeds/JSON tags、provider mappings、provider-alias registry、eventsurface serializers、JS validators/imports 和 consumer refs 生成 turn-scoped 候选集，与 schema/exemptions做 missing/stale exact diff。所有 ledger、buffer 和 open-item key 至少使用 `(threadId,turnId,generation,itemId?)`，缺键、跨 generation、把 remote provider alias当 canonical turnId，或只按 thread 选择“最后一个 open command”一律 contract error。若未来能机械证明 `turnId` 全局唯一，只能另开 governance 变更删除 generation；本计划内不得一边可选 generation 一边依赖它隔离 T1/T2。

provider/remote 的旧 wire 只能在入口处经过 presence-aware legacy adapter；adapter 使用 `json.RawMessage`、指针字段或等价 decoder 在写入 typed DTO 前区分 missing/type mismatch，禁止先解码为零值 `bool`：

| Legacy raw tuple | 唯一允许的 v2 outcome |
|---|---|
| `success:true` 且 status 为 `""|success|completed` | `success` |
| `success:false` 且 status 为 `""|failed|error|aborted`，同时存在 safe error envelope | `failed` |
| provider 原生 terminal interrupted method | `interrupted` |
| `success:false + status:cancelled` 且存在 typed user-cancel/supersede evidence | `cancelled` |
| 缺失 `success`、非布尔 `success`、`success:true+negative status`、未知 status、失败但无 safe error | `invalid` |

legacy `success/status/reason` 只能存在于 raw adapter 输入，不能继续序列化到 v2 DTO。新 v2 wire 只允许 `outcome`，出现 legacy 字段或未知字段即失败。由此消除原表中 `success:false` 同时命中 failed/interrupted/cancelled 的重叠。

中断活性合同由共享 `internal/module/turn/terminalledger` 持有，desktop 与 orchestration 都必须调用它，禁止把 timeout owner 只放在 `cmd/mcp-orch`。provider interrupt 接口必须采用原子两阶段协议：`PrepareInterrupt` 在 provider 锁内验证 active handle并建立 reservation，返回 `InterruptLease{Acceptance:{TurnRef,RequestID,AcceptedAt,Deadline},CommitToken}` 且不产生中断副作用。`CommitToken` 是 provider-private、不可序列化、单次、不可重放的 opaque token，内部绑定 provider instance、session epoch、精确 TurnRef、active handle identity、request id、deadline 和随机 nonce。ledger compare-and-set只能在 TurnRef仍active且尚无terminal时登记 acceptance并发布 exactly-once request event；Prepare与该CAS之间若自然terminal先到，reservation callback必须暂存它或CAS返回stale并原子废止token，绝不能在terminal之后补发request。只有 acceptance成功后 `CommitInterrupt(token)` 才允许触发 provider中断。

Commit 必须在同一 provider 锁内 CAS 重验 token 未用/未过期且当前 active handle仍与 token 全字段一致，然后先消费 token再执行副作用；严禁忽略 token后重新读取“当前 active turn”。T1 已自然终结、T2 已启动、late/expired/replayed/cross-session token 或 Abort 后 token必须返回 typed `STALE_INTERRUPT_ACCEPTANCE` no-op，且对 provider、event、T2 和已存在的 T1 terminal 均为零副作用；不能再生成第二个 failed terminal。真正执行后，`CommitInterrupt` 只能返回 typed `CommitResult=NOT_APPLIED|APPLIED|INDETERMINATE`：只有 provider 明确证明副作用未发生时才是 `NOT_APPLIED`；已确认受理为 `APPLIED`；RPC timeout、ACK丢失、transport断连等“可能已应用”一律为 `INDETERMINATE`，禁止用普通 error冒充未应用。若 provider 无法天然分两阶段，必须用等价的 provider-private reservation + acceptance callback/事件暂存器满足同一不变量，不能继续只返回 `error`。

只有 `APPLIED/INDETERMINATE` 才在可注入 fake clock 的 `5s` finalization deadline 内等待 exactly-one `TurnCompletedV2`；该值由 terminal schema policy 生成唯一 `InterruptFinalizationDeadline`，provider 不得各写 magic timeout。Claude 当前 `handle.finish(context.Canceled)` 后必须直接形成 `outcome=interrupted`；其他 provider 同样不得只发请求事件。prepare 拒绝或无 active turn返回 typed `NO_ACTIVE_TURN` 且不发 request。`NOT_APPLIED` 只结束本次 interrupt attempt：ledger撤销该 acceptance deadline并产生绑定精确 TurnRef/requestId的非终态 `InterruptAttemptFailed{code=INTERRUPT_NOT_APPLIED}` health/action failure，原 turn保持 active，允许用新 requestId重新 Prepare/Commit或等待其自然 success/failed；旧 token/request绝不能盲重放。若产品政策要求立即终止，必须另有 `FenceAndFailTurn` 在同一原子边界停止 provider handle、关闭该 TurnRef事件入口并清理资源，成功后才能写 failed terminal，不能用 `NOT_APPLIED` 代替。`APPLIED` 等待 provider权威 final；`INDETERMINATE` 保持非终态 `interrupt_pending/health`，只允许用同 request id做幂等查询/协调。terminal 先于 RPC return时先暂存并在 request event后按序提交；`APPLIED/INDETERMINATE` 在 deadline内没有权威终态时才形成唯一 `outcome=failed/code=INTERRUPT_FINALIZATION_TIMEOUT`。已应用但 ACK丢失后到达的 `interrupted` 必须胜出且只终结一次；deadline后的晚到或其他冲突终态只产生 sanitized diagnostic，不得翻转 UI。

`TurnStalled`、`turn/stopped`、`agent/stopped`、`thread/stopped` 只表示健康或生命周期同步，不能生成 turn outcome。`TurnStalled` 恢复时清除 degraded health；只有 ledger 的 typed stall deadline 到期才能形成 `TurnCompletedV2{outcome:failed,code:TURN_STALL_TIMEOUT}`。`agent/failed` 必须先让 active turn 经同一 ledger 形成 `outcome=failed`，再处理 agent 生命周期。

消费者领域语义也属于 schema contract，不能靠机械改类型名决定：

| Canonical event/outcome | hooks | memory | insight/trajectory | UI/observation |
|---|---|---|---|---|
| request/stalled | 不发 `TurnAfter/TurnFailed` | 不抽取、不收尾 | 只记录非终态状态，不 flush | request/degraded，可恢复 |
| `success` | `TopicTurnAfter` | 允许成功内容抽取 | 收尾并记录 success | success |
| `failed/interrupted/cancelled` | `TopicTurnFailed`，payload 保留明确 outcome | 不把失败内容当成功记忆 | 收尾并记录原 outcome | 对应互斥终态 |

动态 consumer guard 必须从 generated type references、bus subscriptions、hook mappings、imports 和 serializer/validator callsites 反向生成候选集合，与 registry 做 missing/stale exact diff；不能只证明一个已登记 consumer 会读字段。每个表格单元必须有 provider adapter、eventsurface、hook/bus、memory/insight/trajectory、Go consumer parity、frontend behavior 和 fake-clock liveness 测试。

### 2.2 批准的错误出口

每个 critical async action 必须显式选择至少一个出口：

1. **Visible user error：** 对发送、历史导航、保存、审批、文件选择等用户动作，显示真实原因、动作上下文，以及由明确 capability 支持的恢复动作或安全下一步。
2. **Persistent health/diagnostics：** 对后台更新、订阅回调、teardown、自动刷新等后台动作，写入持久 health/diagnostics，并提供重连、重新检查或可解释的 degraded 状态。
3. **Controlled escalation：** render/lifecycle 错误可继续交给 React Error Boundary；事件处理器和 Promise 错误只能升级到 `window error/unhandledrejection` 与全局 crash pipeline。全局 async crash pipeline 负责报告，不自动等于用户可见错误；关键用户动作升级前仍必须写入 visible 或 persistent sink。

`console.error`、`console.info`、自由格式日志、空 catch、`void error`、返回空对象/空数组或默认值都不能单独作为 error sink。Toast 只有包含真实原因和上下文，并提供 capability-backed recovery 或安全下一步时才算完整用户错误处理。

### 2.3 恢复能力必须有事实来源

`TurnCompletedV2` 的 outcome/public error 不承担 capability 真值，因此不得从 failed outcome、code 或 public message 猜测 retry/resume：

- 已有 thread/provider capability 明确允许时，才展示 retry、resume 或 reconnect。
- 没有可恢复能力证据时，展示安全下一步，例如新建 turn、复制已清洗诊断、重新打开线程或关闭提示。
- 若实现确实需要新增 `recoveryCapabilities` 等生产字段，必须加入 canonical schema，动态枚举 Go producer、event surface、frontend validator、全部消费者和字段守卫；不能只在前端补默认值，也不能让 public message 文本成为 capability owner。

### 2.4 隐私与可观测性

- 唯一 wire 错误 schema 固定为 `internal/dto/shared/schema/public_error.v1.json`，并由同一生成器派生 Go/JS 类型。允许字段只有 `code/category/publicMessage/safeDetails/traceId`；`publicMessage` 必须非空、限定长度且与稳定 code 对应，`safeDetails` 只接受 schema allowlist 的 bounded scalar。
- provider raw cause 中复制的 prompt/message/tool input/output、token、绝对路径、未清洗 stack、DOM 文本和任意 payload dump只能留在服务端 private cause，绝不能经 terminal/public-error/telemetry/error notice 进入 eventsurface、frontend store 或 DOM。该禁令只约束错误/private-cause 旁路，不禁止正常、已授权且通过独立 public tool contract 的工具生命周期 UI。private cause 与 public envelope 只通过 `traceId` 关联。
- 清洗 owner 位于 provider→canonical DTO/eventsurface 边界；frontend validator 必须拒绝旧式顶层 `error/message/reason/stack` 和未知字段，只渲染 `publicMessage`、稳定 code、action context 与安全下一步。不得依赖 UI 最后一刻正则清洗来保护 DOM。
- terminal schema 禁止携带 provider 自由文本 `result/summary/message/reason/stopReason`。失败时的 partial 只能使用 `partialOutputRefs[]` 引用同一 `TurnRef` 下已经过 canonical item validator、已经进入 timeline 的 assistant item；terminal 不得新增或复制内容。引用不存在、跨 turn/generation、指向 raw tool/command payload或包含 terminal 内联文本均 fail-fast。
- 正常工具展示的唯一 public schema 是 `internal/dto/tool/schema/public_tool_result.v1.json`，由同一 contract generator 派生 Go/JS 类型。它严格包含 call/tool identity、`TurnRef`、`visibility=user-authorized|diagnostic-only`、bounded `argumentsPreview/resultPreview`、opaque 或 repo-relative `persistedPathRef`、truncation/size/timing 与可选 `PublicError`；拒绝未知字段、绝对路径、raw stack和未授权 full payload。governance-owned `frontendErrorContractPolicy.v1.json` 必须生成 `visibility×sink` exact矩阵：`user-authorized` 才能进入普通 tool timeline/result/diff DOM及其对应 store，`diagnostic-only` 只能进入持久 diagnostics/受限 telemetry，严禁进入标准工具结果 DOM；每个 sink 的 allow/deny都由动态 consumer guard验证。provider/tool owner 按 capability/approval 和字段 allowlist在服务端清洗后才可发布；前端只能渲染 generated validator与矩阵共同接受的 view。现有正常 tool/diff lifecycle 语义必须保留，安全工具结果仍可见；terminal partial ref 永远不能借此指向 tool/command item。
- telemetry 与 visible UI 消费同一个 public envelope；telemetry sanitizer 仍作纵深防御，但不能成为 raw cause 可进入 frontend 的理由。
- 未知 provider cause 必须映射为明确稳定 code（例如 `PROVIDER_TERMINAL_FAILED`）和安全、可行动的 public message，不能暴露原文，也不能返回空原因或成功外观。
- capability absence 必须显式标记，不能伪造零值成功；合法取消必须由 v2 typed cancellation evidence 证明并与失败分开。
- privacy RED 必须分别向错误通道的 raw `error/result/summary/message/reason/stopReason` 注入 `Bearer`/`sk-*`、绝对路径、raw prompt/tool output、stack 与超长字符串，并同时断言 canonical wire、partial refs、store、DOM、warning entry、trace 全部不含原值且仍保留稳定 code/traceId；正向 fixture 同时证明 schema-valid、user-authorized 的安全 tool result/preview 仍通过 eventsurface/store/DOM 可见，避免用“隐私”误删产品能力。

### 2.5 Critical action manifest 的唯一来源

`100%` 必须同时证明“调用点全集已分类”和“critical 集合已覆盖”，不允许先手工挑选 manifest entries 再对自己计算满分：

```text
frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.schema.json
frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.json
frontend-app/src/shared/errors/frontendFailureTaxonomy.v1.json
frontend-app/src/shared/errors/noncriticalSyncExemptions.v1.schema.json
frontend-app/src/shared/errors/noncriticalSyncExemptions.v1.json
frontend-app/src/shared/errors/frontendErrorContractPolicy.v1.json
frontend-app/src/shared/errors/criticalAsyncActionManifest.test.js
```

manifest 每项 exact schema：

```text
id                 stable action id
kind               user | background
failureClasses[]   { id, requiredSinks[], recoveryPolicy }
productionProofs[] { failureClass, sink, caseId }
legalCancellation? { typedPredicateId, caseId }
```

- `kind=user` 的每个真实 failure class 必须包含 `visible`；`kind=background` 必须包含 `health`；`escalate` 只能补充，不能单独满足任一类。`none-for-legal-cancel` 只能出现在 `legalCancellation`，且 runner 必须观察到 canonical `outcome=cancelled` 与匹配的 typed predicate；它不属于错误 failure class，不能被拿来缩小错误分母。
- `failureClasses[]` 不是作者可自选分母。scanner 必须按每个 callsite/callee 从 typed RPC error union、action contract、generated response schema、typed local error/throw/reject sites 导出 producer failure set，并强制加入 governance taxonomy 中适用的 transport disconnect、timeout、malformed response 与 unexpected rejection 基类；无法解析的 dynamic throw/reject 使该 action BLOCKED。manifest 只映射导出类别到 sink/recovery，不得创造、删除或重命名类别。
- guard 对 producer failure set、governance taxonomy 与 manifest `failureClasses[]` 做 exact-set equality，再从 `requiredSinks[]` 动态生成全部 `(actionId,failureClass,sink)` cells，并要求 `productionProofs` 与 cells exact set equality。smoke result 必须回报同一 tuple 和观察到的 DOM/health sink；一个 case 只有在分别回报并断言多个 tuple 时才能覆盖多个 cell，不能仅凭复用 case id 获得满分。删除真实类别、删除 taxonomy 基类、加入 stale 类别或把未知 rejection 当 legal cancel 的 mutant 必须失败。
- 重复 id/cell、未知字段/sink/proof、空 failure class、kind×sink 非法组合、proof missing/stale、非法 recovery policy 或无法解析 schema 必须 fail-fast。
- Task 0 用 AST/type resolver 动态建立候选全集 `U`：production `runUIAction` callsites、Promise/thenable JSX event handlers、直接 Promise chain、`void` Promise invocation，以及 effect/subscription/teardown 异步入口的并集。稳定 identity 使用 repo-relative path、owner symbol、AST node kind 和 callee signature，不使用易漂移行号。
- scanner 每次在临时目录生成 inventory 并与 manifest/exemption 比较，结束后删除；inventory 不是 checked-in 第二真相源。`noncriticalSyncExemptions.v1.json` 每项必须含 stable identity、owner、非空 reason 和可执行 `evidenceTest`。新增、移动、删除、重导出、Promise 类型变化、missing/stale exemption 均必须失败；scanner 无法枚举或 type resolver 失败也必须失败，不能返回空 `U`。
- `U` 中每项必须且只能映射到一个 action id 或一个仍然有效的 `noncritical-sync` exemption；任何 Promise/thenable、后台 effect/subscription/teardown 都不得使用该 exemption。
- 分类覆盖率分母是 scanner 当次生成的全部 `U`；failure-class 分母是生产契约导出集合与获批 taxonomy 的并集；错误矩阵覆盖率分母是 guard 生成的全部 tuple cells。三者必须都是 100%，不接受人工 entries 数量、manifest 自报类别或手改分母。
- manifest 只描述错误治理契约，不保存 handler、store、router 或 backend client，不成为第二套动作注册表。

### 2.6 七条结构化字段链的动态守卫

不得再用“字段守卫闭环”一句话代替落点。以下 schema 是生产字段集合，guard 必须动态解析 schema/reflection/generated exports；同时从 generated type refs、imports、serializer/validator callsites、loader/wrapper/sink/runner 和 package writer/reader/caller 反向生成 consumer 候选集。消费 registry 只能登记真实 symbol，必须与候选集做 missing/stale exact diff，再用 AST、类型或一热 roundtrip 证明该 symbol 实际消费字段：

| Chain | Canonical producer | 必须证明的 consumers | Guard owner |
|---|---|---|---|
| turn identity | governance-owned `internal/dto/turn/schema/turn_ref.v1.json` + exemptions | generated Go/JS、refregistry、全部 turn-scoped DTO/provider adapter/eventsurface、buffer/item/tool/approval/terminal consumer | `internal/archtest/turn_ref_field_registry_test.go` + frontend guard |
| terminal v2 | `internal/dto/turn/schema/turn_terminal.v2.json` | generated Go、provider adapter output、eventsurface、hooks/bus、memory/insight/trajectory、uistate/observation/notifier/orchestration、generated JS、store/timeline/notice/diagnostics | `internal/archtest/turn_terminal_field_registry_test.go` + `frontend-app/scripts/turn-terminal-contract-guard.mjs` |
| public error | `internal/dto/shared/schema/public_error.v1.json` | provider sanitizer、eventsurface、frontend validator、DOM/health、warning/trace | `internal/archtest/public_error_field_registry_test.go` + frontend guard |
| public tool view | governance-owned `public_tool_result.v1.json` + error policy | tool sanitizer/capability、eventsurface、generated JS、visibility→store/sink、tool timeline/result/diff UI、diagnostics/telemetry、privacy tests | approved verifier + `internal/archtest/public_tool_result_field_registry_test.go` + frontend guard |
| critical action | governance-owned manifest/exemption schemas + error policy 与 subject动态 `U` | strict loader、wrapper/callsite、sink、failure runner/proof result | approved verifier独立重建 + `frontend-app/scripts/no-silent-async-failure.mjs` |
| package smoke | governance-owned `scripts/contracts/frontend-smoke-manifest.v1.schema.json` | shared writer、三平台 package caller、subject BUILD_ONLY runner、trusted probe、trusted lane/composite consumer | approved verifier/probe + `scripts/frontend_smoke_manifest_contract_test.go` + Node contract tests |
| runtime build info | governance-owned `scripts/contracts/frontend-runtime-build-info.v1.schema.json` | actual embed.FS/VCS calculator、typed Go producer、RPC/Wails serializer、generated JS validator/Settings UI、trusted probe、trusted lane/composite consumer | approved verifier/probe + `internal/archtest/frontend_runtime_build_info_field_registry_test.go` + Node contract tests |

每条链必须有五类 fail-first mutant：删除真实 producer 字段/枚举、删除 mapper 分支、增加未登记真实 consumer、添加 stale registry/proof、添加无理由 exemption/未知字段；失败输出必须给出 chain、field/cell、producer、consumer symbol。consumer discovery/parser 无法解析必须 fail-closed。恢复后同一命令 GREEN，并接入 pre-push/CI/release 对应 lane。只能证明登记而不能证明完整候选集合和实现消费时，控制项保持 `NOT_VERIFIED`。

---

## 3. 执行拓扑、共享 seam 与证据格式

### 3.1 串行所有权

Tasks 1-3 共享以下核心 seam，必须由同一时刻唯一 writer 串行修改：

- `frontend-app/src/entities/client/model/helpers/a1/clientStoreBridgeRuntime.js`
- `frontend-app/src/entities/client/model/runtimeAssistantTimeline.js`
- `frontend-app/src/entities/client/model/helpers/assistantEventRuntime.js`
- `frontend-app/src/shared/ui/runUIAction.js`
- `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- `frontend-app/scripts/no-silent-async-failure.mjs`

Task 5 和 Task 6 只有在文件所有权不交叉、base 相同且共享 seam 已冻结时才允许并行。最终集成、生成物同步、全量门禁和复评必须串行。

### 3.2 每个任务的证据记录

评分对象、治理规则与证据载体必须分离，固定使用以下身份协议：

```text
ATTESTATION_ROOT_ID / ROOT_POLICY_SHA256 / ROOT_VERIFIER_SHA256
IMPLEMENTATION_BASE_SHA / GOVERNANCE_BASE_SHA
GOVERNANCE_ATTESTATION_ID / GOVERNANCE_ATTESTATION_CONTENT_SHA256
SUBJECT_SHA              = 被测试的产品提交；所有源码、生成物和 packaged artifact 都必须归属它
LANE_ATTESTATION_IDS     = macOS/Linux/Windows 各自不可变外部记录
COMPOSITE_ATTESTATION_ID = 聚合 job 在复核 exact 三 lane 后唯一签发的最终记录
EVIDENCE_BUNDLE          = 位于强制 RUNNER_TEMP 下本次 run 随机私有目录/外部 artifact store，不写回 SUBJECT_SHA worktree
BUNDLE_MANIFEST_SHA256   = 对 canonical bundle-manifest.json 精确字节计算的 digest
```

执行时从 clean detached checkout 的 `SUBJECT_SHA` 开始，强制存在 `RUNNER_TEMP`，以 `umask 077` 和 `mktemp -d` 创建绑定 workflow run/job nonce 的全新私有根；禁止 `${SUBJECT_SHA}` 可预测目录、`mkdir -p` 复用、预置 symlink、realpath逃逸或已有输出文件。所有日志、截图、JUnit、manifest 副本和 scorer 输入写入该根；失败时保留只读失败证据或由 trap清理，但绝不能与后续 run复用。subject侧 `scripts/write_evidence_bundle_manifest.mjs` 与 `scripts/write_frontend_smoke_manifest.mjs` 只能生成 schema/name均不同的 `frontend-build-only-manifest.v1`、`build-only-bundle-manifest.json` 或 `build-only-package-manifest.json`，顶层强制 `proofClass=BUILD_ONLY`；它们可供本地诊断，但 approved verifier/scorer/composite必须拒绝，不能重命名或转换成 canonical manifest。

唯一 canonical evidence/package writer 是 G0 锁定并以独立 digest构建的 `frontend_trusted_manifest_writer`。evidence模式只能在全部 untrusted child退出、继承fd关闭且 command-result exact set复算完成后，由 trusted parent生成一次 `bundle-manifest.json`；package模式只能在 trusted build以及 codesign/notary/staple/archive/installer等全部字节变换完成后生成一次 final package manifest。writer使用dirfd/handle-relative no-follow、no-clobber和原子rename；manifest固定绑定 run/job nonce、approved command-plan/sandbox/build-recipe digest与完整result set，entries排除manifest自身，只允许regular file，拒绝symlink/重复/大小写折叠冲突/路径逃逸；path相对root、NFC、`/`分隔并按UTF-8 byte order排序，每项严格为 `path/size/sha256`。输出是固定key顺序的compact UTF-8 JSON加单个LF，`BUNDLE_MANIFEST_SHA256` 对该精确字节计算，不依赖tar/zip元数据；生成后目录与最终artifact必须封成只读，任何后续字节变化使lane失败。

`scripts/ai_maintenance/frontend_required_commands.v1.json` 是 required command ID/cwd/argv-template/expected-output 的唯一治理清单。`ROOT_VERIFIER` 验签 governance后必须把批准的 canonical plan bytes与sandbox/build recipe从 verified lock解析一次，写入root-owned content-addressed、只读 trust store并返回 `approvedCommandPlanSha256` 和不可替换handle/已打开fd；必须以 `O_NOFOLLOW`或平台等价机制验证regular-file identity/size/hash。`run-command-plan` 与 `finalize-evidence` 共用同一份已解析内存对象和digest，绝不重新打开 subject worktree相对路径或各自解析第二份plan。rename、symlink、inode交换、verify后替换、run/finalize异plan mutants必须失败。

`frontend-command-evidence.v1.schema.json` 定义每条 canonical record 的 `commandId/subjectSha/runId/jobId/approvedCommandPlanSha256/sandboxPolicySha256/cwd/argvDigest/environmentDigest/toolchainDigests/inputArtifactDigests[]/start/end/exit/stdoutSha256/stderrSha256/testCount?/artifactDigests[]`。approved verifier必须由root-controlled action以clean env和绝对、内容寻址路径启动，静态链接或验证dynamic-loader/runtime依赖；禁止从subject `PATH`、当前目录、`LD_PRELOAD/DYLD_*`、DLL search path、`NODE_OPTIONS/PYTHONPATH`等继承可执行语义。`run-command-plan` 直接以argv执行清单、按治理allowlist重建child env并使用批准toolchain绝对路径，不接受shell string或caller删选command ID。每个命令使用只读 content-addressed subject lowerdir与全新 COW overlay/ephemeral clone；upper/workdir只能位于该command私有输出根，治理plan exact声明允许写路径和 `consumes/produces` DAG，跨命令只能传递 trusted parent导出并复算的内容寻址产物，不能复用可污染worktree。每条命令结束后parent记录overlay exact diff、销毁overlay并输出单独no-clobber record；未声明写入、missing/duplicate/nonzero/超时/左侧pipeline失败/`cd`失败/输出hash不符立即停止且禁止生成PASS。subject child同时受第0.2节network/PID/IPC/credential/ptrace默认拒绝策略约束，不能看到或写verified lock、verifier、probe、writer、其他record或result channel。`finalize-evidence` 确认全部child退出且无继承句柄后调用唯一trusted writer；失败目录只能由 `seal-failed-run` 标记为不可复用诊断，永远不能进入PASS scorer。

外部 governance verifier 的 `evaluate --mode preview|release` 只接受 worktree外、由 approved trusted writer生成且schema-valid/digest匹配的 canonical bundle。product lane只能生成不具证明力的preview、`BUILD_ONLY` raw bundle和不可晋升的repo artifact；三个repo OS jobs只向root-controlled evaluator提交artifact IDs/digests作诊断或缓存提示，不能提交“已验证PASS”。外部 evaluator在隔离的macOS/Linux/Windows trust domain从已验证 source tree启动 trusted builder，使用批准recipe/toolchain/dependency/image/sandbox policy构建本lane最终artifact；macOS随后由外部signing service完成签名/notary/staple，其他平台也先完成archive/installer等全部变换，再由approved trusted writer生成canonical package/evidence manifests并由probe只读验证。只有这些trusted-build字节可进入lane attestation与signer；repo artifact即使probe通过也不可提升。随后同一外部trust domain exact验证 `lanes=macos/linux/windows` 且无重复、共同锁定governance/subject/tree/rubric/plan/recipe/sandbox digest，再以 `--mode release` 运行唯一scorer。repo aggregate只能发起evaluation并轮询opaque operation id；signer只接受evaluator内部result channel。preview/build lane不得自报release control PASS，publish先由ROOT_VERIFIER验签完整composite object。

composite 顶层必须绑定 root policy/content digest、受信 issuer、repository、`subjectSha/subjectTreeSha`、`approvedCommandPlanSha256/buildRecipeSha256/toolchainLockSha256/dependencyLockSha256/sandboxPolicySha256`、外部 signer/evaluator workflow digest、批准的 repo product-workflow-set digest、protected ref/tag、run id和aggregator job id；本地JSON、subject/G0自签记录或同ID不同内容不构成attestation。每个trusted evaluator lane必须记录 `os/arch/evaluatorImageSha256/trustedBuilderIdentity/sourceTreeSha256/buildRecipeSha256/toolchainLockSha256/dependencyLockSha256/sandboxPolicySha256/buildTranscriptSha256/governanceVerifierSha256/trustedProbeSha256/trustedManifestWriterSha256/workflowRunId/jobId/repoBuildOnlyArtifactId?/repoBuildOnlyDigest?/finalArtifactDigests[]/finalExecutableDigests[]/approvedCommandPlanSha256/bundleManifestSha256/packageManifestSha256/commandResultSetSha256/result`；所有治理digest必须命中该OS/arch批准项，final字节只能来自trusted builder内部通道。缺lane、交换OS、不同governance/subject/tree/rubric/plan/recipe/toolchain/dependency/sandbox、重放旧run、repo artifact晋升、build transcript与final字节断链、构建后机器码修改、inner manifest被改、artifact digest不一致、错误builder/verifier/probe/writer/issuer/workflow/ref或lane非PASS均由 `release:frontend-composite-attestation` fail-fast。最终声明只接受ROOT_VERIFIER + composite verify，不接受subject tree、repo lane/aggregate、subject writer/runner或上传系统未绑定canonical inner manifest的自报PASS。

若团队希望把摘要提交到 `docs/plans/evidence/frontend-maintainability-error-discoverability-90/`，它必须是 `SUBJECT_SHA` 之后的独立 docs commit，只引用外部 `COMPOSITE_ATTESTATION_ID`、三 lane IDs 与 bundle-manifest digests，不参与被引用 subject 的评分，也不得声称“证据文件和被测代码属于同一 SHA”。摘要文件不写自己的 commit SHA；其提交身份由 Git/CI 外部记录，避免自引用。

每份机器证据固定包含：

```text
STATE: TODO / RED / GREEN / NO_CHANGE / BLOCKED
ATTESTATION_ROOT_ID / ROOT_POLICY_SHA256 / ROOT_VERIFIER_SHA256
IMPLEMENTATION_BASE_SHA / GOVERNANCE_BASE_SHA
GOVERNANCE_ATTESTATION_ID / GOVERNANCE_ATTESTATION_CONTENT_SHA256
SUBJECT_SHA / SUBJECT_TREE_SHA
APPROVED_GOVERNANCE_ASSETS_MANIFEST_SHA256 / APPROVED_SCORE_GOVERNANCE_SHA256
APPROVED_RUBRIC_SHA256 / APPROVED_COVERAGE_BASELINE_SHA256 / APPROVED_FAILURE_TAXONOMY_SHA256
APPROVED_RUNTIME_IDENTITY_CONTRACT_SHA256 / APPROVED_FRONTEND_ERROR_CONTRACT_SHA256
APPROVED_TRUSTED_EXECUTION_CONTRACT_SHA256 / APPROVED_PROBE_BUNDLE_SHA256
APPROVED_EVIDENCE_CONTRACT_SHA256 / APPROVED_PACKAGE_CONTRACT_SHA256
APPROVED_SUPPLY_CHAIN_POLICY_SHA256 / APPROVED_VERIFIER_SHA256
RUN_ID / JOB_ID / COMMAND_RESULT_SET_SHA256 / TRUSTED_EVALUATOR_OPERATION_ID
WORKTREE_CLEAN_BEFORE / WORKTREE_CLEAN_AFTER / NON_TARGET_DIFF
RUBRIC_VERSION / CONTROL_IDS / PRIMARY_DIMENSION
INTENT / INVARIANT
OWNED_FILES / NON_TARGET_FILES
LSP_LOCATE / INSPECT / XREF / READ / DIAGNOSTICS
RED_COMMAND / EXPECTED_FAILURE / EXIT
IMPLEMENTATION_BOUNDARY
GREEN_COMMAND / TEST_COUNT / EXIT
GENERATED_IMPACT
ARTIFACT_PATHS / ARTIFACT_SHA256 / BUNDLE_MANIFEST_SHA256 / TRUSTED_LANE_ATTESTATION_ID
REMAINING_BLOCKERS
```

`frontend_score_attestation.v1.schema.json` 必须拒绝未知字段、缺 control、重复主要维度、root/external-workflow/repo-workflow-set/governance-attestation-content/subject/bundle/command-result/probe/rubric 不一致、非 clean subject、不同 SHA/run 的测试结果、repo lane自报 PASS和无 digest 文件。RED 必须证明目标坏行为，不能来自缺符号、语法错误、依赖缺失或测试脚手架失败。不得只保存最终 PASS；首次 deterministic RED、修复后 GREEN 和受控 mutant 反证都必须可复查。

---

## 4. 第一波：解除 G2，由 scorer 输出实际分数

### Task G0：独立评分治理提交

**STATE：** TODO

**Intent：** 在任何被评分产品修复前先验证外部 root trust，再建立第 0.2.1 节治理资产和唯一评分/分母真值，由独立 reviewer 审批并由 root signer签发；该提交本身不接受自己的 90 分评价。

步骤：

1. 用组织设置中固定 digest 的 `ROOT_VERIFIER` 解析 `ATTESTATION_ROOT_ID`，验证 root policy/workflow/issuer/ref/key/rotation；G0 不得写入或覆盖 verified root lock。从 `IMPLEMENTATION_BASE_SHA` 建独立 clean governance worktree，只修改第 0.2.1 节逐路径列出的治理文件；所有 verifier/probe/workflow contract tests也必须在该 exact list内，禁止以“必要测试”为由夹带未分组文件。
2. 实现 exact governance-assets manifest、rubric/schema/scorer、固定路径 coverage baseline、failure taxonomy、TurnRef + terminal/public-error/public-tool/critical-action contracts、error policy、required-command plan、command evidence、bundle/composite/governance/package/runtime-build-info schemas、两份 repo release workflow模板及 `frontend:governance-baseline-lock`；构建各目标 OS/arch 的单用途 `frontend-governance-verifier` 和 trusted probe bundle，保存可复查 build provenance。
3. verifier 必须用独立 strict parser/allowlist检查 terminal enum、public字段、visibility×sink、critical-action/exemption语义，并从 subject源码独立重建动态 `U`/consumer set；不得调用 product generator、guard或测试来决定合同是否合规。它的 `run-command-plan` 实施私有目录、no-follow/no-clobber、逐 command记录和 exact result set；trusted probe独立启动真实 executable并验证 runtime/embed/UI，不执行 subject-owned packaged runner作为裁决器。
4. 用该 verifier 的 `render-rubric` 生成本文控制项块；记录全部治理文件 SHA-256、asset/group digests、verifier/probe binaries/build stdout/result/mutant bundle digests、external signer/evaluator与repo workflow-set digests、tree SHA 与 clean 状态。禁止在同一 commit 放入 Blocker A/B 产品修复。
5. 两名独立 reviewer 复核成员集、分母、权重、required profile、baseline 来源、verifier/probe provenance、command exact set和 gate fail-first；G0 producer只能把审批原始记录与内容 digest提交给 root-controlled evaluator。ROOT_VERIFIER 取得并验签 strict governance attestation full object后才冻结 `GOVERNANCE_BASE_SHA`，不能只记录非空 ID。
6. mutants 至少覆盖任意 ID/本地 JSON/自签、错误 external signer/evaluator/repo workflow content digest/ref、旧 base replay、同 ID 换内容、替换 verifier/probe/lock/build/stdout/result/bundle、PATH/dynamic-loader/`NODE_OPTIONS`/DLL劫持、缺 reviewer，及删除/增加/移动/跨组 governance asset、schema+generator+guard+test协同弱化、required command缺失/中途失败/旧目录/symlink、weight/`requiredFor90`/rounding/G2/coverage/failure taxonomy/批准 digest drift；任一均使 product lane verifier失败。

停止条件：外部 root trust/完整 attestation object不可认证、G0 能修改 signer workflow/policy或接触签名凭据、治理资产夹带产品修复、baseline只能从未来 subject回填、外部批准不可得，或 reviewer仍有 P0/P1。

### Task 0：基线、现状 RED 与 LSP 影响面

**STATE：** TODO

**Intent：** 从已批准 `GOVERNANCE_BASE_SHA` 建隔离 product worktree，先验证 governance lock，再证明两条 blocker 可重复，并冻结文件所有权和验证工具。

步骤：

1. 记录 root policy/verifier、implementation/governance base、governance attestation ID + content digest、candidate subject、Git/Node/npm/依赖状态和 dirty fingerprint；ROOT_VERIFIER 先产出 verified governance lock，再从 governance base 建 clean detached worktree，证据输出目录必须位于 worktree 外。
2. 用 verified lock 指定的 governance verifier运行 `frontend:governance-baseline-lock`，复核 asset membership、rubric/coverage/taxonomy/runtime/frontend-error/trusted-execution/package/evidence/supply-chain digests、external signer/evaluator/repo workflow-set身份、祖先关系和 subject 未修改治理文件；复用 Task G0 scorer机械生成产品 baseline，历史 `61.8` 只作差异对照。
3. 通过 approved `run-command-plan --mode baseline` 运行当前前端 lint/test/build/embed，保存 exact command records、exit、测试数和产物 hash；build 后 subject worktree 若产生 tracked drift，baseline 立即 BLOCKED。直接手跑命令只能作诊断，不能进入评分 evidence。
4. 用当前对外 LSP 工具保存：`grep` 定位、`inspect` 理解、`xref` 影响面、`file(read_file)` 精读、`file(diagnostics)` 诊断。
5. 将 browser preflight/bootstrap 前移到本任务：新增 `frontend-app/scripts/desktop-browser-preflight.mjs` 与唯一联网准备入口 `npm run bootstrap:desktop-browser`；显式 bootstrap 后必须实际运行 browser `--version`。第一波任何 desktop RED 前先通过 preflight，gate/smoke 本身仍禁止联网安装。
6. 为 Blocker A 建立 legacy raw malformed、v2 outcome、Claude interrupt liveness、applied-but-ACK-lost、provider restart alias rebind/typed封口、late T1/T2 与 privacy 先失败测试；不能只建立旧 `success:false` happy failure fixture。
7. 为 Blocker B 建立 Prompt History RPC reject 后 Composer 层没有可见错误的先失败测试。
8. 冻结 instrumented production-shape Wails harness owner。harness 必须由 test-only build tag/独立测试入口构建，只监听 loopback；release build guard 必须证明 tag、flag、route、symbol 均不存在。若只能新增 production-reachable debug RPC，计划立即 BLOCKED。
9. 用 AST guard、类型解析与 LSP xref 生成第 2.5 节动态 `U`，记录 stable identity、owner、候选来源、分类和现有 sink；inventory 输出到临时目录而非 evidence Markdown。在 Task 3 落门禁前，任何未分类项均为 blocker。
10. 在 subject SHA 上重新计算每个拟修改目录的 production 文件数和 code-size budget；目录已到 15 个文件时必须选择仍有预算的现有 owner或拆入有明确语义的新子目录，禁止新增 baseline。
11. 为本计划会修改的 `.ps1` 建立真实 PowerShell LSP 支持：在 `cmd/mcp-lsp/multilsp/language_service_config.go` 及测试中锁定 PowerShellEditorServices command/version/provenance，并用对外 `file(diagnostics)` 取得四级 severity；只跑 PSScriptAnalyzer 或记 `language_unsupported` 不能解除 blocker。

停止条件：LSP 工具不可用、两条 blocker 无法稳定复现、基线 build 产生无法归因的 drift、或执行 base 未经确认。

### Task 1：失败终态真实性

**STATE：** TODO

**Invariant：** 完成第 2.1 节 v2 原子迁移：request/stalled 非终态，所有 provider 在 bounded deadline 内形成 exactly-one canonical outcome；failed/interrupted/cancelled 永远不能进入 success UI。全部 turn-scoped event、assistant buffer/item 和共享 terminal ledger 均绑定 `(threadId,turnId,generation)`，晚到 T1 不得封口或污染 T2。partial output 只能引用已验收 canonical item；最终失败必须带失败状态、安全原因，以及 capability-backed recovery 或安全下一步。

**主要修改落点：**

- governance-owned `internal/dto/turn/schema/turn_ref.v1.json`、`turn_ref_exemptions.v1.json`、`turn_terminal.v2.json`、`internal/dto/shared/schema/public_error.v1.json`、`internal/dto/tool/schema/public_tool_result.v1.json` 与 public-tool visibility policy（product lane 全部只读）
- `scripts/generate_turn_terminal_contract.go`、生成 drift/field guard tests
- generated TurnRef/terminal/public-error/public-tool Go/JS types（均为生成物）
- `internal/dto/turn/event.go`、turn-scoped shared/tool/item DTO、`internal/contract/provider.go`
- 共享 `internal/module/turn/refregistry`（含 provider alias rebind）、`internal/module/turn/terminalledger`、provider recovery/raw adapters、eventsurface
- 经动态 references 找到的 hooks/bus/memory/insight/trajectory/uistate/observation/notifier/cron/orchestration consumers
- Claude/Codex interrupt、terminal 和 fake-clock liveness tests
- `frontend-app/src/entities/client/model/helpers/a1/runtimeTurnOutcome.js`（只调用 generated validator，不推导 outcome；写作快照该目录为 11/15，Task 0 仍须按 subject SHA 复核预算）
- `frontend-app/src/entities/client/model/helpers/a1/runtimeTurnOutcome.test.js`
- `frontend-app/src/entities/client/model/helpers/a1/clientStoreBridgeRuntime.js`
- `frontend-app/src/entities/client/model/runtimeAssistantTimeline.js`
- `frontend-app/src/entities/client/model/helpers/assistantEventRuntime.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- `frontend-app/src/pages/chat/thread/TimelineMessage.jsx`
- `frontend-app/src/pages/chat/thread/TimelineMessage.test.jsx`
- `frontend-app/src/pages/chat/ChatTimeline.css`
- `frontend-app/src/shared/i18n/appI18n.zh.json`
- `frontend-app/src/shared/i18n/appI18n.en.json`
- `internal/archtest/turn_ref_field_registry_test.go`、`turn_terminal_field_registry_test.go`、`public_error_field_registry_test.go`、`public_tool_result_field_registry_test.go`
- `frontend-app/scripts/turn-terminal-contract-guard.mjs`
- `frontend-app/package.json`（第一波即新增 `smoke:desktop:failure`）
- `frontend-app/scripts/desktop-failure-smoke.mjs`（只启动 instrumented production-shape harness）
- `frontend-app/tests/e2e/desktop-failure.spec.js`（新增）
- `scripts/frontend_failure_fixture_guard_test.go`（证明 release binary 不含 tag/flag/route/symbol）

**RED 矩阵：**

- legacy raw 缺失/非布尔 `success`、未知 status、`success:true+failed/error/aborted`、失败但无 public error：必须在 typed DTO 前 fail-fast，不能进入 v2。
- legacy `success:false + failed + raw error + partial result`：adapter 输出 `outcome=failed`、safe public error 和 partial；不得保留 legacy truth 字段或产生 success notice。
- v2 payload 缺失/未知 outcome、出现 legacy `success/status/reason` 或未知字段：必须 fail-fast。
- interrupt 两阶段：prepare reject/no active 不发 request；acceptance 在任何 provider 副作用前登记；自然terminal在Prepare与acceptance CAS之间到达时不补发request且token失效，acceptance后terminal先于RPC return则按序暂存；`NOT_APPLIED/APPLIED/INDETERMINATE`、并发重复 request、无 TurnRef、acceptance timeout 和 finalization timeout各形成唯一规定结果。`NOT_APPLIED` 必须覆盖 `running→natural success`、`running→natural failed`、新 requestId retry、T2尝试与late event：只结束interrupt attempt、不写terminal、不关闭T1，旧token重放失败，随后T1自然terminal exactly-once。必须覆盖 `Prepare(T1)→T1自然终结→T2启动→Commit(tokenT1)`、late/expired/replayed/cross-session/Abort token，断言 typed no-op且 T2零副作用；删除 token 任一 session/TurnRef/handle/request/deadline/nonce绑定或 fallback 到 current active 的 mutant 必败。另覆盖 provider 已应用 interrupt但 ACK丢失/timeout：保持 pending，不立即 failed；真实 late `interrupted` 在 deadline前 exactly-once胜出，只在最终 deadline形成 timeout failed。Claude accepted interrupt 先有一次 request event，随后 fake clock `<=5s` exactly-one `outcome=interrupted`。
- `start T1 -> interrupt/stall T1 -> start T2 -> late/duplicate T1 terminal`：只关闭 T1；`stalled T1 -> resumed -> success` 不提前收尾，`stalled -> timeout` 才 failed。
- T2 开始后分别注入 T1 assistant/thinking delta、item completed/final answer、command output 和 approval/tool result；全部被严格关联到 T1 或拒绝，不能写入/关闭 T2，也不能按 thread 选择最后一个 open item。
- TurnRef 动态矩阵故意从任一 plan/delta/item/command/tool/approval/terminal DTO 或 mapper 删除 generation、加入未登记 turn-scoped DTO、错误 exemption、重复 live key、跨 provider session Resolve、close 后 10 分钟内 replay及 tombstone 过期 replay；必须分别命中 guard或 fail-fast diagnostic，永不重绑当前 turn。另覆盖 active T1期间 provider重启并把 remote alias P1换成P2：registry-issued token全字段匹配时原子 rebind后仍是同一 TurnRef，旧 P1晚到事件拒绝；token缺失/过期/重放、request/handle/instance不匹配或alias冲突时先唯一 `PROVIDER_SESSION_RESTARTED`封口，再允许新 Start；删除 token任一绑定、alias tombstone、复用 generation或遗留旧 active的 mutant必败。
- `outcome=cancelled` 缺 typed cancellation evidence、`agent/stopped/thread/stopped` 被当作 success、重复/冲突 terminal：均失败。
- 分别向错误通道 raw error/result/summary/message/reason/stopReason 注入 token/path/prompt/tool output/stack：wire、partial refs、store、DOM、warning、trace均不得出现原值；另用 schema-valid `visibility=user-authorized` 安全 tool result证明正常 eventsurface/store/DOM 可见，并证明 `diagnostic-only` 只进入 diagnostics/受限 telemetry、不会进入普通 tool timeline/result/diff DOM；非法 visibility、absolute persisted path、unknown field和超长 preview失败。
- hook/bus/memory/insight/trajectory consumer 缺失或错误映射：request/stalled 不收尾，success→After/允许 memory，failed/interrupted/cancelled→Failed/禁止成功 memory；registry missing/stale 均失败。
- `_threadPatch` 与终态同时到达：thread patch 可以应用，但不能覆盖终态真实性。
- 第 2.6 节 TurnRef/terminal/public-error/public-tool 任一字段删除、mapper 分支删除、registry stale/unknown/exemption 无理由：对应 field guard必须给出精确 missing/stale。

**Implementation boundary：**

- governance TurnRef/terminal/public-error/public-tool schemas 与 visibility policy 是各字段集合和 sink矩阵的唯一 owner，product lane只实现消费；raw adapter 在进入 v2 前完成 presence/type/legacy tuple校验。前端 validator只验证 generated contract，不映射 reason 文本，也不接受 legacy success/status。
- timeline、notice、activity、diagnostics、hooks/bus、memory/insight/trajectory、uistate、observation、notifier 和 orchestration 必须直接消费同一个 generated outcome；parity test 对同一 fixture比较所有消费者结果和领域矩阵。
- shared terminal ledger 按 `(threadId,turnId,generation)` compare-and-set；turn event 禁止调用 thread-wide finalize。agent/thread lifecycle 需要关闭多个 turn 时必须使用独立、显式测试的 lifecycle owner。
- CommitToken 只活在 provider private state，绝不进入 bus/RPC/frontend；refregistry是 generation/provider-alias/rebind/closed replay唯一 owner，terminal ledger消费 TurnRef但不得重新生成或按 thread猜测。transport error必须先映射 CommitResult，不得把 ACK丢失当成 `NOT_APPLIED`，也不得把明确未发生的 interrupt伪装成 turn failure；`FenceAndFailTurn` 若存在必须是独立 typed capability并证明 handle/event/resource原子封口。
- partial output 只保存同 turn 已验收 assistant item 引用并增加明确失败呈现；terminal DTO 不携带自由文本，也不得复制 message store。
- private provider cause 绝不越过错误 canonical boundary；正常工具 view 只经独立 public-tool sanitizer/validator。错误 envelope为空或非法时 fail-fast，不生成“未知成功”或直接渲染 raw cause。
- 第 2.6 节 TurnRef/terminal/public-error/public-tool 动态字段链和五类 fail-first mutant 未完成时，`C01/C02/C03` 保持 `NOT_VERIFIED`。

**强制 producer RED/GREEN：** 先累计并验收 assistant item，再收到 raw `turn/failed`；断言 adapter 只输出 `TurnCompletedV2{Outcome:failed, PublicError, PartialOutputRefs:[same-turn-item]}`，legacy truth 字段和 raw result/summary/message/reason/stopReason 不进入 canonical DTO。前端对应断言同 turn partial 与 failed 状态并存、无 success notice；T2 不受任何晚到 turn-scoped 事件影响。

**第一波 production-form proof：** Task 1 同时建设 `npm run smoke:desktop:failure -- --case terminal-failed`，通过独立 test build tag 构建的 production-shape Wails harness，证明 raw provider fixture→adapter→v2 eventsurface→真实 frontend DOM。它不是 release executable。runner 只连接 loopback 且要求 harness 随机 token；普通 dev/release binary 不识别该 flag/route。`scripts/frontend_failure_fixture_guard_test.go` 必须从无 test tag 的 production build 扫描并实启验证注入端点不存在。完整错误矩阵和 push/CI 持久化由 Task 4 完成；真实 packaged artifact 只做无注入 identity/provenance smoke。

**GREEN：** TurnRef registry/alias-rebind/tombstone与动态全事件守卫、commit-token TOCTOU + `NOT_APPLIED`保留原turn活性 + indeterminate-ACK矩阵、schema generation/field guards、provider adapter/liveness、全部 Go consumer parity、runtime ledger、Chat可见状态、错误隐私 + public-tool visibility矩阵和最小 failure-harness smoke全部通过；production binary无注入 guard通过；修改文件 LSP diagnostics为0。

### Task 2：Prompt History 用户可见错误与恢复

**STATE：** TODO

**Invariant：** 当前 ArrowUp fetch 失败时用户必须看到失败原因和重试入口；ArrowDown 保持本地恢复，合法 supersede 不显示错误，当前 draft 不丢失。

**主要修改落点：**

- `frontend-app/src/features/prompt-history/hooks/usePromptHistory.js`
- `frontend-app/src/features/prompt-history/hooks/usePromptHistory.test.jsx`
- `frontend-app/src/features/prompt-history/model/promptHistoryController.test.js`
- `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
- 对应中英文 i18n copy

**RED 矩阵：**

- 当前 generation 的 ArrowUp 首次 RPC reject。
- typed stale error 首次重试后再次失败。
- invalid response contract。
- cwd/thread lifecycle 变化后的旧请求作为合法 supersede：不显示错误、不改 draft。
- ArrowDown 不发 RPC，并正确恢复 draft sentinel。
- 用户对当前 ArrowUp 失败重试成功，原始 draft sentinel 保持正确。

**Implementation boundary：** Prompt History hook 持有局部 navigation error/retry intent；Composer 渲染可访问的 `role=alert` 或等价 live region，并提供重试。不得把该错误塞入新的全局 page cache。

**第一波 production-form proof：** 复用 Task 1 的 instrumented production-shape harness，执行 `npm run smoke:desktop:failure -- --case prompt-history-reject`，让真实 Wails/RPC 形态对 `thread/promptHistory` 返回带 stable code/public message 的确定性 reject；断言 Composer 显示安全原因和 retry、draft 不变、无 success notice，且 raw injected cause 不进入 DOM/trace。release binary 无注入 guard 必须同时通过。

**GREEN：** controller reject 语义、hook 状态、Composer DOM 可见错误、focus/keyboard、retry 和最小 Prompt History desktop failure smoke 全部有断言；无 success/degraded 误报。

### 第一波出口

只有同时满足以下条件才进入第一波复评：

- 两条生产 blocker 均有修复前 RED、修复后 GREEN 和至少一个受控 mutant。
- FE-ERR-0、FE-ERR-1 通过。
- 两条 blocker 都已有最小 production-form desktop failure proof，不只使用纯函数 mock。
- 同一 `SUBJECT_SHA` 上 lint/test/build/embed 通过，证据由外部 attestation 绑定。
- scorer 确认第一波 required controls 全部 PASS 并输出实际 Raw/Effective；不得再声明预填 `75.4`。

---

## 5. 第二波：系统化错误治理，由 scorer 输出实际分数

### Task 3：强制 error sink 与语义静态守卫

**STATE：** TODO

**Invariant：** critical async action 不得省略错误出口；console-only/fallback-only catch 必须在门禁阶段失败。

**主要修改落点：**

- `frontend-app/src/shared/ui/runUIAction.js`
- `frontend-app/src/shared/ui/runUIAction.test.js`
- `frontend-app/src/shared/errors/frontendErrorContract.js`（新增，稳定 action/category/sink 契约）
- `frontend-app/src/shared/errors/frontendErrorContract.test.js`
- `frontend-app/src/shared/errors/frontendFailureTaxonomy.v1.json`（governance-owned，product lane 只读）
- governance-owned `frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.schema.json`、`noncriticalSyncExemptions.v1.schema.json` 与 `frontendErrorContractPolicy.v1.json`（product lane只读）
- `frontend-app/src/shared/errors/criticalAsyncActionManifest.v1.json`
- `frontend-app/src/shared/errors/noncriticalSyncExemptions.v1.json`
- `frontend-app/src/shared/errors/criticalAsyncActionManifest.test.js`
- `frontend-app/scripts/no-silent-async-failure.mjs`
- `frontend-app/scripts/no-silent-async-failure.test.mjs`
- `frontend-app/package.json`

**Guard 必须 fail-first 捕获：**

- Promise/try catch 只调用 console。
- registered user-action catch 返回 `{}`、`[]`、`null` 或默认值。
- user failure cell 没有 visible、background failure cell 没有 health、escalate 被当成唯一 sink，或 legal cancellation 没有 typed predicate。
- `(actionId,failureClass,sink)` cell 与 production proof result 不 exact match，runner 未观察同一 action id/sink，或多个 action 只引用一个未区分 tuple 的通用 case。
- producer typed RPC/error union、action contract、response schema、throw/reject site 与 manifest failure class 不 exact match；删除真实类别、缩减 governance taxonomy 或出现无法解析的 dynamic rejection。
- 动态候选全集 `U` 中存在未分类项，Promise/thenable 被错标为 `noncritical-sync`，或 manifest/callsite/exemption 出现 missing/stale stable identity。
- public dev runtime 的 RPC 失败默认值未显式标记 degraded/error。

守卫允许 documented parse fallback 或明确 capability absence，但必须有精确 allowlist、owner、reason、对应测试和非成功 UI；禁止通配豁免。scanner/type resolver/schema parser 失败必须以非零退出，不能产出空 inventory 或沿用旧 snapshot。

能力边界必须分开：AST/type guard 负责动态生成 `U`、producer failure set、stable identity、分类 parity、console-only、fallback-only、manifest cell 和 exemption 完整性；governance taxonomy 只补不可省略的跨动作基类且 product lane 不可缩减；proof runner 负责回报观察到的 action/failure/sink tuple；LSP xref 用于独立复核候选与调用影响面；Task 1 的 runtime/字段契约测试负责证明 terminal outcome 不会进入 success；Task 1/2/4 的 desktop smoke 负责最终 UI 断言。不得要求单文件 AST guard 推理跨 bridge/store/DOM 行为。

### Task 4：错误矩阵与真实桌面失败注入

**STATE：** TODO

**门禁与 smoke 修改落点：**

- `frontend-app/package.json`
- `frontend-app/scripts/desktop-smoke.mjs`
- `frontend-app/scripts/desktop-ux-smoke.mjs`
- `frontend-app/scripts/desktop-browser-preflight.mjs`（Task 0 已创建，本任务复用并加 workflow contract tests）
- 新增 `smoke:desktop:failure` 与 `smoke:desktop:embed` 对应脚本/测试
- `frontend-app/scripts/desktop-packaged-smoke.mjs` 与 `smoke:desktop:packaged`（新增；只作 subject build-lane自检，不产生可信 PASS）
- instrumented production-shape Wails harness 与 production no-injection guard
- `frontend-app/tests/e2e/desktop-ux.spec.js` 及 failure spec
- `scripts/ai_maintenance/push_gates.go`
- `scripts/ai_maintenance/main.go`
- `scripts/ai_maintenance/gate_execution.go`
- `scripts/ai_maintenance/main_test.go`
- `scripts/ai_maintenance/push_risk_gate_test.go`
- `.githooks/pre-push` 与对应 hook runtime tests
- `.github/workflows/ci.yml`
- `.github/workflows/frontend-desktop-release-gates.yml`（Task G0 governance-owned reusable workflow模板；product lane只读）
- `.github/workflows/frontend-desktop-release.yml`（Task G0 governance-owned caller模板；product lane只读且无 release写权限）
- `docs/open-source/RELEASE_CHECKLIST.md`
- `scripts/contracts/frontend-smoke-manifest.v1.schema.json`（Task G0 governance-owned；本任务只实现 writer/consumer）
- `scripts/contracts/frontend-assets.v1.schema.json`（Task G0 governance-owned；本任务只实现 generator/runtime consumer）
- `scripts/contracts/frontend-runtime-build-info.v1.schema.json`（Task G0 governance-owned；本任务只实现 generator/producer/consumer）
- `scripts/contracts/codex-artifact-provenance.v1.schema.json`（Task G0 governance-owned；本任务只实现 verifier）
- `scripts/contracts/lsp-bundle-provenance.v1.schema.json`、`scripts/lsp_bundle.lock.v1.json`（Task G0 governance-owned；本任务只实现 verifier/prepare consumer）
- `scripts/contracts/evidence-bundle-manifest.v1.schema.json`、`frontend-command-evidence.v1.schema.json`、`scripts/contracts/frontend-release-attestation.v1.schema.json`（Task G0 governance-owned；本任务只实现 raw writer/producer consumer）
- `scripts/write_frontend_smoke_manifest.mjs`、`scripts/frontend_smoke_manifest_contract_test.go`
- `scripts/generate_frontend_runtime_contract.go`、generated Go DTO/JS validator 与 drift tests
- `scripts/write_evidence_bundle_manifest.mjs` 与 composite attestation/workflow contract tests
- `internal/platform/packageprovenance/assetdigest.go`、`scripts/frontend_asset_digest` 及跨平台 golden tests
- `cmd/agent-terminal/frontend.go`、`internal/ui/wails/binding.go`、`internal/ui/wails/rpc.go`
- `frontend-app/src/shared/api/wails/wailsBridgeClipboardEvents.js`、`frontend-app/src/pages/settings/settingsRuntimeHook.js` 及测试
- `scripts/package_macos.sh`、`scripts/package_linux.sh`、`scripts/package_windows.ps1` 及 package manifest contract tests
- `scripts/prepare_lsp_bundle_macos.sh`、`scripts/prepare_lsp_bundle_linux.sh`、`scripts/prepare_lsp_bundle_windows.ps1` 及 provenance guard tests
- `cmd/mcp-lsp/multilsp/language_service_config.go` 及 PowerShell language service tests

关键错误矩阵至少覆盖：

| Surface | Failure | Visible/health contract | Recovery | Production-form proof |
|---|---|---|---|---|
| turn send/completion | failed、partial、empty、timeout、interrupt final 丢失 | failed timeline + error notice | capability-backed retry/resume；否则新建 turn/复制诊断 | instrumented provider/Wails harness |
| Prompt History | RPC reject、stale retry、invalid payload | Composer visible alert | retry previous | desktop RPC failure |
| bootstrap/bridge subscribe | startup reject、disconnect | persistent bootstrap/health error | reconnect | desktop startup/bridge smoke |
| subscription callback | callback throw、parse failure | diagnostics/health | resubscribe/reconnect | runtime event injection |
| unsubscribe/teardown | Off/cleanup failure | subscription health | retry cleanup/reload | teardown failure injection |
| updater/background check | network/contract failure | persistent update diagnostics | recheck | desktop updater stub/server |
| dev build-info shim | `ui/buildInfo` reject | explicit dev error/degraded | retry | Vite runtime test |
| save/approval/file action | RPC reject/cancel | action-local visible reason | retry/cancel | desktop UX smoke |

门禁分层固定如下，不允许实现时再二选一：轻量、确定性的 `smoke:desktop:failure` 运行 instrumented production-shape harness，必须进入本地 pre-push 和 CI；较重的 `smoke:desktop:rpc`、`smoke:desktop:ux`、`smoke:desktop:embed` 必须进入 CI/release；`smoke:desktop:packaged` 只对真实无注入 release artifact 做 identity/provenance/startup 验证，必须进入 release。failure harness 证明错误行为，packaged smoke 证明发布字节与来源；两者都 PASS 才满足 FE-ERR-4，但不得互相冒充。任何 lane 缺少真实 app/server/产物前置条件时必须显式失败并报告 `BLOCKED/NOT RUN`，不能静默跳过、联网自建替代物或降级为 mock。

新增 gate id 固定为 `frontend:desktop-failure-smoke`，并用 gate-plan 测试锁定 frontend/provider/eventsurface 相关路径会触发它。Playwright/desktop 断言至少包含：出现 `role=alert`、显示 stable code 对应的 safe public message、不出现成功提示、partial output 明确标记失败、存在 capability-backed recovery 或安全下一步、diagnostics 保留 thread/turn、`pageErrors=[]`。harness 必须确定性、本地可运行，不依赖外部 provider 凭据或网络。

provider/eventsurface-only push 也必须触发该前端 failure gate；pre-push 的 Node runtime 准备条件要按 required gate 判断，不能只按 `frontend-app/**` diff 判断。CI 必须执行同一 failure-harness gate。注入 seam 只存在于 test-tag harness、只监听 loopback、使用随机 token；无 test tag 的 dev/release binary 不得包含 flag、route、handler 或 symbol，production guard 必须以构建字节与实启探测双重证明，而不是在运行时“拒绝一个仍然存在的 debug RPC”。release lane 还必须对每个最终 artifact 物化出的 executable 绑定 SHA 后分别运行 forbidden marker/symbol/byte denylist，以及携带随机 token 的 flag/route 主动探测；只扫描另一次默认 production build或只相信 `injectionEnabled=false` 自报均不计证据。

Task 0 的 browser preflight 是所有 desktop runner 的共同前置：解析顺序固定为显式 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`、项目锁定依赖已安装的 Playwright-managed Chromium、受支持平台候选路径；解析后实际执行 browser `--version`。唯一联网准备入口 `npm run bootstrap:desktop-browser` 固定执行 lockfile 对应 Playwright 的 `install chromium`；Ubuntu workflow 准备 step 使用 `--with-deps`，macOS/Windows 不带该参数。任何 gate/smoke 自身不得联网安装；缺 browser 时在行为测试前 fail-fast 并打印检查来源和精确 bootstrap 命令。Task 0 preflight 未 GREEN 时不得开始第一波 desktop RED。

现有 desktop RPC/UX smoke 都使用 dev URL，不能证明 embed 实际运行。新增 `smoke:desktop:embed` 必须在完成 `npm run build` 和 `make frontend-embed-verify` 后启动不带 `VITE_DEV_URL`/`FRONTEND_DEVSERVER_URL` 的无注入 Wails host，证明实际加载 `cmd/agent-terminal/web-dist`。dev-server smoke、failure harness、embed host 和 packaged identity smoke 是四个不同被测对象，证据不得互相冒充。

subject build-lane packaged smoke 的 owner 固定为 `frontend-app/scripts/desktop-packaged-smoke.mjs`，但它只能产生 `BUILD_ONLY` 自检结果；最终 T05/T06 裁决 owner是 root-controlled evaluator只读挂载的 governance-owned trusted probe，后者不执行或 import subject runner。唯一 manifest schema 是 `scripts/contracts/frontend-smoke-manifest.v1.schema.json`，严格字段固定为：

```text
schemaVersion / subjectSha(40 hex) / governanceBaseSha(40 hex) / platform
runtimeContractVersion / assetTreeDigestAlgorithm
sourceAssetManifestPath / sourceAssetManifestSha256 / sourceEmbeddedAssetTreeSha256
artifacts[] {
  kind / publishName / materialization
  artifactPath / artifactSha256
  executablePath / executableSha256
}
```

三平台 package 脚本不得各自拼 JSON，必须调用同一个 `scripts/write_frontend_smoke_manifest.mjs`；writer 从 clean subject、最终 regular-file artifacts 及其实际物化 executable 自行计算 SHA/hash，不接受调用方传入“已计算 hash”，也不得声称从 Go executable 解包出 `embed.FS`。`artifactPath` 必须指向最终可发布的 DMG/archive/installer；directory-only dev bundle 不计 T05。`executablePath` 是受控 staging/install root 内的相对路径。所有路径必须规范化；symlink 必须解析且仍位于 root。schema unknown/missing/type mismatch、路径逃逸、短 SHA、hash mismatch、空/重复 artifact kind 或 publish name 均 fail-fast。manifest 自身 digest 由外部 evidence bundle/attestation 记录，不把自身 hash 写进自身。

writer 必须在 codesign/notary/staple/archive 等最终字节变换之后运行，并自行物化每个最终 artifact 计算 executable digest；manifest 生成后 artifact 不得再变化。Windows workflow 显式使用 `-Artifact all`，`artifacts[]` 必须 exact 包含 portable zip 与 installer：zip 解压到临时 root，installer 以固定 silent 参数安装到隔离临时 root，验证后卸载并检查无残留。publish artifact name/path/hash 集合必须与 attested `artifacts[]` exact equality；漏证一个、额外上传一个或只替换 installer 均失败。package guard test 必须锁定 writer 位于每个平台最终变换之后，故意在 writer 后修改/重签 artifact 必须使 packaged smoke 失败。

`assetTreeDigestAlgorithm` 只允许 `sha256-path-size-content-v1`：枚举 FS 下除固定自描述文件 `frontend-assets.v1.json` 外的全部 regular files，拒绝 symlink、重复路径和大小写折叠冲突；将路径 NFC 规范化并转为 `/` 分隔，按 UTF-8 byte order 排序；每项编码为 `path + NUL + decimalSize + NUL + lowercaseContentSha256 + LF`，最后对拼接字节计算 SHA-256。自描述文件自身单独计算 `source/embeddedAssetManifestSha256`，不得进入自己的 entries/tree digest。唯一实现位于 `internal/platform/packageprovenance/assetdigest.go`，并提供不接受调用方任意 exclude 的 `DigestFrontendAssetsDir` 与 `DigestFrontendAssetsFS(fs.FS)`；CLI、embed verifier 和 runtime 都调用它，JS/PowerShell/Bash 不得重写算法。三平台 golden fixture 锁定排序、Unicode、空文件、自描述排除和路径冲突。

embed 同步阶段必须生成 schema-valid `cmd/agent-terminal/web-dist/frontend-assets.v1.json`，列出除自身外的 asset exact set、size/content SHA 和 tree digest；`sourceAssetManifestPath` 固定指向该 repo-relative 路径。writer 在 clean subject 上重算 source manifest/hash/tree。production runtime 启动时必须对实际 `frontendDistFS()` 调用 `DigestFS`，并解析 embedded asset manifest做 exact-set 比较；不一致立即启动失败。

runtime build-info 不能继续是 `map[string]string`/`any` 弱契约。governance-owned schema 固定为 strict oneOf：两种 variant 都有 `display={version,appVersion?,commit,runtime,buildTime?,dirty:boolean}`；`mode=dev` 明确无 provenance，`mode=packaged` 必须同时携带严格 `provenance={subjectSha(40 hex),runtimeContractVersion,embeddedAssetManifestSha256(64 hex),embeddedAssetTreeSha256(64 hex)}`。各层拒绝未知字段，必填显示字段非空，packaged不允许 `unknown/dev` identity，且强制 `display.dirty=false`、`display.commit` 为 40 位 lowercase hex并逐字节等于 `provenance.subjectSha`。packaged VCS identity只能来自 `debug.ReadBuildInfo` 的 `vcs.revision/vcs.modified`（构建强制 `-buildvcs=true`），缺 setting、modified或 caller/env/ldflag伪造均启动失败；UI可另行展示短 SHA，但 wire不允许短值。生成器派生 Go DTO与 JS/Node validator。实际 `embed.FS` calculator→typed build-info provider→Wails/RPC serializer→generated frontend validator/Settings UI→subject build runner→trusted probe→trusted lane/composite是单一字段链；dev shim只能返回 schema-valid dev variant，非法/空/默认 `{}` 必须显示可见 build-info error，不能提示“已刷新”。provenance后两项必须来自运行中的真实 `embed.FS` 计算，不能来自生成常量或调用方输入。

field guard/mutants 必须覆盖字段删除/改名、unknown field、空或默认 digest、stale runtimeContractVersion、map/any 回退、serializer/JS/Settings/runner/attestation 分支删除、`dirty=true`、短/错 commit、commit与subject不等、缺 buildvcs和调用方伪造 hash；错误输出给出 producer→consumer symbol。该子链未 GREEN 时 package chain、`C06/T05/T06` 均为 `NOT_VERIFIED`。

trusted probe 独立完成：重算每个 artifact hash→受控物化→重算 executable hash→启动该真实 executable→读取运行时实际 FS/VCS digest→比较 governance/subject manifest、source asset manifest、磁盘 artifact/executable、运行进程，并通过实际 UI/asset 请求证明 shell来自该 FS。它从 root policy/verified lock读取 expected identity，不接受 subject runner生成的 PASS或 expected hash。用 asset A 编译却注入 expected digest B、替换 executable、修改 embed fixture后保留旧 manifest、stale dist、只改 manifest、dirty/commit mismatch、短 SHA、伪造 subject runner或任一 artifact主动注入探测可达都必须失败。trusted probe不注入错误矩阵，只验证 startup、真实 UI shell、provenance和无注入；失败矩阵由前述 governance-owned外部 probe驱动 instrumented harness负责。

### Task 4.1：三平台 workflow 的可执行准备合同

两份 repo workflow 都在 Task G0 以模板形式建立并进入 `trustedExecutionContract`，root attestation锁 `approvedProductWorkflowSetSha256`；product subject不得新增、修改或替代它们。`.github/workflows/frontend-desktop-release-gates.yml` 只暴露 `workflow_call`，caller输入只允许 `governance_base_sha/governance_attestation_id/governance_attestation_content_sha256/subject_sha/release_mode/codex_artifact_attestation`；`attestation_root_id/root_policy_sha256/root_verifier` 必须来自组织级不可变设置和固定 digest action，禁止 caller传入。`.github/workflows/frontend-desktop-release.yml` 在 `workflow_dispatch` 与 release tag上作为唯一 build caller。首个 `verify-attestation-root` job调用外部 root action，验签 governance full object并输出只读、内容寻址 lock；三个 OS build lane只生产 artifact/raw bundle。repo `frontend-release-aggregate` 只能 exact收集三 lane artifact IDs/digests并向外部 trusted evaluator发起请求，不能提交自算 PASS、不能运行 release scorer、不能访问 signer/publish key。repo workflow不含任何 release/package upload写权限；外部 evaluator完成三 OS probe与唯一 scorer、root signer签发 composite后，唯一外部发布服务验签并上传。缺 caller、workflow-set digest不匹配、root job可被subject替换、caller自选 root、subject不是 governance后继、repo内出现publish权限/第二endpoint、任一矩阵 job skipped或 lane自签 T05/T06均为 contract test与组织 policy失败。

`rootSignerWorkflowDigest`、`trustedEvaluatorWorkflowDigest` 和 `approvedProductWorkflowSetSha256` 必须同时进入 governance/composite full object并由 ROOT_VERIFIER逐一比较预批准值。external evaluator可以从受信repository按40位`SUBJECT_SHA`取得、复算并匹配attested `SUBJECT_TREE_SHA`后，把content-addressed source archive只读挂载给approved verifier做静态解析；不得接受caller source tar、执行或import subject probe/scorer，也不得提供可写source checkout。它在各OS sandbox只读挂载approved verifier/probe，下载最终artifacts后独立物化、启动、查询runtime build-info/embed.FS、运行外部UI probe和复算command/bundle/package manifests。repo lane若在某平台不能证明subject命令与verifier/lock/command-record输出的UID/mount/ACL隔离，则该平台command evidence保持`BLOCKED`；执行前后重复hash不能作为降级替代。替换repo workflow、把subject runner改成固定PASS、提交预造result、替换verifier/lock/probe、root-id替换或同权限TOCTOU均必须在签发前失败。

caller 先运行 `trusted-codex-artifacts` 与 `trusted-lsp-artifacts` prerequisite jobs。Codex provenance 采用 strict versioned schema与 governance-owned trust policy，逐 OS/arch 验证 issuer、source repository/workflow identity、protected ref/tag、run id、subject、artifact name/version/digest、statement digest 与签发时间；调用方自报 JSON、自签 issuer、错误 repo/ref/arch 一律拒绝。验证后上传本次 workflow 内不可变 artifact；矩阵 job 只 download 并再次重算，设置 `SUPER_DOLPHIN_CODEX_ARTIFACT/SHA256/VERSION`。缺 source attestation、文件、version 或 digest mismatch 时 BLOCKED，禁止设置 `SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=0` 绕过。

LSP bundle 必须由外部 attested immutable artifact 或 `scripts/lsp_bundle.lock.v1.json` 中逐工具固定的 version/source/integrity/SHA/OS/arch 构建；package job 不得执行浮动 `npm install --prefix`，不得把 host `PATH` 中的 gopls/rust-analyzer/sqruff/Node/Go/JDK 当信任根，也不得用 `version:"bundled"` 占位。三个 prepare 脚本只负责解包、schema/provenance/exact file digest 验证；host PATH 最多用于找到 verifier。浮动 spec、缺 integrity、错误 issuer/arch/version 或 manifest 自签由 `release:lsp-bundle-provenance` 阻断。

| OS | 固定 shell 与准备 | LSP/package 命令 | GUI/package smoke |
|---|---|---|---|
| macOS | repo build lane使用 `bash`、setup-go(go.mod)、Node 20、`npm ci`、browser bootstrap与 `SUPER_DOLPHIN_RELEASE_PROFILE=gray-unsigned`；不接触发布凭据 | `./scripts/prepare_lsp_bundle_macos.sh` 只验证 attested bundle，设置并验证 `SUPER_DOLPHIN_LSP_BUNDLE_DIR`，再 `make package-macos` | subject runner只报 BUILD_ONLY；外部 evaluator/signing service对内容寻址 artifact签名、notary、staple并用 trusted probe启动最终 `.app` |
| Linux | `bash`；setup-go、Node 20、`npm ci`，安装 `pkg-config/libgtk-3-dev/libwebkit2gtk-4.1-dev/xvfb/dbus-x11`，browser bootstrap `--with-deps` | `./scripts/prepare_lsp_bundle_linux.sh`，设置并验证 bundle，再 `make package-linux` | repo BUILD_ONLY self-check可用 dbus/xvfb；外部 evaluator在独立 dbus/xvfb sandbox用 trusted probe启动真实 artifact |
| Windows | `pwsh`；setup-go、Node 20、`npm ci`、browser bootstrap；路径通过 PowerShell resolve/containment 校验；PowerShellEditorServices diagnostics 为 0 | `./scripts/prepare_lsp_bundle_windows.ps1` 只验证 attested bundle，再 `pwsh -NoProfile -File ./scripts/package_windows.ps1 -Artifact all` | repo只上传 zip+installer；外部 trusted probe逐一隔离解压/静默安装、启动真实 executable、验证无注入并卸载且证明无残留 |

macOS tag publish凭据只能存在于外部 signing/evaluator service，repo workflow、subject脚本与 build artifact均不可读取。服务在随机临时 keychain中导入 P12、验证 Developer ID identity/team/issuer、以临时 API key/profile执行 notarytool、staple后复验；成功/失败都强制删除 keychain/profile/key/temp files。日志与 evidence只保留签名、公证和 cleanup attestation，绝不输出 secret。tag/ref、受保护 environment批准、subject、输入 unsigned artifact digest与输出 signed artifact digest必须绑定；缺凭据、错误 team/issuer、unsigned、notary/staple未完成或 cleanup缺失均由外部 `release:macos-notary-provisioning` 阻断。

每个 repo矩阵 job 在 package前打印并验证 Go/Node/npm/Playwright/Wails、Codex/LSP provenance、subject/governance SHA和 release profile；准备缺失立即失败。package后运行 subject manifest contract与 BUILD_ONLY smoke并生成 raw lane bundle，但不得产生 control PASS。外部 evaluator逐平台重新验证 prerequisite metadata、每个 artifact的 packaged identity/无注入/runtime-build-info和 command exact set；`gray-unsigned` 只证明 build producer可复现，不能冒充已签名发布。90分的 `T06` 还要求外部 macOS signing/notary attestation、三 trusted evaluator lane与 composite attestation。任一平台、产物、凭据、环境、evaluator或外部 aggregate未执行时 release lane为 RED，90分声明被阻断。

### Task 5：关键边界类型化与 diagnostics 清零

**STATE：** TODO

**主要修改落点：**

- `frontend-app/tsconfig.contracts.json`
- `frontend-app/jsconfig.json`（仅在增量方案需要时）
- `frontend-app/src/entities/client/model/helpers/providerPreferences.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/services/apiClient.js`
- `cmd/mcp-lsp/multilsp/language_service_config.go` 与 PowerShellEditorServices registry/E2E tests
- 对应测试

要求：

- 移除失效 include，使用 `tsc --listFiles` 证明关键文件确实被检查。
- 按边界逐步开启 `checkJs`/strict/noImplicitAny，不用全局 `@ts-nocheck` 或扩大 exclude 过门禁。
- provider/turn/error payload 的运行时 schema 与类型检查一致；非法反例必须 fail-fast。
- 修复 candidate `SUBJECT_SHA` 上目标范围内实际存在的全部 LSP Error/Warning/Information/Hint；若 embed 相关诊断在新基线上出现，同样必须清零，但不得沿用已经消失的旧 Hint 充当任务证据。
- 对本计划修改的 PowerShell package/prepare scripts，必须经仓库对外 `mcp-lsp file(diagnostics)` 使用锁定 PowerShellEditorServices取得结果；server 缺失、版本/provenance不匹配或 `language_unsupported` 均为 blocker，不能用 PSScriptAnalyzer、shell parse 或人工阅读替代。
- 修改结构化字段时，执行字段生产源到全部消费端的守卫闭环。

### Task 6：覆盖率、关键 mutant 与 Hook 持久化

**STATE：** TODO

- 在 `package.json` 固定新增 `test:coverage:ratchet` 和 `test:error-contract-mutants`；落点包括 `@vitest/coverage-v8` 锁定依赖、`frontend-app/scripts/verify-coverage-ratchet.mjs`、固定治理路径 `frontend-app/config/coverage-baseline.v1.json` 与 `frontend-app/scripts/error-contract-mutants.mjs`。baseline 必须已存在于 `GOVERNANCE_BASE_SHA`，product lane 不得首次创建。
- `verify-coverage-ratchet.mjs --base "$GOVERNANCE_BASE_SHA"` 必须通过 `git show` 读取 governance base baseline，并先验证其 digest 等于 governance attestation，再与 HEAD 比较 branches/functions/lines/statements、include/exclude 和 changed-lines denominator；阈值只能提高，include 不得缩小、exclude 不得扩大、已覆盖路径不得删除。合法治理变更只能进入下一轮独立 governance lane，不能在同一 feature/fix 中审批自己；product subject 修改 baseline 文件本身直接失败。
- ratchet 自测必须临时降低任一阈值、删除 include、扩大 exclude、缩小 changed-lines denominator，四类 mutant 都必须失败且恢复后工作树 hash 不变。
- 关键错误矩阵的行为分支覆盖率必须为 100%。
- mutant runner 必须在临时目录/受控 fixture 中验证以下变异都会使命令失败，且退出后工作树 hash 不变：翻转 canonical outcome、删除 error sink、把 throw 改为 `{}`、把 error tone 改为 success、删除 turnId、复用错误 proof tuple、泄漏 raw cause、取消 desktop smoke 路由。
- 在 `scripts/ai_maintenance` 新增 `frontend:behavior-regression-test` 与 `frontend:desktop-failure-smoke` gate route；feature/refactor 生产变更不能因为提交标题不含“修复”而绕过行为测试要求。
- 在 `scripts/frontend_embed_verify_guard_test.go` 或同包新测试中新增 `TestPrePushRoutesFrontendDesktopFailureSmokeForPushRange`，以 synthetic remote SHA/ref-range 运行真实 `.githooks/pre-push`，证明 frontend、provider、eventsurface 与 gate 自身路径均会路由到 required failure smoke。
- pre-commit 保留快速、确定性门禁；完整串行测试和 desktop failure smoke 进入 pre-push/release，任何分层都不得减少最终覆盖面。

### 第二波出口

- FE-ERR-0 至 FE-ERR-4 全部 PASS。
- critical action manifest 与错误矩阵 100% 对齐。
- 自动候选全集 `U` 分类覆盖率 100%，不存在可省略 sink 的生产异步入口，也不存在把 Promise/thenable 错标为 `noncritical-sync` 的调用。
- contract typecheck 真实覆盖关键文件，约定范围 diagnostics 为 0。
- 同一 `SUBJECT_SHA` 上 failure-harness smoke、lint/test/build/embed 和 Hook 路由证明完整，证据由外部 attestation 绑定。
- scorer 确认第二波 required controls 全部 PASS 并输出实际 Raw/Effective；不得再声明预填 `84.6`。

---

## 6. 第三波：降低局部修改成本并机械跨越 90.0

### Task 7：状态边界、依赖方向与单一描述源

**STATE：** TODO

按独立小提交处理，禁止借机重写全前端：

1. 将 `App.jsx`、`SettingsPage.jsx` 等根级 `useClientStore()` 全量订阅改为窄 selector/page facade；用 React Profiler 证明 streaming delta 不再使无关 shell/page 重渲染。
2. 修复 `PromptPageView` 的 `features -> pages` 反向依赖和未生效的 service 注入；feature 只消费稳定 facade/shared contract。
3. 对 prompt/workflow/skill/shared-files/memory `*PageCacheByCwd` 逐项执行 LSP xref，只删除被证明没有生产消费者的 cache 与写入动作，保持 React Query/局部状态的唯一 owner。`sharedFilesPageCacheByCwd` 当前被 `threadForkState.js` 消费，默认排除在删除清单之外；只有先落地替代 truth source、迁移全部 consumer 并通过 fork 行为测试后才能删除。
4. 建立 canonical page descriptor，派生导航、合法 page id、alias 和 page manifest parity；兼容 alias 独立命名并测试。
5. 将 `PromptPageView.jsx`、`SkillsPage.jsx` 等高密度文件按 contract/model/query/action/view owner 拆分；不得通过压缩单行逃避物理行和复杂度守卫。
6. 前端 code-size freeze 增加 owner、reason、reviewer、到期时间和 fail-first 审批；不允许为本计划新增 baseline。

每个子任务需要独立 RED/GREEN、依赖方向或引用证据、修改前后订阅/复杂度指标，不能因为“拆了文件”自动获得架构分。`A03-state-ssot` 是 score-dependent uplift：若其非 PASS，不得伪装完成，必须作为有 owner/证据缺口的 P2/P3 残留；固定 90 profile 仍必须由其他 required controls 达到 95.5。

### Task 8：性能预算与反馈时延

**STATE：** TODO

- 对 assistant 50ms flush、长历史、settings/log 更新建立 React Profiler 重渲染基线和预先锁定阈值。
- 保留现有 200/1000/5000 turns benchmark，积累同环境样本后建立非 flaky ratchet。
- 记录 bundle/chunk、materialized count、heap delta、store fan-out 和测试反馈时间。
- pre-commit 提供快速测试入口，完整自动发现的回归集合仍由 pre-push/release 强制执行；不得把当前 2322 这个快照数字写成长期硬门槛。
- 性能阈值必须先写入证据再修改代码；不得在同一变更中放宽阈值获得 PASS。
- `P05-resource-budget` 是 score-dependent uplift；未 PASS 时必须记录具体 bundle/heap/scan 缺口和 owner，不能影响或替代 `P01-P04` required controls。

### Task 9：全量门禁、生成物与独立复评

**STATE：** TODO

最低验证集不再是一串可被末尾成功命令掩盖的裸 shell；唯一入口是 governance-owned command plan与 approved verifier。下列 bootstrap只生成不具发布证明力的本地preview；release模式不得信任该preview，必须由Task R0 trusted evaluator独立复算required-command records，并在隔离trust domain执行governance-owned external probe plan与唯一scorer。若它选择重跑任一subject command，只能放入看不到verifier/lock/result channel的untrusted child sandbox：

```bash
set -Eeuo pipefail
umask 077

: "${RUNNER_TEMP:?RUNNER_TEMP is required}"
: "${WORKFLOW_RUN_ID:?WORKFLOW_RUN_ID is required}"
: "${WORKFLOW_JOB_ID:?WORKFLOW_JOB_ID is required}"
: "${ATTESTATION_ROOT_ID:?ATTESTATION_ROOT_ID is required from protected settings}"
: "${ROOT_POLICY_SHA256:?ROOT_POLICY_SHA256 is required from protected settings}"
: "${ROOT_VERIFIER_SHA256:?ROOT_VERIFIER_SHA256 is required from protected settings}"
: "${ROOT_VERIFIER:?ROOT_VERIFIER is required on the external read-only trust mount}"
: "${GOVERNANCE_VERIFIER:?GOVERNANCE_VERIFIER is required on the external read-only trust mount}"
: "${IMPLEMENTATION_BASE_SHA:?IMPLEMENTATION_BASE_SHA is required}"
: "${GOVERNANCE_BASE_SHA:?GOVERNANCE_BASE_SHA is required}"
: "${GOVERNANCE_ATTESTATION_ID:?GOVERNANCE_ATTESTATION_ID is required}"
: "${GOVERNANCE_ATTESTATION_CONTENT_SHA256:?GOVERNANCE_ATTESTATION_CONTENT_SHA256 is required}"
: "${TARGET_OS:?TARGET_OS is required}"
: "${TARGET_ARCH:?TARGET_ARCH is required}"

test -d "$RUNNER_TEMP"
test ! -L "$RUNNER_TEMP"
[[ "$WORKFLOW_RUN_ID" =~ ^[A-Za-z0-9._-]{1,128}$ ]]
[[ "$WORKFLOW_JOB_ID" =~ ^[A-Za-z0-9._-]{1,128}$ ]]
[[ "$ROOT_POLICY_SHA256" =~ ^[0-9a-f]{64}$ ]]
[[ "$ROOT_VERIFIER_SHA256" =~ ^[0-9a-f]{64}$ ]]
[[ "$IMPLEMENTATION_BASE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$GOVERNANCE_BASE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$GOVERNANCE_ATTESTATION_CONTENT_SHA256" =~ ^[0-9a-f]{64}$ ]]
[[ "$TARGET_OS" =~ ^(darwin|linux|windows)$ ]]
[[ "$TARGET_ARCH" =~ ^(amd64|arm64)$ ]]
RUN_ROOT="$(mktemp -d "${RUNNER_TEMP%/}/frontend-maintainability.${WORKFLOW_RUN_ID}.${WORKFLOW_JOB_ID}.XXXXXX")"
RUN_SUCCEEDED=0
cleanup() {
  rc=$?
  trap - EXIT
  if [[ "$RUN_SUCCEEDED" -ne 1 ]]; then
    "$ROOT_VERIFIER" seal-failed-run \
      --run-root "$RUN_ROOT" \
      --run-id "$WORKFLOW_RUN_ID" \
      --job-id "$WORKFLOW_JOB_ID" \
      --exit "$rc" \
      --read-only-no-reuse || rm -rf -- "$RUN_ROOT"
  fi
  exit "$rc"
}
trap cleanup EXIT
EVIDENCE_DIR="$RUN_ROOT/evidence"
VERIFIED_GOVERNANCE_LOCK="$RUN_ROOT/verified-governance.json"
SCORE_PREVIEW="$RUN_ROOT/frontend-score-preview.json"

"$ROOT_VERIFIER" assert-trust-domain \
  --expected-self-sha256 "$ROOT_VERIFIER_SHA256" \
  --subject-worktree "$PWD" \
  --run-root "$RUN_ROOT" \
  --read-only-executable "$GOVERNANCE_VERIFIER"
"$ROOT_VERIFIER" resolve-governance \
  --root-id "$ATTESTATION_ROOT_ID" \
  --root-policy-sha256 "$ROOT_POLICY_SHA256" \
  --attestation-id "$GOVERNANCE_ATTESTATION_ID" \
  --expected-content-sha256 "$GOVERNANCE_ATTESTATION_CONTENT_SHA256" \
  --output-no-clobber "$VERIFIED_GOVERNANCE_LOCK"
"$ROOT_VERIFIER" verify-approved-executable \
  --verified-lock "$VERIFIED_GOVERNANCE_LOCK" \
  --os "$TARGET_OS" \
  --arch "$TARGET_ARCH" \
  --kind governance-verifier \
  --path "$GOVERNANCE_VERIFIER"

SUBJECT_SHA="$(git rev-parse HEAD)"
test "${#SUBJECT_SHA}" -eq 40
test "$SUBJECT_SHA" != "$GOVERNANCE_BASE_SHA"
git cat-file -e "$IMPLEMENTATION_BASE_SHA^{commit}"
git cat-file -e "$GOVERNANCE_BASE_SHA^{commit}"
test "$(git merge-base "$GOVERNANCE_BASE_SHA" "$SUBJECT_SHA")" = "$GOVERNANCE_BASE_SHA"
test -z "$(git status --porcelain)"

"$GOVERNANCE_VERIFIER" verify-governance \
  --verified-lock "$VERIFIED_GOVERNANCE_LOCK" \
  --implementation-base "$IMPLEMENTATION_BASE_SHA" \
  --governance-base "$GOVERNANCE_BASE_SHA" \
  --subject-sha "$SUBJECT_SHA"
"$GOVERNANCE_VERIFIER" run-command-plan \
  --mode preview \
  --verified-lock "$VERIFIED_GOVERNANCE_LOCK" \
  --plan scripts/ai_maintenance/frontend_required_commands.v1.json \
  --subject-worktree "$PWD" \
  --subject-sha "$SUBJECT_SHA" \
  --run-id "$WORKFLOW_RUN_ID" \
  --job-id "$WORKFLOW_JOB_ID" \
  --output-dir-no-clobber "$EVIDENCE_DIR"
"$GOVERNANCE_VERIFIER" finalize-evidence \
  --mode preview \
  --verified-lock "$VERIFIED_GOVERNANCE_LOCK" \
  --required-command-plan scripts/ai_maintenance/frontend_required_commands.v1.json \
  --evidence-bundle "$EVIDENCE_DIR" \
  --output-no-clobber "$SCORE_PREVIEW"
test -z "$(git status --porcelain)"
printf 'preview_evidence_root=%s\n' "$RUN_ROOT"
RUN_SUCCEEDED=1
```

`frontend_required_commands.v1.json` 必须 exact保留原最低集的 stable IDs：critical-skip、contract typecheck、RPC audit、lint、unit/coverage/mutants/build、两项 UI MCP、desktop RPC/UX/failure/embed、history benchmark、provider/eventsurface/orchestration Go tests、五类 arch field guards、governance/scorer tests、rubric drift、fixture/package/push-range tests、gate plan + 双重 scope验证 + execution、embed verify、`make guard/capcontract-check/build-plain/codemap-check/project-map-check`、skill validator、tree/status/full diff/diff-check。每个条目的 cwd和 argv均为数组；删除、重复、重排有依赖的条目、换成 shell string或缺 command record均由 approved verifier失败。

subject `validate-plan-scope` 与外部 governance verifier 必须分别把 plan 的 `changed_files` 与 `git diff --name-only "$GOVERNANCE_BASE_SHA...$SUBJECT_SHA"` 规范化后的 exact set 做 missing/stale 比较；不得接受调用方手选 `--changed-file`。外部 verifier只从 ROOT_VERIFIER 产出的 verified lock读取 expected base/policy，拒绝 caller自选 HEAD/subject、非祖先、merge-base mismatch或与 attestation不同的 base。其测试必须加入 hook、workflow、package、manifest、provider、eventsurface和 frontend混合 diff，并证明删除任一路径、把 base改为 subject/未经批准祖先或修改 subject dispatcher都会失败。

repo release workflow 还必须严格执行 Task 4.1 的 root-verify + 三个固定 OS BUILD_ONLY jobs；不再用 POSIX `${PACKAGE_PLATFORM}` 动态拼 Windows 命令。每个平台保存 preparation、trusted Codex/LSP provenance、package/runtime-build-info manifests、subject自检和 raw lane bundle；repo aggregate只提交 exact三 lane artifact IDs/digests与 approved workflow-set identity。外部 trusted evaluator重新下载并复算 artifact/bundle/package/required-command record exact set，运行批准的外部 probes，并在自己的 trust domain只运行一次 scorer；root signer只签 evaluator内部 result，外部 publish service再验签 composite并保证上传集合与 attested artifacts exact equality。repo aggregate、subject runner或另一 workflow均不能发布。

要求：

- 每条命令必须通过 `run-command-plan` 记录 `commandId/SUBJECT_SHA/runId/jobId/cwd/argvDigest/environmentDigest/toolchainDigests/start/end/exit/stdout/stderr/testCount/artifact hashes`；exact command set任一 missing/duplicate/nonzero即禁止 finalize，不能用最后一条 clean test覆盖中间失败，也不能让subject PATH/loader/env改变approved verifier或命令语义。
- UI MCP 只证明其覆盖的导航/交互，不得替代 provider、Wails、断连、partial-output failure 的桌面证据。
- `make guard` 不替代上述 provider/eventsurface Go 回归或 `--push-gates` plan/execution 真值；新 gate 还必须通过实际 pre-push/push-range 路由测试。
- `ATTESTATION_ROOT_ID/ROOT_POLICY_SHA256/IMPLEMENTATION_BASE_SHA/GOVERNANCE_BASE_SHA/GOVERNANCE_ATTESTATION_ID+CONTENT_SHA256` 必须等于 ROOT_VERIFIER 认证的 full object，且 root id/policy/verifier来自 protected settings而非 caller input；governance 是 subject的祖先且不能等于 subject，`SUBJECT_SHA` 必须等于 clean detached checkout的 HEAD。所有证据写到本次 run随机私有外部目录；旧目录、symlink或不同 run nonce不得复用。若 build产生 tracked drift，提交生成物后选择新 SUBJECT_SHA，从头重跑，不能把 dirty结果拼进旧 subject。
- verifier/probe/verified-lock与 evaluator result channel必须位于 subject命令不可写、不可替换且不可继承写句柄的 trust domain。`run-command-plan` 对 subject进程使用独立 UID/容器或等价 mount namespace，只映射只读源码输入和该 command私有输出；初次 hash后同权限复用可写 executable/lock/evidence不构成验证。
- 在 `SUBJECT_SHA` 上重新执行 LSP 五类证据：`grep` 定位 terminal/error-sink 符号，`inspect` 理解 generated validator 与 gate 入口，`xref` 枚举全部 producer/consumer/callsite，`file(read_file)` 精读修改范围，`file(diagnostics)` 批量检查全部 production JS/JSX 和本次修改的 Go/MJS/脚本。四级 severity 任一非零即失败；Task 0 的旧诊断不能复用。本计划修改的 PowerShell 必须通过锁定 PowerShellEditorServices取得 diagnostics；不支持时保持 BLOCKED，不能写 PASS或用其他工具替代。
- build 后检查 `frontend-app/dist` 与 `cmd/agent-terminal/web-dist` manifest 一致，且 tracked diff 只包含授权范围。
- embed smoke 必须证明桌面未使用 dev proxy；trusted evaluator packaged identity/provenance probe 或任一平台 prerequisite 未执行时，`T05/T06=NOT_VERIFIED`，subject BUILD_ONLY smoke不得补位，也不得声称 source/embed/实际发布行为一致。
- 独立 reviewer 只核对 root policy、rubric/scorer、governance+composite full attestations、trusted evaluator/probe身份、control evidence和 gate truth，不手填分数。最终 90声明必须引用外部 root id/policy digest、external signer/evaluator/repo workflow-set digests、`GOVERNANCE_ATTESTATION_ID+CONTENT_SHA256/COMPOSITE_ATTESTATION_ID+CONTENT_SHA256`、三 trusted lane IDs与 bundle-manifest digests；subject tree、opaque ID、repo build lane或subject runner报告不能自证。
- 最终报告固定输出：`ATTESTATION_ROOT_ID/ROOT_POLICY_SHA256/rootSignerWorkflowDigest/trustedEvaluatorWorkflowDigest/approvedProductWorkflowSetSha256/IMPLEMENTATION_BASE_SHA/GOVERNANCE_BASE_SHA/GOVERNANCE_ATTESTATION_ID+CONTENT_SHA256/SUBJECT_SHA/SUBJECT_TREE_SHA`、`COMPOSITE_ATTESTATION_ID+CONTENT_SHA256`、三 trusted lane artifact/bundle/command-result digests、clean状态、asset membership/rubric/runtime/frontend-error/trusted-execution/package/evidence/supply-chain/probe digests、各 control状态、Raw/Effective basis points、Gate、证据缺口、未验证项和下一复评入口。

---

## 7. 证据上限与防刷分条款

### 7.1 缺证据只改变 control 状态，不由 reviewer 估算上限

所有传播规则必须编码在 `frontend_maintainability_rubric.v1.json.gates` 并由 scorer 测试锁定；本节是生成摘要，不是第二套规则。固定传播如下：

| 条件 | 强制 control 状态 | 全局影响 |
|---|---|---|
| 存在失败显示成功 | `E01=FAIL` | `G2_BLOCKED=true`，Effective 最多 5990 basis points |
| 任一关键 user action console-only/无 visible | `E02/E04=FAIL` | 禁止第一/第二波出口 |
| background/subscription/teardown 无 persistent health | `E03/E04=FAIL` | 禁止第二波出口 |
| 只有静态/单元测试，无 production-shape failure harness | `E05/T04=NOT_VERIFIED` | 禁止 FE-ERR-4 与 90 |
| strict raw decode、field guard 或 safe error schema 缺失 | 对应 `C01/C02/C03=NOT_VERIFIED` | 禁止第一波出口 |
| typecheck 漏文件或 strict/checkJs 失效 | `C04=FAIL` | 禁止第二波出口 |
| full diff gate routing 未证明 | `T03=NOT_VERIFIED` | pre-push/release gate 不成立 |
| root trust/full governance attestation 无法验签、asset membership/base/digest 未外部批准、external signer/evaluator/repo workflow-set identity不匹配或 subject修改治理资产 | 全部 required controls=`BLOCKED` | 禁止启动产品评分 |
| required command result missing/duplicate/nonzero、旧/可预测证据目录、symlink/TOCTOU、subject可写 verifier/lock/probe或 caller自报 PASS | 对应 evidence controls=`BLOCKED` | 禁止 manifest/evaluate/签发 |
| 任一发布 artifact provenance、实际 runtime embed.FS、逐 artifact无注入、trusted probe或三平台 evaluator composite缺失 | `T05/T06=NOT_VERIFIED` | 禁止 90/release 声明 |
| repo workflow拥有发布写权限、存在第二发布 endpoint或外部发布服务未绑定 root-verified composite | `T06=BLOCKED` | 禁止 release 声明 |
| 依赖边界、性能基准或预锁阈值缺失 | 对应 `A04/P02/P03/P05=NOT_VERIFIED` | 不获得该 control 分值 |
| 任一目标 LSP Error/Warning/Information/Hint 非零或 diagnostics 不可得 | final-90 gate FAIL/BLOCKED | 即使 rawBasisPoints 偶然达到 9000 也禁止声明 90 |

scorer 不接受“维持原分”“大约 7.9”或 reviewer 手工 cap；control 非 PASS 就是 0 分并保留状态。

### 7.2 防刷分

- 测试数量、代码行数、文件数量本身不加分。
- 同一 evidence key 只能绑定一个主要 control/维度；测试被 release gate 强制执行后，才可用独立 gate evidence 计门禁持久性。
- mock-only 路径不能证明真实 Wails/provider 行为。
- 可选 `onError` 但调用方可以省略，不算强制错误治理。
- 返回 `{}`、`[]` 或默认值不能包装成“韧性”，除非 UI 明确显示 degraded/error。
- 不能手工编辑 dist/embed 制造一致性。
- 不能扩大 code-size/coverage/performance baseline、allowlist 新债务或修改评分权重获得 PASS。
- 不能由 product subject 新建/修改 governance rubric、terminal/public/error/action schemas、error policy、required-command/probe、repo workflow模板、coverage baseline、failure taxonomy、Codex/LSP trust policy或把自己选择的 base标成 approved。
- 不能由 G0/subject 自举 root trust、混淆 external signer/evaluator/repo workflow digest、修改外部 policy、只校验 attestation ID非空，或让 repo aggregate接触签名/发布凭据。
- 不能省略/跨组重分类 governance asset，或由实现者自行解释 approved contract digest 的成员集合。
- 不能把 failure harness 冒充 release artifact，也不能把无注入 packaged identity smoke冒充错误矩阵。
- 不能让一个 OS build lane、一个 Windows artifact、subject packaged runner、调用方自签 provenance 或 runtime 自报常量代表 trusted evaluator composite/actual-byte proof。
- 不能用错误隐私禁令删除正常授权工具结果，也不能把 raw tool payload复制到 terminal/public-error/telemetry；`diagnostic-only` 不能进入普通 tool timeline/result DOM。
- 历史日志、可预测/复用目录、不同 run或SUBJECT_SHA、dirty worktree、subject tree内自报证据、缺 exact command records或缺外部 attestation的结果不作为最终复评证据。
- 不能依赖执行前一次 hash抵御同权限 TOCTOU；verifier、lock、probe、result channel必须与 subject进程权限隔离。
- 修复后的新测试必须证明 fail-first；至少通过修复前提交、反向补丁或受控 mutant 证明会失败。

---

## 8. 停止条件

出现任一情况立即停止相关生产修改并记录 blocker：

- LSP 定位、理解、影响面、精读或 diagnostics 无法取得，收窄重试后仍失败。
- 基线 blocker 不能稳定复现，或 RED 来自脚手架/环境而非目标行为。
- 必须使用默认值、空 catch、console-only、返回空对象或吞错才能通过测试。
- 新增第二套 terminal/error/loading/store 真相源，或 Go/JS 终态枚举不是从同一 schema 生成。
- `TurnStalled`、request 或 lifecycle event仍触发 terminal completion/drain/flush，interrupt acceptance无法在 provider副作用前原子登记，commit token能重放/跨 session/重新命中 current turn，或 ACK丢失/transport timeout被当成确定 `NOT_APPLIED`。
- TurnRef 无 governance schema/单一 registry/closed replay策略，任一 turn-scoped DTO可缺 generation，provider restart无强证明 rebind或typed封口，remote alias被当 canonical turnId，或未知/过期事件会回退到 current/thread-wide identity。
- 需要扩大 code-size baseline、coverage/performance 容差或通配 allowlist。
- 需要让 production binary 保留 failure-injection flag/route/symbol，或让 packaged smoke冒充错误矩阵。
- root trust或 governance full attestation不可验签、G0/subject能修改 external signer/evaluator policy或接触签名/发布凭据、三个 workflow identity未分别锁定、governance membership/base/digest不可验证、被评分 subject修改治理资产，或 error schema/failure class/coverage分母只能由 subject自报。
- Codex/LSP artifact 无受信 issuer/version/digest/OS/arch 证明、package job依赖浮动下载/host PATH，或 macOS publish 凭据无法安全装配与清理。
- PowerShellEditorServices 无法通过仓库 MCP-LSP 提供本次 `.ps1` diagnostics，或 Windows 任一发布 artifact 无法隔离物化、验证和清理。
- 证据必须写回被测 SUBJECT_SHA、复用旧目录、subject可写 verifier/lock/probe/result、command exact set不完整、trusted evaluator不独立复算、缺外部 attestation，或出现 commit自引用才能完成报告。
- repo workflow仍有 release/package写权限、存在第二发布 workflow/secret/endpoint，或外部发布服务不要求 root-verified composite。
- 修改越过 owned files、污染用户 dirty worktree 或覆盖现有未提交改动。
- build 产生无法解释的 generated drift。
- desktop/UI MCP 不支持目标失败动作，却被当成验收通过。
- `rawBasisPoints<9000`、G2 未解除、任一 required control 非 PASS、任一 P0/P1 未关闭或 diagnostics 未清零，却准备声明“完成/90 分”。

---

## 9. Definition of Done

只有全部满足才算计划完成：

- [ ] Task R0 的组织级 root policy/verifier、external signer/evaluator workflow digests与唯一外部发布权限已在 G0之外固定；repo workflow默认只读，G0/subject不能修改信任根或接触签名/发布 key，任意 ID/自签/错误 issuer/workflow/ref/replay/第二发布路径 mutants均失败。
- [ ] Task G0 独立治理提交已获两名 reviewer审批和 root signer签发；ROOT_VERIFIER验签 full governance object，asset membership exact-set、repo product-workflow-set、verifier/probe build/stdout/result/mutant bundle及 score/runtime/frontend-error/trusted-execution/package/evidence/supply-chain digests全部锁定，product subject对治理资产零 drift。
- [ ] rubric/schema/scorer 是唯一评分真值源；unknown/missing/duplicate/rounding/G2/8999/9000 fixtures 通过，历史 `61.8` 已机械复算并解释差异。
- [ ] Blocker A 和 B 均有跨层 deterministic RED、修复后 GREEN 和受控 mutant。
- [ ] terminal/public-error/public-tool/critical-action schemas、exemption语义与error policy均由G0锁定且subject只读；terminal v2单一生成Go/JS，legacy raw在typed DTO前presence/type校验，canonical DTO只含一个`outcome`真值；外部 verifier独立parser使schema+generator+guard+test协同弱化mutant失败。
- [ ] interrupt 两阶段 acceptance 在任何 provider副作用前进入共享 terminal ledger；provider-private single-use CommitToken绑定 session epoch/TurnRef/handle/request/deadline/nonce并在锁内 CAS重验。T1结束后 commit、T2启动、expired/replay/cross-session/Abort均 typed no-op；`NOT_APPLIED/APPLIED/INDETERMINATE`分流，已应用但ACK丢失不提前failed，随后 `<=5s` exactly-one terminal，Claude、Codex、desktop/orchestration timeout、terminal-before-return与 late-authoritative-final均有 fake-clock/幂等测试。
- [ ] `TurnStalled` 只进入 health；`stalled→resumed→success` 不提前收尾，只有 typed stall timeout形成 failed；hooks/bus/memory/insight/trajectory outcome 矩阵通过。
- [ ] governance `TurnRef(threadId,turnId,generation)` schema生成 Go/JS，turnId是稳定本地逻辑ID且provider ID仅为alias；唯一 refregistry实施 Start/Resolve/PrepareAliasRebind/CommitAliasRebind/Close、private single-use RebindToken、provider session epoch和 bounded tombstone。合法restart强证明后沿用原TurnRef，无法证明则typed唯一封口；全部turn-scoped delta/plan/item/command/tool/approval/terminal动态枚举，`T1 -> T2 -> late T1`任一事件都不会污染或thread-wide封口T2。
- [ ] failed/interrupted/cancelled 和 legacy malformed tuple 均不能产生 success notice；partial 只引用同 turn 已验收 canonical item，并展示 safe public reason 与 capability-backed recovery/safe next step。
- [ ] public error/partial-ref schema在错误边界阻断 raw error/result/summary/message/reason/stopReason中的 prompt/tool/path/token/stack；独立 public-tool schema只允许服务端清洗、visibility授权的 bounded view，governance `visibility×sink`矩阵证明 user-authorized正常可见且diagnostic-only不进入普通DOM；负向隐私 mutants与正向语义均通过。
- [ ] 动态 `U`、生产契约导出的 failure set、governance taxonomy、manifest、exemption 与 `(actionId,failureClass,sink)` proof cells exact parity；approved verifier不调用subject guard而独立重建候选与exact diff，user→visible、background→health、legal cancel typed predicate和observed tuple均由外部probe/runner证明。
- [ ] console-only/fallback-only catch 被守卫阻断；subscription callback、disconnect 和 teardown failure 进入持久 health。
- [ ] TurnRef/terminal/public-error/public-tool/action/package/runtime-build-info 七条字段链同时反向发现完整 consumer候选集，registry missing/stale、producer unknown和 exemption fail-first证据齐全。
- [ ] `typecheck:contracts` 不含失效路径，并用 `--listFiles` 证明检查关键契约文件；production JS/JSX 和本次修改的 Go/MJS/脚本 diagnostics 四级 severity 为 0，PowerShell 由锁定 PowerShellEditorServices 经 MCP-LSP 取得 0 diagnostics。
- [ ] coverage ratchet 从 `GOVERNANCE_BASE_SHA` 固定路径读取批准基线并拒绝 subject 首建/修改、阈值降低、include 缩小、exclude 扩大、分母缩小；完整 mutant 集通过且工作树 hash 不变。
- [ ] gate planner 只使用 attestation 绑定的 governance-base full diff，`validate-plan-scope` 证明 changed_files exact parity并拒绝 self/non-ancestor/mismatched base；真实 synthetic push-range 的 pre-push 与 CI 均触发 failure-harness gate。
- [ ] failure harness、dev RPC/UX、无 dev proxy embed、无注入 packaged identity/provenance 是四类独立证据；production binary 无 injection flag/route/symbol。
- [ ] governance runtime-build-info schema生成 typed Go/JS，strict dev/packaged variants覆盖实际 embed.FS/VCS producer→Wails/RPC→Settings→trusted probe→attestation；packaged强制dirty=false、40hex display.commit等于subjectSha，字段删除/改名/unknown/default/stale version/map-any/短SHA/commit mismatch/caller伪造 mutants均失败。
- [ ] source asset manifest、每个 artifact/executable和运行时真实 `frontendDistFS()` digest一致；asset A/expected B、替换字节、stale dist、只改 manifest、短 SHA、路径逃逸及逐 artifact注入 mutants均失败。
- [ ] Codex 与 LSP bundles 通过 governance-owned issuer/repo/ref/version/integrity/OS/arch policy；package job 无浮动解析或 host-PATH 信任根。
- [ ] macOS、Linux、Windows repo lanes完成 Task 4.1 prerequisite/package并只产BUILD_ONLY artifacts；外部 evaluator用approved probe完成全部 artifact identity/无注入/runtime smoke，Windows zip+installer exact set且清理无残留，外部macOS signing service完成签名/notary/staple与临时凭据 cleanup attestation。
- [ ] 三 trusted evaluator lane在隔离trust domain复算 canonical bundle/command/package manifests并运行固定probe；外部 evaluator exact验证 macOS/Linux/Windows、运行唯一scorer，root signer只签内部result。ROOT_VERIFIER验签唯一 composite full object，外部发布服务才上传且集合与attested artifacts exact equality；repo aggregate不能自报PASS或发布。
- [ ] clean `SUBJECT_SHA` 的全部 raw结果写入本次run随机私有 external evidence bundles，required command exact set逐项有记录；verifier/lock/probe/result对subject不可写，并有 root policy、三个workflow identities、`GOVERNANCE_ATTESTATION_ID+CONTENT_SHA256/COMPOSITE_ATTESTATION_ID+CONTENT_SHA256`、三trusted lane IDs/bundle-manifest/command-result digests；tracked docs summary不参与subject自证。
- [ ] 最终 diff 仅含授权范围，生成物由规范入口产生，用户原工作树未被清理或覆盖。
- [ ] scorer 输出 `rawBasisPoints>=9000`、错误维度 `>=90` tenths、其他维度 `>=85` tenths，全部 required controls PASS；独立 reviewer 复核 scorer/attestation 而非手填分数。
- [ ] G2 通过，已知 P0/P1 为 0；任何 `requiredFor90=yes` control 的 `NOT_VERIFIED/BLOCKED` 都阻断完成声明。仅 `A03/P05` 可作为已量化、带 owner 的 P2/P3 残留，并且不得使维度或 Raw 低于门槛。
