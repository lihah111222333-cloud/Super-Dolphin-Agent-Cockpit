# T09 Workflow UI 最低可用：恢复入口、blocked 可见、真实 schema 编辑、时区输入

来源裁决：`/Users/ai/Desktop/Super-Dolphin/.worktrees/mcp-orch-dag-current-findings-20260605/docs/cc/mcp-orch-dag-current-findings-20260605.md`

覆盖 finding：**G/S/H(frontend)**

建议实现 worktree：`.worktrees/mcp-orch-dag-t09`

## 目标

把最终裁决中本任务覆盖的缺陷修成源码级硬防护/可观测路径，不能只改文案或测试。实现应尽量小、可验证，并保留 fail-fast 语义。

## 建议关注文件/区域

`frontend-app/src/pages/workflows/WorkflowPage.jsx, frontend-app/src/shared/api/backendApi.js, internal/module/dashboard/rpc.go/detail.go, internal/contract/orchestration.go tests as needed`

## 必须满足的验收标准

- Workflow 详情/运行页必须展示 ready/no-wakeup/waiting_for_assignee/blocked/failed 的具体诊断，不能只显示 running。
- 接入用户可执行恢复入口：当后端诊断要求 dispatch/assign 时，UI 要提供设置执行者/调用 dispatch 的按钮或流程；如果缺 dashboard RPC，需要新增窄 RPC。
- 步骤高级设置读写后端真实 schema：`config.exec.provider/model/agent_key/prompt_key/cwd`、`outputs.to_sharedfile`、`outputs.to_node_result`；覆盖 agent/automation/hybrid，而非只编辑 agent 旧顶层字段。
- 保存前做与后端一致的最小 schema 校验；错误 fail-fast 展示。
- UI cron/time 输入要与 T03 的后端时区契约一致（例如生成 `CRON_TZ=Asia/Shanghai` 或显式 timezone 字段），避免本地时间被当 UTC。
- 前端测试覆盖 schema 读写、blocked/dispatch UI、时区生成。

## 明确非目标

- 不要改 AI DAG designer prompt；T10 负责。
- 尽量不要改 scheduler 后端时区算法；T03 负责。

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
