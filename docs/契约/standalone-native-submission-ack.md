# Standalone 原生提交确认契约

## 范围与非声明

本契约约束独立 `mcp-orch` 的 CLI 原生确认，不实现或批准 Draft OUT 的 recovery-custody、effect permit、跨 daemon incarnation 恢复协议。原生 `submitted` 只表示 provider CLI 确认接收本次输入；不是模型完成、权限授予、账单隔离或 exactly-once 承诺。

## 真实来源

- Codex：同一固定执行 session 的 `turn/start` RPC 成功响应及非空 `turn.id`。
- Claude：以 `--replay-user-messages` 启动；发送前登记单个有界确认槽，只有根 `user` 回显明确包含 `isReplay=true`，且 session ID 与本次输入 UUID 精确匹配才确认。本地生成 UUID、stdin 写成功、`system/init`、模型正文或 root `result` 均不能替代该确认。
- 等待受请求 context 和 CLI 进程退出约束；取消或退出而未获确认返回 unknown，不自动重发。已记录确认可以先于随后的取消生效。

`contract.ProviderSubmissionReceipt` 字段链：

| 字段 | 生产事实 | 消费边界 |
|---|---|---|
| Provider | 固定 session 的配置 provider | 执行器身份检查、service provider 围栏、持久事件 |
| AgentID | 提交定位的受管 session | service agent 围栏、消息 receiver、持久事件 |
| ThreadID | 提交前固定的 provider session | 请求 thread 检查、service thread 围栏、持久事件 |
| TurnID | Codex 响应 ID / Claude 被实际回显确认的输入 UUID | service active-turn 绑定、message submission_id、持久事件 |
| ReceiptID | 本协议确认引用，与上述原生 ID 相同 | 完整持久确认、非空与非 message ID 检查 |
| LaunchSeq | 固定 CLI session 的非零本地启动代际 | service 代际围栏、持久事件；不冒充跨 daemon generation |

字段通过整值透传，禁止从 agent 最新快照补造另一 turn 的确认。执行器固定 session 指针并验证仍同一映射；service 复用已有 pending turn/提前终态缓冲和提交所有权检查。没有原生确认能力时不能把旧接口的 nil error 转换为确认。

## 消息持久化与重放

1. 原 grant、trusted caller scope 和工作区授权保持不变；不增加 operator 伪造 sender 的旁路。
2. 消息先持久化 `accepted`，仅首次 Fresh 结果可进入一次提交。
3. `RecordNativeReceipt` 必须持有当前 Store 的 owner capability；验证确认、receiver 和不同于 message ID 的原生身份。
4. 在一个 SQLite 事务内更新 `submitted`、写入带完整 `provider_receipt` 的既有 `message.submitted` 事件、写入原 outbox。任何失败整体回滚。
5. `submitted` 重放按原 message ID 精确读取唯一事件，验证外层事件、完整消息和确认字段。旧状态字符串、缺字段或损坏记录必须报 unknown；不能只读 status 就认定有确认。
6. `accepted` 重放不再次调用 provider。确认后本地事务失败或 crash window 可以永久保持 unknown；本契约不保证能对所有不确定状态进行自动恢复对账。

## 同一 daemon 内的 CLI 异常退出与恢复

这里的恢复仅重建受管 Codex 执行进程、原生恢复同一 provider thread，不恢复或重放旧输入的外部副作用，也不是跨 daemon 的 OUT 恢复协议。

1. 仅接受进程管理边界提供的真实退出，agent、provider session 和启动代际必须匹配；显式 stop、旧代际及重复退出不得重新启动 agent。现有自动恢复预算为两分钟内最多三次，不通过无限循环消除失败。
2. 先通过既有 terminal owner 持久提交 `process_failed`，再清理运行句柄、投影终态、恢复同一 thread 并以 CAS 推进新执行代际。落库失败即阻断恢复，不清除旧身份、不伪造持久成功；当前不保证存储恢复后自动重新投递一次性退出事件。
3. 已持久完成的成功、失败或中止 turn 均保持原历史不变；进程退出使用独立 session 生命周期 head。进程失败可以产生新公开报告及序号，不得把“报告序号增加”解释成模型重新执行。canonical projector 的报告缓存镜像持久 head 的序号、正文与发生时间；恢复生成新的 active head 本身不生成新报告，也不得切回独立计数造成报告序号倒退。
4. `turn/start` 尚未返回时，本地 provisional ID 不是 provider 确认。退出暂存在已有单槽中，由原提交 owner 在 RPC 成功或失败收束、释放锁后按原 agent/thread/launchSeq 消费；旧提交收束不得消费新代际的槽。若有早到模型终态，service 必须先严格持久化并由原执行器完成保留回执的精确重交付，再处理退出；不能仅调用 void sink 或先处理 process_failed。此过程中不生成伪造 receipt，不自动重发输入。
5. Managed CLI 恢复前必须已经结束原 active turn；未终结时手动恢复明确拒绝，不能先改变状态、启动新会话或重放 DAG 输入。未知提交结果仍保持 unknown，进程恢复不把它提升为 submitted 或模型成功。
6. Claude 的持久 thread resume 仍明确不支持；不因 CLI 异常退出而借用本地 command 或创建新会话冒充恢复。
7. 尚未被协议消费者处理并持久化的最后一帧，不构成已确认模型终态。进程退出与该帧的竞争不承诺恢复未持久结果；已落库的模型终态则不可被进程失败覆写。

## 验证边界

执行器测试覆盖早到/错身份确认、写入未确认、取消、退出、代际及字段映射；Store 测试覆盖完整 DTO roundtrip、重新构造 Store 读取、错误 receiver/owner、旧记录和事务故障；工具测试覆盖成功重放与 unknown 不重发。真实 provider 测试必须单列进程内 Fx 与完整 daemon binary 的证据，不以测试名称或自然语言自述替代实际结果。
