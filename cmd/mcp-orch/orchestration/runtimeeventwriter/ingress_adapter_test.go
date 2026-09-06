package runtimeeventwriter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

type adapterIngress struct {
	result eventstore.AppendResult
	err    error
	calls  int
	commit func()
}

func (i *adapterIngress) Append(context.Context, Event) (eventstore.AppendResult, error) {
	i.calls++
	if i.commit != nil {
		i.commit()
	}
	return i.result, i.err
}

type adapterTaskIngress struct {
	result eventstore.AppendResult
	err    error
	calls  int
}

func (i *adapterTaskIngress) Append(context.Context, taskeventsource.RuntimeEvent) (eventstore.AppendResult, error) {
	i.calls++
	return i.result, i.err
}

type adapterNotifier struct {
	events    []eventstore.AgentEvent
	committed bool
	err       error
}

func (n *adapterNotifier) Notify(event eventstore.AgentEvent) error {
	if !n.committed {
		return errors.New("notification attempted before commit")
	}
	n.events = append(n.events, event)
	return n.err
}

func adapterResult() eventstore.AppendResult {
	frame := eventstore.IngressFrame{SourceID: "task/adapter", SourceEpoch: "inc-1", Lane: eventstore.LaneOutput, SourceSequence: 1,
		CanonicalCursor: "task/adapter/inc-1/output/1"}
	frame.EnvelopeHash = mustCanonicalEnvelopeHash(frame.Envelope)
	return eventstore.AppendResult{Receipt: eventstore.IngressReceipt{IngressFrame: frame, Status: "materialized"}, Event: &eventstore.AgentEvent{IngressFrame: frame, Status: "materialized"}, State: eventstore.SourceState{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: frame.Lane, NextSequence: 2, MaterializedSequence: 1, Revision: 2, Status: "active"}}
}

func mustCanonicalEnvelopeHash(envelope eventstore.EventEnvelope) string {
	hash, err := eventstore.CanonicalEnvelopeHash(envelope)
	if err != nil {
		// archguard:ignore panic_count -- fixture construction cannot continue without a canonical hash.
		panic(err)
	}
	return hash
}

func TestCandidateIngressAdapterNotifiesAfterSuccessfulAppend(t *testing.T) {
	notifier := &adapterNotifier{}
	ingress := &adapterIngress{result: adapterResult(), commit: func() { notifier.committed = true }}
	adapter, err := NewCandidateIngressAdapter(ingress, notifier)
	if err != nil {
		t.Fatalf("NewCandidateIngressAdapter() error = %v", err)
	}
	result, err := adapter.Append(t.Context(), Event{AgentID: "adapter", Lane: eventstore.LaneOutput})
	if err != nil || result.Event == nil {
		t.Fatalf("Append() = %#v, error=%v, want materialized result", result, err)
	}
	if ingress.calls != 1 || len(notifier.events) != 1 {
		t.Fatalf("calls/events = %d/%d, want 1/1", ingress.calls, len(notifier.events))
	}
}

func TestCandidateIngressAdapterRejectsForgedMaterializedResult(t *testing.T) {
	result := adapterResult()
	result.Event.EnvelopeHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ingress := &adapterIngress{result: result}
	adapter, err := NewCandidateIngressAdapter(ingress, &adapterNotifier{committed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Append(t.Context(), Event{AgentID: "adapter", Lane: eventstore.LaneOutput}); !errors.Is(err, ErrIngressAdapterInvalid) {
		t.Fatalf("Append() error = %v, want forged result rejection", err)
	}
}

func TestValidateMaterializedResultRejectsCanonicalLookingWrongHash(t *testing.T) {
	result := adapterResult()
	result.Receipt.EnvelopeHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	result.Event.EnvelopeHash = result.Receipt.EnvelopeHash
	if err := ValidateMaterializedResult(result); !errors.Is(err, ErrIngressAdapterInvalid) {
		t.Fatalf("ValidateMaterializedResult() error = %v, want canonical hash rejection", err)
	}
}

func TestValidateMaterializedResultAcceptsCanonicalHash(t *testing.T) {
	if err := ValidateMaterializedResult(adapterResult()); err != nil {
		t.Fatalf("ValidateMaterializedResult() error = %v, want success", err)
	}
}

func TestCandidateIngressAdapterDoesNotNotifyGapOrUnknownCommit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		fence bool
	}{
		{name: "gap", err: errors.Join(ErrGap, eventstore.ErrEventGap)},
		{name: "unknown", err: ErrCommitUnknown, fence: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ingress := &adapterIngress{result: adapterResult(), err: tc.err}
			notifier := &adapterNotifier{committed: true}
			adapter, err := NewCandidateIngressAdapter(ingress, notifier)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := adapter.Append(t.Context(), Event{AgentID: "adapter", Lane: eventstore.LaneOutput}); result != (eventstore.AppendResult{}) || err == nil {
				t.Fatalf("Append() = %#v, error=%v, want zero result and error", result, err)
			}
			if len(notifier.events) != 0 {
				t.Fatalf("notifications = %d, want 0", len(notifier.events))
			}
			if tc.fence {
				if _, err := adapter.Append(t.Context(), Event{AgentID: "adapter", Lane: eventstore.LaneOutput}); !errors.Is(err, ErrIngressAdapterFenced) {
					t.Fatalf("replay error = %v, want adapter fence", err)
				}
			}
		})
	}
}

func TestCandidateIngressAdapterPreservesCommittedResultOnNotifyFailure(t *testing.T) {
	notifierErr := errors.New("watcher unavailable")
	ingress := &adapterIngress{result: adapterResult()}
	notifier := &adapterNotifier{committed: true, err: notifierErr}
	adapter, err := NewCandidateIngressAdapter(ingress, notifier)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Append(t.Context(), Event{AgentID: "adapter", Lane: eventstore.LaneOutput})
	if !errors.Is(err, ErrIngressAdapterNotify) || !errors.Is(err, notifierErr) {
		t.Fatalf("Append() error = %v, want notification error", err)
	}
	if result.Event == nil || result.Receipt.Status != "materialized" {
		t.Fatalf("committed result = %#v, want preserved materialized result", result)
	}
}

func TestCandidateIngressAdapterRejectsInvalidConstruction(t *testing.T) {
	var typedNil *adapterIngress
	var typedNilNotifier *adapterNotifier
	if _, err := NewCandidateIngressAdapter(typedNil, &adapterNotifier{}); !errors.Is(err, ErrIngressAdapterInvalid) {
		t.Fatalf("typed-nil ingress error = %v, want invalid", err)
	}
	if _, err := NewCandidateIngressAdapter(&adapterIngress{}, typedNilNotifier); !errors.Is(err, ErrIngressAdapterInvalid) {
		t.Fatalf("typed-nil notifier error = %v, want invalid", err)
	}
}

func TestTaskCandidateIngressAdapterUsesTaskPort(t *testing.T) {
	ingress := &adapterTaskIngress{result: adapterResult()}
	notifier := &adapterNotifier{committed: true}
	adapter, err := NewTaskCandidateIngressAdapter(ingress, notifier)
	if err != nil {
		t.Fatalf("NewTaskCandidateIngressAdapter() error = %v", err)
	}
	if result, err := adapter.AppendTask(t.Context(), taskeventsource.RuntimeEvent{AgentID: "adapter", Lane: eventstore.LaneOutput}); err != nil || result.Event == nil {
		t.Fatalf("AppendTask() = %#v, error=%v, want materialized result", result, err)
	}
	if ingress.calls != 1 || len(notifier.events) != 1 {
		t.Fatalf("task calls/events = %d/%d, want 1/1", ingress.calls, len(notifier.events))
	}
}

func TestCandidateIngressAdapterRealStoresAllocatesAndNotifiesAfterCommit(t *testing.T) {
	db := openRealWriterDB(t, filepath.Join(t.TempDir(), "adapter-real.sqlite"))
	t.Cleanup(func() { _ = db.Close() })
	identityStore := mustRealIdentityStore(t, db)
	active := seedRealActiveIdentity(t, identityStore)
	eventStore := mustRealEventStore(t, db, identityStore)
	writer := mustRealWriter(t, eventStore, identityStore)
	notifier := &adapterNotifier{committed: true}
	adapter, err := NewCandidateIngressAdapter(writer, notifier)
	if err != nil {
		t.Fatalf("NewCandidateIngressAdapter() error = %v", err)
	}
	result, err := adapter.Append(t.Context(), realRuntimeEvent(active.AgentID, "adapter-real-event"))
	if err != nil || result.Event == nil || result.Receipt.SourceSequence != 1 {
		t.Fatalf("Append() = %#v, error=%v, want materialized sequence 1", result, err)
	}
	if len(notifier.events) != 1 || notifier.events[0].SourceSequence != 1 || notifier.events[0].SourceID != "task/"+active.AgentID {
		t.Fatalf("post-commit events = %#v, want one bound sequence-1 event", notifier.events)
	}
}
