# P2: 多平台通知

## 目标

把系统从纯 Request / Response 交互扩展到可控的外部主动通知：针对长任务、Cron、DAG / orchestration 等终态，把结果或中断提醒发往钉钉、飞书、Slack 等 webhook / bot 通道。

## 现状校准

- 当前仓库已经有 `contract.ToolNotifier`，但它是 MCP control-plane 的 peer fanout，不适合复用为外部 webhook egress。
- 当前还存在 `rpc.PushBridge` / `eventsurface` 这一套 UI JSON-RPC push 面，它也不是外部消息发送器。
- `turn/service.go` 没有 event-bus 依赖，也不适合继续堆完成后发通知这类 side effect。
- 当前 core app 与 `cmd/mcp-orch` 是两套 Fx / Bus；如果只在 core 装 notify module，看不到 `mcp-orch` 自己的 DAG / orchestration 事件。
- `cmd/mcp-orch` 已经实际 import `internal/platform/*`；P2 不需要放宽 archtest，只要按平台库 + 业务 module 双层拆分即可。

## 推荐改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 抽象合约 | `internal/contract/message_notifier.go` [NEW] | 定义 alias-first 的外部通知接口，例如 `TryEnqueue(ctx, NotifyRequest)` |
| 平台库 | `internal/platform/notify/{webhook,resolver,render,dingtalk,feishu,slack}.go` [NEW] | 共享 webhook 客户端、resolver、模板渲染与平台签名逻辑，供 core / orch 两棵 Fx 树同时 import |
| core 业务 module | `internal/module/notify/module.go` [NEW] | Provide core notifier、注册 core turn / provider / cron subscriber，并把 flush worker 放进 `runner.actors` |
| orch 业务 module | `cmd/mcp-orch/notify/` 或等价位置 [NEW] | Provide orch notifier、注册 DAG / orchestration subscriber，并把 flush worker 放进 `runner.actors` |
| 配置承载 | `internal/platform/config/config.go` | 新增 `NotifyConfig`，集中表达 alias -> channel config、queue、timeout、retry、allowlist |
| 解析器 | `internal/platform/notify/resolver.go` [NEW] | 业务层只传 `channelAlias`；resolver 内部把 alias 解析成 `{Platform, URL, Secret}` |
| 接线 | `internal/app/modules.go`、`cmd/mcp-orch/fx.go` | core Fx 和 orch Fx 各装一套 `bus.subscribers` + `runner.actors` wiring |

## 模块与 wiring

- **平台库与业务 module 并存**：`internal/platform/notify/*` 是共享库层；`internal/module/notify` 与 `cmd/mcp-orch/notify` 是各自 Fx 树里的业务接线层。两者本来就并存，不存在平台库层与业务 module 二选一。
- **core Fx**：`fx.Provide` 只构造 notifier / resolver / queue / renderer 等对象；`fx.Invoke(RegisterSubscribers)` 把 core turn / provider / cron subscriber 注进 `bus.subscribers`；flush worker 实现 `Runner.Run(ctx)`，进入 `runner.actors`。
- **orch Fx**：`fx.Provide` 构造 orch 侧 notifier / queue；`fx.Invoke(RegisterSubscribers)` 把 DAG / orchestration subscriber 注进 `bus.subscribers`；flush worker 同样进入 `runner.actors`。
- **shutdown 流**：`ctx cancel → run.Group 全退 → bus 停派发 subscribers → fx.OnStop 只释放 http client / 关闭本地 intake channel`。

## 配置与 fail-fast 规则

```bash
NOTIFY_CHANNELS_JSON='{
  "slack.default": {"platform":"slack","url":"https://hooks.slack.com/services/..."},
  "dingtalk.ops": {"platform":"dingtalk","url":"https://oapi.dingtalk.com/robot/send?access_token=...","secret":"SEC..."},
  "feishu.ops": {"platform":"feishu","url":"https://open.feishu.cn/open-apis/bot/v2/hook/...","secret":"xxx"}
}'
NOTIFY_DEFAULT_CHANNEL='slack.default' # 仅显式 opt-in 场景允许读取
```

- `NOTIFY_CHANNELS_JSON` 必须 fail fast：malformed JSON、duplicate alias、unsupported platform、missing required fields 都应在启动期报错，不能 silent 降级。
- 业务层与 Cron 任务只传 channel alias，由 resolver 内部拿到 URL / secret；不要把 webhook 密钥散落到任务表、业务 payload、RPC input 或 dashboard 输出。
- `NOTIFY_DEFAULT_CHANNEL` 只能在显式 opt-in 场景下读取，**禁止**进入默认优先级链。

## 平台 Auth / Signing

- **钉钉**：使用 HMAC-SHA256 生成 sign query param；secret 只由 resolver 提供，不能落日志。
- **飞书**：按 `timestamp + secret` 做 HMAC-SHA256 并写入 body；timestamp、nonce、secret 与签名基串都要统一脱敏。
- **Slack**：Webhook URL 本身就是 bearer secret，必须整体视为 secret 做 redact；签名不是必须项，但 URL / token 仍统一经 resolver 管理。
- 日志脱敏范围必须覆盖 URL query、signature base string、nonce、timestamp 组合与所有 secret 派生值。

## 三个通知面边界

- `ToolNotifier`：MCP control-plane peer fanout。`internal/platform/mcpcontrol/registry.go:15-19` 当前默认 `notify timeout = 2s`、`peer failure threshold = 3`。
- `PushBridge`：沿 jrpc2 `Notify/Callback` 的 UI push 面；`internal/platform/rpc/push.go:30-52` 直接走调用方 `ctx`，**没有独立 retry / timeout 策略**。
- `MessageNotifier`：新的外部 webhook egress；采用短超时、有限重试、rate limit、drop / coalesce，并与前两者分开建模。

## 目标关联规则

- Cron job 的 `notify_channel` 直接来自 job row；不要从 `TurnCompleted` 里反推。
- DAG / orchestration 的 alias 固定为：`node.config.notify_channel > dag.metadata.notify_channel > drop/error`。缺失时直接丢弃或报错，**不**走全局默认链。
- `NOTIFY_DEFAULT_CHANNEL` 只允许被显式 opt-in 的调用方读取；它不是 DAG / turn / cron 的隐式兜底来源。
- core turn / provider 事件同样不能天然反推出通知目标；若 runtime config / 请求上下文里没有明确 alias，就应 `drop/error`，而不是对所有 turn 自动外发。

## 触发源建议

- `turndto.TurnCompleted`：只适合作为终态触发源；需要正文时还要结合 `ItemCompleted(agentMessage/final_answer)` 或 summary。
- `turndto.TurnInterrupted` / `turndto.TurnStalled`：异常中断提醒。
- `agentdto.AgentFailed` / `AgentWarning`：诊断类可选通知。
- DAG / orchestration 事件：由 `cmd/mcp-orch` 侧实际发布并消费，不要求桥接回 core bus。
- 解析目标时必须先走 `agent_id`，再回退 `thread_id`：`internal/module/thread/lifecycle.go:393-400` 当前会同时记住 `providerThreadID` / `codexThreadID` / `agentID`，而 `internal/module/thread/events.go:176-185` 也是先按 `agent_id` 查 binding，再回退 `thread_id`。

## 发送策略

- bus 回调只负责非阻塞 `TryEnqueue`；回调内只做 state merge / enqueue，**禁同步 HTTP 写**。
- `bus.ResilientSubscribe(...)` 只包 recover；底层 `kelindar/event` 是 per-subscriber queue，队列饱和会反压 publish path，因此 `TryEnqueue` 也必须 bounded + 快速失败。
- 真正的 HTTP 发送只在 worker 内执行：短超时、有限重试、rate limit、drop / coalesce；失败只记日志 / 指标，不反向阻塞业务链路。
- worker 通过 `Runner.Run(ctx)` 进入 `runner.actors`；shutdown 只依赖 `ctx cancel + bus 停派发`，不在 `fx` 里额外写 cancel worker 逻辑。

## 必测项

- invalid alias / malformed `NOTIFY_CHANNELS_JSON` / duplicate alias / unsupported platform 都必须启动即失败。
- alias 优先级：`node.config.notify_channel > dag.metadata.notify_channel > drop/error`，且 `NOTIFY_DEFAULT_CHANNEL` 不得偷偷介入。
- queue full / closed intake / bounded `TryEnqueue` 行为，以及 dual-Fx wiring smoke。
- 钉钉 / 飞书 / Slack 的签名与脱敏规则。
- SSRF：redirect-to-loopback、private CIDR、DNS rebinding、dial IP 校验、日志脱敏。
