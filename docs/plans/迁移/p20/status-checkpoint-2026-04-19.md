# P20 Status Checkpoint — 2026-04-19

> 口径：把 P20 从“单文件总纲”切成 `p20.1` ~ `p20.16` 可派单任务单；本页只记录 2026-04-19 的落地现状与优先级。

## 1. 四路审查交叉共识（精炼版）

1. **方向保留，施工顺序必须重排。** 原方案的 progressive disclosure 思路不推翻，但当前仓库真相已不是“从零实现”，而是“30% 已落地 / 40% 部分落地 / 30% 未落地（4 / 9 / 10）”，所以必须改成 critical path + 并行支线。
2. **Bug #2 才是 P20 真正 blocker。** UI 强制技能失效不是单点问题，而是 `per-turn 断点 B` 与 `launch 断点 A` 两处断裂叠加；修复顺序必须先 `p20.2`，再 `p20.3`，最后 `p20.4`。
3. **Bug #1 是契约回归，不是“可接受噪声”。** `prompts/list|write|delete` 被前端直接依赖，且 P8 已写明宿主 surface 必须保留，因此应单独成 `p20.1`，不再混入 skill 主链。
4. **其余工作可拆成真正并行单。** rollout marker、RPC、MCP、config/policy/metrics、approval wiring、frontend fallback 都能独立推进；provider 实现与前端 launch UI 则待 critical path 契约稳定后并行收口。

> 旁证：实验 A（`docs/研究材料/实验验证/p20-exp-a-agentsmd-merge.md`）已证明 codex `baseInstructions` 语义高于 AGENTS.md；实验 B（`docs/研究材料/实验验证/p20-exp-b-claudecli-native-skills.md`）已锁定 Claude native skills 必须走“扫盘降级”方案。

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
| MCP tool 注册 `skill_expand` / `skill_list` | `internal/sidecar/orch/tools/` 无命中 | ~~`p20.11`~~ **已废弃**：skill 是宿主独有能力，不属于编排层 |

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
- `p20.11` — ~~MCP tools~~ **已废弃**：skill 是宿主独有能力，不属于 mcp-orch 编排层
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

### 修正 10 — ~~p20.11 显式依赖 p20.10，且直调 `skill.Service`~~ **已废弃**
- **原方案**：MCP 可独立推进，或复用 host `Server.Dispatch()`/handler map。
- **废弃原因**：skill 是宿主主程序独有能力，`cmd/mcp-orch` 是独立二进制，无法直接访问 skill 数据（文件系统 `.agent/skills/`）。编排层拉起的子进程也在宿主中运行，只要提示词有 skill 清单，即可触发 skill 行为，无需额外 MCP 工具入口。
- **受影响文档**：`p20.11`、`README`、`p20.16`

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

### 2026-04-19 第十轮 · 文档状态同步（修正落地口径）

> ⚠️ **口径修正**：本文件 §2 "30% / 40% / 30%（4 / 9 / 10）" 为 P20.1 Phase 8 之前的快照，已严重滞后。以下为截至 `b0d2555`（HEAD）的真实落地状态。

| 任务单 | 状态 | 关闭提交 | 说明 |
|---|---|---|---|
| `p20.1`（Bug #1 prompts handler） | ❌ 未开工 | — | 仍需恢复 `prompts/list\|write\|delete` handler |
| `p20.2`（Bug #2 断点 B） | ✅ 已完成 | `78c6907` | PrepareTurn hydrate + codex name-list fallback |
| `p20.3`（Bug #2 断点 A） | ✅ 已完成 | `cec26fe` | thread/start selectedSkills 契约端到端打通 |
| `p20.4`（launch assembly） | ✅ 已完成 | `b0d2555` | AssembleStart 消费 LaunchSkillNames（pin/force） |
| `p20.5`（SkillCatalogProvider） | ✅ Phase 8-10 完成 | `c1ead48`→`00b073f` | 安全投影 + 元指令 + fx 灰度 + 5 counter |
| `p20.6`（claudecli Port） | ⚠️ 基础落地 | `9b0f7e1`/`9707764` | 契约 + 实现 + native scan；carrier/registry 集成待收口 |
| `p20.7`（codexapp Port） | ⚠️ 基础落地 | `9b0f7e1` | 契约 + 实现；carrier/registry 集成待收口 |
| `p20.8`（resolver 矩阵） | ⚠️ 基础落地 | `3fbed75`/`b12df84` | expanded_state 已落；矩阵 + matcher 待完成 |
| `p20.9`（rollout markers） | ⚠️ 读端落地 | `e5947dc`/`25af3d7` | 读端 helper 已落；provider 切换 + 写端待 p20.6/7 |
| `p20.10`（host RPC） | ❌ 未开工 | — | |
| `p20.11`（MCP tools） | 🚫 已废弃 | — | skill 是宿主独有能力，不属于编排层；子进程在宿主中，skill 通过提示词触发 |
| `p20.12`（config/policy/metrics） | ✅ Phase 10 吸收 | `00b073f`/`9f0f4bd` | `pkg/skillmetrics/` + `prompt/config.go` |
| `p20.13`（审批接线） | ❌ 未开工 | — | |
| `p20.14`（前端 LaunchSkillPicker） | ❌ 未开工 | — | |
| `p20.15`（前端 404 降级） | ❌ 未开工 | — | |
| `p20.16`（集成测试） | ❌ 未开工 | — | |

**修正后口径**：16 个任务单中 **5 个 ✅ 已完成 + 4 个 ⚠️ 基础/读端已落地 + 1 个 🚫 已废弃 + 6 个 ❌ 未开工**。实际完成度约 **55-60%**（代码行数加权更高）。
