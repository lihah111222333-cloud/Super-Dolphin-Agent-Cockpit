# B01–B18 blocker revalidation（只读复核）

Status: Active — planning evidence only

日期：2026-09-02；候选增量复核更新于2026-09-03。复核对象为隔离工作树
`/Volumes/hello/wj/super-agent-v3/.worktrees/20260831-orch-subagent-upgrade`。
本记录不改变 `current-blocker-registry.json`：其 schema 仍强制所有历史 blocker 保持
`needs_revalidation`，不能用本文件把任何 blocker 改成 closed，也不能把局部测试当作
生产发布授权。

## 复核口径

- `still_reproduced`：当前源码仍满足历史缺口的可达形状，或 closure requirement 的关键项仍缺失。
- `candidate_mitigated`：局部候选/负测已覆盖历史缺口的一部分，但尚未满足完整 closure requirement。
- `evidence_missing`：源码检查不足以证明关闭，所需运行/发布证据尚未取得。
- 所有本机命令均属于 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；`make orch-upgrade-check` 只证明规划输入一致。

## 结果

| ID | 当前分类 | 当前证据（源码/测试） | 关闭仍需具名证据 |
| --- | --- | --- | --- |
| B01 | still_reproduced | `internal/platform/rpc/control_rpc_auth.go:64-118` 仍只保存 authenticated 状态；`internal/platform/mcpcontrol/handlers.go:118-191`、`report_handlers.go:42-57` 仍从 payload 取 LeaseKey；仅 hooks 路径有局部 transport lookup。 | 真实 ctl connection 的 server-owned principal；A 冒用 B 的每个 method/heartbeat/evict/hook 零副作用。 |
| B02 | still_reproduced | `control_rpc_auth.go:84-118` 未建立 one-connection-one-register；`mcpcontrol/registry.go:137-165,269-296` 仍允许同连接再次注册并生成下一代 lease，hook lookup 仍遍历 map。 | 同 TCP connection 第二次 register 稳定 `already_registered` 且索引零副作用；disconnect/reconnect race 保持唯一 lease。 |
| B03 | candidate_mitigated | SQLite generation owner、resume 校验和多组 restart/crash 负测已通过；仍有 MemoryGenerationStore 与真实 production Fx/TCP host restart 连续性缺口。 | 真实部署切换、host restart、stale resume、并发 register/reconnect 的 transport/E2E 证据。 |
| B04 | still_reproduced | `internal/provider/codexapp/module.go:382-404,508-550` 仍保留 legacy PID/PPID/binary sweep；`orphan_sweeper.go` 与 `process_unix.go:124-138`、`process_windows.go:210-225` 的 TERM/KILL/TerminateProcess 没有两次 stable identity 复核。 | legacy scan 变成 discovery-only，或真实 PID reuse/同名 detached/TERM→KILL identity drift 夹具证明零误杀。 |
| B05 | still_reproduced | `internal/platform/toolbridge/handler_peer_decode.go:711-742`、`handler.go:473-498` 和 `proxy.go:109-152` 仍通过标准 `tools/list|tools/call` 反向调用 ctl peer。 | 恢复 stdio/data-plane 双通道，或冻结独立版本化 `ctl/tool-proxy/*` 的 request/cancel/auth/fence/backpressure 合同及守卫。 |
| B06 | still_reproduced | `handler_peer_decode.go:64-273`、`stdio_mcp_client.go:133-240`、`internal/module/mcp_server/service.go:231-269` 仍以 runtime discovery/backfill 形成工具输入；现有 membership digest/CAS 不是签名 canonical manifest。 | 唯一 registry→signed manifest→安装 seed/reconcile→strict admission；tamper/missing/mixed/new-tool 和 reverse-tools 禁止证据。 |
| B07 | still_reproduced（前端 evidence_missing） | `internal/app/tool_catalog_rpc.go:13-55` 仍返回旧 `ToolCatalogEntry`；`handler_host_tools.go:130-199` 仍 discovery/backfill，缺 workspace/snapshot/lifecycle/manifest/schema digest/cursor；前端 validator 仍锁旧 DTO。 | non-authorizing effective snapshot、完整字段 producer/consumer、direct-call policy deny、前端测试与 selected-tool binding；当前 vitest 因缺 `typescript`/`vite` 未运行。 |
| B08 | still_reproduced | `cmd/mcp-orch/orchestration/wakeup_dispatcher.go:264-303` 仍先 `LaunchAgent` 后 `MarkWakeupSent`，未接 canonical command/attempt/receipt/reconcile。 | before-mark crash/restart/reclaim 的真实外部计数=1，以及 command owner 先 accepted 再 dispatch。 |
| B09 | still_reproduced（含 migration 134 candidate mitigation） | `wakeup_dispatcher.go:395-421` 的旧 dispatcher fallback/生产 command owner 仍未闭合；migration 134 为 `task_dag_wakeups` 增加 `payload_schema_version=0/1`、version1 基础 envelope trigger 与 ClaimDue version filter，Go decoder 才负责完整 strict 语义；历史 v0 行仍需显式 planner/applier/quarantine。 | enqueue/dispatch strict discriminator+schema decoder、typed quarantine/failed、invalid/mixed payload 在 launcher 前零调用；生产 applier/receipt 与跨进程 exactly-once 证据。 |
| B10 | still_reproduced | `internal/platform/db/sqlite/open.go:34-52` 仍提供共享 RW SQLite；`store/taskdag/factory.go:78-94,148-157` 与 `task_dag_wakeup_dispatch.sql:1-90` 没有 writer epoch/deployment CAS。新增 `writer_inventory.json`/archtest 只冻结 M01–M42 的 receiver/file/symbol 集合，未接 owner。 | 全部 mutation owner 的 subprocess A/B stale-writer zero-row 证据、DB/file 写权撤销或 namespace/termination 硬隔离。 |
| B11 | still_reproduced | `internal/platform/rpc/server.go:407-508` accept 后无 pre-auth slot/deadline/byte cap，直接启动 goroutine/jrpc2；jrpc2 split channel 无硬 LF 上限。 | process-global/per-principal cap、auth/register/read/frame/idle deadline、slowloris/oversize/idle 资源归零与高优先级 control SLA。 |
| B12 | still_reproduced | `store/taskdag/store_wakeup.go:123-133` 使用本地 wall clock；`task_dag_wakeup_dispatch.sql:1-90` 使用 SQLite `strftime('now')`；没有统一持久 security-time high-water/clock epoch。 | security-time owner、monotonic/bounded-skew 规则、回拨/前跳/sleep-resume/restart/mixed-process 负测。 |
| B13 | still_reproduced | `frontend-app/src/pages/chat/markdown/markdownMessageModel.js:86-95` 接受任意 HTTP(S) image；`MarkdownMessage.jsx:101-110,276-280` 与 `MarkdownImagePreview.jsx:30-53` 直接渲染 `<img src>`. | inert 默认展示、用户同意、无 ambient credential 的受限 proxy、CSP/sanitizer、private-IP/redirect/DNS-rebind/zero-network 证据；前端 vitest 当前未运行。 |
| B14 | still_reproduced（局部 candidate mitigation） | `cmd/mcp-orch/runtime.go:330-345` 已复制 OutputSchema，`registry.go:126-152` 已拒绝重复工具名；但 MCP wire 的 title/icons/annotations/execution/_meta、revision/hash manifest 与 future-schema guard 仍未闭合。 | 完整 stdio tools/list round-trip、所有 wire 字段/manifest hash、missing/unknown/stale/future/duplicate 的生产链证据。 |
| B15 | still_reproduced | `internal/platform/mcpcontrol/registry_support.go:50-77` 接受 peer 自报 BinaryName/PID/PeerKind/ClientKind；非 managed peer 的 offered capability 仍可进入 index，`fanout.go:40-52` 依该索引路由。 | server-owned profile + supervisor/PID attestation，全部 peer 的 offered∩allowed 负测，伪身份零索引零 fanout。 |
| B16 | still_reproduced | `mcpcontrol/registry.go:330-380` 不消费 ObservedConfigVersion；`registry_helpers.go:31-46`、`config_fanout_worker.go:343-385` 在通知前推进版本，通知失败仅 Warn。 | applied_acked 状态、lease/boot/digest/receipt fenced ack、乱序/gap/resync 和未应用安全策略阻断。 |
| B17 | still_reproduced | `internal/platform/rpc/server.go:472-508` 断连 defer 只有 `removeActive`；production 没有接 `mcpcontrol.OnDisconnect`，sweeper 需等待约 30s+grace 才清理。 | 真实 TCP/WebSocket close→exact lease evict、hook/route/index 即时归零、重复 close 幂等和 reconnect generation race。 |
| B18 | still_reproduced | `internal/platform/db/sqlite/migrate.go:75-103,175-250` 在事务外读取 pending，并用普通 deferred `BeginTx` 逐步执行；没有 migration leader lease/epoch/checkpoint/hash drift owner。 | 双进程每版一次、leader kill/recovery、锁内 marker/hash 重读、per-step checkpoint 和脚本漂移拒绝。 |

## 可复查命令

本轮复核使用的代表性命令均为只读检查：

```text
make orch-upgrade-check
go test ./internal/archtest -count=1 -timeout=30m -p=1
go test ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody -count=1
go test -race ./cmd/mcp-orch/store/identity ./cmd/mcp-orch/store/eventstore ./cmd/mcp-orch/store/accesswait
go vet ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody
```

历史 full archtest、相关包测试/race/vet 与代码守卫结果只能证明当时候选的局部结构和负测；本轮新增候选后的源码冻结 full archtest 已完成并返回 `ARCHTEST_EXIT=0`（包内 1019.957s、外层 real 1075.50s）。所有结果均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。B01–B02、B04–B13、B15–B18 仍存在源码级或证据级缺口；B03、B14 也只有局部缓解，不能关闭。下一批优先级应为 B08/B09/B10/B12 的 canonical command/writer/time owner，再做 B01/B02/B05/B11/B15/B16/B17 的真实 control transport hardening；所有跨任务 send、Room durable owner 和 provider bridge 继续保持 disabled/NO-GO。

## 变更后的候选增量（2026-09-03，不改变台账状态）

本节记录本轮实现后的局部证据；`current-blocker-registry.json` 仍保持全部历史
blocker 为 `needs_revalidation`，以下分类不能替代完整 closure requirement。

- B08：新增内存 `wakeupcommandcandidate`，冻结 command/wakeup fence、唯一 attempt、
  dispatch receipt/permit/nonce 与 unknown-reconcile 规则；fake provider 的 crash-window
  测试通过，但当前 dispatcher 仍是外部 effect 先于 `MarkWakeupSent`，没有 OUT/custody
  接线和真实跨进程计数证据，仍 NO-GO。
- B09：`taskdag` 入队、事务内原子派发和 dispatcher route 前均执行严格版本化载荷
  判别联合；非法行在 launcher/NodeRouter 前 `FailWakeup`。新增的
  `legacywakeupmigration` 仍只是操作员审核 planner；migration 134 已补 version marker、
  ClaimDue filter、基础 direct-SQL trigger、branch/row identity immutable 与独立 quarantine
  表；active v0 行会被启动 gate 阻断，planner 只按显式 source/digest/CAS 生成动作，但
  没有生产 applier、CAS/receipt 或跨进程恢复，故仍为 `candidate_mitigated`、不能 closed。
- B10：新增 `taskdagwriterfence` subprocess A/B cutover 候选；它未接入全部 shared
  taskdag mutation owner、Fx 或 release hard isolation，B10 仍待复核。
- B12：新增 `securitytime` 可注入状态机候选；它未接外部 security-time allocation/
  receipt 或 taskdag SQL 参数化，B12 仍待复核。
- B09 liveness 增量：`terminal_failure_compensation` 已从 running-only claim 分支
  拆出，终态 run 仍可消费补偿；普通 launch/complete wakeup 仍禁止在终态 claim。
  该增量不改变 B09 台账状态，也没有独立消费 receipt/OUT owner。
- RecoveryCustody：外部服务已改为显式 `external_service=true` 的独立私有 SQLite
  模式，authority schema revision 3 持久化 mode；旧 revision-2 文件不做隐式迁移，
  PostgreSQL、产品 DB 和共享文件继续排除。
