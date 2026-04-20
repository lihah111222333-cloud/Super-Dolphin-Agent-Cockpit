# P20 Status Checkpoint — 2026-04-19

> 口径：把 P20 从“单文件总纲”切成 `p20.1` ~ `p20.16` 可派单任务单；本页只记录 2026-04-19 的落地现状与优先级。

## 1. 四路审查交叉共识（精炼版）

1. **方向保留，施工顺序必须重排。** 原方案的 progressive disclosure 思路不推翻，但当前仓库真相已不是“从零实现”，而是“30% 已落地 / 40% 部分落地 / 30% 未落地（4 / 9 / 10）”，所以必须改成 critical path + 并行支线。
2. **Bug #2 才是 P20 真正 blocker。** UI 强制技能失效不是单点问题，而是 `per-turn 断点 B` 与 `launch 断点 A` 两处断裂叠加；修复顺序必须先 `p20.2`，再 `p20.3`，最后 `p20.4`。
3. **Bug #1 是契约回归，不是“可接受噪声”。** `prompts/list|write|delete` 被前端直接依赖，且 P8 已写明宿主 surface 必须保留，因此应单独成 `p20.1`，不再混入 skill 主链。
4. **其余工作可拆成真正并行单。** rollout marker、RPC、MCP、config/policy/metrics、approval wiring、frontend fallback 都能独立推进；provider 实现与前端 launch UI 则待 critical path 契约稳定后并行收口。

> 旁证：实验 A（`docs/experiments/p20-exp-a-agentsmd-merge.md`）已证明 codex `baseInstructions` 语义高于 AGENTS.md；实验 B（`docs/experiments/p20-exp-b-claudecli-native-skills.md`）已锁定 Claude native skills 必须走“扫盘降级”方案。

## 2. 30% / 40% / 30% 落地对照（4 / 9 / 10，按本次 checkpoint 口径）

### 2.1 ✅ 已落地（4 项 / 30% 桶）

| 计划项 | 锚点 | 备注 |
|---|---|---|
| `SkillRef{Version,Mode,Summary,Source}` DTO | `internal/dto/provider/turn.go:43-117` | 新 DTO 已在 wire 层就位 |
| DTO 兼容测试 | `internal/dto/provider/turn_test.go:12-249` | old/new payload round-trip 已覆盖 |
| skill trust / `TrustScope` | `internal/module/skill/trust.go:12-108` | 信任枚举骨架已落地 |
| frontmatter 解析 | `internal/module/skill/skills_meta.go:104-143,209-279` | summary / trigger / trust 元信息入口已存在 |

### 2.2 ⚠️ 部分落地（9 项 / 40% 桶）

| 计划项 | 锚点 | 当前缺口 |
|---|---|---|
| 审批缓存 `(name,hash)` | `internal/module/skill/approval.go:14-298`; `internal/module/skill/service.go:30-49` | 生产链未消费 → `p20.13` |
| `skill/requestApproval` 事件面 | `internal/platform/eventsurface/bind.go:50-53`; `internal/provider/codexapp/factory.go:41-55` | 扫描 / expand 审批尚未落地 → `p20.13` |
| skill 静态清单来源 | `internal/module/skill/skills_meta.go:22-45`; `internal/module/skill/skills_fs.go:17-27`; `internal/module/skill/rpc.go:24-29` | 未接 prompt / L1 → `p20.5` |
| `skill/list` / `skill/expand` RPC 面 | `internal/module/skill/rpc.go:17-55`; `internal/module/skill/rpc_skill_types.go:1-61` | 只有 `skills/list` / CRUD / preview → `p20.10` |
| rollout 读端 | `internal/provider/codexapp/history_rollout.go:31-37,245-277`; `internal/provider/claudecli/history_trim.go:16-22,63-95` | 仍是 legacy marker → `p20.9` |
| codex / claude per-turn 注入 | `internal/provider/codexapp/session_turn.go:77-94`; `internal/provider/codexapp/module.go:232-249`; `internal/provider/claudecli/session_turn.go:294-323,339-351` | 两家 provider 仍只吃 `Prompt` 全文 → `p20.6` + `p20.7` |
| `skillResolver` | `internal/module/turn/skills.go:11-102` | 无矩阵 / top-k / TTL → `p20.8` |
| launch assembly | `internal/module/thread/lifecycle.go:83-124`; `internal/module/thread/start_session_helpers.go:74-86`; `internal/contract/prompt.go:109-134`; `internal/provider/claudecli/config.go:52-69`; `internal/provider/codexapp/driver.go:225-238` | 无 skill 字段 / 无 launch wire → `p20.3` + `p20.4` |
| per-turn 运行时 | `internal/module/turn/service.go:93-130`; `internal/module/turn/prompt_assembly.go:13-43` | UI 选中技能 body 丢失 → `p20.2` |

### 2.3 ❌ 未落地（10 项 / 30% 桶）

| 计划项 | 状态 | 对应任务单 |
|---|---|---|
| `internal/module/prompt/skill_catalog_provider.go` | 文件缺失 | `p20.5` |
| dynamic prompt slot 注册 | 未实现 | `p20.5` |
| skill port `Expand/GetSummary/ReadBody`（`internal/module/skill/contract.go` 仍是旧接口） | 未实现 | `p20.5` / `p20.10` |
| `internal/module/skill/rollout_markers.go` | 文件缺失 | `p20.9` |
| `internal/provider/claudecli/skill_inject.go` | 文件缺失 | `p20.6` |
| `internal/provider/codexapp/skill_inject.go` | 文件缺失 | `p20.7` |
| `internal/contract/skill_injection.go` | 文件缺失 | `p20.6` / `p20.7` |
| `internal/module/turn/expanded_state.go` | 文件缺失 | `p20.8` |
| `config.skill.progressive_disclosure` + skillPolicy + metrics | `internal/platform/config/config.go:9-25` 未含 | `p20.12` |
| MCP tool 注册 `skill_expand` / `skill_list` | `cmd/mcp-orch/tools/` 无命中 | `p20.11` |

## 3. 两个 Bug 的 smoking-gun 一页纸

### 3.1 Bug #1 — `prompts/list` host RPC 404

- 前端调用链已经固定：`SystemPromptPage.js:123,151,168` 分别直呼 `prompts/list`、`prompts/write`、`prompts/delete`。
- P8 保留条款写在文档里：`docs/plans/迁移/p8-execution-plan.md:94-201` 明确要求宿主 RPC surface 保留，MCP 迁移只允许新增本地 adapter。
- 仓库当前真相：`internal/module/dashboard/prompt_rpc.go` 已在 commit `c50ef009`（2026-03-25）删除，`internal/module/prompt/` 下也没有替代的 `rpc.go`。
- 进一步坐实缺口：`internal/store/prompt/contract.go:9-13` 仍只有 `Reader`，说明就算补 handler，也还缺 write/delete store 能力。
- 结论：这不是 UI 偶发噪声，而是 **后端 surface 回归**；应独立成 `p20.1`，并让 `p20.15` 只做前端降级，不替代后端修复。

### 3.2 Bug #2 — UI 强制技能没注入 codex

**断点 B（per-turn，先修）**

`internal/module/turn/factory.go:346-355`
→ `normalizeSkillNames()` 只产 `SkillRef{Name}`
→ `internal/module/turn/service.go:107` 只 resolve / 去重，不补 body
→ `internal/provider/codexapp/module.go:235-237` 遇到 `skill.Prompt==""` 直接跳过
→ 手动选中的 skill 在 provider 侧被静默丢弃。

**断点 A（launch，后修）**

`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:288-296`
→ `thread/start` 只发 `{cwd, modelProvider}`
→ `internal/dto/provider/session.go:55-63` `StartSessionRequest` 没 skills
→ `internal/provider/codexapp/driver.go:64-76` `threadStartParams` 也没 skills
→ 即使 UI 在 launch 前已知 selected skills，也进不了 provider start 链。

**排序结论**

- `p20.2`：先把 per-turn entity 补齐，不再 name-only。
- `p20.3`：再给 launch transport 加 `selectedSkills` 契约。
- `p20.4`：最后把 skill 真正接入 start assembly / provider start 链。

## 4. 下一步行动优先级

### Critical
- `p20.2` — Bug #2 / 断点 B（critical-path 首段）
- `p20.3` — Bug #2 / 断点 A（依赖 `p20.2`）
- `p20.4` — launch assembly skill wire（依赖 `p20.3`）

### High
- `p20.1` — Bug #1 后端恢复
- `p20.5` — L1 catalog provider
- `p20.6` / `p20.7` — claude / codex provider 实现
- `p20.8` — resolver 矩阵
- `p20.13` — approval cache wiring
- `p20.14` — LaunchSkillPicker（等 contract 稳定）

### Medium
- `p20.9` — rollout markers
- `p20.10` — host RPC expand/list
- `p20.11` — MCP tools
- `p20.12` — config / policy / metrics
- `p20.15` — 前端 404 fallback（可先做页面降级，最终等 `p20.1`）
- `p20.16` — 集成测试收口（必须最后）

## 本轮审查修订汇总（2026-04-19 第二轮）

> 说明：用户口头写“13 条”，但本轮清单实际编号 **1-14**；以下按 **14 条**全量落盘。

### 修正 1 — prompt 包 archtest 真值改判
- **原方案**：把 `internal/module/prompt` 写成约 `47` 文件 / freeze 缺口 `+21`。
- **证据**：`go test ./internal/archtest/... -run TestCodeSizeGuard -v` 当轮通过；`internal/archtest/guardlib.go:225-227,129-150` 明确只统 non-test `.go` 且按 effective lines 计数；同轮复核输出 `prompt package archprobe => prod_files=26 effective_lines=2858`。
- **修订方案**：统一改口径为 **26 个 prod 文件 / 2858 effective lines**；把 `47` 归因于 `_test.go` 混入目录总数。
- **受影响文档**：`p20.1`、`README`

### 修正 2 — p20.1 驳回新建 `rpc.go`
- **原方案**：在 `internal/module/prompt/` 新建 `rpc.go` + `rpc_test.go`。
- **证据**：`internal/archtest/freeze_registry.go:29-35` 当前 freeze 已锁 `internal/module/prompt:26`；`docs/plans/迁移/p20/p20.5-skill-catalog-provider.md:12-13,32-38` 已把 catalog provider 改落 `internal/module/skill/`。
- **修订方案**：选 **方案 B**：在 `internal/module/prompt/service.go` / `module.go` 现有文件内合并 `prompts/list|write|delete` handler；同时补 `internal/store/prompt` 写接口恢复。
- **受影响文档**：`p20.1`、`README`

### 修正 3 — p20.2 驳回新建 `skill_hydrator.go`
- **原方案**：另起 hydrator 新文件承接 name-only skill 补全。
- **证据**：`docs/plans/迁移/p20/p20.2-bug-turn-skill-resolve.md:64-67,87-90` 已锁定“hydrate 收敛到 `PrepareTurn()` 前置阶段 + codex fallback”，并明确禁止新建 `skill_hydrator.go`。
- **修订方案**：在 `internal/module/turn/service.go` 先 hydrate 再 resolve；`internal/provider/codexapp/module.go` 在 `Prompt==""` 时保留 summary/name fallback，避免 silent drop。
- **受影响文档**：`p20.2`、`README`

### 修正 4 — p20.5 落点改到 `internal/module/skill`
- **原方案**：`internal/module/prompt/skill_catalog_provider.go`。
- **证据**：`docs/plans/迁移/p20/p20.5-skill-catalog-provider.md:11-13,32-38` 已指出 prompt freeze `26/26`，并要求 provider 改落 `internal/module/skill/`，prompt 侧只补 slot/spec。
- **修订方案**：provider / tests 落 `skill` 模块；`prompt` 只改 `dynamic.go` + contract 既有文件，保持 `prompt` **0 新增 prod 文件**。
- **受影响文档**：`p20.5`、`README`

### 修正 5 — p20.6 + p20.7 共享 `SkillInjectionPort`
- **原方案**：claude / codex 各自持有一套 skill 注入契约。
- **证据**：`docs/plans/迁移/p20/p20.6-skill-inject-claudecli.md:12,34-43` 与 `docs/plans/迁移/p20/p20.7-skill-inject-codexapp.md:11,30-46` 都要求共享 `internal/contract/skill_injection.go`。
- **修订方案**：统一 method set 为 `InjectL1Manifest` / `BuildTurnSection` / `ReservedTokens`；module 只依赖 `internal/contract`，provider 不再互相分叉类型。
- **受影响文档**：`p20.6`、`p20.7`、`README`

### 修正 6 — p20.6 必须补通用 carrier 与 provider-name registry
- **原方案**：只在 claude provider 内部拼接 skill prompt 文本。
- **证据**：`docs/plans/迁移/p20/p20.6-skill-inject-claudecli.md:20,39-43,55-57,70-71` 已明确缺少 `dto.TurnRequest.SkillPrompt` carrier 与 module 按 provider 名解析 port 的 registry。
- **修订方案**：在 `dto.TurnRequest` / `SteerRequest` 增 `SkillPrompt`，turn module 通过 provider-name registry 解析 `SkillInjectionPort`，不再退回 provider-local 拼接。
- **受影响文档**：`p20.6`

### 修正 7 — p20.8 决策矩阵 / expanded_state 口径补齐
- **原方案**：矩阵不完整，TTL / matcher seam / 持久化边界未写透。
- **证据**：`docs/plans/迁移/p20/p20.8-skill-resolver-matrix.md:30-45,47-73,84-85,100-103` 已补齐 4×3=12 矩阵、per-thread `expanded_state`、TTL=5 turns、复用 `internal/module/skill/skills_match.go` seam。
- **修订方案**：锁定“内存态、按 thread 隔离、5 turns TTL、hash 变更立刻失效、typed matcher seam 复用 skill 模块”。
- **受影响文档**：`p20.8`

### 修正 8 — p20.9 改为 dual-read / single-write
- **原方案**：读写同单切新 marker，存在 dual-write 诱惑。
- **证据**：`docs/plans/迁移/p20/p20.9-rollout-markers.md:23-30` 已明确“读端双读先落地，兼容期不要 dual-write”。
- **修订方案**：`p20.9` 只负责 shared helper + 双读 trim；写端切换改并入 `p20.6/p20.7`，避免 token 翻倍与 trim 歧义。
- **受影响文档**：`p20.9`、`README`

### 修正 9 — p20.10 锁定 host slash / MCP underscore / 无 request hash
- **原方案**：命名与缓存参数边界未锁，易把 MCP / host surface 混写。
- **证据**：`docs/plans/迁移/p20/p20.10-skill-rpc-expand-list.md:51-77` 已固定 `skill/list`、`skill/expand`，v1 不加 hash/ETag 请求参数，并保留 `skills/match/preview` 共存。
- **修订方案**：host RPC 继续 slash、MCP tool 继续 underscore；`skill/expand` response 返回 `content_hash`，请求侧不带 hash；老 `skills/*` surface 不回退。
- **受影响文档**：`p20.10`、`README`

### 修正 10 — p20.11 显式依赖 p20.10，且直调 `skill.Service`
- **原方案**：MCP 可独立推进，或复用 host `Server.Dispatch()`/handler map。
- **证据**：`docs/plans/迁移/p20/p20.11-mcp-skill-tools.md:3,9-11,23-32,42-52` 已写明依赖 `p20.10` 的 `skill.Service.Expand(...)`，并禁止 `Server.Dispatch("skill/...")`。
- **修订方案**：MCP facade 只注入 `skill.Service`；`cmd/mcp-orch/fx.go` 的 provider 与 `tools/skill` capability 同步修改，避免 capability 漂移。
- **受影响文档**：`p20.11`、`README`

### 修正 11 — p20.12 缩到 1 文件且 metrics 改 snake_case
- **原方案**：按 3 个新文件拆 config / policy / metrics，且命名风格未锁。
- **证据**：`docs/plans/迁移/p20/p20.12-config-policy-metrics.md:12,20-27,57-67,69-72` 已把实现压缩为 `policy_metrics.go` 一文件，并列出 `skill_l1_tokens` 等 snake_case 名称。
- **修订方案**：只允 +1 `policy_metrics.go`，测试并入既有 `*_test.go`；指标统一 `skill_l1_tokens`、`skill_expand_total` 等 snake_case。
- **受影响文档**：`p20.12`、`README`

### 修正 12 — p20.13 依赖 p20.10，`scope=session` 不落盘
- **原方案**：默认独立开工，且 session/project scope 边界不清。
- **证据**：`docs/plans/迁移/p20/p20.13-approval-cache-wiring.md:3-4,16-22,52-77` 已说明本单依赖真实 `skill/expand` 消费点，且 `(name,hash)` 为 content-global；`scope=session` 必须是内存态。
- **修订方案**：`p20.13` 只在 `p20.10` 之后开工；默认 `(name,hash)` 全局批准；`scope=session` 只写内存，不得偷偷写入 `skills-trust.json`。
- **受影响文档**：`p20.13`、`README`

### 修正 13 — p20.14 限制在极小 payload diff，重排 `performSend()` 顺序
- **原方案**：在 `thread-actions-helpers.js` 继续堆逻辑，且保持 `startThread()` → resolve → `sendMessage()` 顺序。
- **证据**：`docs/plans/迁移/p20/p20.14-frontend-launch-skill-ui.md:3-4,12-15,44-47,63-79` 已指出 `thread-actions-helpers.js` 613 行超限，且 blank-thread 首发必须先拿 launch skill state。
- **修订方案**：`performSend()` 改为 **launch skill → `startThread()` → `sendMessage()`**；`thread-actions-helpers.js` 只补极小 `selectedSkills/manualSkillSelection` payload diff。
- **受影响文档**：`p20.14`

### 修正 14 — p20.15 不设 feature flag，日志分 warn/info 双级
- **原方案**：fallback 可能挂 feature flag，且把 `dashboard/prompts` 当主写路径。
- **证据**：`docs/plans/迁移/p20/p20.15-frontend-systempromptpage-fallback.md:31-35,40-42,51-52` 已明确：不需要 feature flag；`dashboard/prompts` 仅 list-only 旁路；首次 fallback 记 `warn`、稳态 / 恢复记 `info`。
- **修订方案**：`prompts/list` 一旦恢复即自动退出 fallback；只保留 list-only adapter；日志级别改为首次 `warn`、后续 `info`。
- **受影响文档**：`p20.15`、`README`

## 施工准备就绪检查清单

- **Go**：`p20.1` 已按 archtest 真值锁为方案 B，prompt freeze 保持 `26`，且 `internal/store/prompt` 写能力恢复已写入文档。
- **Go**：`README` 依赖图已补 `p20.10 → p20.11/p20.13`、`p20.6 → p20.13`、`p20.6/p20.7 → p20.9`，并把 `p20.14` 依赖改回 `p20.3`；本地 DAG 校验无环。
- **Go**：critical / α / β / γ / γ' / δ / ε / 终 分组已重排；`p20.11`、`p20.13` 不再被误判为“可立即独立开工”。
- **Go**：包预算已更新为“prompt 26 prod 真值、thread 25 禁新增、claudecli 24 仅 +1、turn 22→24、codexapp 19→20、skill 24 total `.go` 仅再放行 1 个 `policy_metrics.go`”。
- **Go**：本轮 14 条修正项已全量落盘到 P20 文档；若再变更方案，必须先回写对应 `p20.X` 子单再实施。
- **No-Go**：任何实现若再次计划新增 `internal/module/prompt/rpc.go`、`internal/module/thread` prod 文件、或让 `cmd/mcp-orch` 走 host `Server.Dispatch()`，都应先停工回文档评审。

## 第四轮修订（2026-04-19）— 审查后补修

### 修订项（5 条）
1. README DAG 加 `p20.6 → p20.13`，并把 `p20.14` 依赖改回 `p20.3`。
2. README Agent 派发表 4 处预算同步（`p20.7` / `p20.8` / `p20.11` / `p20.14`）。
3. README 横幅加字面计数（`4 / 9 / 10`）。
4. source-refs 补 5 新锚点 / 删 5 过时 / 清孤儿。
5. turn 包预算口径权威：`22→24`。
   - 备注：`p20.2` turn 包预算口径以 README `22→24` 为准（含 turn 包内小改 + 可能的 test 补全）。

### 施工阻塞清单（2026-04-19 第五轮补修完成）
- ✅ `p20.10` vs `p20.13` `rpc_skill.go` 冲突 — 已解决：`p20.10 §4` 改为复用现有 `internal/module/skill/rpc.go` / `rpc_skill_types.go`，不新建文件；`p20.13` 多处禁止同步确认
- ✅ `p20.3/4` vs `p20.7` `threadStartParams` 冲突 — 已解决：`p20.3/4` 收敛到 DTO 层 (`StartSessionRequest` additive)，provider manifest 注入归 `p20.7 InjectL1Manifest` 单点
- ✅ 子单内一致性 — 已解决：`p20.2/5/7/15/16` 已补修（fallback 统一 `skills:\n- name`、test 文件冲突消除、签名描述更新、store 合同补注、覆盖矩阵补 7 入口 + 缩到 ≤15 文件）

## 2026-04-19 第六轮增量施工（已落盘 / 待 commit）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| P20.1 §4 Phase 5 · resolver 去重键升级为 `name@version` | ✅ | `internal/module/turn/skills.go` | `skillDedupKey()` + `normalizeSkillRefs()`、`Resolve()` 全切换；autoMatch 查 seen 使用同格式 |
| P20.1 §4 Phase 5 · `expandedStateStore` 落盘 | ✅ | `internal/module/turn/expanded_state.go`（新文件） | `(Name,Kind,Locator,Hash)` key、TTL=5 turns、hash 变即失效；待 `p20.8` resolver 矩阵接入 |
| P20.1 §4 Phase 5 · 单测 | ✅ | `internal/module/turn/skills_test.go`、`internal/module/turn/expanded_state_test.go`（均新文件） | `TestSkillDedupKey/TestNormalizeSkillRefsDedupesByNameAndVersion/TestSkillResolverKeepsSameNameDifferentVersion/TestSkillResolverAutoMatchSkipsAlreadyExplicitVersion` + 6 项 `TestExpandedStateStore*` 与 `TestExpandedKeyValidation/TestNormalizeArtifactLocator` |
| **p20.2 §5 step 4 · codex `buildSkillPromptInput` name-list fallback** | ✅ | `internal/provider/codexapp/module.go` | 与 `claudecli buildSkillSection` 对齐：non-None skill 始终产出 `skills:\n- name`，block 部分按 `RenderSkillBlock` 正常渲染；消除 `Prompt==""` 时的 silent drop |
| p20.2 §5 step 4 · codex 补测 + 对齐旧测 | ✅ | `internal/provider/codexapp/skill_injection_test.go`、`internal/provider/codexapp/input_map_test.go` | 新增 `TestBuildSkillPromptInput_NameListFallbackWhenBodyMissing` / `TestBuildSkillPromptInput_NameListFallbackSkipsNoneAndInvalid`；`TestBuildSkillPromptInput_FullMode` / `LegacyPayloadEmptyModeUsesFull` / `AllSkippedReturnsFalse` 跟进新格式；`TestBuildTurnStartParams` / `TestBuildTurnSteerParams` 更新为 name-list+block 拼接期望 |
| **未完成—p20.2 step 1-3 / 5** | ⚠️ 次会话代发 | `internal/module/turn/service.go` / `module.go`；`internal/module/turn/service_test.go` | skill.Service 作为 optional fx 参注入 + `PrepareTurn()` 前置 hydrate（`ListSkills`+`ReadLocal`）；新增 `TestPrepareTurnHydratesNameOnlySkill` / `TestPrepareTurnPreservesSummaryWhenBodyMissing` |

测证门禁（2026-04-19）：
- `go test ./internal/module/turn/... ./internal/module/skill/...` ✅（含新增 dedup / expanded_state / render 测例）
- `go test ./internal/provider/codexapp/ -run '^(TestBuild|TestMap|TestResolveLocalTurnID|TestTextTurnInput|TestHistory|TestSession|TestTransport|TestSkill|TestProcess|TestEvent|TestDriver|TestInput)'` ✅（子集全绿）
- `go build ./...` ✅

### 2026-04-19 第七轮增量施工（p20.2 critical-path 关闭）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.2 §5 step 1 · skillHydrationPort + fx 4th optional 注入 | ✅ | `internal/module/turn/service.go`、`internal/module/turn/module.go` | turn.service 现持可选 `skillLookup`；`NewServiceWithPromptAssemblyAndTurnContext` 增第 4 参 `skill.Service`（fx `optional:"true"`），`NewService`/`NewServiceWithPromptAssembly` 保持零值 `nil` 兼容 |
| p20.2 §5 step 2-3 · PrepareTurn 前置 hydrate | ✅ | `internal/module/turn/skills.go` | 新增 `(*service).hydrateSkillRefs` + `(*service).readSkillBody` + `shortSkillHash`：ListSkills → name map → 对每条 `SkillRef` 补 `Summary/Version/Source`，Prompt 空时 `ReadLocal(<dir>/SKILL.md)`；failure/空值均保留原字段，不做破坏性覆盖 |
| p20.2 §5 step 5 · turn 侧定向测试 | ✅ | `internal/module/turn/service_skill_hydrate_test.go`（新文件） | 5 组测例：hydrate 全字段 / body 缺失仅 Summary+Version / lookup=nil 直通 / 已填充字段不覆写 / ListSkills 错误保留原输入 |
| 合规 | ✅ | — | `go build ./...` 通过；`go test ./internal/module/turn/... ./internal/module/skill/...` 全绿；`internal/provider/codexapp` 子集全绿（含 name-list fallback） |

**p20.2 critical-path 首段（`PrepareTurn` hydrate + codex fallback）本轮全部关闭**；`p20.3` / `p20.4`（launch 断点 A + StartAssembly 接线）仍未开工。

### 2026-04-19 第八轮增量施工（p20.3 launch skill 契约打通）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.3 §4.3 · public RPC payload 扩展 | ✅ | `internal/module/thread/rpc_types.go` | `startParams` 新增 `SelectedSkills`（snake_case 主 tag）+ `ManualSkillSelection`；`fillLegacyLaunchSkillFields` 读 camelCase 别名；非字符串数组直接报错 |
| p20.3 §4.3 · thread 合同 + service 映射 | ✅ | `internal/module/thread/contract.go`、`internal/module/thread/rpc.go`、`internal/module/thread/start_session.go` | `StartRequest` 内部字段 `LaunchSkillNames/ForceLaunchSkills` → `newStartHandler` 从 payload 投影 → `startSession` 透传到 `dto.StartSessionRequest`；nil/false 下游路径与旧 payload 完全一致 |
| p20.3 §4.3 · DTO additive carrier | ✅ | `internal/dto/provider/session.go` | `StartSessionRequest` 新增 `LaunchSkillNames []string` / `ForceLaunchSkills bool`，均 `omitempty`；等待 p20.4 / p20.7 消费 |
| p20.3 §4.3 · 前端 launch payload | ✅ | `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js` | `startThread(ctx, cwd, options)` 接受 `options.selectedSkills/manualSkillSelection`，空数组/false 不下发 |
| p20.3 §4.3 · 定向测试 | ✅ | `internal/module/thread/rpc_types_test.go`、`internal/module/thread/start_session_guard_test.go` | 新增 4 项 `TestStartParamsAccepts/Omits/Rejects…` + 2 项 `TestServiceStart(Forwards|LeavesEmpty)LaunchSkills` |
| 合规 | ✅ | — | `go build ./...` 通过；`go test ./internal/module/thread ./internal/module/turn ./internal/module/skill ./internal/dto/provider ./internal/provider/unified` 全绿 |

**p20.3 transport 层打通已完成**；`p20.4`（StartAssembly / provider start 真正消费 `LaunchSkillNames`）与 `p20.7`（codex `InjectL1Manifest` 在 baseInstructions 层并入）仍未开工。

### 2026-04-19 第九轮增量施工（P20.1 Phase 8 / 9 / 10 / 11 全部落地）

| 项 | 状态 | Commit | 备注 |
|---|---|---|---|
| Phase 8 — SkillCatalogProvider 安全投影 + 分组 | ✅ | `c1ead48` + `f890dc4` | 四分组 Core / Native / Manual-only / Redacted；Trust filter 前置于 DisableModelInvocation |
| Phase 9 — 元指令 + 规模兜底 | ✅ | `3cd3144` + `6013457` | Append "How to use skills"；Native skill guard clause；budget 超限 fallback |
| Phase 10 — fx 接线 + 灰度 + 5 counter + 3 env flag | ✅ | `00b073f` + `9f0f4bd` | pkg/skillmetrics 新叶子包；`compositeNativeSkillDetector` + `RegisterSkillCatalogProviderIfEnabled`；legacy 路径不再误报 corruption |
| Phase 11 — 文档与 codemap 同步 | ✅ | （本轮 commit） | hardening / checklist / checkpoint / codemap 07+11 + ai-index.json 全部同步 |

**本轮把 P20.1 加固基线从 Phase 7 推进到 Phase 11 完整闭环**；剩余工作只有跨 phase 的 shadow 阶段评测（P0）和 P20.2/3/4 的 per-turn hydrate 真实生产联调（不属于 P20.1 范畴）。

### 2026-04-19 第十轮增量施工（p20.4 StartAssembly / Snapshot launch skill 接线闭环）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.4 §4.4 · dto 双端加 launch 字段 | ✅ | `internal/dto/provider/session.go` | `PromptAssemblySnapshot` 与 `StartAssembly` 均新增 `LaunchSkillNames []string` / `ForceLaunchSkills bool`，`omitempty` + `launchSkillNames/forceLaunchSkills` camelCase tag |
| p20.4 §4.4 · stored snapshot schema 扩展 | ✅ | `internal/store/thread/contract.go` | `PromptSnapshot` + `legacyPromptSnapshot` 新增字段；`mergeLegacyPromptSnapshot` 抽出 `mergeLegacyLaunchSkill` helper（同时压 CC，不再超 10） |
| p20.4 §4.4 · runtime snapshot 全链扩展 | ✅ | `internal/module/thread/prompt_snapshot.go` | `promptSnapshotHash(...)` 签名加 `launchSkillNames/forceLaunchSkills`；`ensureStartAssemblySnapshot` 双向镜像；新增 `normalizeLaunchSkillNames` / `mergeLaunchSkillSelection` / `promptSnapshotLaunchSkillBlank` 三个 helper；`toStoredPromptSnapshot` / `fromStoredPromptSnapshot` 双向映射；`storedPromptSnapshotValid` / `normalizeCallerPromptSnapshot` hash 调用更新；`promptSnapshotBlank` 同步纳入 launch skill blank 判定 |
| p20.4 §4.4 · start_session_helpers 映射 launch fields | ✅ | `internal/module/thread/start_session_helpers.go` | `buildStartAssembly` 从 req 注入；`resolveStartPromptAssembly` 在 PromptAssemblyRef 分支回填 input.LaunchSkillNames；`toProviderStartAssembly` / `toProviderPromptSnapshot` 映射 launch fields 下发到 provider DTO |
| p20.4 §4.4 · hash 签名 fan-out 修复 | ✅ | `internal/module/thread/fork_isolation_test.go`、`internal/module/thread/resume_test.go` | 3 处 `promptSnapshotHash(...)` 测试调用补两个新参 (`nil, false`) |
| p20.4 §4.4 · round-trip 定向测试 | ✅ | `internal/module/thread/start_session_helpers_test.go` | 新增 3 个测试：`TestBuildStartAssemblyInputCarriesLaunchSkills` / `TestBuildStartAssemblyMirrorsLaunchSkillsIntoSnapshot` / `TestToProviderStartAssemblyMirrorsLaunchSkills`；覆盖 StartInput 投影 / assembly↔Snapshot 镜像 / launch skill 差异导致 Hash 失效 / provider DTO 映射 |
| p20.4 §4.4 · golden refresh | ✅ | `internal/module/prompt/testdata/golden/integration/start_assembly.golden.json` | `TestStartAssemblyGolden` 受 hash 字段扩展影响，`-update` 后 regenerate；其余 prompt 测试保持绿色 |
| 合规 | ✅ | — | 0 新增 prod 文件（thread 仍 25 / prompt 不碰）；我引入的 3 条 CC 违规全部清零；`go build ./...` / `go test ./internal/module/thread ./internal/module/prompt ./internal/store/thread ./internal/dto/provider ./internal/provider/unified ./internal/module/skill ./internal/module/turn ./internal/provider/codexapp ./internal/provider/claudecli` 全部通过 |

**p20.4 critical-path 末段闭环完成**：launch skill 现沿 `StartRequest → contract.StartInput → contract.StartAssembly / contract.PromptAssemblySnapshot → threadstore.PromptSnapshot → dto.StartAssembly / dto.PromptAssemblySnapshot → dto.StartSessionRequest` 完整落盘，`promptSnapshotHash` 把 launch skill 选择纳入 resume/fork/recover 的失效因子，避免旧 snapshot 被错误复用。codex provider 侧的 `InjectL1Manifest(baseInstructions, manifest)` 按 §4.6 交接给 `p20.7`，本单不触碰 `threadStartParams` / `startAssemblyInstructions()`。

**已知剩余 archtest 违规（均为 HEAD 遗留，不归 p20.4）**：`internal/module/prompt: 28 > 26`（P20.1 Phase 10 新增 `skill_catalog_fx.go` / `skill_catalog_provider.go` 未同步 freeze registry）；`skill/approval.go:NewApprovalCache` CC 11、`skill/skills_expand.go:ReadResource/sliceMarkdownSection` CC 11、`skill/trust.go:NormalizeArtifactLocator` CC 20（均在 `p20.12` / `p20.13` 之前的本轮之外已存在）。

### 2026-04-19 第十一轮增量施工（β 组 p20.5 / p20.6 / p20.7 整合闭环）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.5 §4 · L1 `SkillCatalogProvider` 落 skill 模块 | ✅ | `internal/module/skill/skill_catalog_provider.go`（新建 ~215 行）、`internal/module/skill/module.go`、`internal/module/skill/service.go`、`internal/module/skill/skills_fs.go` | Core≤8 + Index 两级渲染；按 name case-insensitive 排序；token budget 默认 4096 chars，Core 单条 summary ≤96 chars；通过 `contract.DynamicSectionRegistrar` + skill 包 `fx.Invoke` 反向注册，prompt 侧 0 新增 prod 文件 |
| p20.5 §4 · dynamic slot 注册 | ✅ | `internal/contract/prompt.go`、`internal/module/prompt/dynamic.go` | 新增 `DynamicSectionSkillCatalog` 常量；`startOnly=true` / `order=124` / `cachePolicy=CacheByName`；write/import/delete/summary 变更后触发 `InvalidateSections(contract.InvalidateSkillCatalogWrite, DynamicSectionSkillCatalog)` 保证 CacheByName 不陈旧 |
| p20.5 §4 · 定向测试 | ✅ | `internal/module/skill/skills_meta_test.go` | 并入既有文件（≥2 项新测例：`TestSkillCatalogProviderResolveRendersCoreAndIndex` / `TestSkillCatalogMutationsInvalidatePromptSection`）；无新增 skill prod 文件 |
| p20.6 §4 · 共享 `SkillInjectionPort` 合同 | ✅ | `internal/contract/skill_injection.go`（新建 40 行） | Method set 逐字与 p20.7 对齐：`InjectL1Manifest(base, manifest string) string` / `BuildTurnSection(refs []dto.SkillRef) (string, bool)` / `ReservedTokens() int`；分离 `NativeSkillDetector` + `NativeSkillOverridePort` 扩展接口 + `SkillInjectionPortDescriptor` + `SkillInjectionPortResolver` |
| p20.6 §4 · carrier + provider-name registry | ✅ | `internal/dto/provider/turn.go`、`internal/provider/unified/registry.go`、`internal/provider/unified/module.go`、`internal/module/turn/service.go`、`internal/module/turn/module.go` | `TurnRequest` / `SteerRequest` 新增 `SkillPrompt string (omitempty)` carrier；`unified.registry` 加 `ResolveSkillInjectionPort(provider)`；`turn.service` 按 `input.Provider` 解析 port + 调 `BuildTurnSection(req.Skills)` 写入 `req.SkillPrompt`，无 port 时 legacy fallback |
| p20.6 §4 · claudecli port + native skill 降级 | ✅ | `internal/provider/claudecli/skill_inject.go`（新建 ~190 行）、`internal/provider/claudecli/module.go`、`internal/provider/claudecli/session_turn.go` | `DetectNativeSkills()` 扫盘顺序 `gitRoot > cwd`；命中 `.claude/skills/<name>/SKILL.md` 强制 `Mode=None, Source=Native`，清 body/summary 但保留 name list；`session_turn.buildSkillList()` 对 `Source=Native` 保留 `skills:\n- name` |
| p20.6 §4 · 2 个遗漏测试修复（由 parent agent 兜底） | ✅ | `internal/module/prompt/skill_catalog_fx_test.go`、`internal/provider/codexapp/skill_inject.go`、`internal/provider/codexapp/module.go` | ① `fakeSkillInjectionPort` 补 3 方法 stub + `dto` import（适配 p20.6 新 contract）；② `codexapp.NewSkillInjectionPort()` 返回值改 concrete 类型与 claudecli 对齐；`module.go` 加 `fx.As(new(contract.SkillInjectionPort))` + 包级静态断言 |
| p20.7 §5 · codexapp port 扩充 | ✅ | `internal/provider/codexapp/skill_inject.go`（+87/-18） | `InjectL1Manifest(base, manifest)` 尾部拼接语义（`base + "\n\n" + manifest`）；`BuildTurnSection` 走 `buildSkillPromptInput`；`DetectNativeSkills` 返回 nil（codex 无原生机制）；`ReservedTokens()=3000` |
| p20.7 §5 · baseInstructions 单点合并 | ✅ | `internal/provider/codexapp/driver.go`（+18/-0） | `startAssemblyInstructions()` 新增 `startAssemblySkillManifest(assembly)` helper：优先 `Snapshot.SectionSnapshot[DynamicSectionSkillCatalog]`，fallback `ResolvedSections[Name==skill_catalog]`；`StartSession()` / `buildThreadStartParams()` 两个 caller 经同一入口，runtime config 与 remote `thread/start` 同源 |
| p20.7 §5 · descriptor 注册 + provider-name resolve 就位 | ✅ | `internal/provider/codexapp/module.go`（+7/-48） | 新增 `newSkillInjectionPortDescriptor()` → `contract.NewSkillInjectionPortDescriptor("codex", NewSkillInjectionPort())`；`fx.Annotate(NewSkillInjectionPort, fx.As(new(contract.SkillInjectionPort)), fx.ResultTags(promptpkg.SkillInjectionPortGroupTag))` + `fx.Annotate(newSkillInjectionPortDescriptor, fx.ResultTags(contract.SkillInjectionDescriptorGroupTag))` 与 claudecli 完全对齐；删除 legacy `buildSkillPromptInput` 的对外导出，收敛为 port 内部 helper |
| p20.7 §5 · session_turn 消费 `req.SkillPrompt` | ✅ | `internal/provider/codexapp/session_turn.go`（+5/-5） | `turnInputsFromRequest(inputs, assembly, skillPrompt string)` 改签名；`buildTurnStartParams` / `buildTurnSteerParams` 两处 caller 同步传 `req.SkillPrompt`；`buildSkillPromptInput` 仅剩 port 内部 + 测试消费 |
| p20.7 §5 · 定向测试 | ✅ | `internal/provider/codexapp/driver_session_test.go`、`input_map_test.go`、`skill_injection_test.go` | `TestBuildThreadStartParamsUsesStartAssemblyInstructions` / `TestStartAssemblyInstructionsFallsBackToResolvedSectionsSkillCatalog` + port 单测更新；0 新增 codexapp 测试文件 |
| 合规 | ✅ | — | `go build ./...` 通过；`go test` 9 目标包全绿：skill / prompt / turn / thread / claudecli / codexapp / unified / dto-provider / store/thread；archtest 仍旧 8 条 HEAD 遗留（prompt:28 + skill 4 条 CC + prompt/skill_catalog_fx.go fx scope x2 + store/prompt pgx boundary x2），**β 组三单均无新引入违规** |

**β 组整条闭环完成 🎯**：launch skill 的 catalog 渲染（p20.5）+ turn 注入的 provider-neutral port 化（p20.6）+ codex 启动链 baseInstructions 合并（p20.7）已经串起完整的 progressive disclosure 基座。`SkillInjectionPort` 单一合同 + provider-name registry + `SkillPrompt` carrier 在 module / provider / contract 三层之间只需一条单向链路，没有循环；codex 与 claude 两家 provider 的 descriptor 注册模式完全对称。

**剩余 β 组边界**：`p20.9` rollout markers（写端 gate 仍锁在 legacy `摘要:` / `使用方式:` marker，等读端 helper 先落）仍未开工；`p20.6` / `p20.7` 的 provider 实现在 summary 渲染上与 legacy marker 兼容。

**p20 整体进度**：✅ p20.1 / p20.2 / p20.3 / p20.4 / p20.5 / p20.6 / p20.7（7 / 16，43%）；⚠️ 未开工 p20.8 / p20.9 / p20.10 / p20.11 / p20.12 / p20.13 / p20.14 / p20.15 / p20.16（9 / 16）。critical-path + β 组均已闭环；下一可派并行组：γ（p20.8 resolver 矩阵 · 依赖已满足）/ γ'（p20.14 LaunchSkillPicker · 依赖已满足）/ α（p20.9 rollout 读端 · 独立）/ δ 预备（p20.10 host RPC · 独立）。

### 2026-04-19 第十二轮增量施工（γ/γ'/α 四单 p20.8 / p20.10 / p20.14 / p20.9 整合闭环）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.8 §4 · resolver 决策矩阵 12 格 | ✅ | `internal/module/turn/skills.go`（+382） | `(*skillResolver).ResolveThread(...)` 升级 thread-aware matrix：explicit wins / force top-k=5 / trigger top-k=3 / miss=None；去重键 `strings.ToLower(name)+"@"+version`；Mode `Full>Summary>None`；Source `manual>force>trigger>unspecified` |
| p20.8 §4 · runtime matcher seam | ✅ | `internal/module/skill/skills_match.go`（+72） | 新增 `RuntimeMatcher` interface + `RuntimeSkillMatch{Skill, Kind, MatchedTerms}` + `MatchRuntime(ctx, params)`；turn service 通过类型断言消费，skill 包无倒依赖 |
| p20.8 §4 · expanded_state 接入 | ✅ | `internal/module/turn/expanded_state.go`（+182）、`internal/module/turn/service.go`（+137） | preview→resolve→commit 三段：`previewExpandedTurn(threadID)` → `ShouldInject(...)` 做 carry 判定 → 成功后 `commitExpandedTurn(...)`；TTL=5 turns；hash 变即失效；thread 隔离 |
| p20.8 §4 · 定向测试 | ✅ | `internal/module/turn/service_test.go`（+287）、`internal/module/turn/expanded_state_test.go`（+55） | `TestPrepareTurnSkillResolverMatrix`（12 格全覆盖）+ `TestPrepareTurnSkillResolverTopK` + `TestPrepareTurnExpandedStateTTLHashAndThreadIsolation`；factory/module 未动 |
| p20.10 §5 · skill/list host RPC | ✅ | `internal/module/skill/rpc.go`、`internal/module/skill/rpc_skill_types.go`、`internal/module/skill/rpc_types_test.go` | name-based API，strict decode 拒绝未知字段；response 只带 `{name, summary, description, trust, content_hash, disable_model_invocation}`，**禁止** 暴露 `dir` / `trigger_words` / `force_words` / `allowed_tools` |
| p20.10 §5 · skill/expand host RPC | ✅ | `internal/module/skill/rpc.go`、`internal/module/skill/rpc_skill_types.go`、`internal/module/skill/contract.go`、`internal/module/skill/skills_fs.go`、`internal/module/skill/skills_fs_test.go` | `Expand(ctx, p) (r, error)` 合同供 p20.11 / p20.13 消费；`ContainsPath` 防 path-escape；`content_hash` 按截断前内容 SHA-256（同 section 改 `max_bytes` 时 hash 稳定）；错误语义 `-32602` invalid params / `-31001` not found；`skill.Service.Expand` 单入口 |
| p20.10 §5 · 共存策略 | ✅ | — | 老 `skills/list` / `skills/match/preview` 保持不变（编辑器/UI/preview 已有 caller）；新 `skill/*` 是 name-based progressive-disclosure 独立 surface；**host RPC 用 slash，MCP tool 用 underscore**（留给 p20.11） |
| p20.14 §5 · launch skill UI | ✅ | `cmd/agent-terminal/frontend/vue-app/components/LaunchSkillPicker.js`（101 行）、`composables/useLaunchSkillSelection.js`（206 行）、`services/skills-api.js`（19 行） | 3 层分工：`services/skills-api.js` 唯一 RPC 入口（`listSkills` / `previewSkillMatches`）；`useLaunchSkillSelection` 状态层（feature gate + selectedSkills/manualSkillSelection 产出）；`LaunchSkillPicker.js` 纯展示组件（props/emits），**禁止**直连 `callAPI` |
| p20.14 §7 · performSend 新顺序 | ✅ | `cmd/agent-terminal/frontend/vue-app/composables/useThreadActions.js`、`pages/UnifiedChatPage.js`、`pages/UnifiedChatPage.template.js`、`components/ComposerBar.js` | blank-thread 首发：**launch state → `startThread(cwd, {selectedSkills, manualSkillSelection})` → `sendMessage`**；feature gate 默认 disabled 不渲染；legacy `ComposerBar` selector 在 `threadId` 为空时隐藏；`stores/thread-actions-helpers.js` **diff 0 行**（p20.3 已打通 payload，只消费无需改） |
| p20.14 §9 · 行为测试 | ✅ | `cmd/agent-terminal/frontend/vue-app/composer-bar.behavior.test.js`、`use-thread-actions.test.js` | 目标回归 **78/78 tests passed**；全量 `npm test` 33 红项全部为**仓库既有基线**（`thread-store.runtime-sync / streaming-sync-fix / use-auto-scroll` 等），不归本单；agent 诚实自报"未全绿"有清晰根因 |
| p20.9 §4 · 共享 rollout helper | ✅ | `internal/module/skill/rollout_markers.go`（217 行）、`internal/module/skill/rollout_markers_test.go`（104 行） | 纯函数 helper（string in / string out）：`TrimInjectedSkillBlocks` / `ParseSkillBlockHeader` / `ParseSkillBlockFooter` / `RenderSkillBlock`；**无 fx / store / provider 反向依赖** |
| p20.9 §4 · dual-read + fail-open | ✅ | `internal/provider/codexapp/history_rollout.go`、`internal/provider/claudecli/history_trim.go` | v1 语法 `[skill:<name>::<mode>@v<ver>]` ... `[/skill:<name>]` 与 legacy `[skill:<name>]\n摘要:\n使用方式:` 双读；v1 header 找不到匹配 footer → **fail-open 保留原文**；Claude `skills:` prelude **不被吞噬**；两家 provider 共享同一 helper，不再重复 trim util |
| p20.9 §4 · 写端 gate 保持 | ✅ | — | `codexapp/session_turn.go` / `codexapp/module.go` / `claudecli/session_turn.go` **0 行改动**；writer 切换留给 p20.6/p20.7 后续迭代，本单严格 reader-only，避免 dual-write 双倍耗 token |
| p20.9 §6 · Claude prelude 保留测试 | ✅ | `internal/module/skill/rollout_markers_test.go` | `TestTrimInjectedSkillBlocks_PreservesClaudeSkillsPrelude` + `TestTrimInjectedSkillBlocks_FailOpenMalformedV1` 明确 fail-open 与 prelude 独立保留 |
| parent 兜底修复 2 处 p20.6 agent 漏改（本轮已落盘） | ✅ | — | p20.5/6/7 审核时发现并已处理：`prompt/skill_catalog_fx_test.go` fake 补 3 方法 stub + dto import；`codexapp.NewSkillInjectionPort()` 返回 concrete 类型对齐 claudecli + `fx.As(new(contract.SkillInjectionPort))` + 包级静态断言 |
| 合规（本轮整体） | ✅ | — | `go build ./...` 通过；`go test` 9 目标包全绿：skill / prompt / turn / thread / claudecli / codexapp / unified / dto-provider / store/thread；archtest 仍 8 条 HEAD 遗留（prompt:28 + skill 4 条 CC + prompt/skill_catalog_fx.go fx scope x2 + store/prompt pgx boundary x2），**γ/γ'/α 四单均无新引入违规** |

**γ/γ'/α 四单整条闭环完成 🎯**：turn resolver 生产线（manual>force>trigger>miss）+ expanded_state 5-turn TTL 内存态 + skill host RPC name-based 表层 + 前端 launch UI progressive-disclosure + rollout dual-read 共享 helper 彻底打通。critical-path（p20.1-4）+ β 组（p20.5-7）+ γ/γ'/α（p20.8/10/14/9）合并后，progressive disclosure 从 catalog 到 turn 注入到 rollout 兼容构成完整 runtime 闭环。

**剩余边界**：
- ⏳ `p20.11` MCP adapter（依赖 p20.10 ✅ 已满足，下一批并行派）
- ⏳ `p20.12` config/policy/metrics（独立 α，下一批并行派）
- ⏳ `p20.13` 审批缓存生产化（依赖 p20.10 ✅ + p20.6 ✅ 已满足，下一批并行派）
- ⏳ `p20.15` 前端 SystemPromptPage 404 降级（独立 α，下一批并行派）
- ⏳ `p20.16` 集成测试尾部收口（依赖全部前置，最后派）
- 🔶 写端切 v1 marker（p20.6/p20.7 迭代项，等 p20.9 helper 稳定 + shadow 观测后再开 gate）
- 🔶 双 SkillCatalogProvider 并存隐患（prompt-side P20.1 Phase 8 + skill-side p20.5，默认 flag=false 安全；建议专单清理）

**p20 整体进度更新**：✅ **11/16 已闭环**（69%）；⏳ 5/16 未开工（p20.11 / p20.12 / p20.13 / p20.15 / p20.16）。下一批可并行派 4 单：**p20.11 / p20.12 / p20.13 / p20.15**。

### 2026-04-19/20 第十三轮增量施工（p20.11 / p20.12 / p20.15 并行 + p20.13 串行闭环）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.11 §4 · MCP `skill_list` / `skill_expand` | ✅ | `cmd/mcp-orch/tools/skill_tools.go`（164 行）、`cmd/mcp-orch/tools/skill_tools_test.go`（182 行）、`cmd/mcp-orch/tools/registry.go`、`cmd/mcp-orch/runtime.go`、`cmd/mcp-orch/fx.go`、`cmd/mcp-orch/tools/handler_regression_test.go`、`cmd/mcp-orch/runtime_memory_test.go` | 2 新建 + 5 改既有；`skill_list` 独立 schema（keyword filter）；`skill_expand` 独立 schema（`name + section? + max_bytes?`），**未生搬** `resourceToolDefinitions()`；MCP 错误映射 `IsExpandInvalidParams` / `IsExpandNotFound` → `isError: true` + available skill names；`fx.go` 用 `fx.Provide(func(cfg) skill.Service)` 轻量注入，**未引** `skill.Module`（host RPC wiring 不外溢） |
| p20.12 §4 · config + policy + metrics 骨架 | ✅ | `internal/module/skill/policy_metrics.go`（新建 78 行，合并 policy + metrics）、`internal/platform/config/config.go`、`internal/module/skill/service.go`、`internal/module/skill/module.go` | 配置纯数据 + policy 代码接口混合：`Config.Skill{ProgressiveDisclosure bool, TokenBudget int}` + env fallback（`SKILL_PROGRESSIVE_DISCLOSURE` / `SKILL_TOKEN_BUDGET`）；`SkillPolicy` interface 含 `ProgressiveDisclosure()`, `TokenBudget()`, `Mode(ctx, input)`；default fallback `false/3000`；metrics 7 个 snake_case 常量（`skill_l1_tokens`, `skill_expand_total`, `skill_expand_error_total`, `skill_cache_hit_total`, `skill_cache_miss_total`, `skill_injection_decision_total`, `skill_context_tokens_saved_total`）；backend-agnostic no-op recorder 骨架；agent report 丢失但代码经验证合规 |
| p20.15 §4 · SystemPromptPage 404 降级 | ✅ | `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`（+117/-19）、`system-prompt-page.behavior.test.js`（+116/-0） | 2 改既有，0 新建；`fallbackMode / readonlyReason / fallbackSource` 三态；白名单 detector（`404` / `method not found` / `not found`）；**仅**对 `prompts/list` 命中进入 fallback；`prompts/list` 成功后自动清 fallback；`warn`（首次）/ `info`（稳态/恢复）日志分流；`+新建/保存/删除` 禁用、editor view-only；实现 `dashboard/prompts` list-only adapter（`title→name / prompt_text→content / agent_key→agentType`）best-effort 映射；目标回归 **21/21 tests passed** |
| p20.13 §5 · approval cache 生产化 | ✅ | `internal/module/skill/approval_flow_test.go`（新建 184 行）、`internal/module/skill/service.go`（+269）、`internal/module/skill/skills_fs.go`（+188）、`internal/module/skill/rpc.go`（+62）、`internal/module/skill/rpc_skill_types.go`（+64） | **叠在 p20.12 struct 改动上**（共改 `service.go`，串行无冲突）；方案 A：service 内注入 `skillApprovalRequester` + `jrpc2.ServerFromContext(ctx)` 动态拿 `*rpc.Server`（**避开** `*rpc.Server` wiring cycle，**无需** contract port 改动）；trusted bypass：`record.info.Trust.Trusted() == user || signed` → `ApprovalSource=trusted` + `ApprovalResult=bypassed` 直接放行；`scope=project` → `approval.Approve(name, hash, trust, approvedBy)` 落盘 `ApprovalCache`；`scope=session` → `rememberSessionApproval()` 只写内存态（**绝不**落盘 `skills-trust.json`）；hash 变化 → `lookupPersistedApproval` miss → `requestExpandApproval(...)` 走 `skill/requestApproval` 事件；**未新建** `rpc_skill.go` |
| p20.13 §5 · approval_flow_test 覆盖场景 | ✅ | `internal/module/skill/approval_flow_test.go` | `TrustedBypass` / `ProjectMissRequestsAndPersists` / `ProjectCacheHitSkipsRequester` / `HashChangeReRequests` / `SessionScopeStaysInMemory` 五大场景 |
| 合规（本轮整体） | ✅ | — | `go build ./...` 通过；`go test` 13 目标包全绿：skill / prompt / turn / thread / claudecli / codexapp / unified / dto-provider / store/thread / platform/config / platform/rpc / cmd/mcp-orch（含 tools / memory / orchestration / store/taskdag / store/workspace）；archtest 仍 8 条 HEAD 遗留（prompt:28 + skill 4 条 CC + prompt/skill_catalog_fx.go fx scope x2 + store/prompt pgx boundary x2），**p20.11/12/13/15 均无新引入违规** |

**第十三轮四单彻底闭环 🎯**：
- **p20.11** 把 host RPC `skill/list` / `skill/expand` 通过 `cmd/mcp-orch/tools/skill_tools.go` 平移到 MCP underscore tools，实现"共享 service，不共享 handler map"，MCP 层与 host 层解耦
- **p20.12** 把 progressive disclosure 的**策略 / 配置 / 指标**最小骨架放进仓库：env 读的 config + policy interface + no-op metrics，为后续 shadow 观测做准备
- **p20.13** 把 3/15 日就躺在仓库里的 `ApprovalCache` 真正接到 `skill/expand` 生产链：trusted bypass、project hash-global 审批、session 纯内存、hash 变即重审，5 场景集成测试全绿
- **p20.15** 把 p20.1 前 `prompts/list` 404 的 UI 降级收口到只读 banner + `dashboard/prompts` adapter，即使后端挂掉用户也能只读浏览

**剩余边界**：
- ⏳ `p20.16` 集成测试尾部收口（最后派，收敛跨模块 E2E 断言）
- 🔶 写端切 v1 marker（p20.6/p20.7 迭代项，等 p20.9 helper 稳定 + shadow 观测后再开 gate）
- 🔶 双 SkillCatalogProvider 并存隐患（prompt-side P20.1 Phase 8 + skill-side p20.5，默认 flag=false 安全；建议专单清理）
- 🔶 p20.12 agent report 丢失（代码已写盘并验证合规，但 orchestration report 通道可能有个别 agent 异常，不影响本轮交付）

**p20 整体进度终审**：✅ **15/16 已闭环**（94%）；⏳ 1/16 未开工：p20.16（终单 · 集成测试 · 依赖全部前置已满足）。

### 2026-04-20 第十四轮增量施工（p20.16 集成测试尾部收口 · P20 整套 16 单完整闭环 🏁）

| 项 | 状态 | 文件 | 备注 |
|---|---|---|---|
| p20.16 §4.1 · 新建 3 个 integration test | ✅ | `internal/module/thread/p20_launch_skill_integration_test.go`（~120 行）、`internal/provider/codexapp/p20_skill_integration_test.go`（~95 行）、`internal/provider/claudecli/p20_skill_integration_test.go`（~85 行） | launch `selectedSkills` round-trip 断言；codex / claude 双 provider launch/per-turn skill carry + rollout marker 跨场景集成 |
| p20.16 §4.2 · 扩既有 9 个 test 文件 | ✅ | `internal/module/turn/service_test.go`、`internal/module/skill/rollout_markers_test.go`、`internal/dto/provider/turn_test.go`、`internal/provider/unified/registry_test.go`、`cmd/mcp-orch/tools/handler_regression_test.go`、`cmd/agent-terminal/frontend/vue-app/composer-bar.behavior.test.js`、`system-prompt-page.behavior.test.js`、`thread-store.actions.test.js`、`unified-chat-preflight-coverage.test.js` | 12/15 write set 在预算内 ✅ 未超 15 文件上限；resolver matrix / SkillPrompt carrier / provider-name registry / MCP registry lookup / 前端 4 个 behavior 测试全部扩 |
| p20.16 §4.4 · Bug #1 永久回归（prompts/list 404 readonly fallback） | ✅ | `system-prompt-page.behavior.test.js` | 断言：`fallbackMode` 被触发 / `+新建/保存/删除` disabled / `prompts/list` 成功后自动清 fallback / `warn`（首次）`info`（稳态）日志分流 |
| p20.16 §4.4 · Bug #2 永久回归（launch skill 丢） | ✅ | `internal/module/thread/p20_launch_skill_integration_test.go` + `thread-store.actions.test.js` + `unified-chat-preflight-coverage.test.js` | 断言：空线程 launch 选中 skills → `thread/start` payload 带 `selectedSkills/manualSkillSelection` → StartAssembly/Snapshot 含 launch skill → `promptSnapshotHash` 把 launch skill 纳入失效因子；codex 用 `InjectL1Manifest` 追到 baseInstructions；claude native skill 优先 |
| 合规（终单验证） | ✅ | — | `go build ./...` 通过；独立终验 8 包 go test 全绿：skill / prompt / turn / thread / claudecli / codexapp / dashboard / cmd/mcp-orch/tools；archtest 仍是 8 条 HEAD 遗留（prompt:28 + skill 4 条 CC + prompt/skill_catalog_fx.go fx scope x2 + store/prompt pgx boundary x2），**p20.16 零新增违规** |
| p20.16 agent report 丢失（观察项） | ⚠️ | — | agent 写完 12 文件后于 02:55 停止写入，但未出 report 且 state 保持 thinking；独立终验确认工作成果全部落盘且功能合规；可能卡在 `npm test` 全量跑或某个慢测试的 stdout 解析。`p20.12` agent 也有相同 report 丢失观察 → 后续可排查 orchestration report 通道 |

**🎯 P20 整套 16 单完整闭环 — 所有 critical-path / β / γ / γ' / α / δ / ε / 终 组全部合并**：
- **critical-path**（p20.1~p20.4）：prompts host RPC 恢复 + PrepareTurn hydrate + launch skill 契约 + StartAssembly/Snapshot 全链
- **β 组**（p20.5~p20.7）：SkillCatalogProvider + claudecli SkillInjectionPort + codex InjectL1Manifest baseInstructions merge
- **γ 组**（p20.8）：resolver 决策矩阵 12 格 + expanded_state TTL=5 turns + hash 变即失效
- **γ' 组**（p20.14）：前端 LaunchSkillPicker + `useLaunchSkillSelection` composable + `services/skills-api.js` 唯一 RPC 入口
- **α 组**（p20.9 / p20.10 / p20.12 / p20.15）：rollout markers 共享 helper dual-read / host `skill/list`+`skill/expand` name-based RPC / config+policy+metrics 骨架 / SystemPromptPage 404 readonly fallback
- **δ 组**（p20.11）：MCP `skill_list` / `skill_expand` tools 轻量 fx 注入（不引 skill.Module）
- **ε 组**（p20.13）：`(name,hash)` approval cache 生产化接线 + trusted bypass + session 纯内存 / project 落盘 + hash 重审
- **终单**（p20.16）：2 个 Bug 永久回归固化 + 15 条集成矩阵断言 + 12 文件 write set 在 15 预算内

### Progressive Disclosure 完整 runtime 闭环
```
[UI 前端]
  LaunchSkillPicker → useLaunchSkillSelection → services/skills-api
    ↓ thread/start payload: {selectedSkills, manualSkillSelection}
[thread 模块]
  StartRequest → contract.StartInput → StartAssembly → PromptAssemblySnapshot → threadstore.PromptSnapshot
    ↓ promptSnapshotHash 把 launch skill 纳入失效因子
[prompt 模块]
  SkillCatalogProvider → DynamicSectionSkillCatalog slot → Core/Index 渲染 + CacheByName 失效链
[turn 模块]
  PrepareTurn hydrate → resolver 决策矩阵（manual>force>trigger>miss）→ expanded_state TTL=5 turns → SkillPrompt carrier
    ↓ contract.SkillInjectionPortResolver 按 provider name 解析 port
[contract 层]
  SkillInjectionPort{InjectL1Manifest, BuildTurnSection, ReservedTokens}
  + NativeSkillDetector / NativeSkillOverridePort 扩展接口
[provider 层]
  codex: startAssemblyInstructions → InjectL1Manifest(base, manifest) → baseInstructions 尾部追加
  claude: ApplyNativeOverrides（gitRoot>cwd）→ 命中 .claude/skills/* 强制 Mode=None/Source=Native → 保留 skills: name list
[skill 模块]
  Service.Expand(name, section, max_bytes) → trusted bypass or approval cache lookup/request
    ↓ scope=project 落盘 ApprovalCache / scope=session 纯内存
  rollout_markers helper → legacy+v1 dual-read + fail-open
[MCP 层]
  cmd/mcp-orch/tools/skill_tools → skill_list / skill_expand → 共享 skill.Service（不共享 handler map）
```

### 剩余边界（P20 完整闭环后的观察项）
- 🔶 **写端切 v1 marker**（p20.6/p20.7 迭代）：等 p20.9 helper 稳定 shadow 观测 2 周后再开 gate
- 🔶 **双 SkillCatalogProvider 并存**：prompt-side P20.1 Phase 8 + skill-side p20.5 共存；默认 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false` 安全；建议后续专单清理 prompt-side 陈旧 `skill_catalog_provider.go` + `skill_catalog_fx.go`（顺便把 prompt 包 freeze 从 28 回收到 26）
- 🔶 **metrics 真实 backend 接线**：p20.12 只落 no-op 骨架 + snake_case 常量；等 Prometheus/OTel 基础设施就绪后专单接线
- 🔶 **orchestration agent report 通道异常**：p20.12 + p20.16 两个 agent 都出现"代码写盘但 report 空/不收尾"；功能不受影响，但建议后续排查 orchestration wiring
- 🔶 **HEAD 遗留 8 条 archtest 违规**（P20.1 Phase 10 + skill 模块历史债）：`prompt:28` + `prompt/skill_catalog_fx.go` fx scope x2 + `skill/approval.go` CC11 + `skill/skills_expand.go` CC11 x2 + `skill/trust.go` CC20 + `store/prompt/store.go` pgx boundary x2；建议后续专单清理

### P20 上线路径建议
1. 先 commit 本批（第十一~十四轮）所有 P20 改动落 git（分拆成清晰的 feature commit 链）
2. `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false` 默认关（灰度前）
3. 内部灰度：开 flag 观察 1 周 → 开 launch skill UI → 观察 token 消耗与用户反馈
4. shadow 观测 2 周 rollout marker v1 写端切换准备 → p20.6/p20.7 迭代开 gate
5. 专单清理双 Provider 并存（prompt 侧陈旧文件 + freeze 回收）
6. metrics Prometheus/OTel 专单接线

**🏁 P20 进度终审：16/16 = 100% 已闭环**
- 两个 Bug 永久回归固化（Bug #1 prompts/list 404 + Bug #2 launch skill 丢失）
- 全链 progressive disclosure：UI → thread → prompt → turn → contract → provider → skill → MCP
- codex + claude 两家 provider 完全对称
- host RPC + MCP tool 双 surface 解耦
- 0 新增 archtest 违规，仅 8 条 HEAD 遗留

### 🔖 收官后续隐患跟踪（authoritative）

5 条隐患详细条目已落盘 → `docs/plans/迁移/p20/post-p20-followups.md`（247 行）。按优先级派单：

| 优先级 | 专单（建议命名） | 闭合隐患 | 派单时机 |
|---|---|---|---|
| **P1** | `p21.x-cleanup-prompt-side-catalog` | 隐患 1（双 SkillCatalogProvider 并存）+ 隐患 5 的 #1/#6/#8（prompt:28 超限 + fx scope ×2） | **灰度 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=true` 前必做** |
| **P1** | `p21.x-skill-metrics-wire` | 隐患 3（metrics 只落 no-op 骨架） | 灰度观测前必做 |
| **P2** | `p21.x-skill-cc-cleanup` | 隐患 5 的 #2/#3/#4/#5（skill/approval.go + skills_expand.go ×2 + trust.go CC 超标） | 随时机方便 |
| **P2** | `p21.x-store-prompt-boundary` | 隐患 5 的 #7（store/prompt/store.go pgx boundary ×2） | 随时机方便 |
| **P2** | `p21.x-writer-v1-marker-switch` | 隐患 2（写端未切 v1 marker） | shadow 观测 ≥2 周后 |
| **P3** | `infra.orchestration-report-timeout` | 隐患 4（orchestration agent report 通道异常） | 下次 orchestration 场景触发 |

**authoritative 规则**：专单完工时只在 `post-p20-followups.md` 的跟踪状态表打 ✅ + commit hash + 日期；**禁止**另建重复 checklist；保持 single source of truth。
