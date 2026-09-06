package waitoperation

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateReadTargetTransitionMatchesMigrationEdges(t *testing.T) {
	base := readTransitionFixture()
	valid := []struct {
		name       string
		from, to   string
		uncertain  string
		grantState string
		waitState  string
	}{
		{name: "planned to grant pending", from: "planned", to: "grant_pending"},
		{name: "grant pending to wait pending", from: "grant_pending", to: "wait_pending"},
		{name: "wait pending to waiting", from: "wait_pending", to: "waiting"},
		{name: "waiting to satisfied", from: "waiting", to: "satisfied", grantState: "active", waitState: "satisfied"},
		{name: "terminal to cleanup", from: "satisfied", to: "cleanup_pending", grantState: "active", waitState: "satisfied"},
		{name: "cleanup to cleared", from: "cleanup_pending", to: "cleanup_cleared", grantState: "revoked", waitState: "cancelled"},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			previous := base
			previous.Phase = test.from
			previous.GrantStatus, previous.WaitStatus = phaseResources(test.from)
			next := previous
			next.Version = 2
			next.RecordDigest = strings.Repeat("b", 64)
			next.PreviousRecordDigest = strings.Repeat("a", 64)
			next.Phase, next.GrantStatus, next.WaitStatus = test.to, test.grantState, test.waitState
			if test.grantState == "" && test.waitState == "" {
				next.GrantStatus, next.WaitStatus = phaseResources(test.to)
			}
			if err := validateReadTargetTransition(previous, next); err != nil {
				t.Fatalf("validateReadTargetTransition() = %v", err)
			}
		})
	}
}

func TestValidateReadTargetTransitionRejectsIllegalAndErasingSuccessors(t *testing.T) {
	base := readTransitionFixture()
	previous := base
	previous.Phase, previous.GrantStatus, previous.WaitStatus = "waiting", "active", "pending"
	previous.ResolvedGrantID, previous.ResolvedWaitHandleID = readStringPtr("grant"), readStringPtr("wait")
	next := previous
	next.Version = 2
	next.RecordDigest, next.PreviousRecordDigest = strings.Repeat("b", 64), strings.Repeat("a", 64)
	next.Phase, next.GrantStatus, next.WaitStatus = "planned", "planned", "absent"
	next.ResolvedGrantID, next.ResolvedWaitHandleID = nil, nil
	if err := validateReadTargetTransition(previous, next); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("illegal transition error = %v, want ErrInvalidOperation", err)
	}

	next = previous
	next.Version = 2
	next.RecordDigest, next.PreviousRecordDigest = strings.Repeat("c", 64), strings.Repeat("a", 64)
	next.Phase, next.UncertainPhase = "outcome_unknown", "target_transition"
	next.ResolvedGrantID, next.ResolvedWaitHandleID = nil, nil
	next.GrantStatus, next.WaitStatus = "planned", "absent"
	if err := validateReadTargetTransition(previous, next); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("resource-erasing unknown error = %v, want ErrInvalidOperation", err)
	}
}

func TestValidateReadTargetTransitionRejectsCleanupResourceRegression(t *testing.T) {
	previous := readTransitionFixture()
	previous.Phase, previous.GrantStatus, previous.WaitStatus = "satisfied", "active", "satisfied"
	previous.ResolvedGrantID, previous.ResolvedWaitHandleID = readStringPtr("grant"), readStringPtr("wait")
	next := previous
	next.Version = 2
	next.RecordDigest, next.PreviousRecordDigest = strings.Repeat("b", 64), strings.Repeat("a", 64)
	next.Phase, next.GrantStatus, next.WaitStatus = "cleanup_pending", "planned", "satisfied"
	if err := validateReadTargetTransition(previous, next); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("cleanup regression error = %v, want ErrInvalidOperation", err)
	}
}

func TestValidateReadTargetTransitionRejectsIdentityDrift(t *testing.T) {
	previous := readTransitionFixture()
	next := previous
	next.Version = 2
	next.RecordDigest, next.PreviousRecordDigest = strings.Repeat("b", 64), strings.Repeat("a", 64)
	next.Phase = "grant_pending"
	next.TargetIndex = 2
	if err := validateReadTargetTransition(previous, next); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("identity drift error = %v, want ErrInvalidOperation", err)
	}
}

func readTransitionFixture() TargetState {
	return TargetState{
		OperationKey: "operation", OperationDigest: strings.Repeat("a", 64), Key: "target", TargetIndex: 1,
		TargetDigest: strings.Repeat("1", 64), RecordDigest: strings.Repeat("a", 64), SchemaVersion: SchemaVersion,
	}
}

func phaseResources(phase string) (string, string) {
	switch phase {
	case "planned", "grant_pending":
		return "planned", "absent"
	case "wait_pending":
		return "active", "planned"
	case "waiting":
		return "active", "pending"
	case "satisfied":
		return "active", "satisfied"
	case "cleanup_pending":
		return "active", "satisfied"
	case "cleanup_cleared":
		return "revoked", "cancelled"
	default:
		return "planned", "absent"
	}
}

func readStringPtr(value string) *string { return &value }
