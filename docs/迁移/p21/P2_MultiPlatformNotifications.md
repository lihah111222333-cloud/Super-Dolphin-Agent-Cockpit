# P2: 多平台通知

## 目标
改变 Agent 的交互模式：从纯粹的 Request/Response (被动) 到 Notification (主动推送)。特别是针对执行较长或后台 Cron 执行的任务，可以将结果或中断提醒发往 Webhook (钉钉、飞书、Slack)。

## 现状校准

- 当前仓库已经有 `contract.ToolNotifier`，但它是 MCP 控制面的配置/订阅通知，不适合复用为外部消息推送抽象。
- 当前还存在 `rpc.PushBridge` / `eventsurface` 这一套 UI JSON-RPC push 面，它也不是外部 webhook egress。
- `turn/service.go` 没有 event-bus 依赖，也不适合继续堆“完成后发通知”这类 side effect。
- 当前配置系统由 `internal/platform/config/config.go` 管理，默认是 env 驱动，没有通用 `config.toml` loader。
- 当前 core app 与 `cmd/mcp-orch` 是两套 Fx/Bus。若通知只注册在 core `internal/module/notify`，看不到 `mcp-orch` 内的 orchestration / DAG 事件。

## 推荐改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 抽象合约 | `internal/contract/message_notifier.go` [NEW] | 定义 alias-first 的外部通知接口，例如 `Enqueue(ctx, NotifyRequest)` 或 `Send(ctx, channelAlias, msg)` |
| 基础实现 | `internal/platform/notify/webhook.go` [NEW] | 实现基础 HTTP POST 能力与共享 timeout/retry 策略 |
| 平台适配 | `internal/platform/notify/{dingtalk,feishu,slack}.go` [NEW] | 负责各平台 payload 渲染 |
| 配置承载 | `internal/platform/config/config.go` | 新增 `NotifyConfig`，表达 alias -> channel config、queue、timeout、retry、allowlist 等 |
| 解析器 | `internal/platform/notify/resolver.go` [NEW] | 内部把 `channelAlias` 解析成 `{Platform, URL, Secret}`；URL/secret 不暴露给业务层 |
| Core 事件订阅模块 | `internal/module/notify/module.go` [NEW] | 订阅 core app 的 turn/provider 事件并 enqueue 外发请求 |
| Orch 事件订阅模块 | `cmd/mcp-orch/...` 或共享 notify package | `mcp-orch` 侧单独注册 DAG/orchestration 通知订阅，或桥接到 core bus |
| 消息渲染 | `internal/module/notify/render.go` [NEW] | 将 typed event 渲染成统一 `Message{Title,Body,Level}` |

> **命名说明**：使用 `MessageNotifier` 而非 `Notifier`，以避免与 `contract/mcp_control.go` 中已有的 `ToolNotifier`（MCP 工具变更通知）混淆。

## 接口设计

```go
// internal/contract/message_notifier.go

// MessageNotifier 是外部消息推送的统一抽象。
// 与 ToolNotifier、PushBridge、eventsurface 都语义正交。
type MessageNotifier interface {
    Enqueue(ctx context.Context, req NotifyRequest) error
}

type NotifyRequest struct {
    ChannelAlias string // 逻辑别名，如 "slack.default"
    EventID      string
    DedupeKey    string
    Source       string // "core.turn" | "cron.job" | "orch.dag" ...
    Message      Message
}

type Message struct {
    Title   string
    Body    string // Markdown 格式
    Level   string // "info" | "success" | "error"
}
```

## 配置方案

当前仓库建议先通过环境变量接入：

```bash
NOTIFY_DEFAULT_CHANNEL="slack.default"
NOTIFY_CHANNELS_JSON='{
  "slack.default": {"platform":"slack","url":"https://hooks.slack.com/services/..."},
  "dingtalk.ops": {"platform":"dingtalk","url":"https://oapi.dingtalk.com/robot/send?access_token=...","secret":"SEC..."}
}'
```

> 若后续确实需要文件配置，也应先把结构并入 `internal/platform/config.Config`，而不是为通知功能单独引入另一套配置读取器。

> 推荐运行时约定：业务层与 Cron 任务只传 channel alias（例如 `slack.default`），由内部 resolver 解析成 URL/secret。不要把 webhook 密钥散落到任务表、业务 payload、RPC input 或 dashboard 输出。

## 三个通知面边界

- `ToolNotifier`：MCP control-plane peer fanout。
- `PushBridge` / `eventsurface`：UI / JSON-RPC push。
- `MessageNotifier`：外部 webhook egress。

三者的发送对象、payload、timeout、失败处理、配置来源都不同，不能互相复用。

## 目标关联规则

- Cron job 的 `notify_channel` 不能指望从 `TurnCompleted` 里反推。更稳妥的做法是 Cron scheduler 在 job 完成路径直接 enqueue，或发布自带 `job_id/notify_channel` 的 cron 事件。
- DAG/orchestration 侧同理；若要支持 task 通知，必须在 `mcp-orch` 侧增加带 alias 的业务事件或回查 metadata，不能假设 `TaskWakeupCompleted` DTO 天然带通知目标。

## 触发源建议

- `turndto.TurnCompleted`：成功/失败结果推送
- `turndto.TurnInterrupted` / `turndto.TurnStalled`：异常中断提醒
- `agentdto.AgentFailed`：provider/session 级终态错误
- `agentdto.AgentError` / `AgentWarning`：仅作为诊断类可选通知
- task/DAG 事件仅在生产侧真正有 publish 点后再纳入；当前不能把 `TaskWakeupCompleted` 写成现成可用触发源

## 发送策略

- bus 回调只负责非阻塞 enqueue，不在订阅回调里同步发 HTTP。
- `bus.ResilientSubscribe(...)` 回调本身是同步执行；任何入队操作也必须 bounded + 快速失败，不能在回调里阻塞等待。
- worker 内部才执行真实发送，使用短超时、有限重试、rate limit、drop/coalesce 策略；失败只记日志/指标，不反向阻塞业务链路。
- shutdown 时由 `fx.Lifecycle` 统一 cancel 订阅并在限定时间内 drain worker。
- 若来自 Cron/P1b，建议只传 `notify_channel` 别名，不直接传 URL/secret。

## 各平台消息模板

### 钉钉 Markdown 卡片
```json
{
  "msgtype": "markdown",
  "markdown": {
    "title": "{{.Title}}",
    "text": "### {{.Title}}\n\n{{.Body}}\n\n---\n*Super Agent · {{.Timestamp}}*"
  }
}
```

### Slack Block
```json
{
  "blocks": [
    { "type": "header", "text": { "type": "plain_text", "text": "{{.Title}}" } },
    { "type": "section", "text": { "type": "mrkdwn", "text": "{{.Body}}" } }
  ]
}
```

### 飞书 Rich Text
```json
{
  "msg_type": "interactive",
  "card": {
    "header": { "title": { "tag": "plain_text", "content": "{{.Title}}" } },
    "elements": [
      { "tag": "markdown", "content": "{{.Body}}" }
    ]
  }
}
```

## 关键实现约束

- 不要把外部通知实现成 `turn/service.go` 里的同步 hook。
- 不要与 `ToolNotifier` 混用；二者的发送对象、超时语义、失败处理完全不同。
- 也不要复用 `PushBridge` / `eventsurface` 作为外部 webhook 通道。
- 不要在数据库、任务定义或 dashboard payload 中持久化原始 webhook URL/secret，优先持久化逻辑通道名。
- 只允许 `https` webhook；默认拒绝 localhost、loopback、private CIDR、非 http(s) scheme，redirect 后也要重新校验。
- 所有日志与错误必须 redact URL query/token/secret；消息 body 要做长度上限、Markdown escaping、mention 抑制，默认不发送 raw provider payload、完整 transcript 或工具结果全文。
- 配置、重试、速率限制必须集中在 `module/notify + platform/notify`，避免业务模块各自 `http.Post(...)`。
- timeout 与重试常量应挂到统一配置/timeout helper，不要在业务模块里随手 `context.WithTimeout(...)`。

**Hermes 源码对照点**:
- `tools/send_message_tool.py:1-50` — 包含 65KB 的发消息冗余逻辑
- `tools/discord_tool.py` — Discord 等独立适配文件
- `gateway/` 目录 — 复杂的网关协议
