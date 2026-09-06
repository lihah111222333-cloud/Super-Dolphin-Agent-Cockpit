package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/waitaggregate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/waitoperation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestDecodeWaitOperationInputRejectsOmittedKindBeforeOwnerCall fixes the branch discriminator.
func TestDecodeWaitOperationInputRejectsOmittedKindBeforeOwnerCall(t *testing.T) {
	raw := `{"operation_key":"op","operation_digest":"` + strings.Repeat("a", 64) + `","target_keys":["target"],"condition":"any"}`
	if _, err := decodeWaitOperationInput(json.RawMessage(raw)); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
		t.Fatalf("decode omitted kind error = %v, want invalid request", err)
	}
}

// TestDecodeWaitOperationInputRejectsOversizedAndNULFields verifies bounded text before owner access.
func TestDecodeWaitOperationInputRejectsOversizedAndNULFields(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := []string{
		`{"operation_key":"` + strings.Repeat("x", 257) + `","operation_digest":"` + digest + `","target_keys":["target"],"kind":"event","condition":"any"}`,
		`{"operation_key":"op","operation_digest":"` + digest + `","target_keys":["` + strings.Repeat("x", waitoperation.MaxTargetKey+1) + `"],"kind":"event","condition":"any"}`,
		`{"operation_key":"op","operation_digest":"` + digest + `","target_keys":["bad\u0000key"],"kind":"event","condition":"any"}`,
		`{"operation_key":"op","operation_digest":"` + strings.Repeat("A", 64) + `","target_keys":["target"],"kind":"event","condition":"any"}`,
	}
	for _, raw := range cases {
		if _, err := decodeWaitOperationInput(json.RawMessage(raw)); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
			t.Errorf("decode malformed input error = %v, want invalid request", err)
		}
	}
}

// TestValidateOperationKindKeepsCommandBranchesFailClosed distinguishes commands from event waits.
func TestValidateOperationKindKeepsCommandBranchesFailClosed(t *testing.T) {
	for _, kind := range []string{"command", "command_submission"} {
		if err := validateOperationKind(kind); !errors.Is(err, ErrWaitOutcomeProjectionUnavailable) {
			t.Fatalf("validateOperationKind(%q) = %v, want projection unavailable", kind, err)
		}
	}
	if err := validateOperationKind("unknown"); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
		t.Fatalf("validateOperationKind(unknown) = %v, want invalid request", err)
	}
}

// TestWaitOperationHandleRejectsMalformedInputBeforeOwners proves invalid requests are zero-owner-call.
func TestWaitOperationHandleRejectsMalformedInputBeforeOwners(t *testing.T) {
	resolver, err := taskprincipal.New(constructorIdentityReader{})
	if err != nil {
		t.Fatal(err)
	}
	operation := &countingOperationOwner{}
	adapter, err := NewWaitOperationAdapter(resolver, operation, &fakeWaiter{}, func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatal(err)
	}
	principal, err := common.NewCallerPrincipal("caller", "scope", "task", "auth", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`{"operation_key":"op","operation_digest":"` + strings.Repeat("a", 64) + `","target_keys":["target"],"condition":"any"}`,
		`{"operation_key":"` + strings.Repeat("x", waitoperation.MaxOperationKey+1) + `","operation_digest":"` + strings.Repeat("a", 64) + `","target_keys":["target"],"kind":"event","condition":"any"}`,
	}
	for _, raw := range cases {
		if _, err := adapter.Handle(ctx, json.RawMessage(raw)); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
			t.Errorf("Handle(%s) error = %v, want invalid request", raw, err)
		}
	}
	if operation.getCalls != 0 {
		t.Fatalf("operation Get calls = %d, want zero", operation.getCalls)
	}
}

// TestReadTargetsRejectsDuplicateWaitHandle ensures one durable handle cannot satisfy two target keys.
func TestReadTargetsRejectsDuplicateWaitHandle(t *testing.T) {
	grant := validWaitGrant()
	selector := targetSelectorForTest(t)
	id := "same-wait"
	grantID := grant.GrantID
	state := func(key string) waitoperation.TargetState {
		return waitoperation.TargetState{Key: key, Phase: "waiting", Target: grant.Target, Selector: selector, ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: "active", ResolvedWaitHandleID: &id, WaitRevision: 1, WaitStatus: "pending"}
	}
	waiter := &fakeWaiter{grant: grant, handle: durableWaitHandleFor(grant, WaitTarget{Key: id, Selector: selector})}
	adapter := &WaitOperationAdapter{waiter: waiter}
	operation := waitoperation.Operation{CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Targets: []waitoperation.TargetState{state("one"), state("two")}, TargetCount: 2}
	source := taskeventsource.Source{AgentID: "caller", IncarnationID: "epoch"}
	_, _, err := adapter.readTargets(context.Background(), operation, []string{"one", "two"}, common.CallerPrincipal{PrincipalRef: "caller", ScopeRef: "scope"}, source, grant.Source)
	if !errors.Is(err, ErrWaitOutcomeProjectionUnavailable) {
		t.Fatalf("readTargets duplicate wait handle error = %v, want projection unavailable", err)
	}
}

// TestValidateOperationProjectionRejectsIdentityAndDigestDrift prevents a generic owner from bypassing its durable contract.
func TestValidateOperationProjectionRejectsIdentityAndDigestDrift(t *testing.T) {
	valid := validOperationProjectionForTest(t)
	cases := map[string]func(*waitoperation.Operation){
		"operation digest":     func(operation *waitoperation.Operation) { operation.OperationDigest = strings.Repeat("b", 64) },
		"target operation key": func(operation *waitoperation.Operation) { operation.Targets[0].OperationKey = "other" },
		"target index":         func(operation *waitoperation.Operation) { operation.Targets[0].TargetIndex = 2 },
		"target record digest": func(operation *waitoperation.Operation) { operation.Targets[0].RecordDigest = "bad" },
		"target count":         func(operation *waitoperation.Operation) { operation.TargetCount = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Targets = append([]waitoperation.TargetState(nil), valid.Targets...)
			mutate(&candidate)
			if err := validateOperationProjection(candidate); !errors.Is(err, ErrWaitOperationAdapterUnavailable) {
				t.Fatalf("validateOperationProjection() = %v, want unavailable", err)
			}
		})
	}
}

// TestVerifyOperationHeadRejectsImmutableIdentityDrift locks the second-read rollover fence.
func TestVerifyOperationHeadRejectsImmutableIdentityDrift(t *testing.T) {
	expected := validOperationProjectionForTest(t)
	actual := expected
	actual.TargetSetDigest = strings.Repeat("b", 64)
	if err := verifyOperationHead(expected, actual); !errors.Is(err, ErrWaitOutcomeProjectionUnavailable) {
		t.Fatalf("verifyOperationHead() = %v, want unavailable", err)
	}
	actual = expected
	actual.Targets = append([]waitoperation.TargetState(nil), expected.Targets...)
	actual.Targets[0].AfterSequence++
	if sameTargetProjection(expected.Targets[0], actual.Targets[0]) {
		t.Fatal("sameTargetProjection() = true for forged target snapshot")
	}
}

func validOperationProjectionForTest(t *testing.T) waitoperation.Operation {
	t.Helper()
	grant := validWaitGrant()
	selector := targetSelectorForTest(t)
	digest := strings.Repeat("a", 64)
	target := waitoperation.TargetState{
		OperationKey: "operation", OperationDigest: digest, Key: "target", TargetIndex: 1, Version: 1,
		TargetDigest: digest, RecordDigest: digest, Target: grant.Target, Selector: selector,
		GrantKeyDigest: digest, GrantRequestDigest: digest, WaitKeyDigest: digest, WaitRequestDigest: digest,
		SchemaVersion: waitoperation.SchemaVersion,
	}
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	return waitoperation.Operation{
		OperationKey: "operation", OperationDigest: digest, TargetSetDigest: digest,
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Source: grant.Source,
		IssuedAt: now, DeadlineAt: now.Add(time.Hour), TargetCount: 1, Revision: 1, Status: "ready",
		SchemaVersion: waitoperation.SchemaVersion, Targets: []waitoperation.TargetState{target},
	}
}

type countingOperationOwner struct{ getCalls int }

func (o *countingOperationOwner) Plan(context.Context, waitoperation.PlanRequest) (waitoperation.Operation, error) {
	return waitoperation.Operation{}, nil
}
func (o *countingOperationOwner) Get(context.Context, waitoperation.OperationLookup) (waitoperation.Operation, error) {
	o.getCalls++
	return waitoperation.Operation{}, nil
}
func (o *countingOperationOwner) List(context.Context, waitoperation.OperationLookup) ([]waitoperation.TargetState, error) {
	return nil, nil
}
func (o *countingOperationOwner) AppendTarget(context.Context, waitoperation.TargetTransitionRequest) (waitoperation.TargetState, error) {
	return waitoperation.TargetState{}, nil
}
func (o *countingOperationOwner) CheckSchema(context.Context) error { return nil }
