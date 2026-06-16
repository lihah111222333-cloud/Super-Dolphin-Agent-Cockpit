# T04 失败终态一致性：task_update_node failed 与 wakeup permanent fail 原子级联

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**J/K**

建议实现 worktree：`.worktrees/mcp-orch-dag-t04`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`internal/sidecar/orch/orchestration/dag.go, wakeup_dispatcher.go, retry_strategy.go, internal/sidecar/orch/store/taskdag/store_fail_downstream.go, SQL/tests`

## 必须满足的验收标准

- `task_update_node status=failed` 对合法 failed 转移必须走 `FailNodeAndCancelDownstream`/等价事务路径，级联取消下游并 finalize run；不能只更新单节点。
- 保持状态机边界：`pending -> failed` 仍应被拒绝，测试只覆盖 running/retrying/waiting_human 等合法 failed 入口。
- wakeup 永久失败与 DAG node fail/cascade 不能出现 wakeup=failed 但 DAG node/run 未级联的裂脑；实现同事务、顺序补偿，或持久 repair 记录。
- 失败路径错误必须可观测，不能只 log 后丢失。

## 明确非目标

- 不要处理 turn.completed 成功回写失败；T05 负责。

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
