package terminaleventbridge

import (
	"context"
	"errors"
	"reflect"
	"testing"

	runtimeeventwriter "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/runtimeeventwriter"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type bridgeVerifier struct {
	err     error
	calls   int
	binding Binding
	txCalls int
}

func (v *bridgeVerifier) VerifyTerminalBindingTx(_ context.Context, _ storedb.DBTX, binding Binding) error {
	v.calls++
	v.txCalls++
	v.binding = binding
	return v.err
}

type bridgeIngress struct {
	event  runtimeeventwriter.RuntimeEvent
	calls  int
	result eventstore.AppendResult
	err    error
}

type bridgeTaskIngress struct {
	event  taskeventsource.RuntimeEvent
	calls  int
	result eventstore.AppendResult
}

func (i *bridgeTaskIngress) Append(_ context.Context, event taskeventsource.RuntimeEvent) (eventstore.AppendResult, error) {
	i.calls++
	i.event = event
	return i.result, nil
}

type bridgeNotifier struct{ calls int }

func (n *bridgeNotifier) Notify(eventstore.AgentEvent) error {
	n.calls++
	return nil
}

func (i *bridgeIngress) Append(_ context.Context, event runtimeeventwriter.RuntimeEvent) (eventstore.AppendResult, error) {
	i.calls++
	i.event = event
	return i.result, i.err
}

func (i *bridgeIngress) AppendBound(ctx context.Context, event runtimeeventwriter.RuntimeEvent, verifier runtimeeventwriter.BindingVerifierTx) (eventstore.AppendResult, error) {
	i.calls++
	i.event = event
	if err := verifier.VerifyTerminalBindingTx(ctx, nil, event.Binding); err != nil {
		return eventstore.AppendResult{}, err
	}
	return i.result, i.err
}

func bridgeObservation() Observation {
	return Observation{
		Binding: Binding{AgentID: "agent-1", IncarnationID: "inc-1", IncarnationGeneration: 2, Provider: "codex", ProviderSessionID: "session-1", LaunchSequence: 7},
		EventID: "event-1", EventType: EventTurnCompleted, Lane: eventstore.LaneOutput,
		WriterEpoch: "writer-epoch-3", Provenance: "provider:codex", TrustClass: "observed", ContentRole: "none", ObservedAt: "2026-09-04T00:00:00Z",
	}
}

func materializedResult() eventstore.AppendResult {
	frame := eventstore.IngressFrame{SourceID: "task/agent-1", SourceEpoch: "inc-1", Lane: eventstore.LaneOutput, SourceSequence: 1, CanonicalCursor: "task/agent-1/inc-1/output/1"}
	frame.EnvelopeHash = mustCanonicalEnvelopeHash(frame.Envelope)
	return eventstore.AppendResult{Receipt: eventstore.IngressReceipt{IngressFrame: frame, Status: "materialized"}, Event: &eventstore.AgentEvent{IngressFrame: frame, Status: "materialized"}, State: eventstore.SourceState{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: frame.Lane, NextSequence: 2, MaterializedSequence: 1, Revision: 1, Status: "active"}}
}

func mustCanonicalEnvelopeHash(envelope eventstore.EventEnvelope) string {
	hash, err := eventstore.CanonicalEnvelopeHash(envelope)
	if err != nil {
		// archguard:ignore panic_count -- fixture construction cannot continue without a canonical hash.
		panic(err)
	}
	return hash
}

func TestBridgeSubmitsOnlyAfterBindingVerification(t *testing.T) {
	observation := bridgeObservation()
	ingress := &bridgeIngress{result: materializedResult()}
	verifier := &bridgeVerifier{}
	bridge, err := New(ingress, verifier)
	if err != nil {
		t.Fatal(err)
	}
	result, err := bridge.Submit(t.Context(), observation)
	assertBridgeSubmitResult(t, result, err)
	assertBridgeBindingVerification(t, verifier, observation.Binding)
	assertBridgeIngressCall(t, ingress)
	assertBridgeRuntimeEvent(t, ingress.event, observation)
}

func assertBridgeSubmitResult(t *testing.T, result eventstore.AppendResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Event == nil {
		t.Fatalf("Submit() result = %#v, want materialized event", result)
	}
}

func assertBridgeBindingVerification(t *testing.T, verifier *bridgeVerifier, want Binding) {
	t.Helper()
	if verifier.calls != 1 || verifier.txCalls != 1 {
		t.Fatalf("binding verifier calls = %d/%d, want 1/1", verifier.calls, verifier.txCalls)
	}
	if !reflect.DeepEqual(verifier.binding, want) {
		t.Fatalf("verified binding = %#v, want %#v", verifier.binding, want)
	}
}

func assertBridgeIngressCall(t *testing.T, ingress *bridgeIngress) {
	t.Helper()
	if ingress.calls != 1 {
		t.Fatalf("ingress calls = %d, want 1", ingress.calls)
	}
}

func assertBridgeRuntimeEvent(t *testing.T, got runtimeeventwriter.RuntimeEvent, want Observation) {
	t.Helper()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "binding", got: got.Binding, want: want.Binding},
		{name: "agent id", got: got.AgentID, want: want.Binding.AgentID},
		{name: "lane", got: got.Lane, want: want.Lane},
		{name: "event id", got: got.EventID, want: want.EventID},
		{name: "event type", got: got.EventType, want: want.EventType},
		{name: "provenance", got: got.Provenance, want: want.Provenance},
		{name: "trust class", got: got.TrustClass, want: want.TrustClass},
		{name: "content role", got: got.ContentRole, want: want.ContentRole},
		{name: "content ref", got: got.ContentRef, want: want.ContentRef},
		{name: "content size", got: got.ContentSize, want: want.ContentSize},
		{name: "media type", got: got.MediaType, want: want.MediaType},
		{name: "provider at", got: got.ProviderAt, want: want.ProviderAt},
		{name: "observed at", got: got.ObservedAt, want: want.ObservedAt},
		{name: "writer epoch", got: got.WriterEpoch, want: want.WriterEpoch},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("mapped runtime event %s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
}

func TestBridgeUsesTaskIngressAdapter(t *testing.T) {
	ingress := &bridgeIngress{result: materializedResult()}
	notifier := &bridgeNotifier{}
	adapter, err := runtimeeventwriter.NewCandidateIngressAdapter(ingress, notifier)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := New(adapter, &bridgeVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Submit(t.Context(), bridgeObservation()); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if ingress.calls != 1 || notifier.calls != 1 {
		t.Fatalf("ingress/notifier calls = %d/%d, want 1/1", ingress.calls, notifier.calls)
	}
}

func TestBridgeClassifiesFenceFailuresWithoutRetry(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "late", err: runtimeeventwriter.ErrStale, want: ErrLateGeneration},
		{name: "gap", err: eventstore.ErrEventGap, want: ErrGap},
		{name: "unknown", err: runtimeeventwriter.ErrCommitUnknown, want: ErrOutcomeUnknown},
		{name: "duplicate", err: eventstore.ErrEventConflict, want: ErrDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ingress := &bridgeIngress{result: materializedResult(), err: tc.err}
			bridge, err := New(ingress, &bridgeVerifier{})
			if err != nil {
				t.Fatal(err)
			}
			_, got := bridge.Submit(t.Context(), bridgeObservation())
			if !errors.Is(got, tc.want) {
				t.Fatalf("error = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBridgeRejectsMissingExplicitFields(t *testing.T) {
	bridge, err := New(&bridgeIngress{result: materializedResult()}, &bridgeVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Observation){
		"event id":         func(o *Observation) { o.EventID = "" },
		"writer epoch":     func(o *Observation) { o.WriterEpoch = "" },
		"trust":            func(o *Observation) { o.TrustClass = "" },
		"content role":     func(o *Observation) { o.ContentRole = "" },
		"provider session": func(o *Observation) { o.Binding.ProviderSessionID = "" },
		"launch sequence":  func(o *Observation) { o.Binding.LaunchSequence = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			observation := bridgeObservation()
			mutate(&observation)
			if _, err := bridge.Submit(t.Context(), observation); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Submit() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestBridgeRejectsUnboundedOrNonUTCFields(t *testing.T) {
	bridge, err := New(&bridgeIngress{result: materializedResult()}, &bridgeVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []func(*Observation){
		func(o *Observation) { o.EventID = "bad\nline" },
		func(o *Observation) { o.ObservedAt = "2026-09-04T09:00:00+09:00" },
		func(o *Observation) { o.ContentSize = 256*1024 + 1 },
	}
	for _, mutate := range cases {
		observation := bridgeObservation()
		mutate(&observation)
		if _, err := bridge.Submit(t.Context(), observation); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Submit() error = %v, want bounded-field rejection", err)
		}
	}
}

func TestBridgeIsUnavailableWithoutDurableVerifier(t *testing.T) {
	if _, err := New(&bridgeIngress{result: materializedResult()}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New() error = %v, want ErrUnavailable", err)
	}
}

func TestDTOAdaptersRemainDisabledWhenFieldsAreMissing(t *testing.T) {
	if _, err := AdaptTurnCompleted(turndto.TurnCompleted{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("AdaptTurnCompleted() error = %v, want ErrUnavailable", err)
	}
	if _, err := AdaptTurnInterrupted(turndto.TurnInterrupted{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("AdaptTurnInterrupted() error = %v, want ErrUnavailable", err)
	}
}

func TestBridgeRejectsNonAtomicIngress(t *testing.T) {
	if _, err := New(&plainBridgeIngress{result: materializedResult()}, &bridgeVerifier{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New() error = %v, want ErrUnavailable", err)
	}
}

func TestBridgeRejectsContextOnlyVerifier(t *testing.T) {
	if _, err := New(&bridgeIngress{result: materializedResult()}, &contextOnlyVerifier{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New() error = %v, want ErrUnavailable", err)
	}
}

func TestBridgeAtomicVerifierRunsBeforeAppend(t *testing.T) {
	verifier := &bridgeVerifier{}
	ingress := &orderedBridgeIngress{result: materializedResult(), verifier: verifier}
	bridge, err := New(ingress, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Submit(t.Context(), bridgeObservation()); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got, want := ingress.order, "verify,append"; got != want {
		t.Fatalf("atomic order = %q, want %q", got, want)
	}
}

func TestBridgeAtomicVerifierRejectsRolloverBeforeAppend(t *testing.T) {
	verifier := &bridgeVerifier{err: runtimeeventwriter.ErrStale}
	ingress := &orderedBridgeIngress{result: materializedResult(), verifier: verifier}
	bridge, err := New(ingress, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Submit(t.Context(), bridgeObservation()); !errors.Is(err, ErrLateGeneration) {
		t.Fatalf("Submit() error = %v, want ErrLateGeneration", err)
	}
	if ingress.order != "verify" {
		t.Fatalf("append order after rollover = %q, want verify only", ingress.order)
	}
}

func TestBridgeRejectsForgedMaterializedReceipt(t *testing.T) {
	result := materializedResult()
	result.Receipt.SourceSequence++
	ingress := &bridgeIngress{result: result}
	verifier := &bridgeVerifier{}
	bridge, err := New(ingress, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Submit(t.Context(), bridgeObservation()); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("Submit() error = %v, want ErrOutcomeUnknown", err)
	}
}

type plainBridgeIngress struct{ result eventstore.AppendResult }

type contextOnlyVerifier struct{}

func (*contextOnlyVerifier) VerifyTerminalBinding(context.Context, Binding) error { return nil }

func (i *plainBridgeIngress) Append(_ context.Context, _ runtimeeventwriter.RuntimeEvent) (eventstore.AppendResult, error) {
	return i.result, nil
}

type orderedBridgeIngress struct {
	result   eventstore.AppendResult
	verifier *bridgeVerifier
	order    string
}

func (i *orderedBridgeIngress) Append(_ context.Context, _ runtimeeventwriter.RuntimeEvent) (eventstore.AppendResult, error) {
	i.order += ",append"
	return i.result, nil
}

func (i *orderedBridgeIngress) AppendBound(ctx context.Context, event runtimeeventwriter.RuntimeEvent, verifier runtimeeventwriter.BindingVerifierTx) (eventstore.AppendResult, error) {
	i.order = "verify"
	if err := verifier.VerifyTerminalBindingTx(ctx, nil, event.Binding); err != nil {
		return eventstore.AppendResult{}, err
	}
	return i.Append(ctx, event)
}
