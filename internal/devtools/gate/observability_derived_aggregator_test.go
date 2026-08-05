package gate

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const derivedShardIdentity = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestDurationLedgerDerivedReportBindsFormulaProvenanceAndUnknownFacts(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-formula")
	events := derivedRawEvents(t, record)
	report := mustAggregateDerivedReport(t, events)
	if report.FormulaVersion != DurationLedgerDerivedFormulaVersionV1 {
		t.Fatalf("formula version = %q, want %q", report.FormulaVersion, DurationLedgerDerivedFormulaVersionV1)
	}
	if report.InputProvenance.Authority != "duration_ledger_raw_events" {
		t.Fatalf("provenance authority = %q", report.InputProvenance.Authority)
	}
	requireDerivedProvenance(t, report, events)
	requireDerivedUnknownFacts(t, report)
}

func TestDurationLedgerDerivedReportComputesExactUnionGateAndRun(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-complete")
	report := mustAggregateDerivedReport(t, derivedRawEvents(t, record))
	if report.Completeness.PhaseGateRun.Status != DurationLedgerDerivedKnown {
		t.Fatalf("phase/gate/run completeness = %#v", report.Completeness.PhaseGateRun)
	}

	workloadID := record.WorkloadExecutions[0].GateID
	requireKnownDerivedMetric(t, report, DurationLedgerDerivedMetricScopeWorkload, record.JobID, derivedShardIdentity, workloadID, cicontract.TimingStartup, 5, cicontract.TimingAggregationRaw)
	requireKnownDerivedMetric(t, report, DurationLedgerDerivedMetricScopeShard, record.JobID, derivedShardIdentity, "", cicontract.TimingStartup, 10, cicontract.TimingAggregationIntervalUnion)
	requireKnownDerivedMetric(t, report, DurationLedgerDerivedMetricScopeGate, record.JobID, "", GateIDBackendTestWithGuard, cicontract.TimingStartup, 10, cicontract.TimingAggregationIntervalUnion)
	requireKnownDerivedMetric(t, report, DurationLedgerDerivedMetricScopeRun, record.JobID, "", "", cicontract.TimingTotal, 80, cicontract.TimingAggregationCriticalPath)
	requireKnownMetricDurations(t, report)
}

func TestDurationLedgerDerivedReportPropagatesIncompleteTimingAsUnknown(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-incomplete")
	record.TimingObservations = append([]TimingObservation(nil), record.TimingObservations[:len(record.TimingObservations)-1]...)
	report := mustAggregateDerivedReport(t, derivedRawEvents(t, record))
	if report.Completeness.PhaseGateRun.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("phase/gate/run completeness = %#v", report.Completeness.PhaseGateRun)
	}
	requireUnknownDerivedMetric(t, report, DurationLedgerDerivedMetricScopeRun, record.JobID, "", "", cicontract.TimingTotal)
}

func TestDurationLedgerDerivedReportPreservesTerminalStatusWithoutInferringCosts(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-cancelled")
	record.Status = ResultStatusCancelled
	for index := range record.WorkloadExecutions {
		record.WorkloadExecutions[index].Status = ResultStatusCancelled
	}
	for index := range record.Executions {
		record.Executions[index].Status = ResultStatusCancelled
	}
	report := mustAggregateDerivedReport(t, derivedRawEvents(t, record))
	metric, ok := findDerivedMetric(report, DurationLedgerDerivedMetricScopeRun, record.JobID, "", "", cicontract.TimingTotal)
	if !ok {
		t.Fatal("run status metric is missing")
	}
	if metric.Status != ResultStatusCancelled {
		t.Fatalf("run status = %q", metric.Status)
	}
	if report.RetryCost.Status != DurationLedgerDerivedUnknown || report.CancellationCost.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("costs were inferred: retry=%#v cancellation=%#v", report.RetryCost, report.CancellationCost)
	}
}

func TestDurationLedgerDerivedReportRejectsMalformedConflictingAndTamperedFacts(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-reject")
	events := derivedRawEvents(t, record)

	t.Run("strict payload", func(t *testing.T) {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		payload["unexpected"] = json.RawMessage(`1`)
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		malformed := derivedRawEventFromPayload(t, 1, "", events[0].EventKind, events[0].RunID, events[0].AcceptedGeneration, encoded)
		if _, err := AggregateDurationLedgerDerivedObservations([]DurationLedgerRawObservationEvent{malformed}); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("strict payload error = %v", err)
		}
	})

	t.Run("conflicting snapshots", func(t *testing.T) {
		conflict := record
		conflict.Status = ResultStatusFailed
		second := derivedRawEventFromRecord(t, 2, events[0].EventSHA256, conflict)
		if _, err := AggregateDurationLedgerDerivedObservations([]DurationLedgerRawObservationEvent{events[0], second}); err == nil || !strings.Contains(err.Error(), "conflicting") {
			t.Fatalf("conflicting snapshot error = %v", err)
		}
	})

	t.Run("tampered event", func(t *testing.T) {
		tampered := events[0]
		tampered.PayloadJSON = strings.TrimSuffix(tampered.PayloadJSON, "}") + " }"
		if _, err := AggregateDurationLedgerDerivedObservations([]DurationLedgerRawObservationEvent{tampered}); err == nil || !strings.Contains(err.Error(), "payload sha256 mismatch") {
			t.Fatalf("tamper error = %v", err)
		}
	})
}

func TestDurationLedgerDerivedReportIsDeterministicAndIntervalUnionIsOverflowSafe(t *testing.T) {
	record := completeDerivedRunRecord(t, "derived-order")
	events := derivedRawEvents(t, record)
	reversed := append([]DurationLedgerRawObservationEvent(nil), events...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	first := mustAggregateDerivedReport(t, events)
	second := mustAggregateDerivedReport(t, reversed)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reordered input changed derived output")
	}

	start := time.Unix(0, 0).UTC()
	union := mustDerivedIntervalUnion(t, []DurationLedgerDerivedInterval{
		{Identity: "b", StartedAt: start, CompletedAt: start.Add(2 * time.Millisecond)},
		{Identity: "a", StartedAt: start, CompletedAt: start.Add(time.Millisecond)},
		{Identity: "c", StartedAt: start.Add(4 * time.Millisecond), CompletedAt: start.Add(5 * time.Millisecond)},
	})
	requireDerivedUnion(t, union, start)
	requireDerivedOverflow(t)
}

func mustDerivedIntervalUnion(t *testing.T, intervals []DurationLedgerDerivedInterval) DurationLedgerDerivedMeasurement {
	t.Helper()
	measurement, err := durationLedgerDerivedIntervalUnion(intervals)
	if err != nil {
		t.Fatalf("interval union error = %v", err)
	}
	return measurement
}

func derivedOverflowIntervalError() error {
	_, err := durationLedgerDerivedIntervalUnion([]DurationLedgerDerivedInterval{{Identity: "overflow", StartedAt: time.Unix(math.MinInt64/2, 0), CompletedAt: time.Unix(math.MaxInt64/2, 0)}})
	return err
}

func requireDerivedUnion(t *testing.T, union DurationLedgerDerivedMeasurement, start time.Time) {
	t.Helper()
	if union.DurationMS == nil {
		t.Fatalf("interval union duration is nil")
	}
	if *union.DurationMS != 3 {
		t.Fatalf("interval union duration = %d", *union.DurationMS)
	}
	if union.StartedAt == nil || !union.StartedAt.Equal(start) {
		t.Fatalf("interval union start = %#v", union.StartedAt)
	}
	if union.CompletedAt == nil || !union.CompletedAt.Equal(start.Add(5*time.Millisecond)) {
		t.Fatalf("interval union completion = %#v", union.CompletedAt)
	}
}

func requireDerivedOverflow(t *testing.T) {
	t.Helper()
	if err := derivedOverflowIntervalError(); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("overflow interval error = %v", err)
	}
}

func TestDurationLedgerDerivedStoreDoesNotMutateRawHistory(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	seedAcceptedGenerationForTest(t, store, 1)
	record := completeDerivedRunRecord(t, "derived-store")
	prepareDerivedStore(t, store, &record)
	before := mustLoadDerivedRawEvents(t, store)
	legacyBefore := mustLoadDerivedLegacyReport(t, store)
	if _, err := store.LoadDerivedObservationReport(); err != nil {
		t.Fatalf("LoadDerivedObservationReport() error = %v", err)
	}
	after := mustLoadDerivedRawEvents(t, store)
	legacyAfter := mustLoadDerivedLegacyReport(t, store)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(legacyBefore, legacyAfter) {
		t.Fatalf("derived read changed raw/projection history")
	}
}

func prepareDerivedStore(t *testing.T, store *DurationLedgerStore, record *RemoteCIRunRecord) {
	t.Helper()
	workloads := []Workload{{ID: string(record.WorkloadExecutions[0].GateID), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1, Shardable: true}, {ID: string(record.WorkloadExecutions[1].GateID), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("2", 64), BootstrapEstimateMS: 1, Shardable: true}}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: workloads}
	var err error
	record.CatalogDigest, err = WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: record.SourceTreeSHA, Entrypoint: record.Entrypoint, Profile: record.Profile, AcceptedGeneration: record.AcceptedGeneration, ObservedAt: record.StartedAt}); err != nil {
		t.Fatal(err)
	}
	record.Status = ResultStatusFailed
	if err := store.RecordProvisionalRemoteCIRun(*record); err != nil {
		t.Fatalf("RecordProvisionalRemoteCIRun() error = %v", err)
	}
}

func mustLoadDerivedRawEvents(t *testing.T, store *DurationLedgerStore) []DurationLedgerRawObservationEvent {
	t.Helper()
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func mustLoadDerivedLegacyReport(t *testing.T, store *DurationLedgerStore) DurationLedgerObservationReport {
	t.Helper()
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func mustAggregateDerivedReport(t *testing.T, events []DurationLedgerRawObservationEvent) DurationLedgerDerivedReport {
	t.Helper()
	report, err := AggregateDurationLedgerDerivedObservations(events)
	if err != nil {
		t.Fatalf("AggregateDurationLedgerDerivedObservations() error = %v", err)
	}
	return report
}

func requireDerivedProvenance(t *testing.T, report DurationLedgerDerivedReport, events []DurationLedgerRawObservationEvent) {
	t.Helper()
	if report.InputProvenance.InputDigest == "" {
		t.Fatalf("input provenance digest is empty")
	}
	if len(report.InputProvenance.Events) != len(events) {
		t.Fatalf("input provenance events = %d, want %d", len(report.InputProvenance.Events), len(events))
	}
	if report.InputProvenance.Events[0].EventSHA256 != events[0].EventSHA256 {
		t.Fatalf("event sha provenance drifted")
	}
	if report.InputProvenance.Events[0].PayloadSHA256 != events[0].PayloadSHA256 {
		t.Fatalf("payload sha provenance drifted")
	}
}

func requireDerivedUnknownFacts(t *testing.T, report DurationLedgerDerivedReport) {
	t.Helper()
	if report.Completeness.RetryCost.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("retry completeness = %#v", report.Completeness.RetryCost)
	}
	if report.Completeness.CancellationCost.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("cancellation completeness = %#v", report.Completeness.CancellationCost)
	}
	if report.Completeness.PreV6Completeness.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("pre-v6 completeness = %#v", report.Completeness.PreV6Completeness)
	}
	if report.Completeness.LiveWarningHistory.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("live warning completeness = %#v", report.Completeness.LiveWarningHistory)
	}
	if report.Completeness.UnavailableCapacity.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("capacity completeness = %#v", report.Completeness.UnavailableCapacity)
	}
	if report.RetryCost.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("retry measurement = %#v", report.RetryCost)
	}
	if report.CancellationCost.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("cancellation measurement = %#v", report.CancellationCost)
	}
}

func requireKnownDerivedMetric(t *testing.T, report DurationLedgerDerivedReport, scope DurationLedgerDerivedMetricScope, runID, shard string, workloadOrGate any, phase cicontract.TimingPhase, duration int64, aggregation cicontract.TimingAggregation) {
	t.Helper()
	metric, ok := findDerivedMetric(report, scope, runID, shard, workloadOrGate, phase)
	if !ok {
		t.Fatalf("missing metric scope=%q phase=%q", scope, phase)
	}
	if metric.Measurement.Status != DurationLedgerDerivedKnown {
		t.Fatalf("metric status = %q", metric.Measurement.Status)
	}
	if metric.Measurement.DurationMS == nil {
		t.Fatalf("metric duration is nil: %#v", metric)
	}
	if *metric.Measurement.DurationMS != duration {
		t.Fatalf("metric duration = %d, want %d", *metric.Measurement.DurationMS, duration)
	}
	if metric.Aggregation != aggregation {
		t.Fatalf("metric aggregation = %q, want %q", metric.Aggregation, aggregation)
	}
}

func requireUnknownDerivedMetric(t *testing.T, report DurationLedgerDerivedReport, scope DurationLedgerDerivedMetricScope, runID, shard string, workloadOrGate any, phase cicontract.TimingPhase) {
	t.Helper()
	metric, ok := findDerivedMetric(report, scope, runID, shard, workloadOrGate, phase)
	if !ok {
		t.Fatalf("missing UNKNOWN metric scope=%q phase=%q", scope, phase)
	}
	if metric.Measurement.Status != DurationLedgerDerivedUnknown {
		t.Fatalf("metric status = %q, want UNKNOWN", metric.Measurement.Status)
	}
	if metric.Measurement.DurationMS != nil {
		t.Fatalf("UNKNOWN metric carried duration: %#v", metric)
	}
}

func requireKnownMetricDurations(t *testing.T, report DurationLedgerDerivedReport) {
	t.Helper()
	for _, metric := range report.Metrics {
		if metric.Measurement.Status == DurationLedgerDerivedKnown && metric.Measurement.DurationMS == nil {
			t.Fatalf("known metric has no duration: %#v", metric)
		}
	}
}

func findDerivedMetric(report DurationLedgerDerivedReport, scope DurationLedgerDerivedMetricScope, runID, shard string, workloadOrGate any, phase cicontract.TimingPhase) (DurationLedgerDerivedMetric, bool) {
	for _, metric := range report.Metrics {
		if !derivedMetricSubjectMatches(metric, scope, runID, shard, phase) {
			continue
		}
		if derivedMetricValueMatches(metric, workloadOrGate) {
			return metric, true
		}
	}
	return DurationLedgerDerivedMetric{}, false
}

func derivedMetricSubjectMatches(metric DurationLedgerDerivedMetric, scope DurationLedgerDerivedMetricScope, runID, shard string, phase cicontract.TimingPhase) bool {
	return metric.Scope == scope && metric.RunID == runID && metric.ShardIdentity == shard && metric.Phase == phase
}

func derivedMetricValueMatches(metric DurationLedgerDerivedMetric, value any) bool {
	switch value := value.(type) {
	case GateID:
		return metric.WorkloadID == value || metric.GateID == value
	case string:
		return metric.WorkloadID == GateID(value) || metric.GateID == GateID(value)
	default:
		return false
	}
}

func completeDerivedRunRecord(t *testing.T, jobID string) RemoteCIRunRecord {
	t.Helper()
	base := time.Unix(0, 0).UTC()
	firstWorkload, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoGuard, GoGuardTargetSource)
	if err != nil {
		t.Fatal(err)
	}
	secondWorkload, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoGuard, GoGuardTargetSourceRawGoTest)
	if err != nil {
		t.Fatal(err)
	}
	profile := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 1, StartupMS: 5, TestBodyMS: 15, TotalMS: 20}
	first := PlanGateExecution{ShardIdentity: derivedShardIdentity, GateID: GateID(firstWorkload), Status: ResultStatusPassed, StartedAt: base.Add(20 * time.Millisecond), CompletedAt: base.Add(40 * time.Millisecond), ExecutionProfile: profile}
	second := PlanGateExecution{ShardIdentity: derivedShardIdentity, GateID: GateID(secondWorkload), Status: ResultStatusPassed, StartedAt: base.Add(50 * time.Millisecond), CompletedAt: base.Add(70 * time.Millisecond), ExecutionProfile: profile}
	parent := PlanGateExecution{GateID: GateIDBackendTestWithGuard, Status: ResultStatusPassed, StartedAt: base.Add(20 * time.Millisecond), CompletedAt: base.Add(70 * time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 2, StartupMS: 10, TestBodyMS: 30, TotalMS: 50}}
	return RemoteCIRunRecord{
		JobID: jobID, AgentTokenDigest: "sha256:" + strings.Repeat("a", 64), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		PlanDigest: "sha256:" + strings.Repeat("b", 64), CatalogDigest: "sha256:" + strings.Repeat("c", 64), AcceptedGeneration: 1,
		ImageCacheSnapshotID: "snapshot-1", SourceTreeSHA: strings.Repeat("d", 40), CandidateGateSourceSHA256: "sha256:" + strings.Repeat("e", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("f", 64), RunnerImage: "runner-v1",
		Status: ResultStatusPassed, StartedAt: base, CompletedAt: base.Add(80 * time.Millisecond), CleanupComplete: true,
		Shards:     []RemoteCIShardRecord{{ShardIdentity: derivedShardIdentity, ContainerGroup: "group-1", ContainerStatus: "Succeeded", Workloads: []GateID{first.GateID, second.GateID}, MaterializationTiming: measuredShardMaterializationTiming(derivedShardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 1, MemoryGiB: 1}}},
		Executions: []PlanGateExecution{parent}, WorkloadExecutions: []PlanGateExecution{first, second}, TimingObservations: derivedTimingObservations(jobID, first, second),
	}
}

func derivedTimingObservations(jobID string, first, second PlanGateExecution) []TimingObservation {
	base := time.Unix(0, 0).UTC()
	measured := func(scope cicontract.TimingScope, shard string, workload GateID, phase cicontract.TimingPhase, started, completed time.Time, aggregation cicontract.TimingAggregation, evidence CacheEvidence) TimingObservation {
		return TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shard, WorkloadID: workload, Phase: phase, StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(), Measurement: cicontract.ObservationMeasured, Aggregation: aggregation, CacheEvidence: evidence}
	}
	notApplicable := func(workload GateID) []TimingObservation {
		return []TimingObservation{
			{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: derivedShardIdentity, WorkloadID: workload, Phase: cicontract.TimingECIWait, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + derivedShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)},
			{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: derivedShardIdentity, WorkloadID: workload, Phase: cicontract.TimingSourceMaterialize, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + derivedShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)},
			{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: derivedShardIdentity, WorkloadID: workload, Phase: cicontract.TimingCandidateCompile, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + derivedShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)},
		}
	}
	observations := []TimingObservation{
		measured(cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, base, base.Add(80*time.Millisecond), cicontract.TimingAggregationCriticalPath, NewNotApplicableCacheEvidence("run_has_no_workload_cache")),
		measured(cicontract.TimingScopeShard, derivedShardIdentity, "", cicontract.TimingECIWait, base, base.Add(5*time.Millisecond), cicontract.TimingAggregationRaw, NewNotApplicableCacheEvidence("shard_has_no_workload_cache")),
		measured(cicontract.TimingScopeShard, derivedShardIdentity, "", cicontract.TimingSourceMaterialize, base.Add(5*time.Millisecond), base.Add(10*time.Millisecond), cicontract.TimingAggregationRaw, NewNotApplicableCacheEvidence("shard_has_no_workload_cache")),
		measured(cicontract.TimingScopeShard, derivedShardIdentity, "", cicontract.TimingCandidateCompile, base.Add(10*time.Millisecond), base.Add(20*time.Millisecond), cicontract.TimingAggregationRaw, NewNotApplicableCacheEvidence("shard_has_no_workload_cache")),
		{JobID: jobID, Scope: cicontract.TimingScopeShard, ShardIdentity: derivedShardIdentity, Phase: cicontract.TimingStartup, StartedAt: base.Add(20 * time.Millisecond), CompletedAt: base.Add(55 * time.Millisecond), DurationMS: 10, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationIntervalUnion, CacheEvidence: NewNotApplicableCacheEvidence("shard_has_no_workload_cache")},
		{JobID: jobID, Scope: cicontract.TimingScopeShard, ShardIdentity: derivedShardIdentity, Phase: cicontract.TimingTestBody, StartedAt: base.Add(25 * time.Millisecond), CompletedAt: base.Add(70 * time.Millisecond), DurationMS: 30, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationIntervalUnion, CacheEvidence: NewNotApplicableCacheEvidence("shard_has_no_workload_cache")},
		measured(cicontract.TimingScopeShard, derivedShardIdentity, "", cicontract.TimingTotal, base, base.Add(80*time.Millisecond), cicontract.TimingAggregationCriticalPath, NewNotApplicableCacheEvidence("shard_has_no_workload_cache")),
	}
	observations = append(observations, notApplicable(first.GateID)...)
	observations = append(observations, notApplicable(second.GateID)...)
	observations = append(observations,
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, first.GateID, cicontract.TimingStartup, base.Add(20*time.Millisecond), base.Add(25*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)),
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, first.GateID, cicontract.TimingTestBody, base.Add(25*time.Millisecond), base.Add(40*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)),
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, first.GateID, cicontract.TimingTotal, base.Add(20*time.Millisecond), base.Add(40*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(first.ExecutionProfile)),
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, second.GateID, cicontract.TimingStartup, base.Add(50*time.Millisecond), base.Add(55*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(second.ExecutionProfile)),
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, second.GateID, cicontract.TimingTestBody, base.Add(55*time.Millisecond), base.Add(70*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(second.ExecutionProfile)),
		measured(cicontract.TimingScopeWorkload, derivedShardIdentity, second.GateID, cicontract.TimingTotal, base.Add(50*time.Millisecond), base.Add(70*time.Millisecond), cicontract.TimingAggregationRaw, NewTimingCacheEvidenceFromProfile(second.ExecutionProfile)),
	)
	return observations
}

func derivedRawEvents(t *testing.T, record RemoteCIRunRecord) []DurationLedgerRawObservationEvent {
	t.Helper()
	return []DurationLedgerRawObservationEvent{derivedRawEventFromRecord(t, 1, "", record)}
}

func derivedRawEventFromRecord(t *testing.T, sequence int64, previous string, record RemoteCIRunRecord) DurationLedgerRawObservationEvent {
	t.Helper()
	payload, _, err := marshalDurationLedgerObservationPayload(record)
	if err != nil {
		t.Fatal(err)
	}
	return derivedRawEventFromPayload(t, sequence, previous, durationLedgerObservationEventRemoteRunPersist, record.JobID, "1", payload)
}

func derivedRawEventFromPayload(t *testing.T, sequence int64, previous, kind, runID, generation string, payload []byte) DurationLedgerRawObservationEvent {
	t.Helper()
	payloadDigest := sha256Digest(payload)
	recordedAt := time.Unix(0, sequence*int64(time.Millisecond)).UnixNano()
	eventID, err := canonicalJSONDigest(durationLedgerRawObservationIdentityMaterial{EventKind: kind, RunID: runID, AcceptedGeneration: generation, RecordedAtUnixNS: recordedAt, PayloadSHA256: payloadDigest, PreviousEventSHA256: previous})
	if err != nil {
		t.Fatal(err)
	}
	measurement, err := json.Marshal(DurationLedgerObservationReport{
		LedgerLogicalBytes: knownObservation(1, "test"), LedgerPhysicalBytes: unknownRawObservation("physical"), RecordCount: knownObservation(1, "test"), RunCount: knownObservation(1, "test"), EarliestRecordedAtUnixNS: knownObservation(recordedAt, "test"), LatestRecordedAtUnixNS: knownObservation(recordedAt, "test"), FilesystemAvailableBytes: unknownRawObservation("capacity"),
	})
	if err != nil {
		t.Fatal(err)
	}
	event := DurationLedgerRawObservationEvent{EventSequence: sequence, EventID: eventID, EventKind: kind, RunID: runID, AcceptedGeneration: generation, RecordedAtUnixNS: recordedAt, PayloadJSON: string(payload), PayloadSHA256: payloadDigest, PreviousEventSHA256: previous, MeasurementJSON: string(measurement)}
	event.EventSHA256, err = durationLedgerRawObservationEventDigest(durationLedgerRawObservationPending{eventSequence: sequence, eventID: event.EventID, eventKind: kind, runID: runID, acceptedGeneration: generation, recordedAtUnixNS: recordedAt, payloadJSON: event.PayloadJSON, payloadSHA256: payloadDigest, previousSHA256: previous, measurementJSON: event.MeasurementJSON})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
