# orch 保留真实会话验收

日期：2026-09-08。应用任务名：**orch 可见真实会话验收**。

- 原生 thread ID：`01a07faa-8e8e-73a2-be36-498fbcf8ddff`
- orch agent ID：`visible-e2e-20260908-151715`
- 工作目录：`/Volumes/hello/wj/super-agent-v3`
- 保留运行目录：`.workspace/orch-visible-e2e/20260908-151715`
- 原生会话目录使用实际 `/Users/mima0000/.codex`，未使用临时 provider home。认证文件没有复制进运行目录。

当前源码构建的独立 orch 二进制通过 HTTP/MCP 创建会话。第一轮实际执行 shell 读取运行目录中的随机 challenge，返回 `44a4f062fe1c58ed2e28140a` 和计算结果 423。第二轮通过 orch `send_message`，不调用工具，从上下文回忆相同字符串并返回 442，report_seq 从 2 增至 3，thread ID 不变。

Codex 应用 `list_threads` 确认会话已出现；`read_thread` 读取到前两轮完整消息与第一轮 shell 执行结果；`navigate_to_codex_page` 返回成功，任务已在应用中打开。随后正常停止本次 orch daemon，保留二进制、SQLite、MCP 记录和原生会话。通过应用向同一 thread 发送第三轮续聊，完成事件无错误；原生 rollout 中记录相同字符串与结果 450，确认会话可从应用继续。

## 保留的异常与边界

首次尝试续发紧接报告返回，运行态仍忙，得到 `submission_outcome_unknown`/busy。未重发该请求；之后确认运行态 idle，再发送独立的上下文验收任务。完整失败响应保留在 `mcp-transcript.json`，本次不是无错误单次自动运行。

应用续聊的第三轮返回 completed，但当前 `read_thread` 工具返回该轮 `items: []`。第三轮回答通过本会话精确原生 rollout 核实，不将工具返回空 items 描述为已读取到完整 UI 消息，也未作截图级视觉验收。

此前 orch daemon 在两轮完成后曾正常停止；随后为补齐 MCP 暴露已重新启动并保持运行。真实会话在一次归档/恢复操作后保留且处于未归档状态，仍可在应用继续。此轮验证 Codex 的保留会话，不代表 Claude 或全部 direct-peer 场景也已完成相同验证。

## 本地证据

- `summary.json`：身份、两轮 orch 报告及运行态。
- `mcp-transcript.json`：真实请求与响应，包含 busy 拒绝。
- `app-continuation-evidence.json`：第三轮原生完成证据。
- `state/`：保留的 orch SQLite 数据。
- 原生记录：`/Users/mima0000/.codex/sessions/2026/09/08/rollout-2026-09-08T15-17-59-01a07faa-8e8e-73a2-be36-498fbcf8ddff.jsonl`。

上述前三个 JSON 均位于保留运行目录。该目录中的访问凭据不应写入报告或提交版本库。

## 补验：应用模型实际调用 orch

原验收遗漏了客户端 MCP 注册。已在本机私有用户配置 `~/.codex/config.toml` 注册 `mcp_servers.orch`（HTTP loopback，带认证），保留原配置备份，未将凭据写入仓库配置。此配置对使用该用户配置的客户端可见，不是仅对单个 thread 生效。

仅注册后，旧会话仍未发现工具；独立 CLI resume 被活动写入者保护拒绝，未绕过锁。通过应用归档后恢复同一任务，重新加载后真实工具调用成功：

- `mcp__orch__list_agents({include_inactive:true})` 返回原 agent 与 thread ID。
- `mcp__orch__get_agent_report({agent_id:"visible-e2e-20260908-151715"})` 返回 `report_seq=3`、`state=idle`。
- 原生记录存在上述调用对应的 `custom_tool_call`/`custom_tool_call_output`，两个 MCP 响应均 `isError=false`。调用证据位于保留运行目录的 `orch-tool-exposure-evidence.json`。

服务本次保持运行；固定监听地址已写入本地运行配置。此轮完成的是用户环境的 MCP 注册与应用真实调用，不代表生产 `launch_agent` 已实现对任意新 agent 自动注入 orch，也不代表全部 direct-peer agent 权限已验收。

## 真实用户配置完整链路复验

用户随后要求使用真实配置复跑，并明确将 orch 改为不需要审批。最终私有用户配置为 `mcp_servers.orch.default_tools_approval_mode = "approve"`，此前两个单工具例外已移除；其他 MCP 和沙箱策略保持原值。

实际应用模型通过注册的 orch MCP，调用保留运行中的真实二进制，创建 agent `real-config-e2e-153702`，原生 thread 为 `01a07fbd-216c-7800-a92b-866f5c3fbb2f`，应用标题“orch 真实配置二进制 E2E”。运行二进制 SHA-256 与本轮从当前源码重新构建的文件完全相同。

首轮实际读取新的随机 challenge 并计算 438；第二轮不读文件，从上下文回忆同一 challenge 并计算 1314。初始子 agent orch 调用受审批策略阻断，因此记录为配置修复后的复验通过，不能计为首次无故障通过。

经真实 stop_agent/recover_agent 重载最终配置，同一个原生 thread 已累计完成四轮。子 agent 实际调用 list_agents、get_agent_report 成功；随后又调用此前没有单独放行过的 list_models 和 list_agents，两项原生工具响应均 isError=false。父会话读取最终 report_seq=9，state=idle，保留会话及服务。

本轮详细结果及父/子原生工具记录保存在 `.workspace/orch-visible-e2e/20260908-151715/real-config-153702/` 下的 `result.json`、`parent-tool-records.json`、`child-tool-records.json`。这是实际用户配置的应用模型→MCP→二进制→原生 agent→orch MCP 链路；与此前隔离自动测试统计分别记录。
