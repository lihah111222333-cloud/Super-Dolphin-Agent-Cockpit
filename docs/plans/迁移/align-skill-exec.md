# V2↔V3 1:1 对齐：command/exec + skills/match/preview

## 范围

本次只用 LSP 读取以下实现并做 1:1 对照：

- V2 `go-agent-v2/internal/apiserver/methods_command.go`
- V2 `go-agent-v2/internal/apiserver/methods_config.go`
- V2 `go-agent-v2/internal/apiserver/methods_ui_projects.go`
- V2 `go-agent-v2/internal/skills/helpers.go`
- V2 `go-agent-v2/internal/skills/methods.go`
- V2 `go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go`
- V2 `go-agent-v2/pkg/agentsdk/service/runtime/turn_runtime_adapters.go`
- V2 `go-agent-v2/pkg/agentsdk/service/prompt/turn_prompt_core.go`
- V2 `go-agent-v2/pkg/agentsdk/service/prompt/turn_prompt_automatch_cc_test.go`
- V3 `internal/module/skill/rpc.go`
- V3 `internal/module/skill/rpc_types.go`
- V3 `internal/module/skill/contract.go`
- V3 `internal/module/skill/exec.go`
- V3 `internal/module/skill/skills_match.go`
- V3 `internal/module/skill/skills_fs.go`
- V3 `internal/module/skill/service.go`
- V3 `internal/module/skill/module.go`
- V3 `internal/platform/config/timeouts.go`
- V3 `internal/platform/config/config.go`

结论标记：

- `✅` 等价对齐
- `⚠️` 有对应实现，但不是严格 1:1
- `❌` 未对齐或仍是 stub

## 总览

| 检查项 | 结论 | 说明 |
| --- | --- | --- |
| exec 参数 | `❌` | V2 是 `argv/cwd/env`，V3 变成 `command/args/cwd`，并丢失 caller-supplied `env` |
| timeout（30s） | `✅` | V2 直接 `30*time.Second`，V3 走 `RPCRequestTimeout = 30*time.Second` |
| cwd fallback | `⚠️` | 两边都有 fallback，但 V2 取 active project cwd 并 canonicalize，V3 取启动配置里的 `projectRoot` |
| env 白名单 | `⚠️` | allowlist 前缀基本保留，但 V3 不再接收调用方 `env` overlay，注入语义已变 |
| shell 元字符守卫 | `✅` | 两边都禁止 `|` `;` `&` `$` 和反引号出现在 exec args 中 |
| match AgentID 回退 | `✅` | 两边都是 `threadId` 为空时回退到 `agent_id` |
| auto-match 配置型匹配 | `❌` | V2 是 `configuredSkillNames + options` 的显式/force 过滤；V3 仍是 `ReadConfig` stub，且把配置项一律标成 `configured` |

## 逐项对比

### 1. exec 参数

- V2 `commandExecParams` 是 `argv/cwd/env`，见 `go-agent-v2/internal/apiserver/methods_command.go:20-24`。
- V2 handler 直接消费 `p.Argv`、`p.Cwd`、`p.Env`，见 `go-agent-v2/internal/apiserver/methods_command.go:40-68`。
- V3 RPC 参数已经收缩为 `execParams{command,args,cwd}`，见 `internal/module/skill/rpc_types.go:26-30`。
- V3 handler 和 service 签名也只剩 `ExecCommand(ctx, command string, args []string, cwd string)`，见 `internal/module/skill/rpc.go:51-53`、`internal/module/skill/contract.go:13`。

结论：`❌`

原因：V3 的 wire shape 已经不是 V2 的 `argv + env + cwd`。`argv` 被拆成 `command + args` 还算可桥接，但 `env` 入口完全消失，调用方无法再按 V2 语义显式 overlay 环境变量。

### 2. timeout（30s）

- V2 在 `commandExecTyped` 中直接 `context.WithTimeout(ctx, 30*time.Second)`，见 `go-agent-v2/internal/apiserver/methods_command.go:55-56`。
- V3 在 `execCommand` 中调用 `platformconfig.WithRPCRequestTimeout(ctx)`，见 `internal/module/skill/exec.go:56-59`。
- V3 的 `RPCRequestTimeout` 常量明确是 `30 * time.Second`，见 `internal/platform/config/timeouts.go:17,29-30`。

结论：`✅`

原因：超时值与生效位置等价，都是 command exec 主路径上的 30 秒请求超时。

### 3. cwd fallback

- V2：`p.Cwd` 非空就用它；否则 fallback 到 `CurrentProjectCwd(s)`，见 `go-agent-v2/internal/apiserver/methods_command.go:57-62`。
- V2 的 `CurrentProjectCwd(s)` 读取 `activeProjectCwd`，并经过 `canonicalProjectRoot(active)` 规范化，见 `go-agent-v2/internal/apiserver/methods_ui_projects.go:89-100`。
- V3：`resolveExecCWD(cwd, s.projectRoot)` 在空 `cwd` 时回退到 `projectRoot`，见 `internal/module/skill/exec.go:56-59,107-112`。
- V3 的 `projectRoot` 来自 `cfg.ProjectRoot`，而 `cfg.ProjectRoot` 取 `PROJECT_ROOT` 或进程 `Getwd()`，见 `internal/module/skill/module.go:20-25`、`internal/platform/config/config.go:15-33`。

结论：`⚠️`

原因：V3 保留了“空 cwd 自动回退”的能力，但 fallback 源已经从 V2 的 active project scope 变成了模块启动时注入的 `projectRoot`，也没有看到 V2 那种 `canonicalProjectRoot(active)` 规范化语义，所以不是严格 1:1。

### 4. env 白名单

- V2 先继承完整 `os.Environ()`，再把调用方传入的 `p.Env` 中通过 `isAllowedEnvKey` 的键追加进去，见 `go-agent-v2/internal/apiserver/methods_command.go:63-68`。
- V2 allowlist 前缀是 `OPENAI_ / ANTHROPIC_ / CODEX_ / DYN_TOOL_ / MODEL / LOG_LEVEL / AGENT_ / MCP_ / APP_ / STRESS_TEST_ / TEST_E2E_`，见 `go-agent-v2/internal/apiserver/methods_config.go:81-103`。
- V3 不接收调用方 `env` 参数；它只会构造 `buildExecEnv(dir)`，由固定基础键 `PATH/HOME/USER/...` 加 allowlist 前缀环境变量组成，见 `internal/module/skill/exec.go:28-34,56-59,114-150`。
- V3 的 allowlist 前缀集合与 V2 基本一致，见 `internal/module/skill/exec.go:32-34,143-150`。

结论：`⚠️`

原因：allowlist 前缀本身基本保留了，但 V2 的白名单语义是“允许 caller overlay 某些 env 键”，V3 变成“从进程环境里抽取固定基础键 + allowlist 前缀”，调用方再也不能显式传 `env`。因此只能算部分对齐，不能算 1:1。

### 5. shell 元字符守卫

- V2 会遍历 `argv`，禁止参数中出现 `|` `;` `&` `$` 和反引号，见 `go-agent-v2/internal/apiserver/methods_command.go:48-51`。
- V3 `validateExecArgs` 同样禁止 `|` `;` `&` `$` 和反引号，见 `internal/module/skill/exec.go:75-82`。
- V3 还有测试 `TestExecCommandRejectsShellMetacharacters` 覆盖该守卫，见 `internal/module/skill/exec_test.go:13-20`。

结论：`✅`

原因：`command/exec` 主路径上的元字符防护与 V2 一致。

### 6. match AgentID 回退

- V2 `resolveSkillMatchPreviewThreadID` 的逻辑是：`threadId` 非空返回 `threadId`，否则回退到 `agent_id`，见 `go-agent-v2/internal/skills/helpers.go:122-125`。
- V2 `SkillsMatchPreview` 先调用这个 helper，再把解析后的 ID 传给 collector，见 `go-agent-v2/internal/skills/methods.go:363-370`。
- V3 `MatchPreview` 也先走 `resolveSkillMatchPreviewThreadID(agentID, threadID)`，逻辑同样是优先 `threadID`、否则 `agentID`，见 `internal/module/skill/skills_match.go:14-18,30-35`。
- V3 RPC 参数也显式保留了 `threadId` 和 `agent_id`，见 `internal/module/skill/rpc_skill_types.go:55-60`。

结论：`✅`

原因：参数入口、helper 逻辑、collector 入参三处都保持了相同回退语义。

### 7. auto-match 配置型匹配

- V2 `SkillsMatchPreview` 会把 `IncludeConfiguredExplicit=true` 和 `IncludeConfiguredForce=true` 传给 collector，见 `go-agent-v2/internal/skills/methods.go:363-370`。
- V2 runtime 的 contract 是 `CollectAutoMatchedSkillMatches(prompt, inputs, configuredSkillNames, candidates, options)`；配置型 skill 名单来自 `a.ListAgentSkills(agentID)`，见 `go-agent-v2/pkg/agentsdk/service/runtime/turn_runtime_adapters.go:21-29`、`go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go:259-275`。
- V2 prompt 侧真正的 configured 过滤逻辑是：只有配置过的 skill 且 `matchedBy=explicit/force` 时，才由 `IncludeConfiguredExplicit/Force` 控制是否纳入；配置型 `trigger` 仍被过滤，见 `go-agent-v2/pkg/agentsdk/service/prompt/turn_prompt_core.go:257-291`。
- V2 测试也明确验证了这点：configured + explicit 可放行，configured + trigger 仍过滤，configured + force 可放行，见 `go-agent-v2/pkg/agentsdk/service/prompt/turn_prompt_automatch_cc_test.go:139-219`。
- V3 `MatchPreview` 的 configured 分支来自 `collectConfiguredAutoMatchedSkills(ctx, resolvedID)`，它只会读取 `readConfiguredSkillState/ReadConfig`，然后把 `skills` 列表里的名字直接包装成 `MatchedBy: "configured"`，见 `internal/module/skill/skills_match.go:59-74,76-81,83-103`。
- V3 这里还有明确 TODO：后续才会替换成 provider-backed matcher，以表达 configured explicit vs force，见 `internal/module/skill/skills_match.go:68-72`。
- V3 当前默认 `ReadConfig` 仍是 stub，返回 `skills: []string{}`、`binding_source: "stub"`，见 `internal/module/skill/skills_fs.go:143-156`。
- V3 模块级 TODO 也说明了 auto-match 目前只挂在 `skills/match/preview` 这条 RPC 上，还没有事件驱动运行时接线，见 `internal/module/skill/module.go:12-14`。

结论：`❌`

原因：V3 既没有 V2 的 `configuredSkillNames + options` contract，也没有 explicit/force 分类过滤；默认配置源还是 stub。即便强行注入 `readConfigState`，返回的也是统一的 `MatchedBy: "configured"`，这与 V2 的 configured explicit/force 语义不是一回事。

## 最终判断

- `command/exec`：`❌`，主要 blocker 是协议从 `argv/env/cwd` 漂移成 `command/args/cwd`，且 caller `env` overlay 消失。
- `skills/match/preview`：`⚠️`，`AgentID` 回退对齐，但 configured auto-match 仍未 1:1 落地。
- 合并看：本轮对齐结论是 `❌`，还不能宣称 V2↔V3 在 `command/exec + skills/match/preview` 上完成 1:1 对齐。
