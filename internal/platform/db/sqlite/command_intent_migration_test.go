package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandIntentMigrationReaches133AndConstrainsImmutableSchema(t *testing.T) {
	db := openCommandIntentMigrationDB(t)
	assertCommandIntentSchema(t, db)
	valid := validCommandIntentFixture()
	insertCommandIntent(t, db, valid)
	assertCommandIntentRecordedAt(t, db)
	assertCommandIntentUniqueKey(t, db, valid)
	assertCommandIntentConstraints(t, db, valid)
	assertCommandIntentImmutable(t, db)
}

func openCommandIntentMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, "126_orchestration_command_intents.sql", 1)
	assertMigrationMarkerCount(t, db, "127_orchestration_command_journal.sql", 1)
	return db
}

func assertCommandIntentSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var tableSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'orchestration_command_intents'`).Scan(&tableSQL); err != nil {
		t.Fatalf("read command intent table SQL: %v", err)
	}
	if !strings.Contains(strings.ToUpper(tableSQL), "STRICT") {
		t.Fatalf("command intent table SQL = %q, want STRICT table", tableSQL)
	}
	want := []string{
		"command_id", "command_kind", "caller_principal_ref", "scope_ref",
		"idempotency_namespace", "idempotency_key_digest",
		"request_digest", "digest_key_id", "request_content_ref", "source_fence_ref",
		"target_fence_ref", "schema_version", "command_version", "recorded_at",
	}
	if columns := tableColumns(t, db, "orchestration_command_intents"); !slicesEqual(columns, want) {
		t.Fatalf("command intent columns = %v, want %v", columns, want)
	}
}

func validCommandIntentFixture() commandIntentFixture {
	return commandIntentFixture{
		CommandID: "command-1", CommandKind: "launch_agent", IdempotencyNamespace: "workspace/agent",
		CallerPrincipalRef: "principal/default", ScopeRef: "scope/default",
		IdempotencyKeyDigest: strings.Repeat("a", 64), RequestDigest: strings.Repeat("b", 64),
		DigestKeyID: "key-1", RequestContentRef: "content-ref-1", SourceFenceRef: "source-fence-1",
		TargetFenceRef: "target-fence-1", SchemaVersion: 1, CommandVersion: 1,
	}
}

func assertCommandIntentRecordedAt(t *testing.T, db *sql.DB) {
	t.Helper()
	var recordedAt string
	if err := db.QueryRow(`SELECT recorded_at FROM orchestration_command_intents WHERE command_id = 'command-1'`).Scan(&recordedAt); err != nil {
		t.Fatalf("read generated recorded_at: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, recordedAt)
	if err != nil || parsed.IsZero() || parsed.Location() != time.UTC {
		t.Fatalf("generated recorded_at = %q, want UTC RFC3339 timestamp", recordedAt)
	}
}

func assertCommandIntentUniqueKey(t *testing.T, db *sql.DB, valid commandIntentFixture) {
	t.Helper()
	duplicate := valid
	duplicate.CommandID = "command-2"
	if _, err := insertCommandIntentResult(db, duplicate); err == nil {
		t.Fatal("duplicate idempotency namespace/key digest was accepted")
	}
}

func assertCommandIntentConstraints(t *testing.T, db *sql.DB, valid commandIntentFixture) {
	t.Helper()
	for _, tc := range commandIntentInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			candidate.CommandID = "invalid-" + strings.ReplaceAll(tc.name, " ", "-")
			candidate.IdempotencyKeyDigest = strings.Repeat("c", 64)
			candidate.IdempotencyNamespace = "invalid/" + strings.ReplaceAll(tc.name, " ", "-")
			tc.mutate(&candidate)
			if _, err := insertCommandIntentResult(db, candidate); err == nil {
				t.Fatalf("invalid command intent %q was accepted", tc.name)
			}
		})
	}
}

func commandIntentInvalidCases() []struct {
	name   string
	mutate func(*commandIntentFixture)
} {
	return []struct {
		name   string
		mutate func(*commandIntentFixture)
	}{
		{name: "empty command id", mutate: func(v *commandIntentFixture) { v.CommandID = "" }},
		{name: "long command id", mutate: func(v *commandIntentFixture) { v.CommandID = strings.Repeat("x", 129) }},
		{name: "unknown command kind", mutate: func(v *commandIntentFixture) { v.CommandKind = "dispatch" }},
		{name: "empty caller principal", mutate: func(v *commandIntentFixture) { v.CallerPrincipalRef = "" }},
		{name: "whitespace caller principal", mutate: func(v *commandIntentFixture) { v.CallerPrincipalRef = " principal/default" }},
		{name: "nul caller principal", mutate: func(v *commandIntentFixture) { v.CallerPrincipalRef = "principal\x00default" }},
		{name: "long caller principal", mutate: func(v *commandIntentFixture) { v.CallerPrincipalRef = strings.Repeat("x", 513) }},
		{name: "empty scope", mutate: func(v *commandIntentFixture) { v.ScopeRef = "" }},
		{name: "whitespace scope", mutate: func(v *commandIntentFixture) { v.ScopeRef = "scope/default " }},
		{name: "nul scope", mutate: func(v *commandIntentFixture) { v.ScopeRef = "scope\x00default" }},
		{name: "long scope", mutate: func(v *commandIntentFixture) { v.ScopeRef = strings.Repeat("x", 513) }},
		{name: "empty namespace", mutate: func(v *commandIntentFixture) { v.IdempotencyNamespace = "" }},
		{name: "long namespace", mutate: func(v *commandIntentFixture) { v.IdempotencyNamespace = strings.Repeat("x", 257) }},
		{name: "uppercase key digest", mutate: func(v *commandIntentFixture) { v.IdempotencyKeyDigest = strings.Repeat("A", 64) }},
		{name: "non-hex key digest", mutate: func(v *commandIntentFixture) { v.IdempotencyKeyDigest = strings.Repeat("g", 64) }},
		{name: "short request digest", mutate: func(v *commandIntentFixture) { v.RequestDigest = "not-a-digest" }},
		{name: "empty digest key id", mutate: func(v *commandIntentFixture) { v.DigestKeyID = "" }},
		{name: "long content ref", mutate: func(v *commandIntentFixture) { v.RequestContentRef = strings.Repeat("x", 513) }},
		{name: "empty source fence ref", mutate: func(v *commandIntentFixture) { v.SourceFenceRef = "" }},
		{name: "empty target fence ref", mutate: func(v *commandIntentFixture) { v.TargetFenceRef = "" }},
		{name: "schema version", mutate: func(v *commandIntentFixture) { v.SchemaVersion = 2 }},
		{name: "command version", mutate: func(v *commandIntentFixture) { v.CommandVersion = 2 }},
	}
}

func assertCommandIntentImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE orchestration_command_intents SET request_digest = ? WHERE command_id = 'command-1'`, strings.Repeat("c", 64)); err == nil {
		t.Fatal("immutable command intent accepted UPDATE")
	}
	if _, err := db.Exec(`DELETE FROM orchestration_command_intents WHERE command_id = 'command-1'`); err == nil {
		t.Fatal("immutable command intent accepted DELETE")
	}
}

func TestCommandIntentIdempotencyUniqueKeyUnderConcurrentInsert(t *testing.T) {
	db := openCommandIntentMigrationDB(t)
	db.SetMaxOpenConns(8)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set SQLite busy timeout: %v", err)
	}
	const workers = 8
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		index := i
		group.Go(func() {
			<-start
			candidate := validCommandIntentFixture()
			candidate.CommandID = "concurrent-" + string(rune('a'+index))
			_, err := insertCommandIntentResult(db, candidate)
			results <- err
		})
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent inserts succeeded %d times, want exactly once", successes)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_command_intents WHERE idempotency_namespace = 'workspace/agent' AND idempotency_key_digest = ?`, strings.Repeat("a", 64)).Scan(&count); err != nil {
		t.Fatalf("count concurrent command intents: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent idempotency rows = %d, want 1", count)
	}
}

type commandIntentFixture struct {
	CommandID            string
	CommandKind          string
	CallerPrincipalRef   string
	ScopeRef             string
	IdempotencyNamespace string
	IdempotencyKeyDigest string
	RequestDigest        string
	DigestKeyID          string
	RequestContentRef    string
	SourceFenceRef       string
	TargetFenceRef       string
	SchemaVersion        int64
	CommandVersion       int64
}

func insertCommandIntent(t *testing.T, db *sql.DB, fixture commandIntentFixture) {
	t.Helper()
	if _, err := insertCommandIntentResult(db, fixture); err != nil {
		t.Fatalf("insert command intent: %v", err)
	}
}

func insertCommandIntentResult(db *sql.DB, fixture commandIntentFixture) (sql.Result, error) {
	return db.Exec(`
		INSERT INTO orchestration_command_intents (
			command_id, command_kind, caller_principal_ref, scope_ref,
			idempotency_namespace, idempotency_key_digest,
			request_digest, digest_key_id, request_content_ref, source_fence_ref,
			target_fence_ref, schema_version, command_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fixture.CommandID, fixture.CommandKind, fixture.CallerPrincipalRef, fixture.ScopeRef,
		fixture.IdempotencyNamespace, fixture.IdempotencyKeyDigest,
		fixture.RequestDigest, fixture.DigestKeyID, fixture.RequestContentRef, fixture.SourceFenceRef,
		fixture.TargetFenceRef, fixture.SchemaVersion, fixture.CommandVersion)
}

func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan %s columns: %v", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return columns
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
