package gate

import (
	"encoding/json"
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
	tests := []struct {
		name    string
		profile string
		wantErr string
	}{
		{name: "current structured profile", profile: string(encodedProfile)},
		{name: "missing profile", wantErr: "execution profile is required"},
		{name: "legacy zero profile", profile: `{}`, wantErr: "stored remote CI execution profile is invalid"},
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

// TestLoadRemoteCIRunRequiresCurrentWorkloadExecutionProfile 证明 child row 同样不能接受空值或旧零画像。
func TestLoadRemoteCIRunRequiresCurrentWorkloadExecutionProfile(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile string
		wantErr string
	}{
		{name: "missing profile", wantErr: "execution profile is required"},
		{name: "legacy zero profile", profile: `{}`, wantErr: "stored remote CI execution profile is invalid"},
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

// insertRemoteCIGateExecutionProfile 写入精确的查询边界 fixture，不经生产 encoder 修复测试输入。
func insertRemoteCIGateExecutionProfile(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, profile string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`
		INSERT INTO ci_gate_executions (
			job_id, workload_id, status, exit_code, started_at_unix_ms,
			completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.JobID, string(GateIDWhitespaceCheck), string(ResultStatusPassed), 0,
		record.StartedAt.UnixMilli(), record.StartedAt.Add(2*time.Millisecond).UnixMilli(), "", "sha256:"+strings.Repeat("b", 64), "[]", profile); err != nil {
		t.Fatal(err)
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
