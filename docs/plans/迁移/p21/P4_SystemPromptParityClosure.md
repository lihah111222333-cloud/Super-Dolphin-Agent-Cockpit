# P21 / P4 System Prompt Parity 收口报告

> 目标：把 `internal/module/prompt` 的装配行为、文案和缓存形状对齐 Claude Code
> `src/constants/prompts.ts` 上游。本文件记录**已对齐 / 刻意偏离 / 不复刻**三类决策，
> 以及落地过的验证手段，作为后续维护的真值。

## 0. 决策摘要

| 决策 | 取值 | 备注 |
|---|---|---|
| 缓存一致目标 | **Anthropic org ephemeral（5min TTL）** | global scope 受 Claude CLI 客户端限制，不可达 |
| Subagent 独立 session | **接受 cache 隔离** | subagent 走 `orchestration_launch_agent`，API 连接独立 |
| PROACTIVE / coordinator / side_question | **不复刻** | 依赖 Claude REPL 内部 runtime，本项目无对等语义 |
| `--system-prompt` / `--append-system-prompt` 两段化 | **两段** | CachedPrefix 单独走 `--system-prompt`，其余并入 `--append-system-prompt` |
| `CYBER_RISK_INSTRUCTION` 与 CLI harness 重复 | **接受重复** | 安全红线双层冗余无害，拒绝去重 |
| `USER_TYPE=ant` 分支 | **gate 支持，默认外部版** | `env: USER_TYPE=ant` 触发 ant 文案与 numeric_length_anchors |
| Snapshot v1→v2 迁移 | **重算 + debug 日志** | `storedPromptSnapshotValid` 不匹配时 silently recompute，不报 warn |
| Global cache scope 命中验证 | 列入硬验收 | 对应集成 step，见 §4 |

## 1. 已对齐（~80% parity）

| 模块 | 对应 Claude 路径 | 本项目落点 |
|---|---|---|
| `CLAUDE_CODE_SIMPLE` 三行硬早退 | `prompts.ts:L444-454` | `assembler.go:simpleStartAssembly`，strict `You are Claude Code` / `CWD: …` / `Date: …` |
| Static 7 段 | `prompts.ts:L175-441` | `section.go:staticSectionSpecs`（identity, system_constraints, engineering, actions, tool_preferences, style, output_efficiency） |
| Identity opener | `prompts.ts:L175-184` | `section.go:sectionIdentityHeader`（`You are Claude Code, Anthropic's official CLI for Claude.`） |
| `CYBER_RISK_INSTRUCTION` | `src/constants/cyberRiskInstruction.ts` | `section.go:sectionCyberRiskInstruction` |
| ant output efficiency 长文 | `prompts.ts:L402-428` | `section.go:sectionOutputEfficiencyAntText`（`# Communicating with the user`） |
| ant engineering 补丁 | `prompts.ts:L205-246` | `section.go:sectionEngineeringAntSuffix`（`/issue`、`/share`、`/help`） |
| Dynamic 13 section 矩阵 | `prompts.ts:L491-558` | `dynamic.go:dynamicSectionSpecs`（session_guidance/memory/agent_memory/memory_context/env_info_simple/language/mcp_instructions/output_style/scratchpad/frc/summarize_tool_results/numeric_length_anchors/token_budget/brief/skill_catalog） |
| `session_guidance` Verification Agent 协议 | `prompts.ts:L352-360` | `dynamic.go:sessionGuidanceVerificationItems`（4 bullets：spawn_agent / verdict ownership / PASS spot-check） |
| `mcp_instructions` DANGEROUS + delta | `prompts.ts:L160-165,513-521` | `mcp_provider.go:MCPInstructionsProvider`（`Uncached` + `mcp_instructions_delta` attachment） |
| Memory 行为规则 vs claudeMd 正文分离 | mapping §5.2 + §6.1 | `memory/rules_provider.go`（规则）+ `user_context_builder.go:renderClaudeMdSource`（正文，`Contents of {path}:` 包装） |
| Subagent `DEFAULT_AGENT_PROMPT` / override 直通 / Explore·Plan 裁剪 / env-details 追加 | `prompts.ts:L758-791` + `runAgent.ts:380-410` | `agent_assembler.go:AssembleAgent`（含 `sectionAgentEnvDetails`） |
| Cache 切分（CachedPrefix / UncachedTail） | mapping §四 | `dto.PromptAssemblyBoundary` + `assembler.go:startAssemblyBoundary`；provider 消费见下 |
| `--system-prompt` + `--append-system-prompt` 两段下发 | Claude CLI headless convention | `provider/claudecli/transport_config.go:appendSystemPromptFlags` |
| `PROMPT_START_CURRENT_DATE` 冻结 | mapping §5.0 | `assembler.go:startPromptCurrentDate` + 测试用 |
| 用户可见 userContext 合成 user meta message | mapping §6.1 | `StartAssembly.UserContext/UserContextText` + `TurnAssembly.UserContext/UserContextText` + `contract.RenderUserContextMessage` |
| Snapshot 版本升级与 silent recompute | mapping §八 | `PromptAssemblySnapshotVersion = 2` + `thread/prompt_snapshot.go:resolveStablePromptSnapshot`（hash mismatch → Debug log + recompute） |

## 2. 刻意偏离（为适配本项目架构）

| 项 | Claude 行为 | 本项目行为 | 原因 |
|---|---|---|---|
| `gitStatus` / `currentDate` / `runtimeExtras` 位置 | 追加到 `system` 数组尾（与 CachedPrefix 同入 `system` 参数，靠 `cache_control` block 粒度分 cacheScope） | 从 BaseInstructions 移出，走 `StartAssembly.UserContext{Text}` + `.SystemContext`，由 provider 投递到首轮 synthetic user meta message | Claude CLI 下 custom `options.systemPrompt` 会把整个 `system` 参数合并进单个 `rest` block（mapping §四 fallback 形态），如果 gitStatus/currentDate 留在 BaseInstructions，每次 git 变更 / 日期变更都会破掉 org ephemeral cache |
| tool_preferences 决策树工具名 | `FileReadTool` / `FileEditTool` / `GlobTool` / `GrepTool` | `lsp_file` / `lsp_edit` / `lsp_grep` / `code_run` | 本项目工具集与 Claude 不同；意图（禁止 shell fallback）对齐，工具名按本项目真实暴露名重写 |
| `env_info_simple` 字段 | 含 knowledge cutoff、最新模型家族、`/fast` 说明、产品入口列表 | 只保留 cwd / platform / shell / OS / model ID / worktree / git-repo 布尔 | Claude 产品私货，本项目不应原样抄 |
| `session_guidance` 工具名 | `AskUserQuestionTool` / `AgentTool` / `TaskTool` | `request_user_input` / `spawn_agent` / `orchestration_launch_agent` / `/<skill-name>` | 本项目工具名映射 |
| Subagent 派生方式 | 同进程 `runAgent.ts` | 独立 CLI 子进程（`orchestration_launch_agent` → `unified.StartSession`） | 本项目 orchestration 架构决定；代价是 subagent 与主线程不共享 API prompt cache |

## 3. 不复刻（明确决策）

| 项 | Claude 位置 | 不复刻原因 |
|---|---|---|
| PROACTIVE / KAIROS 自治 10 区块 | `prompts.ts:L466-487, L860-914` | 依赖 tick 唤醒 / SleepTool / terminal focus 等 runtime infra，本项目 `/loop` `/schedule` 语义不同，单独立 RFC 再议 |
| coordinator mode / workerToolsContext / terminalFocus | `coordinatorMode.ts:L80-369` | 依赖 Claude REPL 多 worker 协调模型 |
| `side_question` / `/btw` / `lastCacheSafeParams` snapshot | mapping §八 边缘路径 | 依赖 REPL `runForkedAgent`；本项目无轻量 side Q&A 产品场景 |
| `__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__` API 侧 block 切分 + `PROMPT_CACHING_SCOPE_BETA_HEADER` global scope | `prompts.ts:L105-115` + `api.ts:321-404` | Claude CLI 对 custom systemPrompt 不插 boundary，global scope 不可达；本项目 CachedPrefix/UncachedTail 保留本地语义，为未来直连 Messages API 留接口 |
| Subagent 与主线程共享 API cache | `runAgent.ts` 同进程复用 | orchestration 独立 CLI 进程决定 |
| `ant_model_override` 真 provider | mapping §5.3 | ant family provider 本项目未接入；stub 已删除以避免误导 |

## 4. 验证手段（落地）

| 验证 | 位置 |
|---|---|
| `CachedPrefix` + `BaseInstructions` bytewise stable（同 BuildCtx + 冻结日期） | `internal/module/prompt/cache_stability_test.go:TestAssembleStart_CachedPrefixBytewiseStable` |
| `UserContext` / `SystemContext` 字段正确填充 | `cache_stability_test.go:TestAssembleStart_PopulatesUserMetaFields` |
| CLAUDE_CODE_SIMPLE 严格三行 | `cache_stability_test.go:TestSimpleStartAssembly_ThreeLineForm`、`assembler_test.go:TestSimpleAssembleStartHardEarlyReturn`、`TestSimpleAssembleStartUsesSessionFlag` |
| DANGEROUS 类 dynamic section 不能进 CachedPrefix | `boundary_invariants_test.go:TestBoundary_DynamicLivesInUncachedTail_MCPInstructions` |
| Memory section start-only | `boundary_invariants_test.go:TestBoundary_MemoryIsStartOnly` |
| Fallback assembly 不泄漏 system-reminder | `assembler_test.go:TestAssembleStartFallsBackOnBuildError` |
| `--system-prompt` + `--append-system-prompt` 两段化 | `provider/claudecli/transport_config_test.go:TestBuildCLIArgsEmitsTwoSegmentSystemPrompt` |
| Subagent override 直通 | `agent_assembler_test.go:TestAssembleAgent_OverrideSystemPromptTakesPriority` |
| Subagent Explore/Plan 裁剪 claudeMd + gitStatus | `agent_assembler_test.go:TestAssembleAgent_ExploreRedactsClaudeMdAndGitStatus` |
| Subagent default 保留 context 且追加 env-details | `agent_assembler_test.go:TestAssembleAgent_DefaultKeepsContext` |
| Golden snapshot | `testdata/golden/integration/start_assembly.golden.json` |

### 外部联动验证（硬验收）

**目标**：Claude CLI headless + 同 API Key + 同 cwd + 5 分钟 TTL 内连续两次 `thread/start`，Anthropic 响应 header 应出现 `cache_read_input_tokens > 0`（或等价 `cache_hit_tokens`）。

执行时机：Phase 5 外部联动测试框架就绪后补充。

## 5. 对齐率评估

- 文本可见面（static / dynamic 非 PROACTIVE） ≈ **92%**
- 条件分支与 section gate（keepCoding / USER_TYPE / ultraSimple / DANGEROUS / override） ≈ **80%**
- Context 注入通道（userContext / systemContext / claudeMd） ≈ **85%**
- Cache 机制（org ephemeral） ≈ **70%**（受 CLI 限制）
- Subagent **API + live wiring**（`AssembleAgent` 已接入 orchestration → thread.Start 的 `dispatchPromptAssembly` 路由） ≈ **90%**
- 边缘路径（/clear、/compact、worktree restore、background handoff） ≈ **60%**

加权综合 **≈ 80%**（Phase 5 接入 `AssembleAgent` live wiring 后回升到 80%）。其中无法突破的部分主要来自：
- Anthropic global cache scope（需要自写直连 Messages API 的 provider，放弃 Claude CLI）
- Subagent cache 共享（需要同进程 orchestration 重写）
- PROACTIVE / coordinator / side_question（独立 runtime infra，估算本身不在工作量内）

## 6. 已知缺口（诚实清单）

### 6.1 `AssembleAgent` 已接入 orchestration live path（Phase 5 补丁）

**状态**：Phase 5 review 发现 `AssembleAgent` 无 production 调用者后已完成接入。

- `internal/module/thread/start_session_helpers.go:dispatchPromptAssembly` 根据 `input.AgentType` 路由：
  - `contract.AgentTypeExplore` / `contract.AgentTypePlan` → `AssembleAgent`（裁剪 claudeMd + gitStatus，追加 env-details）
  - 其他值（空 / `worker` / `main` / `Writer` 等用户自定义）→ 继续走 `AssembleStart`，保持向后兼容
- `contract.PromptAssemblyService` 接口正式吸纳 `AssembleAgent`；`AgentType` / `AgentInput` 上移到 contract 层，避免 thread 反向依赖 prompt 包
- orchestration 端 `orchestration_launch_agent` 工具的 `AgentType` 参数一路透传到 `StartRequest.AgentType` → `StartInput.AgentType` → dispatch；用户调用时 `AgentType="Explore"` 或 `"Plan"` 即可触发 subagent 路径
- 验证测试：`internal/module/thread/agent_dispatch_test.go:TestDispatchPromptAssembly_{ExploreRoutes,PlanRoutes,UnknownAgentTypeFallsBack}`

### 6.2 `runtimeExtras` 过滤修复后需复盘

初版 `includeRuntimeExtraSection` 只过滤三个命名 dynamic slot，导致所有 static section 被复制进 `UserContext["runtimeExtras"]`。Phase 5 review 发现后已修（`section.Region == PromptRegionStatic → false`），并新增 `TestBoundary_RuntimeExtrasExcludesStaticSections` 守护。历史 thread 上的旧 snapshot 会在 resume 时走 hash-mismatch 静默重算路径自动纠正。

## 7. 下一步可选扩展

按成本从低到高：

1. ~~**把 `AssembleAgent` 接入 orchestration live path**~~ — Phase 5 已完成，见 §6.1
2. **`mcp_instructions_delta` 全量 attachments 管线** → 让 MCP 连接/断开只发 delta，不破 UncachedTail cache
3. **`/clear` `/compact` → CLI 子进程 prompt cache 联动清除钩子** → 保证 clear 后 CLI 不继续使用 stale org cache
4. **claudeMd `@include` / `settingSources` / `--bare` / `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` 完整门控**
5. **自写 Anthropic Messages API 直连 provider** → 解锁 global cacheScope + pre/post-boundary 多 block cache_control

以上五项都做完预计可再推到 ~95% parity。
