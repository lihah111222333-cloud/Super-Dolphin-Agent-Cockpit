# 第 56 轮审查结论

## 审查范围

- `cmd/mcp-orch/orchestration/dag_query.go`（GetRun、ListRuns、TerminateDAG、terminableRun、stopSpawnedAgentThreads、partitionOps、appendPartitionedOp、rememberNodeOp、runOpsBatch、preflightOpsBatch、planOpsBatch、persistOpsBatch、enforceRunningDAGInvariants、rejectTerminalDAGOps、validateDAGPatch、mergeAdjacency、mergeNodePatch 等 ~50 个函数）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `dag_query.go:56-73` TerminateDAG | 静默 | line 58 `run == nil` 时 `return err`（err 也是 nil）→ 静默成功 | `terminableRun` 返 `(dagKey, runKey, nil, nil)` 当 run 状态非 running/cancelled 时（line 103）。TerminateDAG 静默返 nil——caller 不知道 terminate 未执行 | 返回 `ErrRunNotTerminable` 让 caller 知道 |
| `dag_query.go:64-68` TerminateDAG | 静默 | `TerminateRun` 返 NotFound 时，再 GetRun 检查是否已非 running → 如果已非 running 静默返 nil | 合理的幂等性设计（已终止的 run 不报错）——但如果 GetRun 也失败（DB 不可达），走 line 70 返 error | OK（幂等性正面案例） |
| `dag_query.go:107-122` stopSpawnedAgentThreads | 弱契约 | 串行 stop 所有 spawned agent；任一失败收集到 stopErrs | 串行 stop 在 agent 多时慢；但 errors.Join 收集所有错误是良好实践 | 考虑并行 stop（但需评估 DB 压力） |
| `dag_query.go:204-215` rememberNodeOp | 静默 | `key == ""` 时 `return nil`（line 206-208）| 空 node_key 是 caller bug（应在 PlanAddNodes 等处校验），但此处静默放行 | 改为 return error |
| `dag_query.go:219-239` runOpsBatch | 正面案例 | OCC（Optimistic Concurrency Control）+ 事务内 preflight + plan + persist + bump version | 这是 DAG 操作的核心事务——**正面案例**：OCC 防止并发冲突 | 维持 |
| `dag_query.go:302-316` enforceRunningDAGInvariants | 正面案例 | running DAG 拒绝所有 mutable ops | F4.5 不变量的 fail-fast 实现——**正面案例** | 维持 |
| `dag_query.go:318-325` rejectTerminalDAGOps | 正面案例 | terminal DAG 拒绝 apply_ops | 终态 DAG 不可修改——**正面案例** | 维持 |
| `dag_query.go:157-169` partitionOps | 正面案例 | nil op → fail-fast error（line 161-163）| 空 op 直接拒绝——**正面案例** | 维持 |
| `dag_query.go:171-197` appendPartitionedOp | 正面案例 | `default:` 分支返 `ErrLifecycleNotImplemented`（line 194）| 未知 op kind 直接拒绝——**正面案例** | 维持 |
| `dag_query.go:48` ListRuns | 正面案例 | `shared.ClampLimit(int(req.Limit), 1, 200, 50)` 硬上限 200 | 与第38轮 SQL 层无硬上限形成对比——service 层加了上限——**正面案例** | 维持 |
| `dag_query.go:588` nextRunAtForFinalSchedule | 静默 | `dagPatchCronParser.Parse` 失败时 `return nil`（line 588-589）| cron 表达式在 validateDAGPatch 已校验过（line 431-433），此处失败理论上不可达 | 改为 panic（不可达分支）或 Warn |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `dag_query.go:219-239` runOpsBatch | 事务内多步操作（preflight + plan + persist + bump）| 加 per-step duration；总 > 1s 打 Warn |
| `dag_query.go:107-122` stopSpawnedAgentThreads | 串行 stop 多个 agent | 加 per-agent stop duration；总 > 5s 打 Warn |
| `dag_query.go:243-277` preflightOpsBatch | 多次 DB 查询（GetDAGVersionForUpdate + GetDAG + CountRunningRuns + ListNodes + GetDAGSchedule）| 加 per-query duration |
| `dag_query.go:377-399` planOpsBatch | 纯内存操作（plan + merge + cycle detect）| 节点 < 1000 时 < 1ms；无需监控 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `dag_query.go:56-73` TerminateDAG | run 非 running/cancelled 时静默返 nil |
| `dag_query.go:204-208` rememberNodeOp | 空 key 静默 return nil |
| `dag_query.go:588-589` nextRunAtForFinalSchedule | parse 失败静默返 nil |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `dag_query.go:56-73` | TerminateDAG 的「静默成功」语义 |
| `dag_query.go:107-122` | 串行 stop |
| `dag_query.go:204-208` | 空 key 放行 |

## 修复优先级

### P0（无）
本轮无 P0 问题。`dag_query.go` 整体质量很高——OCC 事务、F4.5 不变量、partitionOps nil-check、unknown op kind fail-fast、ListRuns 硬上限 200 都是 fail-fast 正面案例。

### P1（本月）
1. `dag_query.go:56-73` TerminateDAG 非 running/cancelled 时返 `ErrRunNotTerminable`
2. `dag_query.go:204-208` rememberNodeOp 空 key 改 return error
3. `dag_query.go:588-589` nextRunAtForFinalSchedule 不可达分支改 panic

### P2（下个 sprint）
4. `dag_query.go:107-122` stopSpawnedAgentThreads 评估并行 stop
5. `dag_query.go:219-239` runOpsBatch 加 per-step duration 日志

## 边界条件

1. **`dag_query.go` 整体是项目内 fail-fast 实践的标杆文件**：
   - `partitionOps` nil op → error（line 161-163）
   - `appendPartitionedOp` unknown kind → `ErrLifecycleNotImplemented`（line 194）
   - `enforceRunningDAGInvariants` running DAG 拒绝 mutable ops（line 310-316）
   - `rejectTerminalDAGOps` terminal DAG 拒绝 apply_ops（line 318-325）
   - `preflightOpsBatch` OCC version check（line 248-249）
   - `ListRuns` ClampLimit 硬上限 200（line 48）
   - `rememberNodeOp` 同 node_key 重复 op → `ErrDuplicateOpForNode`（line 209-211）
   
   这些都是「先校验后执行」的良好实践。建议作为项目内 fail-fast 模板推广。

2. **`dag_query.go:219-239` runOpsBatch 的 OCC 设计**：`GetDAGVersionForUpdate`（SELECT FOR UPDATE）+ `BumpDAGVersion`（WHERE version = baseVersion）双重保护。即使并发 ApplyOps，只有一个能成功 bump version，其他返 `ErrVersionConflict`。这是分布式系统中 OCC 的标准实现。

3. **`dag_query.go:377-399` planOpsBatch 的三路 plan + merge + cycle detect**：add → update → merge → remove → DetectCycle 的流水线设计让每一步都基于前一步的输出。注释（line 366-376）详细解释了 adjacency 合并语义。这是复杂算法的良好文档化实践。

4. **`dag_query.go:56-73` TerminateDAG 的「静默成功」设计取舍**：当 run 状态非 running/cancelled 时（如 done/failed），`terminableRun` 返 `(dagKey, runKey, nil, nil)`。TerminateDAG 见 `run == nil && err == nil` 静默返 nil。这是「幂等性」设计——已终止的 run 再次 terminate 不报错。但 caller 无法区分「成功终止」和「已经终止无需操作」。建议返回 `TerminateDAGResponse{AlreadyTerminal: true}` 让 caller 可选择性处理。

5. **`dag_query.go:402-417` planDAGUpdates 的 cron 校验**：`dagPatchCronParser.Parse` 在 plan 阶段校验 cron 表达式合法性。这是「fail-fast at plan time」的良好实践——不让非法 cron 进入 DB。`robcron.NewParser` 配置了 5 字段 cron（Minute|Hour|Dom|Month|Dow|Descriptor），与标准 cron 兼容。

6. **`dag_query.go:622-638` persistUpdateChanges 的防御性检查**：line 625-631 检查 `old, ok := byKey[c.NodeKey]`——如果 PlanUpdateNodes 已校验存在但 persist 时消失，说明并发删除或 lost lock。注释明确说「在 OCC 锁下不应发生」但仍做防御。这是「belt and suspenders」的良好实践。

---

**本轮总结**：本轮无 P0 问题。`dag_query.go` 是项目内 fail-fast 实践的标杆文件——OCC 事务、F4.5 不变量、nil-check、unknown-kind reject、ClampLimit 硬上限、duplicate-op reject 全部到位。建议作为项目内 fail-fast 模板推广到其他模块。唯一改进点是 TerminateDAG 的「静默成功」语义应返回更明确的响应。

**累计进度**：56 轮完成。cron `fd4b4728` 继续推进。

---

## 第56轮后累计状态

- 已完成：第27-56轮，共30轮
- 累计 P0 问题：**62 个**（本轮无新增——dag_query.go 质量极高）
- 正面案例标杆文件：`dag_query.go`（OCC + F4.5 + partition + cycle detect）
