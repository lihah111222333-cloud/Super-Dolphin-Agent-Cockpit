# V3 迁移会话摘要

> 更新时间：2026-04-20
> 会话范围：P20 Skill 渐进披露 α 组（p20.1 / p20.10 / p20.14 / p20.15）实施 + 1:3 互审 + E/F 独立终审 + 双轮补修全部收口
> 当前阶段：P20 α 组 4 单全部 PASS，可合入 main；剩 p20.13（需先修订任务单）/ p20.16（集成测试）

---

## 1. 当前结论

- **P20 α 组 4 单本轮全部完成并经 5 级验证**：p20.1（Bug#1 prompts handler）/ p20.10（host `skill/list|expand`）/ p20.14（前端 LaunchSkillPicker）/ p20.15（SystemPromptPage 404 只读降级 + dashboard cwd scope 活化）
- **基线问题顺手闭环**：
  - archtest `rule2/rule10` fx import → 合并 `skill_catalog_fx.go` 进 `module.go`，prompt prod 文件 **28 → 27**
  - `TestStartAssemblyGolden` 日期漂移 → 新增 `PROMPT_START_CURRENT_DATE` env hook + `t.Setenv` 固定 fixture
- **critical path 保持绿**：p20.2 `78c6907` / p20.3 `cec26fe` / p20.4 `b0d2555` 已合入 main
- **P20.1 Phase 1-11 保持**：SkillCatalogProvider `c1ead48` / 元指令 `3cd3144` / fx 灰度 + 5 counter `00b073f` / 文档同步 `a07067c`
- **全量编译 + 全仓测试全绿**：`go build ./...` ✅；`go test ./internal/archtest/...` ✅；`go test ./...` ✅
- **仍未开工**：p20.13（审批缓存接线，需先按前置核查修订任务单）/ p20.16（集成测试）
- **已废弃**：p20.11（MCP skill tool）— skill 是宿主独有能力，不属编排层
- **P18.3 / P18.4 / P19 B-1 全部收口**：memory 主链、Claude parity、memory 子包拆分与 follow-up 修复均已落地（保持）
- **主 agent LSP 交叉验证通过**：`skill_catalog_fx` 0 命中 / `message.includes` 0 命中 / `dashboardPromptsParams` 真存在 / `launchSkillSelectionEnabled` 真落盘 / 3 个点名回归测试真存在

---

## 2. 本轮收口结果（2026-04-20）

### 2.1 p20.1 — 恢复宿主 `prompts/list|write|delete` + store 写能力

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

- **P20 α 组 4 单（p20.1 / p20.10 / p20.14 / p20.15）本轮全部完成**，经 1:3 互审 + E/F 双 BLOCK 独立终审 + 双轮补修全部闭环
- **基线问题顺手解决**：archtest rule2/10 + golden date；prompt freeze `28 → 27`
- **全仓 build / test / archtest 全绿**
- **下轮可直接起 p20.13**（需先按前置核查修订任务单）+ 后续起 p20.16 集成终验
- **稳定文档基线就绪**：本摘要 + p20/README + 会话习惯 + freeze 四处一致
