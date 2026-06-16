# T10 AI DAG designer authoring contract：assigned_to、真实 schema、恢复工具、running 编辑口径

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**T/S(prompt side)**

建议实现 worktree：`.worktrees/mcp-orch-dag-t10`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`migrations/0084_seed_dag_designer_prompt_zh.sql, internal/module/thread/router_resolve_recall_test.go, prompt/template tests, frontend enabledTools constants if narrowly needed`

## 必须满足的验收标准

- 中文 DAG designer prompt/examples 必须把 `assigned_to` 作为可运行 DAG 的必填语义讲清，并说明它与 wakeup 入队的关系。
- 示例必须使用后端真实 schema：`config.exec.*`、`outputs.to_sharedfile`/`outputs.to_node_result`、`first_turn` 顶层有效；不能继续教旧顶层 provider/model/output_file/cwd。
- prompt 要明确时区契约（与 T03/T09 对齐）和 scheduled 创建的真实路径。
- prompt/工具集要包含或引导 `task_dispatch_node`/恢复动作；不能让诊断建议的恢复工具在 AI 设计入口不可用。
- 修正 running DAG `apply_ops add_node` 说明漂移：当前 running/active run 下 node ops 被拒绝，prompt 不得教不可执行路径。
- 测试锁定 prompt 注入、enabledTools、关键文本/示例 JSON。

## 明确非目标

- 不要改 WorkflowPage UI；T09 负责。

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
