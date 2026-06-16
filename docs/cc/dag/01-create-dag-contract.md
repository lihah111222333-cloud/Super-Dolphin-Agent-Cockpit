# T01 创建入口契约硬化：可信身份、schedule、OCC/拓扑、command_ref

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**R/A/B/C/Q**

建议实现 worktree：`.worktrees/mcp-orch-dag-t01`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`internal/sidecar/orch/tools, internal/sidecar/orch/orchestration/dag.go, internal/sidecar/orch/sql/queries/task_dag_*.sql, internal/sidecar/orch/store/taskdag, internal/contract/orchestration.go tests`

## 必须满足的验收标准

- `task_create_dag` 不再要求模型猜公共 `agent_id`：优先使用可信 ToolScope `_agentId`；若仍保留公共字段，必须与可信 scope 一致，不一致 fail-fast。
- `schedule.trigger=scheduled` 不能只写 metadata 后返回成功：要么写入真实调度列并计算 `next_run_at`，要么明确 fail-fast 并给出必须走 `task_dag_apply_ops` 的错误；不可继续静默成功。
- 创建同名 DAG 不能绕过 ApplyOps OCC/version/running guard 静默覆盖；实现 create-only、显式 replace with version，或复用 ApplyOps 保护。
- 创建路径必须校验重复 node_key、未知 depends_on、环形依赖；重复 node_key 不能后写覆盖。
- 顶层 `command_ref` 且 `node_type` 为空时不能默认成不可执行 agent：fail-fast 或显式推断 automation，并有测试锁定。

## 明确非目标

- 不要修改 WorkflowPage；前端提示与恢复入口由 T09 负责。
- 不要实现 assigned_to apply_ops；由 T02 负责。

## 实现流程要求

1. 先用当前源码复核裁决证据，记录实际修改路径。
2. 先补回归测试或最小 failing fixture，再改实现。若无法先写红测，在报告中说明原因。
3. 每改完 Go 文件，运行 `./scripts/test_with_guard.sh <file.go>`；前端文件运行对应聚焦测试/静态检查。
4. 完成后自查 diff：无 unrelated refactor、无 generated/debug/secret、本任务外文件改动有明确理由。
5. 实现 agent 最终报告必须包含：改动文件、关键行为变化、验证命令与结果、剩余风险。

## 双评审通过标准

### 评审 A：完成度/裁决符合度

- 对照本文件“必须满足的验收标准”逐条判断 PASS/FAIL。
- 确认没有把最终裁决中的真阳性降级为“只提示/只文档”。
- 确认未跨任务边界抢改其它任务的核心范围。

### 评审 B：代码质量/测试/最小变更

- 检查 diff 是否外科化、符合现有风格、无兜底/吞错/静默降级。
- 检查回归测试是否能锁住本任务 bug，验证命令是否与改动面匹配。
- 检查是否引入新依赖、宽泛接口或不必要配置；若有，必须有代码级必要性。

只有 A/B 都明确给出 PASS，主控才可以 commit 并合并到集成 worktree。
