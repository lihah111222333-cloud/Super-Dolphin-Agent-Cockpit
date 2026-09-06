package operatorsource

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestOperatorSourceContract(t *testing.T) {
	t.Run("missing_schema_RED", func(t *testing.T) {
		db := openDB(t, ":memory:")
		s := owner(t, db)
		_, err := s.GetLatest(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no such table: orchestration_operator_sources") {
			t.Fatalf("missing schema: %v", err)
		}
	})
	t.Run("lifecycle_reopen", testLifecycle)
	t.Run("transaction_rollback", testRollback)
	t.Run("selector_one_hot", testSelectors)
	t.Run("activation_fields", testActivationFields)
	t.Run("source_roundtrip_fields", testSourceFields)
	t.Run("direct_sql", testDirectSQL)
	t.Run("timestamp", testTimestamp)
	t.Run("invalid_dependencies", testDependencies)
}
func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func owner(t *testing.T, db *sql.DB) *Store {
	t.Helper()
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func setup(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	db := openDB(t, filepath.Join(t.TempDir(), "source.db"))
	raw, err := os.ReadFile("../../../../internal/platform/db/sqlite/migrations/138_orchestration_operator_sources.sql")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, string(raw))
	return db, owner(t, db)
}
func execSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func request() ActivateRequest {
	return ActivateRequest{"operator/a", "scope/a", "orch-auth:boot-a", 0}
}
func activateOK(t *testing.T, s *Store, r ActivateRequest) Source {
	t.Helper()
	v, err := s.Activate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func selector(v Source) Selector {
	return Selector{v.PrincipalRef, v.ScopeRef, v.AuthContext, v.SourceID, v.SourceFenceRef, v.Generation}
}
func assertLatest(t *testing.T, s *Store, want Source) {
	t.Helper()
	got, err := s.GetLatest(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("latest=%+v err=%v want=%+v", got, err, want)
	}
}
func testLifecycle(t *testing.T) {
	db, s := setup(t)
	ctx := context.Background()
	r := request()
	first := activateOK(t, s, r)
	if got := activateOK(t, s, r); !reflect.DeepEqual(got, first) {
		t.Fatal("replay differs")
	}
	r.AuthContext = "orch-auth:boot-b"
	r.PrincipalRef = "operator/b"
	r.ScopeRef = "scope/b"
	r.ExpectedGeneration = 1
	second := activateOK(t, s, r)
	if second.Generation != 2 {
		t.Fatal(second)
	}
	if _, err := s.ResolveCurrent(ctx, selector(first)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	old, err := s.Revoke(ctx, selector(first))
	if err != nil || old.State != "revoked" {
		t.Fatalf("old close %v %v", old, err)
	}
	assertLatest(t, s, second)
	if _, err = s.Activate(ctx, request()); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	wrong := r
	wrong.AuthContext = "orch-auth:wrong"
	wrong.ExpectedGeneration = 0
	if _, err = s.Activate(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	assertLatest(t, s, second)
	reopened := reopenOwner(t, db)
	assertLatest(t, reopened, second)
	verifyRevokedLifecycle(t, reopened, second, r)
}
func reopenOwner(t *testing.T, db *sql.DB) *Store {
	t.Helper()
	var path string
	if err := db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return owner(t, openDB(t, path))
}
func verifyRevokedLifecycle(t *testing.T, reopened *Store, second Source, r ActivateRequest) {
	t.Helper()
	ctx := context.Background()
	revoked, err := reopened.Revoke(ctx, selector(second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.ResolveCurrent(ctx, selector(second)); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	again, err := reopened.Revoke(ctx, selector(second))
	if err != nil || !reflect.DeepEqual(again, revoked) {
		t.Fatal(err)
	}
	r.AuthContext = "orch-auth:boot-c"
	r.ExpectedGeneration = 2
	if got := activateOK(t, reopened, r); got.Generation != 3 {
		t.Fatal(got)
	}
}
func testRollback(t *testing.T) {
	db, s := setup(t)
	first := activateOK(t, s, request())
	execSQL(t, db, "CREATE TRIGGER inject_failure BEFORE INSERT ON orchestration_operator_sources WHEN NEW.generation=2 BEGIN SELECT RAISE(ABORT,'injected insert fault'); END")
	r := request()
	r.ExpectedGeneration = 1
	r.AuthContext = "orch-auth:boot-b"
	if _, err := s.Activate(context.Background(), r); err == nil || !strings.Contains(err.Error(), "injected insert fault") {
		t.Fatal(err)
	}
	assertLatest(t, s, first)
}
func testSelectors(t *testing.T) {
	_, s := setup(t)
	first := activateOK(t, s, request())
	base := selector(first)
	typ := reflect.TypeOf(base)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			bad := base
			v := reflect.ValueOf(&bad).Elem().Field(i)
			switch v.Kind() {
			case reflect.String:
				v.SetString(v.String() + "x")
			case reflect.Int64:
				v.SetInt(v.Int() + 1)
			default:
				t.Fatalf("uncovered selector field %s", typ.Field(i).Name)
			}
			if _, err := s.ResolveCurrent(context.Background(), bad); err == nil {
				t.Fatal("mutated selector resolved")
			}
			if _, err := s.Revoke(context.Background(), bad); err == nil {
				t.Fatal("mutated selector revoked")
			}
			assertLatest(t, s, first)
		})
	}
}
func testActivationFields(t *testing.T) {
	typ := reflect.TypeOf(ActivateRequest{})
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			_, s := setup(t)
			r := request()
			v := reflect.ValueOf(&r).Elem().Field(i)
			switch v.Kind() {
			case reflect.String:
				v.SetString("")
			case reflect.Int64:
				v.SetInt(-1)
			default:
				t.Fatalf("uncovered activation field %s", typ.Field(i).Name)
			}
			if _, err := s.Activate(context.Background(), r); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			if _, err := s.GetLatest(context.Background()); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
}
func testSourceFields(t *testing.T) {
	_, s := setup(t)
	first := activateOK(t, s, request())
	row, err := s.queries.GetLatestOperatorSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	verifySourceMapping(t, row, first)
	revoked, err := s.Revoke(context.Background(), selector(first))
	if err != nil {
		t.Fatal(err)
	}
	row, err = s.queries.GetLatestOperatorSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	verifySourceMapping(t, row, revoked)
	for i := 0; i < reflect.TypeOf(row).NumField(); i++ {
		damaged := row
		field := reflect.ValueOf(&damaged).Elem().Field(i)
		field.Set(reflect.Zero(field.Type()))
		if _, err := decode(damaged); err == nil {
			t.Fatalf("damaged field accepted: %s", reflect.TypeOf(row).Field(i).Name)
		}
	}

}
func verifySourceMapping(t *testing.T, row sqlc.OrchestrationOperatorSource, source Source) {
	t.Helper()
	sv := reflect.ValueOf(source)
	rv := reflect.ValueOf(row)
	if sv.NumField() != rv.NumField() {
		t.Fatal("source/sql field drift")
	}
	for i := 0; i < sv.NumField(); i++ {
		name := sv.Type().Field(i).Name
		actual := sv.Field(i)
		raw := rv.FieldByName(name)
		if !raw.IsValid() {
			t.Fatalf("missing SQL consumer %s", name)
		}
		verifyMappedField(t, name, actual, raw)
	}
}
func verifyMappedField(t *testing.T, name string, actual, raw reflect.Value) {
	t.Helper()
	switch name {
	case "CreatedAt":
		if actual.Interface().(time.Time).Format(timestampLayout) != raw.String() {
			t.Fatal(name)
		}
	case "RevokedAt":
		if actual.IsNil() != raw.IsNil() {
			t.Fatal(name)
		}
		if !actual.IsNil() && actual.Interface().(*time.Time).Format(timestampLayout) != raw.Elem().String() {
			t.Fatal(name)
		}
	default:
		if !reflect.DeepEqual(actual.Interface(), raw.Interface()) {
			t.Fatalf("field mismatch %s", name)
		}
	}
}
func testDirectSQL(t *testing.T) {
	db, s := setup(t)
	first := activateOK(t, s, request())
	for _, q := range []string{"DELETE FROM orchestration_operator_sources", "UPDATE orchestration_operator_sources SET principal_ref='other'", "UPDATE orchestration_operator_sources SET generation=2", "UPDATE orchestration_operator_sources SET state='revoked'", "UPDATE orchestration_operator_sources SET schema_version=2"} {
		if _, err := db.Exec(q); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	for _, gen := range []int64{1, 2, 3} {
		if _, err := insertRaw(db, "new", gen, "2026-09-06T01:02:03.999999999Z"); err == nil {
			t.Fatalf("active/jump accepted %d", gen)
		}
	}
	assertLatest(t, s, first)
	if _, err := s.Revoke(context.Background(), selector(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := insertRaw(db, "new", 3, "2026-09-06T01:02:03.999999999Z"); err == nil {
		t.Fatal("skip generation accepted")
	}
	if _, err := db.Exec("UPDATE orchestration_operator_sources SET state='active',revoked_at=NULL"); err == nil {
		t.Fatal("resurrection accepted")
	}
}
func insertRaw(db *sql.DB, auth string, gen int64, stamp string) (sql.Result, error) {
	id, fence, err := identity("operator/a", "scope/a", auth, gen)
	if err != nil {
		return nil, err
	}
	return db.Exec("INSERT INTO orchestration_operator_sources(source_id,source_fence_ref,principal_ref,scope_ref,auth_context,generation,state,created_at,schema_version) VALUES(?,?,'operator/a','scope/a',?,?,'active',?,1)", id, fence, auth, gen, stamp)
}
func testTimestamp(t *testing.T) {
	valid := "2026-09-06T01:02:03.999999999Z"
	db, s := setup(t)
	if _, err := insertRaw(db, "orch-auth:nano", 1, valid); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetLatest(context.Background())
	if err != nil || got.CreatedAt.Format(timestampLayout) != valid {
		t.Fatalf("nano %v %v", got, err)
	}
	for _, bad := range []string{"2026-02-30T01:02:03.000000000Z", "2026-09-06T01:02:03.abcdefghiZ", "2026-09-06T01:02:03.999Z", "2026-09-06T01:02:03.000000000+00:00"} {
		t.Run(bad, func(t *testing.T) {
			db, _ := setup(t)
			if _, err := insertRaw(db, "bad", 1, bad); err == nil {
				t.Fatal("SQL accepted bad timestamp")
			}
			if _, err := parseStamp(bad); err == nil {
				t.Fatal("Go accepted bad timestamp")
			}
		})
	}
}

func testDependencies(t *testing.T) {
	if _, err := NewStore(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	_, s := setup(t)
	var typedNil *nilContext
	for _, ctx := range []context.Context{nil, typedNil} {
		if _, err := s.GetLatest(ctx); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.Activate(ctx, request()); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.ResolveCurrent(ctx, Selector{}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.Revoke(ctx, Selector{}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	first := activateOK(t, s, request())
	bad := request()
	bad.ExpectedGeneration = first.Generation
	if _, err := s.Activate(context.Background(), bad); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	assertLatest(t, s, first)
}

type nilContext struct{ context.Context }
