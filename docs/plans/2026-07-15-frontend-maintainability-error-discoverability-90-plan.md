# 前端可维护性与错误可发现性 90 分提升计划（桌面版）

> **状态：** PROPOSED / docs-only execution plan
>
> **同步基线：** main 与 origin/main 均为 b40867229af8e17916c00393639ccb0fcb4bf6fc
>
> **产品边界：** 单机桌面应用。运行边界是 React、Wails、Go 后端和本机 provider 进程，不按分布式系统设计。
>
> **目标：** 错误可发现、终态真实、用户动作不得只写 console；修复与证据闭环后，脚本在同一提交上算出 ≥90 分。

本文是执行合同，不是“当前已经 90 分”的证明。历史估分只能作为参考；最终分数必须绑定同一提交上的代码、测试和审查结果。

---

## 1. 范围与简化原则

### 1.1 本计划覆盖

- frontend-app 中的页面、状态、bridge、错误边界、Prompt History、审批、设置和关键操作。
- Go 侧 turn/provider DTO、事件映射、Wails/RPC 边界和必要的本机 provider 生命周期。
- 前端 lint、test、build，相关 Go 测试，embed 校验和真实桌面 smoke。
- 可机械计算的前端维护性评分，以及每轮两名全新智能体的独立复审。

### 1.2 本计划明确不做

不要求分布式共识/事务、外部 signer/OIDC、证明链、三平台 evaluator 或独立发布授权；本地 RPC 仅在可重试处用 requestId/幂等。远程多节点另立项。

### 1.3 保留的硬原则

- 失败不能显示为成功。
- 直接用户操作失败必须在当前界面可见。
- 后台错误必须进入可持续查看的 Health/Diagnostics 面板，不能只写 console。
- DTO、RPC 和事件缺字段、未知枚举或类型错误时 fail-fast，禁止默认成成功。
- raw cause、token、命令、堆栈和敏感路径不得进入 DOM。
- 不新增第二套 terminal、loading 或 error 真相源。
- 文档只描述可执行规则；测试排列组合放在测试代码和表格中，不在正文重复展开。

---

## 2. 评分协议

### 2.1 五维权重

| 维度 | 权重 | 90 分最低维度线 |
|---|---:|---:|
| 错误可发现性 | 35 | 90 |
| 架构与状态边界 | 20 | 85 |
| 契约与类型 | 15 | 85 |
| 测试与交付 | 20 | 85 |
| 性能与反馈 | 10 | 80 |

每个 control 只有 PASS、FAIL、NOT_VERIFIED 三态。只有 PASS 获得分值，其他状态得 0 分，不给主观部分分。

`dimensionScore=ΣPASS`（0..100）；`rawBasisPoints=Σ(dimensionScore×weight)`（0..10000）；`displayScore=rawBasisPoints/100`，仅显示时取一位小数。

### 2.2 控制项

| 维度 | Control | 分值 | 90 分必需 | 证据 |
|---|---|---:|---|---|
| 错误 | E01-terminal-truth | 25 | yes | provider raw 到 UI outcome 单一映射，失败不产生 success notice |
| 错误 | E02-visible-action-error | 20 | yes | 关键用户动作失败均有 role=alert 或等价可见面 |
| 错误 | E03-background-health | 15 | yes | 断连、订阅、恢复失败进入持久 Health/Diagnostics |
| 错误 | E04-safe-public-error | 15 | yes | safe PublicError、隐私清洗和错误关联 ID |
| 错误 | E05-safe-recovery | 15 | yes | retry/reconnect/restart 只在能力真实存在时展示 |
| 错误 | E06-failure-matrix | 10 | score | 最小失败矩阵全部通过 |
| 架构 | A01-terminal-ssot | 25 | yes | terminal outcome 只有一个 owner |
| 架构 | A02-state-ownership | 20 | yes | 关键状态有唯一 writer 与 ownership 测试 |
| 架构 | A03-dependency-direction | 20 | yes | shared、entity、feature、page 依赖方向受静态守卫 |
| 架构 | A04-action-registry | 20 | yes | 生产 actionId 与 registry missing/stale 均为 0 |
| 架构 | A05-generated-boundary | 15 | score | dist/embed 只由规范构建生成 |
| 契约 | C01-strict-rpc-validation | 25 | yes | 缺字段、错类型、未知枚举 fail-fast |
| 契约 | C02-terminal-field-guard | 25 | yes | Go/JS/event consumers 字段 parity |
| 契约 | C03-public-error-contract | 20 | yes | PublicError 和 recovery action 严格验证 |
| 契约 | C04-critical-typecheck | 15 | yes | 关键 JS/JSX 纳入真实 typecheck |
| 契约 | C05-provider-rpc-parity | 15 | score | provider、event surface、frontend 合法/非法矩阵 |
| 测试 | T01-red-green-regression | 25 | yes | 每个生产 blocker 先 RED 后 GREEN |
| 测试 | T02-critical-action-coverage | 25 | yes | Task 2 |
| 测试 | T03-wails-integration | 20 | yes | Task 3 |
| 测试 | T04-local-gates | 15 | yes | pre-commit/pre-push 或等价 CI 门禁 |
| 测试 | T05-build-embed-smoke | 15 | score | build、embed、桌面启动 smoke |
| 性能 | P01-render-isolation | 30 | yes | 无关 store 更新不使主页面大面积重渲染 |
| 性能 | P02-history-budget | 25 | yes | 200/1000/5000 turns 固定阈值不退化 |
| 性能 | P03-feedback-budget | 25 | yes | 本地轻门禁有冻结的反馈阈值 |
| 性能 | P04-resource-budget | 20 | score | bundle、chunk、heap 有冻结上限 |

Task 0 在 `frontend-app/scripts/` 创建唯一 controls/baseline JSON 和 scorer。每项 `allOf[]` 固定 `cwd+argv[]+timeout`、结构化 `caseIds+testCount` 或 metric/threshold，并与事实 exact diff；手填 PASS、弱命令、零测试、case 未命中及 missing/duplicate/stale/unknown 均为 NOT_VERIFIED。

必需 control 全 PASS 为 86.25；再完成 E06+T05 为 92.75；Task 3 还要求 C05，完整路径为 95.0。只认 scorer，不得预填或用加分补偿错误维度低于 90。

### 2.3 一票否决 G2

存在任一项时，不得声明完成或 90 分：

- failed、interrupted 或 cancelled 被展示成 success。
- 关键用户动作失败只进入 console。
- 非法 RPC/事件被默认成空对象、旧值或成功。
- raw error、token、命令或敏感路径进入 DOM。
- 当前提交的 required control 有 FAIL 或 NOT_VERIFIED。
- 当前复审仍有 open P0/P1。
- lint、test、build 或适用的桌面 smoke 未通过。

---

## 3. 最新代码上的已确认问题

以下事实来自同步基线 b4086722 的复查。

### 3.1 失败 turn 仍可显示为成功

当前链路：provider failed → Go/Wails completion → clientStoreBridgeRuntime → runtimeAssistantCompletion → applyAssistantCompletion → 无条件 success notice。

落点：`assistantEventRuntime.js:212`、client bridge/timeline、`internal/provider/codexapp`、event surface。partial 可保留，失败不能变成功。

### 3.2 Prompt History 失败仍只写 console

当前链路：ComposerDock ArrowUp/Down → runUIAction → RPC/validator 抛错 → 默认 console.error，且没有 onError。

关键落点是 `ComposerDock.jsx:69`、`runUIAction.js:1`、`features/prompt-history` 和 `shared/api/backend`。失败必须保留草稿/光标，显示安全错误并提供安全 retry。

### 3.3 历史审查中仍有效的门禁缺口

- no-silent-async-failure 尚未阻断可省略 `runUIAction.onError` 导致的 console-only、默认空值和假成功。
- `tsconfig.contracts.json` 的 `checkJs:false`、`strict:false` 不能证明 C04 关键 JS/JSX 已检查。
- mock 回归不能替代 provider/Wails 失败到 DOM 的真实 smoke。
- 未冻结阈值前，chat-history 的 P02/P04 只能是 NOT_VERIFIED。
- `internal/provider/codexapp/factory.go:99 unusedfunc` Information 必须清零。

本节没有给当前分数。Task 0 必须在实施提交上重新运行全部基线命令和 scorer。

---

## 4. 最小生产契约

### 4.1 TurnTerminalV2

所有 turn-scoped delta/item/terminal 共享 `TurnRefV1={threadId,turnId}`；同一 thread 内 turnId 不复用，缺失或错配立即 fail-fast。TurnTerminalV2 最小字段为：

~~~text
schemaVersion = 2
eventId
threadId
turnId
outcome = success | failed | interrupted | cancelled
terminationCause? = user_request | provider | system
terminationRequestId?
publicError?
partialItemIds?
occurredAt
~~~

规则：

- success/failed 禁 termination；success 无 PublicError，failed 必须有。interrupted/cancelled 的 user_request 须匹配同一 TurnRef 已受理 Stop requestId 且无 PublicError；provider/system 禁 requestId 且须有 PublicError；未知/错配 cause 是可见契约错误。
- partialItemIds 只引用同一 TurnRef 已验收的 assistant item；buffer 至少按 `(threadId,turnId,itemId)` 键控。
- 每个 TurnRef 首个已验证 terminal 永久封口；同 eventId/内容 no-op，内容冲突报错；封口后事件只进清洗后的 Health，不改 UI truth。
- frontend canonical DTO 禁止 success、status、reason、error 等第二真相字段。
- provider raw 字段只允许在 Go adapter 中映射一次，前端不得再次猜 outcome。
- 未知 outcome、缺字段、错类型或互斥字段同时出现时，显示“响应契约错误”并记录 diagnostics，不得继续成成功。
- `TurnInterruptRequested{TurnRef,requestId}` 只由 Stop accepted result 生成，不能从 raw 名推断。
- provider fixture 锁定 terminal：Claude `turn:interrupted`→interrupted；Codex raw interrupted 或 completed+status=interrupted→interrupted，status=cancelled→cancelled，其余 success=false/status=failed→failed，success=true/status=completed→success；unknown 或 success/status 均缺失→可见 contract error。仅该 mapping 可封口；禁用 canonical `TurnInterrupted`。

Task 1 新建 `turn_ref.v1.json`、`turn_terminal.v2.json` 和一个生成器，产出 Go/JS validator；不得手写两套定义。

### 4.2 PublicErrorV1

最小安全字段：

~~~text
code
title
message
diagnosticId
retryable
recoveryActions[]
~~~

recoveryActions 只允许 retry、reconnect、restart_provider、reopen_thread、copy_diagnostics；能力不存在则为空，UI 不猜重试成功。

禁止进入 wire、store、actionNotice、Health 或 DOM：

- raw cause、stack、环境变量、token、完整命令。
- 未清洗的绝对敏感路径。
- provider 私有 payload。
- 仅开发者可理解的自由文本状态。

### 4.3 关键 action 与错误出口

registry 按稳定 `actionId` 计数。AST 枚举 user/background 异步入口及可达 RPC；RPC matrix P0/P1 调用与 UI-only action 构成生产集合。解析失败、无 ID 或 missing/stale 时 A04/T02 失败。

每个 actionId 绑定调用点、类型、可达错误源（RPC reject、非法响应、适用的 `success:false`）、visible/Health sink、保留状态、owner、失败测试。起始面含 send/stop/history、approval、settings、reconnect、thread、文件、MCP、skill、更新安装；类别不能代替逐项证据。

直接用户动作失败必须 visible。后台断连、订阅、恢复和持久化失败必须进入 Health/Diagnostics；可以同时给非阻塞 banner。

console-only 仅限 exemptions JSON 中无用户影响的 cleanup/telemetry；每项须有 owner/reason/expiry/test。空 catch、无期限例外、默认空对象和假成功均禁止。

### 4.4 Stop 的桌面语义

Stop 不需要分布式 watchdog 或跨服务事务。本计划只要求：

- 点击后立即显示“正在请求停止”，该状态不是 terminal。
- Stop RPC 携带 `{threadId,expectedTurnId,requestId}`；后端在本机同一原子边界比较 active turn 并只操作捕获 handle，禁止重查 current turn。
- target 已变化时返回 TARGET_CHANGED/NOT_APPLIED、零副作用且错误可见；只有受理的 requestId 才能成为 user_request terminal 的证据。
- provider 明确返回 NOT_APPLIED、调用失败或在反馈预算内没有确认时，显示“停止未确认，任务可能仍在运行”。
- UI 只有收到 TurnTerminalV2 后才能显示已停止、已中断或已取消。
- safe recovery 可提供 retry、restart_provider 或 copy_diagnostics；不能在没有事实时显示“已隔离”。

反馈预算默认 2 秒，只约束 UI 给出“已确认或未确认”的可见反馈，不要求 provider 在 2 秒内完成任务终止。

### 4.5 Prompt History 语义

- ComposerDock 调用 runUIAction 时必须传 onError，或使用统一的 visible action wrapper。
- previous/next 失败时 draft、selection 和 history cursor 不变。
- 服务端 stale/非法 response 在安全 retry 后仍失败则可见；thread/cwd 已切换的 superseded response 不报错也不改新 draft。
- 成功切换历史不能清除仍未发送的附件。
- raw RPC cause 不进入 DOM；diagnosticId 可用于查日志。

---

## 5. 执行波次

### Task 0：重新建立基线

**目标：** 在最新 clean worktree 上得到真实分数和确定性 RED。

执行：

1. 记录 BASE_SHA、Node/npm/Go 版本和工作树状态。
2. 用 LSP 定位两条已确认 blocker 的定义、引用、调用链和 diagnostics。
3. 运行 frontend lint/test/build、相关 Go 测试和 embed verify。
4. Task-0 独立提交先冻结 controls/scorer/baseline schema，记 SCORE_BASE_SHA；当 P01-P04 runner 尚不存在时，metric 必须保持 `NOT_VERIFIED`，不得手填数值。Task 4 的 runner 经主 Agent 初审后、候选评分前，必须在隔离 worktree 中以不可变 BASE_SHA 为 subject，使用同一审计 runner 版本回测并冻结 baseline artifact；artifact 记录 BASE_SHA、runner SHA/tree、Node/npm/Go 与 OS/CPU、raw samples 和中位数。P04 必须在新建的 BASE detached worktree 从不存在的 `dist/` 开始执行固定 `npm ci` 和 `npm run build`，绑定 BASE tree、build argv、每个 dist 文件的路径/字节/内容哈希及聚合 manifest hash；runner worktree 的 ignored `dist/` 不能成为证据来源。候选版本必须用同一 runner 与环境比较，禁止候选自比候选；OS/CPU/工具链必须精确相同，三项 load average 的每项差异不得超过双方 logical CPU cores 的四分之一（最小 1），超出即 `FAIL`。P01：预热后 20 次无关 store 更新，候选主页面与无关 subtree update commit 各执行绝对门限 ≤1；BASE 的原始计数只作审计证据，不得放宽该绝对门限，并由 mutation probe 证明宽订阅可被检测。P02：对同一 history 的 production selector 与 direct-slice reference 采用交替顺序的配对 CPU block，冻结 5 次样本的归一化比率中位数，候选不得超过 BASE 对应比率的 115%。P03 为同机预热后 5 次绝对时长中位数 +15%；P04 ≤基线 105%。修复不得放宽 controls/scorer/baseline schema 或阈值公式。
5. 增加 RED：failed+partial 假成功；Stop accepted 后继续 failed；Claude interrupted 无 completed；Codex completed 的 cancelled/interrupted/failed/unknown 与 success 缺失/false；T1→T2 late event；冲突 terminal；Stop target-changed；provider cancel 伪装 user cancel；Prompt History console-only；action/scorer missing/stale/零测试。
6. scorer 输出当前 control 状态和分数；不手填 61.8 或其他历史值。

**出口：** RED/scorer fixtures 稳定失败；SCORE_BASE_SHA 的 allOf 映射保持冻结。P01-P04 可在 runner 尚未审计时维持 `NOT_VERIFIED`，但其 BASE_SHA baseline artifact 必须在 Task 4 候选评分前补齐并经主 Agent 初审；最终三名全新 reviewer 同时审查 scorer、baseline provenance 与预算公式。

### Task 1：修复 terminal truth

**主要落点：**

- Go TurnRef/terminal/interrupt DTO、RPC 与 provider raw adapter。
- internal/provider/codexapp 的 completion/error mapping。
- internal/platform/eventsurface 的 Wails 事件。
- clientStoreBridgeRuntime.js。
- runtimeAssistantTimeline.js。
- assistantEventRuntime.js。
- TimelineMessage.jsx 及测试。
- internal/module/turn 的 Stop service/tracker。

**实现要求：**

- Stop accepted 与 terminal 分型；Claude/Codex exact fixtures 驱动 adapter 与 C05，产出唯一 outcome。
- success notice 只在 outcome=success 时生成。
- failed 和异常 non-success 使用 role=alert；用户主动取消显示可见的中性非成功终态。
- partial output 与失败状态可以共存。
- 全部 turn event 使用 TurnRef，buffer 按 TurnRef/itemId；首 terminal 封口，late/conflict event 不修改 UI。
- Stop 对 expectedTurnId 原子 compare；TARGET_CHANGED 零副作用。user_request cause 必须匹配受理的 requestId。
- 非法 DTO fail-fast，不用旧字段补默认值。
- 字段 guard 从 schema/生成类型枚举 producer，并从真实引用反查 consumer；missing/stale 或 mapper 分支删除均失败。

**出口：** E01/E04/A01/C01/C02/T01 PASS；Stop/Claude/Codex fixtures、late event、target-changed、cause 对照均 GREEN。

### Task 2：补齐用户动作错误出口

**主要落点：**

- runUIAction.js 或统一 visible action wrapper。
- ComposerDock.jsx 与 prompt-history controller。
- approval、settings、thread action、file/MCP/skill action 的关键调用点。
- Health/Diagnostics store 和 UI。
- 应用更新安装及由 RPC matrix/AST 新发现的其他生产 action。

**实现要求：**

- T02-1：AST exact-diff producer callsites→actionId/kind/sink via source+binding；dynamic/unparsed/missing/stale/dup/count drift FAIL。
- T02-2：exact-diff all `producer×reachable errorSource` cells；each fails into user-visible or background persistent Health；zero/wrapper/category invalid。
- T02-3：detached-mutate real component/service error projection in `prompt-history/approval-pending/settings-save/thread-mutation/background-reconnect`；original test RED，config/fixture-only invalid。Proves 5≠161/all；L1/2 retain per-action visibility。
- retry 只重试同一用户意图，不重复成功副作用。
- onError 自身异常也必须进入 health，不能递归吞错。
- exemptions 有边界和过期时间。

**出口：** E02、E03、E05、A04、C03、T02 PASS。

### Task 3：失败矩阵与真实桌面链路

最小矩阵：

| Case | 期望 |
|---|---|
| terminal failed + partial | partial 保留，失败可见，无 success |
| terminal failed 无 partial | 失败可见，无空白完成 |
| failed terminal 后到 success terminal | 首个 failed 保持，冲突只进 Health |
| T1 terminal→T2 start→late T1 delta/item/terminal | T2 不变，Health 有诊断 |
| unknown outcome/缺字段 | 契约错误可见，health 有记录 |
| Stop accepted→继续→failed；Claude 无 completed；Codex completed 各 status/unknown | request 不封口；Claude 封口；Codex 映射非成功或 contract error |
| stop NOT_APPLIED | “未确认停止”，原 turn 仍 active |
| Stop T1 前 T2 已 active | TARGET_CHANGED，T2 零副作用 |
| stop feedback timeout | 可见未确认，可 retry/restart |
| prompt history RPC reject | draft 不变，错误可见 |
| prompt history invalid response | fail-fast，不改变 cursor |
| provider disconnect | Health 持久记录，可 reconnect |
| settings save reject | 编辑值保留，错误可见 |
| approval action reject | decision 保持 pending，错误可见 |
| 匹配 user cancel / provider 自发 cancel | 前者中性；后者必须安全错误 |

T03 PASS iff each `terminal-failed`/`prompt-history-reject` runs Go injection→`NewWailsApplication`→`App`→lifecycle→`EventBridge`→frontend→real DOM and reports hops+DOM；bypass or separate Go+mock stitching/splitting FAILS。

**出口：** E06、C05、T03 PASS。

### Task 4：维护性、性能与交付

- 锁定 allOf：A02=ownership guard；A03=dependency guard；C04=strict typecheck+exact listFiles；T02=Task 2；T03=Task 3；T04=路由/失败注入；T05=build+embed+start/failure smoke。
- 清理重复 outcome/error 推导；以 `frontend-state-ownership-guard.mjs` 和 `frontend-dependency-direction-guard.mjs` 分别锁唯一 writer 与层级 import。
- 将 terminal/PublicError/action 的 producer 与反向 consumer discovery 接入 field/drift guard。
- 让 `tsconfig.contracts.json` 对真实关键 JS/JSX 启用 checkJs/strict 并用 listFiles guard 防漏文件。
- 为 benchmark 增加 `--verify`；P01 同时满足绝对线与不回退，history/feedback/resource 超过 Task 0 阈值时非零退出；修复提交不得放宽。
- 把新静态 guard 和相关路径路由接入现有 `scripts/ai_maintenance`/Git hooks；重型真实桌面 smoke 只在最终候选运行。
- build、embed、桌面 smoke 通过后，scorer 在同一 SUBJECT_SHA 上重新计算。

**出口：** 总分不低于 90，错误维度不低于 90，其余维度达到最低线。

---

## 6. 验证与门禁

### 6.1 修改中

每个任务至少运行：

- 修改文件的 MCP-LSP 定位、定义/hover、引用、精读和 diagnostics。
- 相关 Vitest/Testing Library 测试。
- 相关 Go package 测试。
- git diff --check。

LSP 对某文件类型不支持的能力要记录为证据缺口，不能写成 PASS。

### 6.2 前端完成门禁

~~~bash
cd frontend-app
SCORE_WORKTREE="$(mktemp -d)/score-base"
git -C .. worktree add --detach "$SCORE_WORKTREE" "$SCORE_BASE_SHA"
node "$SCORE_WORKTREE/frontend-app/scripts/frontend-maintainability-score.mjs" --final --repo "$(pwd)/.." --subject "$(git -C .. rev-parse HEAD)"
~~~

`--final` 从 SCORE_BASE worktree 启动；加载 SUBJECT JS 前，用 Git plumbing 确认严格祖先和完整治理闭包逐字节相同。仅接受 clean HEAD，在新 detached worktree 跑 exact command/case、写新结果；tree 漂移、相关 untracked、零测试、结果复用均失败。命令集含 lint/test/typecheck/RPC audit/build、benchmark、真实 smoke、embed、目标 Go tests、diff-check；`--changed` 不能产出 90 分。

### 6.3 桌面验收

- T03 按 Task 3；DOM: visible/no-success/state/recovery/no-raw。
- 本计划不要求外部签名、远程 evaluator 或跨平台发布证明。
- 若本次变更实际修改打包/跨平台代码，再按现有发布流程追加对应平台验证。

### 6.4 证据记录

runner emits SCORE_BASE-schema normalized report with SUBJECT SHA/tree, control, argv, case/metric, exit/env。Scorer embeds it or persists exact bytes+path and recomputes `sha256`；summary-only/unreadable/hash missing/mismatch/cross-SHA/reuse = NOT_VERIFIED。

---

## 7. 对抗复审

最终轮使用三名此前未参与的智能体，只读审查同一冻结文档 SHA、代码 SHA 和 dirty 边界：

- Reviewer A：源码、终态、错误出口、字段链和 fail-fast。
- Reviewer B：计划可执行性、评分、测试成本、桌面产品边界和文档复杂度。
- Reviewer C：基线 provenance、门禁覆盖、集成冲突和发布就绪度。

规则：

- 三名 reviewer 分别给出带锚点、最小反例和修复的 P0/P1/P2；P0/P1 修复后必须更换三名全新 reviewer。
- P2 带 owner 保留，除非导致低于门槛；历史 finding 必须映射到当前规则/测试或明确 N/A。
- “还可以更严格”不单独成项，必须证明会造成错误不可见、错误成功、维护性回退或评分失真。

### 7.1 文档体积门禁

- 计划不超过 500 行、25 KiB；规则就地改，细节留在测试，不重复状态机或单机不需的分布式证明。
- 新的大协议单独建 ADR/计划并写明触发条件。

---

最终候选冻结：`SCORE_BASE=6fca9c98b1e2c91382e67f429013e97014b1b562`；后续 SUBJECT 仅包含本收口记录，评分与总审结论仍以提交绑定的原始证据为准。

## 8. 停止条件

出现以下任一情况，停止声称 PASS 并记录 blocker：

- 为通过测试需要默认成功、空 catch、吞错或 console-only。
- terminal outcome 仍有两个 owner，或首终态能被冲突 terminal/late delta 改写。
- T02/T03 违反 Task 2/3。
- background failure 无可持续查看入口。
- 非法 contract 可进入 store 或 DOM。
- raw sensitive error 进入 DOM。
- 需要扩大 baseline、降低 coverage 或放宽性能阈值才能过门禁。
- LSP diagnostics 存在 Error、Warning、Information 或 Hint。
- 当前复审仍有 open P0/P1。
- scorer allOf/case/command 缺失，阈值比 SCORE_BASE 放宽，或被测树不 clean/不等于 SUBJECT_SHA。

---

## 9. Definition of Done

- [ ] BASE/SCORE_BASE/SUBJECT_SHA 与 tree 已记录；final 由 SCORE_BASE scorer 启动，在 clean committed HEAD 的临时 worktree 执行。
- [ ] 25 项 allOf 的 command/case/threshold 与本轮事实 exact match，零测试/手填/旧结果均失败。
- [ ] TurnRef 贯穿 delta/item/terminal，首终态不可改写；Stop/Claude/Codex exact terminal fixtures 全绿。
- [ ] failed/interrupted/cancelled 永远不产生 success notice。
- [ ] partial output 与失败状态可以同时展示。
- [ ] Prompt History 失败可见，draft/cursor 保持，retry 安全。
- [ ] RPC matrix + AST producer set 与 critical-ui-actions registry missing/stale 为 0。
- [ ] Task 2/3/6.4 PASS；5≠161/all，SHA-256 PASS。
- [ ] background failure 进入 Health/Diagnostics。
- [ ] PublicError 在 wire/store/notice/Health/DOM 均不泄漏 raw cause、token、命令、堆栈或敏感路径。
- [ ] recovery action 只在能力真实存在时展示。
- [ ] 最小失败矩阵全部通过。
- [ ] 修改文件 LSP diagnostics 四级 severity 为 0；不支持能力已记录。
- [ ] frontend lint、test、build 全部通过。
- [ ] 相关 Go 测试、frontend embed verify 和 git diff --check 通过。
- [ ] P01 同时满足绝对 render 线与不回退；history/feedback/resource 通过 SCORE_BASE 公式，完整治理闭包未改。
- [ ] 三名全新 reviewer 对同一冻结对象均无 open P0/P1。
- [ ] 错误维度不低于 90，其他维度达到最低线，总分不低于 90。
- [ ] 最终报告明确区分“计划达标”和“当前实现实际达标”。
- [ ] diff 只包含授权范围，未覆盖任何用户已有非目标改动。
