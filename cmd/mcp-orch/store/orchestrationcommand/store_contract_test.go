package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const commandIntentTableName = "orchestration_command_intents"

func TestCommandIntentStoreContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"same_key_replay", TestCommandIntentStoreSameKeyReplayPreservesWinningIdentityAndTime},
		{"all_command_kinds", TestCommandIntentStoreAcceptsEveryCommandKind},
		{"field_mutations", TestCommandIntentStoreEveryIntentFieldMutationIsCovered},
		{"same_command_id_different_key", TestCommandIntentStoreSameCommandIDWithDifferentKeyConflicts},
		{"independent_namespaces", TestCommandIntentStoreDifferentNamespacesAreIndependent},
		{"invalid_and_cancelled", TestCommandIntentStoreRejectsInvalidInputsAndCancellationWithoutRows},
		{"nil_context_fail_closed", TestCommandIntentStoreNilContextFailsClosed},
		{"immutable_rows", TestCommandIntentStoreRowsAreImmutable},
		{"concurrent_reserve", TestCommandIntentStoreConcurrentReserveAcrossConnectionsHasOneDurableWinner},
		{"close_and_reopen", TestCommandIntentStoreCloseReopenReturnsSameKeyRecord},
		{"wire_fields", TestCommandIntentWireFieldsUseDynamicSnakeCaseAndRoundTrip},
		{"mapper_fields", TestCommandIntentMappersCoverEveryReflectedField},
		{"mapper_omission_red_green", TestCommandIntentFieldGuardRejectsRealMapperOmission},
		{"fresh_write_one_hot", TestCommandIntentFreshWriteOneHotPreservesEveryIntentField},
		{"record_from_row", TestCommandIntentRecordFromRowUsesFixedValuesForEveryField},
		{"schema_and_sqlc", TestCommandIntentSchemaAndSQLCFieldsStayComplete},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

// TestCommandIntentStoreNilContextFailsClosed ensures a programmer error is
// reported before database/sql is called, rather than dereferencing ctx.Err.
func TestCommandIntentStoreNilContextFailsClosed(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	intent := commandIntentFixture("command-nil-context")
	key := Key{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest}
	var nilCtx context.Context
	if _, err := store.Reserve(nilCtx, intent); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("Reserve(nil context) error = %v, want ErrContextRequired", err)
	}
	if _, err := store.Get(nilCtx, intent.CommandID); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("Get(nil context) error = %v, want ErrContextRequired", err)
	}
	if _, err := store.GetByKey(nilCtx, key); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("GetByKey(nil context) error = %v, want ErrContextRequired", err)
	}
	assertCommandIntentRowCount(t, db, 0)
}

func TestCommandIntentStoreSameKeyReplayPreservesWinningIdentityAndTime(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	ctx := context.Background()
	first, err := store.Reserve(ctx, commandIntentFixture("command-winner"))
	if err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	assertCommandIntentRecord(t, first, "command-winner")

	retryIntent := commandIntentFixture("command-retry")
	replay, err := store.Reserve(ctx, retryIntent)
	if err != nil {
		t.Fatalf("Reserve(same key, new candidate ID) error = %v", err)
	}
	assertCommandIntentRecordEqual(t, replay, first)
	if replay.Intent.CommandID != first.Intent.CommandID {
		t.Fatalf("replay command_id = %q, want original %q", replay.Intent.CommandID, first.Intent.CommandID)
	}
	if !replay.RecordedAt.Equal(first.RecordedAt) {
		t.Fatalf("replay recorded_at = %s, want original %s", replay.RecordedAt, first.RecordedAt)
	}

	byID, err := store.Get(ctx, first.Intent.CommandID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertCommandIntentRecordEqual(t, byID, first)
	byKey, err := store.GetByKey(ctx, Key{Namespace: first.Intent.IdempotencyNamespace, Digest: first.Intent.IdempotencyKeyDigest})
	if err != nil {
		t.Fatalf("GetByKey() error = %v", err)
	}
	assertCommandIntentRecordEqual(t, byKey, first)
	assertCommandIntentRowCount(t, db, 1)
}

func TestCommandIntentStoreAcceptsEveryCommandKind(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	kinds := []string{"launch_agent", "fork_agent", "send_message", "interrupt_agent", "stop_agent", "recover_agent", "room_broadcast", "room_round"}
	for index, kind := range kinds {
		intent := commandIntentFixture(fmt.Sprintf("command-kind-%d", index))
		intent.CommandKind = kind
		intent.IdempotencyNamespace = fmt.Sprintf("namespace-kind-%d", index)
		intent.IdempotencyKeyDigest = fmt.Sprintf("%064x", index+1)
		record, err := store.Reserve(context.Background(), intent)
		if err != nil {
			t.Fatalf("Reserve(%s) error = %v", kind, err)
		}
		assertCommandIntentRecord(t, record, intent.CommandID)
		if record.Intent.CommandKind != kind {
			t.Fatalf("Reserve(%s) kind = %q", kind, record.Intent.CommandKind)
		}
	}
	assertCommandIntentRowCount(t, db, len(kinds))
}

func TestCommandIntentStoreEveryIntentFieldMutationIsCovered(t *testing.T) {
	intentType := reflect.TypeFor[Intent]()
	for fieldIndex := 0; fieldIndex < intentType.NumField(); fieldIndex++ {
		field := intentType.Field(fieldIndex)
		t.Run(field.Name, func(t *testing.T) { assertCommandIntentFieldMutation(t, field) })
	}
}

func assertCommandIntentFieldMutation(t *testing.T, field reflect.StructField) {
	t.Helper()
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	ctx := context.Background()
	first, err := store.Reserve(ctx, commandIntentFixture("command-original"))
	if err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	candidate, role := mutateCommandIntentField(t, first.Intent, field)
	got, err := store.Reserve(ctx, candidate)
	assertCommandIntentMutationResult(t, db, field, role, first, got, err)
}

func assertCommandIntentMutationResult(t *testing.T, db *sql.DB, field reflect.StructField, role commandIntentMutationRole, first, got Record, err error) {
	t.Helper()
	switch role {
	case commandIntentMutationReplay:
		if err != nil {
			t.Fatalf("Reserve(%s mutation) error = %v, want replay", field.Name, err)
		}
		assertCommandIntentRecordEqual(t, got, first)
		assertCommandIntentRowCount(t, db, 1)
	case commandIntentMutationIndependent:
		assertCommandIntentIndependentMutation(t, db, field, first, got, err)
	case commandIntentMutationConflict:
		if !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("Reserve(%s mutation) error = %v, want ErrIdempotencyConflict", field.Name, err)
		}
		assertCommandIntentRowCount(t, db, 1)
	default:
		t.Fatalf("unknown mutation role %d for %s", role, field.Name)
	}
}

func assertCommandIntentIndependentMutation(t *testing.T, db *sql.DB, field reflect.StructField, first, got Record, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Reserve(%s mutation) error = %v, want independent row", field.Name, err)
	}
	if got.Intent.CommandID == first.Intent.CommandID {
		t.Fatalf("Reserve(%s mutation) reused command_id %q", field.Name, got.Intent.CommandID)
	}
	if got.Intent.IdempotencyNamespace == first.Intent.IdempotencyNamespace && got.Intent.IdempotencyKeyDigest == first.Intent.IdempotencyKeyDigest {
		t.Fatalf("Reserve(%s mutation) did not change idempotency key", field.Name)
	}
	assertCommandIntentRowCount(t, db, 2)
}

func TestCommandIntentStoreSameCommandIDWithDifferentKeyConflicts(t *testing.T) {
	for _, name := range []string{"namespace", "digest"} {
		t.Run(name, func(t *testing.T) {
			db := openCommandIntentTestDB(t)
			store, err := NewStore(db)
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			ctx := context.Background()
			first, err := store.Reserve(ctx, commandIntentFixture("command-fixed"))
			if err != nil {
				t.Fatalf("Reserve(first) error = %v", err)
			}
			candidate := first.Intent
			switch name {
			case "namespace":
				candidate.IdempotencyNamespace = "namespace-other"
			case "digest":
				candidate.IdempotencyKeyDigest = strings.Repeat("b", 64)
			}
			if _, err := store.Reserve(ctx, candidate); !errors.Is(err, ErrCommandIDConflict) {
				t.Fatalf("Reserve(same command ID, different %s) error = %v, want ErrCommandIDConflict", name, err)
			}
			assertCommandIntentRowCount(t, db, 1)
		})
	}
}

func TestCommandIntentStoreDifferentNamespacesAreIndependent(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	ctx := context.Background()
	first, err := store.Reserve(ctx, commandIntentFixture("command-namespace-one"))
	if err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	secondIntent := commandIntentFixture("command-namespace-two")
	secondIntent.IdempotencyNamespace = "namespace-independent"
	second, err := store.Reserve(ctx, secondIntent)
	if err != nil {
		t.Fatalf("Reserve(different namespace) error = %v", err)
	}
	if second.Intent.CommandID == first.Intent.CommandID || second.Intent.IdempotencyNamespace == first.Intent.IdempotencyNamespace {
		t.Fatalf("independent namespace record = %#v, first = %#v", second.Intent, first.Intent)
	}
	assertCommandIntentRowCount(t, db, 2)
	if _, err := store.GetByKey(ctx, Key{Namespace: first.Intent.IdempotencyNamespace, Digest: first.Intent.IdempotencyKeyDigest}); err != nil {
		t.Fatalf("GetByKey(first namespace) error = %v", err)
	}
	if _, err := store.GetByKey(ctx, Key{Namespace: second.Intent.IdempotencyNamespace, Digest: second.Intent.IdempotencyKeyDigest}); err != nil {
		t.Fatalf("GetByKey(second namespace) error = %v", err)
	}
}

func TestCommandIntentStoreRowsAreImmutable(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	first, err := store.Reserve(context.Background(), commandIntentFixture("immutable-command"))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	statements := []string{
		"UPDATE orchestration_command_intents SET request_digest = '" + strings.Repeat("c", 64) + "' WHERE command_id = ?",
		"DELETE FROM orchestration_command_intents WHERE command_id = ?",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(context.Background(), statement, first.Intent.CommandID); err == nil {
			t.Fatalf("mutable command intent statement succeeded: %s", statement)
		}
	}
	assertCommandIntentRowCount(t, db, 1)
	got, err := store.Get(context.Background(), first.Intent.CommandID)
	if err != nil {
		t.Fatalf("Get() after immutable statements error = %v", err)
	}
	assertCommandIntentRecordEqual(t, got, first)
}

func TestCommandIntentStoreRejectsInvalidInputsAndCancellationWithoutRows(t *testing.T) {
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got, err := NewStore(nil); err == nil || got != nil {
		t.Fatalf("NewStore(nil) = store=%#v err=%v, want nil store and error", got, err)
	}

	valid := commandIntentFixture("command-invalid-fixture")
	for _, tc := range commandIntentInvalidCases() {
		t.Run(tc.name, func(t *testing.T) { assertCommandIntentInvalidCase(t, db, store, valid, tc.mutate) })
	}

	assertCommandIntentCancellation(t, db, store, valid)
	assertCommandIntentMissingReads(t, db, store, valid)
	assertCommandIntentInvalidReads(t, db, store, valid)
}

type commandIntentInvalidCase struct {
	name   string
	mutate func(*Intent)
}

func commandIntentInvalidCases() []commandIntentInvalidCase {
	return []commandIntentInvalidCase{
		{"empty_command_id", func(i *Intent) { i.CommandID = "" }},
		{"empty_caller_principal", func(i *Intent) { i.CallerPrincipalRef = "" }},
		{"whitespace_caller_principal", func(i *Intent) { i.CallerPrincipalRef = " principal/default" }},
		{"nul_caller_principal", func(i *Intent) { i.CallerPrincipalRef = "principal\x00default" }},
		{"oversized_caller_principal", func(i *Intent) { i.CallerPrincipalRef = strings.Repeat("x", 513) }},
		{"empty_scope", func(i *Intent) { i.ScopeRef = "" }},
		{"whitespace_scope", func(i *Intent) { i.ScopeRef = "scope/default " }},
		{"nul_scope", func(i *Intent) { i.ScopeRef = "scope\x00default" }},
		{"oversized_scope", func(i *Intent) { i.ScopeRef = strings.Repeat("x", 513) }},
		{"whitespace_command_id", func(i *Intent) { i.CommandID = " command" }},
		{"nul_command_id", func(i *Intent) { i.CommandID = "command\x00id" }},
		{"oversized_command_id", func(i *Intent) { i.CommandID = strings.Repeat("x", 129) }},
		{"oversized_digest_key_id", func(i *Intent) { i.DigestKeyID = strings.Repeat("x", 129) }},
		{"empty_namespace", func(i *Intent) { i.IdempotencyNamespace = "" }},
		{"oversized_namespace", func(i *Intent) { i.IdempotencyNamespace = strings.Repeat("x", 257) }},
		{"oversized_content_ref", func(i *Intent) { i.RequestContentRef = strings.Repeat("x", 513) }},
		{"oversized_source_ref", func(i *Intent) { i.SourceFenceRef = strings.Repeat("x", 513) }},
		{"oversized_target_ref", func(i *Intent) { i.TargetFenceRef = strings.Repeat("x", 513) }},
		{"unsupported_kind", func(i *Intent) { i.CommandKind = "unsupported" }},
		{"uppercase_key_digest", func(i *Intent) { i.IdempotencyKeyDigest = strings.Repeat("A", 64) }},
		{"short_key_digest", func(i *Intent) { i.IdempotencyKeyDigest = strings.Repeat("a", 63) }},
		{"nonhex_key_digest", func(i *Intent) { i.IdempotencyKeyDigest = strings.Repeat("g", 64) }},
		{"uppercase_request_digest", func(i *Intent) { i.RequestDigest = strings.Repeat("A", 64) }},
		{"short_request_digest", func(i *Intent) { i.RequestDigest = strings.Repeat("a", 63) }},
		{"nonhex_request_digest", func(i *Intent) { i.RequestDigest = strings.Repeat("g", 64) }},
	}
}

func assertCommandIntentInvalidCase(t *testing.T, db *sql.DB, store *Store, valid Intent, mutate func(*Intent)) {
	t.Helper()
	candidate := valid
	mutate(&candidate)
	if _, err := store.Reserve(context.Background(), candidate); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("Reserve() error = %v, want ErrInvalidIntent", err)
	}
	assertCommandIntentRowCount(t, db, 0)
}

func assertCommandIntentCancellation(t *testing.T, db *sql.DB, store *Store, valid Intent) {
	t.Helper()
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Reserve(canceledCtx, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reserve(canceled context) error = %v, want context.Canceled", err)
	}
	assertCommandIntentRowCount(t, db, 0)
}

func assertCommandIntentMissingReads(t *testing.T, db *sql.DB, store *Store, valid Intent) {
	t.Helper()
	if _, err := store.Get(context.Background(), valid.CommandID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetByKey(context.Background(), Key{Namespace: valid.IdempotencyNamespace, Digest: valid.IdempotencyKeyDigest}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByKey(missing) error = %v, want ErrNotFound", err)
	}
	assertCommandIntentRowCount(t, db, 0)
}

func assertCommandIntentInvalidReads(t *testing.T, db *sql.DB, store *Store, valid Intent) {
	t.Helper()
	if _, err := store.Get(context.Background(), " invalid"); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("Get(invalid ID) error = %v, want ErrInvalidIntent", err)
	}
	if _, err := store.GetByKey(context.Background(), Key{Namespace: valid.IdempotencyNamespace, Digest: "bad"}); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("GetByKey(invalid digest) error = %v, want ErrInvalidIntent", err)
	}
	assertCommandIntentRowCount(t, db, 0)
}

func commandIntentFixture(commandID string) Intent {
	return Intent{
		CommandID:            commandID,
		CommandKind:          "send_message",
		CallerPrincipalRef:   "principal/default",
		ScopeRef:             "scope/default",
		IdempotencyNamespace: "namespace-default",
		IdempotencyKeyDigest: strings.Repeat("a", 64),
		RequestDigest:        strings.Repeat("b", 64),
		DigestKeyID:          "digest-key-v1",
		RequestContentRef:    "content-ref-encrypted-v1",
		SourceFenceRef:       "source-fence-v1",
		TargetFenceRef:       "target-fence-v1",
	}
}

type commandIntentMutationRole uint8

const (
	commandIntentMutationConflict commandIntentMutationRole = iota
	commandIntentMutationReplay
	commandIntentMutationIndependent
)

func mutateCommandIntentField(t *testing.T, base Intent, field reflect.StructField) (Intent, commandIntentMutationRole) {
	t.Helper()
	mutated := base
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	if !value.IsValid() || value.Kind() != reflect.String {
		t.Fatalf("Intent.%s type = %v, want string so mutation coverage remains fail-closed", field.Name, value.Type())
	}
	jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
	switch jsonName {
	case "command_id":
		value.SetString("command-candidate")
		return mutated, commandIntentMutationReplay
	case "idempotency_namespace":
		value.SetString("namespace-mutated")
		mutated.CommandID = "command-independent-namespace"
		return mutated, commandIntentMutationIndependent
	case "idempotency_key_digest":
		value.SetString(strings.Repeat("c", 64))
		mutated.CommandID = "command-independent-digest"
		return mutated, commandIntentMutationIndependent
	case "command_kind":
		value.SetString("fork_agent")
	case "request_digest":
		value.SetString(strings.Repeat("d", 64))
	case "digest_key_id":
		value.SetString("digest-key-mutated")
	default:
		value.SetString("mutated-" + jsonName)
	}
	mutated.CommandID = "command-conflict-" + jsonName
	return mutated, commandIntentMutationConflict
}

func assertCommandIntentRecord(t *testing.T, record Record, wantCommandID string) {
	t.Helper()
	if record.Intent.CommandID != wantCommandID {
		t.Fatalf("record command_id = %q, want %q", record.Intent.CommandID, wantCommandID)
	}
	if record.SchemaVersion != 1 || record.CommandVersion != 1 {
		t.Fatalf("record versions = schema %d command %d, want 1/1", record.SchemaVersion, record.CommandVersion)
	}
	if record.RecordedAt.IsZero() {
		t.Fatal("recorded_at is zero")
	}
	if err := record.Intent.Validate(); err != nil {
		t.Fatalf("persisted intent validation error = %v", err)
	}
}

func assertCommandIntentRecordEqual(t *testing.T, got, want Record) {
	t.Helper()
	if !reflect.DeepEqual(got.Intent, want.Intent) || got.SchemaVersion != want.SchemaVersion || got.CommandVersion != want.CommandVersion || !got.RecordedAt.Equal(want.RecordedAt) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
}

func openCommandIntentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openCommandIntentTestDBAtPath(t, filepath.Join(t.TempDir(), "orchestration-command.sqlite"))
}

func openCommandIntentTestDBAtPath(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("sqliteruntime.Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close command intent database: %v", err)
		}
	})
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("RunEmbeddedMigrations() error = %v", err)
	}
	return db
}

func assertCommandIntentRowCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", commandIntentTableName)
	if err := db.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("count command intent rows: %v", err)
	}
	if got != want {
		t.Fatalf("command intent row count = %d, want %d", got, want)
	}
}
