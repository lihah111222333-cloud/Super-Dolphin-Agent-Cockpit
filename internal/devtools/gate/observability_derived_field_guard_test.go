package gate

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestDurationLedgerDerivedJSONFieldRegistryMatchesProducers 固定派生报告全部 JSON producer 字段。
func TestDurationLedgerDerivedJSONFieldRegistryMatchesProducers(t *testing.T) {
	t.Parallel()

	registries := []struct {
		producer reflect.Type
		fields   []string
	}{
		{reflect.TypeFor[DurationLedgerDerivedMeasurement](), []string{"status", "started_at", "completed_at", "duration_ms", "reason"}},
		{reflect.TypeFor[DurationLedgerDerivedFact](), []string{"status", "reason"}},
		{reflect.TypeFor[DurationLedgerDerivedCompleteness](), []string{"overall", "phase_gate_run", "retry_cost", "cancellation_cost", "pre_v6_completeness", "stored_formula_version", "live_warning_history", "unavailable_capacity"}},
		{reflect.TypeFor[DurationLedgerDerivedInputEvent](), []string{"event_sequence", "event_id", "event_kind", "run_id", "accepted_generation", "recorded_at_unix_ns", "payload_sha256", "previous_event_sha256", "event_sha256"}},
		{reflect.TypeFor[DurationLedgerDerivedInputProvenance](), []string{"authority", "input_digest", "events"}},
		{reflect.TypeFor[DurationLedgerDerivedMetric](), []string{"scope", "run_id", "status", "gate_id", "shard_identity", "workload_id", "phase", "aggregation", "measurement"}},
		{reflect.TypeFor[DurationLedgerDerivedReport](), []string{"formula_version", "input_provenance", "completeness", "retry_cost", "cancellation_cost", "metrics"}},
	}
	for _, registry := range registries {
		assertExactDerivedJSONFields(t, registry.producer, registry.fields)
	}
}

func assertExactDerivedJSONFields(t *testing.T, producer reflect.Type, registered []string) {
	t.Helper()
	actual := make([]string, 0, producer.NumField())
	for field := range producer.Fields() {
		name := strings.TrimSpace(strings.SplitN(field.Tag.Get("json"), ",", 2)[0])
		if name == "" || name == "-" {
			t.Fatalf("%s.%s must declare a JSON field", producer, field.Name)
		}
		actual = append(actual, name)
	}
	sort.Strings(actual)
	want := append([]string(nil), registered...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("%s JSON field registry drifted: got=%v want=%v", producer, actual, want)
	}
}
