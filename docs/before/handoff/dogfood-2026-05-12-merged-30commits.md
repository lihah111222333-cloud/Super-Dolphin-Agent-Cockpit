# Dogfood Handoff — 2026-05-12 第二轮 merged 30+ commits 自审

> 范围：F1.2 / F2.2 / F4.2 / F7.1 第二轮并行 worktree 合并后用 DAG 自跑审查。
> 主线 worktree：`main` HEAD `6f333dd1`，含前置 F1.5 / F4.1 / F6.3（2026-05-11 第一轮）+ 本轮四 task。
> 目的：给下一轮 reviewer 留下 baseline + 真实部署阻塞证据。

---

## 1. review-dag-74（第一轮自审，未关闭）

- 创建时间：2026-05-12 早，merge `877193cf` / `4dd5307a` / `d63a623d` / `94502cec` 进 main 之后。
- 节点：n1..n6，覆盖 F1.2 / F2.2 / F4.2 / F7.1 各一节点 + n5/n6 跨任务交叉。
- 结果：DAG 本身 promote 走了 n1→n4，**n6 stuck pending 不进 ready / 不进 running**。
- 关键节点状态：
  - n1..n4 done，时间戳贴近合并时刻。
  - n5 done，但 n5→n6 之间 `PromoteSingleNodePendingToReady` 未触发：n6 始终 pending。
- 揭示真实部署阻塞：**mcp-orch 服务在跑旧二进制**——F6.3 SQL `PromoteSingleNodePendingToReady` 是本周新增（merge `7f51b91e`），运行中的进程未重启，仍是旧 `CompleteNode` 不带 promote 子句的版本。
- 行动：杀掉旧 `mcp-orch` 进程 + 重启拉新二进制。Hot path 验证 step 见下文 verify-dag-75。

## 2. verify-dag-75（单点闭环验证）

- 设计目的：剥离干扰，最小 DAG 单独验证 F6.3 promote 路径在线生效。
- 拓扑：A → B（仅两节点，无 fan-out）。
- 流程：A 直接置 done（用 task_update_node_status manual 推），观察 B。
- 关键时间戳：
  - A.status done 写入 T0。
  - B.status pending → ready 同毫秒（T0+0ms），来自 store 同事务 promote。
- 结论：F6.3 在新二进制下行为 = 预期。可以重做 review-dag-76。

## 3. review-dag-76（第二轮自审，闭环）

- 重新基于 review-dag-74 的 ops 蓝图发起。
- 6 节点全过：n1..n6 全 done，**run.status=succeeded**。
- 关键时间戳序列（相对 run start T0）：
  - T0+0s    run created (status=ready)，n1/n4 root → ready
  - T0+2s    n1 done，n2 promote pending→ready 同事务（F6.3）
  - T0+3s    n2 done，n3 promote pending→ready
  - T0+5s    n3 done，n5 promote pending→ready
  - T0+6s    n4 done，n5 已是 ready（无操作）
  - T0+8s    n5 done，n6 promote pending→ready
  - T0+10s   n6 done，run finalize 触发 → status=succeeded（F6.2）
- 节点 outputs：F1.2 inputs 注入 + F2.2 outputs 写 sharedfile 行为可见，sharedfile 内容正确。
- ops 路径：n4 中途用 task_dag_apply_ops update_node 改了 prompt（F4.2 update_node 真业务），同批 add+update 串行成功。
- 结论：F6.2 + F6.3 真生效；F1.2 / F2.2 / F4.2 / F7.1 端到端无 regression。

---

## 4. 揭示的真实部署阻塞（重要！）

**mcp-orch 服务跑旧二进制是反复出现的隐藏故障源**。dogfood 没有这一步则 review-dag-74 的 n6 stuck pending 会被误判为 F6.3 实装缺陷。

**建议**给 reviewer 加一条 standard preflight：
```bash
# 审查任何含 F6.x 的 DAG 行为前
pgrep -af mcp-orch  # 确认进程的启动时间在最新合并 commit 之后
# 如不确定就 kill + 重启
```

把"重启 mcp-orch"列为 reviewer checklist 的硬步骤，等同于"跑 migrations" 一类操作。

---

## 5. 下一轮 reviewer baseline

- main HEAD（本轮收尾后）：`6f333dd1` + docs-sync 数 commit。
- F 阶段 done：**16 / 37**（详 `docs/plans/dag改造实施计划.md` §0 + §3）。
- ADR 状态新基线：ADR-008 / 009 / 011v1 / 012 Accepted；ADR-006 Deferred to F1.3；ADR-005 / 010 / 011v2 仍 Proposed。
- 已知 follow-up：
  - F4.3 remove_node 真业务（待开工）。
  - F4.5 status=running 下 add_node 约束（待开工）。
  - F1.3 outputs enforce + ADR-006 size_cap 同步落地（待开工）。
  - F3.1 HybridExecutor v1（占位，未开工）。
- 不变量守护：archtest TestInterfaceIsolationBudgets / dag_designer_prompt_seed_test 全过。

## 6. 验证命令清单

```bash
# 本轮交付的全测命令（在 worktree / main 上跑）
go build ./...
go test ./internal/archtest/... -count=1
scripts/test_with_guard.sh --guard-only
# DAG dogfood（需 PG + mcp-orch 已重启）
mcp/task_create_dag → task_start_dag → task_get_dag 周期轮询
```
