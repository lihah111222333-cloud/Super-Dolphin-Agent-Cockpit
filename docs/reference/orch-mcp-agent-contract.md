# orch MCP agent 调用契约

本页描述核心 agent 工具的公开调用方式。事实来源为 `cmd/mcp-orch/tools/orchestration_tool_definitions.go`、`agent_input_contract.go` 与 `agent_output_schema.go`；stdio、HTTP 和 bootstrap 复用同一 Registry。

## 常用闭环

1. 发现并启动：先用 `list_models({"provider":"codex"})` 从可用 provider 的 `data[].models` 选择模型；standalone 模式必须显式传入。调用 `launch_agent({"name":"review","prompt":"检查当前 diff 并报告问题","cwd":"/absolute/project","model":"<已发现模型>","read_only":true})`。保存返回的 `agent_id`。`success:true` 和 `status:launching` 表示接受启动，不表示任务完成。
2. 首次等报告：`get_agent_report({"agent_id":"<返回的ID>","wait":true,"after_report_seq":0})`。保存返回的 `report_seq`。再次等待必须传上次观察到的序号。
3. 追加工作：对 idle agent 使用 `send_message({"agent_id":"<返回的ID>","message":"补充验证证据","wait_report":true})`。该操作先记录旧报告序号，提交新 turn，再等待新报告。
4. 查看状态：`list_agents({})` 总是返回对象 `{agents,data,total,showing,truncated,hint}`；`data` 是 `agents` 的兼容别名。`total` 为过滤后的总数，`showing` 为本次返回数量，`truncated` 仅在存在未返回项时为 true。
5. 结束当前 turn 用 `interrupt_agent`；中断后后台命令可能继续运行。需要结束 agent 并清理资源时用 `stop_agent`。支持归档的 provider 会归档线程，结果中的 `archived` 是实际归档状态。`wait:true` 等待停止状态收口。对 stopped/failed 运行态调用 `recover_agent`；支持原生恢复的 provider 可以取消归档并继续原线程，检查返回的 thread_id/state 后再追加工作。

普通 agent 调用统一优先使用持久 `agent_id`，不可用显示名或 `thread_id` 代替。已有的 `pos:"agent:<agent_id>"` 定位器仍可使用；与 `agent_id` 同时出现时必须一致。

## 参数约束

- `launch_agent` 的 tools/list schema 提供用途分组和普通、focused 只读、父任务 fork、精确模板四个示例。standalone 额外强制显式 `model`，缺失时在创建 agent 状态前拒绝；peer 模式保留 provider 配置解析。省略 `cwd` 仅允许通过真实存在的 `parent_id` 继承目录，父任务验证由 service 执行。
- `minimal` 不接受独立 `context`；`focused` 必须提供非空 `context`；`forked` 必须提供 `parent_id`，不接受独立 `context`。
- 带 `parent_id` 的子任务使用 Codex。三个高级身份字段 `codex_home/codex_instance_key/codex_model_provider` 必须整组提供。
- 运行配置和模板字段属于高级设置；不需要覆盖时省略。合法条件在 tools/list 的 inputSchema 中公开，并在 Registry 执行前验证。
- 单报告 `wait:true` 必须提供非负 `after_report_seq`；批量等待必须为所有目标提供 `after_report_seq_by_agent`，不接受多余目标。
- `list_agents` 不接受 `envelope:false`、负 limit 或未知状态。省略 envelope 即使用对象契约。

## 副作用与失败恢复

| 调用或结果 | 含义与后续动作 |
|---|---|
| launch 返回 existing | 复用已有 agent，本次 prompt 不会再次提交；追加任务使用 send_message |
| send_message 普通成功 | 已提交 turn，仍需读取新报告；该操作没有幂等键 |
| submission_outcome_unknown / delivery_outcome_unknown | 无法确认提交是否被接收；查询原任务与回执，禁止盲目重发 |
| report_wait_incomplete | turn 已提交，报告等待未完成；按照错误 hint 的 agent_id/after_report_seq 继续 get_agent_report，禁止再次发送同一任务 |
| 等待超时 | 结束这次等待，不会自动停止 agent |
| 终态无新报告 | 返回错误或批量条目的 ok:false，不合成成功报告 |
| send_direct_peer | 持久化消息并立即向接收者提交 turn；accepted 只证明持久意图，submitted 需要权威 provider 回执，不代表接收者任务完成 |

直接消息使用同一个 `idempotency_key` 标识同一消息。发生结果未知时先对账，不得换 key 重发。批量报告返回后同时检查整体 `status` 和每项 `ok/error`。

## 结构化恢复

消息错误的 `structuredContent.meta.submission_recovery` 提供实际阶段、提交状态、agent_id、thread_id、可选 previous_report_seq，以及 `recovery.tool/arguments`。只读取文本结果的客户端也可从 Hint 取得由同一对象生成的查询 JSON。

- 普通提交结果未知：只读 `get_agent_report(wait=false)`，没有基线时不编造游标。
- 带基线的提交结果未知：使用已观察的游标等待报告，但继续保持 submission_status=unknown。
- 已提交、报告等待失败：submission_status=submitted，按真实游标继续等待。

恢复指令均不会再次发送任务。报告不是该请求的原生确认，不提供跨重启幂等重发保证。

## 与旧调用方式的差异

原来不带游标的 `get_agent_report(wait:true)` 可能立即返回旧报告，现在明确拒绝。原来默认数组的 `list_agents` 改为对象；消费者应读取 `agents`。原来只在 handler 内可见的跨字段限制，现在由公开 schema 在副作用前强制执行。旧调用方必须按以上契约迁移，没有静默降级分支。

## 验证入口

常规工具测试覆盖游标、真实线程身份、重复启动、提交阶段错误、列表计数和输出字段守卫。`TestStandaloneAgentContract` 会从当前源码编译 orch 并通过真实 HTTP MCP 检查 tools/list、无效参数拒绝、列表 structuredContent 与 outputSchema 一致及退出清理。该契约黑盒场景不调用模型。另有 `TestStandaloneBinaryCodexLifecycleThroughMCP` 使用真实 Codex 低思考调用，验证随机 shell challenge、目录、报告游标、重复启动、fork 及同线程恢复。
