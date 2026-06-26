package nodeexec

import "fmt"

// 本文件维护节点状态转换白名单。
// service.UpdateNodeStatus 和 DAG 调度路径都依赖这里 fail-fast 拦截非法状态跳转，
// 避免调用方绕过 dispatcher 直接把节点从 pending 改成终态。

// transition 是 (from, to) 状态对，作为 legalTransitions map 的 key。
type transition struct {
	From NodeStatus
	To   NodeStatus
}

// legalTransitions 列出当前 runtime 允许的 NodeStatus 转移。
// 终态 done / failed / cancelled / skipped 没有 outgoing 转移（修改终态节点
// 必须经 fork 或 reset，而不是直接改 status）。
//
// 入态条件：
//   - pending → ready：上游完成，dispatcher 可以领取。
//   - pending/ready/retrying → cancelled：上游 fail_fast 或用户取消级联。
//   - ready → running：executor 已被领取并启动。
//   - running → done/failed/retrying/cancelled：执行完成、失败、进入退避或被取消。
//   - retrying → ready/failed：退避结束重新入队，或超过尝试次数后终止。
//
// skipped / waiting_human / awaiting_verify 是兼容保留状态；当前 runtime 不再把它们作为新的转换目标。
var legalTransitions = map[transition]struct{}{
	{NodeStatusPending, NodeStatusReady}:      {},
	{NodeStatusPending, NodeStatusCancelled}:  {},
	{NodeStatusReady, NodeStatusRunning}:      {},
	{NodeStatusReady, NodeStatusCancelled}:    {},
	{NodeStatusRunning, NodeStatusDone}:       {},
	{NodeStatusRunning, NodeStatusFailed}:     {},
	{NodeStatusRunning, NodeStatusRetrying}:   {},
	{NodeStatusRunning, NodeStatusCancelled}:  {},
	{NodeStatusRetrying, NodeStatusReady}:     {},
	{NodeStatusRetrying, NodeStatusFailed}:    {},
	{NodeStatusRetrying, NodeStatusCancelled}: {},
}

// ValidateTransition 校验 from → to 是否是合法的 NodeStatus 转移。
//
// 同态（from == to）一律拒绝：上层应去重而不是依赖 idempotent semantics；
// 这样 dispatcher 死循环重发 update_node 等错误能被立刻发现而不是悄悄成功。
//
// 空字符串视为非法（防 typo）。
func ValidateTransition(from, to NodeStatus) error {
	if from == "" {
		return fmt.Errorf("transition: empty from status")
	}
	if to == "" {
		return fmt.Errorf("transition: empty to status")
	}
	if from == to {
		return fmt.Errorf("transition: same state %q (上层应去重；不允许 idempotent update)", from)
	}
	if _, ok := legalTransitions[transition{from, to}]; !ok {
		return fmt.Errorf("transition: %q → %q 非法 (见 nodeexec/status.go legalTransitions)", from, to)
	}
	return nil
}

// IsTerminal 判定 status 是否是终态（无 outgoing 转移）。
// 终态: done / failed / cancelled / skipped。
func IsTerminal(s NodeStatus) bool {
	switch s {
	case NodeStatusDone, NodeStatusFailed, NodeStatusCancelled, NodeStatusSkipped:
		return true
	}
	return false
}
