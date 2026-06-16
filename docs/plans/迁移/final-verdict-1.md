# 最终裁定（1/3）

## Blocker 验证

### B1 submit 执行链：✅修复完成

- `TurnStarter` 窄接口已定义，并被 orchestration service 持有/注入：`internal/sidecar/orch/orchestration/contract.go:36-38`，`internal/sidecar/orch/orchestration/service.go:29-35,80-95`。
- `SubmitTurn -> claimTurnWork -> runnerActor.processTurnQueues -> startTurnExecution -> turnStarter.StartTurn` 已形成真实执行链：`internal/sidecar/orch/orchestration/service.go:166-186,291-324`，`internal/sidecar/orch/orchestration/runner_actor.go:33-35,62-65`，`internal/sidecar/orch/orchestration/helpers.go:140-150`。
- `turn` 侧 adapter 已提供并实际调用 `PrepareTurn + StartTurn`：`internal/module/turn/module.go:7-15`，`internal/module/turn/orchestration_starter.go:18-52`。
- 模块装配链已闭合，`turn.Module` 与 `orchestration.Module` 同时挂入 app：`internal/app/modules.go:23-37`。

### B4 BinaryDir：✅修复完成

- `turn.NewService` 已在构造时解析当前可执行文件目录并注入 manifest builder：`internal/module/turn/service.go:26-45`。
- `manifestBuilder` 已提供 fallback；未显式传入 `BinaryDir` 时会回退到 service 级默认目录：`internal/module/turn/manifest.go:17-31`。
- provider manifest 已改为 `filepath.Join(ctx.BinaryDir, name)`，不再做根路径字符串拼接：`internal/dto/provider/manifest.go:30-42`。
- 回归测试已覆盖“默认取 executable 目录”和“显式 BinaryDir 覆盖”两条路径：`internal/module/turn/service_test.go:78-112`。

## 总审报告复审

### review-platform-rpc（逐项裁定）

- `⏳ 推迟 P7` `approval_lifecycle` 的 `Cleanup/RestorePending/PendingSnapshot` 仍未接线，但 live callback 主链已闭合，不阻塞当前交付。报告：`docs/plans/迁移/review-platform-rpc.md:14,159-181`；当前定义仍独立存在：`internal/platform/rpc/approval_lifecycle.go:10-43`。
- `🔧 当场修复` handler 聚合仍无重复 key 检测，后注册项会静默覆盖先注册项。报告：`docs/plans/迁移/review-platform-rpc.md:15,77-90`；当前实现：`internal/platform/rpc/module.go:47-49`，`internal/platform/rpc/server.go:36-39`，`internal/platform/rpc/registry.go:5-12`。修复方案：在 `Server.Register`/`Registry` 合并前检测重复 method，重复时返回 error 或至少拒绝覆盖并打日志。
- `🔴 仍 Blocker` approval callback method family 仍未对齐 V2；默认方法仍是 `tool/approval/request`。报告：`docs/plans/迁移/review-platform-rpc.md:16,146-157,301-304`；当前默认值与回退逻辑仍在：`internal/platform/rpc/approval_events.go:13,37-39`。
- `⏳ 推迟 P7` `request_context.go` 仍只承载 CWD；ThreadID 实际走 `ThreadScope`，AgentID 仍无 context helper。报告：`docs/plans/迁移/review-platform-rpc.md:17,232-249`；当前代码：`internal/platform/rpc/request_context.go:5-14`，`internal/platform/rpc/handler.go:43-68,98-101`。
- `🔧 当场修复` `codec.go` 仍是未接线的业务 payload wrapper，不是运行时 JSON-RPC codec。报告：`docs/plans/迁移/review-platform-rpc.md:18,204-218`；当前 `jrpc2` 直接承担协议编解码：`internal/platform/rpc/server.go:49-63,77-102`，`internal/platform/rpc/transport_ws.go:21-43`；`codec.go` 本体仍未接线：`internal/platform/rpc/codec.go:3-22`。修复方案：删除死代码或明确把它接到业务层返回包装。
- `🔴 仍 Blocker` `request_user_input` 统一桥接与自动应答策略仍缺失。报告：`docs/plans/迁移/review-platform-rpc.md:304-305,313-318`；当前 push bridge 仍只桥接 3 类事件：`internal/platform/rpc/push.go:16-19,75-92`；`RequestUserInput` helper 存在但无接线点：`internal/platform/rpc/approval.go:98-103`。
- `⏳ 推迟 P7` 非 approval push 事件仍未透传 `requestId`，前端关联请求与推送仍不完整。报告：`docs/plans/迁移/review-platform-rpc.md:305`；当前 `NotifyAll`/`subscribeCoreEventPushes` 仍直接推 DTO 原始事件：`internal/platform/rpc/server.go:66-74`，`internal/platform/rpc/push.go:82-90`。
- `⏳ 推迟 P7` notifyHook/SSE/Wails fallback 与 pending restore 兜底仍未补齐。报告：`docs/plans/迁移/review-platform-rpc.md:306,313-318`；当前 approval 仍依赖 live callback：`internal/platform/rpc/approval.go:71-96,149-202`，`internal/platform/rpc/approval_lifecycle.go:10-34`。
- `⏳ 推迟 P7` 更宽的 event method map / UI replay 基础设施仍未迁入。报告：`docs/plans/迁移/review-platform-rpc.md:307,313-318`；当前标准推送仍只有 `ui/state/changed`、`turn/started`、`turn/completed`：`internal/platform/rpc/push.go:16-19,75-92`。
- `⏳ 推迟 P7` WS transport 仍缺显式 origin/backpressure/连接退避治理。报告：`docs/plans/迁移/review-platform-rpc.md:308`；当前 upgrader 仍是零值，channel 也只有最小封装：`internal/platform/rpc/transport_ws.go:17,21-43,55-105`。

### review-platform-infra（逐项裁定）

- `🔴 仍 Blocker` config 仍只有“环境变量 + 默认值”，没有文件来源层，也没有来源优先级合并。报告：`docs/plans/迁移/review-platform-infra.md:17,393-414`；当前实现：`internal/platform/config/config.go:15-40`。
- `⏳ 推迟 P7` “听了没人发”的 orphan 事件族仍存在，当前仍只在 `LogSink` 订阅。报告：`docs/plans/迁移/review-platform-infra.md:19,235-251,631-645`；当前订阅点：`internal/platform/bus/sink.go:51-56,68-72,83-86`；仓内对 `TurnStalled`/`TurnResumed`/`TaskDagCreated`/`UIProjectionUpdated` 的命中仍只落在 DTO 定义与 sink：`internal/dto/turn/event.go:24-31,54-55`，`internal/dto/task/event.go:6-13,38`，`internal/dto/ui/event.go:6-13,29`。
- `⏳ 推迟 P7` “只有日志消费”的弱 orphan 仍存在；高价值事件仍缺真实业务消费者。报告：`docs/plans/迁移/review-platform-infra.md:18,183-203,253-268,652-654`；当前业务级非日志订阅仍集中在 orchestration/rpc push：`internal/sidecar/orch/orchestration/module.go:33-44`，`internal/platform/rpc/push.go:75-92`。
- `🔧 当场修复` bus 辅助 API 的“死代码 + 小瑕疵”仍在：`NewRouter` 仍未使用 dispatcher，`Projector.State()` 仍无 nil guard，`BindEventToNotify`/`NewProjector` 仍无运行时调用点。报告：`docs/plans/迁移/review-platform-infra.md:30,116-138,220-226`；当前代码：`internal/platform/bus/router.go:14-22`，`internal/platform/bus/projection.go:16-43`，`internal/platform/rpc/push.go:60-73`。修复方案：为 `State()` 补 nil guard，并清理或接线未使用 helper。
- `🔴 仍 Blocker` `awaiting_user_input` 状态链路仍未接入运行时。报告：`docs/plans/迁移/review-platform-infra.md:21,338-348,642-646`；当前触发器仍只存在于声明表：`internal/dto/agent/state.go:28-29,96-104`；运行时代码没有 fire 点，approval 侧仅把该状态当字符串默认值：`internal/platform/rpc/approval_support.go:27`。
- `🔧 当场修复` runner 提前 `nil` 返回导致整组 cancel、但 app 不主动 shutdown 的半死风险仍在。报告：`docs/plans/迁移/review-platform-infra.md:37,383-391`；当前行为：`internal/platform/runner/group.go:22-37,66-74`，`internal/app/runner.go:37-50`。修复方案：把“非 cancel 的首个 actor 返回 nil”视为异常退出并显式 `Shutdown()`。
- `✅ 已修复` “`Config.LogLevel` 无消费方”这条报告结论已过时；当前 Wails 启动已读取 `cfg.LogLevel` 决定 debug 模式。报告原结论：`docs/plans/迁移/review-platform-infra.md:38,436`；当前消费点：`internal/ui/wails/module.go:69-73`。
- `⏳ 推迟 P7` 多数 timeout 常量仍未接线；当前只有 `WithRPCRequestTimeout` 和 `InterruptSettleTimeout` 有运行时使用。报告：`docs/plans/迁移/review-platform-infra.md:415-433,637`；定义：`internal/platform/config/timeouts.go:8-21`；现有使用：`internal/module/skill/exec.go:57-59`，`internal/module/turn/service.go:200-205`。
- `⏳ 推迟 P7` db pool 仍只建最小可用池，未消费生命周期/空闲/健康检查配置。报告：`docs/plans/迁移/review-platform-infra.md:22,438-466,647-649`；当前实现：`internal/platform/db/module.go:19-39`。
- `🔧 当场修复` `WithTx` 仍缺 panic-safe rollback。报告：`docs/plans/迁移/review-platform-infra.md:491`；当前实现只在 `fn(tx)` 返回 error 时 rollback：`internal/platform/db/tx.go:11-20`。修复方案：在 `WithTx` 外层加 `defer` 捕获 panic，先 rollback 再重新 panic。
- `⏳ 推迟 P7` `RequireNonEmpty` 仍未接入，`NewID` 仍是双实现。报告：`docs/plans/迁移/review-platform-infra.md:23,510-533,638-649`；当前 `RequireNonEmpty` 仍只有定义：`internal/platform/shared/validation.go:9-14`；重复实现仍并存：`internal/platform/shared/idgen.go:10-14`，`internal/dto/shared/ids.go:10-14`；运行时仍双轨调用：`internal/provider/claudecli/config.go:92`，`internal/provider/claudecli/session.go:137`，`internal/module/thread/lifecycle.go:185`，`internal/module/turn/service.go:61,295`。
- `⏳ 推迟 P7` `runner/statemachine` 仍是空壳 module，`shared` 仍无 `module.go`。报告：`docs/plans/迁移/review-platform-infra.md:42,534-589`；当前代码：`internal/platform/runner/module.go:1-5`，`internal/platform/statemachine/module.go:1-5`。

### review-module-thread（逐项裁定）

- `🔴 仍 Blocker` 大量 handler 仍只是 `SendCommand` 骨架或直接 unsupported；thread 模块距离 V2 parity 仍很远。报告：`docs/plans/迁移/review-module-thread.md:13-15,208-240`；当前路由矩阵：`internal/module/thread/rpc.go:58-82`；底层 `SendCommand` 仍只真正支持 `/model`、`/personality`、`/approvals`、`/interrupt`：`internal/module/thread/command.go:20-37`。
- `🔴 仍 Blocker` RPC 参数面仍大范围不兼容 V2。报告：`docs/plans/迁移/review-module-thread.md:83-113,315-336,457-466`；当前参数结构仍是精简版：`internal/module/thread/rpc_types.go:7-37`；`thread/start`/`resume`/`messages`/`config/*`/`realtime/*` 仍按旧 shape 接收：`internal/module/thread/rpc.go:20-32,51-82,118-129`。
- `🔴 仍 Blocker` 返回结构仍不兼容 V2；`StartResult/ForkResult/Ref` 仍无 JSON tag，多个路由仍返回 `nil` 或裸数组。报告：`docs/plans/迁移/review-module-thread.md:338-359`；当前定义：`internal/module/thread/contract.go:40-68`；当前 effect handler 仍统一返回 `nil`：`internal/module/thread/rpc.go:92-96,118-129`。
- `🔴 仍 Blocker` history/messages 仍未实现 V2 语义；`thread/read` 仍没走 `ReadHistory`，`thread/messages` 仍是本地过滤后的裸 `[]Message`。报告：`docs/plans/迁移/review-module-thread.md:127,175-188,349-356`；当前 `ReadHistory` 仍无 RPC 入口：`internal/module/thread/contract.go:17-18`，`internal/module/thread/history.go:13-20`；`thread/read`/`thread/messages` 仍分别走 `Get` 和 `ReadMessages`：`internal/module/thread/rpc.go:47-53`；`before` 仍是字符串并在本地过滤：`internal/module/thread/rpc_types.go:23-27`，`internal/module/thread/history.go:28-45`。
- `🔴 仍 Blocker` archive/unarchive/delete 仍只是“状态位 + session close / 删表”，不是 V2 的归档/恢复语义，也没有停 orchestration 进程。报告：`docs/plans/迁移/review-module-thread.md:190-206,261-268,343-347`；当前 `Archive/Unarchive`：`internal/module/thread/archive.go:5-20`；当前 `Delete`：`internal/module/thread/service.go:102-119`；`stopAgent` helper 存在但 archive/delete 路径未调用：`internal/module/thread/lifecycle.go:309-313`。
- `🔴 仍 Blocker` lifecycle 仍缺独立 thread identity、`running` 状态、完整 stop/recover/fork 语义。报告：`docs/plans/迁移/review-module-thread.md:253-279`；当前启动 thread id 仍直接回退到 `session.ThreadID()`/`agentID`：`internal/module/thread/lifecycle.go:62,338-340`；持久化状态仍固定写 `statusCreated`：`internal/module/thread/lifecycle.go:246-255`；`thread/loaded/list` 仍把 `created` 当 loaded：`internal/module/thread/rpc.go:44-46`，`internal/module/thread/service.go:16-19`。
- `🔧 当场修复` fx optional 依赖仍会把装配错误推迟到运行时。报告：`docs/plans/迁移/review-module-thread.md:295-309`；当前所有核心依赖仍标 `optional:"true"`：`internal/module/thread/module.go:7-18`；对应运行时错误路径仍在：`internal/module/thread/lifecycle.go:205-206,222-223`，`internal/module/thread/service.go:122-145,188-190,218-220`。修复方案：把 `threadStore`、`bindingStore`、`sessions`、`starter` 改为必需依赖，仅保留确属可选的 facade。
- `⏳ 推迟 P7` service 层错误处理仍偏弱，吞错点仍在。报告：`docs/plans/迁移/review-module-thread.md:366-390`；当前 `Delete` 仍忽略 `resolveBinding`/`closeSessionIfActive` 错误：`internal/module/thread/service.go:107-108`；`closeSessionIfActive` 仍吞 binding/session lookup 错误：`internal/module/thread/service.go:228-240`；`stopAgent` 仍丢弃 orchestration 错误：`internal/module/thread/lifecycle.go:309-313`。
- `⏳ 推迟 P7` 并发一致性风险仍存在：多存储、多阶段写入没有事务边界，也没有 per-thread 生命周期锁。报告：`docs/plans/迁移/review-module-thread.md:392-413`；当前 `persistThreadState` 仍是 thread/binding 两次独立写入：`internal/module/thread/lifecycle.go:238-270`；`Delete` 仍是“close session -> 删 binding -> 删 thread”的多阶段路径：`internal/module/thread/service.go:102-119`。
- `🔴 仍 Blocker` 线程模块仍没有单元测试保护。报告：`docs/plans/迁移/review-module-thread.md:430-448`；当前 `internal/module/thread/` 下 `*_test.go` 仍无命中（LSP `text_search(glob="*_test.go", path="internal/module/thread", query="Test")` 返回空）。

### review-module-turn（逐项裁定）

- `🔴 仍 Blocker` `turn/start` 的参数面和返回面仍不兼容 V2。报告：`docs/plans/迁移/review-module-turn.md:19-23,63-70,310-313`；当前 RPC 参数仍只有 `prompt/images/files/model/effort`：`internal/module/turn/rpc_types.go:5-12`；返回仍是 `{"turnId": ...}`：`internal/module/turn/rpc.go:33-46`，`internal/module/turn/rpc_types.go:35-37`。
- `🔴 仍 Blocker` `turn/steer` 仍是“按 prompt 再开一个新 turn”，不是 V2 的 active-turn steer。报告：`docs/plans/迁移/review-module-turn.md:72-79`；当前实现仍是 `PrepareTurn(...Prompt...)` 后直接 `StartTurn(...)`：`internal/module/turn/service.go:104-109`；测试名与行为仍明确是 `StartsPromptAsNewTurn`：`internal/module/turn/service_test.go:158-181`。
- `⏳ 推迟 P7` `turn/interrupt` 参数面基本对齐，但返回仍不是 V2 的 ack map。报告：`docs/plans/迁移/review-module-turn.md:81-89`；当前 handler 成功仍返回 `nil`：`internal/module/turn/rpc.go:60-65`。
- `⏳ 推迟 P7` `turn/forceComplete` 仍只是 `Interrupt(source="force_complete")` 包装，没有独立 contract。报告：`docs/plans/迁移/review-module-turn.md:91-98,314-315`；当前实现：`internal/module/turn/service.go:138-153`；测试也仍按 watcher 收尾设计：`internal/module/turn/service_test.go:183-224`。
- `🔴 仍 Blocker` `review/start` 仍未实现，而且 RPC 参数类型仍只有 `threadId`。报告：`docs/plans/迁移/review-module-turn.md:100-106,314`；当前 handler 仍直接 `ErrNotImplemented`：`internal/module/turn/rpc.go:74-77`；当前参数仍是 `threadIDOnlyParams`：`internal/module/turn/rpc_types.go:24-26`。
- `🔴 仍 Blocker` `approval/respond` 外部 RPC 契约仍不兼容 V2。报告：`docs/plans/迁移/review-module-turn.md:108-115,233-237,316`；当前参数仍暴露 `callId + optional requestId`：`internal/module/turn/rpc_types.go:28-33`；当前 handler 成功仍返回 `nil`：`internal/module/turn/rpc.go:79-91`。
- `🔴 仍 Blocker` assembler / skills / output schema 的 rich input 能力仍没有从 RPC 路径接通。报告：`docs/plans/迁移/review-module-turn.md:18,137-153,175-191,311-313`；当前 `PrepareInput` 能承载 `Inputs/Skills/CandidateSkills/OutputSchema/BinaryDir`：`internal/module/turn/contract.go:27-41`；但 `buildPrepareInput` 仍只填 `Prompt/Images/Files/Model/Effort/ThreadCaps`：`internal/module/turn/rpc_helpers.go:5-14`；assembler/skills 侧能力仍主要停留在 service 内：`internal/module/turn/assembler.go:47-117`，`internal/module/turn/skills.go:11-37`。
- `⏳ 推迟 P7` tracker 仍是纯内存子系统，没有持久化，也没有 RPC 暴露 `TrackTurn`。报告：`docs/plans/迁移/review-module-turn.md:155-173`；当前 tracker 仍是内存 map：`internal/module/turn/tracker.go:13-235`；`TrackTurn` 仍只存在于 service 接口/实现与测试：`internal/module/turn/contract.go:18`，`internal/module/turn/service.go:156-166`，`internal/module/turn/service_test.go:149-156,212-224`。
- `✅ 已修复` manifest `BinaryDir` 坏值问题已修复，报告中的根路径 `/go-agent-mcp-*` 结论已过时。报告原问题：`docs/plans/迁移/review-module-turn.md:22,193-215`；当前 service 默认注入 executable 目录：`internal/module/turn/service.go:26-45`；builder fallback 生效：`internal/module/turn/manifest.go:17-31`；manifest 拼接已改为 `filepath.Join`：`internal/dto/provider/manifest.go:30-42`；测试已覆盖：`internal/module/turn/service_test.go:78-112`。
- `🔧 当场修复` `approval/respond` 对 decision-only payload 仍不做归一化；只传 `"accept"`/`{"decision":"accept"}` 时，resolved event 的 `Approved` 仍可能是 `false`。报告：`docs/plans/迁移/review-module-turn.md:238-245,316`；当前 turn handler 仍只 raw copy `Decision`：`internal/module/turn/rpc.go:84-90`；approval 侧 `decisionApproved` 仍只看 `Approved` 指针：`internal/platform/rpc/approval_support.go:196-210`。修复方案：在 `approval/respond` 入口复用 `decodeApprovalDecision` 或等价归一化逻辑，先把 raw payload 规约到 `ApprovalDecision`。
- `🔧 当场修复` turn 模块的 fx optional 标注仍不严谨，Capability resolver 缺失时会把 capability 直接判成不支持。报告：`docs/plans/迁移/review-module-turn.md:265-281`；当前标注：`internal/module/turn/module.go:7-15`；当前 session resolver 缺失会直接报错：`internal/module/turn/rpc.go:20-29`；cap resolver 缺失会在 `CapabilityGate` 中稳定失败：`internal/platform/rpc/handler.go:71-85,94-95`，`internal/dto/provider/capability.go:30-35`。修复方案：把 `SessionResolver`、`CapabilityResolver` 的 DI 口径与运行时口径统一，必要依赖改必需。

## 互审

### 对 final-verdict-2 的批判

1. `B5 悬空接口：✅` 的口径过满，而且和同文件后文自相矛盾。`docs/plans/迁移/final-verdict-2.md:21-24` 把“当前相关接口面”概括为只剩 `orchestration.Service / SessionCleaner / TurnStarter` 与 `workspace.Service`，但 `docs/plans/迁移/final-verdict-2.md:41-42` 又承认 `SetReport` 仍是死接口。当前 `SetReport` 仍在接口和实现里，但 LSP `references` 为 0：`internal/sidecar/orch/orchestration/contract.go:19`、`internal/sidecar/orch/orchestration/report.go:39-49`。这说明 B5 至少需要补充“仅指 3 个已知悬空接口名已清理”的边界，否则 `✅` 会误导成“接口面已无悬空项”。

2. `review-module-skill` 的覆盖率项没有做当前轮验证，证据链不满足“每条必须 LSP 验证”。`docs/plans/迁移/final-verdict-2.md:67-68` 明确写的是“没有重新跑覆盖率，因此沿用报告结论”；它引用的仍是旧报告段落 `docs/plans/迁移/review-module-skill.md:263-325`，而不是当前代码证据。这一条最多只能算“未重新核实”，不能直接作为本轮 `⏳ 推迟 P7` 裁定。

3. `review-module-workspace` 漏掉了 `CreateRun` 的一个更实质的残留风险：文件系统副作用仍不在事务回滚面内。原审报告把这点明确写在 `docs/plans/迁移/review-module-workspace.md:66`；当前代码也仍然是先做 `os.MkdirAll` 和 `bootstrapFiles`，后才进入 `persistRun` 的 `WithTx`：`internal/module/workspace/service.go:57-73`、`internal/module/workspace/service_helpers.go:166-179`。但 `docs/plans/迁移/final-verdict-2.md:70-88` 只回收了 `runKey`、merge、bootstrap guard、`ListRuns` 和 dry-run event，没有复核这条，遗漏了原报告里比 `ListRuns` limit 更重的边界问题。

4. `review-module-orch` 少复核了一条结构性负面结论：`runner_actor` 仍不是 execute/interrupt actor。原审把这条单列为结论，见 `docs/plans/迁移/review-module-orch.md:327-342`；当前实现仍只是 `ticker + waiters + processTurnQueues + recoverStalledAgents` 的状态泵：`internal/sidecar/orch/orchestration/runner_actor.go:26-45,48-65`。但 `docs/plans/迁移/final-verdict-2.md:27-52` 只谈 submit 链、wire、report、recover、stop 时序，没有把这一条回收，导致 orchestration 的复审结论偏乐观。

### 对 final-verdict-3 的批判

1. 这份报告发生了明显的 scope drift，导致汇总口径失真。`docs/plans/迁移/final-verdict-3.md:17-94` 不只复核 `provider/store/contract-dto-app`，还重新展开了 `review-platform-rpc`、`review-platform-infra`、`review-module-thread`、`review-module-turn`、`review-module-orch`、`review-module-skill`、`review-module-workspace` 共 7 份前两批报告；最终 `docs/plans/迁移/final-verdict-3.md:115-119` 又把这些混到一张 `46` 项总表里。这样做会把 verify-3 的独立裁定和前两批问题重复计数，结论边界不清。

2. 它把 `approval callback method family` 降格成了 `⏳ 推迟 P7`，这个严重性判断站不住。`docs/plans/迁移/final-verdict-3.md:21-23` 把该项列为 P7；但原审在 `docs/plans/迁移/review-platform-rpc.md:299-318` 已把它列为 V2 RPC 基础设施的 6 项关键缺失之一。当前代码仍硬编码默认 method 为 `tool/approval/request`，且回退逻辑仍无覆盖点：`internal/platform/rpc/approval_events.go:13,37-39`。这直接影响 V2 前端 method 兼容，不应被轻描淡写地下沉成普通 P7。

3. `review-platform-infra` 里关于 `LogLevel` 的判断已经过时，但它仍照旧写成 P7。`docs/plans/迁移/final-verdict-3.md:29` 说“`LogLevel` 也仍未在配置装载层外扩展”，可当前 Wails 入口已经实际消费 `cfg.LogLevel` 决定 debug 模式：`internal/ui/wails/module.go:69-73`。这不是“待做项”，而是事实陈述已经失真。

4. 如果它决定重开 `review-platform-rpc`，那一节复核是不完整的。原审在 `docs/plans/迁移/review-platform-rpc.md:299-318` 明确列了 6 项关键 V2 缺口，其中包括 `request_user_input` 统一桥接、非 approval 事件 `requestId` 透传、fallback/UI replay、transport hardening；但 `docs/plans/迁移/final-verdict-3.md:19-25` 只回收了前 5 条“主要发现”，完全没提这组更关键的 V2 gap。当前代码也确实仍只桥接 3 类 core event：`internal/platform/rpc/push.go:16-19,75-92`。所以它一方面扩大了复核范围，另一方面又没有把被重开的报告复核完整。
