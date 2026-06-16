# T07 产物传递契约：automation sharedfile envelope 与 downstream upstream context

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**O/P**

建议实现 worktree：`.worktrees/mcp-orch-dag-t07`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go, node_router.go, wakeup_dispatcher.go, store/taskdag/store_complete_downstream.go, tests`

## 必须满足的验收标准

- automation 节点写 sharedfile-only 输出时，也必须在 node result 中写入机器可读 envelope（至少 path/kind/dag/run/node），除非用户明确禁用。
- 下游 `inputs.from_nodes` 读取该 envelope 能拿到 sharedfile path，不再只能得到 `{}`。
- 对 `DownstreamWakeupPayload.UpstreamOutputs` 做出一个真实可达选择：router 主路径消费并注入/映射，或删除/废弃该隐式契约并用显式 inputs 替代；不能继续让 legacy prompt hint 在主路径不可达。
- 测试覆盖 automation sharedfile-only -> downstream agent/automation 可读取路径。

## 明确非目标

- 不要改 sharedfile overwrite semantics；T06 负责。

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
