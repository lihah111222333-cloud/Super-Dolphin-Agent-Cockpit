# Codex 工具映射

技能使用 Claude Code 的工具名称。当你在技能中遇到这些名称时，使用所在平台的等价工具：

| 技能引用 | super-agent-v3 / Codex 等价工具 |
|-----------------|------------------|
| `Task` 工具（派发子代理） | 使用平台当前可用的子代理能力；需要持久 DAG/重试/租约/交接记录时可选使用 `mcp-orch` 的 `task_*` 工具 |
| 多个 `Task` 调用（并行） | 原生多代理可直接并行；若选择 mcp-orch，则给每个独立任务建 node |
| Task 返回结果 | 收集子代理返回摘要；若本轮使用 mcp-orch，再用 `task_update_node` 写入 `done` / `failed` / `blocked` |
| Task 自动完成 | 节点收口后释放本地代理资源 |
| `TodoWrite`（任务跟踪） | `update_plan` |
| `Skill` 工具（调用技能） | 技能原生加载：直接遵循指令 |
| `Read`、`Write`、`Edit`（文件） | 使用你的原生文件工具 |
| `Bash`（运行命令） | 使用你的原生 shell 工具 |

## super-agent-v3 子代理编排选择

在本仓库里，子代理生命周期不绑定 mcp-orch。按任务需要选择派发方式：

1. 平台原生子代理/多代理：默认可用路径，适合普通实现、审查、调查和并行拆分。
2. `mcp-orch` 的 `task_*` 工具：可选路径，适合需要持久 DAG 状态、重试/租约、cron/wakeup 或结构化跨代理交接记录的任务。
3. 当前会话执行：适合工具不可用、任务太小、或派发会增加冲突风险的场景。

如果本轮选择 mcp-orch，使用下面生命周期记录：

1. `task_create_dag`：为本轮工作创建 DAG，节点要有 `node_key`、`title`、`node_type`、`assigned_to`、`depends_on` 和可执行 `config`。
2. `task_start_dag`：在用户要求执行时启动 run，读取返回的 `run_id` 与执行状态。
3. `task_dispatch_node`：当 ready 节点缺少 `assigned_to` 或需要人工指派时，带 `dag_key` / `node_key` / `run_id` 显式派发。
4. `task_update_node`：写入 `running`、`done`、`failed` 或 `blocked`，不要只依赖聊天摘要。

如果当前 Codex 会话没有暴露这些 `mcp-orch` 工具，继续使用平台原生子代理能力；只需在报告里说明缺少持久 DAG 观测。

## Codex 多代理兼容说明

添加到你的 Codex 配置（`~/.codex/config.toml`）可启用本地 fallback：

```toml
[features]
multi_agent = true
```

Codex 多代理是本仓库允许的正常派发路径，不是绕过仓库规则。使用它时，用 `update_plan`、子代理返回摘要、文件 diff 和验证命令记录状态；不要伪造 DAG/node/run 证据。

## 命名代理派发

Claude Code 技能会引用 `superpowers:code-reviewer` 这样的命名代理类型。
Codex 没有命名代理注册表；`spawn_agent` 会从内置角色（`default`、`explorer`、`worker`）创建通用代理。

当技能要求派发某个命名代理类型时，优先使用平台可用的命名代理机制。Codex 没有命名代理注册表时，按下面步骤构造 `spawn_agent` 消息；如果本轮选择 mcp-orch，再把同一提示词放入 node 的 `assigned_to` / `config.exec.prompt`。

1. 找到该代理的提示词文件（例如 `agents/code-reviewer.md`，或技能本地提示词模板如 `code-quality-reviewer-prompt.md`）
2. 读取提示词内容
3. 填写任何模板占位符（`{BASE_SHA}`、`{WHAT_WAS_IMPLEMENTED}` 等）
4. 用填好的内容作为 `message`，派发一个 `worker` 代理

| 技能指令 | Codex fallback 等价方式 |
|-------------------|------------------|
| `Task tool (superpowers:code-reviewer)` | `spawn_agent(agent_type="worker", message=...)`，内容来自 `code-reviewer.md` |
| 带内联提示词的 `Task tool (general-purpose)` | `spawn_agent(message=...)`，使用相同提示词 |

### 消息组织

`message` 参数是用户级输入，不是系统提示。为最大化指令遵循度，按下面结构组织：

```
Your task is to perform the following. Follow the instructions below exactly.

<agent-instructions>
[filled prompt content from the agent's .md file]
</agent-instructions>

Execute this now. Output ONLY the structured response following the format
specified in the instructions above.
```

- 使用任务委派式框架（“Your task is...”），而不是人格设定式框架（“You are...”）
- 用 XML 标签包住指令：模型会把带标签块视为权威内容
- 结尾加明确执行指令，防止模型只总结指令

### 何时可以移除此兼容方式

这个方式是在补偿 Codex 插件系统目前尚不支持 `plugin.json` 中的 `agents` 字段。当 `RawPluginManifest` 获得 `agents` 字段后，插件可以符号链接到 `agents/`（类似现有的 `skills/` 符号链接），技能就能直接派发命名代理类型。

## 环境检测

创建 worktree 或完成分支的技能，继续前应使用只读 git 命令检测环境：

```bash
GIT_DIR=$(cd "$(git rev-parse --git-dir)" 2>/dev/null && pwd -P)
GIT_COMMON=$(cd "$(git rev-parse --git-common-dir)" 2>/dev/null && pwd -P)
BRANCH=$(git branch --show-current)
```

- `GIT_DIR != GIT_COMMON` → 已经在链接 worktree 中（跳过创建）
- `BRANCH` 为空 → detached HEAD（无法从沙箱创建分支/推送/PR）

每个技能如何使用这些信号，见 `使用git工作区` 第 0 步和 `结束开发分支` 第 1 步。

## Codex App 收尾

当沙箱阻止分支/推送操作（处于外部管理 worktree 的 detached HEAD）时，代理会提交所有工作，并通知用户使用 App 的原生控件：

- **"Create branch"**：命名分支，然后通过 App UI 提交/推送/创建 PR
- **"Hand off to local"**：把工作转移到用户本地 checkout

代理仍然可以运行测试、暂存文件，并输出建议的分支名、提交信息和 PR 描述，供用户复制使用。
