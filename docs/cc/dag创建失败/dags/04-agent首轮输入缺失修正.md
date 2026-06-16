# 修复点 4：agent 节点首轮输入缺失修正

## 目标

让聊天创建出来的自动化 DAG 在运行时一定会向子 agent 提交首轮执行输入，避免出现“agent 已启动、消息页返回 `{}`、节点一直 running 但没有产出”的空跑。

这个问题不是上一轮 bugfix 直接改坏执行器导致的。上一轮修复让 prompt 资源发现和 DAG 创建校验跑通后，暴露出更下一层的 schema 缺口：创建入口允许模型提交 `config.input.task`，但运行执行器只读取 `config.first_turn`。

用户仍然不应该填写 `first_turn`、`prompt_key`、`provider` 等内部字段。聊天 agent 应自动生成正确 DAG；如果生成了错误 schema，工具入口必须 fail-fast 拒绝落库，而不是保存一个会空跑的 DAG。

## 当前问题

运行时 `node_type=agent` 的执行配置由 `cmd/mcp-orch/orchestration/nodeexec.AgentNodeConfig` 解析：

```go
type AgentNodeConfig struct {
	Exec      AgentExecConfig `json:"exec"`
	Inputs    InputsConfig    `json:"inputs,omitempty"`
	Outputs   OutputsConfig   `json:"outputs,omitempty"`
	FirstTurn string          `json:"first_turn,omitempty"`
}
```

执行器构造启动请求时只把 `FirstTurn` 写入首轮 prompt：

```go
req := buildLaunchRequestFromAgentConfig(cfg, node, runCtx)
req.Prompt = composePrompt(inputsPrefix, artifactOutputContract(cfg.Outputs.ToArtifact), cfg.FirstTurn)
```

而本次实际创建出的节点形态是：

```json
{
  "config": {
    "exec": {
      "prompt_key": "main/dag_designer_zh",
      "provider": "codex",
      "model": "gpt-5.4-mini",
      "cwd": "/repo"
    },
    "input": {
      "task": "生成一份中文热点新闻简报。",
      "outputs": {
        "to_sharedfile": "reports/news/daily-briefing.md"
      }
    }
  }
}
```

这里有两个问题：

- `input` 是单数，执行器只认 `inputs`。
- `task` 不会映射成 `first_turn`。
- `outputs` 被放进 `input.outputs`，执行器只认顶层 `outputs`。

Go 的 `json.Unmarshal` 会忽略未知字段，所以 `config.input.task` 会在解析时静默丢失。结果是：

```text
task_start_dag 入队成功
-> wakeup dispatcher 启动子 agent
-> LaunchRequest.Prompt 为空
-> submitInitialLaunchPrompt 跳过
-> 没有 turn/started、没有 TurnCompleted、没有 rollout
-> thread.MessagesPage total_count=0
-> 节点 result 仍是默认 {}
```

这违反了 fail-fast 规则，也会让用户误以为“消息发过去了但模型没回复”。

## 修改范围

预计修改文件：

- `cmd/mcp-orch/tools/task_schemas.go`
- `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go` 或新增同包 shape 校验 helper
- `cmd/mcp-orch/orchestration/nodeexec/config_test.go`
- 如需要强化工具说明，再改：
  - `internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-zh/00-runtime-tools.md`
  - `internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-en/00-runtime-tools.md`

不建议修改：

- 不要在运行时把空 prompt 当作成功。
- 不要在执行器里把 `input.task` 静默兜底为 `first_turn`。
- 不要为了兼容坏 DAG 自动填默认任务文本。

## 如何修改

### 1. 创建阶段校验 agent 节点首轮输入

在 `validateAgentNodeLaunchConfigs` 或其下游 helper 中，解析 agent config 后增加校验：

```text
executable agent node must provide non-empty config.first_turn
```

错误信息建议包含字段路径：

```text
nodes[0].config.first_turn required for executable agent node "generate_briefing"
```

这样聊天 agent 能看到明确错误并重建正确 DAG；用户侧只看到“系统没有生成完整任务配置，任务未创建”，不需要理解内部字段。

### 2. 拒绝常见旧/错 schema

在创建入口对 raw `config` 做形状检查，至少拒绝这些字段：

- `config.input`
- `config.input.task`
- `config.input.outputs`
- `config.output_file`
- `config.prompt_key`
- `config.provider`
- `config.model`
- `config.cwd`

建议实现为小 helper：

```go
func validateAgentConfigShape(raw json.RawMessage, label, nodeKey string) error
```

注意不要只依赖 typed struct 解码，因为 `json.Unmarshal` 会忽略未知字段。需要先用 `map[string]json.RawMessage` 检查字段名。

如果要做更严格的 allow-list，必须确认现有合法字段不会被误杀。建议 allow：

- `exec`
- `inputs`
- `outputs`
- `first_turn`
- `execution`

其中 `execution` 是当前 `task_create_dag` flat execution shortcut 可能写入的配置块，不能贸然拒绝。

### 3. 不做隐式迁移

不要把：

```json
{"input":{"task":"..."}}
```

自动改成：

```json
{"first_turn":"..."}
```

原因：

- `input.task` 不是当前执行器契约。
- `input.outputs` 也不是当前输出契约。
- 静默转换会掩盖模型生成错误，未来还会继续创建半兼容 DAG。

正确行为是创建失败，模型收到字段级错误后重新调用 `task_create_dag`，用当前 schema 创建。

### 4. 强化工具 schema 和 prompt 约束

`task_create_dag` 的 `config` 当前是 `RawObjectSchema`，描述应明确说明 agent 节点必须使用：

```json
{
  "exec": {
    "prompt_key": "main/expert/prompt",
    "provider": "codex",
    "model": "gpt-5.4-mini",
    "cwd": "/absolute/project/cwd"
  },
  "first_turn": "执行这次自动化任务的自然语言指令。",
  "outputs": {
    "to_sharedfile": {
      "path": "reports/final.md",
      "lock_mode": "exclusive"
    },
    "to_node_result": true
  }
}
```

如果 prompt 文档已写 `first_turn`，仍需要创建入口校验。模型可能忽略提示词，工具入口必须是最终防线。

## 回归测试

### 缺 first_turn 的 agent 节点被拒绝

输入：

```json
{
  "dag_key": "bad_first_turn",
  "title": "Bad First Turn",
  "nodes": [{
    "node_key": "brief",
    "title": "生成简报",
    "node_type": "agent",
    "assigned_to": "bad_first_turn_brief_runner",
    "config": {
      "exec": {
        "prompt_key": "main/dag_designer_zh",
        "provider": "codex",
        "model": "gpt-5.4-mini",
        "cwd": "/repo/a",
        "codex_home": "/tmp/codex-home",
        "codex_instance_key": "default",
        "codex_model_provider": "openai"
      }
    }
  }]
}
```

期望：

- `HandleCreateDAG` 返回 validation 错误。
- 错误包含 `nodes[0].config.first_turn`。
- DAG 不落库。
- 不产生 run 或 wakeup。

### `config.input.task` 被拒绝

输入：

```json
{
  "config": {
    "exec": {
      "prompt_key": "main/dag_designer_zh",
      "provider": "codex",
      "cwd": "/repo/a",
      "codex_home": "/tmp/codex-home",
      "codex_instance_key": "default",
      "codex_model_provider": "openai"
    },
    "input": {
      "task": "生成热点新闻简报。"
    }
  }
}
```

期望：

- 创建阶段直接失败。
- 错误包含 `config.input` 和 `first_turn` 的修正提示。
- 不启动子 agent。

### 有 first_turn 的 agent 节点可创建并执行

输入：

```json
{
  "config": {
    "exec": {
      "prompt_key": "main/dag_designer_zh",
      "provider": "codex",
      "model": "gpt-5.4-mini",
      "cwd": "/repo/a",
      "codex_home": "/tmp/codex-home",
      "codex_instance_key": "default",
      "codex_model_provider": "openai"
    },
    "first_turn": "生成一份中文热点新闻简报，并写入 reports/news/daily-briefing.md。",
    "outputs": {
      "to_sharedfile": {
        "path": "reports/news/daily-briefing.md",
        "lock_mode": "exclusive"
      },
      "to_node_result": true
    }
  }
}
```

期望：

- `task_create_dag` 成功。
- `task_start_dag` 后子 agent 产生 `turn/started`。
- wakeup 绑定 `bound_turn_id`。
- 节点不再只有默认 `{}`。
- 如果模型执行成功，run 可看到最终输出或 sharedfile 引用。

## 验收标准

- 用户用自然语言创建自动化任务时，不需要了解 `first_turn` 等内部字段。
- 聊天 agent 生成的 DAG 节点必须包含有效 `config.first_turn`。
- `config.input.task`、`config.input.outputs` 等旧/错字段在创建阶段被拒绝。
- 空首轮输入的 agent 节点不能落库，不能产生 wakeup，不能启动空 agent。
- 运行成功路径能看到子 agent 的 `turn/started`、`TurnCompleted` 或明确失败事件。
- UI 不再把空历史表现成“回复 `{}` 但无说明”；若创建失败，应显示可理解的失败原因。

## 验证命令

实现阶段按实际落点运行：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec -count=1
make guard
```

如果修改了内置 prompt asset，再补：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
```
