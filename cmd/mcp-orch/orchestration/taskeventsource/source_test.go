package taskeventsource

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

// TestTaskEventSourceContract 聚合 active binding、selector、cursor 与 recover epoch 合同。
func TestTaskEventSourceContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "active binding", run: TestFromCurrentBindingMapsCompleteActiveFence},
		{name: "unsafe binding", run: TestFromCurrentBindingRejectsIncompleteOrUnsafeBinding},
		{name: "selector", run: TestSourceSelectorIsFixedAndOwnsEventTypes},
		{name: "invalid selector and cursor", run: TestSourceSelectorAndCursorRejectInvalidInputs},
		{name: "recover epoch", run: TestRecoverChangesEpochWithoutRetargetingOldCursor},
		{name: "forged source", run: TestSourceRejectsForgedDerivedTuple},
		{name: "field registry", run: TestSourceFieldMappingRegistryIsExact},
		{name: "one hot mapping", run: TestFromCurrentBindingOneHotMapping},
		{name: "field exemptions", run: TestCurrentBindingExemptionsDoNotLeakIntoSource},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestFromCurrentBindingMapsCompleteActiveFence(t *testing.T) {
	binding := validCurrentBinding()
	got, err := FromCurrentBinding(binding)
	if err != nil {
		t.Fatalf("FromCurrentBinding() error = %v", err)
	}
	want := Source{
		SourceID: "task/agent-alpha", SourceEpoch: "incarnation-alpha", AgentID: "agent-alpha",
		IncarnationID: "incarnation-alpha", BindingVersion: 7, BindingDigest: strings.Repeat("a", 64),
		SourceFenceRef: "source-fence-alpha",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromCurrentBinding() = %#v, want %#v", got, want)
	}
}

func TestFromCurrentBindingRejectsIncompleteOrUnsafeBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*identity.CurrentBinding)
	}{
		{name: "non active", mutate: func(binding *identity.CurrentBinding) { binding.State = "revoked" }},
		{name: "wrong schema", mutate: func(binding *identity.CurrentBinding) { binding.SchemaVersion = 2 }},
		{name: "empty agent", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = "" }},
		{name: "agent slash", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = "agent/escape" }},
		{name: "agent backslash", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = `agent\escape` }},
		{name: "agent control", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = "agent\nalpha" }},
		{name: "agent oversized", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = strings.Repeat("a", 129) }},
		{name: "empty incarnation", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationID = "" }},
		{name: "incarnation slash", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationID = "incarnation/escape" }},
		{name: "incarnation control", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationID = "incarnation\talpha" }},
		{name: "incarnation oversized", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationID = strings.Repeat("i", 129) }},
		{name: "zero binding version", mutate: func(binding *identity.CurrentBinding) { binding.BindingVersion = 0 }},
		{name: "invalid binding digest", mutate: func(binding *identity.CurrentBinding) { binding.BindingDigest = strings.Repeat("A", 64) }},
		{name: "empty fence", mutate: func(binding *identity.CurrentBinding) { binding.SourceFenceRef = "" }},
		{name: "fence control", mutate: func(binding *identity.CurrentBinding) { binding.SourceFenceRef = "fence\x00bad" }},
		{name: "fence newline", mutate: func(binding *identity.CurrentBinding) { binding.SourceFenceRef = "fence\nref" }},
		{name: "fence oversized", mutate: func(binding *identity.CurrentBinding) { binding.SourceFenceRef = strings.Repeat("f", 513) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := validCurrentBinding()
			tt.mutate(&binding)
			got, err := FromCurrentBinding(binding)
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("FromCurrentBinding() error = %v, want ErrInvalidSource", err)
			}
			if got != (Source{}) {
				t.Fatalf("FromCurrentBinding() returned partial source %#v", got)
			}
		})
	}
}

func TestSourceSelectorIsFixedAndOwnsEventTypes(t *testing.T) {
	source := mustSource(t, validCurrentBinding())
	eventTypes := []string{"control", "output"}
	selector, err := source.Selector(eventstore.LaneOutput, "target-alpha", eventTypes)
	if err != nil {
		t.Fatalf("Selector() error = %v", err)
	}
	if selector.Version != 1 || selector.SourceID != source.SourceID || selector.SourceEpoch != source.SourceEpoch ||
		selector.Lane != eventstore.LaneOutput || selector.TargetAgentID != "target-alpha" || !reflect.DeepEqual(selector.EventTypes, eventTypes) {
		t.Fatalf("Selector() = %#v, source = %#v", selector, source)
	}
	eventTypes[0] = "mutated"
	if selector.EventTypes[0] != "control" {
		t.Fatalf("Selector() retained caller event_types backing array: %#v", selector.EventTypes)
	}
}

func TestSourceSelectorAndCursorRejectInvalidInputs(t *testing.T) {
	source := mustSource(t, validCurrentBinding())
	selectorTests := []struct {
		name       string
		lane       eventstore.Lane
		target     string
		eventTypes []string
	}{
		{name: "unknown lane", lane: eventstore.Lane("unknown"), target: "target", eventTypes: []string{"output"}},
		{name: "empty target", lane: eventstore.LaneOutput, eventTypes: []string{"output"}},
		{name: "unsorted events", lane: eventstore.LaneOutput, target: "target", eventTypes: []string{"output", "control"}},
		{name: "duplicate events", lane: eventstore.LaneOutput, target: "target", eventTypes: []string{"output", "output"}},
	}
	for _, tt := range selectorTests {
		t.Run("selector "+tt.name, func(t *testing.T) {
			if _, err := source.Selector(tt.lane, tt.target, tt.eventTypes); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Selector() error = %v, want ErrInvalidSource", err)
			}
		})
	}
	for _, sequence := range []int64{0, -1} {
		if _, err := source.Cursor(eventstore.LaneControl, sequence); !errors.Is(err, ErrInvalidSource) || !errors.Is(err, eventstore.ErrInvalidEvent) {
			t.Fatalf("Cursor(sequence=%d) error = %v, want ErrInvalidSource and ErrInvalidEvent", sequence, err)
		}
	}
	if _, err := source.Cursor(eventstore.Lane("unknown"), 1); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Cursor(unknown lane) error = %v, want ErrInvalidSource", err)
	}
}

func TestRecoverChangesEpochWithoutRetargetingOldCursor(t *testing.T) {
	firstBinding := validCurrentBinding()
	first := mustSource(t, firstBinding)
	oldCursor, err := first.Cursor(eventstore.LaneOutput, 9)
	if err != nil {
		t.Fatalf("first Cursor() error = %v", err)
	}

	recoveredBinding := firstBinding
	recoveredBinding.IncarnationID = "incarnation-recovered"
	recoveredBinding.BindingVersion++
	recoveredBinding.BindingDigest = strings.Repeat("b", 64)
	recoveredBinding.SourceFenceRef = "source-fence-recovered"
	recovered := mustSource(t, recoveredBinding)
	newCursor, err := recovered.Cursor(eventstore.LaneOutput, 9)
	if err != nil {
		t.Fatalf("recovered Cursor() error = %v", err)
	}

	if first.SourceID != recovered.SourceID || first.SourceEpoch == recovered.SourceEpoch {
		t.Fatalf("recover source tuples = %#v then %#v", first, recovered)
	}
	if oldCursor != "task/agent-alpha/incarnation-alpha/output/9" {
		t.Fatalf("old cursor changed after recover: %q", oldCursor)
	}
	if newCursor != "task/agent-alpha/incarnation-recovered/output/9" || newCursor == oldCursor {
		t.Fatalf("new cursor = %q, old cursor = %q", newCursor, oldCursor)
	}
	oldSelector, err := first.Selector(eventstore.LaneOutput, "target-alpha", []string{"output"})
	if err != nil {
		t.Fatalf("old Selector() error = %v", err)
	}
	if oldSelector.SourceEpoch != firstBinding.IncarnationID {
		t.Fatalf("old selector followed recovered epoch: %#v", oldSelector)
	}
}

func TestSourceRejectsForgedDerivedTuple(t *testing.T) {
	source := mustSource(t, validCurrentBinding())
	tests := []struct {
		name   string
		mutate func(*Source)
	}{
		{name: "source id", mutate: func(value *Source) { value.SourceID = "task/other" }},
		{name: "source epoch", mutate: func(value *Source) { value.SourceEpoch = "other-epoch" }},
		{name: "agent slash", mutate: func(value *Source) { value.AgentID = "agent/escape"; value.SourceID = "task/agent/escape" }},
		{name: "incarnation control", mutate: func(value *Source) {
			value.IncarnationID = "incarnation\nother"
			value.SourceEpoch = value.IncarnationID
		}},
		{name: "binding version", mutate: func(value *Source) { value.BindingVersion = 0 }},
		{name: "binding digest", mutate: func(value *Source) { value.BindingDigest = "bad" }},
		{name: "source fence", mutate: func(value *Source) { value.SourceFenceRef = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forged := source
			tt.mutate(&forged)
			if _, err := forged.Cursor(eventstore.LaneControl, 1); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Cursor() error = %v, want ErrInvalidSource", err)
			}
			if _, err := forged.Selector(eventstore.LaneControl, "target", []string{"control"}); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Selector() error = %v, want ErrInvalidSource", err)
			}
		})
	}
}

func mustSource(t *testing.T, binding identity.CurrentBinding) Source {
	t.Helper()
	source, err := FromCurrentBinding(binding)
	if err != nil {
		t.Fatalf("FromCurrentBinding() error = %v", err)
	}
	return source
}

func validCurrentBinding() identity.CurrentBinding {
	return identity.CurrentBinding{
		AgentID: "agent-alpha", BindingVersion: 7, ReservationID: "reservation-alpha",
		IncarnationID: "incarnation-alpha", IdentityKind: "managed_agent", ReservationGeneration: 3,
		IncarnationGeneration: 4, ExternalIdentityRef: "external-alpha", SourceFenceRef: "source-fence-alpha",
		ActiveProcessReceiptRef: "process-alpha", BindingDigest: strings.Repeat("a", 64), State: "active",
		SchemaVersion: 1, UpdatedAt: "2026-09-02T00:00:00Z",
	}
}
