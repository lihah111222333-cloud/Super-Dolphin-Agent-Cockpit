package nodeexec

import (
	"context"
	"encoding/json"
	"testing"
)

// TestNodeStatusConstants_NineStates: 蓝图 v2 §8 声明 9 态，骨架阶段必须齐全。
func TestNodeStatusConstants_NineStates(t *testing.T) {
	t.Parallel()
	all := []NodeStatus{
		NodeStatusPending,
		NodeStatusReady,
		NodeStatusRunning,
		NodeStatusRetrying,
		NodeStatusDone,
		NodeStatusFailed,
		NodeStatusCancelled,
		NodeStatusSkipped,
		NodeStatusWaitingHuman,
	}
	if got, want := len(all), 9; got != want {
		t.Fatalf("NodeStatus count = %d, want %d", got, want)
	}
	seen := make(map[NodeStatus]bool, len(all))
	for _, s := range all {
		if s == "" {
			t.Errorf("empty NodeStatus constant detected")
		}
		if seen[s] {
			t.Errorf("duplicate NodeStatus value: %q", s)
		}
		seen[s] = true
	}
}

func TestPersistedNodeStatuses_CurrentRuntimeContract(t *testing.T) {
	t.Parallel()
	want := []NodeStatus{
		NodeStatusPending,
		NodeStatusReady,
		NodeStatusRunning,
		NodeStatusRetrying,
		NodeStatusDone,
		NodeStatusFailed,
		NodeStatusCancelled,
		NodeStatusSkipped,
		NodeStatusWaitingHuman,
	}
	got := persistedNodeStatuses()
	if len(got) != len(want) {
		t.Fatalf("persisted node status count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("persisted node status[%d] = %q, want %q", i, got[i], want[i])
		}
		if !isPersistedNodeStatus(string(want[i])) {
			t.Fatalf("isPersistedNodeStatus(%q) = false, want true", want[i])
		}
	}
}

func TestPersistedNodeStatuses_RejectDerivedDisplayStates(t *testing.T) {
	t.Parallel()
	for _, status := range []string{
		"waiting_for_assignee",
		"waiting_timer",
		"blocked_by_policy",
		"awaiting_review",
		"awaiting_acceptance",
		"awaiting_verify",
	} {
		if isPersistedNodeStatus(status) {
			t.Fatalf("isPersistedNodeStatus(%q) = true, want false", status)
		}
	}
}

func TestReservedNodeStatuses_AreMarkedReservedOrLegacy(t *testing.T) {
	t.Parallel()
	for _, status := range []string{
		string(NodeStatusSkipped),
		string(NodeStatusWaitingHuman),
		"awaiting_verify",
	} {
		if !isReservedOrLegacyNodeStatus(status) {
			t.Fatalf("isReservedOrLegacyNodeStatus(%q) = false, want true", status)
		}
	}
}

// TestFailureClassConstants_SevenClasses: 蓝图 v2 §8 七类失败。
func TestFailureClassConstants_SevenClasses(t *testing.T) {
	t.Parallel()
	all := []FailureClass{
		FailureClassTransient,
		FailureClassQuota,
		FailureClassValidation,
		FailureClassCapability,
		FailureClassHard,
		FailureClassNeedsHuman,
		FailureClassInfrastructure,
	}
	if got, want := len(all), 7; got != want {
		t.Fatalf("FailureClass count = %d, want %d", got, want)
	}
	seen := make(map[FailureClass]bool, len(all))
	for _, c := range all {
		if c == "" {
			t.Errorf("empty FailureClass constant detected")
		}
		if seen[c] {
			t.Errorf("duplicate FailureClass value: %q", c)
		}
		seen[c] = true
	}
}

// TestOnFailureStrategyConstants_SevenStrategies: 蓝图 v2 §7 注释列出的 7 项策略。
func TestOnFailureStrategyConstants_SevenStrategies(t *testing.T) {
	t.Parallel()
	all := []OnFailureStrategy{
		OnFailureRetry,
		OnFailureEscalateModel,
		OnFailureAppendError,
		OnFailureReplan,
		OnFailureSkip,
		OnFailureFailFast,
		OnFailureAskHuman,
	}
	if got, want := len(all), 7; got != want {
		t.Fatalf("OnFailureStrategy count = %d, want %d", got, want)
	}
}

// TestHookPointConstants_FourPoints: 蓝图 v2 §10 补丁 10 列出的 4 个 hook 点。
func TestHookPointConstants_FourPoints(t *testing.T) {
	t.Parallel()
	all := []HookPoint{
		HookBeforeExecute,
		HookAfterExecute,
		HookOnStateChange,
		HookOnFailure,
	}
	if got, want := len(all), 4; got != want {
		t.Fatalf("HookPoint count = %d, want %d", got, want)
	}
}

// TestNodeOutcome_FieldRoundTrip: NodeOutcome 字段读写与 zero value 行为。
func TestNodeOutcome_FieldRoundTrip(t *testing.T) {
	t.Parallel()
	o := NodeOutcome{
		Status:       NodeStatusDone,
		Result:       json.RawMessage(`{"summary":"ok"}`),
		FailureClass: "",
		ErrorSummary: "",
		RetryHint:    nil,
	}
	if o.Status != NodeStatusDone {
		t.Fatalf("Status round-trip failed: got %q", o.Status)
	}
	if string(o.Result) != `{"summary":"ok"}` {
		t.Fatalf("Result round-trip failed: got %s", o.Result)
	}
	if o.RetryHint != nil {
		t.Fatalf("zero RetryHint should be nil")
	}
}

// stubExecutor 验证 NodeExecutor 接口可实现（骨架阶段三个 executor stub 都长这样）。
type stubExecutor struct{}

func (stubExecutor) Execute(_ context.Context, _ Node, _ RunContext) (NodeOutcome, error) {
	return NodeOutcome{Status: NodeStatusDone}, nil
}

func (stubExecutor) Hooks() map[HookPoint]HookHandler { return nil }

// TestNodeExecutorInterface_Implementable: 编译时验证接口形状稳定。
func TestNodeExecutorInterface_Implementable(t *testing.T) {
	t.Parallel()
	var _ NodeExecutor = stubExecutor{}

	// 走一次 happy path，确认 NodeOutcome zero value 不 panic。
	exec := stubExecutor{}
	out, err := exec.Execute(context.Background(), Node{}, RunContext{})
	if err != nil {
		t.Fatalf("stub Execute returned error: %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("stub Execute Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if hooks := exec.Hooks(); hooks != nil {
		t.Fatalf("stub Hooks() should return nil")
	}
}
