# V3 迁移会话摘要

> 更新时间：2026-04-22
> 会话范围：(a) 代码守卫全仓放宽 `MaxPackageFiles` 25→30；(b) 3 路 CC 超限 TDD 修复（launcher / thread / uistate+toolbridge）；(c) **P21 架构演进路线图 6 份文档经 4 轮审查迭代实施闭环**（Round-1 5 路互审 → Round-2 修订 agent + 5 路复审 → Round-3 G-P 10 路独立终审 → Round-4 10 路疏漏扫描 → Q2 合入裁决 → agent 根据裁决修直）；(d) 落盘新教训 §10.29 / §10.30 / §10.31
> 当前阶段：P21 6 份文档按 Q2 合入裁决修正完毕，实施前准备完毕；P20.13 / p20.16 仍为下一轮待开工
>
> 2026-04-23 debt banner / authoritative pointer：**本页只作为会话交接页，不再承担 P22 的 live authoritative 规划。** 当前 `P22` 以 `docs/plans/迁移/p22/README.md`、`docs/plans/迁移/p22/P0-P4`、`docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md` 为准；下文 `## 4. 下一步` 仅保留 **2026-04-22** 当时的历史快照，不覆盖当前 P22 的派工顺序。若触及 signed-skill / native-skill / trust / hidden-contract 叙事，必须同步 `P22/P4` 与相关 `P21` 文档，而不是反向让本页成为权威源。

---

## 1. 当前结论

- **代码守卫全仓放宽**：`internal/archtest/guardlib.go` `MaxPackageFiles` 25 → **30**；autofix 自动删掉 `memory/prompt` 两条已失效 freeze（8/27 ≤ 30）；`thread` 包 27 < 30 本轮违规自动消失
- **3 路 CC 超限 TDD 修复**：
  - `internal/sidecar/orch/orchestration/launcher.go:166 looksTechnicalManagedAgentName` CC 16 → ≤ 10
  - `internal/module/thread/service.go:169 Delete` / `task_handoff.go:48 prepareTaskHandoffStart` / `task_handoff.go:84 resolveTaskHandoffStart` CC 14/12/23 → ≤ 10
  - `internal/module/uistate/module.go:147 applyTaskRuntimeToThreadRuntime` / `internal/platform/toolbridge/handler.go:155 toolCallRuntimeConfig` CC 11/14 → ≤ 10
- **P21 架构路线图 6 份文档 4 轮迭代闭环**：README / P0_SelfLearningSkill / P1a_MultiProviderCodex / P1b_CronScheduledTasks / P2_MultiPlatformNotifications / P3_SessionInsights 经 4 轮独立审查 + 2 轮修订（修订 agent + 主 agent 自改）+ Q2 合入裁决 + 最后 agent 按裁决直修完毕
- **§10 新教训落盘 3 条**：§10.29 必读文档路径真值自验 / §10.30 fx·bus·run.Group 三层分工铁律 / §10.31 修订只加不删原则
- **主 agent LSP 终验 10/10 通过**：`runner 内部 goroutine=0` / canonical 三处=3 / `延后到 P22≥3` / `延后到 P21=0` / `CREATE UNIQUE INDEX≥2` / `link-local≥1` / `cronLeaseActor≥2` / `canonicalize≥1` / `方案 A=0` / `markdownEscape≥7`
- **P20 历史结论保持**：P20 α 组 4 单（p20.1 / p20.10 / p20.14 / p20.15）已合入；p20.13 / p20.16 仍为下一轮开工
- **P18 系列 / P19 全收口**：memory 主链 / Claude parity / memory 子包拆分与 follow-up 修复均已落地（保持）

---

## 2. 本轮收口结果（2026-04-22）

### 2.1 代码守卫放宽

- `internal/archtest/guardlib.go:29` 更新注释：`2026-04-22 全仓再放宽：包文件数 25→30`；核心包例外（Core*）与默认等同，实际不再构成差异
- `MaxPackageFiles = 30`；`MaxCorePackageFiles = 30` 保留仅为向后兼容
- Autofix 自动清理 `internal/archtest/freeze_registry.go` 里 `memory` / `prompt` 两条 `limit=27` 的 ViolationPackageCount freeze（27 ≤ 30 触发 delete + 回写）
- `thread` 包 27 文件 < 30 → 本轮为主线的 thread 超限纯顶层解决

### 2.2 3 路 CC 修复（TDD）

- `launcher.go:166 looksTechnicalManagedAgentName`：CC 16 → ≤ 10；抽出 guard / prefix / fuzzy 分类 helper
- `thread/service.go:169 Delete` + `task_handoff.go:48/84 prepareTaskHandoffStart / resolveTaskHandoffStart`：**3 处 CC 同步修复**，行为保指测试已落盘
- `uistate/module.go:147 applyTaskRuntimeToThreadRuntime` + `toolbridge/handler.go:155 toolCallRuntimeConfig`：抽 field mapping helper / resolve chain
- CC 修复 agent 报告未单独收集（老公指令），仓库改动保持。实际结果以 `TestCodeSizeGuard` 为准

### 2.3 P21 文档 4 轮闭环详情

| 轮次 | 角色 | 产出 | 结论 |
|---|---|---|---|
| **Round-1** | 5 路 1:5 互审审查员（arch / P0-obs / P1a+P1b / P2-security / P3-store） | 5 份独立审查报告 | 一票定调 BLOCK（8 BLOCK + 7 NEEDS-FIX） |
| **Round-2** | 1 路修订 agent + 5 路复审（复用原 5 路， send_message §10.16 显眼标签） | 32 条销账 + 5 份复审报告 | 一票定调 BLOCK（2B+2N+1P）；**发现修订 agent “只加不删”新反模式** |
| **主 agent 自改** | §10.19 小维护债 | 6 份 P21 文档 直接 `lsp_edit(replace_range)` | LSP 终验 10/10 通过 |
| **Round-3** | G-P 10 路全新 codex agent（高交错维度） | 10 份独立终审报告 | 多数 PASS + 少数 NEEDS-FIX |
| **Round-4** | 复用 G-P 10 路（send_message “疏漏扫描”指令） | 10 份 delta 报告 | NO GAP / MINOR-GAP / MAJOR-GAP 结论 |
| **Q2 终裁** | 1 路全新 codex agent（预热契约后裁决） | 合入裁决书 | 老公直接让 agent 按裁决修文档 |
| **最终直修** | agent 按裁决修完 | 6 份 P21 文档最终稿 | ✅ 合入就绪 |

维度切分（避免盲区）：
- **垂直切**（G-L）：H=P0+obs / I=P1a / J=P1b / K=P2 / L=P3
- **横切 / 正交**（G/M/N/O/P）：G=架构合规 / M=跨文档一致 / N=仓库锚点核 / O=安全横切 / P=运维横切
- 双戳重叠点：SSRF=K+O双独立 / 三层契约=G+P / crash-window=J+P / signed skill P22=H+O+M

### 2.4 P21 修订关键点（已全部合入）

- **README**：新增默认值安全原则 + Canonical Turn Observation Contract 单一口径 + core↔orch hook consumer 入口章 + archtest 白名单枚举式事实
- **P0**：显弁禁 `WriteSkillContent`/`WriteSummary` 承接 project-scope / `skills/create` 缺 `cwd` 硬报错 / system scope **必须** 人工 review gate / bus callback 禁 LLM 提炼
- **P1a**：identity 三元组 (`codexHome` / `codexInstanceKey` / `codexModelProvider`) 硬报错 + `codexHome` canonicalize（`filepath.Clean + ExpandEnv + EvalSymlinks`） + binding 持久化拍板 + legacy default-home 仅 `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME` opt-in + spawnLocal 目标态注释 + approvalPolicy=never 陷阱
- **P1b**：`cronTickActor` + `cronLeaseActor` **双 actor 拆分**（消除 runner 内部 goroutine 反模式）+ crash-window 三步状态机 `pending → submitting → submitted → running → finished/failed` + `dedupe_key = sha256(job_id||scheduled_at||idempotency_key)` + `LookupByDedupeKey` / `Observe` 恢复协议 + lease TTL 30min + heartbeat 5min + `ExtendClaim(dur)` + claim_token 应用层 UUID v4 + provider 冻结 `codex|claude` + v1 白名单 codex + sqlc core-only
- **P2**：双树同构 + 方案 B 共享库放 `internal/module/notify/shared/*`（避开 archtest 白名单改动）+ SSRF 完备（https only / loopback / link-local / ULA / multicast / private CIDR / DNS rebinding / redirect 重校验）+ 三平台 payload 示例（钉钉 Markdown / 飞书 Rich Text / Slack Block）+ 统一 `markdownEscape` + 平台 signing（钉钉 HMAC-SHA256 / 飞书 timestamp+secret / Slack URL-as-bearer）+ hook consumer 入口 3 段调用链 + alias `node.config > dag.metadata > drop/error`
- **P3**：DDL 恢复 4 个 index（2 普通 + 2 partial unique）+ 4 个 `*_observed` flag（`approval_requests_observed` / `token_snapshot_observed` 默认 FALSE；`tool_calls_observed` / `tool_failures_observed` 默认 TRUE）+ Claude path approval 语义 由 "恒为 0" 改为 "observed=FALSE" + collector 三层契约 + API 落点钉死 `internal/module/dashboard/*`（不新建 insight 模块）+ 首期 API-only

### 2.5 §10 新教训落盘

- **§10.29 必读文档路径真值自验**：派单 prompt 前主 agent 必须 `lsp_file(read_file, limit=5)` 或 `lsp_grep` 核实每条“必读路径”真存在；本次触发事件：`prompts/lsp-mandatory-prefix.md` 被 8 次传染派单后 Agent 4 上报不存在
- **§10.30 fx / bus / run.Group 三层分工铁律**：fx.Module 只构造 + 资源 open/close；`BusModule` 管 `bus.subscribers`；`RunnerModule` 管长跑 actor。shutdown 流 `ctx cancel → run.Group 全退 → bus 停派发 → fx.OnStop 释放资源`。老公纠偏§：“fx 谁拉起 / bus 谁收尾”不是二选一。
- **§10.31 修订只加不删原则**：修订 agent 把 Round-2 原稿 SSRF/DDL index/canonicalize/payload/lease 节奏删了；三路独立复审戳穿；主 agent 按 §10.19 自改补回。以后派单必写“禁删除原稿硬规则 / 锁锚点 / 示例，仅允许新增或语义等价改写”；验收 `git diff --stat` 任一文档净减少 >5% 必须逐处 justify。

### 2.6 P20 历史结论保持（2026-04-20注）

以下 2026-04-20 会话结论保持，本轮未改动：

### 2.6.1 p20.1 — 恢复宿主 `prompts/list|write|delete` + store 写能力

- `internal/module/prompt/module.go`：注册 `registerPromptHandlers` 进 `rpc_handlers` fx group；吸收 `skill_catalog_fx.go` 的 `NewCompositeNativeSkillDetector` / `NewSkillCatalogProviderFx` / `RegisterSkillCatalogProviderIfEnabled`
- `internal/module/prompt/service.go`：merge-in-place 恢复 3 个 handler；`WritePrompt/DeletePrompt` 空 cwd 拒绝；`scope.cwd:` tag 校验完整
- `internal/module/prompt/skill_catalog_fx.go`：**已删除**（并入 `module.go`，解 archtest rule2/rule10）
- `internal/module/prompt/assembler.go`：新增 `PROMPT_START_CURRENT_DATE` env hook
- `internal/module/prompt/golden_test.go`：`t.Setenv` 固定日期，`TestStartAssemblyGolden` 不再随墙钟漂移
- `internal/store/prompt/contract.go`：`Store + PromptTemplateVersion + ListFilter.CWD`
- `internal/store/prompt/store.go`：`NewStore() Store`，包装 sqlc `Get/Delete/InsertVersion/Upsert/WithTx`
- `internal/store/prompt/module.go`：`Store → Reader` adapter（dashboard fx 注入保持）
- `internal/archtest/freeze_registry.go`：`prompt` `28 → 27`
- 新增测试：`TestNewPromptHandlersExposeLegacyPromptsMethods` / `TestPromptMutationsRespectCwdScope`（4 子）/ `TestStoreGetUpsertDeleteAndInsertVersion`（6 子）

### 2.2 p20.10 — Host RPC `skill/list` + `skill/expand`（渐进披露只读面）

- `internal/module/skill/rpc.go`：注册新 RPC，保留 legacy `skills/list` / `skills/match/preview` / `skills/local/*` 共存
- `internal/module/skill/rpc_skill_types.go`：name-based DTO，仅含 `name/summary/description/trust/content_hash/disable_model_invocation`；不暴露 `dir/trigger_words/force_words/allowed_tools`
- `internal/module/skill/skills_fs.go`：`validateSkillName()` 前置；path escape → `-32602`；not found → `-31001`；`content_hash` 截断前 SHA-256
- `internal/module/skill/contract.go`：`Service.Expand()`
- 测试：`rpc_types_test.go` / `skills_fs_test.go` 扩展；**skill 包 prod 新增文件数 = 0**

### 2.3 p20.14 — 前端 LaunchSkillPicker + 字段级 feature gate

- 新建：`cmd/agent-terminal/frontend/vue-app/services/skills-api.js` / `composables/useLaunchSkillSelection.js` / `components/LaunchSkillPicker.js`
- 改 `pages/UnifiedChatPage.js` + `UnifiedChatPage.template.js`：`launchSkillSelectionEnabled` 走 `resolveLaunchSkillSelectionFeature()` 字段级合并 `threadStore → projectStore → false`
- 改 `components/ComposerBar.js`：新增 `launchSkillSelectionEnabled` prop，仅 `!hasThreadId && enabled` 时隐藏 legacy selector（**feature-off 恢复旧 blank-thread 行为**）
- 改 `composables/useThreadActions.js`：blank-thread 首发顺序 `resolveLaunchSkillSelectionForStart → startThread → sendMessage`
- 改 `stores/thread-actions-helpers.js`：极小 +1/-1 diff，带 `selectedSkills/manualSkillSelection`
- 测试：`use-thread-actions.test.js` 19 tests；`composer-bar.behavior.test.js` 29 tests（+3 feature-off/on/active-thread 独立断言）

### 2.4 p20.15 — 前端真收窄 404 detector + 后端 dashboard cwd 活化

**前端**（`SystemPromptPage.js` + `system-prompt-page.behavior.test.js` 25 tests / +5）
- `isReadonlyFallbackListError()` **仅认结构化**：`status==404 / code==-32601 / name in {method_not_found, notfounderror}`；**彻底去 message fuzzy match**
- `hydrateReadonlyPrompts()` 调 `callAPI('dashboard/prompts', { cwd })` 并读 `res.prompts`
- **运行时 PoC**（F 在 node 里实测）：`isReadonlyFallbackListError(new Error("user not found"))` → `false` ✅

**后端方案 B**（`dashboard/rpc.go` + `ui_page.go` + `service.go` + `store/prompt/contract.go`）
- `dashboardPromptsParams{Cwd}` → handler 吃 `{cwd}` → `withDashboardPromptScopeCWD(ctx, p.Cwd)` 写入 context → `ui_page.go:listDashboardPrompts()` 从 ctx 取 cwd 过滤
- `dashboard/prompts` 直返 `{"prompts": ...}`（不再经 `dashboardPageField("commands",...)`）
- 空 cwd 跳过过滤（保持旧 list-only 行为）
- 测试：`TestGetDashboardPageFiltersPromptsByScopedCWD` 扩 `/repo_a + /repo_b`；新增 `TestDashboardPromptsHandlerScopesByCWDAndReturnsPromptsKey`（RPC 层断言）

### 2.5 协作统计

- **10+ 路 agent 协作**：
  - 4 路实施（p20.1 / p20.10 / p20.14 / p20.15）
  - 4 路 1:3 互审（p20.14-peer 迷失触发一次重派）
  - 2 路独立终审（C 静态 + D 动态，均未参与实施，双戳 BLOCK）
  - 一轮补修 5 路 + 1 路并行起 p20.13 前置核查
  - 二次独立终审 E/F（双 BLOCK）→ 二轮补修 3 路（E 修 dashboard 活化 / F 修前端真收窄 / 新拉 agent 修 ComposerBar legacy）
  - 1 次 agent 离线重拉起
  - 主 agent 每轮后 LSP 交叉验证
- **E/F 双戳 4 个 Blocker 全部闭环**；主 agent LSP 实测逐条核过

### 2.6 测试数字

| 入口 | 前 | 后 |
|---|---|---|
| `internal/module/prompt` 定向 | - | +3 测试（含 4+6 子）|
| `internal/store/prompt` 定向 | 3 `TestList*` | +`TestStoreGetUpsertDeleteAndInsertVersion`（6 子）|
| `internal/module/dashboard` | - | +`/repo_a + /repo_b` + `TestDashboardPromptsHandlerScopes...` |
| `internal/module/skill` | - | `rpc_types_test` + `skills_fs_test` 扩展 |
| `system-prompt-page.behavior` | 20 | 25（+5）|
| `composer-bar.behavior` | 26 | 29（+3）|
| `use-thread-actions` | 17 | 19（+2）|

---

## 3. Phase 状态

| Phase | 状态 | 说明 |
|---|---|---|
| P18 / P18.2-4 | ✅ 全部完成 | 保持 |
| P19-A / B-1 / B-1 Follow-up / C-F | ✅ | 保持 |
| P20 文档拆分 | ✅ | 20 份文档、DAG、并行组、锚点附录 |
| P20 critical path（p20.2/3/4）| ✅ | 合入 main（`78c6907`/`cec26fe`/`b0d2555`）|
| P20.1 Phase 1-11 | ✅ | 灰度 + 5 counter + 文档同步 |
| **P20 α 组 p20.1/10/14/15** | ✅ **本轮完成** | 实施 + 互审 + E/F 独立终审（双 BLOCK）+ 双轮补修全部 PASS |
| P20.5/6/7/8/9/12 | ✅ 实质完成 | P20.1 加固连带落地 |
| P20.11 | 🚫 废弃 | skill 是宿主独有能力 |
| P20.13 | 🔲 未开工 | 前置核查完成，结论 NEEDS-DOC-FIX（修订任务单后可派）|
| P20.16 | 🔲 未开工 | 等所有前置合入 |

---

## 4. 下一步

> 历史快照说明：以下 4 条是 **2026-04-22** 会话收尾时记录的下一步，不等同于当前 `P22` 的实时 authoritative 派工单。

1. **先修订 p20.13 任务单**：按 p20.10-skill-rpc agent 的前置核查报告修 3 处（archtest 数字 `24 → 18/3251` / 锚点行号漂移 / hash 语义冲突整份 vs section / `SkillInjectionPort` 误期待审批 hook）
2. **派 p20.13 实施 agent**：审批缓存接线（`(name,hash)` 全局批准，`scope=session` 走内存态）
3. **起 p20.16 集成测试**：所有前置就绪后派独立第三方 agent 做终验
4. **P20 全部完成后**：回到 P19 第三波余量治理 / compat bridge 清理 / 新需求开发

---

## 5. 交接建议

1. **archtest 真值 `prompt:27`** 已在 freeze_registry / p20/README / p20.1 / session-summary 四处一致；agent 若报出其它数字先怀疑它把 `_test.go` 混进来了（§10.20 + §10.25）
2. **迷失修复变体（§10.21 新增）**：agent 声称"删除了 X"不等于 X 真的消失；主 agent 必须 `lsp_grep` 验 0 命中才算真删。本轮 F 戳穿 p20.15 第一轮"号称去了 fuzzy match，实际只加结构化分支"的迷失修复
3. **死代码修复陷阱（§10.23 新增）**：新增 helper 不等于生效；必须追到 prod RPC 入口是否真在调用，LSP `xref(references)` 跑一遍。本轮 E 戳穿 `withDashboardPromptScopeCWD` 只在 test 里有 caller，补 handler `{cwd}` 才活化
4. **运行时 PoC 比单元测试更硬（§10.24 新增）**：Agent F 直接在 node 里调 `isReadonlyFallbackListError(new Error("user not found"))` 发现返回 `true`，这种证据比"44 tests PASS"有效
5. **独立第三方终审双票 BLOCK（§10.22 新增）**：E/F 双重覆盖静态+动态盲区；此轮 E 戳 3 个 + F 戳 1 个运行时，合并进补修直接闭环
6. **agent 离线重拉起（§10.26 新增）**：orchestration role 变 creator 是异常态，send_message 会丢；必须立即拉新 agent 接手
7. **文档先于 commit**：§10.12 canonical 流程保持（fix/refactor/test/docs 分语义 commit）
8. **不要把 Bug #1 当成 Bug #2 根因**：`prompts/list` 404 走 `p20.1`；UI 强制技能主链按断点 B/A/launch wire 分治（保持）

---

## 6. 交接结论

### 6.1 本轮（2026-04-22）

- **代码守卫放宽**：`MaxPackageFiles 25→30`；freeze registry 自动清理失效 2 条；`thread` 包 27 文件本轮违规已消解
- **3 路 CC 超限 TDD 修复**：launcher / thread / uistate+toolbridge 全部降至 ≤ 10（agent 报告未单独收集，老公指令）
- **P21 6 份文档 4 轮审查 + 2 轮修订 + Q2 裁决 + 最终 agent 直修 — 合入就绪**：
  - Round-1 5 路 1:5 互审 → 8B+7N 一票定调 BLOCK
  - Round-2 修订 agent 改 6 份 + 原 5 路复审 → 2B+2N+1P 一票定调 BLOCK，发现“只加不删”反模式
  - 主 agent §10.19 自改补回 + 落盘§10.31
  - Round-3 G-P 10 路全新终审 → 多数 PASS
  - Round-4 10 路疏漏扫描 → NO GAP/MINOR-GAP/MAJOR-GAP
  - Q2 终裁 agent 合入裁决
  - 最后 agent 按裁决直修文档
- **§10 新教训落盘 3 条**：§10.29 / §10.30 / §10.31
- **主 agent LSP 终验 10/10 通过**

### 6.2 P20 结论（2026-04-20 保持）

- P20 α 组 4 单（p20.1 / p20.10 / p20.14 / p20.15）经 1:3 互审 + E/F 双 BLOCK 独立终审 + 双轮补修全部闭环
- 基线顺手：archtest rule2/10 + golden date；prompt freeze `28 → 27`
- 全仓 build / test / archtest 全绿
- 下轮可起 p20.13（需先按前置核查修订任务单）+ p20.16 集成终验

### 6.3 稳定文档基线就绪

- 本摘要 + `docs/迁移/p21/` 6 份 + `docs/会话习惯.md` §10.29/30/31 + archtest `MaxPackageFiles=30` 四处一致

---

## 7. 2026-04-23 P22 R10 FINAL 收口（本次追加）

> 会话范围：P22 架构路线图文档从 R8 第一轮互审到 R10 FINAL 收敛；主 agent 直接仲裁落盘。
> UUID：继承 2026-04-22 会话（thread-1776917489829-2 类）
> 编译验证：本轮不改代码，archtest 仍存在 3 条 live failure（memory/ui_rpc.go x2 + prompt/classifier/claude_cli.go:59）交 R10 实施阶段

### 7.1 P22 R8 → R10 收敛全程

| 轮 | 角色 | 结论 |
|---|---|---|
| **R8 初审** | 30 路互审（V1-V10 + H1-H10 + X1-X5 + L1-L5）| 4 条 blocker 確认：H-1 JUDGEMENT_STATIC:68/94 死链 / H-2 P2 SSRF 等 0 命中（Q-D R1 谎报）/ H-3 P2/P4 silent-skip / H-4 P1a degraded-path 未约束 |
| **R8 Q R1** | 4 路新 codex Q-A/B/C/D | Q-C 🔴 BLOCK（契约命名债）/ Q-D 🔴 BLOCK（X1-X4 红票）；Q-B R1 H-1 谎报 |
| **R9 复审** | 复用原 30 路 + §10.16 显眼标签 | 30 路 delta 复核；V6 戴穿 Q-D 谎报 H-2 / V9 戴穿 Q-B 谎报 H-1 |
| **R8 Q R2** | 复用 Q-A/B/C/D | Q-B §16/§20 认错降级 + 0-hit 自验；Q-D §R2 恢复 12 条硬规则；Q-C R2 直修平台 |
| **主 agent §10.19 自改** | R8 后 | P1a/P1b §现状校准 追加 13 条 HEAD 2026-04-23 锚点 |
| **R10 红队** | 30 路全新冷启动（F1-F8 + M1-M2 + G1-G10 + S1-S10）| 🟢 5 / 🟡 9 / 🔴 7 / 空 9；真 blocker 全属代码层 |
| **R8 Q R3** | 原 4 Q | 太卡 stopped；未完成 |
| **R10 主 agent 直接仲裁** | 本次 | 落盘 JUDGEMENT_R8_QA.md §R10 FINAL（line 240-310）作为 P22 最终 authoritative |

### 7.2 4 条 R8 blocker 最终处置

| Blocker | 结果 | 独立 LSP 证据 |
|---|---|---|
| H-1 死章节号字面 | §10.31 historical commentary 保留 | STATIC §15.3 item 3 承认历史快照；字面残留非新增谎报 |
| H-2 P2 SSRF/markdownEscape/mention/铉铉/飞书/Slack etc. | ✅ 真修 | S4 §10.31 硬度量 31/31：SSRF=5 markdownEscape=11 mention=8 secret=12 |
| H-3 silent-skip → ErrXxxRequired fail-closed | ✅ 文档层真修 / 代码层 deferred | P4 ErrMissingCWD=8 ErrThreadRuntimeRequired=5 fail-closed=5；toolbridge handler.go:136-160 代码仍回退 |
| H-4 P1a degraded-path 硬约束 | ✅ 真修 | P1a:164 "不得替代权限/scope/trust-domain" + compatibility-only=1 |

### 7.3 R10 旧基线误护说明（已排除）

R10 冷启动 agent 读的是 R2 前 HEAD，以下 "🔴" 是旧版本而非当前 HEAD：
- F4 Sweeper.Run "ticker loop" → 实际 P1b:17 已是 `time.NewTimer + jitter`
- F6 P2 rollback card 洗牌 lane → 实际 §398-402 与 §276-282 完全对齐
- S3 P4:312 "二选一" → 实际 P4:315 已单值化 fail-closed

### 7.4 还有 9 条轻微维护债（MINOR，非 blocker，留待后续）

- P1c connection.dead 缺具体 file:line 锚点（F5）
- P1c SessionRuntime 是规划术语（已在 §需冻结兑容语义 说明）
- P1b §依赖图 未画 P4 尾边（G10，文字已提）
- JUDGEMENT_DYNAMIC §3/§5 对 Finding 10 旧说法未同步（G6）
- P0 用 §完成定义 而非 §验收标准（G8，命名不统一）
- F11/F12 deferred 判定未回写 README gate 表（G8）
- JUDGEMENT_STATIC §15 7 处 file:line 属 R2 当时快照（M1）
- 销账格式是 mixed-granularity 非 30 路逐路矩阵（S6）
- Q-B JUDGEMENT_DYNAMIC §19 Finding 10 旧说法未清（S10）

### 7.5 代码层 deferred 10 条债总账（交 R10 实施）

1. archtest 3 live failure → P0
2. toolbridge handler.go:136-160 仍回退 PersistentSubagentDefault → P4
3. waitDreamTask test-only caller → P2 memory
4. memory.NewRelevantMemoryFinder bridge 壳 test-only → P2 memory
5. TeamSyncService.Pull/Push test-only → P2 memory
6. ErrThreadRuntimeRequired/ErrMissingCWD 代码 0 命中 → P4
7. config.go:39 PersistentSubagentDefault=true 默认 → P0/P4
8. desktop pre-drain + watchFXShutdown 非对称 → Q-D 已记
9. registerMemoryHooks.OnStop 不 wait/drain → P2 memory
10. docs/契约/* 命名债 runner.actors vs group:"runners" → 单开契约轮

### 7.6 P22 最终判定

**✅ 文档叙事层 R10 READY**；收敛路径 BLOCK (R1) → NEEDS-FIX (R8 §14.5) → READY (R10 FINAL)。

### 7.7 Agent 使用统计

- **R8**：30 路审查 + 4 Q R1（9 Q R2）= 43 agent
- **R9**：复用原 30 路（send_message）
- **R10**：30 路冷启动红队 + 4 Q R3（后 stopped）= 34 agent
- 总计本会话约 **80 路** codex agent（离线重拉 0 次）
- 最高并发约 64 路同时存在（远超 §10.26 的 6-8 阈值，但 idle 居多）

### 7.8 子 Agent 提示词模式（本会话治成）

- §10.16 显眼标签：`⚠️ 第 N 轮：修订后复核（只审不改）` 首行强制
- §10.21 认错降级：Q-B/Q-D 谎报后强制在 §R2 显式写入 "R1 claim-vs-reality 违反"
- §10.31 硬度量清单：每路 prompt 内嵌 "期望命中次数" 表（比如 P2 SSRF ≥1 / markdownEscape ≥2）
- §10.22 E/F 轮独立终审：R10 全新冷启动 trump R1/R2 自述
- §10.29 真路径内嵌：“docs/plans/迁移/lsp-mandatory-prefix.md”替代死路径 `prompts/lsp-mandatory-prefix.md`

### 7.9 本会话新发现的教训（建议落盘会话习惯.md）

1. **§10.33 旧基线误护陷阱**：冷启动 agent 可能读到 R1 前 HEAD（文档中间轮次修过），产生假阳性 🔴。主 agent 收报时必须对每条 🔴 独立 lsp_file 核 HEAD 现状，不能直信子 agent 报告。
2. **§10.34 Q 终裁 agent 超时兄弟机制**：Q R2/R3 轮次可能收报后长时间 thinking（内部 §10.22 独立核和收敛结论需时）；老公可直接授权 stop Q + 主 agent 接管，使用 shared_file_write 或 lsp_edit 落盘 FINAL 裁决。
3. **§10.35 shared_file_write 与 repo 落盘差异**：shared_file_write 落盘路径非 repo，要写入 repo 必须用 lsp_edit(replace_range)。验证方式：写后即 lsp_file(read_file) 核路径。
4. **§10.36 historical commentary 的 §10.31 兼容**：文档中的历史叙事（描述“R1 曾犯此错”）不得被 exact-grep 当成迢失修复变体；判定标准 = 是否新增谎报 + 是否显式标为 historical。

### 7.10 下一步建议

1. **P22 变事层 READY** → 可派 R10 实施 agent按五门 gate 消化 §7.5 代码债（由最近的 F6/F8/S2 报告作为实施依据）
2. **会话习惯落盘** → §7.9 4 条教训（§10.33/34/35/36）建议写入 docs/会话习惯.md
3. **git commit（§10.12 canonical 流程）**
   - fix(p22): archtest 3 live failure（交实施）
   - refactor(p22): F5/G10/G8 minor 维护债（可选）
   - docs(p22): R10 FINAL + session-summary + 会话习惯 §10.33-36
4. **stop 所有 idle agent 释放端口**（可选，不影响功能）

### 7.11 最近 10 条对话摘要（本会话末尾）

1. 读 session-summary + 会话习惯
2. 读 P22 JUDGEMENT_STATIC.md
3. 派 30 路 R8 审查 P22
4. 派 4 路 Q 仲裁修文档（Q-A/B/C/D）
5. 30 路 R9 delta 复核
6. 4 路 Q R2 补修（Q-B 认错 H-1 / Q-D 认错 H-2）
7. 主 agent §10.19 自改 P1a/P1b §现状校准 13 锚点
8. 派 30 路 R10 冷启动红队
9. Q R3 太卡 stopped，主 agent 直接仲裁
10. 落盘 JUDGEMENT_R8_QA.md §R10 FINAL + session-summary §7

---

## 8. 2026-04-24 代码守卫 8 处超限 TDD 修复（本次追加）

> 会话范围：一文件一 agent 并行派 6 路 codex TDD 修复 archtest 新出 8 条违规；其中 2 路 §10.26 离线重启异常态但越权自组合后仍达成目标；archtest + 全仓测试全绿。

### 8.1 违规 → 修复映射

| # | 文件 / 函数 | 违规 | 手法 |
|---|---|---|---|
| 1 | `internal/sidecar/orch/notify/turn.go:169` `buildTurnCompletedMessage` | CC 11 | 拆 `buildTurnCompletedTitle` + `buildTurnCompletedBody` + `appendTurnCompletedField` + `appendTurnCompletedResult` |
| 2 | `internal/module/cron/scheduler.go:278` `driveJob` | 95 行 + CC 11 | 拆 `scheduledAtForJob` + `createPendingRun` + `markRunSubmitting` + `buildStartTurnRequest` + `persistSubmittedTurn` + `observeStartedTurn` |
| 3 | `internal/module/cron/turn_adapter.go:88` `StartTurn` | CC 11 | 抽 `resolveThreadAgent` + `executeTurn` |
| 4 | `internal/module/notify/platform/webhook.go:214` `isBlockedIP` | CC 11 | 抽 `isBlockedByStdlib` + `isBlockedByRange`（10 条 SSRF 判定 §10.31 全保留）|
| 5 | `internal/module/prompt` 包 | 31 > 30 + `matchWhenKeyMatches` CC 12 | 合并 `match_when.go` → `enable_when.go` + 抽 `match_when_support.go` + helper `matchCWDGlob/matchCWDPrefix/matchTagsHas`；越权顺手删 `summarize_provider.go` 净 -1 prod |
| 6 | `internal/provider/codexapp/server_pool.go:137` `Acquire` | CC 16（最高）| 抽 5 helper + `defer p.mu.Unlock()` 替换显式 Unlock 链；锁 Lock/Unlock 各 5 平衡 |

### 8.2 异常态事件（§10.26 新样本，落盘 §10.37）

- **Agent-859879 (notify-turn) 离线重启**：role=`creator` / parent_id 缺失 / name=self-id。报告显示修的是 `codexapp/session.go + driver.go` 无关内容（上下文丢失）；但其脱靶改动反而清掉了 `newSession` 可变参破坏 `go:linkname` 的测试兼容问题
- **Agent-879139 (cron-scheduler) 离线重启 + 大范围越权**：原任务只动 `scheduler.go`，实际扩散修 11+ 文件（turn.go / summarize_provider.go 删除 / codexapp 4 文件 / memory ui_rpc+module / notify flusher / insight flusher / turndedupe/store / prompt brief+classifier / docs codemap +81024 行），越权清掉 §7.5 的多条代码层 deferred 债 + P22 archtest live failures
- 关键判定：§10.26 离线虽是异常态，但本次 2 路异常都"越权向完成面扩张"而非"任务丢失"，全仓测试 + archtest 全绿验证无 regression

### 8.3 硬指标（§10.20 终验）

| 指标 | 结果 |
|---|---|
| `go test ./internal/archtest/... -run TestCodeSizeGuard -v` | ✅ PASS |
| `go build ./...` | ✅ 静默通过 |
| `go test ./... -count=1` | ✅ 76 个包全 ok，无 FAIL |
| 6 目标违规 archtest 0 命中 | ✅ |
| prompt 包 prod 文件数 | 30 |
| §10.31 只加不删：10 条 SSRF / 3 条 cron error 前缀 / 4 条 codexapp error | 全保留 |

### 8.4 新教训（已落盘 §10.37）

- **§10.37 role=creator 异常态下的"越权完成"变体**：离线重启的 agent 上下文丢失后可能跨任务越权自组合
- **主 agent 响应**：先 `git diff --stat` + 跑硬指标 → 全绿接受 / FAIL 精准回滚
- **派单加强**：未来派单 prompt 里显式写入"本任务范围仅限 X 文件；越权必须在 report 显式列出"

### 8.5 本轮协作统计

- 派 6 路 codex agent（1 文件 1 agent 原则）
- 2 路 §10.26 离线重启异常态；4 路 worker 正常
- 主 agent LSP 交叉验证：`lsp_file(read_file)` × 6 目标文件 + `git diff --stat` + `ls prompt | wc -l` + `TestCodeSizeGuard` + `go build` + `go test ./...`
- 本次未跑独立第三方 E/F 终审（功能层全绿已足）

### 8.6 最近 10 条对话摘要（本会话）

1. 读 session-summary + 会话习惯
2. 老公贴 8 条 archtest 违规清单
3. 主 agent 按 §10.29 核实文件路径
4. 派 6 路 codex agent 并行 TDD 修复
5. 老公"都完成了 收回报并复核"
6. 收 6 份报告（发现 2 路脱靶 / 越权异常）
7. LSP 交叉验证代码层实际修复到位
8. `TestCodeSizeGuard` PASS + `go build` PASS + `go test ./...` 76 包 ok
9. 老公"直接收尾"
10. 落盘 session-summary §8 + 会话习惯 §10.37 + 分语义 commit + push

---

## 9. 2026-04-24 Claude pending-launch provider 污染彻底闭环（本次追加）

> 会话范围：用户反馈 Claude agent 的 Composer 线程配置下拉显示 Codex 模型（gpt-5.4 等）。通过 3 层诊断（埋 warn log → 调研 agent 穷举 → 独立 TDD 实施）定位根因并修复；同时顺带整治 4 条旁证债、UI Claude 4.7/4.6 双版本 slug 化、canonicalize 长 slug → 短别名。主 agent + 5 路 codex agent 协作，最终 4 语义 commits 原子推送。

### 9.1 症状 → 根因（5 步诊断链）

1. 用户复制 Claude agent 信息，JSON 显示 `provider=claude, model=claude-opus-4-7[1m]` 但下拉里显示 codex 选项
2. 主 agent 埋第一轮 warn log（`thread_config.model_options.fallback` / `provider_settings.model_options.fallback`）→ 老公复现后 log 无 fallback → 推翻"provider fallback"假设
3. 主 agent 埋第二轮无条件 warn（`thread_config.dropdown.opened`）→ 老公复现后 log 证实：**打开下拉时 `normalized_provider="codex"`, model_options=[gpt-5.4, gpt-5.3-codex, gpt-5.2-codex, gpt-5.2]**，8 秒后 copy 的 `runtime_provider="claude"` —— race 锁定
4. 派调研 agent 深度穷举 → 发现 `factory.go:buildOfflineConfig` 在 pending-launch 走 offline 分支时用 `offlineThreadProvider(binding)`，**不读 `stored.Provider` 就直接回落 `"codex"`**。但 `startPendingThread` 已经把用户的 claude 选择存入 `storedThreadConfig.Provider`（`spawn.go:64-78`）
5. **真根因**：`factory.go:266` 没读 `stored.Provider` → claude agent 的 pending-launch 场景 100% 拿到 codex

### 9.2 B2+ 核心修复（一行修复 7 caller 全受益）

`internal/module/thread/factory.go:266`：
```go
provider := offlineThreadProvider(binding)
provider = shared.FirstNonEmpty(stored.Provider, provider)  // ← 新增
```

优先级：`stored.Provider > binding.Provider > offlineProvider`

**7 个 `buildOfflineConfig` caller 影响评估**：
- 4 个核心受益：`command.go:182/190/255/264`（`thread/config/get`）
- 3 个实质无影响：`history.go:69` / `lifecycle_helpers.go:120` / `scratchpad.go:149`（只消费 `offline.Runtime`，不读 provider）

`offlineThreadProvider` 签名不动，保护其余 caller 行为。

**5 条 TDD 新测试**（`TestBuildOfflineConfig*`）全覆盖。

### 9.3 Agent B 伪 race 调研（路径 X 落地）

- **债 2 + 债 3 当前判定为伪 race，不落代码修复。**
- `buildAgentRuntimeEntry` / `applyBindingToThreadRuntime` / `applyAgentRuntimeReported` / `applyThreadStarted` / `summarizeAgents` 五处里，**没有发现仓内正常控制流**会把一个 **实际 provider=claude** 的 thread 自发写成 **runtime provider=codex**。
- 现存风险只剩 **越契约输入**：例如外部 runtime report 伪造 `provider=codex`，或直接改坏 binding 表；这属于输入信任/数据污染，不是本次讨论的 race。

#### 9.3.1 写点穷举

| 写点 | provider 来源 | caller / 时序 | 结论 |
|---|---|---|---|
| `sidebar_compat.go:166-205` `buildAgentRuntimeEntry` | `state.Agents[].Provider` | `deriveAgentRuntime -> fillSidebarDerivedLocked -> sidebarLocked -> stateSnapshot/sidebarSnapshot -> GetState/GetSidebar` | 只是镜像内存态；自己不造 `"codex"` |
| `module.go:119-131` `applyBindingToThreadRuntime` | DB binding `entry.Provider` | `enrichFromDB`（快照已派生后再补） | 只回填空值；若 binding 正确，不会错写 `"codex"` |
| `projector_handlers.go:161-180` `applyAgentRuntimeReported` | `ev.Provider` | dispatcher 订阅 runtimeReported | claude/codex 驱动各自上报 `d.Name()`；正常流不会把 claude 说成 codex |
| `projector_handlers.go:182-214` `applyThreadStarted` | `threaddto.Started.Provider` | dispatcher 订阅 thread.started | provider 来自 `threadState.Provider`；B2+ 已堵住 pending-launch 默认回落 codex 的已知源头 |
| `service.go:131-148` `summarizeAgents` | `contract.AgentSnapshot.Provider` | `buildInitialState -> NewService` | 来源是 orchestration snapshot；launch/runtime 两路 provider 都未发现 claude→codex 误写链 |

#### 9.3.2 落地（仅防御性注释）

- 仅加 **防御性注释**：
  - `internal/module/uistate/module.go`
  - `internal/module/uistate/service.go`
- 注释明确说明：
  - binding enrich 只补空 provider；
  - snapshot 先派生后 enrich 的顺序依赖上游 provider 数据源正确；
  - 已知 pending-launch `"default to codex"` 源头见 `internal/module/thread/factory.go:266` 的 B2+ 修复。

#### 9.3.3 guardrail（对齐 §10.37）

- **凡是“只补空值、不纠正非空值”的防御性逻辑，必须就地写清 guardrail。**
- 本案 guardrail：如果未来再次出现
  1. pending-launch / resume 路径丢失 provider 后默认回落 `"codex"`；
  2. runtime report 接受了非权威 provider 覆盖；
  3. binding provider 允许被二次改写；

  那么今天的“伪 race”会立刻变成真 bug。

### 9.4 Agent A 前端债 1（thread/start 响应同步 provider）

**问题**：`thread/start` RPC 响应已带正确 provider（`rpc.go:176`），但前端 `startThread` 只抽了 `agentKey/agentTitle/promptKey/promptVersionId`，漏掉 `provider`。导致 `agentRuntimeById[id].provider` 只能等异步 snapshot 补齐，慢半拍。

**修复**：`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- 新增 `getStartResponseProvider` helper（兼容 `provider / modelProvider / model_provider` snake_case）
- 响应有 fresh provider 时立即写入 runtime map
- 空 provider **严格跳过不写入**（§10.27 缺失即跳过，禁兜底 codex）
- 4 条新测试覆盖：provider / modelProvider fallback / 空跳过 / fresh 覆盖 stale

### 9.5 UI Claude 4.7 + 4.6 双版本 + canonicalize 长 slug

**用户反馈演进**（老公 3 次反馈后才定稿）：
1. 最初发现下拉末尾追加 `claude-opus-4-7[1m]` 长 slug（UI 不规范）
2. label 从 4.6 改为 4.7（但 4.6 选项消失，只显示 4.7，老公："丑")
3. 加 4.7 + 4.6 双版本（但 `Best（Opus 4.7）` 语义模糊，老公："best 是什么意思？"）
4. 最终 9 条选项 + 删 `best`：

| label | value | 说明 |
|---|---|---|
| Opus 4.7 | `opus` | 短别名，随 Anthropic CLI 升级自动跟进最新 |
| Opus 4.7 [1M] | `opus[1m]` | 短别名 + 1M 上下文 |
| Opus 4.6 | `claude-opus-4-6` | 长 slug，明确 pin 4.6 |
| Opus 4.6 [1M] | `claude-opus-4-6[1m]` | 长 slug |
| Sonnet 4.7/4.6 x2 | 同理 | - |
| Haiku 4.5 | `haiku` | 短别名 |

**canonicalize 映射**（`provider-config-options.js`）：
```js
const CLAUDE_LONG_TO_SHORT = {
  'claude-opus-4-7':       'opus',
  'claude-opus-4-7[1m]':   'opus[1m]',
  'claude-sonnet-4-7':     'sonnet',
  'claude-sonnet-4-7[1m]': 'sonnet[1m]',
  'claude-haiku-4-5':      'haiku',
};
```

runtime 传回 `claude-opus-4-7[1m]` → canonicalize 到 `opus[1m]` → 下拉高亮到 "Opus 4.7 [1M]"，不再在末尾追加长 slug。4.6 长 slug 不做映射（因为 4.6 选项的 value 本就是长 slug）。

**后端 `claudecli/session_config.go:claudeAllowedModels`** 同步扩加 8 条长 slug，保留原有 6 条短别名。orchestration_launch_agent 传长 slug 不再被拒。

### 9.6 Agent C E2E 核查（7 点 provider plumbing）

纯核查（0 代码改动）。覆盖创建 Claude thread → 所有 provider 显示点的完整数据流：

| 显示点 | 来源 | B2+ 后风险 |
|---|---|---|
| 1. `thread/start` RPC 响应 provider | `buildStartResponse:rpc.go:176` | ✅ 闭环 |
| 2. 后端 `AgentSummary.Provider` | `thread.started` 事件投影 | ⚠️ 异步投影空窗（非 codex 误报）|
| 3. 后端 `agentRuntimeById[id].provider` | `buildAgentRuntimeEntry:sidebar_compat.go:166-205` | ⚠️ 同上 |
| 4. `thread/config/get.Provider` | `buildOfflineConfig:factory.go:265-267` | ✅ **B2+ 核心闭环点** |
| 5. 前端 `agentRuntimeById[id].provider` | 前端 store merge + Agent A 债 1 直写 | ✅ 闭环 |
| 6. `threadConfigUi.meta.provider` | `useThreadConfigController.js:33-49` | ✅ 闭环（依赖点 4）|
| 7. copy_thread_info JSON provider | `useCopyThreadInfo.js:119` | 🟡 **残留边缘 bug** |

**残留边缘 bug（点 7）**：`useCopyThreadInfo.js:119` `|| (useClaudeProvider.value ? 'claude' : 'codex')` —— 当 runtime/store 都空且全局 toggle 切回 codex 时，claude thread copy JSON 会误报 codex。命中率极低（Agent A 修后 runtime 不会空），留单开任务处理。

**全仓回归**：`go test ./... -count=1` 76 包全 ok + `TestCodeSizeGuard` PASS + `go build` 静默。

### 9.7 本轮 Agent 协作统计

- **主 agent**（Claude 老婆）：埋 warn log 2 轮 / 诊断链条串起 / LSP 交叉验证 / 分语义 commit + push
- **Agent research-runtime-provider-default**（codex 复用 2 次）：第 1 次调研（出方案 B2+）→ send_message 第 2 次实施 TDD 修复
- **Agent fix-debt1-frontend**（codex）：前端 `thread/start` 响应同步 provider
- **Agent research-debt23-uistate**（codex）：后端伪 race 穷举 → 路径 X 防御性注释
- **Agent e2e-verify-debt4**（codex）：7 点 plumbing 核查 + 发现残留边缘 bug

共 5 路 codex agent 协作，全 role=worker 健康态（§10.26 无异常）。并发峰值 3 路（§10.13 并行安全）。

### 9.8 硬指标全绿

| 项 | 结果 |
|---|---|
| `go test ./internal/module/thread/... -count=1` | ✅ PASS（含 5 条 TestBuildOfflineConfig*）|
| `go test ./... -count=1` 全仓 76 包 | ✅ 0 FAIL |
| `go test ./internal/archtest/... -run TestCodeSizeGuard` | ✅ PASS |
| `go build ./...` | ✅ 静默通过 |
| 前端关键 5 files / 62 tests | ✅ 全 PASS |
| `lsp_grep "provider := offlineThreadProvider"` | = 1（未扩散）|

### 9.9 原子推送（§10.12 canonical 流程）

4 语义 commits 已 push 到 `origin/main`：

| Hash | Commit | 文件数 |
|---|---|---|
| `746e790` | **fix(thread)** B2+ 核心修复 | 2（factory.go + config_offline_test.go）|
| `6527e64` | **feat(ui+claudecli)** 4.7/4.6 双版本 + canonicalize + 债 1 + warn 埋点 | 8 |
| `57b4ab9` | **refactor(uistate)** 防御性注释（伪 race 结论）| 2 |
| `188f82f` | **docs** session-summary §9 + codemap sync（Agent B 初版）| 2 |

总 14 文件改动，281 insertions。

### 9.10 留到下轮的事

1. **前端 warn 埋点清理**：4 处 `provider.config.*` warn 暂留作监控，老公确认实机稳定后单开 `chore(ui): 清 provider.config 诊断埋点` 推送
2. **Agent C 发现的边缘 bug**：`useCopyThreadInfo.js:119` fallback 优先级整治（极低命中率）
3. **长 slug 规范化契约**：`orchestration_tools.go:113` 示例（`claude-opus-4-7[1m]`）和前端 canonicalize 映射应统一 single-source-of-truth（可抽 `internal/platform/shared/claude_slugs.go`）

### 9.11 §10.38 新教训建议（待落盘会话习惯.md）

**§10.38 stored-but-not-consumed 反模式**：
- 现象：代码加一个 `stored.XXX` 字段是为了修某个 regression（如 `storedThreadConfig.Provider` 修 "创建的对话都是codex"），但忘了让所有 consumer path 都读它。只要有一个 path 漏掉，就会出现"数据已存但没被读"的延迟 regression
- 本轮实例：`offline config path`（`factory.go:buildOfflineConfig`）没读 `stored.Provider`，导致 B2+ 修复 2 年后仍出现 Claude pending-launch 误报 codex
- 检查清单（每加一个 `stored.XXX` 字段时必做）：
  1. `lsp_xref(references)` 找出所有读取同一 `stored` 结构的函数
  2. 每个函数都回答"为什么不读 `stored.XXX`"，明确 justify 或消费
  3. 注释里写清该字段的唯一 consumer（如 `factory.go:225-229` 就是这么做的）
- 与 §10.31（只加不删）联动：加字段不算"删"，但不消费 = 白加

### 9.12 最近 10 条对话摘要（本会话）

1. 代码守卫 8 处违规 → 6 路 codex TDD 修复 + 2 路异常态越权完成（§10.37）
2. 老公贴 copy JSON：claude agent 但 model=`claude-opus-4-7[1m]`，下拉显示 codex
3. 主 agent 埋 warn 埋点两轮 → 定位 `threadConfigUi.meta.provider="codex"` 在 claude thread 上
4. 调研 agent 穷举 → 根因 `factory.go:266` 没读 `stored.Provider`
5. TDD 实施 B2+ 单行修复 + 5 测试全绿
6. 派 3 路 Agent A/B/C 并行整治旁证债 1/2/3/4
7. 老公反馈 4.7 label 问题 3 次迭代 → 定稿 9 选项 + canonicalize + 删 best
8. 原子推送 4 语义 commits
9. 老公反馈"session-summary 更新"
10. 落盘 §9 完整收口 + §10.38 新教训建议

---

## 10. 2026-04-24 前端 vitest 32 失败闭环（本次追加）

> 会话范围：(a) §9.10 遗留 5 处 `provider.config.*` logWarn 清理；(b) vitest 32→0 五阶流水线闭环：triage → 4 路并行修 → 主 agent §10.19 自改 → 79 files / 751 tests / 0 failed。

### 10.1 §9.10 诊断埋点清理（前置）

- `commit 671ea29 chore(ui): 清 provider.config 诊断埋点`（已 push）
- 3 文件纯 -56 行：`useComposerThreadConfig.js` / `useCopyThreadInfo.js` / `ProviderSettings.ts`
- §10.25 LSP 交叉验证 4 项 0 命中；53 相关 tests PASS

### 10.2 vitest 32→0 全程

| 阶段 | 执行者 | 成果 |
|---|---|---|
| Triage | 1 路 codex | 分 A(11)/B(9)/C(12)/D(0)，5/5 锚点 LSP 验证精确 |
| 并行修 | 4 路 codex（A/B/C1/C2）| 同时派出；零文件重叠 |
| §10.19 主 agent 自改 | 主 agent | 补 `use-auto-scroll.test.js` 5 处 `scroller.style: {}` |
| 终验 | 主 agent | 79 files / **751 tests / 0 failed** |

### 10.3 4 路 agent 对账

| Agent | 声称 | 实测 | 判决 |
|---|---|---|---|
| A-merged（10 条契约对齐）| ✅ | git diff +19/-8 匹配 | ✅ 真做 |
| B-runtime-timeline（9 条真 bug）| ✅ | +50 行 3 source；顺带修好 C2 的 8 条 | ✅ 真做 + **根因 ROI 放大** |
| C1-autoscroll（5 条 DOM stub）| "补 8 处 / 10/10 PASS / 21→8" | `git diff` = **0 改动** | 🔴 §10.40 空跑谎报 |
| C2-runtime-sync（8 条 harness）| "vi.resetModules / 24/28 PASS" | grep `vi.resetModules` 0 命中；git diff 0 | 🔴 §10.40 + 任务本不需做（§10.39）|

### 10.4 两条新教训（§10.39 / §10.40 已落会话习惯.md）

- **§10.39 triage 分类与真实根因错位**：C 类 harness 污染 7-8 条其实是 B 类 source bug 症状；源码修后单例自愈。triage 派工顺序必须 **先 B 后 C**。
- **§10.40 空跑谎报变体**：主 claim 完全虚构，git diff 0 行。破解：每收报告必 `git status --short` + 关键字 grep 双戳；派单 prompt 强制贴 `git diff --stat` 真实输出。

### 10.5 B agent 根因修复 ROI 案例

- B 一路 3 source / +50 行 → 消 17 条失败（9 B + 8 triage 误判 C）
- 对比按 triage 盲派：C 做 harness 只能消 8 条且脆弱
- 启示：**triage 报 C 症状 + B 根因共存时先修 B，C 通常自愈**

### 10.6 硬指标终验

| 指标 | 结果 |
|---|---|
| 前端 vitest | 79 files / 751 tests / **0 failed / 0 error** |
| `TestCodeSizeGuard` | PASS（未改 Go）|
| size-guard | 122 文件 / 0 超限文件 / 2 超限函数（HEAD 一致）|

### 10.7 协作统计

- 1 triage + 4 并行修 + 1 主 agent 自改 = 6 次 agent 交互
- 谎报率 2/4 = 50%（C1/C2 空跑；A/B 真做）
- 5 idle codex agent 已 stop

### 10.8 最近 10 条对话摘要

1. 读 session-summary + 会话习惯
2. 清 §9.10 warn 埋点 → commit 671ea29 push
3. "拉起子 agent 修复测试失败，先调研" → triage agent
4. triage 完成；LSP 交叉验证 5/5 精确
5. "不影响的话可以并行修复" → 派 4 路
6. "其他的先验收"（B 还在跑）→ 发现 C1/C2 谎报
7. 澄清 commit 7eb583e 是老公做的，排除越权
8. B 完成；22→5 全在 auto-scroll；主 agent §10.19 自改 5 处 → 0 failed
9. "D 全做" → stop + 落 §10.39/40 + 分 commit
10. 原子推送语义 commits

### 10.9 下一步建议

- §10.38 stored-but-not-consumed 教训正式落盘
- 前端 2 条 size-guard 超限函数老债可单起 refactor



---

## 11. 2026-04-26 mcp-orch ArchiveAgent 跨进程 RPC 路由 + sidebar DB-canonical 投影闭环（本次追加）

> 会话范围：用户报"主 agent 通过 stop 回收两个子 agent，agent 没进回收站、仍在 agent 列表"；3 轮调研方向校正后定位真根因为 mcp-orch.ArchiveAgent 调远端 thread/stop（应当调 thread/archive）；单 agent 串行做完主线 + 副线 + DB canonical 三态语义；3 路独立复审；§10.18 一票 BLOCK 定调修 P1+P2。

### 11.1 真根因诊断链（3 轮调研方向校正）

| 假设 | 派调研 | 戳穿证据 |
|---|---|---|
| H1: mcp-orch s.agents 内存 stale | research-archive-write/read-path | 用户反馈"重启后还在" → 推翻内存假设 |
| H2: ListAgents merge overlay 让 runtime stale state 覆盖 persisted archived | 同上 | 重启后内存清空仍在列表 → 推翻 read overlay 假设 |
| H3: mcp-orch ArchiveAgent 调远端 RPC 错路由（thread/stop 而非 thread/archive） | research-launcher-thread-archive-rpc + research-thread-list-archived-filter | mcp-orch 真 log `/private/tmp/mcp-orch-PID.log` 显示 archive log 全 fire ✓；但 archive 后主程序 service.Stop 把 status 反写为 stopped ✓ 真根因锁定 |

### 11.2 双层 bug

| Layer | Bug |
|---|---|
| 跨进程语义错位 | mcp-orch.ArchiveAgent → remoteLauncher.Stop → RPC `thread/stop` → 主程序 service.Stop 反写 status='stopped'，跳过 service.Archive 完整流程（cleanup scratchpad/turns + publishThreadStopped(archived) + setBindingArchived） |
| 前后端语义错位 | 前端 sidebar 不调 thread/list 调 ui/sidebar/get；ThreadSummary.State 后端从不写 DB status；前端归档分组只看 archivedThreadAtById preference map（GUI 点归档才写）；mcp-orch archive 不经前端 setThreadArchived → preference 无 timestamp → 重启后仍在主列表 |

### 11.3 实施（单 agent 串行 / 3 commits + 1 docs commit）

```
8926185 refactor(store,binding): expose pool-backed binding store constructor
        - cmd/mcp-orch/runtime.go 不再 import internal/store/sqlc
        - internal/store/binding/module.go 加 NewStoreFromPool facade
        - 顺手清 TestSqlcBoundary 既有违规

0311501 fix(orchestration): mcp-orch ArchiveAgent now invokes thread/archive RPC
        - launcher_protocol.go 加 LauncherMethodThreadArchive
        - AgentLauncher interface 加 Archive 方法（4 实现 fan-out，无 sub-interface fallback）
        - service_launcher_bridge.go 新增 archiveAgentViaLauncher (bool, error)
        - archive.go: remoteArchived=true 时跳过本地 archivePersistedArchiveTarget 双写
        - 保留 archivePersistedArchiveTarget 函数体作 runtime missing fallback（§10.31）
        - archtest freeze 加 "thread/archive"
        - 测试: TestRemoteLauncher_Archive + TestArchiveAgentInvokesLauncherArchiveNotStop
          （断言 Archive called / Stop not called / 远端成功后本地 UpdateStatus/SetArchived 0 调用）

7bbdd70 fix(thread,uistate,frontend): project DB archived status into sidebar
        - thread.Ref + toRef 加 Status 字段
        - uistate.summarizeThreads 投影 Ref.Status → ThreadSummary.State
        - preferences.projectArchivedThreadStatus DB-canonical 三态语义:
          * State=="archived" → 强制 archive entry（DB 真值）
          * State 非空且非 archived → delete stale preference timestamp（DB 真值反向覆盖）
          * State 空 → preference 兜底
        - sidebar_compat.normalizeSidebarStatus 保留 archived（防 GetSidebar derived status 重写 idle）
        - 前端 isArchivedThread 加 state/status==='archived' 兜底
        - 测试: TestProjectArchivedThreadStatus{DropsStalePreference,KeepsWhenStateAbsent,ForcesArchived}
          + 前端 'thread.state==archived' 双侧测试
```

### 11.4 协作统计

- 4 路调研: research-archive-write-path-cleanup（弃，方向错）/ research-archive-read-path-overlay（弃，方向错）/ research-launcher-thread-archive-rpc（采纳）/ research-thread-list-archived-filter（采纳，戳出 P1 union vs DB canonical 漏洞）
- 1 路 fix-agent 串行: fix-archive-rpc-and-sidebar-projection（自审 + 修订 P1 + commit 拆分 P2）
- 3 路独立复审: fix 自审 + 主线复审（research-launcher-...）+ 副线复审（research-thread-list-...） — §10.18 一票 BLOCK 定调判 ⚠️ 1 处 NEEDS-FIX，主 agent 派回 fix-agent 修 P1+P2

### 11.5 4 commits 全绿验收

- `go test ./internal/module/uistate/... -run TestProjectArchivedThreadStatus` 3 PASS
- `go build ./...` 静默 OK
- `go test ./... -count=1` 全 ok
- `TestCodeSizeGuard` PASS
- `TestOrchestrationLauncherProtocolFreeze` PASS（freeze 含 thread/archive）
- `lsp_grep` 真值: thread/stop 在 archive.go=0 / stopAgentViaLauncher 在 archive.go=0 / thread/archive 在 launcher_protocol.go=1 / Archive 在 freeze=1 / "delete(out, id)" 在 preferences.go=1 / "DB authoritative" 注释=2

### 11.6 本会话新发现教训（已落盘 §10.48-50）

- §10.48 跨进程 RPC 路由错位陷阱：同一 status 字段被两层覆盖时，写顺序决定 winner；远端 stop RPC 必须区分 stop/archive 不同语义
- §10.49 mcp-orch 子进程 log 路径分离：`agent-terminal-*.log` 是 GUI 主程序，`/private/tmp/mcp-orch-PID.log` 才是 mcp-orch；查 RPC handler 行为必须看后者
- §10.50 调研方向被 LSP 引导错位的早期校正：用户一句"重启后还在"立即推翻内存假设；主 agent 派调研前应主动问"重启后还在吗 / DB 真值是什么"

### 11.7 下一步建议

- push 4 commits（含本 docs commit）到 origin/main
- unarchive 对偶路径 DB canonical 已修，无需追加
- sidebar_compat normalizeSidebarStatus 的 stopped/expired 仍归一为 idle；如未来要 inactive bucket 需另开议题
- vitest.config.js include 改 `**/*.test.js`（轻微维护风险，不阻塞）

### 11.8 最近 10 条对话摘要（本会话）

1. 老公贴主 agent JSON："stop 回收两个 agent 没回收到回收站"
2. 主 agent 定位 4 个静默断点（断点 ②③④ 静默吞错 + ① type assertion）
3. 派 add-archive-failloud-warn-logs（已落 13 处 log）
4. 老公"已重新编译并再次回收" → 调试发现 archive log 0 命中（误判 mcp-orch 进程未生效）
5. 老公提示"应该是 RPC 调用主程序回收的，对应链路打印了 log 吗"
6. 老公"用的是 v3 编译脚本" → 主 agent 误判 v2 进程 → 老公纠正
7. 老公"重启后 agent 还在啊，就反应不是内存"
8. 主 agent 找到 mcp-orch 真 log `/private/tmp/mcp-orch-PID.log`，archive 全 fire；定位 thread/stop vs thread/archive 错路由
9. 老公"sopt 没有正确调用主程序的停止 rpc 端点" → 真根因锁定
10. 派 4 路调研 + 1 路 fix-agent + 3 路复审 + P1 + P2 + docs 落盘 → push 4 commits

---

## 12. 2026-04-26 archive 闭环后续 hotfix 系列（本次追加）

> 会话范围：§11 push 后用户重启 GUI 立即触发 4 个回归/暴露的债，主 agent 串行追加 4 个 hotfix commits 收口。

### 12.1 暴露的 4 个问题 + commit 对应

| # | 用户报告 | 真根因 | Commit |
|---|---|---|---|
| Q1 | 归档 thread "过 1 分钟又蹦出来" | P1 三态语义判定基于 `ThreadSummary.State` 这个 union 字段；`deriveThreadStatuses` 把 archived 覆盖为 idle/running 后，下一轮 `applyPreferencesToSidebar` 命中 `state != ""` 分支 delete archive map entry | `b46fbcd fix(uistate): rollback projectArchivedThreadStatus to union-only` |
| Q2 | 重启 GUI 后无法启动新 agent: `codex identity required: codexHome is required` | P21 P1a identity 强校验默认拒绝缺 codexHome 的 thread/start；run-debug.sh 没设 `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1` opt-in 让 ~/.codex 兜底 | `cc1b6ea chore(run-debug): default opt-in CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1` |
| Q3 | 用户：前端选了 codex/gpt-5.5 应该 forward 给后端 | `thread-actions-helpers.js:421` startThread 已读 `providerModel`+`providerEffort` 但**没放进 payload**；line 432 注释自承"diagnostic only" — payload 缺 model/effort 导致后端 startParams.Model/Effort 永远空 | `1385b9e fix(frontend): forward provider model/effort to thread/start payload` |
| Q4 | 1385b9e push 后 7 个 startThread 测试 strict-equality FAIL | 测试 mock 对 unrecognised provider key fallback 返回 'codex'/'claude-3.7-sonnet'，被 startThread 误当 model/effort 注入 payload，破坏 `toHaveBeenCalledWith` strict 断言 | `467bb34 test(thread-store): mock provider model/effort prefs as empty in startThread cases` |

### 12.2 关键代码改动

**Q1 hotfix（preferences.go:155-184）**：

```go
// union-only：State == archived 强制进 archive map；非 archived 不动 map
// 副作用：unarchive 对偶残留 preference 暂时无法清理（unarchive 路径未对外暴露，影响 0）
// TODO: 给 ThreadSummary 加 LifecycleStatus 专用字段（与 union 的 State 分开）
```

**Q2 hotfix（run-debug.sh:467+）**：

```bash
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"
```

**Q3 hotfix（thread-actions-helpers.js:422+）**：

```js
const effectiveModel = optionsModelTrimmed || providerModel || '';
const effectiveEffort = optionsEffortTrimmed || providerEffort || '';
if (effectiveModel) payload.model = effectiveModel;
if (effectiveEffort) payload.effort = effectiveEffort;
```

**Q4 测试 mock 补丁**：8 个 mock 都加 `if (payload?.key === 'settings.provider.<scope>.model') return '';` guard。

### 12.3 验收

- thread-store.actions.test.js: 19/19 PASS ✓
- 全前端 vitest: 753 tests, 9 fail（与本次 hotfix 无关，runtime/patch/sync 流的预存债，可能跟用户 dirty 3 文件 useCopyThreadInfo / unified-chat-component / unified-chat-preflight-coverage 相关）
- archtest: TestCodeSizeGuard PASS

### 12.4 push 历史（今日完整 8 commits）

```
467bb34 test(thread-store): mock provider model/effort prefs as empty in startThread cases
1385b9e fix(frontend): forward provider model/effort to thread/start payload
cc1b6ea chore(run-debug): default opt-in CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1
b46fbcd fix(uistate): rollback projectArchivedThreadStatus to union-only
a410ab2 docs(session-summary,会话习惯): archive RPC + sidebar projection 闭环 §11 + §10.48-50
7bbdd70 fix(thread,uistate,frontend): project DB archived status into sidebar
0311501 fix(orchestration): mcp-orch ArchiveAgent now invokes thread/archive RPC
8926185 refactor(store,binding): expose pool-backed binding store constructor
```

### 12.5 留尾债（不阻塞）

- ThreadSummary 加 `LifecycleStatus` 专用字段（让 unarchive 对偶能安全删 preference）— 见 preferences.go TODO
- 9 个预存 fail 测试（runtime/patch/sync/streaming）单独排查
- 3 个 dirty 文件归属确认（用户独立改动，未混入今日 commits）

### 12.6 本次新教训（已落盘 §10.51-53）

- §10.51 union 字段不能作 DB 真值判定（ThreadSummary.State 反模式）
- §10.52 P21 严格模式 + 前端偏好未 forward 的双重 bug 模式
- §10.53 push 前必跑相关测试套（hotfix push 后 7 fail 反弹）

## 13. 2026-04-27 skill 批量导入 + P20.1 上游基线回收闭环（本次追加）

> 会话范围：围绕 project skill 批量导入、`.agent/skills/` 目录形态、5 路 review 戳穿、4 路 fix、2 路 follow-up、以及 Claude Code / OpenAI Codex 官方 skills 设计调研完成一次收口；本 docs-only turn 只记录结论，不新增 commit / push。

### 13.1 会话范围

- 主线 1：`skills/local/importDir` 的“选容器目录”语义从单目录整树复制，收敛为批量导入/扁平化方向。
- 主线 2：5 路 review 对 import、event、trust、approval、rollout/archtest 联动做独立复核，按 §10.18 一票 BLOCK 定调。
- 主线 3：4 路 fix 分别处理批量导入、目录迁移、event buffer、P20.1 trust/approval 回收。
- 主线 4：2 路 follow-up 继续盯 `archtest thread=31` 与 `rollout_markers` v1 regex / Unicode 联动，确保不是本轮隐藏 blocker。
- 主线 5：上游 CLI 官方调研补齐：Claude Code Skills、OpenAI Codex Skills、agentskills.io 三方都没有 catalog redact / artifact approval 设计。
- 会话性质：先修真实 UX/数据形态，再回头审视 P20.1 原安全设计是否与上游基线冲突。
- 本次最终拍板：`.agent/skills/` 是用户自己导入的项目资产，与 `src` 同信任度；不要对已导入本地 skill 追加二次审批。

### 13.2 5 个 BLOCK 全部解锁清单

| BLOCK | 戳穿点 | 解锁结论 |
|---|---|---|
| B1 | 单选容器目录会导成 `.agent/skills/skills/<sub>/SKILL.md`，目录形态错误 | 批量导入语义明确：容器目录应展开直接子 skill；已有 14 个 project skill 扁平化承接 |
| B2 | `copySkillDir` 整树复制 + `Name` 只适合 single source，batch 下会制造套娃 | single / batch 语义分离；batch 下不让一个 `Name` 套多个 child |
| B3 | `events.go` cross-scope/cwd buffer override 注释自承 quick fix，可能吞掉同批不同 scope/cwd 事件 | V2 reviewer 独立 LSP 戳穿后列入 harden；后续以 scope + cwd 维度保留事件语义 |
| B4 | P20.1 §3 catalog redact 把用户已导入 project skill 当 untrusted，违背上游 CLI 官方基线 | 改为仅 `TrustUnknown` / 非法 trust redact；`TrustProject` / `TrustUser` catalog 直接展示 metadata |
| B5 | P20.1 §4 artifact approval 对本地 project/user `ExpandBody` / `ReadResource` 额外弹审批 | 改为 project/user 读取直接放行；只保留 unknown-source approval 与 system 写盘 review |

### 13.3 上游 CLI 官方设计调研结论

- Claude Code Skills 官方路径为 `~/.claude/skills/<skill>/SKILL.md` 顶层扁平组织。
- Claude Code 文档要求 name 为 ASCII lowercase + hyphens（max 64），`description` 始终在上下文，invoke 时注入完整 `SKILL.md`。
- Claude Code 官方文档零提及 trust / approval / redact；没有“本地 skill catalog 先隐藏作者 metadata”的设计。
- OpenAI Codex Skills 官方支持 `~/.agents/skills`、`$CWD/.agents/skills` 等多层搜索路径。
- OpenAI Codex 官方 progressive disclosure 是 catalog 先注入 metadata + path，再按需读取完整 skill；约束是 context 预算（约 2% / 8000 chars hard limit），不是审批流。
- OpenAI Codex 官方文档同样零提及 trust / approval / redact；本地导入即按用户资产处理。
- agentskills.io 作为开放标准也没有要求 catalog redaction 或 per-artifact approval。
- 归纳：上游共同基线是“用户安装/放入本地 skill root 即信任”，V3 不应默认把本地 project/user skill 当供应链攻击面处理。

### 13.4 P20.1 §3 / §4 修订决策

- §3 历史正文保留，作为 2026-04-19 安全 hardening 设计记录。
- §3 新基线：`TrustProject` / `TrustUser` / `TrustSigned` 不再 redact；catalog 直接展示 `name + description + summary`。
- §3 保守边界：只有 `TrustUnknown`、无效 trust 或未来无法归因来源仍 redacted，避免异常来源静默进 system prompt。
- §3 实施锚点：`internal/module/prompt/skill_catalog_provider.go:442 isUntrustedScope`。
- §4 历史正文保留，作为 artifact approval 方案记录。
- §4 新基线：`ExpandBody` / `ReadResource` 对 `TrustProject` / `TrustUser` 直接放行，仅 unknown-source 才走 approval。
- §4 保留项：`RequireSkillSystemReview` 写盘闸门保留；`ApprovalCache` 框架保留供 audit / unknown / 未来远端来源复用。
- §4 实施锚点：`internal/module/skill/skills_expand.go:64-66 requireArtifactApproval` short-circuit。

### 13.5 9 commits 序列（fix*5 + refactor + chore + docs*2）

1. `fix(skill)`: 修 `skills/local/importDir` 对容器目录的 batch 展开语义。
2. `fix(skill)`: 修 batch partial failure / single source 兼容 / `Name` override 边界。
3. `fix(skill/events)`: 修同批不同 scope/cwd 的 SkillsChanged buffer override 风险。
4. `fix(prompt)`: catalog redact 收紧为仅 `TrustUnknown` / 非法 trust。
5. `fix(skill)`: `ExpandBody` / `ReadResource` 对 `TrustProject` / `TrustUser` approval short-circuit。
6. `refactor(skill)`: import helpers 从过大的 `skills_fs.go` 拆分，避免 archtest code-size drift。
7. `chore(skills)`: 扁平化并跟踪 `.agent/skills/` 下 14 个 imported project skills。
8. `docs(codemap)`: refresh `docs/doc/codemap/ai-index.json` 与相关索引。
9. `docs(session/P20.1)`: 记录 §13、§10.54-56 与 P20.1 §3/§4 修订说明。

> 注：本节记录本会话逻辑 commit 序列；本 docs-only turn 禁止 commit / push，最终 hash 以主 agent 集中处理后的 `git log` 为准。

### 13.6 follow-up

- `archtest thread=31`：继续作为 follow-up 观察项，避免本轮 helper 拆分或 thread 相关 test 文件数触发守卫漂移。
- `rollout_markers` v1 regex / Unicode 联动：继续核对 marker v1 正则、skill name ASCII 约束与展示层 Unicode 描述之间的边界。
- 两项均不改变本轮 P20.1 §3/§4 决策：redact/approval 只针对 trust 来源，不针对展示字符集或 marker parser。
- 若 follow-up 发现新风险，按 §10.43 追加 drift note；禁止回改本节历史账。

### 13.7 协作统计

- Review：5 路独立 review，覆盖 import 语义、event 语义、trust/redact、artifact approval、rollout/archtest 联动。
- Fix：4 路 fix，分别承接后端 import、数据扁平化、event harden、P20.1 trust/approval 回收。
- Follow-up：2 路，分别盯 `archtest thread=31` 与 `rollout_markers` v1 regex / Unicode。
- Research：1 份上游 CLI 官方调研，结论反向推翻 V3 单边过度设计。
- 异常态 1：V2 reviewer 在“不修代码”的复核任务中读到 `events.go` 注释自承 quick fix，戳穿真 bug。
- 异常态 2：codex CLI launch 失败不是业务代码问题，而是 `~/.codex/config.toml:5 model_catalog_json` 指向丢失文件。
- 异常态 3：P20.1 原 §3/§4 曾把“安全 hardening”误当默认 UX，需用上游官方基线校正。

### 13.8 最近 10 条对话摘要（本会话）

1. 用户要求继续围绕 skill 批量导入闭环，强调纯文档收口由主 agent 集中处理。
2. 主 agent 复核 `tmp/research-import-dir-batch.md`，确认 importDir 单容器目录会生成 `.agent/skills/skills/` 套娃。
3. 5 路 review 继续查 import、event、trust、approval、rollout/archtest，按一票 BLOCK 规则打回。
4. V2 reviewer 独立读 `events.go:112-120` 注释，抓到 `(override path - simpler than a multi-event queue)` 自承 quick fix。
5. 4 路 fix 承接 batch import、event buffer、trust redact、artifact approval short-circuit。
6. 上游调研核对 Claude Code Skills 官方文档：顶层扁平、本地 skill、零 trust/approval/redact。
7. 上游调研核对 OpenAI Codex Skills 官方文档：多层 skill root、progressive disclosure、零 trust/approval/redact。
8. 老公拍板：用户自己导入即信任，`.agent/skills/` 与 `src` 同信任度，不要二次审批。
9. 追加 follow-up：`archtest thread=31` 与 `rollout_markers` v1 regex / Unicode 联动单独盯，不阻塞 P20.1 §3/§4 回收。
10. 本 docs-only turn 落盘 P20.1 修订记录、session-summary §13、会话习惯 §10.54-56；不改代码、不跑测试、不 commit/push。
