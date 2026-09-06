package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

const wakeupV1 = `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"new-agent"}`

func TestWakeupPayloadMigrationPreservesLegacyAndGatesNewRows(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 133)); err != nil {
		t.Fatal(err)
	}
	seedLegacyWakeups(t, db)
	applyWakeup134(t, db)
	assertLegacyWakeup(t, db)
	assertWakeupV1InsertGuards(t, db)
	assertWakeupV1Claim(t, db)
	assertWakeupQuarantine(t, db)
}

func seedLegacyWakeups(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_dag_runs (run_key,dag_key,dag_version_snapshot,trigger_source,status,started_at,events,metadata,created_at,updated_at) VALUES ('legacy-run','legacy-dag',1,'manual','running',1,'[]','{}',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_dag_nodes (dag_key,node_key,title,node_type,assigned_to,status,command_ref,config,result,created_at,updated_at,run_id) VALUES ('legacy-dag','root','root','agent','new-agent','ready','{}','{}','{}',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ key, payload string }{{"legacy-key", `{"legacy":true}`}, {"legacy-v0-key", `{"legacy":false}`}} {
		if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('legacy-dag','root',1,'node_start','legacy-agent',?,?, 'pending',1,1,1)`, row.payload, row.key); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		key, kind, target, payload string
	}{
		{"legacy-retry-v0-key", "turn_complete_retry", "mcp-orch", `{"result":true}`},
		{"legacy-terminal-v0-key", "terminal_failure_compensation", "mcp-orch", `{"reason":"x","store_error":"y","fail_fast":false}`},
	} {
		if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('legacy-dag','root',1,?,?,?,?, 'pending',1,1,1)`, row.kind, row.target, row.payload, row.key); err != nil {
			t.Fatal(err)
		}
	}
}

func applyWakeup134(t *testing.T, db *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "134_orchestration_wakeup_payload_version.sql", readMigrationTestFile(t, "134_orchestration_wakeup_payload_version.sql"))
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatal(err)
	}
}

func assertLegacyWakeup(t *testing.T, db *sql.DB) {
	t.Helper()
	var payload string
	var version int64
	if err := db.QueryRow(`SELECT prompt_payload,payload_schema_version FROM task_dag_wakeups WHERE idempotency_key='legacy-v0-key'`).Scan(&payload, &version); err != nil {
		t.Fatal(err)
	}
	if payload != `{"legacy":false}` || version != 0 {
		t.Fatalf("legacy payload=%q version=%d", payload, version)
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET prompt_payload='{"rewritten":true}' WHERE idempotency_key='legacy-v0-key'`); err == nil {
		t.Fatal("v0 payload rewrite succeeded")
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET target_agent_id=' legacy-agent ', prompt_payload=?, payload_schema_version=1 WHERE idempotency_key='legacy-v0-key'`, `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"legacy-agent"}`); err == nil {
		t.Fatal("v0 to v1 target with surrounding whitespace accepted")
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET target_agent_id='legacy-agent', prompt_payload=?, payload_schema_version=1 WHERE idempotency_key='legacy-v0-key'`, `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":" legacy-agent"}`); err == nil {
		t.Fatal("v0 to v1 payload agent with surrounding whitespace accepted")
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET prompt_payload=?,payload_schema_version=1 WHERE idempotency_key='legacy-key'`, `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"legacy-agent"}`); err != nil {
		t.Fatal(err)
	}
}

func assertWakeupV1InsertGuards(t *testing.T, db *sql.DB) {
	t.Helper()
	insert := `INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('legacy-dag','root',1,?,?,?,?,?,'pending',1,1,1)`
	if _, err := db.Exec(insert, "node_start", "new-agent", wakeupV1, 1, "new-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET idempotency_key='tampered-key' WHERE idempotency_key='new-key'`); err == nil {
		t.Fatal("v1 idempotency key rewrite succeeded")
	}
	if _, err := db.Exec(insert, "node_start", "new-agent", `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"new-agent"}`, 1, "bad-kind"); err == nil {
		t.Fatal("mismatched v1 payload accepted")
	}
	if _, err := db.Exec(insert, "node_start", "new-agent", `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":" new-agent"}`, 1, "noncanonical-payload-agent"); err == nil {
		t.Fatal("v1 payload agent with surrounding whitespace accepted")
	}
	assertMissingV1Fields(t, db, insert)
	assertInternalV1Guards(t, db, insert)
	assertV1RowIdentityGuards(t, db)
	assertLaunchV1Guards(t, db)
	assertV1Immutable(t, db)
}

func assertV1Immutable(t *testing.T, db *sql.DB) {
	t.Helper()
	updates := []string{
		`UPDATE task_dag_wakeups SET prompt_payload=? WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET target_agent_id='other-agent' WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET wakeup_kind='manual_dispatch' WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET dag_key='other-dag' WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET node_key='other-node' WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET run_id=2 WHERE idempotency_key='new-key'`,
		`UPDATE task_dag_wakeups SET payload_schema_version=0 WHERE idempotency_key='new-key'`,
	}
	for i, statement := range updates {
		var args []any
		if i == 0 {
			args = []any{`{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"new-agent","upstream_outputs":[]}`}
		}
		if _, err := db.Exec(statement, args...); err == nil {
			t.Fatalf("v1 canonical rewrite %d accepted", i)
		}
	}
}

func assertMissingV1Fields(t *testing.T, db *sql.DB, insert string) {
	t.Helper()
	for _, payload := range []string{
		`{"wakeup_payload_kind":"dag_downstream","schema_version":1}`,
		`{"wakeup_payload_kind":"dag_downstream","agent_id":"new-agent"}`,
		`{"schema_version":1,"agent_id":"new-agent"}`,
	} {
		if _, err := db.Exec(insert, "node_start", "new-agent", payload, 1, "missing-fields-"+strings.ReplaceAll(payload, "\"", "")); err == nil {
			t.Fatalf("v1 payload with missing fields accepted: %s", payload)
		}
	}
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('legacy-dag','root',1,'node_start','new-agent',?,'implicit-v0','pending',1,1,1)`, wakeupV1); err == nil {
		t.Fatal("implicit v0 insert accepted")
	}
}

func assertInternalV1Guards(t *testing.T, db *sql.DB, insert string) {
	t.Helper()
	for _, tc := range []struct {
		kind, target, key string
	}{
		{"turn_complete_retry", " mcp-orch ", "noncanonical-retry-target"},
		{"terminal_failure_compensation", " mcp-orch ", "noncanonical-terminal-target"},
	} {
		payload := `{"result":true}`
		if tc.kind == "terminal_failure_compensation" {
			payload = `{"reason":"x","store_error":"y","fail_fast":false}`
		}
		if _, err := db.Exec(insert, tc.kind, tc.target, payload, 1, tc.key); err == nil {
			t.Fatalf("internal target with surrounding whitespace accepted: kind=%s", tc.kind)
		}
	}
	if _, err := db.Exec(insert, "turn_complete_retry", "mcp-orch", `{"result":true}`, 1, "retry-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "turn_complete_retry", "wrong", `{"result":true}`, 1, "bad-target"); err == nil {
		t.Fatal("internal wrong target accepted")
	}
	if _, err := db.Exec(insert, "terminal_failure_compensation", "mcp-orch", `{"reason":"x"}`, 1, "bad-terminal-fields"); err == nil {
		t.Fatal("internal terminal payload with missing fields accepted")
	}
	if _, err := db.Exec(insert, "turn_complete_retry", "mcp-orch", `{}`, 1, "internal-good"); err != nil {
		t.Fatalf("valid internal object rejected: %v", err)
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET target_agent_id=' mcp-orch ', payload_schema_version=1 WHERE idempotency_key='legacy-retry-v0-key'`); err == nil {
		t.Fatal("v0 to v1 internal retry target with surrounding whitespace accepted")
	}
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET target_agent_id=' mcp-orch ', payload_schema_version=1 WHERE idempotency_key='legacy-terminal-v0-key'`); err == nil {
		t.Fatal("v0 to v1 internal terminal target with surrounding whitespace accepted")
	}
}

func assertV1RowIdentityGuards(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, args := range [][5]string{
		{"wrong-dag", "root", "1", "node_start", "row-id-dag"},
		{"legacy-dag", "", "1", "node_start", "row-id-node"},
		{"legacy-dag", "root", "999", "node_start", "row-id-run"},
	} {
		rowInsert := `INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES (?,?,?,?,?,?,1,?,'pending',1,1,1)`
		if _, err := db.Exec(rowInsert, args[0], args[1], args[2], args[3], "new-agent", wakeupV1, args[4]); err == nil {
			t.Fatalf("v1 row identity mismatch accepted: %+v", args)
		}
	}
}

func assertLaunchV1Guards(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('','',NULL,'start','launch-agent',? ,1,'launch-good','pending',1,1,1)`, `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"launch-agent"}`); err != nil {
		t.Fatalf("valid launch row rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('','',NULL,'start',' launch-agent ',? ,1,'launch-noncanonical-target','pending',1,1,1)`, `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"launch-agent"}`); err == nil {
		t.Fatal("launch target with surrounding whitespace accepted")
	}
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('','',NULL,'start','launch-agent',? ,1,'launch-noncanonical-payload-agent','pending',1,1,1)`, `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":" launch-agent"}`); err == nil {
		t.Fatal("launch payload agent with surrounding whitespace accepted")
	}
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('legacy-dag','root',1,'start','launch-agent',? ,1,'launch-bad-identity','pending',1,1,1)`, `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"launch-agent"}`); err == nil {
		t.Fatal("launch row with DAG identity accepted")
	}
	if _, err := db.Exec(`INSERT INTO task_dag_wakeups (dag_key,node_key,run_id,wakeup_kind,target_agent_id,prompt_payload,payload_schema_version,idempotency_key,status,next_retry_at,created_at,updated_at) VALUES ('','',NULL,'start','launch-agent',? ,1,'launch-null-optional','pending',1,1,1)`, `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"launch-agent","cwd":null}`); err == nil {
		t.Fatal("launch row with optional null accepted")
	}
}

func assertWakeupV1Claim(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE task_dag_wakeups SET status='dispatching' WHERE status='pending' AND payload_schema_version=1`); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, key := range []string{"legacy-v0-key", "legacy-key", "new-key", "retry-key", "internal-good", "launch-good"} {
		var status string
		if err := db.QueryRow(`SELECT status FROM task_dag_wakeups WHERE idempotency_key=?`, key).Scan(&status); err != nil {
			t.Fatal(err)
		}
		statuses[key] = status
	}
	if statuses["legacy-v0-key"] != "pending" || statuses["legacy-key"] != "dispatching" || statuses["new-key"] != "dispatching" || statuses["retry-key"] != "dispatching" || statuses["internal-good"] != "dispatching" || statuses["launch-good"] != "dispatching" {
		t.Fatalf("claim statuses=%v", statuses)
	}
}

func assertWakeupQuarantine(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO orchestration_legacy_wakeup_quarantine (quarantine_id,source_wakeup_id,source_payload_digest,wakeup_kind,target_agent_id,prompt_payload,reason,source_schema_version,schema_version,created_at) VALUES ('legacy-wakeup-quarantine:v1:1:bad',999999,?,'node_start','legacy-agent',?,'malformed_json',0,1,1)`, strings.Repeat("a", 64), `{`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE orchestration_legacy_wakeup_quarantine SET reason='changed' WHERE quarantine_id='legacy-wakeup-quarantine:v1:1:bad'`); err == nil {
		t.Fatal("quarantine update succeeded")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_legacy_wakeup_quarantine (quarantine_id,source_wakeup_id,source_payload_digest,wakeup_kind,target_agent_id,prompt_payload,reason,source_schema_version,schema_version,created_at) VALUES ('legacy-wakeup-quarantine:v1:1:empty',1000,?,'start','legacy-agent','', 'empty',0,1,1)`, strings.Repeat("c", 64)); err == nil {
		t.Fatal("empty quarantine payload accepted")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_legacy_wakeup_quarantine (quarantine_id,source_wakeup_id,source_payload_digest,wakeup_kind,target_agent_id,prompt_payload,reason,source_schema_version,schema_version,created_at) VALUES ('legacy-wakeup-quarantine:v1:1:oversized',1001,?,'start','legacy-agent',?, 'oversized',0,1,1)`, strings.Repeat("d", 64), strings.Repeat("x", 262145)); err == nil {
		t.Fatal("oversized quarantine payload accepted")
	}
}

func TestWakeupPayloadMigrationRequiresQuarantineKeyPrefix(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 134)); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO orchestration_legacy_wakeup_quarantine (quarantine_id,source_wakeup_id,source_payload_digest,wakeup_kind,target_agent_id,prompt_payload,reason,source_schema_version,schema_version,created_at) VALUES ('not-stable',1,?,'start','agent','{}','bad',0,1,1)`, strings.Repeat("b", 64))
	if err == nil {
		t.Fatal("non-derived quarantine key accepted")
	}
}
