package nodeexec

import "fmt"

// 节点状态转移合法性 —— 蓝图 v2 §8 状态机 + 实施计划 S7.1。
// ValidateTransition 是 service.UpdateNodeStatus (S7.2) 的前置校验：
// 防止 task_update_node 把节点直接从 pending 跳到 done 之类的非法转移。
// 这条规则由 ADR docs/adr/0001-dag-v2-contracts.md (S16.1) 固化。

// transition 是 (from, to) 状态对，作为 legalTransitions map 的 key。
type transition struct {
	From NodeStatus
	To   NodeStatus
}

// legalTransitions 列出所有合法的 NodeStatus 转移（11 条）。
// 终态 done / failed / cancelled / skipped 没有 outgoing 转移（修改终态节点
// 必须经 fork 或 reset，而不是直接改 status）。
//
// 入态条件见蓝图 v2 §8：
//   - pending → ready                upstream 全 done，dispatcher 准备 pick
//   - pending → cancelled            上游 fail_fast 级联取消
//   - ready → running                dispatcher pick
//   - ready → cancelled              上游 fail_fast 级联（含 ready 状态的节点）
//   - running → done                 success
//   - running → failed               fail + no retries / hard fail / timeout
//   - running → retrying             fail + retries left
//   - running → cancelled            用户终止当前 run
//   - retrying → ready               退避结束，重新入队
//   - retrying → failed              放弃重试
//   - retrying → cancelled           上游 fail_fast 级联且当前节点正在退避
//     （避免强制转 failed 误罪待重试节点）
//
// skipped / waiting_human / awaiting_verify 是 reserved 或 legacy 状态；runtime
// 闭环前不再作为新的合法迁移目标。
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
