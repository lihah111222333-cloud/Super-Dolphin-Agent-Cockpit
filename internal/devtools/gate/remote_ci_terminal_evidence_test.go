package gate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRemoteCITerminalEvidenceSQLiteRoundTrip(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	record := remoteCITerminalEvidenceRoundTripRecord(t, store)
	assertRemoteCITerminalEvidenceRoundTrip(t, store, record)
	assertRemoteCITerminalEvidenceNormalizedCounts(t, store, record.JobID)
}

func TestRemoteCITerminalEvidenceSQLiteLoadUsesPrimaryKeyIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertSQLiteQueryPlan(
		t, database,
		`SELECT shard_identity, container_kind, ordinal, name, state, exit_code, reason, message
		 FROM ci_shard_terminal_containers WHERE job_id = ? ORDER BY shard_identity, container_kind, ordinal`,
		[]string{"USING INDEX sqlite_autoindex_ci_shard_terminal_containers_1"},
		[]string{"ci_shard_terminal_containers"},
		"job",
	)
	assertSQLiteQueryPlan(
		t, database,
		`SELECT shard_identity, ordinal, type, reason, message, count, last_timestamp
		 FROM ci_shard_terminal_events WHERE job_id = ? ORDER BY shard_identity, ordinal`,
		[]string{"USING INDEX sqlite_autoindex_ci_shard_terminal_events_1"},
		[]string{"ci_shard_terminal_events"},
		"job",
	)
}

func TestRemoteCITerminalEvidenceSQLiteRejectsOrdinalGap(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_shard_terminal_containers(job_id, shard_identity, container_kind, ordinal, name, state, reason, message) VALUES ('job-corrupt', 'shard-corrupt', 'container', 1, 'worker', 'Terminated', 'OOMKilled', 'memory limit exceeded')`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRemoteCIShardTerminalEvidence(database, "job-corrupt"); err == nil || !strings.Contains(err.Error(), "ordinal") {
		t.Fatalf("ordinal-gap evidence load error = %v, want contiguous-ordinal refusal", err)
	}
}

func TestRemoteCITerminalEvidenceRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe})
	exitCode := int64(1)
	evidence := &RemoteCITerminalEvidence{
		Containers: []RemoteCIContainerTerminalEvidence{{Name: "worker", State: "Terminated", ExitCode: &exitCode, Reason: invalid}},
	}
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 evidence validation error = %v, want fail-fast refusal", err)
	}
}

func remoteCITerminalEvidenceRoundTripRecord(t *testing.T, store *DurationLedgerStore) RemoteCIRunRecord {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := durationLedgerSQLiteRecordManualSelectionCatalog(t, store, now)
	record := durationLedgerSQLiteManualSelectionRecord(now, digest)
	record.JobID = "terminal-evidence-roundtrip"
	record.Status = ResultStatusFailed
	record.ErrorText = `shard sha256:terminal-provider container_status="Failed" exit_code=137 reason="OOMKilled"`
	execution := durationLedgerSQLitePopulateManualSelectionRecord(t, &record, now)
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	record.Shards[0].ContainerStatus = "Failed"
	exitCode := int64(137)
	record.Shards[0].TerminalEvidence = &RemoteCITerminalEvidence{
		Containers:     []RemoteCIContainerTerminalEvidence{{Name: "worker", State: "Terminated", ExitCode: &exitCode, Reason: "OOMKilled", Message: "memory limit exceeded"}},
		InitContainers: []RemoteCIContainerTerminalEvidence{{Name: "materializer", State: "Terminated", Reason: "Completed"}},
		Events:         []RemoteCIEventEvidence{{Type: "Warning", Reason: "BackOff", Message: "worker exited", Count: 2, LastTimestamp: "2026-08-07T00:00:00Z"}},
	}
	return record
}

func assertRemoteCITerminalEvidenceRoundTrip(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord) {
	t.Helper()
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record terminal evidence run: %v", err)
	}
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("load terminal evidence run: %v", err)
	}
	if !reflect.DeepEqual(loaded.Shards[0].TerminalEvidence, record.Shards[0].TerminalEvidence) {
		t.Fatalf("loaded terminal evidence = %#v, want %#v", loaded.Shards[0].TerminalEvidence, record.Shards[0].TerminalEvidence)
	}
	if !strings.Contains(loaded.ErrorText, `exit_code=137`) || !strings.Contains(loaded.ErrorText, "OOMKilled") {
		t.Fatalf("loaded first-cause error text = %q", loaded.ErrorText)
	}
}

func assertRemoteCITerminalEvidenceNormalizedCounts(t *testing.T, store *DurationLedgerStore, jobID string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var containers, events int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_shard_terminal_containers WHERE job_id = ?`, jobID).Scan(&containers); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_shard_terminal_events WHERE job_id = ?`, jobID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if containers != 2 || events != 1 {
		t.Fatalf("normalized terminal evidence counts = (%d, %d), want (2, 1)", containers, events)
	}
}
