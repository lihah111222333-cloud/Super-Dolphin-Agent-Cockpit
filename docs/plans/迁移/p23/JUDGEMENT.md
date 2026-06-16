# P23 R3/R3B 终审仲裁

> 日期：2026-04-25  
> 范围：`docs/plans/迁移/p23/` 全量文档 + 现有 migration/sql/store/archtest 事实  
> 方式：15 个 Codex agent 终审 / 红队，只审不改；主 agent 收报告并仲裁落盘  
> 总裁定：**BLOCK**

---

## 1. 参与 agent 与结论

| 批次 | Agent ID | 维度 | 结论 |
|---|---|---|---|
| R3 | `agent_1777109529901721000` | runtime / watcher / dispatcher / lease | **BLOCK** |
| R3 | `agent_1777109531408650000` | 状态机 / crash / idempotency | NEEDS-FIX |
| R3 | `agent_1777109532954365000` | security / auth / tenant / audit | NEEDS-FIX |
| R3 | `agent_1777109534627162000` | archtest / CI / gates | **BLOCK** |
| R3 | `agent_1777109536169696000` | DB / migration / sqlc | **BLOCK** |
| R3 | `agent_1777109537711918000` | Host / Cron / External RPC | NEEDS-FIX |
| R3 | `agent_1777109543867104000` | Verifier / Arbiter / Strict JSON | NEEDS-FIX |
| R3 | `agent_1777109545349971000` | Scale / observability / backpressure | **BLOCK** |
| R3 | `agent_1777109546865153000` | 文档一致性 / 漂移 | NEEDS-FIX |
| R3 | `agent_1777109548388840000` | write-set / 实施拆分 | **BLOCK** |
| R3B | `agent_1777109591770624000` | runtime 红队 | **BLOCK** |
| R3B | `agent_1777109593358066000` | migration / sqlc 红队 | 空报告 / idle，未纳入计票 |
| R3B | `agent_1777109594924677000` | security 红队 | **BLOCK** |
| R3B | `agent_1777109596491655000` | gate 红队 | **BLOCK** |
| R3B | `agent_1777109598231231000` | 状态机红队 | **BLOCK** |

有效报告：14 份。  
结论分布：**8 BLOCK / 6 NEEDS-FIX / 0 PASS**。  
按会话规则“BLOCK 一票定调”，整体裁定为 **BLOCK**。

---

## 2. 已销账项

R2.1 修订有效，以下问题已基本销账：

1. P23 migration 主编号从旧 `0063/0064` 冲突修正为 `0065–0073` 暂排。
2. `schedule.trigger` 与 runtime `trigger_source` 已区分：`host` 不进入 `schedule.trigger`。
3. `active_wakeup_id` / `wakeup_id` 已统一为 BIGINT 口径。
4. watcher / dispatcher 分工主体已收敛：watcher ready claim；dispatcher launch + bind。
5. `pending_verify` 已清零，verify phase 统一到 `awaiting_verify / verifying / repairing`。
6. P8 错误旧锚 `service.go:323-325` / `service_launcher_bridge.go:277-290` 已清除。
7. P6 文档层已覆盖同 transport 全写面，不再只守 `task/*`。
8. localhost / Wails / MCP 已按 untrusted local attack surface 写入文档。
9. P13 RFC8785 / `$ref` / defaults / redaction 口径大体收敛。
10. schema registry、RPC registrar、hook tap registry 的拆分方向已写入 README。

---

## 3. BLOCK 仲裁清单

### B1. Phase0 gate 仍不可执行

**裁定：CONFIRM / BLOCK**

证据与共识：

- `go test ./internal/archtest/... -count=1` 当前仍失败。
- P23 21 项 archtest 仍是 planned，未落可发现 skeleton。
- `internal/archtest/dependency_direction_mcp_orch_test.go` 仍宽泛放行 `internal/store` / `internal/module`。
- 手动 fallback 载体仍不存在：无 PR template / 等价强制记录机制。
- 多个 agent 指出 COMPLIANCE 对 CI / archtest 当前事实有漂移：仓库已有通用 `.github/workflows/ci.yml` / `deep-ci.yml`，但无 P23 gate；archtest 当前失败不再是旧 unused import 单点。

**必须修：** Phase0 gate PR 先落地：archtest 恢复为可用红绿门、21 项 skeleton、allowlist 收紧、P23 CI 或 PR template/manual fallback。

---

### B2. 当前代码事实与 P23 状态机契约冲突

**裁定：CONFIRM / BLOCK**

主要事实：

- `CompleteTaskDagNode` 仍无 `active_turn_id + attempt_no` fence。
- `UpdateTaskDagNodeStatus` / flexible update 仍是裸状态写。
- `UpdateAwaitingVerifyNodeStatus` 与 `CompleteTaskDagNode status IN ('running','awaiting_verify')` 仍保留旧主状态路径。
- `GetTaskDagNodesForUpdate` 仍整 DAG `FOR UPDATE`。
- `EnqueueWakeup` 仍是 `:execrows`，不是返回真实 wakeup id。

**仲裁：** 文档已把很多契约写清，但 P0/P1/P2/P8 不能声称可实施通过；Phase0/P0 必须把这些作为 hard blockers。

---

### B3. lease subject 仍未冻结

**裁定：CONFIRM / BLOCK**

冲突：

- README 仍有 `dagLeaseActor` 续租 “node lease” 口径。
- 现有半成品 `task_dag_worker_leases` 是 `target_agent_id` 维度。
- wakeup 表本身有 `claimed_by / lease_expires_at`。
- P0 没有 node-level `lease_owner / lease_expires_at` DDL。

**必须修：** 明确三类 lease 的边界：

| 类型 | scope | 续租者 | 过期处理 |
|---|---|---|---|
| wakeup claim lease | `wakeup_id` | dispatcher | reclaim wakeup |
| node attempt lease | `dag_key,node_key,attempt_no` 或明确不引入 | lease/reconcile | reconcile candidate / observe_lost |
| worker lease | `target_agent_id` | worker/lease actor | 不直接改 node terminal |

若 P23 不做 node lease，必须删除 “node lease 30min / 5min heartbeat” 旧口径。

---

### B4. `observe_lost` retry 语义冲突

**裁定：CONFIRM / BLOCK**

冲突：

- README 说 `observe_lost` 是第三类终态，不自动重试、不计 retry budget。
- P0/P2 真值表仍写 `observe_lost + retry > 0` 可开新 attempt。

**必须修：** 二选一冻结：

1. `observe_lost` 是不可恢复终态：删除 P0/P2 retry 行。
2. `observe_lost` 是 terminal fact 但可 relaunch：README 改掉“不自动重试 / 不计 retry budget”，并说明是否扣 `execution.retry`。

---

### B5. P0/P1/P2 实施边界仍会制造半提交与 write-set 冲突

**裁定：CONFIRM / BLOCK**

问题：

- P0 若单独启用 watcher claim，会产生 `running + active_wakeup_id` 但无 dispatcher 的半提交死区。
- P1/P2 actor 文件落点仍与 README/P0 的 `internal/sidecar/orch/orchestration/runtime/*_actor.go` 不一致。
- `0065_dag_state_machine.sql` 同时承载 P0/P1/P2，但唯一 owner 未硬锁。
- P0 待办仍与 P3 `StartDAG` 所有权有冲突风险。

**必须修：**

- P0 只落 DDL/CAS/actor skeleton，默认 runtime disabled；P1 ready 后再允许 watcher claim。
- P1/P2 actor 路径统一到 `internal/sidecar/orch/orchestration/runtime/dispatcher_actor.go` / `reconcile_actor.go`。
- `0065` 只允许 Phase0/P0 schema owner 修改；P1/P2 不并行抢同一 migration。

---

### B6. 安全 gate 仍是文档契约，真实写面无 guard

**裁定：CONFIRM / BLOCK for implementation**

事实：

- TCP JSON-RPC / Wails WS / MCP HTTP 当前仍可直接进入写面。
- `agent.launch`、`agent.submit*`、`agent.stop`、report runtime/event、`task/dag/create`、`task/node/update` 当前无统一 AuthN/AuthZ/rate/quota/audit guard。
- `agent_id` / `created_by` 仍是可控入参事实；tenant 字段不存在。
- `task/node/update` 仍裸状态写。
- DAG create 仍按 `dag_key` upsert，可覆盖既有 DAG。

**仲裁：** 文档安全设计可继续收敛，但实现前必须以 Phase0/P3/P6 hard gate 拦住；不得把 P23 文档修订视为安全闭环。

---

### B7. L6 metrics / scheduled audit fallback 未落可执行 artifact

**裁定：CONFIRM / BLOCK for P7–P13**

事实：

- 当前无 promhttp `/metrics` exporter。
- 无 executable scheduled-audit fallback artifact。
- COMPLIANCE 自己要求 P7 前二选一，否则 P7–P13 freeze。

**必须修：** 落一个可执行观测入口：promhttp exporter 或脚本/命令卡 fallback，包含固定命令、输入源、输出 artifact、频率、退出码、owner。

---

### B8. 根 README 存在 merge conflict marker

**裁定：CONFIRM / BLOCK for repo hygiene**

Agent D 指出根 `README.md` 仍有 `<<<<<<< / ======= / >>>>>>>`。虽非 P23 设计主体，但它是终审必读入口之一，会影响文档索引与审查可信度。

**必须修：** 单独清理根 README 冲突标记；不混入 P23 逻辑修改。

---

## 4. NEEDS-FIX 仲裁清单

以下不单独构成最终 BLOCK，但应随下一轮修订处理：

1. P3/P6 external idempotency scope：P3 unique key 不含 `caller_id`，P6 matrix 要 caller-scoped。应统一为 `(tenant_id, caller_id, dag_key, trigger_source, trigger_instance_key)` 或明确 `trigger_instance_key = hash(caller_id, client_key)`。
2. P3/P5 tenant DDL 草案仍有 `tenant_id TEXT NOT NULL DEFAULT ''`，需要同步 strict DAG 禁空、backfill 后 CHECK、空 tenant fail-closed。
3. P4 “trigger 默认 auto” 文案需区分 `schedule.trigger=auto` 与 runtime `trigger_source=host`。
4. P5 no-transaction cron index 与主 DDL 同号 `0067` 口径需明确 `0067a/0067b` 或单独编号。
5. P6 audit spool 需要补 `status` enum、`locked_by/locked_until`、max retry、poison/dead-letter、replay 幂等键。
6. P8/P12/P13 verdict enum 不一致：P8 `pass/reject/inconclusive`，P12 `pass/fail/verdict_lost/dissent/timeout`，P13 又有另一套描述。需冻结映射表。
7. P12/P13 FK/GC/retention hard contract 不如 P8 明确，需要补 archive-safe logical FK、orphan postcheck SQL、TTL/retention owner。
8. P12 `audit_chain_scope` 与 `chain_scope` 字段重复，应统一为 `chain_scope`。
9. P11 活表 partial index、P9 tenant/fence 复合索引需要明确 no-transaction / `CREATE INDEX CONCURRENTLY`。
10. P10 依赖摘要低估 P7–P13 schema freeze / extension slot 前置。
11. `growth_phase` vs `growth_capped` 命名漂移，统一为 DAG 级 `growth_capped`。
12. P10 display_state 需冻结 `verify_phase=repairing` 与 `output_validation_phase=repairing` 的映射优先级。
13. P0/P2 terminal event unique key 字段名 `turn_id` vs `active_turn_id` 应统一。
14. P2 hook tap 需明确覆盖 completed / failed / interrupted / timeout synthetic event，而不仅 `OnTurnCompleted`。
15. 旧 `hook_consumer.go:96-220` 行锚仍需替换为真实 terminal/progress 入口锚点。
16. COMPLIANCE 中实施成本/工程日列如需避免排期承诺，应删除或标记非承诺估算。
17. Stub 中仍有测试函数名/局部 checklist，和“COMPLIANCE 是唯一 gate 权威”存在轻微第二事实源风险。

---

## 5. 下一步裁决

不得进入 P0/P1/P2/P3/P7–P13 实施。下一步只允许做 **Phase0 Gate + R4 文档修订**：

### Phase0 Gate PR（代码/测试）

1. 修到 `go test ./internal/archtest/... -count=1` 可作为红绿门。
2. 收紧 `cmd/mcp-orch` dependency allowlist，去掉宽泛 `internal/store` / `internal/module`。
3. 落 P23 21 项 archtest skeleton。
4. 落 P23 migration sequence guard。
5. 新增 P23 CI workflow 或 PR template/manual fallback 载体。
6. 清理根 README conflict markers。

### R4 文档小修

1. 冻结 lease subject。
2. 冻结 `observe_lost` retry 语义。
3. 统一 actor 路径、`0065` owner、P0 runtime disabled until P1。
4. 修 trigger/idempotency/audit spool/verdict enum/FK-GC/no-tx index/display state 等 NEEDS-FIX。

---

## 6. 最终仲裁

**P23 R3/R3B 终审不通过。**

当前状态：**BLOCK**。  
阻塞性质：不是单纯文档措辞，而是 gate 不可执行、代码事实与文档契约冲突、runtime lease / observe_lost / write-set 仍未冻结。  
放行条件：Phase0 gate PR 可执行 + R4 小修复审无 BLOCK。
