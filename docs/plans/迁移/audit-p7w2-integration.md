# P7w2 审查：集成测试

## 1. V3 handler 总数

V3 当前 RPC 暴露面由 5 个 `rpc.HandlerMapResult` producer 通过 `group:"rpc_handlers"` 聚合到 `Server.Register(...)`，这是当前审查口径的总面。证据见 [internal/platform/rpc/module.go:36](/Volumes/bot/super-agent-v3/internal/platform/rpc/module.go#L36) 和 [internal/platform/rpc/server.go:37](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L37)。

| 模块 | key 数 | 证据 |
| --- | ---: | --- |
| `thread` | 29 | [internal/module/thread/rpc.go:19](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L19) |
| `turn` | 6 | [internal/module/turn/rpc.go:14](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L14) |
| `skill` | 22 | [internal/module/skill/rpc.go:42](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L42) |
| `workspace` | 7 | [internal/module/workspace/rpc.go:13](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L13) |
| `orchestration` | 15 | [internal/sidecar/orch/orchestration/rpc.go:15](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L15) |
| **总计** | **79** | 上述 5 个 `handler.Map{...}` 构造点 [internal/module/thread/rpc.go:19](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L19) [internal/module/turn/rpc.go:14](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L14) [internal/module/skill/rpc.go:42](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L42) [internal/module/workspace/rpc.go:13](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L13) [internal/sidecar/orch/orchestration/rpc.go:15](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L15) |

补充说明：

- `turn` 模块的 6 个 key 里包含两个非 `turn/` 前缀方法：`review/start` 与 `approval/respond`，它们同样注册在 [internal/module/turn/rpc.go:77](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L77) 和 [internal/module/turn/rpc.go:82](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L82)。

## 2. V2 方法总数

V2 的总注册入口是 `registerMethods()`；默认路径保留 full registry，只有 `DisableOffline52Methods=true` 时才会删除 `offline52MethodList()`，见 [go-agent-v2/internal/apiserver/methods.go:134](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L134) 和 [go-agent-v2/internal/apiserver/methods.go:149](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L149)。V2 另有 guard test 固化该 gate 行为，见 [go-agent-v2/internal/apiserver/methods_offline_alignment_test.go:10](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_offline_alignment_test.go#L10)。

| 族 | 方法数 | 证据 |
| --- | ---: | --- |
| `core static + noop` | 13 | [go-agent-v2/internal/apiserver/methods.go:157](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L157) |
| `dashboard dynamic` | 12 | [go-agent-v2/internal/apiserver/dashboard_bindings.go:152](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/dashboard_bindings.go#L152) [go-agent-v2/internal/dashrpc/register.go:89](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L89) |
| `orchestration` | 12 | [go-agent-v2/internal/apiserver/methods_orchestration.go:14](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_orchestration.go#L14) |
| `thread/turn` | 35 | [go-agent-v2/internal/apiserver/methods_thread_turn.go:8](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_thread_turn.go#L8) |
| `skill` | 14 | [go-agent-v2/internal/apiserver/methods.go:229](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L229) |
| `config/account` | 21 | [go-agent-v2/internal/apiserver/methods.go:239](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L239) |
| `workspace` | 5 | [go-agent-v2/internal/apiserver/methods.go:255](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L255) |
| `UI` | 19 | [go-agent-v2/internal/apiserver/methods.go:262](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L262) |
| `debug/stub` | 20 | [go-agent-v2/internal/apiserver/methods.go:320](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L320) |
| **总计** | **151** | 上述各族之和；入口仍是 [go-agent-v2/internal/apiserver/methods.go:134](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L134) |

## 3. 覆盖率 + 缺失清单

原始题面公式 `V3 已注册 / V2 总注册` 的结果是 `79 / 151 = 52.32%`。证据见 V3 79 个 handler 的 5 个构造点 [internal/module/thread/rpc.go:19](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L19) [internal/module/turn/rpc.go:14](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L14) [internal/module/skill/rpc.go:42](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L42) [internal/module/workspace/rpc.go:13](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L13) [internal/sidecar/orch/orchestration/rpc.go:15](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L15) 与 V2 151 个方法的注册入口 [go-agent-v2/internal/apiserver/methods.go:134](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L134)。

推断说明：

- 这个 `52.32%` 会高估“V2 同名迁移覆盖率”，因为 V3 新增了 15 个 V2 不存在的方法：`command/card/*` 7 个 [internal/module/skill/rpc.go:44](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L44)，`workspace/run/files/list` 与 `workspace/run/file/get` [internal/module/workspace/rpc.go:20](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L20)，`agent.snapshot` [internal/sidecar/orch/orchestration/rpc.go:46](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L46)，以及 `task/dag/*`、`task/node/update`、`orchestration/report` 5 个 [internal/sidecar/orch/orchestration/rpc.go:61](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L61)。
- 只看 V2 同名方法的实际交集，当前是 `64 / 151 = 42.38%`；也就是说仍有 **87** 个 V2 方法没有进入当前 V3 RPC 面。

V2 有但 V3 缺的方法清单如下。

### Core / Dashboard / Orchestration 缺口

- `initialize`、`fuzzyFileSearch`、`app/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:159](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L159)。
- `log/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:160](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L160)。
- `log/filters`、`log/relay` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:161](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L161)。
- `initialized`、`fuzzyFileSearch/sessionStart`、`fuzzyFileSearch/sessionUpdate`、`fuzzyFileSearch/sessionStop`、`feedback/upload` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:163](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L163)。
- `dashboard/agentStatus` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:91](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L91)。
- `dashboard/dags` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:92](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L92)。
- `dashboard/taskAcks` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:93](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L93)。
- `dashboard/taskTraces` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:94](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L94)。
- `dashboard/commandCards` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:95](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L95)。
- `dashboard/prompts` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:96](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L96)。
- `dashboard/sharedFiles` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:97](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L97)。
- `dashboard/auditLogs` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:98](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L98)。
- `dashboard/aiLogs` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:99](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L99)。
- `dashboard/busLogs` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:100](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L100)。
- `dashboard/skills` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:101](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L101)。
- `dashboard/dagDetail` 仍缺失，注册点在 [go-agent-v2/internal/dashrpc/register.go:102](/Volumes/bot/super-agent-v3/go-agent-v2/internal/dashrpc/register.go#L102)。
- `agent.saveSubAgent` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods_orchestration.go:24](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_orchestration.go#L24)。
- `agent.deleteSubAgent` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods_orchestration.go:25](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_orchestration.go#L25)。
- `agent.persistSubAgentBinding` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods_orchestration.go:26](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_orchestration.go#L26)。

### Thread / Turn 缺口

- `mock/experimentalMethod` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods_thread_turn.go:112](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_thread_turn.go#L112)。

### Config / Account 缺口

- `model/list`、`collaborationMode/list`、`experimentalFeature/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:242](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L242)。
- `config/read`、`externalAgentConfig/detect` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:243](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L243)。
- `externalAgentConfig/import`、`config/value/write` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:244](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L244)。
- `config/batchWrite`、`config/lspPromptHint/read`、`config/lspPromptHint/write` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:245](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L245)。
- `configRequirements/read`、`account/login/start` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:246](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L246)。
- `account/login/cancel`、`account/logout` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:247](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L247)。
- `account/read`、`account/rateLimits/read` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:248](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L248)。
- `mcpServer/oauth/login`、`config/mcpServer/reload` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:249](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L249)。
- `mcpServerStatus/list`、`windowsSandbox/setupStart` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:250](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L250)。
- `lsp_diagnostics_query` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:251](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L251)。

### UI 缺口

- `ui/preferences/get`、`ui/preferences/set`、`ui/preferences/getAll` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:264](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L264)。
- `ui/projects/get`、`ui/projects/add`、`ui/projects/remove` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:265](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L265)。
- `ui/projects/setActive`、`ui/code/open`、`ui/code/locate` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:266](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L266)。
- `ui/code/save`、`ui/dashboard/get` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:267](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L267)。
- `ui/state/get`、`ui/sidebar/get`、`lsp/gui_file` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:268](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L268)。
- `lsp/gui_grep`、`lsp/gui_structure`、`lsp/gui_inspect` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:269](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L269)。
- `lsp/gui_xref`、`ui/log` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:270](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L270)。

### Debug / Stub 缺口

- `debug/runtime` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:321](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L321)。
- `debug/gc` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:322](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L322)。
- `ml-interceptor/status` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:323](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L323)。
- `workspace-root-options` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:326](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L326)。
- `agent-home` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:327](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L327)。
- `git-origins` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:328](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L328)。
- `mcp-servers` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:329](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L329)。
- `platform-info` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:330](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L330)。
- `open-in-targets` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:331](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L331)。
- `agent-agents-md` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:332](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L332)。
- `local-environments/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:333](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L333)。
- `worktrees/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:334](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L334)。
- `tasks/list` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:335](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L335)。
- `tasks/get` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:336](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L336)。
- `inbox-items` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:337](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L337)。
- `inbox-items/get` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:338](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L338)。
- `pending-automation-runs` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:339](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L339)。
- `mcp/status` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:340](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L340)。
- `config/read-all` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:341](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L341)。
- `diff/get` 仍缺失，注册点在 [go-agent-v2/internal/apiserver/methods.go:342](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L342)。

## 4. V2 契约测试

有 `_contract_test.go`，也有 `_schema_test.go`，但两者覆盖范围并不相同。

- RPC 直接相关的 contract test 主要有 4 组：`App.CallAPI` 路由与参数归一化 [go-agent-v2/cmd/agent-terminal/app_contract_test.go:87](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_contract_test.go#L87)，`approval/respond`/默认路由边界 [go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:17](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go#L17)，`ui/log` 特殊路由处理 [go-agent-v2/cmd/agent-terminal/app_frontend_log_contract_test.go:13](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_frontend_log_contract_test.go#L13)，以及 `ui/state/get` 返回 shape 合同 [go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go:90](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go#L90)。
- `_schema_test.go` 只命中 `go-agent-v2/internal/mcp/runtime_schema_test.go`，覆盖的是 schema provider 注入与透传，不是 RPC 方法合同，见 [go-agent-v2/internal/mcp/runtime_schema_test.go:15](/Volumes/bot/super-agent-v3/go-agent-v2/internal/mcp/runtime_schema_test.go#L15)。
- 其余 `_contract_test.go` 大多是 runner/store/runtime/build/shutdown 合同，不直接覆盖 RPC registry，例如 [go-agent-v2/internal/runner/manager_contract_test.go:79](/Volumes/bot/super-agent-v3/go-agent-v2/internal/runner/manager_contract_test.go#L79)、[go-agent-v2/internal/store/store_db_contract_test.go:35](/Volumes/bot/super-agent-v3/go-agent-v2/internal/store/store_db_contract_test.go#L35)、[go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic_contract_test.go:13](/Volumes/bot/super-agent-v3/go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic_contract_test.go#L13)。

这些 contract/schema 测试命中的 RPC 方法如下。

- `approval/respond`：`CallAPI` 归一化与 approval token 兼容性在 [go-agent-v2/cmd/agent-terminal/app_contract_test.go:141](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_contract_test.go#L141) 和 [go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:45](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go#L45)。
- `thread/list`：默认转发路径与 marshal 失败边界在 [go-agent-v2/cmd/agent-terminal/app_contract_test.go:169](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_contract_test.go#L169) 和 [go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:17](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go#L17)。
- `ui/log`：`CallAPI` special route 与 batched payload shape 在 [go-agent-v2/cmd/agent-terminal/app_contract_test.go:113](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_contract_test.go#L113) 和 [go-agent-v2/cmd/agent-terminal/app_frontend_log_contract_test.go:13](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_frontend_log_contract_test.go#L13)。
- `ui/state/get`：结果 shape、thread scope、archive/prefs 行为在 [go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go:90](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go#L90)。
- 只在 helper 层出现、并未直接验证 handler 语义的方法名还有 `ui/preferences/set`、`ui/state/get`、`turn/start`、`thread/messages`，证据在 [go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:268](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go#L268) 和 [go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:454](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go#L454)。
- `runtime_schema_test.go` 没有覆盖任何 RPC 方法，只覆盖 `AllSchemas()`/`providers().Schema` 的 schema 透传，见 [go-agent-v2/internal/mcp/runtime_schema_test.go:15](/Volumes/bot/super-agent-v3/go-agent-v2/internal/mcp/runtime_schema_test.go#L15)。

对 V3 的迁移建议如下。

- 应迁移 `approval/respond` 的 transport/dispatch 合同，因为 V3 已注册该方法 [internal/module/turn/rpc.go:82](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L82)，而 provider transport 仍会显式调用它 [internal/provider/codexapp/session_approval.go:89](/Volumes/bot/super-agent-v3/internal/provider/codexapp/session_approval.go#L89)。
- 应迁移 `thread/list` 的默认 dispatch 合同，因为 V3 已注册该方法 [internal/module/thread/rpc.go:42](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L42)，provider session 同样依赖它 [internal/provider/codexapp/session.go:151](/Volumes/bot/super-agent-v3/internal/provider/codexapp/session.go#L151)。
- 现阶段不应把 `ui/log` 与 `ui/state/get` 直接塞进当前 V3 RPC 套件。推断依据是：当前 `rpc_handlers` producer 只有 thread/turn/skill/workspace/orchestration 5 个模块 [internal/module/thread/module.go:7](/Volumes/bot/super-agent-v3/internal/module/thread/module.go#L7) [internal/module/turn/module.go:7](/Volumes/bot/super-agent-v3/internal/module/turn/module.go#L7) [internal/module/skill/module.go:15](/Volumes/bot/super-agent-v3/internal/module/skill/module.go#L15) [internal/module/workspace/module.go:9](/Volumes/bot/super-agent-v3/internal/module/workspace/module.go#L9) [internal/sidecar/orch/orchestration/module.go:15](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/module.go#L15)，并由 [internal/platform/rpc/module.go:36](/Volumes/bot/super-agent-v3/internal/platform/rpc/module.go#L36) 聚合；当前 surface 里没有 `ui` family handler producer。
- `_schema_test.go` 当前不需要迁移到 V3 RPC 层；它更适合作为 MCP/runtime 层测试继续保留，除非未来把 schema 透传本身暴露为 RPC 接口，证据仍是 [go-agent-v2/internal/mcp/runtime_schema_test.go:15](/Volumes/bot/super-agent-v3/go-agent-v2/internal/mcp/runtime_schema_test.go#L15)。

## 5. V3 现有测试

`internal/archtest/` 目前有 8 个测试：

- `TestCodeSizeGuard` [internal/archtest/code_size_guard_test.go:10](/Volumes/bot/super-agent-v3/internal/archtest/code_size_guard_test.go#L10)
- `TestDependencyDirection` [internal/archtest/dependency_direction_test.go:32](/Volumes/bot/super-agent-v3/internal/archtest/dependency_direction_test.go#L32)
- `TestWave3DependencyDirection` [internal/archtest/dependency_direction_wave3_test.go:5](/Volumes/bot/super-agent-v3/internal/archtest/dependency_direction_wave3_test.go#L5)
- `TestFxValidateApp` [internal/archtest/fx_graph_test.go:11](/Volumes/bot/super-agent-v3/internal/archtest/fx_graph_test.go#L11)
- `TestMCPFamilyIsolation` [internal/archtest/mcp_family_isolation_test.go:9](/Volumes/bot/super-agent-v3/internal/archtest/mcp_family_isolation_test.go#L9)
- `TestSharedBudget` [internal/archtest/shared_budget_test.go:13](/Volumes/bot/super-agent-v3/internal/archtest/shared_budget_test.go#L13)
- `TestSqlcBoundary` [internal/archtest/sqlc_boundary_test.go:9](/Volumes/bot/super-agent-v3/internal/archtest/sqlc_boundary_test.go#L9)
- `TestTimeoutLocality` [internal/archtest/timeout_locality_test.go:13](/Volumes/bot/super-agent-v3/internal/archtest/timeout_locality_test.go#L13)

`internal/module/*/` 当前的 `_test.go` 分布如下：

- `internal/sidecar/orch/orchestration/execution_test.go` [internal/sidecar/orch/orchestration/execution_test.go:16](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/execution_test.go#L16)
- `internal/sidecar/orch/orchestration/submission_test.go` [internal/sidecar/orch/orchestration/submission_test.go:17](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/submission_test.go#L17)
- `internal/sidecar/orch/orchestration/turn_lifecycle_test.go` [internal/sidecar/orch/orchestration/turn_lifecycle_test.go:14](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/turn_lifecycle_test.go#L14)
- `internal/module/skill/exec_test.go` [internal/module/skill/exec_test.go:13](/Volumes/bot/super-agent-v3/internal/module/skill/exec_test.go#L13)
- `internal/module/skill/skills_fs_test.go` [internal/module/skill/skills_fs_test.go:10](/Volumes/bot/super-agent-v3/internal/module/skill/skills_fs_test.go#L10)
- `internal/module/skill/skills_match_test.go` [internal/module/skill/skills_match_test.go:11](/Volumes/bot/super-agent-v3/internal/module/skill/skills_match_test.go#L11)
- `internal/module/turn/orchestration_starter_test.go` [internal/module/turn/orchestration_starter_test.go:12](/Volumes/bot/super-agent-v3/internal/module/turn/orchestration_starter_test.go#L12)
- `internal/module/turn/service_test.go` [internal/module/turn/service_test.go:18](/Volumes/bot/super-agent-v3/internal/module/turn/service_test.go#L18)
- `internal/module/workspace/service_test.go` [internal/module/workspace/service_test.go:16](/Volumes/bot/super-agent-v3/internal/module/workspace/service_test.go#L16)

零测试覆盖模块的结论：

- `internal/module/thread` 是唯一零 `_test.go` 覆盖的模块。推断依据是：5 个模块目录分别是 [internal/sidecar/orch/orchestration/module.go:1](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/module.go#L1)、[internal/module/skill/module.go:1](/Volumes/bot/super-agent-v3/internal/module/skill/module.go#L1)、[internal/module/thread/module.go:1](/Volumes/bot/super-agent-v3/internal/module/thread/module.go#L1)、[internal/module/turn/module.go:1](/Volumes/bot/super-agent-v3/internal/module/turn/module.go#L1)、[internal/module/workspace/module.go:1](/Volumes/bot/super-agent-v3/internal/module/workspace/module.go#L1)；其中只有后 4 个目录各自命中了 `_test.go` 文件 [internal/sidecar/orch/orchestration/execution_test.go:16](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/execution_test.go#L16) [internal/module/skill/exec_test.go:13](/Volumes/bot/super-agent-v3/internal/module/skill/exec_test.go#L13) [internal/module/turn/service_test.go:18](/Volumes/bot/super-agent-v3/internal/module/turn/service_test.go#L18) [internal/module/workspace/service_test.go:16](/Volumes/bot/super-agent-v3/internal/module/workspace/service_test.go#L16)。
- `internal/platform/rpc` 有 approval/capability 相关测试，但没有 registry/server 完整性测试；当前只命中 [internal/platform/rpc/approval_test.go:5](/Volumes/bot/super-agent-v3/internal/platform/rpc/approval_test.go#L5) 和 [internal/platform/rpc/handler_test.go:14](/Volumes/bot/super-agent-v3/internal/platform/rpc/handler_test.go#L14)。

## 6. 集成测试方案

### Smoke test：必须覆盖的核心链路

- `thread` 生命周期最小链路：`thread/start` -> `thread/list` -> `thread/read` -> `thread/archive` -> `thread/unarchive` -> `thread/delete`，这些入口都集中在 [internal/module/thread/rpc.go:21](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L21) 到 [internal/module/thread/rpc.go:95](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L95)。
- `turn` 最小链路：`turn/start` -> `turn/interrupt`/`turn/forceComplete`，并补 `approval/respond` 合同；对应注册点在 [internal/module/turn/rpc.go:33](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L33) 到 [internal/module/turn/rpc.go:94](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L94)。
- `skill` 最小链路：`command/exec`、`skills/list`、`skills/local/read|write`、`skills/config/read|write`、`skills/match/preview`；这些是当前 skill 面的主路径，见 [internal/module/skill/rpc.go:44](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L44) 到 [internal/module/skill/rpc.go:86](/Volumes/bot/super-agent-v3/internal/module/skill/rpc.go#L86)。
- `workspace` 必须覆盖完整生命周期：`workspace/run/create` -> `get` -> `list` -> `merge`/`abort`，并补上 V3 新增的 `files/list` 与 `file/get`，见 [internal/module/workspace/rpc.go:15](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L15) 到 [internal/module/workspace/rpc.go:21](/Volumes/bot/super-agent-v3/internal/module/workspace/rpc.go#L21)。
- `orchestration` 必须至少覆盖 `agent.launch` -> `agent.submitPrompt` -> `agent.getState` -> `agent.getReport`，以及 V3 新增的 `task/dag/create|get|list`、`task/node/update`；见 [internal/sidecar/orch/orchestration/rpc.go:17](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L17) 到 [internal/sidecar/orch/orchestration/rpc.go:75](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go#L75)。

### handler 注册完整性测试怎么做

- 直接围绕 `rpc_handlers` group 做精确集合测试。`HandlerMapResult` 的聚合点已经在 [internal/platform/rpc/module.go:36](/Volumes/bot/super-agent-v3/internal/platform/rpc/module.go#L36) 到 [internal/platform/rpc/module.go:52](/Volumes/bot/super-agent-v3/internal/platform/rpc/module.go#L52) 固化。
- 先在测试里构造 5 个模块的 handler map，按当前精确值断言 `29 + 6 + 22 + 7 + 15 = 79`；再用 `rpc.Registry(parts...)` 合并后断言 merged set 仍是 79 个唯一 key，合并逻辑见 [internal/platform/rpc/registry.go:5](/Volumes/bot/super-agent-v3/internal/platform/rpc/registry.go#L5)。
- 需要显式做“无重复 key”断言，因为 `Registry` 只是 `merged[name] = handlerFunc` 覆盖写入 [internal/platform/rpc/registry.go:6](/Volumes/bot/super-agent-v3/internal/platform/rpc/registry.go#L6)，`Server.Register` 也是 `maps.Copy` 静默覆盖 [internal/platform/rpc/server.go:37](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L37)。
- 最稳妥的断言方式不是只看长度，而是维护一份显式 expected method set，并对 merged set 做双向 diff。V2 已有同类 guard 先例，可直接参考 [go-agent-v2/internal/guards/rpc_registry_guard_test.go:22](/Volumes/bot/super-agent-v3/go-agent-v2/internal/guards/rpc_registry_guard_test.go#L22) 到 [go-agent-v2/internal/guards/rpc_registry_guard_test.go:45](/Volumes/bot/super-agent-v3/go-agent-v2/internal/guards/rpc_registry_guard_test.go#L45)。

### 建议的测试架构

- 第一层放 `internal/platform/rpc`：做 registry completeness test，锁定 79 个当前 key、各模块计数、无重复 key，以及 V2 缺口清单的 expected diff。聚合入口和 silent-overwrite 风险都在 [internal/platform/rpc/module.go:50](/Volumes/bot/super-agent-v3/internal/platform/rpc/module.go#L50)、[internal/platform/rpc/registry.go:5](/Volumes/bot/super-agent-v3/internal/platform/rpc/registry.go#L5)、[internal/platform/rpc/server.go:37](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L37)。
- 第二层放 in-process dispatch smoke：优先使用 `Server.Dispatch(...)` 而不是起真实 ws server，这样可以直接覆盖 handler 解码、调用和编码链路，入口已在 [internal/platform/rpc/server.go:43](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L43) 到 [internal/platform/rpc/server.go:65](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L65)。
- 第三层放模块级 rpc integration：给 thread/turn/skill/workspace/orchestration 各自准备 fake service/fake resolver，只验证“参数解码 + handler routing + 返回 shape”，不要把 provider/store 真依赖混进同一层。当前 `internal/module/*` 测试大多还停在 service/queue/fs 层 [internal/module/skill/exec_test.go:13](/Volumes/bot/super-agent-v3/internal/module/skill/exec_test.go#L13) [internal/module/turn/service_test.go:18](/Volumes/bot/super-agent-v3/internal/module/turn/service_test.go#L18) [internal/module/workspace/service_test.go:16](/Volumes/bot/super-agent-v3/internal/module/workspace/service_test.go#L16)。
- 第四层放 app/Fx 装配烟测：继续保留 `fx.ValidateApp(app.Module)` 这种装配守卫，并在其上补一个最小 RPC dispatch smoke；现有装配守卫见 [internal/archtest/fx_graph_test.go:11](/Volumes/bot/super-agent-v3/internal/archtest/fx_graph_test.go#L11)。

## 结论

- 以当前 V3 五个 handler producer 为口径，RPC surface 是 **79** 个方法；V2 full registry 基线是 **151** 个方法，原始覆盖率 **52.32%**，但同名迁移覆盖率只有 **42.38%**，仍缺 **87** 个 V2 方法 [internal/module/thread/rpc.go:19](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L19) [go-agent-v2/internal/apiserver/methods.go:134](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods.go#L134)。
- V2 的 RPC 合同测试并不广，核心只锁住了 `approval/respond`、`thread/list`、`ui/log`、`ui/state/get`；其中前两项应立即迁到 V3 transport/dispatch 层，后两项则取决于 UI family 是否回归当前 V3 RPC surface [go-agent-v2/cmd/agent-terminal/app_contract_test.go:87](/Volumes/bot/super-agent-v3/go-agent-v2/cmd/agent-terminal/app_contract_test.go#L87) [go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go:90](/Volumes/bot/super-agent-v3/go-agent-v2/internal/apiserver/methods_ui_state_contract_test.go#L90) [internal/module/turn/rpc.go:82](/Volumes/bot/super-agent-v3/internal/module/turn/rpc.go#L82) [internal/module/thread/rpc.go:42](/Volumes/bot/super-agent-v3/internal/module/thread/rpc.go#L42)。
- V3 当前最大的测试缺口不是业务 service，而是 `thread` 模块零测试，以及 `rpc_handlers` 聚合路径没有精确注册完整性 guard；优先级应高于继续补零散 unit test，因为 `Registry` 和 `Server.Register` 都会静默覆盖重复 key [internal/module/thread/module.go:1](/Volumes/bot/super-agent-v3/internal/module/thread/module.go#L1) [internal/platform/rpc/registry.go:5](/Volumes/bot/super-agent-v3/internal/platform/rpc/registry.go#L5) [internal/platform/rpc/server.go:37](/Volumes/bot/super-agent-v3/internal/platform/rpc/server.go#L37)。
