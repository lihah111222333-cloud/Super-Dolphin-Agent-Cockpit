# 修复点 2：DAG 创建入口执行者身份校验

## 目标

`task_create_dag` 在创建阶段拒绝缺少执行者身份的 agent 节点和 hybrid verifier，避免坏 DAG 落库后在定时运行或手动启动时才失败。

这个修复不是让用户填写 `prompt_key` 或 `agent_key`，而是系统防线：聊天 agent 应该自动填这些字段；如果没有填完整，创建入口必须 fail-fast。

## 当前问题

`cmd/mcp-orch/tools/task_schemas.go` 的 `validateCreateDAGNodesForCreate` 当前调用：

```go
validateRootAgentAssignees(nodes)
validateAgentNodeLaunchConfigs(nodes)
```

`validateAgentNodeLaunchConfigs` 校验了：

- agent 节点有 `config.exec` 时必须有 `provider`。
- `provider=codex` 时必须有 `codex_home`、`codex_instance_key`、`codex_model_provider`。

但它没有校验：

- `config.exec.prompt_key`
- `config.exec.agent_key`
- `config.exec.verifier.prompt_key`
- `config.exec.verifier.agent_key`
- hybrid verifier 的 `provider/codex_home/codex_instance_key/codex_model_provider`

因此以下节点可能创建成功：

```json
{
  "node_key": "final",
  "node_type": "agent",
  "assigned_to": "daily_hot_news_brief_final_runner",
  "config": {
    "exec": {
      "provider": "codex",
      "model": "gpt-5",
      "cwd": "/repo/project",
      "codex_home": "/home/user/.codex",
      "codex_instance_key": "default",
      "codex_model_provider": "openai"
    }
  }
}
```

该节点缺 `prompt_key/agent_key`，运行时会在 `store_dispatch_guard.go` 失败。

hybrid 节点也有同类风险。运行时 `validateAutoHybridConfig` 会校验 `node.config.exec.verifier`，但创建入口如果不提前校验，缺 verifier 身份的 hybrid 节点也可能落库后才失败。

## 修改范围

预计修改文件：

- `cmd/mcp-orch/tools/task_schemas.go`
- `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go`

## 如何修改

### 1. 在创建入口复用 agent exec launch 校验

在 `validateAgentNodeLaunchConfigs` 中解析 `cfg := nodeexec.ParseAgentConfig(node.Config)` 后，用统一 helper 校验完整 launch 配置。不要先写只校验身份的 helper，再写 provider helper，否则 agent 节点和 hybrid verifier 很容易出现两套规则。

错误信息面向工具层可以保留字段路径；聊天层再转译成用户可理解的话，例如：

```text
系统没有为该自动化节点绑定执行模板，任务未创建。请恢复内置提示词或创建可用执行模板后重试。
```

### 2. 抽出可复用的 agent exec launch 校验

不要只在 agent 节点里散写校验。`nodeexec.HybridExecConfig.Verifier` 也是 `nodeexec.AgentExecConfig`，所以建议抽出一个 helper，同时覆盖身份和 provider/Codex 字段：

```go
func validateAgentExecLaunchConfig(exec nodeexec.AgentExecConfig, label, nodeKey string) error {
	if strings.TrimSpace(exec.PromptKey) == "" && strings.TrimSpace(exec.AgentKey) == "" {
		return fmt.Errorf("%s.prompt_key or %s.agent_key required for agent node %q", label, label, nodeKey)
	}
	provider := strings.ToLower(strings.TrimSpace(exec.Provider))
	switch provider {
	case "":
		return fmt.Errorf("%s.provider required for agent node %q; set provider to claude or codex", label, nodeKey)
	case "claude":
		return nil
	case "codex":
		if missing := missingCodexIdentityFields(exec); len(missing) != 0 {
			return fmt.Errorf("%s provider=codex for agent node %q requires %s", label, nodeKey, strings.Join(missing, ", "))
		}
		return nil
	default:
		return fmt.Errorf("%s.provider invalid for agent node %q: must be claude or codex", label, nodeKey)
	}
}
```

agent 节点调用：

```go
if err := validateAgentExecLaunchConfig(cfg.Exec, fmt.Sprintf("nodes[%d].config.exec", i), node.NodeKey); err != nil {
	return err
}
```

这样 agent 节点和 hybrid verifier 使用同一套规则，避免未来一个路径修了、另一个路径继续漏。

### 3. 保持 provider/Codex 校验

不要删除当前 provider 校验：

```go
provider := strings.ToLower(strings.TrimSpace(cfg.Exec.Provider))
switch provider {
case "":
	...
case "claude":
	...
case "codex":
	...
}
```

身份校验和 provider 校验分别保护两个维度：

- `prompt_key/agent_key`：用哪个自动化人物或 prompt 模板执行。
- `provider/codex identity`：用哪个 provider 和运行身份启动。

### 4. 覆盖 hybrid verifier

如果 `node_type == "hybrid"` 且 `config.exec.verifier` 存在，也必须用 `validateAgentExecLaunchConfig` 校验 verifier 的 `prompt_key/agent_key/provider/codex identity`。

建议不要把 hybrid verifier 当作普通 agent 节点解析。应复用或补充现有 hybrid config 解析结构，直接读取：

```json
{
  "config": {
    "exec": {
      "verifier": {
        "prompt_key": "main/review-task",
        "provider": "codex",
        "cwd": "/repo/project"
      }
    }
  }
}
```

验收标准是创建阶段和运行时对 agent 身份字段的要求一致。

### 5. 不要在这里静默补默认 prompt

`task_create_dag` 是持久化入口，不能把缺失字段替换成固定默认值。静默默认会导致任务用错执行模板，看似创建成功但行为错误。

正确路径是：

```text
prompt_list 可靠返回资源
-> DAG designer 自动选择 prompt_key
-> task_create_dag 校验完整性
```

## 已有坏 DAG 的处置

这个修复会阻止新坏 DAG 落库，但不会自动修复历史数据。上线前应增加一次只读诊断或运维检查，列出已存在的坏配置：

- agent 节点：`node_type=""` 或 `node_type="agent"`，有 `config.exec`，但 `config.exec.prompt_key` 和 `config.exec.agent_key` 都为空。
- hybrid 节点：`node_type="hybrid"`，有 `config.exec.verifier`，但 verifier 缺 `prompt_key/agent_key/provider` 或 Codex identity。

处置策略：

- 不自动写入默认 prompt。
- 对还未运行的 DAG，建议由聊天层或管理入口重新绑定明确 prompt 后更新。
- 对已失败 run，保留失败事件作为审计证据，修复 DAG 模板后重新手动启动。
- 如果需要批量修复，必须先通过 `prompt_list/prompt_get` 选出明确模板，再生成可审计的更新操作。

## 回归测试

在 `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go` 增加测试。

### 缺 prompt_key/agent_key 的 agent 节点被拒绝

测试输入示例：

```json
{
  "dag_key": "missing_prompt_identity",
  "title": "缺执行者身份测试",
  "nodes": [
    {
      "node_key": "final",
      "title": "最终输出",
      "node_type": "agent",
      "assigned_to": "missing_prompt_identity_final_runner",
      "config": {
        "exec": {
          "provider": "codex",
          "cwd": "/repo/project",
          "codex_home": "/tmp/codex-home",
          "codex_instance_key": "default",
          "codex_model_provider": "openai"
        }
      }
    }
  ],
  "final_node_key": "final"
}
```

期望：

- `HandleCreateDAG` 返回错误。
- 错误包含 `prompt_key` 或 `agent_key required`。
- store 没有创建 DAG。

### 有 prompt_key 的 agent 节点继续通过

测试输入：

```json
{
  "config": {
    "exec": {
      "prompt_key": "main/code-task",
      "provider": "codex",
      "cwd": "/repo/project",
      "codex_home": "/tmp/codex-home",
      "codex_instance_key": "default",
      "codex_model_provider": "openai"
    }
  }
}
```

期望：

- 原有通过路径不回归。

### 有 agent_key 的 agent 节点继续通过

测试输入：

```json
{
  "config": {
    "exec": {
      "agent_key": "daily_brief_agent",
      "provider": "claude",
      "cwd": "/repo/project"
    }
  }
}
```

期望：

- 创建入口接受。

### 缺 verifier prompt_key/agent_key 的 hybrid 节点被拒绝

测试输入：

```json
{
  "dag_key": "missing_hybrid_verifier_identity",
  "title": "缺 hybrid verifier 身份测试",
  "nodes": [
    {
      "node_key": "review",
      "title": "执行并复核",
      "node_type": "hybrid",
      "assigned_to": "missing_hybrid_verifier_identity_review_runner",
      "config": {
        "exec": {
          "automation": {
            "kind": "command_card",
            "command_ref": "run_tests"
          },
          "verifier": {
            "provider": "codex",
            "cwd": "/repo/project",
            "codex_home": "/tmp/codex-home",
            "codex_instance_key": "default",
            "codex_model_provider": "openai"
          }
        }
      }
    }
  ],
  "final_node_key": "review"
}
```

期望：

- `HandleCreateDAG` 返回错误。
- 错误包含 `config.exec.verifier.prompt_key` 或 `config.exec.verifier.agent_key`。
- store 没有创建 DAG。

### hybrid verifier 缺 provider 或 Codex identity 也被拒绝

测试输入：

```json
{
  "dag_key": "missing_hybrid_verifier_provider",
  "title": "缺 hybrid verifier provider 测试",
  "nodes": [
    {
      "node_key": "review",
      "title": "执行并复核",
      "node_type": "hybrid",
      "assigned_to": "missing_hybrid_verifier_provider_review_runner",
      "config": {
        "exec": {
          "automation": {
            "kind": "command_card",
            "command_ref": "run_tests"
          },
          "verifier": {
            "prompt_key": "main/review-task",
            "cwd": "/repo/project"
          }
        }
      }
    }
  ],
  "final_node_key": "review"
}
```

期望：

- `HandleCreateDAG` 返回错误。
- 错误包含 `config.exec.verifier.provider required`。
- store 没有创建 DAG。

## 验收标准

- `task_create_dag` 不再保存缺 `config.exec.prompt_key/config.exec.agent_key` 的 agent 节点。
- `task_create_dag` 不再保存缺 `config.exec.verifier.prompt_key/config.exec.verifier.agent_key` 的 hybrid verifier。
- `task_create_dag` 对 agent 节点和 hybrid verifier 使用同一套 provider/Codex identity 校验。
- 缺身份错误在创建阶段返回，不等到 `task_start_dag` 或自动派发阶段。
- 已有合法 DAG 创建测试仍通过。
- provider/Codex identity 校验仍保留。
- 创建阶段校验与 `store_dispatch_guard.go` 的运行时校验保持一致，避免同类坏配置绕过。
- 最终用户不需要理解字段名；聊天层可以把技术错误转译成“系统没有可用自动化执行模板”。
- 已有坏 DAG 有只读诊断和人工/显式绑定处置路径；系统不静默批量补默认 prompt。

## 验证命令

修改 Go 文件后先跑单文件 guard：

```bash
./scripts/test_with_guard.sh cmd/mcp-orch/tools/task_schemas.go
./scripts/test_with_guard.sh cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go
```

再跑工具包测试：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -count=1
```

如果后续实现触及运行时 guard，再补：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag -count=1
```
