package nodeexec

import (
	"strings"
	"testing"
)

func TestValidateTransition_AllLegal(t *testing.T) {
	t.Parallel()
	legal := []struct {
		from, to NodeStatus
		desc     string
	}{
		{NodeStatusPending, NodeStatusReady, "deps done"},
		{NodeStatusPending, NodeStatusCancelled, "upstream fail_fast"},
		{NodeStatusReady, NodeStatusRunning, "dispatcher pick"},
		{NodeStatusReady, NodeStatusCancelled, "upstream fail_fast (ready)"},
		{NodeStatusRunning, NodeStatusDone, "success"},
		{NodeStatusRunning, NodeStatusFailed, "fail no retries"},
		{NodeStatusRunning, NodeStatusRetrying, "fail with retries"},
		{NodeStatusRunning, NodeStatusCancelled, "user cancelled run"},
		{NodeStatusRetrying, NodeStatusReady, "backoff over"},
		{NodeStatusRetrying, NodeStatusFailed, "give up"},
		{NodeStatusRetrying, NodeStatusCancelled, "upstream fail_fast while retrying"},
	}
	if got, want := len(legal), 11; got != want {
		t.Fatalf("legal transitions in test = %d, want %d (与 legalTransitions map 同步)", got, want)
	}
	for _, tc := range legal {
		if err := ValidateTransition(tc.from, tc.to); err != nil {
			t.Errorf("%q→%q (%s) should be legal, got: %v", tc.from, tc.to, tc.desc, err)
		}
	}
}

func TestRunningAndRetryingCanTransitionToCancelled(t *testing.T) {
	t.Parallel()
	for _, from := range []NodeStatus{NodeStatusRunning, NodeStatusRetrying} {
		if err := ValidateTransition(from, NodeStatusCancelled); err != nil {
			t.Fatalf("%s → cancelled should be legal for user run termination, got: %v", from, err)
		}
		if _, ok := legalTransitions[transition{from, NodeStatusCancelled}]; !ok {
			t.Fatalf("legalTransitions 缺少 %s → cancelled 条目", from)
		}
	}
}

func TestValidateTransition_Illegal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to NodeStatus
		want     string // 错误消息子串
	}{
		// 跳态：pending 直接到 done
		{NodeStatusPending, NodeStatusDone, "非法"},
		// 终态出态：done 不允许任何后续
		{NodeStatusDone, NodeStatusReady, "非法"},
		{NodeStatusDone, NodeStatusFailed, "非法"},
		// failed → 任何：禁止
		{NodeStatusFailed, NodeStatusReady, "非法"},
		// cancelled → 任何：禁止
		{NodeStatusCancelled, NodeStatusReady, "非法"},
		// skipped → 任何：禁止
		{NodeStatusSkipped, NodeStatusReady, "非法"},
		// reserved/legacy 状态不再作为新 runtime transition 目标或来源
		{NodeStatusRunning, NodeStatusSkipped, "非法"},
		{NodeStatusRunning, NodeStatusWaitingHuman, "非法"},
		{NodeStatusWaitingHuman, NodeStatusReady, "非法"},
		{NodeStatusWaitingHuman, NodeStatusFailed, "非法"},
		{NodeStatusWaitingHuman, NodeStatusCancelled, "非法"},
		// 反向：ready → pending
		{NodeStatusReady, NodeStatusPending, "非法"},
		// 反向：running → ready (跳过 retrying)
		{NodeStatusRunning, NodeStatusReady, "非法"},
		// 同态
		{NodeStatusRunning, NodeStatusRunning, "same state"},
		{NodeStatusPending, NodeStatusPending, "same state"},
		// 空 from
		{"", NodeStatusReady, "empty from"},
		// 空 to
		{NodeStatusReady, "", "empty to"},
		// 未知 to
		{NodeStatusReady, "bogus", "非法"},
	}
	for _, tc := range cases {
		err := ValidateTransition(tc.from, tc.to)
		if err == nil {
			t.Errorf("%q→%q should be illegal, got nil error", tc.from, tc.to)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q→%q error = %v, want substring %q", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestLegalTransitionTargetStatusStrings(t *testing.T) {
	t.Parallel()
	got := LegalTransitionTargetStatusStrings()
	want := []string{"ready", "running", "retrying", "done", "failed", "cancelled"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("LegalTransitionTargetStatusStrings() = %v, want %v", got, want)
	}
	for _, status := range got {
		if status == string(NodeStatusPending) {
			t.Fatalf("public transition targets must not expose unreachable pending: %v", got)
		}
	}
}

func TestIsTerminal_FourStates(t *testing.T) {
	t.Parallel()
	terminal := []NodeStatus{
		NodeStatusDone,
		NodeStatusFailed,
		NodeStatusCancelled,
		NodeStatusSkipped,
	}
	for _, s := range terminal {
		if !IsTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	nonTerminal := []NodeStatus{
		NodeStatusPending,
		NodeStatusReady,
		NodeStatusRunning,
		NodeStatusRetrying,
		NodeStatusWaitingHuman,
	}
	for _, s := range nonTerminal {
		if IsTerminal(s) {
			t.Errorf("%q should NOT be terminal", s)
		}
	}
}

// TestTerminalsHaveNoOutgoing 强制不变量：legalTransitions 里不能有任何
// 终态作为 from（守护"终态出态"的封闭性）。
func TestTerminalsHaveNoOutgoing(t *testing.T) {
	t.Parallel()
	for tr := range legalTransitions {
		if IsTerminal(tr.From) {
			t.Errorf("terminal status %q has outgoing transition to %q (违反终态封闭原则)", tr.From, tr.To)
		}
	}
}

// TestRetryingCanTransitionToCancelled 针对新增的 retrying → cancelled 合法
// 转移单独守护（防未来误删）。场景：上游节点 fail_fast 级联取消
// 且当前节点正在退避。以前该转移不合法，只能强转 failed，导致
// 该节点被误认为“自身失败”。
// English: dedicated guard for the newly legal retrying → cancelled
// transition. Scenario: upstream fail_fast cascades while this node is in
// backoff; previously the only path was a forced → failed, mislabelling a
// node that never had a chance to retry.
func TestRetryingCanTransitionToCancelled(t *testing.T) {
	t.Parallel()
	if err := ValidateTransition(NodeStatusRetrying, NodeStatusCancelled); err != nil {
		t.Fatalf("retrying → cancelled should be legal, got: %v", err)
	}
	if _, ok := legalTransitions[transition{NodeStatusRetrying, NodeStatusCancelled}]; !ok {
		t.Fatal("legalTransitions 缺少 retrying → cancelled 条目")
	}
}
