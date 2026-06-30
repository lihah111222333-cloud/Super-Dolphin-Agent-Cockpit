# Reasonix 剩余架构优点复核与吸收计划

> 日期：2026-06-30
> 状态：NEEDS_APPROVAL
> 范围：代码复核后的后续吸收计划；本文件不代表生产代码已经实现
> 基线：`deepseek-reasonix@3e824fc3`，`super-agent-v3@bf3b5a77`（本地 `main` ahead origin 30）

## 0. 复核结论

当前 V3 已经吸收 Reasonix 的主架构边界：Session ports、PromptAssemblyBoundary、MCP namespace / per-tool lifecycle、event surface、host-direct history/memory tool、desktop dependency guard 都已有代码和测试面。剩余值得吸收的不是新的大框架，而是两条 hardening lane 和一条 spike：

1. **需要吸收：Plan/read-only/tool trust 单一策略门**，包括 `ReadOnly != PlanSafe`、untrusted read-only fail-closed、read-only subagent 工具面过滤。
2. **需要吸收：session / provider artifact path helper authority**，限定为 sidecar/path layout helper，不吸收 Reasonix 的数据 owner 表述。
3. **先 spike：writer tool preview contract**，必须先补全 V3 model-callable writer inventory。

以下候选不应立即吸收：

- **Provider wire-normalization**：Reasonix 是 API provider 出线前修复 OpenAI/Anthropic chat history；V3 当前主要是 Codex/Claude CLI adapter，不构造同类 chat `messages` request。保留为“未来 API provider 引入时再吸收”的条件项。
- **Tool schema canonical-cache registry**：Reasonix 的 canonical schema cache 有价值，不只是性能优化，也修复空 schema、OpenAPI-style `required: true`、非法 `dependentRequired` 等 provider-wire validity 风险。但 V3 toolbridge 已有 schema object validation、host 优先、MCP lifecycle backfill/filter/direct-call deny；没有当前性能、schema drift 或 provider-wire validity 证据时，不应新增平行 registry。Reasonix 的 `SuspendPrefix` / `ResumePrefix` 也不能等同为 V3 持久 lifecycle。
- **Reasonix 全局 built-in registry、blank import wiring、event bus 或 workers/site/accounts 外壳**：不适配 V3 的 Fx/module/desktop runtime 边界。

## 1. 代码证据矩阵

| 候选 | Reasonix 代码证据 | V3 当前代码证据 | 结论 |
| --- | --- | --- | --- |
| Plan/read-only/tool trust | `internal/planmode/policy.go` 定义 `PlanSafety`、`Untrusted`、`PlanSafe => ReadOnly`、bash plan gate；`ReadOnly()` 与 plan-safe 明确分离，`complete_step` 是 read-only 但 post-approval only；`internal/shellsafe/shellsafe.go` 是 permission 与 plan-mode 共享命令分类源；`read_only_task` 通过受限 registry 排除 writers、workflow/meta、job/process、planning-state mutator 和 untrusted MCP `readOnlyHint`。 | V3 有 provider sandbox / approval policy 映射：`internal/provider/codexapp/driver_pool_routing.go`、`internal/provider/claudecli/transport_config.go`；Claude native `EnterPlanMode` / `ExitPlanMode` / `TodoWrite` 默认 hard-disable；Codex `update_plan` 只是 default-disabled / soft-audit。`AgentTypePlan` 只是 prompt agent type。当前 `ToolCallRequest` 无 stage 字段，`MCPTool` 无 read-only hint 字段，全仓未发现 `PlanSafety` / `planmode` / `shellsafe` 同类 owner。V3 另有未完全接线的 reviewer tool preset。 | **吸收**。这是仍然存在的真实缺口，但必须先定义 runtime stage 来源。 |
| Session / artifact path authority | `internal/store/session.go` 集中 `SessionMeta`、goal state、checkpoint、jobs、cleanup-pending sidecar suffix；但它实际只暴露 sidecar path helper，session JSONL save/load、session filename minting、branch meta、subagent artifacts、config/convention dirs、job artifact 删除和 cleanup 编排仍在 agent/jobs/control/config。 | V3 路径推导分散：Codex rollout glob 在 `internal/provider/codexapp/history_rollout.go` 与 `internal/util/historyjsonl/history.go`，scratchpad 在 `internal/module/thread/scratchpad.go`，Codex home canonicalization 在 `internal/contract/codex_identity.go`。 | **限定吸收**。只做 leaf path helper，不改数据布局，也不引入 session data owner。 |
| Writer preview contract | `internal/tool/tool.go` 的 `Previewer` 是可选 writer 能力；Reasonix 的核心 file-edit writers 和部分 specialized writers 提供 preview，但并非所有 writer 都有 preview（如 `move_file`）。Preview 是 UI/checkpoint 辅助，不是 permission 或事务边界，且 edit/multi-edit 的 preview/execute 变换需要漂移测试。 | V3 有 post-call diff：`internal/platform/toolbridge/diff_gen.go`、`diff_fallback.go`；host write approval 目前只给 capability payload：`internal/platform/toolbridge/host_tools.go`；model-callable writer 还包括 mcp-lsp `edit`、mcp-orch `shared_file_write` / `defineTaskWriteTool` workflow.write、workspace merge/run tools、artifact/media generation side-effect tools。 | **先 spike**。先补真实 writer inventory，再决定可选 preview contract。 |
| Provider wire-normalization | `internal/provider/provider.go` 的 `NormalizeMessages` / `NormalizeSessionMessages` / `SanitizeToolPairing`。 | V3 的 provider message DTO 是 history/read projection：`internal/dto/provider/message.go`，Codex/Claude history reader 只读取 CLI 历史，不向 provider API 发送 chat messages。 | **暂不吸收**。条件触发项。 |
| Tool schema canonical-cache | `internal/tool/tool.go` registry 在 `Add` 时 canonicalize schema，并稳定排序输出 provider 工具表；canonicalizer 还把空 schema 转为 object、删除非法 `required` / `dependentRequired`。`SuspendPrefix` / `ResumePrefix` 只是当前 registry 的内存可见性门闩，不是持久 lifecycle。 | V3 toolbridge 已校验 schema object、合并 host/peer、执行 lifecycle backfill/filter/direct-call deny：`internal/platform/toolbridge/types.go`、`handler_host_tools.go`、`handler_peer_decode_helpers.go`；尚未发现等价 canonical schema cache。 | **条件吸收**。只在性能、schema drift 或 provider-wire validity 有证据时吸收 canonical schema cache，不吸收 SuspendPrefix 作为 lifecycle。 |

## 2. Lane A：Plan/read-only/tool trust 策略门

### 目标

建立 V3 自己的工具安全分类 owner，把“只读”“计划阶段可用”“信任来源”“shell 命令安全”分开，不把 provider sandbox、MCP lifecycle、approval policy 混成一个隐式判断。

### 非目标

- 不替换 Codex/Claude CLI 的 sandbox / permission-mode。
- 不把 Reasonix 的 tool registry 或 built-in tool model 搬进 V3。
- 不信任外部 MCP 的自报 read-only 信息；V3 当前 `internal/dto/mcp/tool.go` 也没有 `readOnlyHint` 字段，不能凭空设计成已可信。
- 不把 Claude native `EnterPlanMode` / `ExitPlanMode` hard-disable 视为已完成；它只是 provider-native guard，不是 V3 tool execution stage policy。
- 不把 Codex `update_plan` 当成 native hard-disable；当前它是 default-disabled / soft-audit，需要后续 stage/mode/toolpolicy 明确处理。
- 不把 `AgentTypePlan` 当成执行阶段；它只是 prompt 拼装类型。

### 建议代码落点

- A0 先确认或新增 stage 来源：
  - 当前 `internal/platform/toolbridge/types.go` 的 `ToolCallRequest` 无 stage/mode 字段；
  - `planning` 必须由明确 runtime/contract 字段传入，默认值必须是 `execution`；
  - 没有 stage source 前，只允许落 `toolpolicy` unit tests，不接入 runtime blocking。
- 新增 `internal/platform/toolpolicy`，作为 stdlib-only 或低依赖 leaf package：
  - `Stage`: `planning` / `execution`。
  - `TrustSource`: `first_party` / `provider_native` / `mcp_owner_policy` / `external_hint` / `unknown`。
  - `Capability`: `read_only`、`writer`、`process_control`、`memory_mutation`、`approval_finalizer`。
  - `Decision`: `allow` / `deny`，包含 stable `code` 和面向模型的 `hint`。
  - 明确不变量：`PlanSafe => ReadOnly`，但 `ReadOnly` 不自动等于 `PlanSafe`。
- 新增 `internal/platform/toolpolicy/shellsafe.go`：
  - 从 Reasonix 吸收“命令表 + 子命令表 + shell syntax fail-closed”模式。
  - V3 首批只覆盖现有可控入口需要的命令，不追求一次性全量。
- 在 `internal/platform/toolbridge` 接入只读/计划阶段 gate：
  - 先覆盖 host-direct tool 和 V3-owned MCP surface；
  - lifecycle deny 仍由 `MCPToolLifecyclePolicyReader` 保持 owner 权威；
  - external/untrusted read-only hint fail-closed；first-party/internal read-only 可按明确 owner policy 信任。
- 盘点并复用现有 `internal/provider/toolfilter` reviewer preset，但不能把它当成完整 stage/trust owner；需要接到 read-only subagent/delegation 启动路径。
- read-only subagent 或 planning-only delegation 只能拿受限工具面，不能只靠 prompt 约束。

### 测试门

- `internal/platform/toolpolicy/*_test.go`
  - `PlanSafe => ReadOnly` 不变量。
  - `ReadOnly` 不自动等于 `PlanSafe`，post-approval-only 工具必须可拒绝。
  - unknown / external hint fail-closed。
  - bash shell syntax、background/process-control、危险参数阻断。
- read-only subagent / delegation 工具面过滤测试：
  - writer、workflow/meta、job/process 工具（含 `wait` / `bash_output`）、planning-state mutator（含 `todo_write` / `complete_step`）、递归 agent/skill、tool-source connector（如 `connect_tool_source`）、external untrusted read-only hint 不进入受限工具面。
  - bash wrapper 使用同一 plan-mode shell policy，不能绕过 `shellsafe` / stage gate。
- `internal/platform/toolbridge/*_test.go`
  - host tool 在 planning stage 的允许/拒绝。
  - lifecycle disabled/suspended/removed 不因 plan policy 绕过。
  - schema validation 仍先于 handler 调用。
- 现有 provider sandbox 测试必须继续通过：
  - `internal/provider/codexapp/native_tool_policy_validation_test.go`
  - `internal/provider/claudecli/transport_config_security_test.go`

### 验收命令

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider' -count=1
make guard
```

## 3. Lane B：Session / provider artifact path helper authority

### 目标

把 V3 的 session/provider artifact 路径推导收口成一个 leaf helper owner，先只迁移函数位置，不改变任何现有路径布局，也不把路径 helper 扩大成 session data owner。

### 非目标

- 不迁移已有数据库字段。
- 不迁移 session JSONL、thread/message、job/artifact 或 cleanup 的业务 owner。
- 不吸收 session filename minting、config root discovery、subagent artifact layout、branch/session metadata owner。
- 不迁移 provider home、skill mirror、runtime install root；这些仍归 provider identity / runtime install owner。
- 不改变 Codex rollout 文件名 glob。
- 不改变 scratchpad 清理语义。
- 不把 `contract.CanonicalizeCodexHome` 的身份校验职责挪走；它已经是 contract 层身份边界。

### 建议代码落点

- 新增 `internal/platform/sessionpaths`，作为 stdlib-only leaf package，不能 import 任何 `github.com/anthropic-ai/super-agent-v3/...` 内部包：
  - `CodexRolloutGlob(codexHome, threadID string) (string, error)`。
  - `ManagedScratchpadDir(tempRoot, projectRoot, threadID string) string`。
  - `IsManagedScratchpadDir(tempRoot, dir string) bool`。
  - `SanitizeProjectPath(raw string) string`。
- 迁移调用点：
  - `internal/provider/codexapp/history_rollout.go` 的 rollout glob 构造。
  - `internal/util/historyjsonl/history.go` 的 Codex history discovery / `codexRoot` 路径推导需要纳入 inventory；迁移时必须保留它“空 codex home 默认回退 `~/.codex`”的语义，不能和 `provider/codexapp` 的显式 opt-in fallback 合并。
  - `internal/module/thread/scratchpad.go` 的 scratchpad path / sanitize / managed check。
- 保持 owner 边界：
  - provider/codexapp 仍负责读取 Codex rollout。
  - thread module 仍负责 scratchpad 生命周期。
  - sessionpaths 只负责 deterministic path derivation。

### 测试门

- 新增 `internal/platform/sessionpaths/*_test.go`，锁定当前路径 golden。
- 迁移并保留现有测试语义：
  - `internal/provider/codexapp/history_rollout_path_test.go`
  - `internal/module/thread/phasef_scratchpad_test.go`
  - `internal/module/thread/stop_test.go` 中 scratchpad cleanup 覆盖。
- arch guard：
  - sessionpaths 必须 stdlib-only；archtest 对任意 `github.com/anthropic-ai/super-agent-v3/` import prefix fail。
  - provider/module 只能调用 path helper，不复制 suffix/glob 规则。
  - 增加 grep guard：只针对 session/history/scratchpad artifact literals；生产代码里的 Codex rollout glob 片段（如 `rollout-*-`、`sessions/*/*/*`）和 managed scratchpad suffix 只能出现在 `sessionpaths` 或明确允许的测试 fixture / legacy compatibility guard。
  - provider home / runtime root / skill mirror literals 需要显式 allowlist，例如 `.codex`、`.claude`、`.super-dolphin/providers/codex`、`$SUPER_DOLPHIN_HOME/providers/codex`、`.agents/skills`。

### 验收命令

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path|Provider|Thread' -count=1
make guard
```

## 4. Lane C：Writer preview contract spike

### 目标

评估 V3 是否需要 model-callable first-party writer 在执行前产出 preview/diff，避免 approval card 只能展示 capability，而无法展示将要写什么。

### 先做 spike 的原因

V3 的主要写入来源不是 Reasonix 那种 in-process built-in file writer，而是 Codex/Claude native tool、mcp-lsp edit、mcp-orch workflow/shared-file writer、host-direct memory writer。Reasonix 的 `Previewer` 只是 UI/checkpoint preview，不是 permission 或事务边界；直接搬接口会误导 owner 边界。

### spike 内容

1. 列出现有 model-callable first-party writer：
   - host-direct 默认面：`memory_write`。
   - host-direct 非默认面：`workflow_template_save` / `workflow_template_rollback` 只有 `WorkflowTemplateWriteHostToolRegistry` 被显式授权装配时才算 model-callable writer。
   - mcp-lsp：`edit` 工具及其 `replace_range` / `rename` / `code_action` / `format` actions；当前 mcp-lsp manifest 没有独立 `format_preview` 工具，toolbridge/uistate 的 legacy alias 痕迹不能当成当前 writer surface。
   - mcp-orch：`shared_file_write`，以及所有通过 `defineTaskWriteTool` 暴露的 workflow.write / high-risk task 工具，例如 DAG create/update/start/terminate/delete、node update/dispatch、`task_workflow_recovery_action`。
   - mcp-orch workspace：`workspace_create_run` / `workspace_merge_run` / `workspace_abort_run` 是模型可调用状态/文件写面；`workspace_merge_run.dry_run` 可单独评估为 preview，真实 merge 必须纳入 writer preview / policy 评估。
   - Codex/Claude native writer 只纳入能力边界盘点，不在首轮实现 preview。
2. 明确排除 `internal/module/skill` 等 module service/mirror/policy 内部写入，除非它们已经通过 `HostToolRegistry` 或 MCP surface 暴露给模型调用。
3. mcp-orch `launch_agent` / `send_message` / `stop_agent` / `recover_agent` / `interrupt_agent` 属于 process-control / session-lifecycle mutator，先由 Lane A policy 覆盖；除非 ADR 扩大 preview 范围，否则不纳入 writer preview spike。
4. artifact/media generation tools（如 `tts_generate`、`av_merge`、`video_with_audio`）也是模型可调用 side-effect surface；若本轮只覆盖代码/工作流写入，必须明确排除并转入 artifact/process policy。
5. 判断每类 writer 是否能在不执行副作用的情况下生成 deterministic preview。
6. mcp-lsp `edit` 的 writer action 包含 `rename` / `code_action` / `format`，但现有 post-call diff evidence 主要覆盖 `patch` / `replace_range`；preview spike 不能把 post-call diff 当作这些 action 的前置证明。
7. 若可行，只新增可选接口，不要求所有工具实现：
   - `PreviewHostTool(ctx, HostToolCall) (PreviewResult, error)`
   - `PreviewResult` 只包含 path、kind、diff、redacted metadata，不含 secret 或完整大文件。
8. approval envelope 可以携带 preview metadata，但 permission/authority 仍是唯一执行 gate。

### spike 验收

- 输出一份 ADR 或计划修订，明确哪些 writer 能 preview，哪些不能。
- ADR 必须标注 writer 是否默认 model-callable，以及 owner 是 host-direct、mcp-lsp、mcp-orch 还是 provider-native。
- 至少为一个 host-direct writer 补单元测试证明 preview 与 execute 语义一致。
- preview/execute 一致性不得只测 happy path；必须让 preview/execute 共享同一转换函数，或至少覆盖换行、编码/大文件、路径校验、权限拒绝前后顺序。
- Reasonix edit/multi-edit 的 CRLF 适配路径说明，仅靠 happy-path PreviewMatchesExecute 不足以证明无漂移。
- 不允许把 post-call `difftracker` 当成 pre-call approval preview。

## 5. 明确不吸收 / 暂缓项

### Provider wire-normalization

暂不执行。触发条件是 V3 引入 OpenAI/Anthropic API chat provider，或者 CLI adapter 开始自己构造 provider `messages` request。届时可借鉴 Reasonix：

- wire-safe normalization 与 persisted-session normalization 分离；
- 修复 unanswered tool call、orphan tool result、truncated JSON args；
- 健康路径 zero-allocation / no mutation。

当前 V3 只应继续加强 CLI history reader 的 filtering、pagination、metadata redaction。

### Tool schema canonical-cache registry

条件吸收，暂不执行。触发条件：

- `ListToolsForCodex` 或 surface preparation 出现可测性能瓶颈；
- schema key-order drift 影响 cache/hash/测试；
- 外部 / MCP schema 出现 provider-wire validity 风险，例如空 schema、OpenAPI-style `required: true`、非法 `dependentRequired`；届时可选择 canonicalization 或 fail-fast validation。
- 外部 MCP schema 需要 canonical snapshot 才能稳定审计。

当前 V3 已有更重要的 toolbridge policy 面：schema object validation、host-first dedup、lifecycle owner backfill/filter/direct-call deny。即使未来吸收，也只吸收 canonical schema cache / stable schema snapshot / provider-wire validity normalization，不吸收 Reasonix `SuspendPrefix` / `ResumePrefix` 作为 V3 lifecycle。

### Reasonix global registry / blank imports / event bus / business workers

不吸收。V3 必须保留 Fx module graph、provider runtime adapters、desktop/event surface 和 MCP sidecar owner 分层。

## 6. 推荐执行顺序

1. **先做 Lane A**：安全收益最高，且能纠正文档中“plan/read-only/tool trust 已落地”与当前代码缺少中央 owner 的不一致。
2. **再做 Lane B**：低风险收口，适合独立小 PR，路径 golden 能锁住不改行为。
3. **最后做 Lane C spike**：不要在没有 writer inventory 的情况下提前设计通用 preview interface。

当前 `plan_executable=false`，原因是这只是代码复核后的 docs-only 计划；生产改动需要单独批准，并且当前 worktree 已有与本计划无关的未提交修改。
