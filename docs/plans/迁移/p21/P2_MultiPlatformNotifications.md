# P2: 多平台通知

## 目标

把系统从纯 Request / Response 交互扩展到可控的外部主动通知：针对长任务、Cron、DAG / orchestration 等终态，把结果或中断提醒发往钉钉、飞书、Slack 等 webhook / bot 通道。

## 现状校准

- 当前仓库已经有 `contract.ToolNotifier`，但它是 MCP control-plane 的 peer fanout，不适合复用为外部 webhook egress。
- 当前还存在 `rpc.PushBridge` / `eventsurface` 这一套 UI JSON-RPC push 面，它也不是外部消息发送器。
- `turn/service.go` 没有 event-bus 依赖，也不适合继续堆完成后发通知这类 side effect。
- 当前 core app 与 `cmd/mcp-orch` 是两套 Fx / Bus；如果只在 core 装 notify module，看不到 `mcp-orch` 自己的 DAG / orchestration 事件。
- **core terminal turn 在 orch 侧的真实入口是 hook consumer**，不是跨 Fx 桥接的 core bus：`cmd/mcp-orch/runtime.go:216-219` 的 `subscribeOrchestrationHooks` → `cmd/mcp-orch/hook_subscription.go:13-40` 订 `agent.turn.after / failed / progress` → `internal/sidecar/orch/orchestration/hook_consumer.go:96-220` 处理 `TurnCompleted` 与 `ItemCompleted(final_answer)`。因此 orch 侧 notify 对 **core terminal turn** 的观察 tap 必须接在 hook consumer 处理链上；而 orch 自己产生的 DAG / task / wakeup 事件仍走 orch 本地 `bus.subscribers`，两者不能互相替代。
- **archtest 白名单事实**：`internal/archtest/dependency_direction_mcp_orch_test.go:23-29` **枚举** 放行 `internal/platform/{config,db,bus,runner,rpc,runtimesafe,shared,statemachine,eventsurface,rlimit}` 十个子包，不是 `internal/platform/*` 前缀通配。因此若新建 `internal/platform/notify` 包会撞护栏。

## 推荐改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 抽象合约 | `internal/contract/message_notifier.go` [NEW] | 定义 alias-first 的外部通知接口，例如 `TryEnqueue(ctx, NotifyRequest)` |
| 共享库位置（已拍板） | `internal/module/notify/platform/{webhook,resolver,render,dingtalk,feishu,slack}.go` [NEW]，package doc 标注 *cross-tree shared*；避免与 module 内部 helper 的 `shared` 命名混淆 | 共享 webhook 客户端 / resolver / 模板 / 平台签名；放在 `internal/module` 子包会被 `internal/archtest/dependency_direction_mcp_orch_test.go` 的白名单放行，orch 侧 import 不撞护栏。**禁止** 新建 `internal/platform/notify` 顶层包（那会触发 archtest 护栏改动，本期不采）。 |
| core 业务 module | `internal/module/notify/module.go` [NEW] | Provide core notifier、注册 core turn / provider / cron subscriber，并把 flush worker 放进 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） |
| orch 业务 module | `internal/sidecar/orch/notify/` 或等价位置 [NEW] | Provide orch notifier；`fx.Invoke(RegisterSubscribers)` 负责 orch 本地 DAG / task / wakeup 订阅器，`fx.Invoke(RegisterHookConsumerTaps)` 只负责 core terminal turn 的 hook tap；flush worker 进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） |
| 配置承载 | `internal/platform/config/config.go` | 新增 `NotifyConfig`，集中表达 alias -> channel config、queue、timeout、retry、allowlist（`platform/config` 已在 archtest 白名单内，配置加在这里不需动护栏） |
| 解析器 | `internal/module/notify/platform/resolver.go` [NEW] | 业务层只传 `channelAlias`；resolver 内部把 alias 解析成 `{Platform, URL, Secret}` |
| 接线 | `internal/app/modules.go`、`cmd/mcp-orch/fx.go` | core Fx 和 orch Fx 各装一套 `bus.subscribers` + `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） wiring；平台共享库位置与业务 module 落点可并存，按双树同构接线即可 |

> `[...] [NEW]` 表示目标新增路径，当前仓库尚不存在；它们是实施落点，不是“现状已存在文件”的事实锚点。

## 模块与 wiring（双树同构）

- **双树同构原则**：core Fx 与 `cmd/mcp-orch` Fx 各自有一棹 bus / run.Group；两树同时 import 同一套共享库（放在 `internal/module/notify/platform/*` 以命中 archtest 白名单，见上文“archtest 白名单事实”），后在各自业务 module 里装一套 subscriber + runner。两树同构不要求“只留 core 或只留共享库”这类互斥裁剪，且互不用跨树订阅来盖对方事件。
- **core Fx**：`fx.Provide` 只构造 notifier / resolver / queue / renderer 等对象；`fx.Invoke(RegisterSubscribers)` 把 core turn / provider / cron subscriber 注进 `bus.subscribers`；flush worker 实现 `Runner.Run(ctx)`，进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）。
- **orch Fx**：`fx.Provide` 构造 orch 侧 notifier / queue；`fx.Invoke(RegisterSubscribers)` 负责 orch 本地 DAG / task / wakeup 订阅器，`fx.Invoke(RegisterHookConsumerTaps)` 只把 core terminal turn 的观察 tap 接到 hook consumer 处理链上（而不是指望 orch 本地 bus 会重发 core terminal turn）；flush worker 同样进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）。
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

- `NOTIFY_CHANNELS_JSON` 必须 fail fast：malformed JSON、duplicate alias、unsupported platform、missing required fields 都应在启动期报错，不能 silent 降级；实现上必须使用 duplicate-key-aware 解析，不能让 `encoding/json` 默认“后值覆盖前值”悄悄吞掉重复 alias。
- **推荐实现**：用 `json.NewDecoder(r)` 流式解析 + 对顶层对象扫描 token；或先 `Unmarshal` 到 `map[string]json.RawMessage`，同时用 `json.Decoder.Token()` 循环记录原始 key 出现次数，命中重复直接 `ErrDuplicateAlias`。禁止直接 `json.Unmarshal` 成强类型 struct 就过关。
- 业务层与 Cron 任务只传 channel alias，由 resolver 内部拿到 URL / secret；不要把 webhook 密钥散落到任务表、业务 payload、RPC input 或 dashboard 输出。
- `NOTIFY_DEFAULT_CHANNEL` 只能在显式 opt-in 场景下读取，**禁止**进入默认优先级链。

## 平台 Auth / Signing

- **钉钉**：使用 HMAC-SHA256 生成 sign query param；secret 只由 resolver 提供，不能落日志。
- **飞书**：按 `timestamp + secret` 做 HMAC-SHA256 并写入 body；timestamp、nonce、secret 与签名基串都要统一脱敏。
- **Slack**：Webhook URL 本身就是 bearer secret，必须整体视为 secret 做 redact；签名不是必须项，但 URL / token 仍统一经 resolver 管理。
- 日志脱敏范围必须覆盖 URL query、signature base string、nonce、timestamp 组合与所有 secret 派生值。

## SSRF / Secret / 内容洁化（必始终要硬规则）

- **仅 `https` scheme**：HTTP POST 前强制校验；`http://` / `file://` / `ftp://` / 空 scheme 均启动时即跟 webhook 配置一起 fail fast。
- **默认拒绝内网 / 特殊地址**：loopback (`127.0.0.0/8` / `::1`)、link-local (`169.254.0.0/16` / `fe80::/10`)、ULA (`fc00::/7`)、multicast (`224.0.0.0/4` / `ff00::/8`)、private CIDR (`10/8` / `172.16/12` / `192.168/16`) 一律拒绝；要求内网发送需显式 `NOTIFY_ALLOW_PRIVATE_CIDR=1` 的 opt-in。
- **DNS rebinding 防御**：必须基于 DNS 解析结果与实际 dial IP 连带校验（而不是仅 hostname 字符串匹配）；HTTP 3xx redirect 后必须对新 URL 重新执行上述全套校验，不能复用首次校验结果；HTTP client 必须显式禁用环境代理 / 自定义代理（`HTTP_PROXY` / `HTTPS_PROXY` / `ProxyFromEnvironment` 都不允许介入 webhook egress）。
- **推荐实现**：`http.Transport{Proxy: nil, DialContext: ssrfGuardedDialer}`；`ssrfGuardedDialer` 在 `net.Dialer.DialContext` 返回 `net.Conn` 后立刻 `conn.RemoteAddr()` 拿真实 IP 做 allowlist 校验，失败则 `_ = conn.Close()` + 返回 `ErrDisallowedAddress`。`redirect.Func` 必须对每一跳重新跑 scheme / host / DNS / IP 校验链，**禁止**共享首次校验结果。
- **内容脱敏 & 洁化**：
  - 所有日志 / 错误 / 指标标签必须 redact：URL query / fragment / `Authorization` header / bearer / webhook token / signing secret / nonce / timestamp；**禁止**记录 webhook provider 原始响应 body / headers，必要时只记状态码与脱敏后的摘要。
  - Message body 必须 Markdown escape（尤其 `>`/`` ` ``/`*`/`[`/`]`/`\`）；禁止自动 `@all` / `@channel` / `@here` / `张三` 类 mention——需 mention 时必须由模板显式声明，且模板内不能嵌入用户输入的 mention。平台特有 mention 语法也要一并抑制，例如 Slack `<!channel>` / `<!here>` / `<@U123>`、飞书 mention tag / id、钉钉 `@手机号` / `@all`。
  - Body 套用长度上限（默认 4 KiB，default 可配）；大于上限则略并附“…truncated … N bytes”。
  - **禁发 raw provider payload**：绝对不能将 raw provider event / 完整 transcript / 完整 tool result 直接传入模板；渲染层只能读规范化过的 `Message{Title,Body,Level}`。
- **模板渲染**：统一使用 `text/template` 的 `htmlSafe` 封装实现 + 单元级别的 MarkdownEscaper；不为某个平台写 raw `fmt.Sprintf(...)` 流拼成 JSON。

## 平台消息模板（实施可直接裁剪）

### 钉钉 Markdown 卡片
```json
{
  "msgtype": "markdown",
  "markdown": {
    "title": "{{.Title | markdownEscape}}",
    "text": "### {{.Title | markdownEscape}}\n\n{{.Body | markdownEscape}}\n\n---\n*Super Agent · {{.Timestamp}}*"
  }
}
```

### 飞书 Rich Text
```json
{
  "msg_type": "interactive",
  "card": {
    "header": { "title": { "tag": "plain_text", "content": "{{.Title | markdownEscape}}" } },
    "elements": [
      { "tag": "markdown", "content": "{{.Body | markdownEscape}}" }
    ]
  }
}
```

### Slack Block
```json
{
  "blocks": [
    { "type": "header", "text": { "type": "plain_text", "text": "{{.Title | markdownEscape}}" } },
    { "type": "section", "text": { "type": "mrkdwn", "text": "{{.Body | markdownEscape}}" } }
  ]
}
```

> 三个模板实现时一律在 `internal/module/notify/platform/render.go` 走统一 `markdownEscape` / 长度截断 / mention 抓找。禁止在每平台实现里再写一遍。

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
- DAG / orchestration 事件：由 `cmd/mcp-orch` 侧实际发布并消费，不要求桥接回 core bus。**core terminal turn 在 orch 侧的入口仅有 hook consumer**（`cmd/mcp-orch/runtime.go:216-219` → `hook_subscription.go:13-40` → `orchestration/hook_consumer.go:96-220`），orch notify 订阅器必须在这条链上留 tap。
- 解析目标时必须先走 `agent_id`，再回退 `thread_id`：`internal/module/thread/lifecycle.go:393-400` 当前会同时记住 `providerThreadID` / `codexThreadID` / `agentID`，而 `internal/module/thread/events.go:176-185` 也是先按 `agent_id` 查 binding，再回退 `thread_id`。

## 事件到消息的映射表

`NotifyRequest.Message{Title, Body, Level}` 的填充策略必须标准化，**禁止**把 raw provider payload 直接喂给模板：

| 触发源 | Title | Body | Level | 备注 |
|---|---|---|---|---|
| `turndto.TurnCompleted(Success=true)` | `"Turn 完成: " + agent.name` | `FirstTrimmed(ev.Result, ev.Summary, ev.Message)`，截断 4 KiB | info | Success 为非指针 bool，未显式 true 时禁止当成功处理 |
| `turndto.TurnCompleted(Success=false)` | `"Turn 失败: " + agent.name` | `ev.StopReason + "\n" + ev.Error`（脱敏后） | warn | |
| `turndto.TurnInterrupted` / `TurnStalled` | `"Turn 中断"` / `"Turn 停滞"` | `ev.Reason` | warn | terminal precedence：不得被后到 `completed` 覆盖 |
| `agentdto.AgentFailed` | `"Agent 失败: " + agent.name` | `ev.Error`（脱敏后） | error | |
| Cron job 完成 | `job.name` | 从 `active_turn_id` 归因 observation 层的 `final_answer` 或 `result` | 按 turn success 映射 | `notify_channel` 来自 job row，不能从 turn 反推 |
| DAG node 终态 | `node.name` | 按 `node.config.notify_template` 渲染；缺 template → 丢弃 | 按 node status 映射 | alias 来源 `node.config.notify_channel > dag.metadata.notify_channel > drop/error` |

所有 Body 在落入模板前都走 `markdownEscape` + mention 抑制 + 长度截断；模板层只能读规范化过的 `Message` struct，不得直接消费 provider 事件字段。

## 发送策略

- bus 回调只负责非阻塞 `TryEnqueue`；回调内只做 state merge / enqueue，**禁同步 HTTP 写**。
- `bus.ResilientSubscribe(...)` 只包 recover；底层 `kelindar/event` 是 per-subscriber queue，队列饱和会反压 publish path，因此 `TryEnqueue` 也必须 bounded + 快速失败。
- 真正的 HTTP 发送只在 worker 内执行：短超时、有限重试、rate limit、drop / coalesce；失败只记日志 / 指标，不反向阻塞业务链路。
- worker 通过 `Runner.Run(ctx)` 进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）；shutdown 只依赖 `ctx cancel + bus 停派发`，不在 `fx` 里额外写 cancel worker 逻辑。v1 采用 **bounded drain 5s**：超时仍未发出的 webhook 直接 drop，并记指标 `notify_drop_total{reason="shutdown_drain"}`（以及 `reason="queue_full"` / `reason="rate_limit"` / `reason="ssrf_blocked"`）；不承诺 durable retry/WAL。drop 指标不能省——没有它故障时无法归因。

## 必测项

- invalid alias / malformed `NOTIFY_CHANNELS_JSON` / duplicate alias / unsupported platform / missing required fields 都必须启动即失败。
- alias 优先级：`node.config.notify_channel > dag.metadata.notify_channel > drop/error`，且 `NOTIFY_DEFAULT_CHANNEL` 不得偷偷介入。
- queue full / closed intake / bounded `TryEnqueue` 行为，以及 dual-Fx wiring smoke。
- 钉钉 / 飞书 / Slack 的签名与脱敏规则。
- golden payload：钉钉 Markdown / 飞书 Rich Text / Slack Block 各维护一份 golden fixture，验证统一 `markdownEscape` / mention 抑制 / 长度截断。
- SSRF：redirect-to-loopback、private CIDR、DNS rebinding、dial IP 校验、禁用 env proxy、日志脱敏。
- duplicate-key JSON：构造包含重复 alias 的 `NOTIFY_CHANNELS_JSON` fixture，断言启动期报错（不是“后值覆盖前值”静默通过）。
- 事件映射表：对每一行触发源至少一份 golden test，断言 `NotifyRequest.Message` 字段填充正确且不含 raw payload 原文。
- drain 指标：shutdown 时队列有未发送项，断言 `notify_drop_total{reason="shutdown_drain"}` 递增。
- 共享库命名：archtest 验证 `internal/module/notify/platform/*` 被 core 与 `cmd/mcp-orch` 两树 import 均不触发 `dependency_direction_mcp_orch_test.go` 违规。
