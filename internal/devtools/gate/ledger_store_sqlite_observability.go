package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	durationLedgerObservationEventLedgerReplace      = "duration_ledger_replace"
	durationLedgerObservationEventCalibrationReplace = "duration_ledger_calibration_replace"
	durationLedgerObservationEventSamplesAppend      = "duration_ledger_samples_append"
	durationLedgerObservationEventRemoteRunPersist   = "remote_ci_run_persist"
	durationLedgerObservationEventRemoteRunFinalize  = "remote_ci_run_finalize"
	durationLedgerObservationEventTest               = "test_observation"
	durationLedgerObservationKnown                   = "KNOWN"
	durationLedgerObservationUnknown                 = "UNKNOWN"
)

// durationLedgerRawObservationEventsTableSchema is the only raw history table.
// Its rows are intentionally outside the existing projection retention roots.
const durationLedgerRawObservationEventsTableSchema = `
CREATE TABLE IF NOT EXISTS duration_ledger_raw_events (
	event_sequence INTEGER PRIMARY KEY CHECK (event_sequence > 0),
	event_id TEXT NOT NULL UNIQUE CHECK (length(trim(event_id)) > 0),
	event_kind TEXT NOT NULL CHECK (length(trim(event_kind)) > 0),
	run_id TEXT NOT NULL DEFAULT '',
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	recorded_at_unix_ns INTEGER NOT NULL,
	payload_json TEXT NOT NULL CHECK (length(payload_json) > 0),
	payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 71 AND substr(payload_sha256, 1, 7) = 'sha256:' AND substr(payload_sha256, 8) NOT GLOB '*[^0-9a-f]*'),
	previous_event_sha256 TEXT NOT NULL DEFAULT '' CHECK (previous_event_sha256 = '' OR (length(previous_event_sha256) = 71 AND substr(previous_event_sha256, 1, 7) = 'sha256:' AND substr(previous_event_sha256, 8) NOT GLOB '*[^0-9a-f]*')),
	measurement_json TEXT NOT NULL CHECK (length(measurement_json) > 0),
	event_sha256 TEXT NOT NULL UNIQUE CHECK (length(event_sha256) = 71 AND substr(event_sha256, 1, 7) = 'sha256:' AND substr(event_sha256, 8) NOT GLOB '*[^0-9a-f]*')
)`

const durationLedgerRawObservationEventsIndexSchema = `CREATE INDEX IF NOT EXISTS idx_duration_ledger_raw_events_recorded_at
	ON duration_ledger_raw_events (recorded_at_unix_ns, event_sequence)`

const durationLedgerRawObservationEventsUpdateTriggerSchema = `CREATE TRIGGER IF NOT EXISTS duration_ledger_raw_events_no_update
	BEFORE UPDATE ON duration_ledger_raw_events
	BEGIN
		SELECT RAISE(ABORT, 'duration ledger raw events are append-only');
	END`

const durationLedgerRawObservationEventsDeleteTriggerSchema = `CREATE TRIGGER IF NOT EXISTS duration_ledger_raw_events_no_delete
	BEFORE DELETE ON duration_ledger_raw_events
	BEGIN
		SELECT RAISE(ABORT, 'duration ledger raw events are append-only');
	END`

// DurationLedgerObservationStatus distinguishes an observed value from an unavailable fact.
type DurationLedgerObservationStatus string

const (
	DurationLedgerObservationKnown   DurationLedgerObservationStatus = durationLedgerObservationKnown
	DurationLedgerObservationUnknown DurationLedgerObservationStatus = durationLedgerObservationUnknown
)

// DurationLedgerObservationMeasurement is a typed capacity/health measurement.
// UNKNOWN values never use Value as an implicit zero.
type DurationLedgerObservationMeasurement struct {
	Status     DurationLedgerObservationStatus `json:"status"`
	Value      *int64                          `json:"value,omitempty"`
	Provenance string                          `json:"provenance"`
	Warning    string                          `json:"warning,omitempty"`
}

// DurationLedgerObservationReport is the derived report published by each raw event.
type DurationLedgerObservationReport struct {
	LedgerLogicalBytes       DurationLedgerObservationMeasurement `json:"ledger_logical_bytes"`
	LedgerPhysicalBytes      DurationLedgerObservationMeasurement `json:"ledger_physical_bytes"`
	RecordCount              DurationLedgerObservationMeasurement `json:"record_count"`
	RunCount                 DurationLedgerObservationMeasurement `json:"run_count"`
	EarliestRecordedAtUnixNS DurationLedgerObservationMeasurement `json:"earliest_recorded_at_unix_ns"`
	LatestRecordedAtUnixNS   DurationLedgerObservationMeasurement `json:"latest_recorded_at_unix_ns"`
	FilesystemAvailableBytes DurationLedgerObservationMeasurement `json:"filesystem_available_bytes"`
}

// Validate 拒绝畸形或以隐式零值伪装的观测测量。
func (report DurationLedgerObservationReport) Validate() error {
	measurements := map[string]DurationLedgerObservationMeasurement{
		"ledger_logical_bytes":         report.LedgerLogicalBytes,
		"ledger_physical_bytes":        report.LedgerPhysicalBytes,
		"record_count":                 report.RecordCount,
		"run_count":                    report.RunCount,
		"earliest_recorded_at_unix_ns": report.EarliestRecordedAtUnixNS,
		"latest_recorded_at_unix_ns":   report.LatestRecordedAtUnixNS,
		"filesystem_available_bytes":   report.FilesystemAvailableBytes,
	}
	for name, measurement := range measurements {
		if err := validateDurationLedgerObservationMeasurement(name, measurement); err != nil {
			return err
		}
	}
	return nil
}

// validateDurationLedgerObservationMeasurement 校验单个报告测量的状态和值来源。
func validateDurationLedgerObservationMeasurement(name string, measurement DurationLedgerObservationMeasurement) error {
	switch measurement.Status {
	case DurationLedgerObservationKnown:
		return validateKnownDurationLedgerObservation(name, measurement)
	case DurationLedgerObservationUnknown:
		return validateUnknownDurationLedgerObservation(name, measurement)
	default:
		return fmt.Errorf("observation %s status %q is invalid", name, measurement.Status)
	}
}

// validateKnownDurationLedgerObservation 校验 KNOWN 值必须有真实值和来源。
func validateKnownDurationLedgerObservation(name string, measurement DurationLedgerObservationMeasurement) error {
	if measurement.Value == nil || strings.TrimSpace(measurement.Provenance) == "" || measurement.Warning != "" {
		return fmt.Errorf("observation %s known value is incomplete", name)
	}
	if *measurement.Value < 0 && name != "earliest_recorded_at_unix_ns" && name != "latest_recorded_at_unix_ns" {
		return fmt.Errorf("observation %s known value is negative", name)
	}
	return nil
}

func validateUnknownDurationLedgerObservation(name string, measurement DurationLedgerObservationMeasurement) error {
	if measurement.Value != nil || strings.TrimSpace(measurement.Provenance) == "" || strings.TrimSpace(measurement.Warning) == "" {
		return fmt.Errorf("observation %s UNKNOWN value is incomplete", name)
	}
	return nil
}

// DurationLedgerRawObservationEvent is one immutable row in the same SQLite authority.
type DurationLedgerRawObservationEvent struct {
	EventSequence       int64  `json:"event_sequence"`
	EventID             string `json:"event_id"`
	EventKind           string `json:"event_kind"`
	RunID               string `json:"run_id"`
	AcceptedGeneration  string `json:"accepted_generation"`
	RecordedAtUnixNS    int64  `json:"recorded_at_unix_ns"`
	PayloadJSON         string `json:"payload_json"`
	PayloadSHA256       string `json:"payload_sha256"`
	PreviousEventSHA256 string `json:"previous_event_sha256"`
	MeasurementJSON     string `json:"measurement_json"`
	EventSHA256         string `json:"event_sha256"`
}

// durationLedgerObservationFilesystemFacts contains only read-only provider facts.
type durationLedgerObservationFilesystemFacts struct {
	PhysicalBytes  *int64
	AvailableBytes *int64
}

type durationLedgerObservationFilesystemProvider func(string) (durationLedgerObservationFilesystemFacts, error)

// ErrDurationLedgerObservationUnavailable 标记只读提供方事实不可用或不受支持。
var ErrDurationLedgerObservationUnavailable = errors.New("duration ledger observation filesystem facts unavailable")

var errDurationLedgerObservationUnavailable = ErrDurationLedgerObservationUnavailable

type durationLedgerRawObservationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type durationLedgerRawObservationPending struct {
	eventSequence      int64
	eventID            string
	eventKind          string
	runID              string
	acceptedGeneration string
	recordedAtUnixNS   int64
	payloadJSON        string
	payloadSHA256      string
	previousSHA256     string
	measurementJSON    string
	eventSHA256        string
}

type durationLedgerRawUnknownFacts struct {
	ConfiguredShardsPerJob         DurationLedgerObservationMeasurement `json:"configured_shards_per_job"`
	ConfiguredMaxActiveCIWorkloads DurationLedgerObservationMeasurement `json:"configured_max_active_ci_workloads"`
	ReservationLeaseCount          DurationLedgerObservationMeasurement `json:"reservation_lease_count"`
	ReservationIdentityDigest      DurationLedgerObservationMeasurement `json:"reservation_identity_digest"`
	RuntimeGroupSize               DurationLedgerObservationMeasurement `json:"runtime_group_size"`
	LegacyShardSchemaVersion       DurationLedgerObservationMeasurement `json:"legacy_shard_schema_version"`
}

type durationLedgerRawObservationPayload struct {
	UnknownFacts durationLedgerRawUnknownFacts `json:"unknown_facts"`
	Value        any                           `json:"value"`
}

type durationLedgerRawObservationHashMaterial struct {
	EventSequence       int64  `json:"event_sequence"`
	EventID             string `json:"event_id"`
	EventKind           string `json:"event_kind"`
	RunID               string `json:"run_id"`
	AcceptedGeneration  string `json:"accepted_generation"`
	RecordedAtUnixNS    int64  `json:"recorded_at_unix_ns"`
	PayloadJSON         string `json:"payload_json"`
	PayloadSHA256       string `json:"payload_sha256"`
	PreviousEventSHA256 string `json:"previous_event_sha256"`
	MeasurementJSON     string `json:"measurement_json"`
}

type durationLedgerRawObservationIdentityMaterial struct {
	EventKind           string `json:"event_kind"`
	RunID               string `json:"run_id"`
	AcceptedGeneration  string `json:"accepted_generation"`
	RecordedAtUnixNS    int64  `json:"recorded_at_unix_ns"`
	PayloadSHA256       string `json:"payload_sha256"`
	PreviousEventSHA256 string `json:"previous_event_sha256"`
}

// LoadRawObservationEvents 读取并验证完整的无限制原始历史。
func (store *DurationLedgerStore) LoadRawObservationEvents() ([]DurationLedgerRawObservationEvent, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin duration ledger raw observation read", err)
	}
	defer transaction.Rollback()
	if err := verifyDurationLedgerRawObservationIntegrity(transaction); err != nil {
		return nil, err
	}
	events, err := loadDurationLedgerRawObservationEvents(transaction)
	if err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit duration ledger raw observation read", err)
	}
	return events, nil
}

// VerifyRawObservationIntegrity 检查追加链和每个内容摘要。
func (store *DurationLedgerStore) VerifyRawObservationIntegrity() error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return verifyDurationLedgerRawObservationIntegrity(database)
}

// LoadObservationReport 确定性聚合原始历史与只读文件系统事实。
func (store *DurationLedgerStore) LoadObservationReport() (DurationLedgerObservationReport, error) {
	if store == nil {
		return DurationLedgerObservationReport{}, errors.New("duration ledger store is nil")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return DurationLedgerObservationReport{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DurationLedgerObservationReport{}, mapDurationLedgerSQLiteError("begin duration ledger observation report", err)
	}
	defer transaction.Rollback()
	if err := verifyDurationLedgerRawObservationIntegrity(transaction); err != nil {
		return DurationLedgerObservationReport{}, err
	}
	report, err := store.aggregateDurationLedgerObservationReport(transaction, nil)
	if err != nil {
		return DurationLedgerObservationReport{}, err
	}
	if err := transaction.Commit(); err != nil {
		return DurationLedgerObservationReport{}, mapDurationLedgerSQLiteError("commit duration ledger observation report", err)
	}
	return report, nil
}

// appendDurationLedgerObservationEvent 在调用方提交前追加一个规范事件。
func (store *DurationLedgerStore) appendDurationLedgerObservationEvent(
	transaction *sql.Tx,
	eventKind string,
	runID string,
	acceptedGeneration string,
	payload any,
) error {
	if err := validateDurationLedgerObservationAppend(store, transaction, eventKind, runID, acceptedGeneration); err != nil {
		return err
	}
	payloadBytes, payloadDigest, err := marshalDurationLedgerObservationPayload(payload)
	if err != nil {
		return err
	}
	if err := verifyDurationLedgerRawObservationIntegrity(transaction); err != nil {
		return err
	}
	previousSHA, err := latestDurationLedgerRawObservationHash(transaction)
	if err != nil {
		return err
	}
	pending, err := newDurationLedgerRawObservationPending(store, transaction, eventKind, runID, acceptedGeneration, payloadBytes, payloadDigest, previousSHA)
	if err != nil {
		return err
	}
	report, err := store.aggregateDurationLedgerObservationReport(transaction, &pending)
	if err != nil {
		return err
	}
	if err := completeDurationLedgerRawObservationPending(&pending, report); err != nil {
		return err
	}
	return insertDurationLedgerRawObservationEvent(transaction, pending)
}

// validateDurationLedgerObservationAppend 校验追加事件的边界和不可推断身份。
func validateDurationLedgerObservationAppend(store *DurationLedgerStore, transaction *sql.Tx, eventKind, runID, acceptedGeneration string) error {
	if store == nil || transaction == nil {
		return errors.New("duration ledger observation store and transaction are required")
	}
	if strings.TrimSpace(eventKind) == "" || eventKind != strings.TrimSpace(eventKind) {
		return errors.New("duration ledger observation event kind is required")
	}
	if strings.TrimSpace(runID) != runID {
		return errors.New("duration ledger observation run ID is not canonical")
	}
	if err := validateDurationLedgerObservationGeneration(acceptedGeneration); err != nil {
		return err
	}
	if store.nowFunc == nil {
		return errors.New("duration ledger observation clock is required")
	}
	return nil
}

func marshalDurationLedgerObservationPayload(payload any) ([]byte, string, error) {
	payloadBytes, err := json.Marshal(durationLedgerRawObservationPayload{
		UnknownFacts: durationLedgerRawUnknownFacts{
			ConfiguredShardsPerJob:         unknownRawObservation("configured_shards_per_job"),
			ConfiguredMaxActiveCIWorkloads: unknownRawObservation("configured_max_active_ci_workloads"),
			ReservationLeaseCount:          unknownRawObservation("reservation_lease_count"),
			ReservationIdentityDigest:      unknownRawObservation("reservation_identity_digest"),
			RuntimeGroupSize:               unknownRawObservation("runtime_group_size"),
			LegacyShardSchemaVersion:       unknownRawObservation("legacy_shard_schema_version"),
		},
		Value: payload,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode duration ledger observation payload: %w", err)
	}
	if len(payloadBytes) == 0 || !json.Valid(payloadBytes) {
		return nil, "", errors.New("duration ledger observation payload is not valid JSON")
	}
	return payloadBytes, sha256Digest(payloadBytes), nil
}

func newDurationLedgerRawObservationPending(
	store *DurationLedgerStore,
	transaction *sql.Tx,
	eventKind, runID, acceptedGeneration string,
	payloadBytes []byte,
	payloadDigest, previousSHA string,
) (durationLedgerRawObservationPending, error) {
	recordedAt := store.nowFunc().UTC().UnixNano()
	identity, err := canonicalJSONDigest(durationLedgerRawObservationIdentityMaterial{
		EventKind: eventKind, RunID: runID, AcceptedGeneration: acceptedGeneration,
		RecordedAtUnixNS: recordedAt, PayloadSHA256: payloadDigest, PreviousEventSHA256: previousSHA,
	})
	if err != nil {
		return durationLedgerRawObservationPending{}, fmt.Errorf("derive duration ledger observation event ID: %w", err)
	}
	var nextSequence int64
	if err := transaction.QueryRow(`SELECT COALESCE(MAX(event_sequence), 0) + 1 FROM duration_ledger_raw_events`).Scan(&nextSequence); err != nil {
		return durationLedgerRawObservationPending{}, mapDurationLedgerSQLiteError("allocate duration ledger observation sequence", err)
	}
	if nextSequence <= 0 {
		return durationLedgerRawObservationPending{}, errors.New("duration ledger observation sequence overflow")
	}
	return durationLedgerRawObservationPending{
		eventSequence: nextSequence, eventID: identity, eventKind: eventKind, runID: runID,
		acceptedGeneration: acceptedGeneration, recordedAtUnixNS: recordedAt,
		payloadJSON: string(payloadBytes), payloadSHA256: payloadDigest, previousSHA256: previousSHA,
	}, nil
}

func completeDurationLedgerRawObservationPending(pending *durationLedgerRawObservationPending, report DurationLedgerObservationReport) error {
	measurementBytes, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode duration ledger observation report: %w", err)
	}
	pending.measurementJSON = string(measurementBytes)
	pending.eventSHA256, err = durationLedgerRawObservationEventDigest(*pending)
	if err != nil {
		return err
	}
	return nil
}

func insertDurationLedgerRawObservationEvent(transaction *sql.Tx, pending durationLedgerRawObservationPending) error {
	_, err := transaction.Exec(`
		INSERT INTO duration_ledger_raw_events (
			event_sequence, event_id, event_kind, run_id, accepted_generation,
			recorded_at_unix_ns, payload_json, payload_sha256, previous_event_sha256,
			measurement_json, event_sha256
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pending.eventSequence, pending.eventID, pending.eventKind, pending.runID,
		pending.acceptedGeneration, pending.recordedAtUnixNS, pending.payloadJSON,
		pending.payloadSHA256, pending.previousSHA256, pending.measurementJSON, pending.eventSHA256)
	if err != nil {
		return mapDurationLedgerSQLiteError("append duration ledger raw observation event", err)
	}
	return nil
}

type durationLedgerRawObservationAggregate struct {
	logicalBytes int64
	recordCount  int64
	runCount     int64
	earliest     sql.NullInt64
	latest       sql.NullInt64
}

func loadDurationLedgerRawObservationAggregate(queryer durationLedgerRawObservationQueryer) (durationLedgerRawObservationAggregate, error) {
	var aggregate durationLedgerRawObservationAggregate
	if err := queryer.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(length(payload_json)), 0), COUNT(*),
			COUNT(DISTINCT NULLIF(run_id, '')), MIN(recorded_at_unix_ns), MAX(recorded_at_unix_ns)
		FROM duration_ledger_raw_events
	`).Scan(&aggregate.logicalBytes, &aggregate.recordCount, &aggregate.runCount, &aggregate.earliest, &aggregate.latest); err != nil {
		return durationLedgerRawObservationAggregate{}, mapDurationLedgerSQLiteError("aggregate duration ledger raw observation history", err)
	}
	return aggregate, nil
}

// applyDurationLedgerRawObservationPending 将待追加事件纳入确定性计数和时间边界。
func applyDurationLedgerRawObservationPending(queryer durationLedgerRawObservationQueryer, aggregate *durationLedgerRawObservationAggregate, pending durationLedgerRawObservationPending) error {
	payloadLength := int64(len(pending.payloadJSON))
	if payloadLength < 0 || aggregate.logicalBytes > math.MaxInt64-payloadLength {
		return errors.New("duration ledger logical bytes overflow")
	}
	aggregate.logicalBytes += payloadLength
	aggregate.recordCount++
	if pending.runID != "" {
		var exists int
		if err := queryer.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM duration_ledger_raw_events WHERE run_id = ?)`, pending.runID).Scan(&exists); err != nil {
			return mapDurationLedgerSQLiteError("aggregate duration ledger raw run count", err)
		}
		if exists == 0 {
			aggregate.runCount++
		}
	}
	if !aggregate.earliest.Valid || pending.recordedAtUnixNS < aggregate.earliest.Int64 {
		aggregate.earliest = sql.NullInt64{Int64: pending.recordedAtUnixNS, Valid: true}
	}
	if !aggregate.latest.Valid || pending.recordedAtUnixNS > aggregate.latest.Int64 {
		aggregate.latest = sql.NullInt64{Int64: pending.recordedAtUnixNS, Valid: true}
	}
	return nil
}

// aggregateDurationLedgerObservationReport 在同一权威事务内确定性生成七项报告。
func (store *DurationLedgerStore) aggregateDurationLedgerObservationReport(
	queryer durationLedgerRawObservationQueryer,
	pending *durationLedgerRawObservationPending,
) (DurationLedgerObservationReport, error) {
	aggregate, err := loadDurationLedgerRawObservationAggregate(queryer)
	if err != nil {
		return DurationLedgerObservationReport{}, err
	}
	if pending != nil {
		if err := applyDurationLedgerRawObservationPending(queryer, &aggregate, *pending); err != nil {
			return DurationLedgerObservationReport{}, err
		}
	}
	facts, providerWarning, err := store.durationLedgerObservationFilesystemFacts()
	if err != nil {
		return DurationLedgerObservationReport{}, err
	}
	report := DurationLedgerObservationReport{
		LedgerLogicalBytes:       knownObservation(aggregate.logicalBytes, "raw_event_payload_bytes"),
		LedgerPhysicalBytes:      filesystemObservation(facts.PhysicalBytes, "filesystem_stat_provider", providerWarning, "physical bytes were not available"),
		RecordCount:              knownObservation(aggregate.recordCount, "raw_event_authority"),
		RunCount:                 knownObservation(aggregate.runCount, "raw_event_authority"),
		EarliestRecordedAtUnixNS: observationTime(aggregate.earliest, "earliest recorded raw event is unavailable"),
		LatestRecordedAtUnixNS:   observationTime(aggregate.latest, "latest recorded raw event is unavailable"),
		FilesystemAvailableBytes: filesystemObservation(facts.AvailableBytes, "filesystem_statfs_provider", providerWarning, "filesystem available bytes were not available"),
	}
	if err := report.Validate(); err != nil {
		return DurationLedgerObservationReport{}, err
	}
	return report, nil
}

// durationLedgerObservationFilesystemFacts 读取只读文件系统提供方并保留 UNKNOWN 警告。
func (store *DurationLedgerStore) durationLedgerObservationFilesystemFacts() (durationLedgerObservationFilesystemFacts, string, error) {
	provider := store.observationFilesystemProvider
	if provider == nil {
		provider = defaultDurationLedgerObservationFilesystemProvider
	}
	facts, err := provider(store.path)
	if err != nil && !errors.Is(err, errDurationLedgerObservationUnavailable) {
		return durationLedgerObservationFilesystemFacts{}, "", fmt.Errorf("duration ledger observation filesystem provider: %w", err)
	}
	if facts.PhysicalBytes != nil && *facts.PhysicalBytes < 0 {
		return durationLedgerObservationFilesystemFacts{}, "", errors.New("duration ledger physical bytes provider returned a negative value")
	}
	if facts.AvailableBytes != nil && *facts.AvailableBytes < 0 {
		return durationLedgerObservationFilesystemFacts{}, "", errors.New("duration ledger filesystem available bytes provider returned a negative value")
	}
	warning := ""
	if err != nil {
		warning = err.Error()
	}
	return facts, warning, nil
}

// defaultDurationLedgerObservationFilesystemProvider 从 authority 文件和父文件系统读取容量事实。
func defaultDurationLedgerObservationFilesystemProvider(path string) (durationLedgerObservationFilesystemFacts, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return durationLedgerObservationFilesystemFacts{}, fmt.Errorf("%w: stat ledger authority: %v", errDurationLedgerObservationUnavailable, err)
	}
	if stat.Blocks < 0 || stat.Blocks > math.MaxInt64/512 {
		return durationLedgerObservationFilesystemFacts{}, errors.New("duration ledger physical bytes provider returned malformed block count")
	}
	physical := stat.Blocks * 512
	facts := durationLedgerObservationFilesystemFacts{PhysicalBytes: &physical}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &filesystem); err != nil {
		return facts, fmt.Errorf("%w: stat filesystem capacity: %v", errDurationLedgerObservationUnavailable, err)
	}
	if uint64(filesystem.Bsize) != 0 && filesystem.Bavail > uint64(math.MaxInt64)/uint64(filesystem.Bsize) {
		return facts, errors.New("filesystem available bytes provider returned malformed capacity")
	}
	available := int64(filesystem.Bavail) * int64(filesystem.Bsize)
	facts.AvailableBytes = &available
	return facts, nil
}

func loadDurationLedgerRawObservationEvents(queryer durationLedgerRawObservationQueryer) ([]DurationLedgerRawObservationEvent, error) {
	rows, err := queryer.QueryContext(context.Background(), `
		SELECT event_sequence, event_id, event_kind, run_id, accepted_generation,
			recorded_at_unix_ns, payload_json, payload_sha256, previous_event_sha256,
			measurement_json, event_sha256
		FROM duration_ledger_raw_events ORDER BY event_sequence
	`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("load duration ledger raw observation events", err)
	}
	defer rows.Close()
	var events []DurationLedgerRawObservationEvent
	for rows.Next() {
		var event DurationLedgerRawObservationEvent
		if err := rows.Scan(&event.EventSequence, &event.EventID, &event.EventKind, &event.RunID, &event.AcceptedGeneration, &event.RecordedAtUnixNS, &event.PayloadJSON, &event.PayloadSHA256, &event.PreviousEventSHA256, &event.MeasurementJSON, &event.EventSHA256); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan duration ledger raw observation event", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate duration ledger raw observation events", err)
	}
	return events, nil
}

// verifyDurationLedgerRawObservationIntegrity 验证无限原始链的序列、载荷和摘要。
func verifyDurationLedgerRawObservationIntegrity(queryer durationLedgerRawObservationQueryer) error {
	events, err := loadDurationLedgerRawObservationEvents(queryer)
	if err != nil {
		return err
	}
	var previous string
	for index, event := range events {
		next, err := verifyDurationLedgerRawObservationEvent(index, event, previous)
		if err != nil {
			return err
		}
		previous = next
	}
	return nil
}

// verifyDurationLedgerRawObservationEvent 校验单行序列、身份及链接关系。
func verifyDurationLedgerRawObservationEvent(index int, event DurationLedgerRawObservationEvent, previous string) (string, error) {
	if event.EventSequence != int64(index+1) {
		return "", fmt.Errorf("duration ledger raw observation sequence %d is not contiguous", event.EventSequence)
	}
	if err := validateDurationLedgerObservationGeneration(event.AcceptedGeneration); err != nil {
		return "", err
	}
	if event.EventID == "" || !isDurationLedgerObservationDigest(event.EventID) {
		return "", fmt.Errorf("duration ledger raw observation event ID is invalid")
	}
	if strings.TrimSpace(event.EventKind) == "" || event.EventKind != strings.TrimSpace(event.EventKind) || strings.TrimSpace(event.RunID) != event.RunID {
		return "", errors.New("duration ledger raw observation event identity is invalid")
	}
	if err := verifyDurationLedgerRawObservationPayload(event, previous); err != nil {
		return "", err
	}
	if err := verifyDurationLedgerRawObservationHashes(event); err != nil {
		return "", err
	}
	return event.EventSHA256, nil
}

// verifyDurationLedgerRawObservationPayload 校验原始载荷和已发布测量的可解码性。
func verifyDurationLedgerRawObservationPayload(event DurationLedgerRawObservationEvent, previous string) error {
	if !json.Valid([]byte(event.PayloadJSON)) {
		return fmt.Errorf("duration ledger raw observation payload is invalid")
	}
	if event.PayloadSHA256 != sha256Digest([]byte(event.PayloadJSON)) {
		return fmt.Errorf("duration ledger raw observation payload sha256 mismatch")
	}
	if event.PreviousEventSHA256 != previous {
		return fmt.Errorf("duration ledger raw observation previous event sha256 mismatch at sequence %d", event.EventSequence)
	}
	if event.MeasurementJSON == "" {
		return errors.New("duration ledger raw observation measurement is missing")
	}
	var report DurationLedgerObservationReport
	if err := decodeStrictDurationLedgerObservationReport([]byte(event.MeasurementJSON), &report); err != nil {
		return fmt.Errorf("duration ledger raw observation measurement: %w", err)
	}
	return nil
}

func verifyDurationLedgerRawObservationHashes(event DurationLedgerRawObservationEvent) error {
	expectedEventID, err := canonicalJSONDigest(durationLedgerRawObservationIdentityMaterial{
		EventKind: event.EventKind, RunID: event.RunID, AcceptedGeneration: event.AcceptedGeneration,
		RecordedAtUnixNS: event.RecordedAtUnixNS, PayloadSHA256: event.PayloadSHA256,
		PreviousEventSHA256: event.PreviousEventSHA256,
	})
	if err != nil {
		return err
	}
	if event.EventID != expectedEventID {
		return fmt.Errorf("duration ledger raw observation event ID mismatch at sequence %d", event.EventSequence)
	}
	expectedHash, err := durationLedgerRawObservationEventDigest(durationLedgerRawObservationPending{
		eventSequence: event.EventSequence, eventID: event.EventID, eventKind: event.EventKind,
		runID: event.RunID, acceptedGeneration: event.AcceptedGeneration,
		recordedAtUnixNS: event.RecordedAtUnixNS, payloadJSON: event.PayloadJSON,
		payloadSHA256: event.PayloadSHA256, previousSHA256: event.PreviousEventSHA256,
		measurementJSON: event.MeasurementJSON,
	})
	if err != nil {
		return err
	}
	if event.EventSHA256 != expectedHash {
		return fmt.Errorf("duration ledger raw observation event sha256 mismatch at sequence %d", event.EventSequence)
	}
	return nil
}

func decodeStrictDurationLedgerObservationReport(data []byte, report *DurationLedgerObservationReport) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(report); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("duration ledger observation report has trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode duration ledger observation report trailing JSON: %w", err)
	}
	return report.Validate()
}

func latestDurationLedgerRawObservationHash(queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var hash string
	err := queryer.QueryRowContext(context.Background(), `SELECT event_sha256 FROM duration_ledger_raw_events ORDER BY event_sequence DESC LIMIT 1`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", mapDurationLedgerSQLiteError("load previous duration ledger raw observation hash", err)
	}
	if !isDurationLedgerObservationDigest(hash) {
		return "", errors.New("previous duration ledger raw observation hash is invalid")
	}
	return hash, nil
}

func durationLedgerRawObservationEventDigest(pending durationLedgerRawObservationPending) (string, error) {
	return canonicalJSONDigest(durationLedgerRawObservationHashMaterial{
		EventSequence: pending.eventSequence, EventID: pending.eventID, EventKind: pending.eventKind,
		RunID: pending.runID, AcceptedGeneration: pending.acceptedGeneration,
		RecordedAtUnixNS: pending.recordedAtUnixNS, PayloadJSON: pending.payloadJSON,
		PayloadSHA256: pending.payloadSHA256, PreviousEventSHA256: pending.previousSHA256,
		MeasurementJSON: pending.measurementJSON,
	})
}

func canonicalJSONDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Digest(encoded), nil
}

func sha256Digest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func isDurationLedgerObservationDigest(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && isSHA256Digest(value[7:])
}

func validateDurationLedgerObservationGeneration(value string) error {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value {
		return fmt.Errorf("duration ledger raw observation accepted generation %q is invalid", value)
	}
	return nil
}

func knownObservation(value int64, provenance string) DurationLedgerObservationMeasurement {
	copy := value
	return DurationLedgerObservationMeasurement{Status: DurationLedgerObservationKnown, Value: &copy, Provenance: provenance}
}

func unknownRawObservation(field string) DurationLedgerObservationMeasurement {
	return DurationLedgerObservationMeasurement{
		Status:     DurationLedgerObservationUnknown,
		Provenance: "not_authorized_producer",
		Warning:    field + " producer is not authorized",
	}
}

func filesystemObservation(value *int64, provenance, providerWarning, absentWarning string) DurationLedgerObservationMeasurement {
	if value == nil {
		warning := absentWarning
		if providerWarning != "" {
			warning += ": " + providerWarning
		}
		return DurationLedgerObservationMeasurement{Status: DurationLedgerObservationUnknown, Provenance: provenance, Warning: warning}
	}
	copy := *value
	return DurationLedgerObservationMeasurement{Status: DurationLedgerObservationKnown, Value: &copy, Provenance: provenance}
}

func observationTime(value sql.NullInt64, absentWarning string) DurationLedgerObservationMeasurement {
	if !value.Valid {
		return DurationLedgerObservationMeasurement{Status: DurationLedgerObservationUnknown, Provenance: "raw_event_authority", Warning: absentWarning}
	}
	return knownObservation(value.Int64, "raw_event_authority")
}
