# P20 Source Refs Appendix

> 创建时间：2026-04-19 | 更新时间：2026-04-19
> 用途：集中维护 P20 checkpoint / 拆分任务单复用的源码锚点；正文优先引用本附录的 `SRC-*` 条目。
> 2026-04-19 第四轮补修：按 `p20.1` ~ `p20.16` 子单反查清理 **33 条孤儿锚点**，补入 **5 条新锚点**，现保留 **40 条活跃锚点**。

## A. Go 后端

### A.1 DTO / shared contract

<a id="src-a01"></a>
### SRC-A01 — `internal/dto/provider/turn.go:43-117`
- `SkillRef{Version,Mode,Summary,Source}` DTO 已落地；`Prompt` 仍保留 legacy wire 兼容。

<a id="src-b08"></a>
### SRC-B08 — `internal/dto/provider/session.go:55-63`
- `StartSessionRequest` 当前没有 `skills` 字段，launch-time skill 还进不了 provider start DTO。

<a id="src-b10"></a>
### SRC-B10 — `internal/module/thread/contract.go:35-63`
- `thread.StartRequest` 也没有 launch-time skill 载荷；前端即使传值，合同层也无入口承接。

<a id="src-a21"></a>
### SRC-A21 — `internal/contract/prompt.go:109-140`
- `contract.StartInput` 当前无 skill 字段；turn 侧仍只保留单一 `SkillPrompt string`。

<a id="src-a49"></a>
### SRC-A49 — `internal/dto/provider/turn.go:9-19,131-140`
- `TurnRequest` / `SteerRequest` 当前只有 `Skills []SkillRef` + `ManualSkillSelection`；provider-generic `SkillPrompt` carrier 仍缺失，待 `p20.6` 落地。

<a id="src-a52"></a>
### SRC-A52 — `internal/contract/approval.go:5-16`
- 当前 contract 只有 `ApprovalResponder` / `ApprovalDecision`；尚无“请求审批” requester port，`p20.13` 若走 contract 方案需在此扩展。

### A.2 Prompt / Skill 模块

<a id="src-a26"></a>
### SRC-A26 — `internal/module/prompt/service.go:59-72`
- prompt service 只注册 built-in dynamic providers，尚未接入任何 skill catalog provider。

<a id="src-a09"></a>
### SRC-A09 — `internal/module/skill/skills_meta.go:22-45`
- 本地 skill 扫描 `scanSkills()` 已能产出静态清单。

<a id="src-a10"></a>
### SRC-A10 — `internal/module/skill/skills_fs.go:17-27`
- `ListSkills()` 已能返回 `[]SkillInfo`；尚未进入 prompt/L1，也未提供 name-based expand。

<a id="src-a11"></a>
### SRC-A11 — `internal/module/skill/rpc.go:17-55`
- 当前只有 `skills/list` 与 CRUD / preview RPC；未提供 `skill/list` / `skill/expand`。

<a id="src-a12"></a>
### SRC-A12 — `internal/module/skill/rpc_skill_types.go:1-61`
- 现有 skill RPC DTO 类型文件可作为 host RPC 扩容基线。

<a id="src-a05"></a>
### SRC-A05 — `internal/module/skill/approval.go:14-298`
- `(name, content_hash)` 审批缓存骨架完整存在，但尚未接入生产读链。

<a id="src-a06"></a>
### SRC-A06 — `internal/module/skill/service.go:30-49`
- `service` 已持有 `approval *ApprovalCache`，当前仍是预留字段。

<a id="src-a50"></a>
### SRC-A50 — `internal/module/skill/skills_match.go:14-28`
- `MatchPreview()` 已把匹配结果规范成 `{thread_id, matches[]}`，可作为 `p20.8` runtime matcher 输出形状基线。

<a id="src-a51"></a>
### SRC-A51 — `internal/module/skill/skills_match.go:59-132,146-185`
- `collectConfiguredAutoMatchedSkills()`、`collectLocalAutoMatchedSkills()` 与 `classifySkillMatch()` 已提供 configured / force / explicit / trigger 分类与去重语义，`p20.8` 应复用而不是在 turn 包重写一套。

<a id="src-a07"></a>
### SRC-A07 — `internal/platform/eventsurface/bind.go:50-53`
- 事件面已暴露 `skill/requestApproval`。

<a id="src-a08"></a>
### SRC-A08 — `internal/provider/codexapp/factory.go:41-55`
- codex approval bridge 已接收 `skill/requestApproval` 方法名。

<a id="src-b03"></a>
### SRC-B03 — `internal/store/prompt/contract.go:9-13`
- prompt store 合同当前只有只读 `Reader.List`；缺少 `Get/Upsert/Delete`。

### A.3 Thread / Turn 主链

<a id="src-a19"></a>
### SRC-A19 — `internal/module/thread/lifecycle.go:83-124`
- 线程启动期会构造 start assembly 并 launch session，是 launch contract 的实际主链。

<a id="src-a20"></a>
### SRC-A20 — `internal/module/thread/start_session_helpers.go:74-106`
- `resolveStartPromptAssembly()`、`toProviderStartAssembly()` 与 snapshot 映射都在这里；当前仍未携 skill 载荷。

<a id="src-a18"></a>
### SRC-A18 — `internal/module/turn/skills.go:11-102`
- `skillResolver` 已存在，但只有简单 name 去重与 substring 匹配。

<a id="src-b04"></a>
### SRC-B04 — `internal/module/turn/factory.go:346-355`
- `normalizeSkillNames()` 只产 `SkillRef{Name}`，不补 prompt/summary/version。

<a id="src-b05"></a>
### SRC-B05 — `internal/module/turn/service.go:93-130`
- `PrepareTurn()` 只做去重 / resolve，未补全文实体。

### A.4 Provider 注入与 rollout

<a id="src-a22"></a>
### SRC-A22 — `internal/provider/claudecli/config.go:52-69`
- claude start assembly 只处理 base/developer instructions。

<a id="src-a23"></a>
### SRC-A23 — `internal/provider/codexapp/driver.go:225-238`
- codex `startAssemblyInstructions()` 当前只抽 instructions，不携带 skill 数据。

<a id="src-b06"></a>
### SRC-B06 — `internal/provider/codexapp/module.go:232-249`
- codex provider 看到 `skill.Prompt==""` 会直接 `continue`，导致 UI 强制技能失效。

<a id="src-b09"></a>
### SRC-B09 — `internal/provider/codexapp/driver.go:64-76`
- codex `threadStartParams` 当前没有 `skills` 字段。

<a id="src-a13"></a>
### SRC-A13 — `internal/provider/codexapp/history_rollout.go:31-37,245-277`
- codex rollout trim 仍依赖 legacy marker 组合判断。

<a id="src-a14"></a>
### SRC-A14 — `internal/provider/claudecli/history_trim.go:16-22,63-95`
- claude rollout trim 同样仍是 legacy `摘要:` / `使用方式:` 判定。

## B. 前端

<a id="src-b01"></a>
### SRC-B01 — `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js:120-169`
- 页面直接调用 `prompts/list`、`prompts/write`、`prompts/delete`；当前 404 会形成前端噪声。

<a id="src-b07"></a>
### SRC-B07 — `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:288-297`
- `startThread()` 调 `thread/start` 时只发 `{cwd, modelProvider}`，launch 仍没有 skills 字段。

<a id="src-b11"></a>
### SRC-B11 — `cmd/agent-terminal/frontend/vue-app/composables/useThreadActions.js:103-115`
- 首次发送前会先 `startThread()`，但 launch 路径还没把 composer 选中的技能带过去。

<a id="src-b12"></a>
### SRC-B12 — `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:369-418`
- send 路径已能组 `selectedSkills` / `manualSkillSelection` 并下发请求，launch/send 形成断层。

## C. 配置 / 装配 / 审批入口

<a id="src-a28"></a>
### SRC-A28 — `internal/platform/config/config.go:9-25`
- 全局 config 目前仅含 DB/RPC/log/projectRoot；无 `skill.progressive_disclosure`。

<a id="src-a29"></a>
### SRC-A29 — `internal/sidecar/orch/tools/registry.go:11-37`
- MCP tool registry 当前只装配 orchestration / workspace / prompt / command / shared_file / memory。

<a id="src-a30"></a>
### SRC-A30 — `internal/sidecar/orch/tools/prompt_tools.go:50-60`
- `prompt_list` / `prompt_get` 是新增 resource-style `skill_list` / `skill_expand` 的直接模式。

<a id="src-a53"></a>
### SRC-A53 — `internal/platform/rpc/approval.go:85-118,174-233`
- `ApprovalManager.RequestApproval()`、`ensureDispatch()`、`dispatchApproval()` 已覆盖审批请求 → 派发 → 等待闭环，是 `p20.13` host-side approval flow 的 authoritative implementation。

## D. 实验文档 / 历史约束

<a id="src-b02"></a>
### SRC-B02 — `docs/plans/迁移/p8-execution-plan.md:94-201`
- P8 明确保留 `prompts/list|write|delete` 宿主 UI surface，不允许被 MCP 迁移搬空。

<a id="src-c01"></a>
### SRC-C01 — `docs/研究材料/实验验证/p20-exp-a-agentsmd-merge.md:35-55`
- 实验 A 结论：`baseInstructions` 语义优先级高于 AGENTS.md；Phase 8 放尾部即可，Phase 9 从“必需”降为“可选加固”。

<a id="src-c02"></a>
### SRC-C02 — `docs/研究材料/实验验证/p20-exp-b-claudecli-native-skills.md:37-64`
- 实验 B 结论：Claude CLI 原生 skills 不能被 flag 关闭；P20 必须扫盘降级，native skills 不应再由 harness 注入全文。

## 合规结论

### 1. 活跃锚点维护结果

- 本轮按 `p20.1` ~ `p20.16` 子单反查：旧附录 68 条锚点中仅 35 条仍被任务单直接引用。
- 已删除 33 条孤儿锚点，新增 5 条本轮补修锚点（`SRC-A49` ~ `SRC-A53`），当前保留 40 条活跃锚点。
- 已从“仍未命中的计划文件”中移除 5 个被驳回方案路径：`internal/module/prompt/rpc.go`、`internal/module/prompt/skill_catalog_provider.go`、`internal/module/turn/skill_hydrator.go`、`internal/module/skill/policy.go`、`internal/module/skill/metrics.go`。

### 2. 预算 / 依赖口径（与 README / checkpoint 同步）

- `internal/module/prompt`：authoritative 口径保持 **26 个 prod `.go` 文件**；`p20.1` 方案 B + `p20.5` 改落 `skill` 后，不再预设 `26→28`。
- `internal/module/thread`：当前 **25**；`p20.3/p20.4` 只能改既有文件。
- `internal/provider/claudecli`：当前 **24**；`p20.6` 仅允许 +1。
- `internal/module/turn`：README / checkpoint authoritative 口径保持 **`22→24`**。
- `p20.13` 依赖口径按 **`p20.10` + `p20.6`** 维护；`p20.14` 依赖口径按 **`p20.3`** 维护。

### 3. 当前仓库仍未命中的计划文件（截至 2026-04-19）

### Go 后端
- `internal/module/skill/rollout_markers.go`
- `internal/module/turn/expanded_state.go`
- `internal/contract/skill_injection.go`
- `internal/provider/claudecli/skill_inject.go`
- `internal/provider/codexapp/skill_inject.go`

### 前端
- `cmd/agent-terminal/frontend/vue-app/components/LaunchSkillPicker.js`
