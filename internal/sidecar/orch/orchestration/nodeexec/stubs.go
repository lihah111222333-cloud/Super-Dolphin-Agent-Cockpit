package nodeexec

import "context"

// 节点执行器三种 stub —— 蓝图 v2 §10 骨架补丁 1 / 实施计划 S1.3+S1.4+S1.5。
// 三个 stub 合并在同一文件以遵守 orchestration 包文件数上限（≤ 30）。
// 真实实现按 node_type 分别在 F1/F2/F3 落地：
//   - AgentExecutor  → F1.1（已落地，见 executor_agent.go）：解码 node.config.exec →
//     orchestration_launch_agent 拉子 agent；本文件不再保留 AgentExecutor stub。
//   - AutomationExecutor → F2.1：解码 command_ref → command_get + 执行确定性任务
//   - HybridExecutor → F3.1：先 automation 后 agent verifier（高风险操作）

// HybridExecutor 是 node_type=hybrid 节点的执行器 stub
// （真实实现先跑 automation，再用 agent verifier 验证输出）。
type HybridExecutor struct{}

// Execute 执行编排。
func (HybridExecutor) Execute(_ context.Context, _ Node, _ RunContext) (NodeOutcome, error) {
	return NodeOutcome{Status: NodeStatusDone}, nil
}

// Hooks 处理hooks。
func (HybridExecutor) Hooks() map[HookPoint]HookHandler { return nil }
