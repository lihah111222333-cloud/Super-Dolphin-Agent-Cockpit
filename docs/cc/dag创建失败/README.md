# DAG 创建失败总修复文档

## 背景

用户通过聊天创建自动化任务时，DAG 运行失败：

```text
run_key: daily_hot_news_brief#run-manual-2026-06-16-daily-hot-news-brief
status: failed
failure_class: validation
reason: node.config.exec.agent_key or node.config.exec.prompt_key required
```

产品目标是用户只用自然语言描述自动化任务，例如“每天早上生成热点新闻简报”。用户不应该理解或填写 `prompt_key`、`agent_key`、`provider`、`cwd`、`assigned_to` 等内部字段。

## 结论

根因不是运行时校验过严，而是两处链路断开：

1. `mcp-orch` 的 `prompt_list/prompt_get` 只查 SQLite DB，没有合并内置 prompt registry。新库或迁移后 DB 为空时，聊天 DAG designer 查不到可用 `prompt_key`。
2. `task_create_dag` 创建入口没有校验 agent 节点是否具备 `prompt_key` 或 `agent_key`，导致缺执行者身份的坏 DAG 可以落库，直到运行时自动派发才失败。

因此修复应同时覆盖：

- 用户无感资源发现：聊天 agent 总能发现系统内置或用户已创建的自动化执行模板。
- 创建入口 fail-fast：任何缺执行者身份的 agent 节点或 hybrid verifier 不能落库。
- 端到端用户体验：聊天层自动填内部字段，最终用户只看到任务名称、触发规则、输出位置和失败原因。

## 源码追溯

### 运行时失败位置

`cmd/mcp-orch/store/taskdag/store_dispatch_guard.go`

```go
func validateAutoAgentExec(exec autoAgentExec, label string) error {
	if strings.TrimSpace(exec.AgentKey) == "" && strings.TrimSpace(exec.PromptKey) == "" {
		return fmt.Errorf("%s.agent_key or %s.prompt_key required", label, label)
	}
	...
}
```

自动派发前要求 agent 节点有 `exec.agent_key` 或 `exec.prompt_key`。这是正确的 fail-fast 校验：没有执行者身份，后端不知道应该启动哪个自动化人物或 prompt 模板。

### 创建入口没有拦住坏 DAG

`cmd/mcp-orch/tools/task_schemas.go`

```go
func validateCreateDAGNodesForCreate(nodes []contract.CreateDAGNodeRequest) error {
	if err := validateRootAgentAssignees(nodes); err != nil {
		return err
	}
	return validateAgentNodeLaunchConfigs(nodes)
}
```

`validateAgentNodeLaunchConfigs` 当前校验 `provider` 和 Codex 身份字段，但没有校验 `exec.prompt_key` 或 `exec.agent_key`。因此模型生成的坏节点能被保存。

### 聊天 DAG designer 被要求不要编造 prompt_key

`internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-zh/00-runtime-tools.md`

关键约束：

```text
查资源：调用 list_models()、prompt_list(keyword?)、command_list(keyword?)、shared_file_list(prefix?)。
禁止凭记忆编 provider/model/prompt_key/agent_key/command_ref/sharedfile path。

prompt_list 返回 prompt_templates。
agent 节点优先使用 exec.prompt_key = 返回的 prompt_key。
```

所以当 `prompt_list` 返回空时，聊天 agent 不应该编造 `prompt_key`。它创建出缺身份节点，是资源发现链路失败后的下游症状。

### `prompt_list` 只查 DB

`cmd/mcp-orch/tools/prompt_tools.go`

```go
templates, err := store.List(ctx, promptstore.ListFilter{
	AgentKey:       "",
	Keyword:        strings.TrimSpace(input.Keyword),
	CWD:            cwd,
	RuntimeVisible: true,
	Limit:          resourceListLimit,
})
```

`cmd/mcp-orch/store/prompt/store.go`

```go
rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{...})
```

该路径没有合并 `internal/platform/shared/builtinprompts` 的内置 prompt registry。内置 prompt 实际存在，但 `mcp-orch` 的 prompt 工具看不到。

### 主线程已有合并 catalog，mcp-orch 没有复用

`internal/module/threadprompt/runtime_catalog.go` 已经实现 builtin + DB 的运行时合并视图，并且同 key 时让 builtin registry 胜出，避免历史 system seed 或用户误写覆盖内置定义。

`mcp-orch` 不能直接复用该实现的主要原因是两边 store 类型不同：

- 主线程使用 `internal/store/prompt`。
- `mcp-orch` 使用 `cmd/mcp-orch/store/prompt`。

因此 `mcp-orch` 需要一个轻量本地 adapter，行为要与主线程 catalog 对齐。

### ToolBridge 只注入运行环境，不注入 prompt 身份

`internal/platform/toolbridge/handler_dag_launch.go`

```go
setArgStringIfMissing(exec, "provider", provider)
setArgStringIfMissing(exec, "codex_home", binding.CodexHome)
setArgStringIfMissing(exec, "codex_instance_key", binding.CodexInstanceKey)
setArgStringIfMissing(exec, "codex_model_provider", binding.CodexModelProvider)
```

这层只继承 provider 和 Codex identity，不会也不应该静默补一个默认 `prompt_key`。

## 修复范围拆分

### 修复点 1：恢复 prompt 资源发现

文档：`docs/cc/dag创建失败/dags/01-prompt资源发现恢复.md`

目标：

- 空 DB 时 `prompt_list` 仍返回内置可执行 prompt。
- `prompt_get` 能读取 `prompt_list` 返回的内置 prompt。
- 聊天 DAG designer 可以无感选择 `prompt_key`，用户不需要填写内部参数。

### 修复点 2：DAG 创建入口执行者身份校验

文档：`docs/cc/dag创建失败/dags/02-dag创建入口校验.md`

目标：

- `task_create_dag` 拒绝缺 `exec.prompt_key/exec.agent_key` 的 agent 节点和 hybrid verifier。
- 坏 DAG 不再落库。
- 错误在创建阶段暴露，避免定时运行时才失败。

### 修复点 3：端到端验收与历史坏数据诊断

文档：`docs/cc/dag创建失败/dags/03-端到端验收与历史坏数据诊断.md`

目标：

- 用空 DB 场景验证自然语言创建自动化任务的完整链路。
- 上线前只读列出已有坏 DAG，不静默批量补默认 prompt。
- 给失败 run 和历史 DAG 模板提供显式重绑或重建处置路径。

## 生产就绪要求

- builtin registry 必须在进程启动时通过 fx 注入并复用，不能在每次 `prompt_list` 调用时重新扫描 embedded assets。
- `prompt_list` 合并结果必须稳定排序、去重、限量，避免不同调用返回顺序抖动影响模型选择。
- `prompt_list` 的 DB 查询错误必须 fail-fast，不能因为 builtin 存在就吞掉 DB 故障；`prompt_get` 对 builtin key 可直接返回 builtin，builtin 未命中后再查询 DB 并 fail-fast。
- 创建入口校验必须覆盖 `node_type=""`、`node_type="agent"` 和 `node_type="hybrid"` 的 verifier 配置，并复用同一套 provider/Codex identity 规则。
- 上线前要有历史坏 DAG 的只读诊断和人工/显式绑定处置路径，不能静默批量补默认 prompt。
- 文档和错误提示要区分工具层技术错误与用户层说明。工具层保留字段路径，聊天层转译成人话。

## 不建议的修复

- 不要移除 `store_dispatch_guard.go` 的运行时校验。
- 不要在运行时或创建入口静默填固定默认 prompt。
- 不要只靠 DB seed 修复。seed 可以补历史数据，但新库、测试库、迁移遗漏仍会复发。
- 不要把技术参数暴露给最终用户，让用户手动填写 `prompt_key` 或 `agent_key`。

## 总体验收标准

- 用户通过自然语言创建常见自动化任务时，不需要理解或填写 `prompt_key/agent_key/provider/cwd/assigned_to`。
- 在空 `prompt_templates` DB 的环境里，`prompt_list` 仍能返回内置 runtime-visible prompt。
- `prompt_list` 与 `prompt_get` 对同一个 prompt_key 的来源优先级一致；同 key 不重复、不会 list 出来却 get 不到。
- builtin key 隐藏 DB 同 key 的规则在 keyword 过滤前生效，历史 seed 不能通过 keyword 绕过内置优先级。
- DAG designer 生成的 agent 节点包含 `node.config.exec.prompt_key` 或 `node.config.exec.agent_key`。
- DAG designer 生成的 hybrid verifier 包含 `node.config.exec.verifier.prompt_key` 或 `node.config.exec.verifier.agent_key`。
- `task_create_dag` 对缺执行者身份的 agent 节点或 hybrid verifier 返回创建阶段错误，且不保存坏 DAG。
- `task_create_dag` 对 agent 节点和 hybrid verifier 的 provider/Codex identity 校验一致。
- 原失败场景不再出现运行时错误：`node.config.exec.agent_key or node.config.exec.prompt_key required`。
- 端到端验收通过：在空 DB 环境，用户用自然语言创建“每日热点新闻简报”自动化任务，系统创建出可启动 DAG，用户无需填写内部字段。
- 上线前能列出已有缺 `prompt_key/agent_key` 的 DAG 节点，并给出显式重绑或重建方案。

## 建议实现顺序

1. 先做 `prompt_list/prompt_get` 合并 builtin registry，并补空 DB 回归测试。
2. 再做 `task_create_dag` 创建入口身份校验，并补坏 DAG 拒绝测试。
3. 最后做端到端验收与历史坏数据诊断，确认空 DB 用户路径可用且历史坏数据有显式处置方案。
4. 跑 `cmd/mcp-orch/tools`、`cmd/mcp-orch` 相关测试和 guard。
