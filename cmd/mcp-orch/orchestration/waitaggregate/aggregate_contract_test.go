package waitaggregate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

type fakeWaitResponse struct {
	result       accesswait.WaitResult
	err          error
	waitForClose bool
}

type contractWaiter struct {
	mu        sync.Mutex
	responses map[string]fakeWaitResponse
	calls     []string
	cancelled map[string]bool
}

func (f *contractWaiter) Wait(ctx context.Context, poll accesswait.PollRequest) (accesswait.WaitResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, poll.WaitHandleID)
	response := f.responses[poll.WaitHandleID]
	f.mu.Unlock()
	if response.waitForClose {
		<-ctx.Done()
		f.mu.Lock()
		f.cancelled[poll.WaitHandleID] = true
		f.mu.Unlock()
		return accesswait.WaitResult{}, ctx.Err()
	}
	return response.result, response.err
}

func (f *contractWaiter) wasCancelled(handleID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled[handleID]
}

func TestWaitAggregateContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	t.Run("request validation", func(t *testing.T) { testRequestValidation(t, now) })
	t.Run("conditions succeed", func(t *testing.T) { testConditionSuccess(t, now) })
	t.Run("predicate metadata uses content fields", func(t *testing.T) { testPredicateMetadata(t, now) })
	t.Run("aggregate failure all terminal", func(t *testing.T) { testAggregateFailure(t, now) })
	t.Run("timeout with pending", testTimeoutPending)
	t.Run("indeterminate gap", func(t *testing.T) { testIndeterminateGap(t, now) })
	t.Run("sibling cancellation does not call CancelWait", func(t *testing.T) { testSiblingCancellation(t, now) })
	t.Run("typed nil New", testTypedNilNew)
}

func TestWaitAggregateWithClockUsesOneFutureSkewObservation(t *testing.T) {
	t.Parallel()
	securityNow := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	request := contractRequest(securityNow.Add(25*time.Millisecond), ConditionAny, target("one", "h-1"))
	waiter := &contractWaiter{responses: map[string]fakeWaitResponse{"h-1": {waitForClose: true}}, cancelled: make(map[string]bool)}
	clockCalls := 0
	aggregator, err := NewWithClock(waiter, func() time.Time {
		clockCalls++
		return securityNow
	})
	if err != nil {
		t.Fatalf("NewWithClock() error = %v", err)
	}
	started := time.Now()
	result, err := aggregator.Wait(context.Background(), request)
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil with pending item", err)
	}
	if clockCalls != 1 {
		t.Fatalf("security clock calls = %d, want one captured observation", clockCalls)
	}
	if result.Status != StatusTimeout || result.Items[0].Outcome != OutcomePending {
		t.Fatalf("result = %#v, want timeout with pending item", result)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("future-skew deadline was not bounded by injected duration: elapsed=%v", elapsed)
	}
}

func TestWaitAggregateWithClockRejectsInvalidObservationBeforeOwner(t *testing.T) {
	t.Parallel()
	request := contractRequest(time.Now().UTC().Add(time.Second), ConditionAny, target("one", "h-1"))
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{name: "zero", now: time.Time{}},
		{name: "non UTC", now: time.Date(2040, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 3600))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			waiter := &contractWaiter{responses: successResponses(), cancelled: make(map[string]bool)}
			aggregator, err := NewWithClock(waiter, func() time.Time { return tc.now })
			if err != nil {
				t.Fatalf("NewWithClock() error = %v", err)
			}
			if _, err := aggregator.Wait(context.Background(), request); !errors.Is(err, ErrInvalidClock) {
				t.Fatalf("Wait() error = %v, want ErrInvalidClock", err)
			}
			if len(waiter.calls) != 0 {
				t.Fatalf("owner calls = %d, want zero for invalid clock", len(waiter.calls))
			}
		})
	}
}

func TestNewWithClockRejectsNilClock(t *testing.T) {
	t.Parallel()
	if _, err := NewWithClock(&contractWaiter{}, nil); !errors.Is(err, ErrNilClock) {
		t.Fatalf("NewWithClock(nil clock) error = %v, want ErrNilClock", err)
	}
}

func testRequestValidation(t *testing.T, now time.Time) {
	t.Helper()
	base := contractRequest(now.Add(2*time.Second), ConditionAny, target("one", "h-1"), target("two", "h-2"))
	cases := []struct {
		name string
		edit func(*Request)
	}{
		{name: "max32", edit: func(r *Request) {
			r.Targets = make([]Target, MaxTargets+1)
			for i := range r.Targets {
				r.Targets[i] = target(fmt.Sprintf("target-%d", i), fmt.Sprintf("h-%d", i))
			}
		}},
		{name: "duplicate key", edit: func(r *Request) { r.Targets[1].Key = r.Targets[0].Key }},
		{name: "scope mismatch", edit: func(r *Request) { r.Targets[1].Poll.ScopeRef = "other-scope" }},
		{name: "quorum out of range", edit: func(r *Request) { r.Condition, r.QuorumCount = ConditionQuorum, 3 }},
		{name: "first predicate required", edit: func(r *Request) { r.Condition, r.Predicate = ConditionFirstSuccess, Predicate{} }},
		{name: "deadline required", edit: func(r *Request) { r.DeadlineAt = time.Time{} }},
		{name: "deadline UTC", edit: func(r *Request) { r.DeadlineAt = now.Add(2 * time.Second).In(time.FixedZone("offset", 3600)) }},
		{name: "deadline future", edit: func(r *Request) { r.DeadlineAt = now.Add(-time.Second) }},
		{name: "deadline too soon", edit: func(r *Request) { r.DeadlineAt = now.Add(minWaitWindow - time.Nanosecond) }},
		{name: "deadline bounded", edit: func(r *Request) { r.DeadlineAt = now.Add(61 * time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.Targets = append([]Target(nil), base.Targets...)
			tc.edit(&req)
			if err := req.ValidateAt(now); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ValidateAt() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func testConditionSuccess(t *testing.T, now time.Time) {
	t.Helper()
	cases := []struct {
		name      string
		condition Condition
		quorum    int
	}{
		{name: "any", condition: ConditionAny},
		{name: "all", condition: ConditionAll},
		{name: "quorum", condition: ConditionQuorum, quorum: 2},
		{name: "first_success", condition: ConditionFirstSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := contractRequest(now.Add(2*time.Second), tc.condition, target("one", "h-1"), target("two", "h-2"))
			req.QuorumCount = tc.quorum
			waiter := &contractWaiter{responses: successResponses(), cancelled: make(map[string]bool)}
			result, err := waitAggregate(context.Background(), waiter, req)
			if err != nil {
				t.Fatalf("Wait() error = %v", err)
			}
			if result.Status != StatusSatisfied || result.SuccessCount != 2 || result.WinnerKey != "one" {
				t.Fatalf("result = %#v, want satisfied count=2 winner=one", result)
			}
			if got := []string{result.Items[0].TargetKey, result.Items[1].TargetKey}; fmt.Sprint(got) != "[one two]" {
				t.Fatalf("item order = %v, want [one two]", got)
			}
		})
	}
}

func testPredicateMetadata(t *testing.T, now time.Time) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		metadata bool
		want     ItemOutcome
	}{
		{name: "content present", metadata: true, want: OutcomeSuccess},
		{name: "content absent", metadata: false, want: OutcomeDefinitiveFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := contractRequest(now.Add(2*time.Second), ConditionAny, target("one", "h-1"))
			req.Predicate = Predicate{RequiredEventType: "done", RequireNonEmptyMetadata: true}
			event := contentEvent(tc.metadata)
			waiter := &contractWaiter{responses: map[string]fakeWaitResponse{"h-1": {result: accesswait.WaitResult{Status: "satisfied", Event: &event}}}, cancelled: make(map[string]bool)}
			result, err := waitAggregate(context.Background(), waiter, req)
			if err != nil {
				t.Fatalf("Wait() error = %v", err)
			}
			if result.Items[0].Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q", result.Items[0].Outcome, tc.want)
			}
		})
	}
}

func testAggregateFailure(t *testing.T, now time.Time) {
	t.Helper()
	req := contractRequest(now.Add(2*time.Second), ConditionAll, target("one", "h-1"), target("two", "h-2"))
	waiter := &contractWaiter{responses: map[string]fakeWaitResponse{
		"h-1": {result: accesswait.WaitResult{Status: "timed_out"}},
		"h-2": {result: accesswait.WaitResult{Status: "revoked"}},
	}, cancelled: make(map[string]bool)}
	result, err := waitAggregate(context.Background(), waiter, req)
	if err != nil || result.Status != StatusAggregateFailure || !result.IsError {
		t.Fatalf("result=%#v err=%v, want aggregate_failure error", result, err)
	}
}

func testTimeoutPending(t *testing.T) {
	t.Helper()
	req := contractRequest(time.Now().UTC().Add(100*time.Millisecond), ConditionAny, target("one", "h-1"))
	waiter := &contractWaiter{responses: map[string]fakeWaitResponse{"h-1": {waitForClose: true}}, cancelled: make(map[string]bool)}
	result, err := waitAggregate(context.Background(), waiter, req)
	if err != nil || result.Status != StatusTimeout || result.Items[0].Outcome != OutcomePending {
		t.Fatalf("result=%#v err=%v, want timeout with pending item", result, err)
	}
}

func testIndeterminateGap(t *testing.T, now time.Time) {
	t.Helper()
	req := contractRequest(now.Add(2*time.Second), ConditionAny, target("one", "h-1"))
	waiter := &contractWaiter{responses: map[string]fakeWaitResponse{"h-1": {err: accesswait.ErrWaitGap}}, cancelled: make(map[string]bool)}
	result, err := waitAggregate(context.Background(), waiter, req)
	if err != nil || result.Status != StatusIndeterminate || result.Items[0].Outcome != OutcomeIndeterminate || result.IsError {
		t.Fatalf("result=%#v err=%v, want indeterminate gap", result, err)
	}
}

func testSiblingCancellation(t *testing.T, now time.Time) {
	t.Helper()
	req := contractRequest(now.Add(2*time.Second), ConditionAny, target("winner", "h-1"), target("sibling", "h-2"))
	req.Predicate = Predicate{}
	waiter := &contractWaiter{responses: map[string]fakeWaitResponse{
		"h-1": {result: accesswait.WaitResult{Status: "satisfied"}},
		"h-2": {waitForClose: true},
	}, cancelled: make(map[string]bool)}
	result, err := waitAggregate(context.Background(), waiter, req)
	if err != nil || result.Status != StatusSatisfied || result.WinnerKey != "winner" {
		t.Fatalf("result=%#v err=%v, want winner", result, err)
	}
	if !waiter.wasCancelled("h-2") {
		t.Fatal("sibling waiter did not observe context cancellation")
	}
}

func testTypedNilNew(t *testing.T) {
	t.Helper()
	var waiter *contractWaiter
	if _, err := New(waiter); !errors.Is(err, ErrNilWaiter) {
		t.Fatalf("New(typed nil) error = %v, want ErrNilWaiter", err)
	}
}

func contractRequest(deadline time.Time, condition Condition, targets ...Target) Request {
	return Request{Targets: targets, Condition: condition, DeadlineAt: deadline, Predicate: Predicate{RequiredEventType: "done"}}
}

func waitAggregate(ctx context.Context, waiter Waiter, request Request) (Result, error) {
	aggregator, err := New(waiter)
	if err != nil {
		return Result{}, err
	}
	return aggregator.Wait(ctx, request)
}

func target(key, handleID string) Target {
	return Target{Key: key, Poll: accesswait.PollRequest{WaitHandleID: handleID, CallerPrincipalRef: "caller", ScopeRef: "scope", GrantRevision: 1}}
}

func successResponses() map[string]fakeWaitResponse {
	return map[string]fakeWaitResponse{
		"h-1": {result: accesswait.WaitResult{Status: "satisfied", Event: &eventstore.AgentEvent{IngressFrame: eventstore.IngressFrame{Envelope: eventstore.EventEnvelope{EventType: "done"}}}}},
		"h-2": {result: accesswait.WaitResult{Status: "satisfied", Event: &eventstore.AgentEvent{IngressFrame: eventstore.IngressFrame{Envelope: eventstore.EventEnvelope{EventType: "done"}}}}},
	}
}

func contentEvent(withContent bool) eventstore.AgentEvent {
	event := eventstore.AgentEvent{IngressFrame: eventstore.IngressFrame{Envelope: eventstore.EventEnvelope{EventType: "done"}}}
	if withContent {
		event.Envelope.ContentRole = "event"
		event.Envelope.ContentRef = "content/ref"
		event.Envelope.ContentSize = 1
		event.Envelope.MediaType = "application/json"
	}
	return event
}
