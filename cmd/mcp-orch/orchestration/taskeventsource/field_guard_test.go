package taskeventsource

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

func TestSourceFieldMappingRegistryIsExact(t *testing.T) {
	sourceType := reflect.TypeFor[Source]()
	bindingType := reflect.TypeFor[identity.CurrentBinding]()
	sourceProducers := map[string]string{
		"SourceID": "AgentID", "SourceEpoch": "IncarnationID", "AgentID": "AgentID",
		"IncarnationID": "IncarnationID", "BindingVersion": "BindingVersion",
		"BindingDigest": "BindingDigest", "SourceFenceRef": "SourceFenceRef",
	}
	bindingConsumers := map[string]string{
		"AgentID": "source identity and task source id", "IncarnationID": "source incarnation and epoch",
		"BindingVersion": "binding fence", "BindingDigest": "binding fence", "SourceFenceRef": "binding fence",
		"State": "active discriminator", "SchemaVersion": "schema v1 discriminator",
	}
	bindingExemptions := map[string]string{
		"ReservationID":           "source retains the finalized binding fence instead of reservation metadata",
		"IdentityKind":            "identity owner already validated the active binding kind",
		"ReservationGeneration":   "event envelope verifier owns reservation generation checks",
		"IncarnationGeneration":   "source epoch is the immutable incarnation id",
		"ExternalIdentityRef":     "provider identity is outside the canonical event source tuple",
		"ActiveProcessReceiptRef": "process receipt is verified by the event fence verifier",
		"UpdatedAt":               "observation time does not identify an immutable event source",
	}
	assertSourceProducerRegistry(t, sourceType, bindingType, sourceProducers)
	assertBindingConsumerRegistry(t, bindingType, bindingConsumers, bindingExemptions)
}

func TestFromCurrentBindingOneHotMapping(t *testing.T) {
	baselineBinding := validCurrentBinding()
	baseline := mustSource(t, baselineBinding)
	tests := []struct {
		name       string
		mutate     func(*identity.CurrentBinding)
		wantFields []string
	}{
		{name: "agent", mutate: func(binding *identity.CurrentBinding) { binding.AgentID = "agent-beta" }, wantFields: []string{"SourceID", "AgentID"}},
		{name: "incarnation", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationID = "incarnation-beta" }, wantFields: []string{"SourceEpoch", "IncarnationID"}},
		{name: "binding version", mutate: func(binding *identity.CurrentBinding) { binding.BindingVersion++ }, wantFields: []string{"BindingVersion"}},
		{name: "binding digest", mutate: func(binding *identity.CurrentBinding) { binding.BindingDigest = strings.Repeat("b", 64) }, wantFields: []string{"BindingDigest"}},
		{name: "source fence", mutate: func(binding *identity.CurrentBinding) { binding.SourceFenceRef = "source-fence-beta" }, wantFields: []string{"SourceFenceRef"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := baselineBinding
			tt.mutate(&binding)
			got := mustSource(t, binding)
			if fields := changedSourceFields(baseline, got); !reflect.DeepEqual(fields, tt.wantFields) {
				t.Fatalf("changed fields = %#v, want %#v", fields, tt.wantFields)
			}
		})
	}
}

func TestCurrentBindingExemptionsDoNotLeakIntoSource(t *testing.T) {
	baselineBinding := validCurrentBinding()
	baseline := mustSource(t, baselineBinding)
	tests := []struct {
		name   string
		mutate func(*identity.CurrentBinding)
	}{
		{name: "reservation id", mutate: func(binding *identity.CurrentBinding) { binding.ReservationID = "reservation-beta" }},
		{name: "identity kind", mutate: func(binding *identity.CurrentBinding) { binding.IdentityKind = "native_subagent" }},
		{name: "reservation generation", mutate: func(binding *identity.CurrentBinding) { binding.ReservationGeneration++ }},
		{name: "incarnation generation", mutate: func(binding *identity.CurrentBinding) { binding.IncarnationGeneration++ }},
		{name: "external identity", mutate: func(binding *identity.CurrentBinding) { binding.ExternalIdentityRef = "external-beta" }},
		{name: "process receipt", mutate: func(binding *identity.CurrentBinding) { binding.ActiveProcessReceiptRef = "process-beta" }},
		{name: "updated at", mutate: func(binding *identity.CurrentBinding) { binding.UpdatedAt = "2026-09-03T00:00:00Z" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := baselineBinding
			tt.mutate(&binding)
			if got := mustSource(t, binding); got != baseline {
				t.Fatalf("exempt binding field leaked into Source: got %#v, want %#v", got, baseline)
			}
		})
	}
}

func assertSourceProducerRegistry(t *testing.T, sourceType, bindingType reflect.Type, registry map[string]string) {
	t.Helper()
	for field := range sourceType.Fields() {
		producer, ok := registry[field.Name]
		if !ok {
			t.Fatalf("Source.%s is missing from producer registry", field.Name)
		}
		bindingField, ok := bindingType.FieldByName(producer)
		if !ok {
			t.Fatalf("Source.%s producer identity.CurrentBinding.%s is stale", field.Name, producer)
		}
		if field.Name != "SourceID" && field.Name != "SourceEpoch" && field.Type != bindingField.Type {
			t.Fatalf("Source.%s type = %s, producer %s type = %s", field.Name, field.Type, producer, bindingField.Type)
		}
	}
	for field := range registry {
		if _, ok := sourceType.FieldByName(field); !ok {
			t.Fatalf("producer registry contains stale Source.%s", field)
		}
	}
}

func assertBindingConsumerRegistry(t *testing.T, bindingType reflect.Type, consumers, exemptions map[string]string) {
	t.Helper()
	for field := range bindingType.Fields() {
		name := field.Name
		reason, consumed := consumers[name]
		if !consumed {
			reason = exemptions[name]
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("identity.CurrentBinding.%s has no task event source consumer or exemption", name)
		}
	}
	for name := range consumers {
		if _, ok := bindingType.FieldByName(name); !ok {
			t.Fatalf("consumer registry contains stale identity.CurrentBinding.%s", name)
		}
		if _, duplicate := exemptions[name]; duplicate {
			t.Fatalf("identity.CurrentBinding.%s is both consumed and exempt", name)
		}
	}
	for name := range exemptions {
		if _, ok := bindingType.FieldByName(name); !ok {
			t.Fatalf("exemption registry contains stale identity.CurrentBinding.%s", name)
		}
	}
}

func changedSourceFields(before, after Source) []string {
	beforeValue := reflect.ValueOf(before)
	afterValue := reflect.ValueOf(after)
	typ := beforeValue.Type()
	changed := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		if !reflect.DeepEqual(beforeValue.Field(index).Interface(), afterValue.Field(index).Interface()) {
			changed = append(changed, typ.Field(index).Name)
		}
	}
	return changed
}
