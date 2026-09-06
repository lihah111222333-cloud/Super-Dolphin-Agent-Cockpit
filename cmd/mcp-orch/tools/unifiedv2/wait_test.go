package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/waitaggregate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type fakeWaiter struct {
	calls        int
	getWaitCalls int
	result       accesswait.WaitResult
	grant        accesswait.AccessGrant
	handle       accesswait.WaitHandle
}

func (f *fakeWaiter) GetGrant(context.Context, string) (accesswait.AccessGrant, error) {
	if f.grant.GrantID != "" {
		return f.grant, nil
	}
	return validWaitGrant(), nil
}

func (f *fakeWaiter) GetWait(_ context.Context, request accesswait.PollRequest) (accesswait.WaitHandle, error) {
	f.getWaitCalls++
	if f.handle.WaitHandleID != "" {
		return f.handle, nil
	}
	grant := f.grant
	if grant.GrantID == "" {
		grant = validWaitGrant()
	}
	return accesswait.WaitHandle{WaitHandleID: request.WaitHandleID, GrantID: grant.GrantID, GrantRevision: grant.Revision,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, SourceAgentID: grant.Source.AgentID,
		Target: grant.Target, Selector: accesswait.EventSelector{}}, nil
}

func validWaitGrant() accesswait.AccessGrant {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	return accesswait.AccessGrant{GrantID: "grant", Revision: 1, Status: "active", SchemaVersion: 1,
		CanRead: true, CallerPrincipalRef: "caller", ScopeRef: "scope", Source: testFence("caller", "epoch"),
		Target: testFence("target", "target-epoch"), IdempotencyNamespace: "wait", IdempotencyKeyDigest: strings.Repeat("b", 64),
		RequestDigest: strings.Repeat("c", 64), IssuedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func testFence(agent, epoch string) accesswait.FenceSnapshot {
	return accesswait.FenceSnapshot{AgentID: agent, ReservationID: "reservation-" + agent, ReservationGeneration: 1, IncarnationID: epoch, IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-" + agent}
}

func durableWaitHandleFor(grant accesswait.AccessGrant, target WaitTarget) accesswait.WaitHandle {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	return accesswait.WaitHandle{WaitHandleID: target.Key, GrantID: grant.GrantID, GrantRevision: grant.Revision,
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, SourceAgentID: grant.Source.AgentID,
		Target: grant.Target, Selector: target.Selector, AfterSequence: target.AfterSequence, AfterCursor: target.AfterCursor,
		DeadlineAt: now.Add(time.Hour), IdempotencyNamespace: grant.IdempotencyNamespace, IdempotencyKeyDigest: grant.IdempotencyKeyDigest,
		RequestDigest: grant.RequestDigest, Status: "pending", Revision: 1, CreatedAt: now, UpdatedAt: now}
}

func satisfiedWaitHandleFor(t *testing.T, grant accesswait.AccessGrant, target WaitTarget) accesswait.WaitHandle {
	t.Helper()
	handle := durableWaitHandleFor(grant, target)
	handle.Status = "satisfied"
	handle.MatchedEventID = "event"
	handle.MatchedSourceSequence = 1
	handle.MatchedCanonicalCursor = canonicalCursorForTest(t, target.Selector, handle.MatchedSourceSequence)
	resolvedAt := handle.UpdatedAt
	handle.ResolvedAt = &resolvedAt
	return handle
}

func canonicalCursorForTest(t *testing.T, selector accesswait.EventSelector, sequence int64) string {
	t.Helper()
	cursor, err := eventstore.CanonicalCursor(selector.SourceID, selector.SourceEpoch, selector.Lane, sequence)
	if err != nil {
		t.Fatalf("CanonicalCursor() error = %v", err)
	}
	return cursor
}

func targetEventSourceForTest() taskeventsource.Source {
	return taskeventsource.Source{SourceID: "task/target", SourceEpoch: "target-epoch", AgentID: "target", IncarnationID: "target-epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-target"}
}

func targetSelectorForTest(t *testing.T) accesswait.EventSelector {
	t.Helper()
	selector, err := targetEventSourceForTest().Selector(eventstore.LaneOutput, "target", []string{"done"})
	if err != nil {
		t.Fatalf("Selector() error = %v", err)
	}
	return selector
}

func TestValidatingWaiterRejectsForgedReservationFenceBeforeWait(t *testing.T) {
	source := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence"}
	selector := targetSelectorForTest(t)
	forged := validWaitGrant()
	forged.Source.ReservationID = "forged"
	w := &fakeWaiter{grant: forged}
	guard := &validatingWaiter{waiter: w, source: source, callerFence: accesswait.FenceSnapshot{AgentID: "caller", ReservationID: "real", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64)}, targets: []WaitTarget{{Key: "h", GrantID: "grant", GrantRevision: 1, Selector: selector}}}
	_, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: "h", CallerPrincipalRef: "caller", ScopeRef: "scope", GrantRevision: 1})
	if !errors.Is(err, accesswait.ErrAccessDenied) || w.calls != 0 {
		t.Fatalf("err=%v calls=%d, want denied before Wait", err, w.calls)
	}
}

func (f *fakeWaiter) Wait(context.Context, accesswait.PollRequest) (accesswait.WaitResult, error) {
	f.calls++
	return f.result, nil
}

func TestDecodeInputRejectsUnknownDuplicateNullAndOneHot(t *testing.T) {
	cases := []string{
		`{"targets":[],"condition":"any","timeout_ms":1,"timeout_ms":2}`,
		`{"targets":[],"condition":"any","timeout_ms":1,"deadline":null}`,
		`{"targets":[],"condition":"any","timeout_ms":1,"unknown":true}`,
		`{"targets":[],"condition":"any","timeout_ms":1,"deadline":"2026-01-01T00:00:00Z"}`,
	}
	for _, raw := range cases {
		if _, err := decodeInput(json.RawMessage(raw)); err == nil {
			t.Errorf("decodeInput(%s) error = nil", raw)
		}
	}
}

func TestDecodeInputRejectsTooShortTimeoutBeforeOwner(t *testing.T) {
	for _, timeout := range []int{0, waitaggregate.MinTimeoutMS - 1} {
		raw := fmt.Sprintf(`{"targets":[{"key":"h"}],"condition":"any","timeout_ms":%d}`, timeout)
		if _, err := decodeInput(json.RawMessage(raw)); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
			t.Fatalf("decodeInput(timeout_ms=%d) error = %v, want ErrInvalidRequest", timeout, err)
		}
	}
}

func TestDeadlineRejectsTooShortTimeoutWindow(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	for _, timeout := range []int{0, waitaggregate.MinTimeoutMS - 1} {
		in := WaitAgentsInput{TimeoutMS: &timeout}
		if _, err := in.deadline(now); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
			t.Fatalf("deadline(timeout_ms=%d) error = %v, want ErrInvalidRequest", timeout, err)
		}
	}
	valid := waitaggregate.MinTimeoutMS
	if deadline, err := (WaitAgentsInput{TimeoutMS: &valid}).deadline(now); err != nil || !deadline.Equal(now.Add(time.Duration(valid)*time.Millisecond)) {
		t.Fatalf("deadline(timeout_ms=%d) = %v, %v; want exact captured deadline", valid, deadline, err)
	}
	tooSoon := now.Add((time.Duration(waitaggregate.MinTimeoutMS) * time.Millisecond) - time.Nanosecond).Format(time.RFC3339Nano)
	if _, err := (WaitAgentsInput{Deadline: &tooSoon}).deadline(now); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
		t.Fatalf("deadline(%q) error = %v, want ErrInvalidRequest", tooSoon, err)
	}
}

func TestBuildRequestAtUsesInjectedSecurityClockForFutureDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	timeout := 250
	selector := targetSelectorForTest(t)
	in := WaitAgentsInput{Targets: []WaitTarget{{Key: "h", GrantID: "grant", GrantRevision: 1, Selector: selector}}, Condition: waitaggregate.ConditionAny, TimeoutMS: &timeout}
	principal := common.CallerPrincipal{PrincipalRef: "caller", ScopeRef: "scope"}
	request, err := buildRequestAt(in, principal, taskeventsource.Source{}, now)
	if err != nil {
		t.Fatalf("buildRequestAt() error = %v", err)
	}
	want := now.Add(time.Duration(timeout) * time.Millisecond)
	if !request.DeadlineAt.Equal(want) || request.DeadlineAt.Location() != time.UTC {
		t.Fatalf("deadline = %v, want %v UTC", request.DeadlineAt, want)
	}
}

func TestBuildRequestCompatibilityWrapperRemainsAvailable(t *testing.T) {
	t.Parallel()
	timeout := waitaggregate.MinTimeoutMS
	selector := targetSelectorForTest(t)
	in := WaitAgentsInput{Targets: []WaitTarget{{Key: "h", GrantID: "grant", GrantRevision: 1, Selector: selector}}, Condition: waitaggregate.ConditionAny, TimeoutMS: &timeout}
	if _, err := buildRequest(in, common.CallerPrincipal{PrincipalRef: "caller", ScopeRef: "scope"}, taskeventsource.Source{}); err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
}

func TestWaitAgentsConstructorRequiresExplicitClock(t *testing.T) {
	t.Parallel()
	resolver, err := taskprincipal.New(constructorIdentityReader{})
	if err != nil {
		t.Fatal(err)
	}
	owner := &fakeWaiter{}
	if _, err := NewWithClock(resolver, owner, nil); !errors.Is(err, ErrWaitAgentsUnavailable) || !errors.Is(err, waitaggregate.ErrNilClock) {
		t.Fatalf("NewWithClock(nil clock) error = %v, want unavailable/nil-clock", err)
	}
	if handler, err := NewWithClock(resolver, owner, func() time.Time { return time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC) }); err != nil || handler == nil {
		t.Fatalf("NewWithClock(valid clock) = %#v, %v; want handler", handler, err)
	}
}

func TestConstructorDisabledWithoutDependencies(t *testing.T) {
	if _, err := New(nil, nil); !errors.Is(err, ErrUnifiedDisabled) {
		t.Fatalf("New() error = %v, want ErrUnifiedDisabled", err)
	}
}

func TestConstructorDisabledWithoutDurableWaitReader(t *testing.T) {
	resolver, err := taskprincipal.New(constructorIdentityReader{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(resolver, grantOnlyWaiter{}); !errors.Is(err, ErrUnifiedDisabled) {
		t.Fatalf("New(owner without GetWait) error = %v, want ErrUnifiedDisabled", err)
	}
}

func TestRequireTaskPrincipalRejectsOperatorProfileCaseInsensitively(t *testing.T) {
	for _, profile := range []string{"operator", "OPERATOR", "Operator"} {
		t.Run(profile, func(t *testing.T) {
			principal, err := common.NewCallerPrincipal("principal/operator", "scope/operator", profile, "auth/operator", time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := common.BindCallerPrincipal(context.Background(), principal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := requireTaskPrincipal(ctx); !errors.Is(err, ErrWaitAgentsUnavailable) {
				t.Fatalf("requireTaskPrincipal(%q) error = %v, want unavailable", profile, err)
			}
		})
	}
}

type grantOnlyWaiter struct{}

func (grantOnlyWaiter) GetGrant(context.Context, string) (accesswait.AccessGrant, error) {
	return accesswait.AccessGrant{}, nil
}

func (grantOnlyWaiter) Wait(context.Context, accesswait.PollRequest) (accesswait.WaitResult, error) {
	return accesswait.WaitResult{}, nil
}

type constructorIdentityReader struct{}

func (constructorIdentityReader) Get(context.Context, string) (identity.Reservation, error) {
	return identity.Reservation{}, nil
}

func (constructorIdentityReader) GetIncarnation(context.Context, string) (identity.Incarnation, error) {
	return identity.Incarnation{}, nil
}

func (constructorIdentityReader) GetCurrent(context.Context, string) (identity.CurrentBinding, error) {
	return identity.CurrentBinding{}, nil
}

func TestValidatingWaiterRejectsStaleCursorAndTargetRollover(t *testing.T) {
	source := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "handle", GrantID: "grant", GrantRevision: 1, Selector: selector, AfterSequence: 1, AfterCursor: canonicalCursorForTest(t, selector, 1)}
	handle := durableWaitHandleFor(grant, target)
	handle.AfterSequence = 9
	handle.AfterCursor = canonicalCursorForTest(t, selector, 9)
	w := &fakeWaiter{grant: grant, handle: handle, result: accesswait.WaitResult{Handle: handle, Status: handle.Status}}
	guard := &validatingWaiter{waiter: w, targets: []WaitTarget{target}, source: source}
	guard.callerFence = testFence("caller", "epoch")
	_, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: "handle", GrantRevision: 1, CallerPrincipalRef: "caller", ScopeRef: "scope"})
	if !errors.Is(err, accesswait.ErrAccessDenied) {
		t.Fatalf("Wait() error = %v, want access denied", err)
	}
	if w.calls != 0 {
		t.Fatalf("waiter calls = %d, want 0 before owner Wait", w.calls)
	}
}

func TestValidatingWaiterRejectsForgedDurableHandleBeforeOwnerWait(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "h", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: selector}
	for _, forge := range []struct {
		name string
		edit func(*accesswait.WaitHandle)
	}{
		{name: "selector", edit: func(handle *accesswait.WaitHandle) { handle.Selector.EventTypes = []string{"forged"} }},
		{name: "source fence marker", edit: func(handle *accesswait.WaitHandle) { handle.SourceAgentID = "forged-source" }},
		{name: "target fence", edit: func(handle *accesswait.WaitHandle) { handle.Target.ReservationID = "forged-target" }},
	} {
		t.Run(forge.name, func(t *testing.T) {
			forged := durableWaitHandleFor(grant, target)
			forge.edit(&forged)
			w := &fakeWaiter{grant: grant, handle: forged}
			guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{target}}
			_, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: target.Key, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
			if !errors.Is(err, accesswait.ErrAccessDenied) {
				t.Fatalf("Wait() error = %v, want access denied", err)
			}
			if w.calls != 0 {
				t.Fatalf("owner Wait calls = %d, want zero for forged durable handle", w.calls)
			}
		})
	}
}

func TestValidatingWaiterAllowsTargetSourceForCrossTaskGrant(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "h", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: selector}
	handle := durableWaitHandleFor(grant, target)
	w := &fakeWaiter{grant: grant, handle: handle, result: accesswait.WaitResult{Handle: handle, Status: handle.Status}}
	guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{target}}
	result, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: "h", CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
	if err != nil {
		t.Fatalf("Wait(target source) error = %v, want success", err)
	}
	if w.calls != 1 || result.Handle.Selector.SourceID != "task/target" || result.Handle.Selector.SourceEpoch != grant.Target.IncarnationID {
		t.Fatalf("Wait(target source) calls=%d result=%#v, want one call with target source", w.calls, result)
	}
}

func TestValidatingWaiterRejectsMalformedOwnerResults(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "h", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: selector}
	for _, test := range []struct {
		name   string
		handle accesswait.WaitHandle
		result accesswait.WaitResult
	}{
		{
			name:   "satisfied without event",
			handle: satisfiedWaitHandleFor(t, grant, target),
			result: accesswait.WaitResult{Handle: satisfiedWaitHandleFor(t, grant, target), Status: "satisfied"},
		},
		{
			name:   "status and handle mismatch",
			handle: durableWaitHandleFor(grant, target),
			result: accesswait.WaitResult{Handle: durableWaitHandleFor(grant, target), Status: "timed_out"},
		},
		{
			name:   "malformed event",
			handle: durableWaitHandleFor(grant, target),
			result: accesswait.WaitResult{Handle: durableWaitHandleFor(grant, target), Status: "pending", Event: &eventstore.AgentEvent{}},
		},
		{
			name:   "malformed cursor",
			handle: durableWaitHandleFor(grant, target),
			result: accesswait.WaitResult{Handle: durableWaitHandleFor(grant, target), Status: "pending", NextCursor: " bad"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &fakeWaiter{grant: grant, handle: test.handle, result: test.result}
			guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{target}}
			result, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: target.Key, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
			if !errors.Is(err, ErrWaitAgentsUnavailable) {
				t.Fatalf("Wait() error = %v, want ErrWaitAgentsUnavailable", err)
			}
			if !reflect.DeepEqual(result, accesswait.WaitResult{}) {
				t.Fatalf("result = %#v, want zero result", result)
			}
			if w.calls != 1 {
				t.Fatalf("owner Wait calls = %d, want one call before result validation", w.calls)
			}
		})
	}
}

func TestValidatingWaiterAcceptsValidOwnerResult(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "h", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: selector}
	handle := durableWaitHandleFor(grant, target)
	w := &fakeWaiter{grant: grant, handle: handle, result: accesswait.WaitResult{Handle: handle, Status: handle.Status}}
	guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{target}}
	result, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: target.Key, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
	if err != nil {
		t.Fatalf("Wait() error = %v, want success", err)
	}
	if !reflect.DeepEqual(result, w.result) {
		t.Fatalf("result = %#v, want %#v", result, w.result)
	}
}

func TestValidatingWaiterRejectsWrongResultHandleIDAfterOwnerWait(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	selector := targetSelectorForTest(t)
	grant := validWaitGrant()
	target := WaitTarget{Key: "expected", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: selector}
	durable := durableWaitHandleFor(grant, target)
	returned := durable
	returned.WaitHandleID = "wrong"
	w := &fakeWaiter{grant: grant, handle: durable, result: accesswait.WaitResult{Handle: returned, Status: returned.Status}}
	guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{target}}
	result, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: target.Key, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
	if !errors.Is(err, accesswait.ErrAccessDenied) {
		t.Fatalf("Wait() error = %v, want access denied for wrong result handle id", err)
	}
	if w.calls != 1 {
		t.Fatalf("owner Wait calls = %d, want one post-owner result to validate", w.calls)
	}
	if !reflect.DeepEqual(result, accesswait.WaitResult{}) {
		t.Fatalf("result = %#v, want zero result on binding mismatch", result)
	}
}

func TestValidatingWaiterRejectsCallerSourceSelectorForCrossTaskGrant(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	callerSelector, err := caller.Selector(eventstore.LaneOutput, "target", []string{"done"})
	if err != nil {
		t.Fatal(err)
	}
	staleTargetSelector := targetSelectorForTest(t)
	staleTargetSelector.SourceEpoch = "stale-target-epoch"
	grant := validWaitGrant()
	for _, test := range []struct {
		name     string
		selector accesswait.EventSelector
	}{
		{name: "caller source", selector: callerSelector},
		{name: "stale target epoch", selector: staleTargetSelector},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &fakeWaiter{grant: grant}
			guard := &validatingWaiter{waiter: w, source: caller, callerFence: grant.Source, targets: []WaitTarget{{Key: "h", GrantID: grant.GrantID, GrantRevision: grant.Revision, Selector: test.selector}}}
			_, err := guard.Wait(context.Background(), accesswait.PollRequest{WaitHandleID: "h", CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision})
			if !errors.Is(err, accesswait.ErrAccessDenied) || w.calls != 0 {
				t.Fatalf("Wait(%s) error=%v calls=%d, want denied before owner Wait", test.name, err, w.calls)
			}
		})
	}
}

func TestValidateTargetRejectsNonCanonicalTargetSource(t *testing.T) {
	caller := taskeventsource.Source{SourceID: "task/caller", SourceEpoch: "epoch", AgentID: "caller", IncarnationID: "epoch", BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence-caller"}
	targetSelector := targetSelectorForTest(t)
	valid := WaitTarget{Key: "h", GrantID: "grant", GrantRevision: 1, Selector: targetSelector}
	if err := validateTarget(valid, caller); err != nil {
		t.Fatalf("validateTarget(target source) = %v, want success", err)
	}
	forged := valid
	forged.Selector.SourceID = caller.SourceID
	forged.Selector.SourceEpoch = caller.SourceEpoch
	if err := validateTarget(forged, caller); !errors.Is(err, waitaggregate.ErrInvalidRequest) {
		t.Fatalf("validateTarget(caller source) = %v, want invalid request", err)
	}
}
