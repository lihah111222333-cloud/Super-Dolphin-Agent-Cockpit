# MCP-Orch DAG 当前裁决修复任务拆分（2026-06-05）

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

集成 worktree：`/Users/ai/Desktop/Super-Dolphin/.worktrees/integration/mcp-orch-dag-current-fixes-20260605`

## 执行约束

- 10 个任务按文件/事故域拆分，默认并行执行；每个任务一个独立 worktree + 一个实现 agent。
- 每个实现 worktree 完成后，必须由两个独立评审 agent 根据对应任务文档评审：
  1. 完成度/裁决符合度评审；
  2. 代码质量/最小变更/回归测试评审。
- 两个评审都通过后，才允许在任务 worktree commit，并由主控合并到集成 worktree。
- 任一评审未通过，不允许 commit/merge；新开修复 agent 在同一任务 worktree 或新的 attempt worktree 中只修评审指出的问题，然后重新双评审。
- 每个 Go 文件修改后先运行 `./scripts/test_with_guard.sh <file.go>`；最终按改动面运行包级 `./scripts/test_with_guard.sh <affected packages> -count=1`。
- 前端任务先跑聚焦测试；合入前至少跑 `cd frontend-app && npm run lint && npm test`，需要构建面变更时再跑 `npm run build`。
- 严禁 `git add .`；只 stage 本任务拥有的文件。不得回滚或格式化无关改动。
- 遇到需求跨出任务边界，先在 agent 报告里说明，不自行扩大范围。

## 任务总表

| 任务 | 文档 | 覆盖 finding | 主题 |
|---|---|---|---|
| T01 | [`01-create-dag-contract.md`](./01-create-dag-contract.md) | R/A/B/C/Q | 创建入口契约硬化：可信身份、schedule、OCC/拓扑、command_ref |
| T02 | [`02-assignee-exec-runtime-guards.md`](./02-assignee-exec-runtime-guards.md) | D/E/F | 执行者/执行配置硬防护：assigned_to、root/downstream 入队、exec 校验 |
| T03 | [`03-scheduler-timezone-error-isolation.md`](./03-scheduler-timezone-error-isolation.md) | H/I | 调度器语义：时区契约与同 tick 错误隔离 |
| T04 | [`04-failure-cascade-atomicity.md`](./04-failure-cascade-atomicity.md) | J/K | 失败终态一致性：task_update_node failed 与 wakeup permanent fail 原子级联 |
| T05 | [`05-turn-completed-durable-retry.md`](./05-turn-completed-durable-retry.md) | M | 完成回写可靠性：turn.completed 推 done 失败要持久重试/可修复 |
| T06 | [`06-sharedfile-freshness-ownership.md`](./06-sharedfile-freshness-ownership.md) | N | 共享文件 freshness/ownership：固定路径 recurring run 不能用旧文件伪装本轮成功 |
| T07 | [`07-output-envelope-upstream-context.md`](./07-output-envelope-upstream-context.md) | O/P | 产物传递契约：automation sharedfile envelope 与 downstream upstream context |
| T08 | [`08-local-standalone-launcher-contract.md`](./08-local-standalone-launcher-contract.md) | L | local/standalone DAG agent launcher 契约：明确禁止或补齐兼容实现 |
| T09 | [`09-workflow-ui-recovery-schema.md`](./09-workflow-ui-recovery-schema.md) | G/S/H(frontend) | Workflow UI 最低可用：恢复入口、blocked 可见、真实 schema 编辑、时区输入 |
| T10 | [`10-dag-designer-prompt-contract.md`](./10-dag-designer-prompt-contract.md) | T/S(prompt side) | AI DAG designer authoring contract：assigned_to、真实 schema、恢复工具、running 编辑口径 |

## 合入顺序建议

1. 先合后端基础契约：T01/T02/T03/T04/T08。
2. 再合执行回写与产物链：T05/T06/T07。
3. 最后合用户入口：T09/T10。

该顺序只是降低冲突的集成建议；实现阶段仍按 10 路并行推进。
