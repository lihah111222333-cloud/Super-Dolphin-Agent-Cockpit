package waitoperation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func cleanupRequestFor(operation Operation, target TargetState, reason string) CleanupTargetRequest {
	digest, _ := CanonicalCleanupRequestDigest(operation.OperationKey, operation.OperationDigest, reason)
	return CleanupTargetRequest{
		OperationKey:         operation.OperationKey,
		OperationDigest:      operation.OperationDigest,
		TargetKey:            target.Key,
		ExpectedVersion:      target.Version,
		ExpectedPhase:        target.Phase,
		ExpectedRecordDigest: target.RecordDigest,
		CleanupRequestDigest: digest,
		CleanupReasonRef:     reason,
	}
}

func TestCleanupTargetRevokesGrantAndReplaysDurableResult(t *testing.T) {
	store, _, _, pending, cleanup, digest := prepareCleanupReplay(t)
	reason := "cleanup-target"
	cleared, err := store.CleanupTarget(t.Context(), cleanup)
	if err != nil {
		t.Fatalf("CleanupTarget = %#v err=%v", cleared, err)
	}
	assertCleanupCleared(t, store, cleared)
	replay, err := store.CleanupTarget(t.Context(), cleanup)
	if err != nil || replay.RecordDigest != cleared.RecordDigest || replay.Version != cleared.Version {
		t.Fatalf("cleanup replay = %#v err=%v", replay, err)
	}
	planOnly, err := NewStore(store.db, store.clock)
	if err != nil {
		t.Fatal(err)
	}
	planReplay, err := planOnly.CleanupTarget(t.Context(), cleanup)
	if err != nil || planReplay.RecordDigest != cleared.RecordDigest {
		t.Fatalf("plan-only cleanup replay = %#v err=%v", planReplay, err)
	}
	final, err := store.AdvanceState(t.Context(), stateRequest(pending, "cleanup_cleared", "cleanup", digest, reason))
	if err != nil || final.Status != "cleanup_cleared" {
		t.Fatalf("operation cleanup_cleared = %#v err=%v", final, err)
	}
}

func assertCleanupCleared(t *testing.T, store *Store, cleared TargetState) {
	t.Helper()
	if cleared.Phase != "cleanup_cleared" || cleared.GrantStatus != "revoked" || cleared.WaitStatus != "cancelled" || cleared.GrantCleanupState != "cleared" || cleared.WaitCleanupState != "cleared" {
		t.Fatalf("cleared target = %#v", cleared)
	}
	grant, err := store.access.GetGrant(t.Context(), *cleared.ResolvedGrantID)
	if err != nil || grant.Status != "revoked" || grant.Revision != 2 {
		t.Fatalf("grant after cleanup = %#v err=%v", grant, err)
	}
}

func prepareCleanupReplay(t *testing.T) (*Store, PlanRequest, Operation, Operation, CleanupTargetRequest, string) {
	t.Helper()
	store, request, operation := statePlan(t, 1)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "cleanup-target"
	digest, err := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatalf("cleanup_pending = %#v err=%v", pending, err)
	}
	return store, request, operation, pending, cleanupRequestFor(pending, terminal, reason), digest
}

func TestCleanupTargetRequiresTerminalResourceFacts(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	reason := "cleanup-pending"
	digest, _ := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	if _, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason)); !errors.Is(err, ErrOperationStateConflict) {
		t.Fatalf("cleanup_pending with waiting target = %v, want conflict", err)
	}
	bad := cleanupRequestFor(operation, operation.Targets[0], reason)
	bad.ExpectedPhase = "cancelled"
	bad.CleanupRequestDigest = strings.Repeat("f", 64)
	if _, err := store.CleanupTarget(t.Context(), bad); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("malformed cleanup digest = %v, want replay conflict", err)
	}
}

func TestCleanupTargetClearsFailedTargetWithoutGrant(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 1, 0)
	failed, err := store.AppendTarget(t.Context(), TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: operation.Targets[0].Key,
		ExpectedVersion: operation.Targets[0].Version, ExpectedPhase: operation.Targets[0].Phase, ExpectedRecordDigest: operation.Targets[0].RecordDigest,
		GrantStatus: "planned", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "failed", ErrorCode: "target_failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	reason := "cleanup-failed-without-grant"
	digest, err := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := store.CleanupTarget(t.Context(), cleanupRequestFor(pending, failed, reason))
	if err != nil {
		t.Fatalf("CleanupTarget() = %#v err=%v", cleared, err)
	}
	if cleared.Phase != "cleanup_cleared" || cleared.ResolvedGrantID != nil || cleared.GrantStatus != "planned" || cleared.GrantRevision != 0 {
		t.Fatalf("cleared target = %#v", cleared)
	}
}

func TestCleanupTargetReconcilesAlreadyRevokedGrant(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "cleanup-pre-revoked"
	digest, _ := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.access.GetGrant(t.Context(), *terminal.ResolvedGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.access.RevokeGrant(t.Context(), accesswait.RevokeGrantRequest{
		GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef,
		ScopeRef: grant.ScopeRef, ReasonRef: "external-cleanup", RequestDigest: grant.RequestDigest, RevokedAt: store.clock().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := store.CleanupTarget(t.Context(), cleanupRequestFor(pending, terminal, reason))
	if err != nil || cleared.Phase != "cleanup_cleared" || cleared.GrantStatus != "revoked" || cleared.GrantRevision != 2 {
		t.Fatalf("already revoked cleanup = %#v err=%v", cleared, err)
	}
}

func TestCleanupTargetRejectsOperationReopenAfterCleared(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "cleanup-reopen"
	digest, _ := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatal(err)
	}
	cleanup := cleanupRequestFor(pending, terminal, reason)
	cleared, err := store.CleanupTarget(t.Context(), cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdvanceState(t.Context(), stateRequest(pending, "cleanup_cleared", "cleanup", digest, reason)); err != nil {
		t.Fatal(err)
	}
	replay, err := store.CleanupTarget(t.Context(), cleanup)
	if err != nil || replay.Phase != "cleanup_cleared" || replay.RecordDigest != cleared.RecordDigest {
		t.Fatalf("cleared cleanup replay = %#v err=%v", replay, err)
	}
	other := cleanup
	other.CleanupReasonRef = "different-cleanup"
	other.CleanupRequestDigest, err = CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, other.CleanupReasonRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CleanupTarget(t.Context(), other); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("different cleanup replay = %v, want ErrReplayConflict", err)
	}
}

func TestCleanupTargetReopensAndReplaysDurableResult(t *testing.T) {
	store, request, operation := statePlan(t, 1)
	path := sqliteDatabasePath(t, store.db)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "cleanup-reopen-durable"
	digest, _ := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	pending, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatal(err)
	}
	cleanup := cleanupRequestFor(pending, terminal, reason)
	first, err := store.CleanupTarget(t.Context(), cleanup)
	if err != nil {
		t.Fatal(err)
	}
	reopenedStore := reopenCleanupStore(t, store, path, operation)
	replay, err := reopenedStore.CleanupTarget(context.Background(), cleanup)
	if err != nil || replay.Phase != "cleanup_cleared" || replay.RecordDigest != first.RecordDigest {
		t.Fatalf("reopened cleanup replay = %#v err=%v", replay, err)
	}
	if _, err := reopenedStore.AdvanceState(context.Background(), stateRequest(pending, "cleanup_cleared", "cleanup", digest, reason)); err != nil {
		t.Fatalf("reopened operation cleanup_cleared = %v", err)
	}
}

func reopenCleanupStore(t *testing.T, store *Store, path string, operation Operation) *Store {
	t.Helper()
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), reopened); err != nil {
		t.Fatal(err)
	}
	identities, err := identity.NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	access, err := accesswait.NewStore(reopened, identities, candidateEvents{}, func() time.Time { return operation.IssuedAt })
	if err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := NewStoreWithAccess(reopened, func() time.Time { return operation.IssuedAt }, access)
	if err != nil {
		t.Fatal(err)
	}
	return reopenedStore
}
