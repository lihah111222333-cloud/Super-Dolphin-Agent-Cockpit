package sqliteauthority

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

// TestFinalizationRequestFieldGuardDetectsMissingAndStale 验证生产 DTO 与消费登记的双向差集。
func TestFinalizationRequestFieldGuardDetectsMissingAndStale(t *testing.T) {
	producer := reflectedFieldPaths(reflect.TypeOf(FinalizationRequest{}), "FinalizationRequest")
	registry := finalizationRequestCoverage()
	missing, stale := fieldCoverageDiff(producer, registry)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("baseline coverage missing=%v stale=%v", missing, stale)
	}

	const missingField = "FinalizationRequest.ExpectedResolution.ExpectedHeadRootDigest"
	delete(registry, missingField)
	registry["FinalizationRequest.StaleDigest"] = 99
	missing, stale = fieldCoverageDiff(producer, registry)
	if !reflect.DeepEqual(missing, []string{missingField}) ||
		!reflect.DeepEqual(stale, []string{"FinalizationRequest.StaleDigest"}) {
		t.Fatalf("mutated coverage missing=%v stale=%v", missing, stale)
	}
}

// TestFinalizationObservationFieldGuardDetectsMissingAndStale 动态锁定 observation 的完整生产字段集合。
func TestFinalizationObservationFieldGuardDetectsMissingAndStale(t *testing.T) {
	producer := reflectedFieldPaths(reflect.TypeOf(FinalizationObservation{}), "FinalizationObservation")
	registry := finalizationObservationCoverage()
	missing, stale := fieldCoverageDiff(producer, registry)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("baseline coverage missing=%v stale=%v", missing, stale)
	}

	const missingField = "FinalizationObservation.ReadFenceDigest"
	delete(registry, missingField)
	registry["FinalizationObservation.StaleFence"] = 99
	missing, stale = fieldCoverageDiff(producer, registry)
	if !reflect.DeepEqual(missing, []string{missingField}) ||
		!reflect.DeepEqual(stale, []string{"FinalizationObservation.StaleFence"}) {
		t.Fatalf("mutated coverage missing=%v stale=%v", missing, stale)
	}
}

// finalizationObservationCoverage 登记 observation 的全部生产字段到严格 Validate 消费位点。
func finalizationObservationCoverage() map[string]int {
	return map[string]int{
		"FinalizationObservation.SchemaVersion":             0,
		"FinalizationObservation.State":                     1,
		"FinalizationObservation.FinalizationOperationID":   2,
		"FinalizationObservation.FinalizationRequestDigest": 3,
		"FinalizationObservation.ObservationGeneration":     4,
		"FinalizationObservation.ReadFenceDigest":           5,
		"FinalizationObservation.Evidence":                  6,
	}
}

// TestResolutionMapperRejectsRealExpectedHeadRootOmission 验证真实 scanner 不会漏消费 expected head root 标量。
func TestResolutionMapperRejectsRealExpectedHeadRootOmission(t *testing.T) {
	terminal := terminalIntegrityFixture(t)
	values := scanValues(resolutionScanTargets(&terminal.Resolution))
	values[20] = schemaTestDigest("d")
	if _, err := scanStoredResolution(valueRow(values)); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("scanStoredResolution() error = %v, want ErrSchemaMismatch", err)
	}
}

// fieldCoverageDiff 计算动态生产字段与消费登记之间的 missing/stale 差集。
func fieldCoverageDiff(producer []string, registry map[string]int) ([]string, []string) {
	producerSet := make(map[string]struct{}, len(producer))
	for _, field := range producer {
		producerSet[field] = struct{}{}
	}
	var missing []string
	for _, field := range producer {
		if _, ok := registry[field]; !ok {
			missing = append(missing, field)
		}
	}
	var stale []string
	for field := range registry {
		if _, ok := producerSet[field]; !ok {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
