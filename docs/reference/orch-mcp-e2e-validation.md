# orch MCP 端到端验证

日期：2026-09-08。对象：基于 `07b5ee1414758b009807e37600bb61a98c7be412` 的未提交工作区。使用当前源码构建独立 `mcp-orch` binary；Codex CLI 0.153.3 使用 `gpt-6-astra`、低思考，Claude Code 2.1.237 显式使用 `haiku`。Codex provider home 和测试工作目录隔离；Claude 按既有 `host_default` 契约使用宿主已登录认证，认证文件不进入仓库或报告。

## 最新全量重跑

2026-09-08 本轮重新执行整个登记矩阵：**60/60 PASS、0 FAIL、0 SKIP，单次命令退出码 0，554.897 秒**。不是沿用此前分组记录。control 30 项用时 12.55 秒、clients 9 项 192.18 秒、lifecycle 9 项 146.96 秒、delivery 12 项 203.19 秒。原始日志 `/tmp/orch-full60-final-20260908.jsonl`，逐项结果见[验收结果](orch-message-delivery-results.json)。

另外使用最终真实用户配置 `orch.default_tools_approval_mode=approve`，由应用模型新建 `full-real-config-160052`，原生 thread `01a07fd2-7a74-78f1-bd9f-0f53cbab4858`。首次完成三轮真实模型交互、停止/同线程恢复、子 agent list_models/list_agents/get_agent_report 实际 MCP 调用；随机字符串跨轮一致，算术结果依次 803、1606、1611，最终 report_seq=6、idle。未发生审批错误、unknown 或 busy。原生工具记录和结果保存在 `.workspace/orch-visible-e2e/20260908-151715/full-real-config-160052/`。

与之并行的 orch/提示词回归 78 个测试包通过，direct-peer 全包 race 128.313 秒通过，代码守卫通过。项目地图首次发现生成物漂移，执行刷新后严格检查通过。矩阵故障场景使用隔离目录；真实用户配置专项与矩阵统计分开，避免将进程内测试当作 binary 测试。

## 此前独立真实二进制复跑

2026-09-08 对当前工作区的全部 19 个 `binary` 证据层场景执行单次真实 E2E：**19/19 PASS、0 FAIL、0 SKIP，命令退出码 0，235.879 秒**。每个场景通过 `go build` 构建并启动独立 orch 二进制，使用实际 HTTP/MCP；需要模型的场景调用真实 Codex/Claude。此结果独立于此前分组复验，逐项耗时与层级见[真实二进制结果](orch-real-binary-e2e-results.json)。原始日志：`/tmp/orch-real-binary-run.jsonl`。

包括 Codex provider 崩溃恢复（44.87 秒）、Codex 生命周期（72.69 秒）、消息超时/中断/停止恢复（61.19 秒）、Claude 完整生命周期（11.23 秒）、Claude 真实 MCP 工具调用（8.01 秒）及官方 Codex 客户端启动受管 agent（19.59 秒），其余场景验证协议、认证、单实例、隔离及启动边界。

范围边界：direct-peer 的二进制场景验证 operator 授权拒绝；此前 12 个 direct-peer 真实 provider 成功交付场景仍是进程内 Fx/Registry 链路，本次没有将其计入 binary E2E。

当前登记矩阵已扩展为 60 项；增量 control 复验及最新累计证据以[消息交付覆盖清单](orch-message-delivery-coverage.md)为准。

## 此前分组执行结果

此前阶段登记矩阵共 **56 个场景，按修复后的分组复验全部通过，0 skip、0 未验证**。真实交付由 3 个扩展为 12 个，另新增 8 个非模型契约/故障注入场景和 1 个原生记录解析守卫。逐分支对应关系及边界见[消息交付覆盖清单](orch-message-delivery-coverage.md)，逐项结果见[机器可读记录](orch-message-delivery-results.json)。

| 最终复验组 | 结果 | 耗时 |
|---|---:|---:|
| 控制与确定性故障 | 26/26 | 9.583 秒 |
| 客户端 | 9/9 | 191.72 秒 |
| 生命周期 | 9/9 | 167.44 秒 |
| 真实 provider 消息交付 | 12/12 | 190.37 秒 |

完整运行先遇到一项 outbox 测试时序错误；修复后 control 单独整体重跑通过，outbox 另重复 30 次通过。因此不将原完整运行的失败退出码改写为成功，详见文末复验口径。以下保留此前 38 场景的历史执行证据。

修复三项原有测试失败后，重新执行完整 `TestStandaloneAcceptanceMatrix`：**38/38 场景通过，0 fail、0 skip，总耗时 349.893 秒**。上轮未执行的 8 项 Claude 或跨 provider 场景本轮全部覆盖。矩阵包含真实 binary、CLI、模型、进程内集成及合成证据，不将每一项都称作 binary E2E，也不外推为全仓或发布验收。

| 场景组 | 边界与主要断言 | 结果 |
|---|---|---|
| 控制与存储 | 10 项真实 binary，3 项进程内 Fx/store，4 项输入帧、进程身份或 SQLite 证据测试 | 17/17 通过，8.47 秒 |
| 客户端与模型 | Codex/Claude 官方 CLI、实际 MCP 工具调用、binary 受管模型、消息恢复和完整生命周期 | 9/9 通过，180.18 秒 |
| 生命周期 | 崩溃恢复、fork、resume、archive、interrupt、Claude 执行/停止与 execution 丢失恢复 | 9/9 通过，130.96 秒 |
| 消息交付 | Codex、Claude 和双向跨 provider 的真实提交与接收报告 | 3/3 通过，30.27 秒 |

审查发现旧 delivery 断言只检查报告非空，未充分关联本次消息。已增强这 3 个场景：提交前读取报告基线；每方向使用随机 marker；提交后同时校验接收者身份、序号推进、正文 marker、同报告的 success outcome 和 active turn 清空。保留原生 submitted/submission_id、权限 scope 和幂等键断言。随后独立重跑 `TestStandaloneNativeDeliveryMatrix`：**3/3 通过，0 skip，43.296 秒**（Codex 21.09 秒、Claude 5.58 秒、双向跨 provider 16.60 秒）。完整矩阵使用补强前的已编译测试快照；补强后的交付测试单独重跑，未重复计算为新增场景。

Codex 11 项包括：`TestStandaloneBinaryCodexLifecycleThroughMCP`、`TestStandaloneBinaryCodexAutoRecoversProviderCrash`、`TestStandaloneOfficialCodexClient`、`TestStandaloneCodexClientLaunchesManagedCodexThroughMCP`、`TestStandaloneManagedCodexTurn`、`TestStandaloneManagedCodexFork`、`TestStandaloneManagedCodexResume`、`TestStandaloneManagedCodexArchive`、`TestStandaloneManagedCodexInterrupt`、`TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss`、`TestStandaloneDirectPeerSubmitsReceiverTurn`。测试登记表明确各证据层级，没有把模拟或进程内测试计为 binary E2E。

## 新场景与发现

消息交付扩展新增：Codex/Claude 各三轮正文与分页、三轮跨 provider 往返、双方幂等与 busy/unknown、撤权/重授/停止、真实 Codex receipt 跨服务重启重放；输入/scope、读写权限和授权版本矩阵；同 key 竞争单次 native 提交；native ack 后 event/outbox 写入失败及重建 owner 后不重发。权限和模型身份均通过真实 Registry/SQLite 路径校验；故障窗口使用明确标注的 gated native fixture，不能冒充真实 provider 崩溃。

新增测试发现并修复两处生产问题：

- 撤销后同方向重新授权被唯一约束阻断。现在为明确新授权生成新 grant ID 并递增 revision，保留历史幂等 key；旧 grant/revoke 重放不修改当前状态，旧消息不能跨 revision 重发或读取。没有删除历史记录或放宽唯一约束，没有 DDL 变更；SQL 使用固定 sqlc 版本生成并重复校验一致。
- Claude `system` 事件的顶层 `message` 可以是字符串。此前被对象解码器拒绝而终止 provider；现在单独保存为系统元数据，不能进入模型报告或 native ack。用户/助手消息仍严格校验，非法字段类型和 JSON 继续报错；错误诊断不含正文。

测试前置也已校正：报告生成并不保证 receiver 已完全 idle，授权恢复场景现在显式等待 idle；Claude 的 busy 前置用真实 receipt 对应的同 thread/turn 与 `turn_running` 证明，不要求生产 dontAsk 模式执行未授权 Bash。Codex busy 仍使用真实 shell 随机就绪文件。初次扩展矩阵中 Claude 正文 marker 缺失和授权恢复 unknown 的失败日志保留在 `/tmp/orch-delivery-expanded-final.jsonl`，不得将该次整套运行计为通过；后续定向复测在 `/tmp/orch-delivery-targeted-recheck.jsonl` 通过。

`TestStandaloneBinaryCodexMessageRecovery` 验证：

1. 模型实际执行 shell，读取每轮更新的随机文件并返回真实 cwd，避免仅复述提示就通过。
2. `send_message` 已提交而报告等待超时，错误必须具有准确阶段、身份、序号和不可重试标志；将返回的原始只读恢复参数直接交给 MCP，不重新拼装或重发消息。
3. 原生 provider history 中本次用户输入恰好一次；恢复后的新报告序号递增且 thread ID 不变。
4. 随机就绪文件证明实际 shell 已启动，仍是同一个 active turn 后才调用公开 `interrupt_agent`；核对 durable terminal head、`interrupted` outcome、idle 状态及清空 active turn。
5. 中断后在同一 thread 继续真实模型轮次；随后启动 `sleep 60`，确认随机就绪证据和 active turn 后立即调用 `stop_agent(wait:true)`，要求停止及归档成功、已观察的受管进程在 5 秒内全部消失。不会等命令自然结束才停止。
6. 停止前打开唯一匹配的测试 thread 历史文件，归档移动路径后仍从同一文件句柄核对原生输入次数；daemon 退出后，再验证中断前与停止前观察到的全部稳定进程身份均已消失。

真实 E2E 发现公开状态读取把 `turn_completed` 的 interrupted/failed outcome 当成 agent 的 stopped/failed 状态。修复后以 projection kind 决定运行态，保持报告 outcome 的失败/中断语义；公共读取、投影和恢复重放使用同一映射。未知 projection kind 报错。对应真实红态、SQLite 回归和动态契约守卫已验证；完整 orchestration 包通过。

测试自身也暴露过提示中 `Marker` 与原生输入断言 `marker` 大小写不一致的问题。修正提示并新增合成原生帧回归，未放宽输入次数断言。

随后清理断言发现实际 `sleep` 以独立 PGID 存活，daemon 退出后 PPID 为 1；5 秒等待结束时依然存在。按进程组发送信号无法覆盖该独立会话。关闭时改由 Codex 原生 `thread/backgroundTerminals/clean` 清理其拥有的工具会话，保持错误可见，不按观察到的 OS PID 杀进程。该阶段真实场景已通过（53.859 秒），归档和早期中断的真实 CLI 回归也通过（2/2，16.219 秒）。

曾额外要求 `interrupt_agent` 返回后 5 秒内命令即退出。真实协议诊断显示，当前工具命令并未出现在 thread 的最终 item 或后台终端列表中。进一步核对官方协议并经独立审查确认：原生 `turn/interrupt` 不终止后台终端，显式资源清理属于另一操作。因此撤回了这一额外要求及实验性 interrupt 清理实现，保留原生 turn 中断；将严格的长命令退出检查放在 `stop_agent` 路径。公开工具说明和 binary schema 测试现已明确提示“后台命令可能继续运行，需要清理资源时使用 stop_agent”。这没有撤销最初的关闭残留缺陷或 5 秒清理断言。[OpenAI Codex app-server 协议](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md)

最终清理实现使用显式 experimental API 能力：`Close` 在关闭协议前以独立、有界 context 请求 owner 清理；归档先清理再执行 `thread/archive`，成功后记录归档身份，避免 `Close` 重复访问已经卸载的线程。清理、归档或响应缺失均报错，未加入 OS PID 信号兜底。

## 复现与证据边界

控制面不需要模型：

```sh
go test -tags=e2e ./cmd/mcp-orch -run '^TestStandaloneControlAndStoreMatrix$' -count=1 -timeout=180s
```

真实 Codex 场景需要显式设置 `ORCH_E2E_CODEX_BIN`（解析符号链接后的真实文件）、`ORCH_E2E_CODEX_HOME`（隔离且已认证）、`ORCH_E2E_CODEX_MODEL`：

```sh
go test -tags=e2e ./cmd/mcp-orch -run '^(TestStandaloneBinaryCodexMessageRecovery|TestStandaloneBinaryCodexLifecycleThroughMCP|TestStandaloneBinaryCodexAutoRecoversProviderCrash)$' -count=1 -timeout=900s
```

完整矩阵通过命令如下；需同时显式设置 `ORCH_E2E_CLAUDE_BIN`、`ORCH_E2E_CLAUDE_HOME`、`ORCH_E2E_CLAUDE_MODEL` 并具有可用的宿主 Claude 登录。运行器把 skip 视为失败。

```sh
go test -tags=e2e ./cmd/mcp-orch -run '^TestStandaloneAcceptanceMatrix$' -count=1 -timeout=2400s -json
```

覆盖边界：Claude probe 是无模型握手；ManagedClaudeStop 是无 prompt 的真实 CLI 停止；跨 provider 消息由测试通过 Registry 注入，不是模型自主发信。以上均按真实证据层级登记。

最初存在的三个基线失败已用独立 HEAD 归档复现，后经用户授权全部修复，详情见[认知评审报告](orch-mcp-cognition-review.md)。修复后的 `go test ./cmd/mcp-orch/... ./internal/platform/shared/builtinprompts -count=1` 整体通过，78 个有测试包通过、11 个包无测试文件。此命令范围和上述端到端范围均不等于全仓发布验收。

## 回归与独立审查

- 完整 orchestration、tools、cmd/mcp-orch、internal/mcpserver/common、standalone/codexwire 相关包测试通过；后续修复基线失败后，standalone 全包及整个 cmd/mcp-orch/... 也已通过。
- 轻量代码守卫、生产/测试冻结棘轮、priority SSA、项目地图刷新与检查、`git diff --check` 通过；变更生产文件与新增测试的 gopls 检查无诊断。
- Astra 低思考子代理独立检查清理错误到 MCP 的传播、归档顺序、原生中断边界与端到端证据，未发现未关闭的 P1/P2。复审不代替真实模型执行结果。
- 新增测试验证原生清理能力的初始化、clean/archive 字段覆盖、操作顺序、错误与 null 响应拒绝，以及实际受管进程退出和错误保留。没有新增未经所有权保证的 PID/PGID 补杀。

本轮完整矩阵日志 `/tmp/orch-e2e-full-matrix.jsonl`，增强交付复测 `/tmp/orch-e2e-delivery-strengthened.jsonl`，守卫 `/tmp/orch-e2e-comprehensive-guard.log`，gopls `/tmp/orch-e2e-comprehensive-gopls.log`。此前日志仍保留：控制矩阵 `/tmp/orch-e2e-control-final.jsonl`，Codex `/tmp/orch-e2e-codex-final.jsonl`，Claude CLI `/tmp/orch-e2e-claude-cli.jsonl`，核心回归 `/tmp/orch-e2e-core-final.log` 和 `/tmp/orch-e2e-final-regression.log`。最初关闭残留红态保存在 `/tmp/orch-e2e-message-recovery-detached-red.jsonl`；协议语义校正前的探测记录保存在 `/tmp/orch-e2e-message-recovery-native-final-items-red.jsonl`。这些为本机临时证据，重现应使用上面的仓库测试入口。

## 最终复验的口径

`/tmp/orch-delivery-frozen-56.jsonl` 保留完整运行的真实结果：其中原有 outbox 场景在通知到达但 ACK 尚未落盘时读到 claimed，不能将这次顶层结果改写为 PASS。修正测试后，按事件 ID 等待 owner ACK 提交；30 次重复执行通过（2.300 秒），整个 control 组 26/26 通过（9.583 秒，`/tmp/orch-delivery-verified-control26.jsonl`）。其它三组来自同一完整运行的实际结果，按组汇总，不隐藏中间红态。

`/tmp/orch-delivery-full-52.jsonl` 是较早失败尝试：新增解析测试漏登记及 Claude 拒绝 marker-only 合成消息。前者已登记，后者改为正常发票计算任务，并保留随机业务编号、准确金额、正文原样持久化与报告序号断言。两个 provider 的发票场景后续均通过。Claude 重启后的 receipt 读取已单独覆盖；重新激活线程当前为显式 unsupported capability，不能从 receipt 可读推断该功能已支持。
