package gate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const observationHistoryEventCount = 18

func TestDurationLedgerObservationRawHistoryAndReport(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	store.nowFunc = func() time.Time { return time.Unix(123, 456).UTC() }
	createObservationLedgerAndAssert(t, store)
	appendObservationSampleAndAssert(t, store)
}

func createObservationLedgerAndAssert(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	assertInitialObservationEvent(t, events)
	assertUnknownRawFacts(t, events[0])
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	assertPositiveLogicalBytes(t, report)
	assertKnownObservation(t, report.RecordCount, 1, false)
	assertKnownObservation(t, report.RunCount, 0, false)
	assertKnownObservation(t, report.EarliestRecordedAtUnixNS, time.Unix(123, 456).UnixNano(), false)
	assertKnownObservation(t, report.LatestRecordedAtUnixNS, time.Unix(123, 456).UnixNano(), false)
}

func assertInitialObservationEvent(t *testing.T, events []DurationLedgerRawObservationEvent) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("initial raw event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.EventKind != durationLedgerObservationEventLedgerReplace || event.PreviousEventSHA256 != "" || event.EventSHA256 == "" || event.PayloadSHA256 == "" {
		t.Fatalf("initial raw event = %#v, want anchored ledger replace", event)
	}
}

func assertPositiveLogicalBytes(t *testing.T, report DurationLedgerObservationReport) {
	t.Helper()
	if report.LedgerLogicalBytes.Value == nil || *report.LedgerLogicalBytes.Value <= 0 {
		t.Fatalf("logical bytes = %#v, want positive canonical raw payload bytes", report.LedgerLogicalBytes)
	}
}

func appendObservationSampleAndAssert(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	seedAcceptedGenerationForTest(t, store, 1)
	store.nowFunc = func() time.Time { return time.Unix(124, 0).UTC() }
	if _, err := store.AppendSamplesFast(1, []DurationSample{testDurationSample("observed", testWorkloadDigest, true, 17)}); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyRawObservationIntegrity(); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].PreviousEventSHA256 != events[0].EventSHA256 {
		t.Fatalf("raw event chain = %#v, want two linked events", events)
	}
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	assertKnownObservation(t, report.RecordCount, 2, false)
	assertKnownObservation(t, report.LatestRecordedAtUnixNS, time.Unix(124, 0).UnixNano(), false)
}

func assertUnknownRawFacts(t *testing.T, event DurationLedgerRawObservationEvent) {
	t.Helper()
	var envelope durationLedgerRawObservationPayload
	if err := json.Unmarshal([]byte(event.PayloadJSON), &envelope); err != nil {
		t.Fatal(err)
	}
	for field, measurement := range map[string]DurationLedgerObservationMeasurement{
		"configured_shards_per_job":          envelope.UnknownFacts.ConfiguredShardsPerJob,
		"configured_max_active_ci_workloads": envelope.UnknownFacts.ConfiguredMaxActiveCIWorkloads,
		"reservation_lease_count":            envelope.UnknownFacts.ReservationLeaseCount,
		"reservation_identity_digest":        envelope.UnknownFacts.ReservationIdentityDigest,
		"runtime_group_size":                 envelope.UnknownFacts.RuntimeGroupSize,
		"legacy_shard_schema_version":        envelope.UnknownFacts.LegacyShardSchemaVersion,
	} {
		if measurement.Status != DurationLedgerObservationUnknown || measurement.Value != nil || measurement.Warning == "" {
			t.Fatalf("raw UNKNOWN field %s = %#v", field, measurement)
		}
	}
}

func TestDurationLedgerObservationIsAppendOnlyAndDetectsTamper(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if _, err := database.Exec(`UPDATE duration_ledger_raw_events SET payload_json = '{}'`); err == nil {
		t.Fatal("raw event update unexpectedly succeeded")
	}
	if _, err := database.Exec(`DELETE FROM duration_ledger_raw_events`); err == nil {
		t.Fatal("raw event delete unexpectedly succeeded")
	}
	if err := store.VerifyRawObservationIntegrity(); err != nil {
		t.Fatalf("verify untampered raw history: %v", err)
	}

	if _, err := database.Exec(`DROP TRIGGER duration_ledger_raw_events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE duration_ledger_raw_events SET payload_json = '{}'`); err != nil {
		t.Fatal(err)
	}
	if err := verifyDurationLedgerRawObservationIntegrity(database); err == nil || !strings.Contains(err.Error(), "payload sha256") {
		t.Fatalf("tampered raw history error = %v, want payload digest failure", err)
	}
}

func TestDurationLedgerObservationRollbackDoesNotPublishEvent(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	err = withSQLiteWriteTransaction(database, "observation rollback test", func(transaction *sql.Tx) error {
		if err := store.appendDurationLedgerObservationEvent(transaction, durationLedgerObservationEventTest, "run-rollback", "1", map[string]any{"status": "cancelled"}); err != nil {
			return err
		}
		return errors.New("forced rollback")
	})
	if err == nil || !strings.Contains(err.Error(), "forced rollback") {
		t.Fatalf("rollback transaction error = %v, want forced rollback", err)
	}
	after, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("raw event count after rollback = %d, want %d", len(after), len(before))
	}
}

func TestDurationLedgerObservationUnknownCapacityIsWarningOnly(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	store.observationFilesystemProvider = func(string) (durationLedgerObservationFilesystemFacts, error) {
		return durationLedgerObservationFilesystemFacts{}, errDurationLedgerObservationUnavailable
	}
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatalf("unknown capacity must not fail ledger persist: %v", err)
	}
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	assertUnknownObservation(t, report.LedgerPhysicalBytes)
	assertUnknownObservation(t, report.FilesystemAvailableBytes)
	assertKnownObservation(t, report.RecordCount, 1, false)
}

func TestDurationLedgerObservationRejectsMalformedCapacity(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	negative := int64(-1)
	store.observationFilesystemProvider = func(string) (durationLedgerObservationFilesystemFacts, error) {
		return durationLedgerObservationFilesystemFacts{PhysicalBytes: &negative}, nil
	}
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err == nil || !strings.Contains(err.Error(), "physical bytes") {
		t.Fatalf("malformed capacity error = %v, want fail-fast physical bytes validation", err)
	}
	if _, err := store.LoadRawObservationEvents(); err != nil {
		t.Fatal(err)
	}
}

func TestDurationLedgerObservationSchemaMigrationPreservesProjectionAndAddsRawHistory(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "observability-migration.sqlite")
	defer database.Close()
	legacy := durationLedgerSQLiteLegacySchemaStatements()
	for _, statement := range legacy {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	if _, err := database.Exec(`
		INSERT INTO duration_ledger_meta(singleton, authority_id, schema_version, generation, ledger_version)
		VALUES (1, 'duration-ledger-sqlite/v1', 1, '1', 1);
		PRAGMA user_version = 5;
	`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	if got := sqliteTableColumns(t, database, "duration_ledger_raw_events"); len(got) == 0 {
		t.Fatal("migrated schema has no raw event columns")
	}
	var generation string
	if err := database.QueryRow(`SELECT generation FROM duration_ledger_meta WHERE singleton = 1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != "1" {
		t.Fatalf("projection generation after migration = %q, want 1", generation)
	}
}

func assertKnownObservation(t *testing.T, observation DurationLedgerObservationMeasurement, want int64, requireProvenance bool) {
	t.Helper()
	if observation.Status != DurationLedgerObservationKnown || observation.Value == nil || *observation.Value != want {
		t.Fatalf("observation = %#v, want known value %d", observation, want)
	}
	if requireProvenance && strings.TrimSpace(observation.Provenance) == "" {
		t.Fatalf("observation = %#v, want provenance", observation)
	}
}

func assertUnknownObservation(t *testing.T, observation DurationLedgerObservationMeasurement) {
	t.Helper()
	if observation.Status != DurationLedgerObservationUnknown || observation.Value != nil || strings.TrimSpace(observation.Warning) == "" {
		t.Fatalf("observation = %#v, want warning-bearing UNKNOWN", observation)
	}
}

func TestDurationLedgerObservationReportUsesExactMetricNames(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{
		"ledger_logical_bytes", "ledger_physical_bytes", "record_count", "run_count",
		"earliest_recorded_at_unix_ns", "latest_recorded_at_unix_ns", "filesystem_available_bytes",
	} {
		if !strings.Contains(string(encoded), fmt.Sprintf("%q", metric)) {
			t.Fatalf("report JSON %s lacks exact metric name: %s", metric, encoded)
		}
	}
}

func TestDurationLedgerObservationFinalizationPublishesAtomically(t *testing.T) {
	assertSuccessfulObservationFinalization(t)
	assertFailedObservationFinalizationRollsBack(t)
}

func assertSuccessfulObservationFinalization(t *testing.T) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordProvisionalWorkloadPassRun(t, store, "observability-finalize", 1, "observability-workload")
	before, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 || after[len(after)-1].EventKind != durationLedgerObservationEventRemoteRunFinalize || after[len(after)-1].RunID != record.JobID {
		t.Fatalf("finalization raw events = %#v, want one atomic finalization event", after)
	}
}

func assertFailedObservationFinalizationRollsBack(t *testing.T) {
	t.Helper()
	failingStore := newWorkloadPassEvidenceStore(t, 1)
	failingRecord, _, failingReceipts := recordProvisionalWorkloadPassRun(t, failingStore, "observability-finalize-rollback", 1, "observability-rollback-workload")
	installFinalizeFailure(t, failingStore, durationLedgerFinalizeStepPromotion)
	failingBefore, err := failingStore.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := failingStore.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(failingRecord), failingReceipts, nil, true); err == nil {
		t.Fatal("injected finalization failure unexpectedly succeeded")
	}
	failingAfter, err := failingStore.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(failingAfter) != len(failingBefore) {
		t.Fatalf("raw events after failed finalization = %d, want %d", len(failingAfter), len(failingBefore))
	}
}

func TestDurationLedgerObservationHistorySurvivesCompactionAndRetainsOutliers(t *testing.T) {
	store := buildObservationCompactionHistory(t)
	assertObservationHistorySurvivesCompaction(t, store)
	appendObservationOutlier(t, store)
}

func buildObservationCompactionHistory(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	now := time.Unix(500, 0).UTC()
	store.nowFunc = func() time.Time {
		current := now
		now = now.Add(time.Nanosecond)
		return current
	}
	for generation := uint64(0); generation < observationHistoryEventCount; generation++ {
		if _, err := store.CompareAndSwap(generation, NewDurationLedger()); err != nil {
			t.Fatalf("replace generation %d: %v", generation, err)
		}
	}
	seedAcceptedGenerationForTest(t, store, observationHistoryEventCount)
	return store
}

func assertObservationHistorySurvivesCompaction(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := withSQLiteWriteTransaction(database, "observability compaction test", func(transaction *sql.Tx) error {
		return compactDurationLedgerAuthority(transaction)
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != observationHistoryEventCount {
		t.Fatalf("raw history after compaction = %d, want unlimited %d", len(events), observationHistoryEventCount)
	}
}

func appendObservationOutlier(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := withSQLiteWriteTransaction(database, "observability outlier test", func(transaction *sql.Tx) error {
		return store.appendDurationLedgerObservationEvent(transaction, durationLedgerObservationEventTest, "outlier-run", "18", map[string]any{"status": "failed", "duration_ms": 999999999})
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != observationHistoryEventCount+1 || !strings.Contains(events[len(events)-1].PayloadJSON, "999999999") {
		t.Fatalf("outlier raw history = %#v, want preserved outlier", events[len(events)-1])
	}
}

func TestDurationLedgerObservationDeterministicAggregationAcrossColdAndWarmReads(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	physical, available := int64(1234), int64(5678)
	store.observationFilesystemProvider = func(string) (durationLedgerObservationFilesystemFacts, error) {
		return durationLedgerObservationFilesystemFacts{PhysicalBytes: &physical, AvailableBytes: &available}, nil
	}
	store.nowFunc = func() time.Time { return time.Unix(700, 8).UTC() }
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	warm, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	cold, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(warm, cold) {
		t.Fatalf("warm report = %#v, cold report = %#v", warm, cold)
	}
	assertKnownObservation(t, warm.LedgerPhysicalBytes, physical, true)
	assertKnownObservation(t, warm.FilesystemAvailableBytes, available, true)
}

func TestDurationLedgerObservationPreservesFailureTimeoutCancelAndOutlierFacts(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	appendObservationTerminalStatuses(t, store)
	assertObservationTerminalStatuses(t, store)
}

func appendObservationTerminalStatuses(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := withSQLiteWriteTransaction(database, "observability terminal statuses", func(transaction *sql.Tx) error {
		for _, status := range []string{"failed", "timeout", "cancelled"} {
			if err := store.appendDurationLedgerObservationEvent(transaction, durationLedgerObservationEventTest, "run-"+status, "1", map[string]any{"status": status, "duration_ms": 999999999}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
}

func assertObservationTerminalStatuses(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	events, err := store.LoadRawObservationEvents()
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "timeout", "cancelled"} {
		found := false
		for _, event := range events {
			if strings.Contains(event.PayloadJSON, fmt.Sprintf(`"status":"%s"`, status)) && strings.Contains(event.PayloadJSON, "999999999") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("status %s was not preserved in raw history", status)
		}
	}
	report, err := store.LoadObservationReport()
	if err != nil {
		t.Fatal(err)
	}
	assertKnownObservation(t, report.RunCount, 3, false)
}
