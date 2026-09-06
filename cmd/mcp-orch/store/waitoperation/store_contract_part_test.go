package waitoperation

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

func mustPlanWaitOperation(t *testing.T, fixture waitOperationFixture, clock *time.Time) (*Store, PlanRequest, Operation) {
	t.Helper()
	store, err := NewStore(fixture.db, func() time.Time { return *clock })
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{OperationKey: "wait-operation-1", OperationDigest: strings.Repeat("b", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: clock.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
	first, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if first.Status != "provisioning" || first.Revision != 1 || len(first.Targets) != 1 || first.Targets[0].Phase != "planned" {
		t.Fatalf("Plan() = %#v, want provisioning revision 1 with planned target", first)
	}
	return store, request, first
}

func assertPlanWaitOperationReplay(t *testing.T, store *Store, request PlanRequest, first Operation, clock *time.Time) {
	t.Helper()
	*clock = clock.Add(2 * time.Minute)
	replay, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatalf("expired exact Plan replay error = %v", err)
	}
	if replay.OperationKey != first.OperationKey || replay.Targets[0].RecordDigest != first.Targets[0].RecordDigest || !replay.IssuedAt.Equal(first.IssuedAt) {
		t.Fatalf("Plan replay = %#v, want original durable operation", replay)
	}
}

func appendPlanWaitOperationTarget(t *testing.T, store *Store, request PlanRequest, first Operation, clock *time.Time) TargetState {
	t.Helper()
	*clock = first.IssuedAt.Add(10 * time.Second)
	transition := TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: first.Targets[0].RecordDigest, NextPhase: "grant_pending", GrantRevision: 0, GrantStatus: "planned", WaitRevision: 0, WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none"}
	advanced, err := store.AppendTarget(t.Context(), transition)
	if err != nil {
		t.Fatalf("AppendTarget() error = %v", err)
	}
	if advanced.Version != 2 || advanced.Phase != "grant_pending" || advanced.RecordDigest == first.Targets[0].RecordDigest {
		t.Fatalf("AppendTarget() = %#v, want version 2 grant_pending successor", advanced)
	}
	return advanced
}

func assertFencedPlanWaitOperation(t *testing.T, store *Store, request PlanRequest, first Operation, advanced TargetState, clock *time.Time) {
	t.Helper()
	*clock = request.DeadlineAt.Add(2 * time.Minute)
	transition := TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: first.Targets[0].RecordDigest, NextPhase: "grant_pending", GrantRevision: 0, GrantStatus: "planned", WaitRevision: 0, WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none", RecordDigest: advanced.RecordDigest}
	store.fenced.Store(true)
	replayed, err := store.AppendTarget(t.Context(), transition)
	if err != nil || replayed.RecordDigest != advanced.RecordDigest || replayed.Version != advanced.Version {
		t.Fatalf("AppendTarget replay = %#v err=%v, want exact successor", replayed, err)
	}
	got, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || len(got.Targets) != 1 || got.Targets[0].Version != 2 {
		t.Fatalf("Get() targets = %#v err=%v, want latest target version 2", got.Targets, err)
	}
	newRequest := request
	newRequest.OperationKey = "wait-operation-fenced-new"
	newRequest.DeadlineAt = clock.Add(time.Second)
	if _, err := store.Plan(t.Context(), newRequest); !errors.Is(err, ErrFenced) {
		t.Fatalf("new Plan while fenced = %v, want ErrFenced", err)
	}
	store.fenced.Store(false)
}

func assertExpiredPlanWaitOperation(t *testing.T, store *Store, request PlanRequest, first Operation, clock *time.Time) {
	t.Helper()
	*clock = first.IssuedAt.Add(3 * time.Minute)
	expiredRequest := request
	expiredRequest.OperationKey = "wait-operation-expired"
	expiredRequest.DeadlineAt = clock.Add(30 * time.Second)
	expired, err := store.Plan(t.Context(), expiredRequest)
	if err != nil {
		t.Fatalf("Plan(expired candidate) error = %v", err)
	}
	*clock = clock.Add(time.Minute)
	_, err = store.AppendTarget(t.Context(), TargetTransitionRequest{OperationKey: expiredRequest.OperationKey, OperationDigest: expiredRequest.OperationDigest, TargetKey: "target-1", ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: expired.Targets[0].RecordDigest, NextPhase: "grant_pending", GrantRevision: 0, GrantStatus: "planned", WaitRevision: 0, WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none"})
	if !errors.Is(err, ErrDeadlineExpired) {
		t.Fatalf("post-deadline provisioning append error = %v, want ErrDeadlineExpired", err)
	}
	afterExpired, err := store.Get(t.Context(), OperationLookup{Key: expiredRequest.OperationKey, Digest: expiredRequest.OperationDigest})
	if err != nil || afterExpired.Targets[0].Version != 1 {
		t.Fatalf("post-deadline provisioning changed target = %#v err=%v, want unchanged version 1", afterExpired, err)
	}
}
