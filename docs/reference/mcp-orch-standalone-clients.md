# standalone `mcp-orch` 接入 Claude Code 与 Codex CLI

本页描述当前工作树中已实现的独立入口。它不要求启动 V3 主程序；V3 只能作为可选 MCP 客户端。服务进程拥有唯一数据库、单实例锁和受管 CLI 进程。

> 当前复核（2026-09-08）：见 [自动恢复续验记录](mcp-orch-standalone-auto-recovery-validation-2026-09-08.md)。本轮登记矩阵 35/35 通过（17 个涉及真实 daemon binary），Codex 手动恢复及同一 daemon 内受管进程强杀后的自动恢复均取得真实模型证据；本页不是发布授权，必须区分已通过、明确不支持和未验收的边界。

## 启动

先准备三个绝对路径：工作区目录、状态目录和只允许 owner 读取的 bearer 文件。配置文件必须是严格 JSON：

```json
{
  "schema_version": 1,
  "workspace_root": "/absolute/workspace",
  "state_dir": "/absolute/orch-state",
  "listen": "127.0.0.1:0",
  "credential_file": "/absolute/orch-state/bearer-token",
  "principal_ref": "operator/local-user",
  "scope_ref": "workspace/my-project",
  "providers": [
    {
      "name": "codex",
      "executable": "/absolute/bin/codex",
      "home": "/absolute/codex-home",
      "models": ["explicit-model"],
      "sandbox": "workspace-write"
    },
    {
      "name": "claude",
      "executable": "/absolute/bin/claude",
      "home": "/absolute/claude-config",
      "models": ["sonnet"]
    }
  ]
}
```

`principal_ref` 和 `scope_ref` 是服务端配置的 canonical 身份引用，必须显式提供；服务在读取 bearer、获取单实例锁、打开数据库或发布 ready 之前校验它们。它们不从 bearer 内容、路径、客户端 `_meta`、agent ID 或 thread ID 推导。每个 MCP initialize 由同一 server-boot issuer 签发新的短期 session 快照，避免长跑 daemon 复用已经过期的启动快照。

Codex 的 `sandbox` 是必填执行策略：`read-only` 只允许审阅，`workspace-write` 才允许在工作区内完成 coding-agent 修改，`danger-full-access` 仅在明确承担宿主风险时使用。服务不会替 Codex 补默认值；Claude provider 不接受这个字段。

standalone 的 `list_models` 会将配置中的 `codex` 和 `claude` 标记为可直接启动的 provider；这表示 standalone 执行器已接入对应官方 CLI，不表示凭据、配额或模型服务一定可用。实际启动仍以配置中的绝对 executable、home 和显式 models 校验为准。

启动命令：

```text
mcp-orch serve --config /absolute/orch.json --ready-file /absolute/orch-state/ready.json
```

ready 文件只包含 endpoint 和 instance id，不包含 token。相同规范化状态目录只能有一个 owner；不同状态目录在实现语义上是不同实例，已有 `TestMCPOrchServeIndependentStateDomains` 用例覆盖两个独立 binary 的状态目录与停止隔离（是否通过以本次运行记录为准）。owner 异常退出后，内核锁释放，新的 standalone binary 可以接管同一状态目录；不会通过删除或重建锁文件来制造第二 owner。锁接管不保证旧 CLI 子进程、命令 receipt 或任务恢复；优雅关闭才会走受管进程的清理路径。

服务进程发布 ready 时会同时持有该 ready inode 的内核锁。接管同一状态目录前，只有能够取得 ready inode 锁的未完成文件才会被视为崩溃遗留并受锁保护地替换；仍被其他发布者持有的 ready 路径会 fail-closed，绝不覆盖活跃 owner 的发现信息。

## Claude Code

使用 Claude 的 HTTP MCP 配置，将 ready 文件中的 endpoint 和 bearer token 作为 HTTP 头提供。官方 CLI 的配置命令形态如下：

```text
claude mcp add --transport http orch http://127.0.0.1:<port>/mcp --header "Authorization: Bearer <token>"
claude mcp get orch
```

必须先完成 MCP `initialize`，再发送 `notifications/initialized`，之后才能调用工具。连接健康检查使用临时隔离的 `CLAUDE_CONFIG_DIR`；真实工具调用使用宿主默认 keychain/config，受管 Claude 另需显式提供 provider home/model。`ORCH_E2E_CLAUDE_AUTH_DIR` 不是当前测试输入。宿主默认配置可能含个人设置、插件和 MCP 注册，不得把它误称为隔离认证副本；测试不会删除、复制或改写用户认证。

## Codex CLI

在隔离的 `CODEX_HOME/config.toml` 中声明：

```toml
[mcp_servers.orch]
url = "http://127.0.0.1:<port>/mcp"
bearer_token_env_var = "ORCH_TOKEN"
startup_timeout_sec = 15
```

然后以 `ORCH_TOKEN=<token>` 启动 Codex CLI。Codex app-server 通过 `thread/start` 创建客户端线程，再使用 `mcpServer/tool/call` 调用 `list_agents` 等工具。`send_message(wait_report=true)` 只等待目标当前可提交的 turn；运行中的目标不会被自动排队或打断，显式 `timeout_ms` 同时覆盖提交和等待。

## 跨任务对话（direct-peer）

跨任务消息由同一个 standalone daemon 持久化和投递，客户端不直接传入发送者身份：

1. operator scope 调用 `manage_direct_peer`，为同一 workspace 中已登记的 `sender_agent_id → receiver_agent_id` 创建有向 `can_send`/`can_read` grant；grant 和 revoke 都必须带非空幂等 key，同 key 的不同 digest 会冲突。撤权使用同一个工具的 `action=revoke`。
2. sender 的 trusted agent scope 调用 `send_direct_peer`，必须提供稳定 `idempotency_key` 和非空 `message`。receiver 必须已有服务登记、可恢复的 thread/runtime 且当前可提交（通常为 idle）；服务先写入 `accepted`，再调用 receiver 的后续 turn 提交端口。独立 CLI 路径要求 typed native acknowledgement：Codex 使用 `turn/start` 响应，Claude 等待匹配 session/UUID 的 `--replay-user-messages` 确认。只有确认通过执行代际围栏并与消息状态、事件/outbox 同事务持久化后才返回 `submitted`；旧 error-only 端口、缺确认或持久化结果不明仍保留 `accepted` 并返回不可授权的 `delivery_outcome_unknown`；依赖缺失或 receiver 快照在 provider 调用前明确失败时返回 `delivery_failed`。未知结果不会被降级成失败或自动重发，也不会排队 busy turn。
3. receiver 的 trusted agent scope 用调用方自维护的 `after_cursor` 读取消息；服务端没有 durable cursor，撤权后历史也不再对该 receiver 可见。`read_direct_peer_events` 读取可见的 `grant.*`/`message.*` append-only 历史。

消息、状态事件和 pending outbox 在同一个 SQLite 事务中提交；已有 Fx app 重建测试验证了事务数据可持久化，但独立 `serve` binary 的 crash/restart 恢复仍未验收。standalone runner 已接入 outbox claim/lease/过期接管和投影 ACK；投影失败会保留任务，租约到期后按 at-least-once 语义重放，消费方必须按 `EventID` 去重，当前没有 max-attempt/backoff/dead-letter。`submitted` 仅证明官方 CLI 原生协议确认，不表示模型完成，也不是 OUT 恢复域 authority。完整 `provider_receipt` 与原消息一起保存在 `message.submitted` 事件内；同 key 重放必须按原 message ID 验证该持久确认，旧 `submitted` 字符串或缺字段记录返回 unknown。一次性 `Fresh` 事务标记只允许首次 accepted 消息进入提交路径；accepted 重放不再次调用 provider。provider 已确认但本地提交失败/响应丢失仍可能保持 unknown，不重发、不改用新 key 绕过去重。只有调用前明确失败才能写 `delivery_failed`。详见 [原生提交确认契约](../契约/standalone-native-submission-ack.md)。ST-11 的完整 durable command/attempt/recovery-custody/reconcile 尚未完成；生产 command receipt verifier 仍明确不可用，不能将本次 native acknowledgement 当作该候选协议已获批准或完成。

standalone HTTP profile 给 Claude Code/Codex CLI 的是服务级 operator scope；客户端不能在请求 metadata 中自填 `agent_id`、`thread_id` 或 `cwd` 来伪造某个任务。`send_direct_peer`/`read_direct_peer` 需要由服务端建立的 trusted agent scope，当前不把它们开放成可由任意 operator 选择 sender 的公共旁路；公共客户端应使用 `launch_agent`、`send_message` 和 report 工具操作已登记任务。这样既保留跨任务能力，也不把共同 bearer 或可猜测 task ID 当成跨任务授权。

## 已验证范围与限制

### 停止、终态投影与恢复的语义

同一 daemon 内 Codex CLI 非预期退出先持久化进程失败，再原生恢复原线程并推进代际。提交确认窗口中的退出由原提交 owner 精确暂存；早到模型结果必须先持久化并确认原执行器保留回执，之后才能处理退出。恢复后的报告缓存继续使用持久报告的序号和发生时间，不因切换读源回退或伪造新报告。未终结 active turn 的手动恢复明确拒绝，未知提交不重发。

模型 turn 完成与 CLI 进程停止是两个不同事实。`stop_agent` 不得覆盖已完成 turn 的历史，也不能把停止伪造成第二次模型完成。已提交的成功报告可能先于异步 runtime 投影可见；停止入口须先经既有终态投影器应用该持久事实，再以独立 `session-terminal` head 记录 `process_stopped`。活动 turn 的停止则保留原 turn/thread 身份直到终态提交与投影完成。

旧 turn outbox 晚于进程清理或恢复重试时，只能依据严格匹配的持久身份和生命周期继承证明跳过旧 runtime 投影，仍须交付原 DAG 结果并确认 outbox；不能因 runtime generation 被回收就吞掉旧事件，也不能把旧状态覆盖到新代际。

Codex 的 `recover_agent` 保持现有不可变 provider binding，以原生 unarchive（仅归档时）和 `thread/resume` 恢复同一 provider thread，再推进执行代际；不能用删除/重新插入 binding 或新建空会话冒充恢复。Claude 当前明确拒绝该操作并返回 `capability_unsupported`（`thread_resume`）；不能把新建 Claude session 冒充恢复成功。停止等待超时返回 `stop_agent_timeout`，请求取消返回 `stop_agent_canceled`；两者均不能证明停止已完成，应先通过 `list_agents` 核实状态，不应提示用户等待语言服务器索引。

以下测试范围以对应运行证据为准，不从本节的实现语义推断验收通过。

当前 E2E 已覆盖独立 daemon 启动、单实例竞争、双 HTTP session 的请求/会话隔离（两个 session 复用相同 request ID 仍分别路由）；这不等同于运行中任务的跨 session 事件共享。另有真实 Claude/Codex 客户端连接与工具调用、真实受管 Codex/Claude provider turn，以及 direct-peer 的最小持久授权/消息/cursor/event history/outbox projector 闭环；后者是在进程内 trusted scope 下验证，不是第三方 HTTP bearer 客户端的 agent 身份旁路。`TestStandaloneDirectPeerSubmitsReceiverTurn`、`TestStandaloneDirectPeerSubmitsClaudeReceiverTurn` 和 `TestStandaloneDirectPeerCrossProviderConversation` 是成功投递的目标回归；现已接入 typed native acknowledgement，必须以当前精确测试结果判断是否通过；该进程内 Fx 证据不等同于独立 daemon 的 trusted-agent transport 或崩溃恢复验收。工作树中的 `TestStandaloneBinaryLaunchesManagedClaudeThroughMCP` 进一步覆盖真实 `mcp-orch serve` binary 经 HTTP MCP `launch_agent` 启动 Claude、在同一 session 完成第二轮 `send_message(wait_report=true)`；`TestStandaloneCodexClientLaunchesManagedCodexThroughMCP` 则从官方 Codex app-server MCP 客户端走同一条 binary→daemon→managed Codex 链路，验证第二轮 report fence。测试 harness 构造了不带 V3 变量的 provider 环境，命令行检查不含禁用 runtime token，并做一次有界的进程后代快照；这不是读取进程环境或正式 provenance/孤儿清理证明。它们当前仍是未纳入基线的工作树 E2E，证据等级为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。Codex `thread/fork` 的真实 child thread、parent 关系和 child 后续 turn、Codex 在已有 rollout 上的 `thread/archive`，以及 Claude 不支持 archive 时的真实 Stop fallback 也已有受管 E2E。

官方 Claude 工具调用 E2E 需要调用方显式提供 `ORCH_E2E_CLAUDE_BIN` 和显式 `ORCH_E2E_CLAUDE_MODEL`，认证使用宿主默认 keychain/config；测试使用临时 `--mcp-config`，不会把 orch 写入用户 MCP 配置，也不会复制或输出认证凭据。

以下能力仍未完成，不应从当前命令推断出来：Room、完整双向跨任务 receipt、完整事件/内容恢复、硬崩溃接管时的跨 daemon incarnation fence、旧 incarnation follow 隔离、存储不可写后一次性退出事件的自动重投，以及原生 Windows 运行时验收。Codex 手动 `recover_agent` 及同一 daemon 内受管 CLI 异常退出的自动恢复均已通过独立 binary 的同 thread 原生恢复及真实后续模型轮次验证；恢复进程不自动重发未知旧输入，不等同于跨 daemon 崩溃接管。Claude 持久恢复明确不支持。受控执行器句柄丢失后的第二 standalone Fx app/Execution 实例 `thread/resume` E2E 已有证据；这不等同于独立 `serve` daemon 重启、SIGKILL 锁接管或代际验收。Codex fork/archive/interrupt 已验证，但 Codex archive 要求 thread 已产生 rollout；Claude 对 fork/archive/interrupt 明确返回 typed capability error，不会把 Stop 或新会话冒充成功；Claude 的 `stop_agent` 会在归档能力明确缺失时回退到真实 Stop，但不伪造 `archived=true`；通过 MCP `tools/call` 返回时，能力缺失稳定编码为既有 `capability_unsupported`。本地 turn 提交、provider acknowledgement 和模型完成是不同证据，不能互相替代；当前 direct-peer 原生确认不构成跨崩溃恢复域的权威 receipt。上述 standalone binary E2E 使用显式认证目录并会访问真实模型服务；测试进程设置 `NO_COLOR=1`、`DISABLE_TELEMETRY=1` 和 `DISABLE_ERROR_REPORTING=1`，但仍不能把外部 provider 调用当作离线或无副作用。它不是官方发行制品签名、binary provenance、计费隔离或远程 CI 证据。两条 binary→MCP→后续 turn 测试都需要分别提供显式 provider CLI/home/model，Codex 的自然语言正文只作非空/序号/fence 证据，不承诺固定措辞。

## 运行上限与前置条件

- 配置 JSON 最大 64 KiB；bearer credential 文件要求 owner-only、最多 4096 bytes，token 至少 24 个可见 ASCII 字符。provider executable/home 只做绝对路径、存在性和目录/普通文件校验，不做官方身份、可执行位或模型配额探测；`list_models.available=true` 仅表示已配置路由。
- 本地 HTTP profile 设有全局 64 个 request slots、最多 1024 个 session、每 session 最多 64 个并发 `tools/call`；单条 MCP body 上限 1 MiB。HTTP session idle TTL 为 15 分钟，reaper 周期 1 分钟；runner 的 header/read/idle/shutdown 超时分别是 5 秒/15 秒/1 分钟/5 秒。`GET /mcp` 返回 405，当前没有 SSE replay/server-push；`Last-Event-ID` 也不会触发恢复。
- `send_message` 的等待默认共享 RPC deadline 为 30 秒；显式 `timeout_ms` 目前没有更高的服务端上限，会占用一个 request slot，ST-16 的统一有界等待尚未完成。
- direct-peer message body 上限 1 MiB；消息/event 读取 limit 为 1..200（默认 50），outbox claim limit 为 1..200（standalone projector 默认每秒、lease 30 秒、batch 32）。这些是资源边界，不是 provider 并行能力或 SLA；provider session 仍可能 busy。
- Codex 恢复前的归档确认最多读取 128 页、每页 100 条，使用可信 cwd、完整 cursor 和精确 thread ID；超界或响应不完整立即失败，不按“未归档”兜底。
- Claude/Codex JSONL provider 单帧上限 4 MiB；Codex 增量正文累计超过 1 MiB 会 fail-closed，公开终态摘要最多 4096 rune，完整 provider 内容尚无独立 content store。
- `workspace-write` 是传给 Codex 的执行策略，不是 kernel/container sandbox；启动前路径检查不能防止并发恶意替换。standalone stdio connect connector（ST-18）、Room、完整 receipt/reconcile、原生 Windows 和发布 provenance 仍在计划中；现有无参数 legacy stdio 入口仍有 V3 parent-runtime 前置，不是该 connector。
