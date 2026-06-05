# T02 执行者/执行配置硬防护：assigned_to、root/downstream 入队、exec 校验

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**D/E/F**

建议实现 worktree：`.worktrees/mcp-orch-dag-t02`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`cmd/mcp-orch/orchestration/dag_query.go, cmd/mcp-orch/orchestration/nodeexec/ops.go, cmd/mcp-orch/tools/task_apply_ops.go, cmd/mcp-orch/tools/task_schemas.go(apply_ops区域), cmd/mcp-orch/store/taskdag/store_root_wakeup.go, store_complete_downstream.go, cmd/mcp-orch/orchestration/scheduledstart, nodeexec config tests`

## 必须满足的验收标准

- `task_dag_apply_ops add_node` 可原子设置 `assigned_to`，schema、typed op、persist 路径一致；未知关键字段不能被静默忽略。
- root/downstream promote 到 ready 时，如果缺 `assigned_to`，必须写入可诊断 blocked/waiting 事件或状态，不能让 run 长期 `running` 且 wakeup=0 无解释。
- scheduled start 对 ready root 但 scheduled wakeups=0 的情形不能无声推进：必须 fail-fast、写 run/node event，或给出可被 UI/API 读取的 blocked diagnostic。
- 自动 root/downstream 入队前要校验 agent 执行配置（至少 `config.exec.cwd` 与 `agent_key/prompt_key` 类必需项），与手动 dispatch 的硬校验保持一致。
- 新增/更新回归测试覆盖：空 assignee、缺 cwd、downstream 缺 assignee、apply_ops add_node assigned_to。

## 明确非目标

- 不要改 AI prompt；由 T10 负责。
- 不要改 Workflow UI 的恢复按钮；由 T09 负责。

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
