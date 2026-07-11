package hooks

import (
	"errors"
	"slices"
	"testing"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestMergeBeforePrefersDenyOverAllow(t *testing.T) {
	t.Parallel()

	result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
		{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}},
		{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionDeny, Reason: "policy"}},
	})

	if result.Decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionDeny)
	}
	if result.Decision.Reason != "policy" {
		t.Fatalf("reason = %q, want %q", result.Decision.Reason, "policy")
	}
}

func TestMergeAfterPriority(t *testing.T) {
	t.Parallel()

	t.Run("escalate_over_approve", func(t *testing.T) {
		result := MergeAfter([]peerDecision[mcp.AfterDecision]{
			{Decision: mcp.AfterDecision{Decision: mcp.HookDecisionApprove}},
			{Decision: mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, Reason: "needs review", TTLMs: 30_000}},
		})

		if result.Decision.Decision != mcp.HookDecisionEscalate {
			t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionEscalate)
		}
		if result.Decision.TTLMs != 30_000 {
			t.Fatalf("ttl_ms = %d, want %d", result.Decision.TTLMs, 30_000)
		}
	})

	t.Run("reject_over_escalate", func(t *testing.T) {
		result := MergeAfter([]peerDecision[mcp.AfterDecision]{
			{Decision: mcp.AfterDecision{Decision: mcp.HookDecisionApprove}},
			{Decision: mcp.AfterDecision{Decision: mcp.HookDecisionEscalate}},
			{Decision: mcp.AfterDecision{Decision: mcp.HookDecisionReject, Reason: "hard stop"}},
		})

		if result.Decision.Decision != mcp.HookDecisionReject {
			t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionReject)
		}
		if result.Decision.Reason != "hard stop" {
			t.Fatalf("reason = %q, want %q", result.Decision.Reason, "hard stop")
		}
	})

	t.Run("escalate_ttl_uses_first_sorted_subscriber", func(t *testing.T) {
		result := MergeAfter([]peerDecision[mcp.AfterDecision]{
			{Lease: mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}, Decision: mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, TTLMs: 5_000}},
			{Lease: mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}, Decision: mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, TTLMs: 10_000}},
			{Lease: mcp.LeaseKey{InstanceID: "lease-c", Generation: 1}, Decision: mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, TTLMs: 3_000}},
		})

		if result.Decision.Decision != mcp.HookDecisionEscalate {
			t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionEscalate)
		}
		if result.Decision.TTLMs != 5_000 {
			t.Fatalf("ttl_ms = %d, want %d from first sorted escalate subscriber", result.Decision.TTLMs, 5_000)
		}
	})
}

func TestMergeBeforeFillsLostLeases(t *testing.T) {
	t.Parallel()

	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
		{Lease: lease, Err: errors.New("subscriber lost"), ConsecutiveFailures: 3},
	})

	if !slices.Equal(result.FailedLeases, []mcp.LeaseKey{lease}) {
		t.Fatalf("failed leases = %#v, want %#v", result.FailedLeases, []mcp.LeaseKey{lease})
	}
	if !slices.Equal(result.LostLeases, []mcp.LeaseKey{lease}) {
		t.Fatalf("lost leases = %#v, want %#v", result.LostLeases, []mcp.LeaseKey{lease})
	}
}

func TestMergeDuringFillsLostLeases(t *testing.T) {
	t.Parallel()

	lease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}
	result := MergeDuring([]peerDecision[mcp.CheckDecision]{
		{Lease: lease, Err: errors.New("subscriber lost"), ConsecutiveFailures: 3},
	})

	if !slices.Equal(result.FailedLeases, []mcp.LeaseKey{lease}) {
		t.Fatalf("failed leases = %#v, want %#v", result.FailedLeases, []mcp.LeaseKey{lease})
	}
	if !slices.Equal(result.LostLeases, []mcp.LeaseKey{lease}) {
		t.Fatalf("lost leases = %#v, want %#v", result.LostLeases, []mcp.LeaseKey{lease})
	}
}

func TestMergeBeforeAllowedToolsUsesIntersection(t *testing.T) {
	t.Parallel()

	result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
		{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: []string{"shell", "read"}}},
		{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionModify, AllowedTools: []string{"read", "write", "shell"}}},
		{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: []string{"read", "shell"}}},
	})

	if !slices.Equal(result.Decision.AllowedTools, []string{"read", "shell"}) {
		t.Fatalf("allowed tools = %#v, want %#v", result.Decision.AllowedTools, []string{"read", "shell"})
	}
}

func TestMergeBeforeAllowedToolsNilVsEmpty(t *testing.T) {
	t.Parallel()

	t.Run("nil_is_ignored", func(t *testing.T) {
		result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: nil}},
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: []string{"read"}}},
		})

		if !slices.Equal(result.Decision.AllowedTools, []string{"read"}) {
			t.Fatalf("allowed tools = %#v, want %#v", result.Decision.AllowedTools, []string{"read"})
		}
	})

	t.Run("empty_collapses_to_empty_set", func(t *testing.T) {
		result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: []string{}}},
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: []string{"read"}}},
		})

		if result.Decision.AllowedTools == nil {
			t.Fatal("allowed tools = nil, want empty slice")
		}
		if len(result.Decision.AllowedTools) != 0 {
			t.Fatalf("allowed tools len = %d, want 0", len(result.Decision.AllowedTools))
		}
	})

	t.Run("all_nil_stays_unrestricted", func(t *testing.T) {
		result := MergeBefore([]peerDecision[mcp.BeforeDecision]{
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionAllow, AllowedTools: nil}},
			{Decision: mcp.BeforeDecision{Decision: mcp.HookDecisionModify, AllowedTools: nil}},
		})

		if result.Decision.AllowedTools != nil {
			t.Fatalf("allowed tools = %#v, want nil", result.Decision.AllowedTools)
		}
	})
}

func TestMergeEmptySlicesCollapseToDefaults(t *testing.T) {
	t.Parallel()

	before := MergeBefore(nil)
	if before.Decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("before = %q, want %q", before.Decision.Decision, mcp.HookDecisionDeny)
	}

	check := MergeDuring(nil)
	if check.Decision.Decision != mcp.HookDecisionContinue {
		t.Fatalf("check = %q, want %q", check.Decision.Decision, mcp.HookDecisionContinue)
	}

	after := MergeAfter(nil)
	if after.Decision.Decision != mcp.HookDecisionReject {
		t.Fatalf("after = %q, want %q", after.Decision.Decision, mcp.HookDecisionReject)
	}
}

func TestMergeDuring_AbortOverContinue(t *testing.T) {
	t.Parallel()

	result := MergeDuring([]peerDecision[mcp.CheckDecision]{
		{Decision: mcp.CheckDecision{Decision: mcp.HookDecisionContinue}},
		{Decision: mcp.CheckDecision{
			Decision: " Abort ",
			Severity: " high ",
			Reason:   " blocked ",
		}},
	})

	if result.Decision.Decision != mcp.HookDecisionAbort {
		t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionAbort)
	}
	if result.Decision.Severity != "high" {
		t.Fatalf("severity = %q, want %q", result.Decision.Severity, "high")
	}
	if result.Decision.Reason != "blocked" {
		t.Fatalf("reason = %q, want %q", result.Decision.Reason, "blocked")
	}
}

func TestMergeAfter_LostLeases(t *testing.T) {
	t.Parallel()

	leaseLost := mcp.LeaseKey{InstanceID: "lease-lost", Generation: 1}
	leaseRetrying := mcp.LeaseKey{InstanceID: "lease-retrying", Generation: 1}
	result := MergeAfter([]peerDecision[mcp.AfterDecision]{
		{Decision: mcp.AfterDecision{Decision: " approve ", Reason: "ok"}},
		{Lease: leaseLost, Err: errors.New("timeout"), ConsecutiveFailures: 3},
		{Lease: leaseLost, Err: errors.New("timeout"), ConsecutiveFailures: 4},
		{Lease: leaseRetrying, Err: errors.New("timeout"), ConsecutiveFailures: 2},
	})

	if result.Decision.Decision != mcp.HookDecisionApprove {
		t.Fatalf("decision = %q, want %q", result.Decision.Decision, mcp.HookDecisionApprove)
	}
	if !result.PartialFailure {
		t.Fatal("PartialFailure = false, want true")
	}
	if !slices.Equal(result.FailedLeases, []mcp.LeaseKey{leaseLost, leaseRetrying}) {
		t.Fatalf("FailedLeases = %#v, want %#v", result.FailedLeases, []mcp.LeaseKey{leaseLost, leaseRetrying})
	}
	if !slices.Equal(result.LostLeases, []mcp.LeaseKey{leaseLost}) {
		t.Fatalf("LostLeases = %#v, want %#v", result.LostLeases, []mcp.LeaseKey{leaseLost})
	}
}
