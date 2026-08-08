package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// DurationLedgerDerivedFormulaVersionV1 is the compile-time formula identity.
// It is deliberately independent from the raw ledger schema and producer versions.
const DurationLedgerDerivedFormulaVersionV1 = "local-observability-derived/v1"

const durationLedgerDerivedAuthority = "duration_ledger_raw_events"

// DurationLedgerDerivedStatus distinguishes a mechanically proven value from an
// unavailable fact. UNKNOWN never uses a zero duration as an implicit value.
type DurationLedgerDerivedStatus string

const (
	DurationLedgerDerivedKnown   DurationLedgerDerivedStatus = "KNOWN"
	DurationLedgerDerivedUnknown DurationLedgerDerivedStatus = "UNKNOWN"
)

// DurationLedgerDerivedMetricScope identifies the exact subject of a derived metric.
type DurationLedgerDerivedMetricScope string

const (
	DurationLedgerDerivedMetricScopeWorkload DurationLedgerDerivedMetricScope = "workload"
	DurationLedgerDerivedMetricScopeShard    DurationLedgerDerivedMetricScope = "shard"
	DurationLedgerDerivedMetricScopeGate     DurationLedgerDerivedMetricScope = "gate"
	DurationLedgerDerivedMetricScopeRun      DurationLedgerDerivedMetricScope = "run"
)

// DurationLedgerDerivedMeasurement is a typed time interval. A known value
// always carries its real interval and duration; an unknown value carries only
// a reason.
type DurationLedgerDerivedMeasurement struct {
	Status      DurationLedgerDerivedStatus `json:"status"`
	StartedAt   *time.Time                  `json:"started_at,omitempty"`
	CompletedAt *time.Time                  `json:"completed_at,omitempty"`
	DurationMS  *int64                      `json:"duration_ms,omitempty"`
	Reason      string                      `json:"reason,omitempty"`
}

// DurationLedgerDerivedFact is the typed completeness state of one fact family.
type DurationLedgerDerivedFact struct {
	Status DurationLedgerDerivedStatus `json:"status"`
	Reason string                      `json:"reason,omitempty"`
}

// DurationLedgerDerivedCompleteness records the facts that the V1 consumer can
// and cannot prove from the open raw event contract.
type DurationLedgerDerivedCompleteness struct {
	Overall              DurationLedgerDerivedFact `json:"overall"`
	PhaseGateRun         DurationLedgerDerivedFact `json:"phase_gate_run"`
	RetryCost            DurationLedgerDerivedFact `json:"retry_cost"`
	CancellationCost     DurationLedgerDerivedFact `json:"cancellation_cost"`
	PreV6Completeness    DurationLedgerDerivedFact `json:"pre_v6_completeness"`
	StoredFormulaVersion DurationLedgerDerivedFact `json:"stored_formula_version"`
	LiveWarningHistory   DurationLedgerDerivedFact `json:"live_warning_history"`
	UnavailableCapacity  DurationLedgerDerivedFact `json:"unavailable_capacity"`
}

// DurationLedgerDerivedInputEvent binds one output to the exact immutable raw
// event identity accepted by this formula.
type DurationLedgerDerivedInputEvent struct {
	EventSequence       int64  `json:"event_sequence"`
	EventID             string `json:"event_id"`
	EventKind           string `json:"event_kind"`
	RunID               string `json:"run_id"`
	AcceptedGeneration  string `json:"accepted_generation"`
	RecordedAtUnixNS    int64  `json:"recorded_at_unix_ns"`
	PayloadSHA256       string `json:"payload_sha256"`
	PreviousEventSHA256 string `json:"previous_event_sha256"`
	EventSHA256         string `json:"event_sha256"`
}

// DurationLedgerDerivedInputProvenance makes the raw history and its digest
// explicit without copying or rewriting raw rows.
type DurationLedgerDerivedInputProvenance struct {
	Authority   string                            `json:"authority"`
	InputDigest string                            `json:"input_digest"`
	Events      []DurationLedgerDerivedInputEvent `json:"events"`
}

// DurationLedgerDerivedMetric is one phase, gate, or run result with its exact
// source scope and aggregation vocabulary.
type DurationLedgerDerivedMetric struct {
	Scope         DurationLedgerDerivedMetricScope `json:"scope"`
	RunID         string                           `json:"run_id"`
	Status        ResultStatus                     `json:"status,omitempty"`
	GateID        GateID                           `json:"gate_id,omitempty"`
	ShardIdentity string                           `json:"shard_identity,omitempty"`
	WorkloadID    GateID                           `json:"workload_id,omitempty"`
	Phase         cicontract.TimingPhase           `json:"phase"`
	Aggregation   cicontract.TimingAggregation     `json:"aggregation"`
	Measurement   DurationLedgerDerivedMeasurement `json:"measurement"`
}

// DurationLedgerDerivedReport is a versioned, read-only projection over raw
// event facts. Retry, cancellation, pre-v6, live-warning, and capacity facts
// are intentionally separate typed UNKNOWN values.
type DurationLedgerDerivedReport struct {
	FormulaVersion   string                               `json:"formula_version"`
	InputProvenance  DurationLedgerDerivedInputProvenance `json:"input_provenance"`
	Completeness     DurationLedgerDerivedCompleteness    `json:"completeness"`
	RetryCost        DurationLedgerDerivedMeasurement     `json:"retry_cost"`
	CancellationCost DurationLedgerDerivedMeasurement     `json:"cancellation_cost"`
	Metrics          []DurationLedgerDerivedMetric        `json:"metrics"`
}

// DurationLedgerDerivedInterval is an explicitly identified interval used by
// the deterministic union and critical-path helpers.
type DurationLedgerDerivedInterval struct {
	Identity    string
	StartedAt   time.Time
	CompletedAt time.Time
}

// loadDerivedObservationReport通过现有只读完整性路径加载原始历史并计算独立派生报告。
func (store *DurationLedgerStore) loadDerivedObservationReport() (DurationLedgerDerivedReport, error) {
	if store == nil {
		return DurationLedgerDerivedReport{}, errors.New("duration ledger store is nil")
	}
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		return DurationLedgerDerivedReport{}, err
	}
	return aggregateDurationLedgerDerivedObservations(events)
}

// aggregateDurationLedgerDerivedObservations确定性消费精确原始事件，不写原始账本、历史、投影或事实。
func aggregateDurationLedgerDerivedObservations(events []DurationLedgerRawObservationEvent) (DurationLedgerDerivedReport, error) {
	ordered := durationLedgerDerivedOrderedEvents(events)
	provenance, err := durationLedgerDerivedProvenance(ordered)
	if err != nil {
		return DurationLedgerDerivedReport{}, err
	}
	report := newDurationLedgerDerivedReport(provenance)
	records, opaqueRunIDs, err := durationLedgerDerivedRecords(ordered)
	if err != nil {
		return DurationLedgerDerivedReport{}, err
	}
	report.Metrics, report.Completeness.PhaseGateRun, err = durationLedgerDerivedRunResults(records, opaqueRunIDs)
	if err != nil {
		return DurationLedgerDerivedReport{}, err
	}
	report.Completeness.Overall = unknownDerivedFact("retry, cancellation, pre-v6, live-warning, and capacity facts are not in the raw contract")
	sort.SliceStable(report.Metrics, func(left, right int) bool {
		return durationLedgerDerivedMetricLess(report.Metrics[left], report.Metrics[right])
	})
	return report, nil
}

// durationLedgerDerivedOrderedEvents固定原始输入排序，保证校验和 tie-break 只依赖事件身份。
func durationLedgerDerivedOrderedEvents(events []DurationLedgerRawObservationEvent) []DurationLedgerRawObservationEvent {
	ordered := append([]DurationLedgerRawObservationEvent(nil), events...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].EventSequence != ordered[right].EventSequence {
			return ordered[left].EventSequence < ordered[right].EventSequence
		}
		if ordered[left].EventID != ordered[right].EventID {
			return ordered[left].EventID < ordered[right].EventID
		}
		return ordered[left].EventSHA256 < ordered[right].EventSHA256
	})
	return ordered
}

// newDurationLedgerDerivedReport初始化只包含契约允许的派生和 UNKNOWN 字段。
func newDurationLedgerDerivedReport(provenance DurationLedgerDerivedInputProvenance) DurationLedgerDerivedReport {
	return DurationLedgerDerivedReport{FormulaVersion: DurationLedgerDerivedFormulaVersionV1, InputProvenance: provenance, Completeness: unknownDerivedCompleteness("raw contract does not expose all V1 completeness facts"), RetryCost: unknownDerivedMeasurement("retry attempt facts are absent from the raw contract"), CancellationCost: unknownDerivedMeasurement("cancellation attempt facts are absent from the raw contract")}
}

// durationLedgerDerivedRecords严格消费当前唯一可识别的 remote run persist 事实。
func durationLedgerDerivedRecords(events []DurationLedgerRawObservationEvent) (map[string]RemoteCIRunRecord, map[string]struct{}, error) {
	records, digests, opaque := make(map[string]RemoteCIRunRecord), make(map[string]string), make(map[string]struct{})
	for _, event := range events {
		if event.EventKind != durationLedgerObservationEventRemoteRunPersist {
			if event.RunID != "" {
				opaque[event.RunID] = struct{}{}
			}
			continue
		}
		record, err := durationLedgerDerivedRecordFromEvent(event)
		if err != nil {
			return nil, nil, err
		}
		digest, err := canonicalJSONDigest(record)
		if err != nil {
			return nil, nil, fmt.Errorf("remote CI run persist event %d record digest: %w", event.EventSequence, err)
		}
		if previous, exists := digests[record.JobID]; exists && previous != digest {
			return nil, nil, fmt.Errorf("remote CI run %q has conflicting persisted facts", record.JobID)
		}
		records[record.JobID], digests[record.JobID] = record, digest
	}
	return records, opaque, nil
}

// durationLedgerDerivedRecordFromEvent绑定事件 envelope、run identity和 accepted generation。
func durationLedgerDerivedRecordFromEvent(event DurationLedgerRawObservationEvent) (RemoteCIRunRecord, error) {
	record, err := decodeDurationLedgerDerivedRunRecord(event.PayloadJSON)
	if err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("remote CI run persist event %d: %w", event.EventSequence, err)
	}
	if record.JobID != event.RunID {
		return RemoteCIRunRecord{}, fmt.Errorf("remote CI run persist event %d run identity conflicts with payload", event.EventSequence)
	}
	if strconv.FormatUint(record.AcceptedGeneration, 10) != event.AcceptedGeneration {
		return RemoteCIRunRecord{}, fmt.Errorf("remote CI run persist event %d accepted generation conflicts with payload", event.EventSequence)
	}
	if err := validateDurationLedgerDerivedRunRecord(record); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("remote CI run persist event %d contains malformed record: %w", event.EventSequence, err)
	}
	return record, nil
}

// durationLedgerDerivedRunResults按 job ID 汇总，保留每个不完整 run 的 UNKNOWN 结论。
func durationLedgerDerivedRunResults(records map[string]RemoteCIRunRecord, opaque map[string]struct{}) ([]DurationLedgerDerivedMetric, DurationLedgerDerivedFact, error) {
	jobIDs := make([]string, 0, len(records))
	for jobID := range records {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)
	complete, reason := len(jobIDs) > 0, "all required phase, gate, run identity and timing facts are mechanically present"
	var metrics []DurationLedgerDerivedMetric
	for _, jobID := range jobIDs {
		runMetrics, runComplete, runReason, err := aggregateDurationLedgerDerivedRun(records[jobID])
		if err != nil {
			return nil, DurationLedgerDerivedFact{}, err
		}
		if _, exists := opaque[jobID]; exists {
			for index := range runMetrics {
				runMetrics[index].Measurement = unknownDerivedMeasurement("an opaque raw event for this run is not consumed by V1")
			}
			runComplete, runReason = false, "an additional opaque raw event for this run is not consumed by V1"
		}
		metrics = append(metrics, runMetrics...)
		if !runComplete {
			complete, reason = false, runReason
		}
	}
	if len(jobIDs) == 0 {
		reason = "no complete remote CI run persist fact is present"
	}
	if len(opaque) > 0 {
		complete, reason = false, "raw event history contains an opaque event relevant to derived completeness"
	}
	if complete {
		return metrics, knownDerivedFact(), nil
	}
	return metrics, unknownDerivedFact(reason), nil
}

func durationLedgerDerivedProvenance(events []DurationLedgerRawObservationEvent) (DurationLedgerDerivedInputProvenance, error) {
	inputEvents, previous := make([]DurationLedgerDerivedInputEvent, 0, len(events)), ""
	for index, event := range events {
		if err := rejectDurationLedgerDerivedDuplicateJSONKeys([]byte(event.MeasurementJSON)); err != nil && event.MeasurementJSON != "" {
			return DurationLedgerDerivedInputProvenance{}, fmt.Errorf("raw event measurement at sequence %d: %w", event.EventSequence, err)
		}
		next, err := verifyDurationLedgerRawObservationEvent(index, event, previous)
		if err != nil {
			return DurationLedgerDerivedInputProvenance{}, fmt.Errorf("raw event integrity at sequence %d: %w", event.EventSequence, err)
		}
		previous = next
		inputEvents = append(inputEvents, DurationLedgerDerivedInputEvent{EventSequence: event.EventSequence, EventID: event.EventID, EventKind: event.EventKind, RunID: event.RunID, AcceptedGeneration: event.AcceptedGeneration, RecordedAtUnixNS: event.RecordedAtUnixNS, PayloadSHA256: event.PayloadSHA256, PreviousEventSHA256: event.PreviousEventSHA256, EventSHA256: event.EventSHA256})
	}
	digest, err := canonicalJSONDigest(events)
	if err != nil {
		return DurationLedgerDerivedInputProvenance{}, fmt.Errorf("derive raw input provenance digest: %w", err)
	}
	return DurationLedgerDerivedInputProvenance{Authority: durationLedgerDerivedAuthority, InputDigest: digest, Events: inputEvents}, nil
}

// decodeDurationLedgerDerivedRunRecord严格解码 raw envelope 和现有 run record。
func decodeDurationLedgerDerivedRunRecord(payload string) (RemoteCIRunRecord, error) {
	var envelope struct {
		UnknownFacts durationLedgerRawUnknownFacts `json:"unknown_facts"`
		Value        json.RawMessage               `json:"value"`
	}
	if err := decodeDurationLedgerDerivedJSON([]byte(payload), &envelope); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("decode raw payload: %w", err)
	}
	if len(envelope.Value) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Value), []byte("null")) {
		return RemoteCIRunRecord{}, errors.New("raw payload value is missing")
	}
	for field, measurement := range map[string]DurationLedgerObservationMeasurement{"configured_shards_per_job": envelope.UnknownFacts.ConfiguredShardsPerJob, "configured_max_active_ci_workloads": envelope.UnknownFacts.ConfiguredMaxActiveCIWorkloads, "reservation_lease_count": envelope.UnknownFacts.ReservationLeaseCount, "reservation_identity_digest": envelope.UnknownFacts.ReservationIdentityDigest, "runtime_group_size": envelope.UnknownFacts.RuntimeGroupSize, "legacy_shard_schema_version": envelope.UnknownFacts.LegacyShardSchemaVersion} {
		if err := validateDurationLedgerObservationMeasurement(field, measurement); err != nil {
			return RemoteCIRunRecord{}, fmt.Errorf("raw payload unknown fact %s: %w", field, err)
		}
	}
	var record RemoteCIRunRecord
	if err := decodeDurationLedgerDerivedJSON(envelope.Value, &record); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("decode run record: %w", err)
	}
	return record, nil
}

func decodeDurationLedgerDerivedJSON(data []byte, destination any) error {
	if err := rejectDurationLedgerDerivedDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("JSON has trailing values")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

// rejectDurationLedgerDerivedDuplicateJSONKeys递归拒绝同一 JSON object 中的冲突字段名。
func rejectDurationLedgerDerivedDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return scanDurationLedgerDerivedJSONValue(decoder, nil)
}

// scanDurationLedgerDerivedJSONValue递归扫描 value、数组和嵌套 object 的键唯一性。
func scanDurationLedgerDerivedJSONValue(decoder *json.Decoder, seen map[string]struct{}) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter == '{' {
		seen = make(map[string]struct{})
	} else if delimiter != '[' {
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	for decoder.More() {
		if seen != nil {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key := keyToken.(string)
			canonicalKey := strings.ToLower(key)
			if _, duplicate := seen[canonicalKey]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[canonicalKey] = struct{}{}
		}
		if err := scanDurationLedgerDerivedJSONValue(decoder, nil); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// validateDurationLedgerDerivedRunRecord校验不可变 run/execution 身份，并把缺失 timing warning 留给 UNKNOWN。
func validateDurationLedgerDerivedRunRecord(record RemoteCIRunRecord) error {
	if err := validateRemoteCIRunIdentity(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunShards(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunExecutions(record.Executions); err != nil {
		return err
	}
	if err := validateRemoteCIRunWorkloadExecutions(record.WorkloadExecutions); err != nil {
		return err
	}
	if err := validateRemoteCIWorkloadResults(record.WorkloadResults); err != nil {
		return err
	}
	for _, observation := range record.TimingObservations {
		if observation.JobID != record.JobID {
			return errors.New("remote CI timing observation job binding is invalid")
		}
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("remote CI timing observation: %w", err)
		}
	}
	return nil
}

type durationLedgerDerivedRunFacts struct {
	record             RemoteCIRunRecord
	observed           map[observationKey]TimingObservation
	expected           map[observationKey]struct{}
	workloadExecutions map[GateID]PlanGateExecution
	parentGroups       map[GateID][]PlanGateExecution
	identityComplete   bool
	fullTiming         bool
}

// aggregateDurationLedgerDerivedRun只在 run facts 闭合时发布 phase、gate 和 run 派生值。
func aggregateDurationLedgerDerivedRun(record RemoteCIRunRecord) ([]DurationLedgerDerivedMetric, bool, string, error) {
	facts, err := newDurationLedgerDerivedRunFacts(record)
	if err != nil {
		return nil, false, "run facts are malformed", err
	}
	metrics := durationLedgerDerivedTimingMetrics(facts)
	if facts.fullTiming {
		if err := ValidateAuthoritativeTimingObservations(record.JobID, record.TimingObservations, record.WorkloadExecutions, record.Shards); err != nil {
			return nil, false, "complete timing facts conflict with the existing timing contract", fmt.Errorf("remote CI run %q timing contract: %w", record.JobID, err)
		}
		if err := verifyDurationLedgerDerivedTiming(record, facts.observed); err != nil {
			return nil, false, "complete timing facts conflict with an exact union or critical path", err
		}
	}
	gateMetrics, gateComplete := durationLedgerDerivedGateMetrics(facts)
	metrics = append(metrics, gateMetrics...)
	if facts.identityComplete && facts.fullTiming && gateComplete {
		return metrics, true, "all required phase, gate, run identity and timing facts are mechanically present", nil
	}
	return metrics, false, "phase, gate, or run facts are absent or incomplete; derived aggregates remain UNKNOWN", nil
}

// newDurationLedgerDerivedRunFacts建立严格 identity 集合并标记 timing completeness。
func newDurationLedgerDerivedRunFacts(record RemoteCIRunRecord) (durationLedgerDerivedRunFacts, error) {
	observed, err := durationLedgerDerivedObserved(record)
	if err != nil {
		return durationLedgerDerivedRunFacts{}, err
	}
	facts := durationLedgerDerivedRunFacts{record: record, observed: observed, expected: make(map[observationKey]struct{}), workloadExecutions: make(map[GateID]PlanGateExecution), parentGroups: make(map[GateID][]PlanGateExecution), identityComplete: len(record.Shards) > 0 && len(record.WorkloadExecutions) > 0}
	shardWorkloads := make(map[GateID]string)
	if err := durationLedgerDerivedShardIdentity(&facts, shardWorkloads); err != nil {
		return durationLedgerDerivedRunFacts{}, err
	}
	if err := durationLedgerDerivedWorkloadIdentity(&facts, shardWorkloads); err != nil {
		return durationLedgerDerivedRunFacts{}, err
	}
	facts.expected[observationKey{scope: cicontract.TimingScopeRun, phase: cicontract.TimingTotal}] = struct{}{}
	facts.fullTiming = facts.identityComplete && len(facts.expected) > 0 && len(facts.observed) == len(facts.expected) && durationLedgerDerivedTimingStatesComplete(facts)
	for key := range facts.observed {
		if _, exists := facts.expected[key]; !exists {
			return durationLedgerDerivedRunFacts{}, fmt.Errorf("remote CI run %q has extra timing observation scope=%q shard=%q workload=%q phase=%q", record.JobID, key.scope, key.shard, key.workload, key.phase)
		}
	}
	return facts, nil
}

// durationLedgerDerivedObserved读取并校验现有 timing observation 身份。
func durationLedgerDerivedObserved(record RemoteCIRunRecord) (map[observationKey]TimingObservation, error) {
	observed := make(map[observationKey]TimingObservation, len(record.TimingObservations))
	for _, observation := range record.TimingObservations {
		if observation.JobID != record.JobID {
			return nil, fmt.Errorf("remote CI run %q timing observation job identity conflicts", record.JobID)
		}
		if err := observation.Validate(); err != nil {
			return nil, fmt.Errorf("remote CI run %q timing observation: %w", record.JobID, err)
		}
		key := observationKey{scope: observation.Scope, shard: observation.ShardIdentity, workload: observation.WorkloadID, phase: observation.Phase}
		if _, exists := observed[key]; exists {
			return nil, fmt.Errorf("remote CI run %q timing observation is duplicated", record.JobID)
		}
		observed[key] = observation
	}
	return observed, nil
}

// durationLedgerDerivedShardIdentity收集稳定 shard/workload 绑定并建立 shard phase 集合。
func durationLedgerDerivedShardIdentity(facts *durationLedgerDerivedRunFacts, shardWorkloads map[GateID]string) error {
	seenShards := make(map[string]struct{}, len(facts.record.Shards))
	for _, shard := range facts.record.Shards {
		if _, duplicate := seenShards[shard.ShardIdentity]; duplicate {
			return fmt.Errorf("remote CI run %q shard %q is duplicated", facts.record.JobID, shard.ShardIdentity)
		}
		seenShards[shard.ShardIdentity] = struct{}{}
		for _, workloadID := range shard.Workloads {
			if previous, duplicate := shardWorkloads[workloadID]; duplicate {
				return fmt.Errorf("remote CI run %q workload %q is bound to shards %q and %q", facts.record.JobID, workloadID, previous, shard.ShardIdentity)
			}
			shardWorkloads[workloadID] = shard.ShardIdentity
		}
		for _, phase := range cicontract.TimingPhases() {
			facts.expected[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: phase}] = struct{}{}
		}
	}
	return nil
}

// durationLedgerDerivedWorkloadIdentity收集 workload phase、父 gate 和精确 shard 归属。
func durationLedgerDerivedWorkloadIdentity(facts *durationLedgerDerivedRunFacts, shardWorkloads map[GateID]string) error {
	for _, execution := range facts.record.WorkloadExecutions {
		if err := durationLedgerDerivedAddWorkloadIdentity(facts, shardWorkloads, execution); err != nil {
			return err
		}
	}
	if len(facts.workloadExecutions) != len(shardWorkloads) {
		facts.identityComplete = false
	}
	for workloadID, shard := range shardWorkloads {
		execution, exists := facts.workloadExecutions[workloadID]
		if !exists || execution.ShardIdentity != shard {
			facts.identityComplete = false
		}
	}
	return nil
}

// durationLedgerDerivedAddWorkloadIdentity加入一个 workload 的 parent 和 phase key。
func durationLedgerDerivedAddWorkloadIdentity(facts *durationLedgerDerivedRunFacts, shardWorkloads map[GateID]string, execution PlanGateExecution) error {
	if _, duplicate := facts.workloadExecutions[execution.GateID]; duplicate {
		return fmt.Errorf("remote CI run %q workload execution %q is duplicated", facts.record.JobID, execution.GateID)
	}
	facts.workloadExecutions[execution.GateID] = execution
	shard, exists := shardWorkloads[execution.GateID]
	if !exists || shard != execution.ShardIdentity {
		facts.identityComplete = false
	} else {
		parent, err := WorkloadParentGateID(string(execution.GateID))
		if err != nil {
			return fmt.Errorf("remote CI run %q workload %q: %w", facts.record.JobID, execution.GateID, err)
		}
		facts.parentGroups[parent] = append(facts.parentGroups[parent], execution)
	}
	for _, phase := range cicontract.TimingPhases() {
		facts.expected[observationKey{scope: cicontract.TimingScopeWorkload, shard: execution.ShardIdentity, workload: execution.GateID, phase: phase}] = struct{}{}
	}
	return nil
}

// durationLedgerDerivedTimingStatesComplete允许既有契约明确声明的 workload N/A phase。
func durationLedgerDerivedTimingStatesComplete(facts durationLedgerDerivedRunFacts) bool {
	for key := range facts.expected {
		observation := facts.observed[key]
		if observation.Measurement == cicontract.ObservationNotApplicable && !(key.scope == cicontract.TimingScopeWorkload && (key.phase == cicontract.TimingECIWait || key.phase == cicontract.TimingSourceMaterialize || key.phase == cicontract.TimingCandidateCompile)) {
			return false
		}
	}
	return true
}

// durationLedgerDerivedTimingMetrics为每个 exact key 发布实测或 UNKNOWN。
func durationLedgerDerivedTimingMetrics(facts durationLedgerDerivedRunFacts) []DurationLedgerDerivedMetric {
	metrics := make([]DurationLedgerDerivedMetric, 0, len(facts.expected))
	for key := range facts.expected {
		metric := durationLedgerDerivedTimingMetric(facts.record.JobID, key, facts.observed[key], facts.observed[key].JobID != "", facts.fullTiming)
		if key.scope == cicontract.TimingScopeWorkload {
			metric.Status = facts.workloadExecutions[key.workload].Status
		}
		if key.scope == cicontract.TimingScopeRun {
			metric.Status = facts.record.Status
		}
		metrics = append(metrics, metric)
	}
	return metrics
}

// durationLedgerDerivedTimingMetric保留 raw measurement，并在不完整时隐藏派生 aggregate。
func durationLedgerDerivedTimingMetric(runID string, key observationKey, observation TimingObservation, exists, fullTiming bool) DurationLedgerDerivedMetric {
	if !exists {
		return durationLedgerDerivedMetricForKey(runID, key, cicontract.TimingAggregationRaw, unknownDerivedMeasurement("required timing fact is absent"))
	}
	if observation.Measurement == cicontract.ObservationNotApplicable {
		return durationLedgerDerivedMetricForKey(runID, key, observation.Aggregation, unknownDerivedMeasurement("timing fact is not applicable: "+observation.Reason))
	}
	measurement, err := durationLedgerDerivedMeasurementFromTiming(observation)
	if err != nil {
		return durationLedgerDerivedMetricForKey(runID, key, observation.Aggregation, unknownDerivedMeasurement("timing measurement is unavailable: "+err.Error()))
	}
	if observation.Aggregation != cicontract.TimingAggregationRaw && !fullTiming {
		measurement = unknownDerivedMeasurement("incomplete timing prevents derived aggregate proof")
	}
	return durationLedgerDerivedMetricForKey(runID, key, observation.Aggregation, measurement)
}

// durationLedgerDerivedGateMetrics仅在 gate envelope 与 workload intervals 双向一致时发布 KNOWN。
func durationLedgerDerivedGateMetrics(facts durationLedgerDerivedRunFacts) ([]DurationLedgerDerivedMetric, bool) {
	parents := make([]GateID, 0, len(facts.parentGroups))
	for parent := range facts.parentGroups {
		parents = append(parents, parent)
	}
	slices.Sort(parents)
	executions := make(map[GateID]PlanGateExecution, len(facts.record.Executions))
	for _, execution := range facts.record.Executions {
		executions[execution.GateID] = execution
	}
	complete := facts.identityComplete && facts.fullTiming && len(parents) > 0 && len(executions) == len(parents)
	complete = complete && durationLedgerDerivedGateExecutionSetComplete(parents, executions)
	var metrics []DurationLedgerDerivedMetric
	for _, parent := range parents {
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
			aggregation := map[cicontract.TimingPhase]cicontract.TimingAggregation{cicontract.TimingStartup: cicontract.TimingAggregationIntervalUnion, cicontract.TimingTestBody: cicontract.TimingAggregationIntervalUnion, cicontract.TimingTotal: cicontract.TimingAggregationCriticalPath}[phase]
			measurement := unknownDerivedMeasurement("gate envelope is unavailable without complete workload identity and timing facts")
			if complete {
				measurement, complete = durationLedgerDerivedGateMeasurement(parent, facts.parentGroups[parent], phase, facts.observed, executions[parent])
			}
			metrics = append(metrics, DurationLedgerDerivedMetric{Scope: DurationLedgerDerivedMetricScopeGate, RunID: facts.record.JobID, Status: executions[parent].Status, GateID: parent, Phase: phase, Aggregation: aggregation, Measurement: measurement})
		}
	}
	return metrics, complete
}

// durationLedgerDerivedGateExecutionSetComplete要求 gate execution 与 workload parent 集合完全相同。
func durationLedgerDerivedGateExecutionSetComplete(parents []GateID, executions map[GateID]PlanGateExecution) bool {
	for _, parent := range parents {
		if _, exists := executions[parent]; !exists {
			return false
		}
	}
	return true
}

// verifyDurationLedgerDerivedTiming独立复核 workload、shard union 和 run critical path。
func verifyDurationLedgerDerivedTiming(record RemoteCIRunRecord, observed map[observationKey]TimingObservation) error {
	if err := verifyDurationLedgerDerivedWorkloadTiming(record.WorkloadExecutions, observed); err != nil {
		return err
	}
	if err := verifyDurationLedgerDerivedShardUnions(record.Shards, observed); err != nil {
		return err
	}
	return verifyDurationLedgerDerivedRunCriticalPath(record.Shards, observed)
}

// verifyDurationLedgerDerivedWorkloadTiming把 profile 时长绑定到真实 workload interval。
func verifyDurationLedgerDerivedWorkloadTiming(executions []PlanGateExecution, observed map[observationKey]TimingObservation) error {
	for _, execution := range executions {
		workloadID := execution.GateID
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
			observation := observed[observationKey{scope: cicontract.TimingScopeWorkload, shard: execution.ShardIdentity, workload: workloadID, phase: phase}]
			if observation.DurationMS != durationLedgerDerivedWorkloadProfileDuration(execution.ExecutionProfile, phase) {
				return fmt.Errorf("workload %q phase %q profile conflicts with timing fact", workloadID, phase)
			}
			if phase == cicontract.TimingTestBody && !observation.CompletedAt.Equal(execution.CompletedAt) {
				return fmt.Errorf("workload %q test body completion conflicts with execution identity", workloadID)
			}
			if phase != cicontract.TimingTestBody && !observation.StartedAt.Equal(execution.StartedAt) {
				return fmt.Errorf("workload %q phase %q start conflicts with execution identity", workloadID, phase)
			}
			if phase == cicontract.TimingTotal && !observation.CompletedAt.Equal(execution.CompletedAt) {
				return fmt.Errorf("workload %q total interval conflicts with execution identity", workloadID)
			}
		}
	}
	return nil
}

// durationLedgerDerivedWorkloadProfileDuration取 child profile 的三种真实 duration。
func durationLedgerDerivedWorkloadProfileDuration(profile ExecutionProfile, phase cicontract.TimingPhase) int64 {
	switch phase {
	case cicontract.TimingStartup:
		return profile.StartupMS
	case cicontract.TimingTestBody:
		return profile.TestBodyMS
	default:
		return profile.TotalMS
	}
}

// verifyDurationLedgerDerivedShardUnions复核每个 shard 的 startup/body exact union。
func verifyDurationLedgerDerivedShardUnions(shards []RemoteCIShardRecord, observed map[observationKey]TimingObservation) error {
	for _, shard := range shards {
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody} {
			intervals := durationLedgerDerivedWorkloadIntervals(shard, phase, observed)
			union, err := durationLedgerDerivedIntervalUnion(intervals)
			if err != nil {
				return fmt.Errorf("shard %q phase %q exact interval union: %w", shard.ShardIdentity, phase, err)
			}
			aggregate := observed[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: phase}]
			if !durationLedgerDerivedMeasurementMatchesTiming(union, aggregate) {
				return fmt.Errorf("shard %q phase %q does not equal the exact workload interval union", shard.ShardIdentity, phase)
			}
		}
	}
	return nil
}

// durationLedgerDerivedWorkloadIntervals建立 union 的稳定 workload tie-break 输入。
func durationLedgerDerivedWorkloadIntervals(shard RemoteCIShardRecord, phase cicontract.TimingPhase, observed map[observationKey]TimingObservation) []DurationLedgerDerivedInterval {
	intervals := make([]DurationLedgerDerivedInterval, 0, len(shard.Workloads))
	for _, workloadID := range shard.Workloads {
		observation := observed[observationKey{scope: cicontract.TimingScopeWorkload, shard: shard.ShardIdentity, workload: workloadID, phase: phase}]
		intervals = append(intervals, DurationLedgerDerivedInterval{Identity: string(workloadID), StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt})
	}
	return intervals
}

// verifyDurationLedgerDerivedRunCriticalPath复核 run total 与 shard envelope 一致。
func verifyDurationLedgerDerivedRunCriticalPath(shards []RemoteCIShardRecord, observed map[observationKey]TimingObservation) error {
	intervals := make([]DurationLedgerDerivedInterval, 0, len(shards))
	for _, shard := range shards {
		observation := observed[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: cicontract.TimingTotal}]
		intervals = append(intervals, DurationLedgerDerivedInterval{Identity: shard.ShardIdentity, StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt})
	}
	critical, err := durationLedgerDerivedEnvelope(intervals)
	if err != nil {
		return fmt.Errorf("run critical path: %w", err)
	}
	if !durationLedgerDerivedMeasurementMatchesTiming(critical, observed[observationKey{scope: cicontract.TimingScopeRun, phase: cicontract.TimingTotal}]) {
		return errors.New("run total does not equal the exact shard critical path envelope")
	}
	return nil
}

// durationLedgerDerivedGateMeasurement核对 gate 边界、精确 union 和既有 profile。
func durationLedgerDerivedGateMeasurement(parent GateID, group []PlanGateExecution, phase cicontract.TimingPhase, observed map[observationKey]TimingObservation, execution PlanGateExecution) (DurationLedgerDerivedMeasurement, bool) {
	if execution.GateID != parent {
		return unknownDerivedMeasurement("gate execution envelope is absent"), false
	}
	intervals := make([]DurationLedgerDerivedInterval, 0, len(group))
	for _, workload := range group {
		observation := observed[observationKey{scope: cicontract.TimingScopeWorkload, shard: workload.ShardIdentity, workload: workload.GateID, phase: phase}]
		intervals = append(intervals, DurationLedgerDerivedInterval{Identity: string(workload.GateID), StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt})
	}
	var want DurationLedgerDerivedMeasurement
	var err error
	if phase == cicontract.TimingTotal {
		want, err = durationLedgerDerivedEnvelope(intervals)
	} else {
		want, err = durationLedgerDerivedIntervalUnion(intervals)
	}
	if err != nil {
		return unknownDerivedMeasurement("gate envelope interval is malformed"), false
	}
	if phase == cicontract.TimingTotal && (!want.StartedAt.Equal(execution.StartedAt) || !want.CompletedAt.Equal(execution.CompletedAt)) {
		return unknownDerivedMeasurement("gate envelope conflicts with execution boundaries"), false
	}
	if want.DurationMS == nil {
		return unknownDerivedMeasurement("gate envelope duration is unavailable"), false
	}
	profileDuration := durationLedgerDerivedWorkloadProfileDuration(execution.ExecutionProfile, phase)
	if profileDuration != *want.DurationMS {
		return unknownDerivedMeasurement("gate profile duration conflicts with exact workload facts"), false
	}
	return want, true
}

func durationLedgerDerivedMetricForKey(runID string, key observationKey, aggregation cicontract.TimingAggregation, measurement DurationLedgerDerivedMeasurement) DurationLedgerDerivedMetric {
	scope := DurationLedgerDerivedMetricScopeWorkload
	switch key.scope {
	case cicontract.TimingScopeShard:
		scope = DurationLedgerDerivedMetricScopeShard
	case cicontract.TimingScopeRun:
		scope = DurationLedgerDerivedMetricScopeRun
	}
	return DurationLedgerDerivedMetric{Scope: scope, RunID: runID, ShardIdentity: key.shard, WorkloadID: key.workload, Phase: key.phase, Aggregation: aggregation, Measurement: measurement}
}

// durationLedgerDerivedMeasurementFromTiming把已有实测 observation 转成安全派生 interval。
func durationLedgerDerivedMeasurementFromTiming(observation TimingObservation) (DurationLedgerDerivedMeasurement, error) {
	duration, err := durationLedgerDerivedMilliseconds(observation.StartedAt, observation.CompletedAt)
	if err != nil {
		return DurationLedgerDerivedMeasurement{}, err
	}
	if observation.DurationMS <= 0 || ((observation.Aggregation == cicontract.TimingAggregationRaw || observation.Aggregation == cicontract.TimingAggregationCriticalPath) && observation.DurationMS != duration) || observation.DurationMS > duration {
		return DurationLedgerDerivedMeasurement{}, errors.New("timing duration conflicts with its real interval")
	}
	started, completed := observation.StartedAt.UTC(), observation.CompletedAt.UTC()
	value := observation.DurationMS
	return DurationLedgerDerivedMeasurement{Status: DurationLedgerDerivedKnown, StartedAt: &started, CompletedAt: &completed, DurationMS: &value}, nil
}

// durationLedgerDerivedMeasurementMatchesTiming比较派生 interval 与已有 timing 的全部边界。
func durationLedgerDerivedMeasurementMatchesTiming(measurement DurationLedgerDerivedMeasurement, observation TimingObservation) bool {
	return measurement.Status == DurationLedgerDerivedKnown && observation.Measurement == cicontract.ObservationMeasured && measurement.StartedAt != nil && measurement.CompletedAt != nil && measurement.DurationMS != nil && measurement.StartedAt.Equal(observation.StartedAt) && measurement.CompletedAt.Equal(observation.CompletedAt) && *measurement.DurationMS == observation.DurationMS
}

func durationLedgerDerivedMetricLess(left, right DurationLedgerDerivedMetric) bool {
	leftKey := []string{string(left.Scope), left.RunID, string(left.GateID), left.ShardIdentity, string(left.WorkloadID), string(left.Phase), string(left.Aggregation)}
	rightKey := []string{string(right.Scope), right.RunID, string(right.GateID), right.ShardIdentity, string(right.WorkloadID), string(right.Phase), string(right.Aggregation)}
	for index := range leftKey {
		if leftKey[index] != rightKey[index] {
			return leftKey[index] < rightKey[index]
		}
	}
	return false
}

// durationLedgerDerivedIntervalUnion按稳定 identity 计算真实活动区间并集。
func durationLedgerDerivedIntervalUnion(intervals []DurationLedgerDerivedInterval) (DurationLedgerDerivedMeasurement, error) {
	ordered, err := durationLedgerDerivedSortedIntervals(intervals, "interval union")
	if err != nil {
		return DurationLedgerDerivedMeasurement{}, err
	}
	start, end, total := ordered[0].StartedAt, ordered[0].CompletedAt, new(big.Int)
	for _, interval := range ordered[1:] {
		if interval.StartedAt.After(end) {
			if total, err = durationLedgerDerivedAddSegment(total, start, end); err != nil {
				return DurationLedgerDerivedMeasurement{}, err
			}
			start, end = interval.StartedAt, interval.CompletedAt
			continue
		}
		if interval.CompletedAt.After(end) {
			end = interval.CompletedAt
		}
	}
	if total, err = durationLedgerDerivedAddSegment(total, start, end); err != nil {
		return DurationLedgerDerivedMeasurement{}, err
	}
	if !total.IsInt64() || total.Sign() <= 0 || total.Int64() <= 0 {
		return DurationLedgerDerivedMeasurement{}, errors.New("interval union duration overflows int64")
	}
	value := total.Int64()
	startUTC, endUTC := ordered[0].StartedAt.UTC(), end.UTC()
	return DurationLedgerDerivedMeasurement{Status: DurationLedgerDerivedKnown, StartedAt: &startUTC, CompletedAt: &endUTC, DurationMS: &value}, nil
}

// durationLedgerDerivedEnvelope按稳定 identity 计算关键路径的最小开始到最大完成。
func durationLedgerDerivedEnvelope(intervals []DurationLedgerDerivedInterval) (DurationLedgerDerivedMeasurement, error) {
	ordered, err := durationLedgerDerivedSortedIntervals(intervals, "critical path")
	if err != nil {
		return DurationLedgerDerivedMeasurement{}, err
	}
	start, end := ordered[0].StartedAt, ordered[0].CompletedAt
	for _, interval := range ordered[1:] {
		if interval.StartedAt.Before(start) {
			start = interval.StartedAt
		}
		if interval.CompletedAt.After(end) {
			end = interval.CompletedAt
		}
	}
	duration, err := durationLedgerDerivedMilliseconds(start, end)
	if err != nil {
		return DurationLedgerDerivedMeasurement{}, fmt.Errorf("critical path duration overflows int64: %w", err)
	}
	if duration <= 0 {
		return DurationLedgerDerivedMeasurement{}, errors.New("critical path duration is not positive")
	}
	value := duration
	startUTC, endUTC := start.UTC(), end.UTC()
	return DurationLedgerDerivedMeasurement{Status: DurationLedgerDerivedKnown, StartedAt: &startUTC, CompletedAt: &endUTC, DurationMS: &value}, nil
}

// durationLedgerDerivedSortedIntervals校验 identity 和时间，并返回唯一稳定排序。
func durationLedgerDerivedSortedIntervals(intervals []DurationLedgerDerivedInterval, label string) ([]DurationLedgerDerivedInterval, error) {
	if len(intervals) == 0 {
		return nil, fmt.Errorf("%s needs at least one interval", label)
	}
	ordered := append([]DurationLedgerDerivedInterval(nil), intervals...)
	seen := make(map[string]struct{}, len(ordered))
	for _, interval := range ordered {
		if strings.TrimSpace(interval.Identity) == "" {
			return nil, fmt.Errorf("%s identity is required", label)
		}
		if _, duplicate := seen[interval.Identity]; duplicate {
			return nil, fmt.Errorf("%s identity %q is duplicated", label, interval.Identity)
		}
		seen[interval.Identity] = struct{}{}
		if interval.StartedAt.IsZero() || interval.CompletedAt.IsZero() || !interval.CompletedAt.After(interval.StartedAt) {
			return nil, fmt.Errorf("%s identity %q has an invalid interval", label, interval.Identity)
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if !ordered[left].StartedAt.Equal(ordered[right].StartedAt) {
			return ordered[left].StartedAt.Before(ordered[right].StartedAt)
		}
		if !ordered[left].CompletedAt.Equal(ordered[right].CompletedAt) {
			return ordered[left].CompletedAt.Before(ordered[right].CompletedAt)
		}
		return ordered[left].Identity < ordered[right].Identity
	})
	return ordered, nil
}

// durationLedgerDerivedAddSegment安全累加一个 union segment 的毫秒数。
func durationLedgerDerivedAddSegment(total *big.Int, start, end time.Time) (*big.Int, error) {
	segment, err := durationLedgerDerivedBigMilliseconds(start, end)
	if err != nil {
		return nil, err
	}
	return total.Add(total, segment), nil
}

func durationLedgerDerivedMilliseconds(start, end time.Time) (int64, error) {
	value, err := durationLedgerDerivedBigMilliseconds(start, end)
	if err != nil {
		return 0, err
	}
	if !value.IsInt64() || value.Sign() <= 0 {
		return 0, errors.New("duration overflows int64")
	}
	return value.Int64(), nil
}

func durationLedgerDerivedBigMilliseconds(start, end time.Time) (*big.Int, error) {
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return nil, errors.New("duration interval is invalid")
	}
	seconds := new(big.Int).Sub(big.NewInt(end.Unix()), big.NewInt(start.Unix()))
	seconds.Mul(seconds, big.NewInt(int64(time.Second)))
	nanos := big.NewInt(int64(end.Nanosecond() - start.Nanosecond()))
	seconds.Add(seconds, nanos)
	if seconds.Sign() <= 0 {
		return nil, errors.New("duration interval is not positive")
	}
	return seconds.Quo(seconds, big.NewInt(int64(time.Millisecond))), nil
}
