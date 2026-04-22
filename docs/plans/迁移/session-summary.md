# V3 迁移会话摘要

> 更新时间：2026-04-22
> 会话范围：(a) 代码守卫全仓放宽 `MaxPackageFiles` 25→30；(b) 3 路 CC 超限 TDD 修复（launcher / thread / uistate+toolbridge）；(c) **P21 架构演进路线图 6 份文档经 4 轮审查迭代实施闭环**（Round-1 5 路互审 → Round-2 修订 agent + 5 路复审 → Round-3 G-P 10 路独立终审 → Round-4 10 路疏漏扫描 → Q2 合入裁决 → agent 根据裁决修直）；(d) 落盘新教训 §10.29 / §10.30 / §10.31
> 当前阶段：P21 6 份文档按 Q2 合入裁决修正完毕，实施前准备完毕；P20.13 / p20.16 仍为下一轮待开工

---

## 1. 当前结论

- **代码守卫全仓放宽**：`internal/archtest/guardlib.go` `MaxPackageFiles` 25 → **30**；autofix 自动删掉 `memory/prompt` 两条已失效 freeze（8/27 ≤ 30）；`thread` 包 27 < 30 本轮违规自动消失
- **3 路 CC 超限 TDD 修复**：
  - `cmd/mcp-orch/orchestration/launcher.go:166 looksTechnicalManagedAgentName` CC 16 → ≤ 10
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
