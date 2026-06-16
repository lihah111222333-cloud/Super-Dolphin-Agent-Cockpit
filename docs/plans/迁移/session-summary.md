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
| P20.13 | 🟡 已实施待终验 | commit `7a1f49c`，2026-04-24 落入生产 `skill/expand` approval 链；文档修订：`docs/plans/迁移/p20/p20.13-approval-cache-wiring.md` 已同步 NEEDS-DOC-FIX 3 处 |
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

### §7.5.1 POST-R10 status overlay（2026-04-25 HEAD drift）

| 债 # | R10 原文 | HEAD 2026-04-25 状态 | 证据 |
|---|---|---|---|
| 1 | archtest 3 live failure | ✅ 已销账 | `TestDependencyDirection|TestTimeoutLocality|TestCodeSizeGuard` 当前 PASS（C2/D1 双独立验证） |
| 2 | toolbridge handler.go:136-160 回退 PersistentSubagentDefault | 🟡 部分 | 缺 runtime → fail-closed（`handler.go:177-191`）；runtime present 但 flag 缺仍 fallback cfg（Z-B NEEDS-FIX 开放） |
| 3 | waitDreamTask test-only | ✅ 已销账 | `drainDreamTask` prod caller（`module.go:455`；历史报告写 `494` 属行号漂移）|
| 4 | NewRelevantMemoryFinder bridge 壳 | ✅ 已销账 | `retrieval/prefetch.go:52` prod caller |
| 5 | TeamSyncService.Pull/Push test-only | ✅ 公开 API 收敛 | `pullLocked`/`pushLocked`/`pushLocalChanges` 有 prod caller |
| 6 | ErrThreadRuntimeRequired/ErrMissingCWD 0 命中 | ✅ 已销账 | `ErrThreadRuntimeRequired` 13 命中 / `ErrMissingCWD` 42 命中 |
| 7 | config.go:39 PersistentSubagentDefault=true | ✅ 已销账 | `config.go:54` 默认 false，有测试 |
| 8 | desktop pre-drain + watchFXShutdown 非对称 | 🔲 未修 | 仍是 `context.Background` + 非 root ctx |
| 9 | registerMemoryHooks.OnStop 不 wait/drain | ✅ 已销账 | `drainMemoryHooks` 已 drain scheduler/nested/teamSync/dream |
| 10 | docs/契约/* runner.actors vs group:"runners" | 🔲 未修 | 仍是文档契约债 |

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

## 8. 2026-04-25 P20/P21/P22 闭环 + P22.1 子任务成形（本次追加）

> 会话范围：(a) 20 路 codex 调研 P20/P21/P22 完成度；(b) 12 BLOCK + 12 NEEDS-FIX + 5 文档漂移裁决（Z 独立终审 OVERTURN/UPGRADE 各 1 条）；(c) 6 路 F agent 实施 + 8 commit push origin/main；(d) 4 路 G agent 修遗留（N-10 生产接线 / Z-B toolbridge flag fail-closed / P23 规划 / workspace 测试荒岛）；(e) M1 P23 → P22.1 归属迁移；(f) 4 路 GC 第二轮 commit；(g) P22.1 文档 R1-R4 迭代闭环（W×4 / J / E1 / X×4 / Y×4 最终验收）。

### 8.1 第一批 commit（已 push origin/main）

| agent/范围 | commit | subject |
|---|---:|---|
| F5 phase1 | `96cd28b` | `phase1 dashboard owns insight RPC` |
| F5 phase2 | `f3b8a15` | `phase2 align cron timers recovery leases` |
| F5 phase3 | `f884efe` | `phase3 route stop exits through monitor` |
| F5 patch | `6e66b41` | `patch drain stop exit monitor handling` |
| F1 | `2ff69c9` | `fix(skill/p20.1+p21-p0): 补齐 artifact approval gate 与 system scope review` |
| F2/F4 | `6440092` | `fix(prompt+frontend/p20.1+p20.15): 默认值、fallback 与 CWD` |
| F3 | `dcc8dcd` | `fix(codexapp+thread/p21-p1a): identity 与 runtime fail-closed` |
| F6 | `1e74c3d` | `fix(notify/p21-p2): 修正 mention 抑制顺序 + signed URL 日志脱敏` |

### 8.2 第二批 commit（G1/G2/G4/M1，GC 第二轮执行）

| agent/范围 | commit | subject |
|---|---:|---|
| G1 / N-10 生产接线 | `c75a51f` | `feat(turn/obs): 接线 Canonical Contract 与 raw dedupe` |
| G2 / Z-B toolbridge flag | `80d97b6` | `fix(toolbridge): persistent subagent flag fail-closed` |
| G4 / workspace 测试荒岛 | `343dabc` | `test(workspace): 补齐 handler 级测试覆盖荒岛 6 条` |
| M1 / P23 归属迁移 | `8269070` | `docs(p22.1): P23 规划文档归属为 P22.1 架构债子任务` |

> GC 第二轮已见上述 4 个 commit 落在 `git log --oneline -20`；另有 `95aab5a fix(prompt): update e2e memory-section assertion to combined-mode order` 为 prompt e2e assertion follow-up，未计入 G1/G2/G4/M1 四项表。

### 8.3 P22.1 架构债子任务归属（P22 R10 deferred 遗留）

- 归属来源：`JUDGEMENT_R8_QA.md` §R10.6 + `JUDGEMENT_R8_QC.md` §7 + §10.30 三层分工铁律
- 4 份主体 + `JUDGEMENT.md`（R1-R4 仲裁轨迹）+ `R2_CORRECTIONS.md` 落盘
- §10.31 全历史保留：`JUDGEMENT.md` 行 1-386 SHA256 = `8dc1a2a3d8b801975a9697c82b5ada4c5cf6c9b9ca0146229cc13edc7d1a9cce`
- §2.2.1 Phase 0 file-level write-set 冻结已落（Y3 补项）
- 最终判定：🟢 合入就绪，可派 Phase 0 单 owner 实施 agent

### 8.4 遗留代码债（本会话未修，作 follow-up）

- P22.1 Phase 0/1/2/3 代码层 11 违例修复（文档 ready，代码 0）
- §10.30 全仓 11 处违例（即 P22.1 F-1~F-11，同一批）
- `runner.actors` vs `group:"runners"` 契约命名债（`docs/契约/*` 另起 lane）

### 8.5 Agent 使用统计

- 本会话累计约 **49 路 agent**：20 路 P20/P21/P22 调研 + 1 路 Z 独立终审 + 6 路 F 实施 + 4 路 G 遗留修复 + 4 路 GC 第二轮执行 + 14 路 P22.1 文档 W/J/E/X/Y 迭代。
- 多轮迭代：P22 裁决/实施 2 批；P22.1 文档 R1→R2→R3→R4 + Y 最终验收；R2/R3/R4 均含销账复核而非重扫。
- OVERTURN/UPGRADE/REINSTATE：Z 独立终审明确 OVERTURN 1 条、UPGRADE 1 条；P22.1 W 轮保留 REINSTATE 能力并用于独立权重，不因 E 反驳自动撤销。

### 8.6 子 Agent 提示词模式（本会话治成）

- §10.16 显眼标签：首行写明“第 N 轮：修订后复审（只审不改）”，防止 agent 重跑上一轮或改文档。
- 5 级分类：`BLOCK / NEEDS-FIX / TRUE-BUT-DEFERRED / DOC-DRIFT / PASS` 分层，避免把 HEAD 漂移误判为当前阻塞。
- SHA 硬锁：每轮仲裁前记录历史前缀 SHA256；新增 §R(N+1) 只能末尾追加，追加后复算旧行范围。
- 独立性声明：终审/复核 agent 必须说明未参与前轮、保留一票否决权与 REINSTATE 权重。
- 防 §10.40 空跑：派单与收报告均要求 before/after diff、`git diff --stat`、关键 grep/xref/LSP 证据；空 report 但 state=idle 需按 §10.45 区分 orchestration 异常与 0 改动谎报。

### 8.7 下一步建议

- 派 Phase 0 单 owner 实施 agent（按 `docs/plans/迁移/p22/p22.1/DAG.md` §2.2.1 精确 write-set）
- 或先 commit + push P22.1 文档 + §R6 + §2.2.1 修订


### 8.4.1 P22.1 HEAD `a81554c` implementation overlay（2026-04-25，第 6 轮文档一致性修复）

> 本节按 §10.31 只加不删追加；`§8.4` 行 708-712 的“代码 0”是 **2026-04-25 之前快照**，保留为历史叙事，不再代表 HEAD。当前核验基线为 HEAD `a81554c`；P22.1 实施链按本轮交接固定为 `25a37ad` → `f737e45` → `17b5ce7` → `dfe12e6` → `b386217` → `a9a018e` → `a81554c`。

| Phase / 节点 | HEAD `a81554c` 实施状态 | 销账明细 |
|---|---|---|
| Phase 0 / P0A-P0C | ✅ 大部完成 | `25a37ad` 起引入 BusModule `bus.subscribers` contract、RunnerModule adapter contract 与 P22.1 archtest skeleton；后续 `f737e45`/`17b5ce7`/`dfe12e6`/`b386217`/`a9a018e`/`a81554c` 迭代 hardening。 |
| Phase 1 / P1A-P1B | ✅ 已完成 | F-1 root shutdown ordering 已从历史 drain→cancel 反转为 cancel→RunGroup wait→drain；F-2 `watchFXShutdown` 已改为 owner ctx 边界并进入 session-private allowlist。 |
| Phase 2 / P2A-P2F | ✅ 大部完成 | F-3 memory、F-4 thread、F-5 cachekeepalive、F-6 hooks、F-7 rpc、F-8 mcpcontrol、F-9 toolbridge、F-10 insight、F-11 observation 均已有 BusModule subscriber spec / RunnerModule adapter 迁移证据；剩余争议由 Audit-A/B/C 的 cross-file gap 复核继续收口。 |
| Phase 3 / P3A-P3B | 🟡 部分完成 | session-private allowlist 已落到 HEAD `a81554c`；gate hardening 仍有 3 处 NEEDS-FIX 待 Audit-A/B/C 修复，包括 cron+uistate cross-file gap、BusSubscriberGroup 命名/覆盖、ShutdownOrdering hybrid 充分性。 |

**HEAD `a81554c` 当前遗留 follow-up**：`runner.actors` vs `group:"runners"` 仍是 `docs/契约/*` 命名债；P22.1 不在本轮修改 `docs/契约/*`。`TestMemoryRulesInjectIntoPrompt` prompt regression 与旧 `events_test.go:53` race 锚点归 pre-existing/follow-up，不作为本 P22.1 文档 overlay 的代码阻塞项。

### 8.4.2 P22+P22.1 HEAD `5d6a93c` Round-3 BLOCK 收口 overlay（2026-04-25）

> 本节按 §10.31 只加不删追加；§8.4.1 的 HEAD `a81554c` 为第 6 轮历史 overlay，保留不改。当前 Round-3 修复基线为 HEAD `5d6a93c`，本轮只追加代码/文档收口说明，禁止重写历史叙事。

- `internal/app/runner.go` root `BindRuntime.OnStop` 已修正为 `cancel → waitForRuntimeDone(done, ctx) → drainRuntimeBeforeStop(ctx, p)`；§10.30 要求的 `cancel → run.Group 全退 → resource drain/close` 在本轮成为代码真状态。
- `internal/app/app.go` desktop pre-drain helper 已确认/调整为 `WaitRuntimeDone` 先于 `DrainRuntime`，避免 desktop shutdown 分支保留旧的 drain-before-wait 语义。
- 本轮同步补强 `TestShutdownOrdering` AST gate、`TestBindRuntimeWaitsRunGroupBeforeDrain`、session-private allowlist integrity、memory/thread race 回归测试；`a81554c` 记录的 NEEDS-FIX 项以本 overlay 后续验证结果为准。
- `runner.actors` 在 P21 文档中保留为 historical role naming；active Fx tag 统一澄清为 `group:"runners"`。


### 8.4.3 P22+P22.1 HEAD `aa09f58` V3-B 锚点修正 overlay（2026-04-25）

> 本节按 §10.31 只加不删追加；§8.4.2 的 HEAD `5d6a93c` 记录保留为 Round-3 代码修复基线历史 overlay。V3-B 复核实测当前仓库 `git rev-parse --short HEAD` 为 `aa09f58`，因此文档 HEAD 锚点以 `aa09f58` 为准。

- `5d6a93c` 仍作为 Round-3 代码修复提交锚点保留，不再表述为当前 Git HEAD。
- 当前文档复核基线为 HEAD `aa09f58`；代码真值仍为 root `BindRuntime.OnStop` 的 `cancel → waitForRuntimeDone → drainRuntimeBeforeStop`，以及 desktop `preDrainDesktopRuntime` 的 `WaitRuntimeDone → DrainRuntime`。
- P21 `runner.actors` historical role naming 与 active Fx tag `group:"runners"` 澄清继续沿用 §8.4.2 结论。

---

## 9. 2026-04-25 P22+P22.1 双 lane 完整收口（本会话追加）

> 会话范围：(a) P22.1 14 节点 DAG 完整实施（Phase 0 → P3B）；(b) Round 1 互审 + 修复（10 真问题）；(c) Round 2 BUG 修正（Audit-C wait<drain 矛盾 + P22-P4 接管）；(d) Final 4 路 R10 级终验戳穿 BindRuntime 顺序 regression；(e) Round 3 集中修复（6 BLOCK + 5 文档 + p21 docs）；(f) Round 3 复核 4 路 Recheck 中。共 ~35 路 codex agent 协作。
> HEAD：`aa09f58 docs(p22.1): update round-3 BLOCK overlay and sync lifecycle test integrity`

### 9.1 commit 链

| Commit | 来源 |
|---|---|
| `9f29294` | Phase 0 骨架（BusModule subscribers + RunnerModule contract + archtest skeleton）|
| `25a37ad` | P1A + P1B（root shutdown 反转 + watchFXShutdown owner ctx）|
| `f737e45` | P2A.1 insight BusModule（golden rules 模板）|
| `17b5ce7` | P2A.2 + P2B 4 包（observation + rpc + hooks + mcpcontrol）|
| `dfe12e6` | P2E cachekeepalive |
| `b386217` | P2F + P2C + P2D 三路并行（toolbridge + thread + memory）|
| `a9a018e` | P3A + P3B factory 整合 + archtest 加固 |
| `a81554c` | Round 1+2 archtest 加固 + insight/observation sync.Once |
| `fafa864` | Round 1+2 全合入（cron + uistate BusModule + rpc race + P1a/P1b/P4-4 root ctx）|
| `5d6a93c` | prompt e2e regression 修 |
| `aa09f58` | Round 3 集中修复（runner.go OnStop 顺序 + AST gate + integrity + p21 docs）|

### 9.2 销账完整度

**F-1~F-11（11 条 P22.1 finding）**：
- F-1 root shutdown ordering：✅ Round 3 真修（cancel→wait→drain）
- F-2 watchFXShutdown boundary：✅ P1B + P3A allowlist
- F-3~F-11：✅ Phase 2 全销账（memory / thread / cachekeepalive / hooks / rpc / mcpcontrol / toolbridge / insight / observation）

**P22 §7.5 deferred 10 条**：8 ✅ + 1 🟡（#2 toolbridge env opt-in 兼容路径保留）+ 1 ✅（#10 docs/契约 P22-1 处理）

**Round 3 6 条代码 BLOCK**：全 ✅（runner.go 顺序 / runtime test 反向 / TestShutdownOrdering AST / vet 错 / nested race / events_test fake mutex）

**5 处文档 HEAD overlay**：✅ `a81554c → 5d6a93c → aa09f58` 三层叠加保留历史

### 9.3 Agent 使用统计

| 阶段 | 路数 |
|---|---:|
| **P22.1 实施**：Phase 0 + Batch 1 + Batch 2 + Batch 3 + Batch 5 | 11 |
| Round 1 互审（复用原 agent）V1-V5 | 5 |
| Round 1 修复（"发现问题的 agent 直接修"）Audit-A/B/C/D + P22-1/2/3/4 | 8 |
| Round 2 重修（Audit-C + P22-P4-takeover）| 2 |
| Final 4 路 R10 终验（V1/V2/V3/V4 全新 codex）| 4 |
| Round 3 集中修复 | 1 |
| Round 3 复核 4 路（Recheck-1/2/3/4 全新 codex）| 4 |
| **会话总计** | **~35** |

### 9.4 §10 新教训建议（待落盘 `docs/plans/迁移/会话习惯.md`）

- **§10.46 多轮独立复核必须用全新 codex（R10 级别）**：本会话 4 路终验 + 4 路复核都派全新 codex（不复用任何前序 agent），保证独立性。这是 §10.22 独立第三方终审的工程化扩展。
- **§10.47 修复 agent 也会引入 regression**：P22-P4-takeover 修 P4-4 时蹭手反转了 drain 顺序（cancel→drain→wait），违反 P1A 原始正确顺序。Round 3 才修正。教训：复杂修复必须有独立验证戳穿。
- **§10.48 archtest fail-mode 真生效需 AST 而非 literal grep**：`TestShutdownOrdering` 原依赖 `strings.Index "<-done"`，但 runner.go 用 `waitForRuntimeDone` helper 不含 literal → wait 检测 dormant 表面 PASS 掩盖真违规。Round 3 升级 AST 后真生效。
- **§10.49 文档 HEAD overlay 应自动跟随 commit**：本会话 5 处文档 HEAD overlay 写 `a81554c` 但实际 HEAD 已变 `5d6a93c`/`aa09f58`，需要每次 commit 后追加新 overlay。§10.43 漂移记账规范 + §10.31 只加不删。
- **§10.50 主审报告幻觉戳穿**：Audit-D 报告 `insight/module.go:48-63` 有 two-hop subscription 是 LSP 缓存或自身实验未真回滚误判；主 agent LSP 实证 41 行干净。教训：审查报告必须 `lsp_grep` 真值复核，不直信。

### 9.5 Defer 项（follow-up 债）

- §7.5 #2 toolbridge `TOOLBRIDGE_ALLOW_DEFAULT_PERSISTENT_SUBAGENT=1` 兼容路径（保留为 explicit env opt-in，符合 P4 R2 共识）
- factory.go 800 行豁免边界（C7 实证 738 行可塞 25 组业务 Start/Stop；P22.1 P3B 设计接受，可单开 lane）
- lint 110 issues（errcheck 34 / staticcheck 24 / unused 50；optional 不阻塞）

### 9.6 下一步建议

1. **等 Recheck-1/2/3/4 整体裁决**（当前 running）— Recheck-4 给最终 P22+P22.1 双 READY 判定
2. ✅ 双 READY → P22+P22.1 完整收口宣布；§10.46-50 5 条新教训落盘 `会话习惯.md`
3. 🟠/🔴 → 派 Round 4 单点修
4. P22 主线可 release：BusModule contract + RunnerModule contract + Canonical Turn Observation Contract + fail-mode gate (AST one-hop helper resolver) + 9 字段 session-private allowlist
5. P22.1 文档建议归档为只读快照（不再追加）

### 9.7 最近 10 条对话摘要

1. 老公读 session-summary + 会话习惯（启动会话）
2. 老公开干 P22.1 按 DAG 执行 → Phase 0 骨架
3. 跳过互审推进 Batch 1 P1A+P1B 并行
4. P2A.1 insight 模板 → P2A.2 串行 → P2B 最大 slice 3 包 → P2E → 老公"加速"P2F+P2C+P2D 三路并行 → P3A+P3B 合并收口
5. 老公"金融1:5互审" → 复用 5 原 agent → 8 路修复 + 文档 5 overlay → 老公 OVERTURN BLOCK-B（factory.go 多 caller 合理）
6. Round 2 重修（Audit-C 矛盾 + P22-P4 接管）
7. 老公"再拉4个全量验证 P22 P22.1" → Final-V1/V2/V3/V4 4 路终审
8. Final 4 路 cross-check 戳穿 P22-P4-takeover regression（cancel→drain→wait 违反 §10.30）+ 文档 5 处 HEAD drift
9. 老公"安排第二轮修复" → Round 3 集中修 18 文件 → 老公 commit `aa09f58`
10. 老公"4 agent 复核" → Recheck-1/2/3/4 全新 codex 派出，进行中

### 9.8 子 Agent 提示词模式（本会话治成）

- **§10.4 复用 + §10.22 独立第三方混合**：实施轮复用原 agent，终验/复核轮强制全新 codex；不重叠
- **§10.40 防空跑独立形态**：复核 agent 必须用与原 agent **不同** receiver 名 / helper 名 / goroutine 形态做 fail-injection
- **§10.34 兄弟机制**：thinking 卡死 agent stop + 主 agent 接管核验工作区落盘改动
- **5 级分类硬规则**：🔴 BLOCK / 🟠 NEEDS-FIX / 🟡 TRUE-BUT-DEFERRED / 🟢 DOC-DRIFT / ✅ PASS — 每路终审必须用此分级
- **OVERTURN/REINSTATE 老公一票裁决权**：互审报告对 factory.go 三路 BLOCK 被老公 OVERTURN（多 caller 证明 assembly factory pattern 合理，不破坏 §10.30）
