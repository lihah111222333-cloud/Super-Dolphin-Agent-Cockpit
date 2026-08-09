package gate

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestLoadRemoteCIRunRequiresCurrentExecutionProfile 证明旧行不能从 started/completed 推导第二份耗时。
func TestLoadRemoteCIRunRequiresCurrentExecutionProfile(t *testing.T) {
	profile := ExecutionProfile{
		CacheSource:      "go_build_cache",
		CacheStatus:      CacheObservationMiss,
		CacheMeasurement: "measured",
		CacheMissCount:   1,
		StartupMS:        1,
		TestBodyMS:       1,
		TotalMS:          2,
	}
	encodedProfile, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	nullGoFlagsProfile := strings.Replace(string(encodedProfile), `"go_flags":""`, `"go_flags":null`, 1)
	nonStringGoFlagsProfile := strings.Replace(string(encodedProfile), `"go_flags":""`, `"go_flags":1`, 1)
	tests := []struct {
		name    string
		profile string
		wantErr string
	}{
		{name: "current structured profile", profile: string(encodedProfile)},
		{name: "missing profile", wantErr: "execution profile is required"},
		{name: "whole null profile", profile: "null", wantErr: "stored remote CI execution profile is invalid"},
		{name: "legacy zero profile", profile: `{}`, wantErr: "stored remote CI execution profile is invalid"},
		{name: "null go flags", profile: nullGoFlagsProfile, wantErr: "stored remote CI execution profile is invalid"},
		{name: "non-string go flags", profile: nonStringGoFlagsProfile, wantErr: "stored remote CI execution profile is invalid"},
		{name: "unknown field", profile: strings.TrimSuffix(string(encodedProfile), "}") + `,"legacy_timing_ms":1}`, wantErr: "execution profile is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, 1)
			jobID := "strict-profile-" + strings.ReplaceAll(test.name, " ", "-")
			record, _, _ := recordWorkloadPassRun(t, store, jobID, 1, string(GateIDWhitespaceCheck))
			insertRemoteCIGateExecutionProfile(t, store, record, test.profile)

			loaded, loadErr := store.LoadRemoteCIRun(jobID)
			if test.wantErr != "" {
				if loadErr == nil || !strings.Contains(loadErr.Error(), test.wantErr) {
					t.Fatalf("LoadRemoteCIRun() error = %v", loadErr)
				}
				return
			}
			if loadErr != nil {
				t.Fatalf("LoadRemoteCIRun() error = %v", loadErr)
			}
			if len(loaded.Executions) != 1 || !reflect.DeepEqual(loaded.Executions[0].ExecutionProfile, profile) {
				t.Fatalf("loaded executions = %#v, want profile %#v", loaded.Executions, profile)
			}
		})
	}
}

// TestLoadRemoteCIRunAcceptsAggregateExecutionProfileOverlap 证明 parent gate 的阶段区间可重叠但均受关键路径约束。
func TestLoadRemoteCIRunAcceptsAggregateExecutionProfileOverlap(t *testing.T) {
	profile := ExecutionProfile{
		CacheSource:      "none",
		CacheStatus:      CacheObservationNotApplicable,
		CacheMeasurement: "measured",
		StartupMS:        2,
		TestBodyMS:       2,
		TotalMS:          2,
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	nullGoFlagsProfile := strings.Replace(string(encoded), `"go_flags":""`, `"go_flags":null`, 1)
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordWorkloadPassRun(t, store, "aggregate-overlap-profile", 1, string(GateIDWhitespaceCheck))
	insertRemoteCIGateExecutionProfile(t, store, record, string(encoded))

	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	if len(loaded.Executions) != 1 || !reflect.DeepEqual(loaded.Executions[0].ExecutionProfile, profile) {
		t.Fatalf("loaded executions = %#v, want aggregate profile %#v", loaded.Executions, profile)
	}
	updateRemoteCIGateExecutionProfile(t, store, record.JobID, nullGoFlagsProfile)
	if _, err := store.LoadRemoteCIRun(record.JobID); err == nil || !strings.Contains(err.Error(), "stored remote CI execution profile is invalid") {
		t.Fatalf("LoadRemoteCIRun() null aggregate GoFlags error = %v", err)
	}
}

func TestLoadRemoteCIRunCanonicalAggregateTimingRoundTripRejectsDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordWorkloadPassRun(t, store, "aggregate-canonical-roundtrip", 1, string(GateIDWhitespaceCheck))
	rawStarted := record.StartedAt.Add(123456789 * time.Nanosecond)
	rawCompleted := rawStarted.Add(12*time.Millisecond + 900*time.Microsecond)
	started, completed, totalMS, err := CanonicalExecutionInterval(rawStarted, rawCompleted)
	if err != nil {
		t.Fatal(err)
	}
	profile := ExecutionProfile{
		CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured",
		StartupMS: 1, TestBodyMS: 2, TotalMS: totalMS,
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	insertRemoteCIGateExecutionProfileAt(t, store, record, started, completed, string(encoded))
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() canonical roundtrip error = %v", err)
	}
	assertCanonicalGateExecution(t, loaded.Executions, started, completed, totalMS)
	corrupted := profile
	corrupted.TotalMS++
	corruptedJSON, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatal(err)
	}
	updateRemoteCIGateExecutionProfile(t, store, record.JobID, string(corruptedJSON))
	if _, err := store.LoadRemoteCIRun(record.JobID); err == nil || !strings.Contains(err.Error(), "stored remote CI aggregate execution interval is invalid") {
		t.Fatalf("LoadRemoteCIRun() corrupted aggregate error = %v", err)
	}
}

func assertCanonicalGateExecution(t *testing.T, executions []PlanGateExecution, started, completed time.Time, totalMS int64) {
	t.Helper()
	if len(executions) != 1 {
		t.Fatalf("loaded canonical executions = %#v, want one", executions)
	}
	execution := executions[0]
	if !execution.StartedAt.Equal(started) {
		t.Fatalf("loaded canonical started_at = %s, want %s", execution.StartedAt, started)
	}
	if !execution.CompletedAt.Equal(completed) {
		t.Fatalf("loaded canonical completed_at = %s, want %s", execution.CompletedAt, completed)
	}
	if execution.ExecutionProfile.TotalMS != totalMS {
		t.Fatalf("loaded canonical total_ms = %d, want %d", execution.ExecutionProfile.TotalMS, totalMS)
	}
}

// TestLoadRemoteCIRunRequiresCurrentWorkloadExecutionProfile 证明 child row 同样不能接受空值或旧零画像。
func TestLoadRemoteCIRunRequiresCurrentWorkloadExecutionProfile(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile string
		wantErr string
	}{
		{name: "missing profile", wantErr: "execution profile is required"},
		{name: "whole null profile", profile: "null", wantErr: "stored remote CI execution profile is invalid"},
		{name: "legacy zero profile", profile: `{}`, wantErr: "stored remote CI execution profile is invalid"},
		{name: "null go flags", profile: `{"go_flags":null,"cache_source":"none","cache_status":"not_applicable","cache_measurement":"measured","startup_ms":1,"test_body_ms":1,"total_ms":2}`, wantErr: "stored remote CI execution profile is invalid"},
		{name: "non-string go flags", profile: `{"go_flags":1,"cache_source":"none","cache_status":"not_applicable","cache_measurement":"measured","startup_ms":1,"test_body_ms":1,"total_ms":2}`, wantErr: "stored remote CI execution profile is invalid"},
		{name: "unknown field", profile: `{"legacy_timing_ms":1}`, wantErr: "execution profile is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, 1)
			jobID := "strict-workload-profile-" + strings.ReplaceAll(test.name, " ", "-")
			record, _, _ := recordWorkloadPassRun(t, store, jobID, 1, string(GateIDWhitespaceCheck))
			updateRemoteCIWorkloadExecutionProfile(t, store, record.JobID, test.profile)

			_, err := store.LoadRemoteCIRun(jobID)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("LoadRemoteCIRun() error = %v", err)
			}
		})
	}
}

func TestStoredRemoteCIExecutionProfilesRejectWholeNullWithoutLegacyMiss(t *testing.T) {
	decoders := []struct {
		name string
		fn   func(string) (ExecutionProfile, error)
	}{
		{name: "workload", fn: decodeStoredRemoteCIExecutionProfile},
		{name: "aggregate", fn: decodeStoredRemoteCIAggregateExecutionProfile},
	}
	for _, decoder := range decoders {
		t.Run(decoder.name, func(t *testing.T) {
			_, err := decoder.fn("null")
			if err == nil || !strings.Contains(err.Error(), "stored remote CI execution profile is invalid") {
				t.Fatalf("decode whole null error = %v", err)
			}
			if errors.Is(err, errLegacyRemoteCIExecutionProfile) {
				t.Fatalf("decode whole null was classified as legacy MISS: %v", err)
			}
		})
	}
}

// insertRemoteCIGateExecutionProfile 写入精确的查询边界 fixture，不经生产 encoder 修复测试输入。
func insertRemoteCIGateExecutionProfile(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, profile string) {
	t.Helper()
	insertRemoteCIGateExecutionProfileAt(t, store, record, record.StartedAt, record.StartedAt.Add(2*time.Millisecond), profile)
}

func insertRemoteCIGateExecutionProfileAt(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, startedAt, completedAt time.Time, profile string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`
		INSERT INTO ci_gate_executions (
			job_id, workload_id, status, exit_code, started_at_unix_ms,
			completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.JobID, string(GateIDWhitespaceCheck), string(ResultStatusPassed), 0,
		startedAt.UnixMilli(), completedAt.UnixMilli(), "", "sha256:"+strings.Repeat("b", 64), "[]", profile); err != nil {
		t.Fatal(err)
	}
}

func updateRemoteCIGateExecutionProfile(t *testing.T, store *DurationLedgerStore, jobID, profile string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	result, err := database.Exec(`UPDATE ci_gate_executions SET execution_profile_json = ? WHERE job_id = ?`, profile, jobID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated gate profiles = %d, want 1", updated)
	}
}

// updateRemoteCIWorkloadExecutionProfile 直接篡改 child row，以验证 readback 不会合成兼容画像。
func updateRemoteCIWorkloadExecutionProfile(t *testing.T, store *DurationLedgerStore, jobID, profile string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	result, err := database.Exec(`UPDATE ci_workload_executions SET execution_profile_json = ? WHERE job_id = ?`, profile, jobID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated workload profiles = %d, want 1", updated)
	}
}
