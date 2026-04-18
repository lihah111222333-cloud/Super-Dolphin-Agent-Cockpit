# V3 迁移会话摘要

> 更新时间：2026-04-19
> 会话范围：P18/P19 全量 + P20 Skill 渐进披露文档完整拆分（16 份可派单子计划 + 30% 落地基线 + 70% 拆分路径）
> 当前阶段：P20 实施前置准备完成，16 份可派单子计划就绪，待派实施 agent 进入五阶流水线 ② 阶段

---

## 1. 当前结论

- **P18.3 / P18.4 / P19 B-1 全部收口**：memory 主链、Claude parity、memory 子包拆分与 follow-up 修复均已落地。
- **全量编译 + 全仓测试全绿**：`go build ./...` ✅；`go test ./internal/module/memory/...` ✅；`go test ./internal/archtest/...` ✅；`go test ./...` ✅。
- **主包体量已显著收缩**：`internal/module/memory` 主包从 **82 文件 / 19,777 行** 收敛到 **52 个 `.go` 文件 / 12,356 raw 行（约 12.3k）**；按 archtest / freeze registry 口径，已收缩到 **30 non-test / 7,161 effective**。
- **子包拆分已成型**：
  - 按拆分交付 baseline：`team (19)` / `nested (12)` / `retrieval (13)` / `agent (5)` / `shared (2，含 pathsafe)`
  - 当前物理树在 follow-up 补强后为：`team (20)` / `nested (12)` / `retrieval (14)` / `agent (5)` / `shared (3)`
- **净效果**：memory 主包迁出约 **50 个文件**、净减少约 **7.4k raw 行**，compat bridge / shim / freeze / wiring 均已同步收口。
- **审查历程**：完成 **2 轮 1:3 互审 + 1 次独立第三方终审 + 2 次 follow-up 修复**，所有 blocker 均已关闭。
- **P20 文档拆分已完成**：`docs/plans/迁移/p20/` 已形成 `README.md` + `p20-original-plan.md` + `status-checkpoint-2026-04-19.md` + `source-refs-appendix.md` + `p20.1~p20.16` 共 **20 份文档**，单文件已压到 ≤600 行，进入可直接派单状态。
- **P20 真 blocker 已定性**：UI 强制技能未注入 codex 的主因不是 `prompts/list` 404，而是双断点叠加——per-turn 断点 B：`internal/module/turn/factory.go:346-355` → `internal/module/turn/service.go:107` → `internal/provider/codexapp/module.go:232-237`；launch 断点 A：`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:288-297` → `internal/dto/provider/session.go:55-63` → `internal/provider/codexapp/driver.go:64-76`。
- **P20 原方案已改判为“30% 已落地 + 70% 待拆分实施”**：已落地集中在 `SkillRef` DTO / trust / skills_meta，launch/L1 skill 主链仍待按 `p20.2 → p20.3 → p20.4` critical path 实施（见 `docs/plans/迁移/p20/status-checkpoint-2026-04-19.md:16-52,64-86`）。
- **Bug #1 已单列**：`prompts/list|write|delete` host RPC 404 属于 P8 契约回归；`internal/module/dashboard/prompt_rpc.go` 于 commit `c50ef009`（2026-03-25）被删除，已独立拆成 `p20.1`（见 `docs/plans/迁移/p20/status-checkpoint-2026-04-19.md:56-63`）。
- **本次会话只产出文档**：未提交 Go / 前端代码改动；当前交接仅反映计划拆分、根因钉牢与派单准备状态。

---

## 2. 本轮收口结果（2026-04-19）

### 2.1 代码面
- **P19 B-1 — memory 子包拆分完成**：
  - `memory/team/`：Team Memory 整包（manager/path/guard/sync/watcher/module）
  - `memory/nested/`：nested runtime/rules + claudeMd source/filter/parse/candidate
  - `memory/retrieval/`：manifest/finder/ranking/prefetch/render/path/module
  - `memory/agent/`：Agent Memory manager/prompt/type sanitize/module
  - `memory/shared/`：shared types + pathsafe helper
- **root 兼容层已就位**：`agent_bridge.go` / `team_bridge.go` / `nested_shim.go` / `retrieval_bridge.go` / `retrieval_helpers_bridge.go` 保持旧 root caller 可运行，同时给后续直接 import 子包留迁移窗口。
- **Follow-up 全收口**：
  - path canonical：root / retrieval / team 共用 path helper 逻辑已收缩并对齐
  - TeamSync 生产链：`memory/team/module.go` 已接入 `NewTeamSyncService` + lifecycle
  - orphan provider：retrieval ranking / helper / provider wiring 已补齐调用链
  - bridge owner：root compat bridge / shim 已补 owner / remove-when 语义
  - archtest freeze：`module/memory` freeze 已自动收缩到 **30 / 7161**

### 2.2 文档面
- `session-summary.md` 已刷新为 P19 B-1 完成态，并在本轮追加 P20 文档拆分就绪状态。
- `p19-contract-violation-remediation.md` 已把 B-1 更新为完成，并记录 2026-04-17 收口结果。
- `11-memory-prompt-thread.md` 已补 memory 新结构、compat bridge/shim 与子包职责。
- `会话习惯.md` 将在本轮追加 P20 多轮协作的 6 条新经验（§10.15~§10.20）。

### 2.3 LSP 包搬迁（2026-04-17）
- 动作：LSP 10 个子包（`edit/exec/format/gopls/installer/manager/middleware/protocol/search/tools`）迁入 `cmd/mcp-lsp/*`
- commits（5 个，Step -1 实际拆成 A/B 两步）：`ff4083d`（Step -1A 守卫常量 + autofix 串入）、`ec96cab`（Step -1B spec / 契约 / 会话习惯 / codemap / 计划文档同步 + 中间态 ai-index）、`1bac4c1`（Step 1-3 搬迁 git mv+sed）、`70bb462`（Step 4 archtest rule7/7b/10 + mcp_family_isolation 重设计 + guardlib/freeze 残差）、`f3b228a`（Step 5 剩余文档同步 + 新 review note + 最新 ai-index）
- 验证：`go build ./...` ✅；`go test -p 1 ./...` ✅；`TestCodeSizeGuard` / `TestMCPFamilyIsolation` 全绿
- 守卫变更：默认 `25/10000/600`（见 `docs/plans/迁移/v3-code-guard-spec.md §1`）

### 2.4 发现与根因
- **Bug #2 才是 P20 主 blocker**：`internal/module/turn/factory.go:346-355` 仍只把 skill 规格归一为 `SkillRef{Name}`，`internal/module/turn/service.go:107` 只做 resolve / 去重，`internal/provider/codexapp/module.go:232-237` 遇到 `skill.Prompt==""` 直接跳过，导致 per-turn 手动技能静默丢失。
- **launch 断点 A 仍未打通**：`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:288-297` 的 `thread/start` 只发 `{cwd, modelProvider}`；`internal/dto/provider/session.go:55-63` 的 `StartSessionRequest` 与 `internal/provider/codexapp/driver.go:64-76` 的 `threadStartParams` 都还没有 skills 字段。
- **`prompts/list` 404 是旁路 bug #1**：P8 已要求保留宿主 prompt surface，但 `internal/module/dashboard/prompt_rpc.go` 在 commit `c50ef009`（2026-03-25）被删除，形成后端 contract regression（见 `docs/plans/迁移/p20/p20.1-bug-prompts-list-handler.md:20-29,67-69`）。

### 2.5 P20 文档拆分
- **目录已完整成型**：`docs/plans/迁移/p20/` 当前共 **20 份文档**；最大文件 `p20-original-plan.md` 为 **594 行**，其余均显著低于上限（本轮 `find + wc -l` 实测）。
- **README 已成为 authoritative 调度入口**：依赖图 Mermaid DAG 已确认无环（`docs/plans/迁移/p20/README.md:42-86`），并已固化 α / critical / β / γ / γ' / δ / ε / 终 并行组（`docs/plans/迁移/p20/README.md:88-97`）。
- **锚点附录已完成去孤儿**：`source-refs-appendix.md` 把旧 68 条锚点收敛为 40 条活跃锚点，并移除了 5 个被驳回方案路径（`docs/plans/迁移/p20/source-refs-appendix.md:185-189`）。
- **关键技术决策已固化**：shared `SkillInjectionPort` method set 已在 `p20.6` / `p20.7` 对齐（`docs/plans/迁移/p20/p20.6-skill-inject-claudecli.md:34-54`; `docs/plans/迁移/p20/p20.7-skill-inject-codexapp.md:30-46`）；codex launch 注入单点锁定为 `internal/provider/codexapp/driver.go:225-238` `startAssemblyInstructions()`；rollout marker 口径锁定为 **dual-read / single-write**（`docs/plans/迁移/p20/README.md:151-154`）；host RPC 用 slash、MCP tool 用 underscore（`docs/plans/迁移/p20/status-checkpoint-2026-04-19.md:163-166`）；`expanded_state` 锁定为 per-thread / 5 turns TTL / 不持久化（`docs/plans/迁移/p20/status-checkpoint-2026-04-19.md:151-154`）。
- **archtest 权威数字已统一**：`internal/module/prompt` 当前 authoritative 真值为 **26 个 prod `.go` 文件 / 2858 effective lines**（`docs/plans/迁移/p20/status-checkpoint-2026-04-19.md:115-118`; `docs/plans/迁移/p20/p20.1-bug-prompts-list-handler.md:20-29`）。

### 2.6 协作统计
- **30+ agent 多轮协作**：4 路 bug 定位 + 1 路文档重构 + 10 路审查 + 2 路修订 + 10 路第三轮复审 + 3 路 fix + 1 路主 agent LSP 交叉验证。
- **最终产出只有文档**：本轮没有把 Go / 前端实现并入提交；交付物是 20 份 P20 文档、更新后的 `session-summary.md` 与 `会话习惯.md`。

---

## 3. Phase 状态

| Phase | 状态 | 说明 |
|------|------|------|
| P18 Phase 0-8 | ✅ 全部完成 | 基础设施、memory/prompt/provider/thread/turn 全链路落地 |
| P18.2-A/B/C/D | ✅ 全部完成 | Turn 上下文 + CachePolicy 三分法 + 门禁快照 + Snapshot 持久化 |
| P18.3-E/F/G/H/I/J/K/L | ✅ 全部完成 | claudeMd / output style / scope-path / transcript / cleanup / kairos / team / nested 全链路收口 |
| P18.4 | ✅ 完成 | ADR-001/002 + parity / docs debt 已落盘 |
| P19-A | ✅ 完成 | 依赖方向修正与 fx 泄漏回收完成 |
| P19-B-1 | ✅ 完成 | memory 主包拆为 team / nested / retrieval / agent / shared 五个 leaf 子包 |
| P19-B-1 Follow-up | ✅ 完成 | path canonical / bridge owner / TeamSync 生产链 / orphan provider / freeze 收缩 已全部收口 |
| P19-C/D/E/F | ✅ 完成 | auto_dream 拆分、接口纯化、timeout/sqlc/MCP 壳、archtest/spec 对齐全绿 |
| P20 文档拆分 | ✅ 完成 | `docs/plans/迁移/p20/` 20 份文档、DAG、并行组、锚点附录、状态 checkpoint 已就位 |
| P20 实施 | 🔲 待派 | 待按 `p20.2 → p20.3 → p20.4` critical path 进入五阶流水线 ② 阶段 |

---

## 4. 下一步

1. **快速路径（1-2 agent）**：先起 `p20.2`，补齐 codex per-turn name-list 兜底与 entity hydrate，让 UI 强制技能优先生效。
2. **critical-path**：严格按 `p20.2 → p20.3 → p20.4` 推进，先修断点 B，再修断点 A，最后接 launch assembly / provider start 链。
3. **完整路径**：按 `README` 最终并行组派 10+ agent：α 先发，critical 串行，随后 β / γ / γ' / δ / ε 并行，最后 `p20.16` 做整体验收。
4. **P20 完成后恢复余量治理与新功能开发**：回到原 P19 第三波余量治理、compat bridge 清理，以及后续新需求开发。

---

## 5. 交接建议

1. **freeze registry 现已可信**：`module/memory` 的 package_count / package_lines 已收缩到真实当前值，后续只许继续下降。
2. **leaf package 已就位**：新增逻辑优先放到 `memory/team|nested|retrieval|agent|shared`，避免再回灌 root 包。
3. **compat bridge 不是长期归宿**：owner/remove-when 已写清，后续要按 caller 迁移情况逐步删除。
4. **独立第三方终审是必需环节**：参与 agent 的互审能发现同层问题，但发现不了“大家都默认了”的盲区。
5. **文档先于 commit**：session-summary / P19 / codemap / 会话习惯 需要作为最终收尾动作一次性刷新，避免代码领先文档。
6. **P20 只认三份 authoritative 文档**：实施前先读 `docs/plans/迁移/p20/README.md`、`status-checkpoint-2026-04-19.md`、`source-refs-appendix.md`，再进入各 `p20.X` 子单。
7. **不要把 Bug #1 当成 Bug #2 根因**：`prompts/list` 404 要走 `p20.1`，UI 强制技能主链必须按断点 B / A / launch wire 分治。
8. **archtest 数字以 prompt=26/2858 为唯一真值**：agent 报出 `47` 时，默认是把 `_test.go` 混进目录总数；不要再据此抬 freeze。
9. **本轮没有代码合入**：下轮实施必须重新派 agent 进入五阶流水线 ② 阶段，不要把“文档就绪”误读成“功能已完成”。

---

## 6. 交接结论

- **P18.3 / P18.4 / P19 B-1 已全部完成**。
- **memory 子包拆分与 follow-up 修复全部收口**：TeamSync 生产链、retrieval provider、path canonical、bridge owner、archtest freeze 全部解决。
- **`docs/plans/迁移/p20/` 已完成完整拆分**：20 份文档、16 份可派单子计划、无环 DAG、40 条活跃锚点、并行组与派发表全部就位。
- **P20 代码实现尚未开始提交**：唯一 authoritative 结论是“实施前置准备完成，待派实施 agent 入场”；本轮交接不包含 Go / 前端功能改动。
- **当前仓库已具备稳定文档基线**：可直接按 `p20.2 → p20.3 → p20.4` 开工，并在 P20 完成后恢复 P19 余量治理与新功能开发。
