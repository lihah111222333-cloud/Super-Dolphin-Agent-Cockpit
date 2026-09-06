package eventstore

import (
	"database/sql"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestEventStoreFieldGuard 以反射、AST 和 SQLite schema 动态检查字段闭环。
func TestEventStoreFieldGuard(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[EventEnvelope](), reflect.TypeFor[IngressFrame](), reflect.TypeFor[IngressReceipt](), reflect.TypeFor[AgentEvent](), reflect.TypeFor[SourceState](), reflect.TypeFor[AppendResult](), reflect.TypeFor[MaterializationCursor](), reflect.TypeFor[Tombstone](), reflect.TypeFor[TombstoneRequest]()} {
		t.Run("json/"+typ.Name(), func(t *testing.T) { assertEventJSONFields(t, typ) })
	}
	functions := eventStoreProductionFunctions(t)
	assertEventMapperFields(t, functions, "ingressReceiptParams", reflect.TypeFor[IngressFrame](), reflect.TypeFor[EventEnvelope]())
	assertEventMapperFields(t, functions, "agentEventParams", reflect.TypeFor[IngressFrame](), reflect.TypeFor[EventEnvelope]())
	assertEventMapperFields(t, functions, "sourceStateFromRow", reflect.TypeFor[SourceState]())
	assertEventMapperFields(t, functions, "materializationCursorFromRow", reflect.TypeFor[MaterializationCursor]())
	assertEventMapperFields(t, functions, "tombstoneFromRow", reflect.TypeFor[Tombstone]())
	assertEventMapperFields(t, functions, "frameFromReceiptRow", reflect.TypeFor[IngressFrame](), reflect.TypeFor[EventEnvelope]())
	assertEventMapperFields(t, functions, "frameFromEventRow", reflect.TypeFor[IngressFrame](), reflect.TypeFor[EventEnvelope]())

	db := openEventTestDB(t)
	for _, table := range []struct {
		name string
		typ  reflect.Type
	}{
		{"orchestration_event_ingress_receipts", reflect.TypeFor[storedb.OrchestrationEventIngressReceipt]()},
		{"orchestration_agent_events", reflect.TypeFor[storedb.OrchestrationAgentEvent]()},
		{"orchestration_event_source_states", reflect.TypeFor[storedb.OrchestrationEventSourceState]()},
		{"orchestration_event_materialization_cursors", reflect.TypeFor[storedb.OrchestrationEventMaterializationCursor]()},
		{"orchestration_event_tombstones", reflect.TypeFor[storedb.OrchestrationEventTombstone]()},
	} {
		assertEventSchemaFields(t, db, table.name, table.typ)
	}
	assertEventJSONRoundTrips(t)
}

func assertEventJSONFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	want := eventJSONFieldNames(t, typ)
	value := reflect.New(typ).Elem().Interface()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%s) error = %v", typ.Name(), err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", typ.Name(), err)
	}
	if len(object) != len(want) {
		t.Fatalf("JSON field count for %s = %d, want %d", typ.Name(), len(object), len(want))
	}
	for name := range want {
		if _, ok := object[name]; !ok {
			t.Fatalf("JSON field %q missing from %s", name, typ.Name())
		}
	}
}

func eventJSONFieldNames(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool)
	var walk func(reflect.Type)
	walk = func(current reflect.Type) {
		for index := 0; index < current.NumField(); index++ {
			field := current.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.Anonymous && tag == "" {
				embedded := field.Type
				if embedded.Kind() == reflect.Pointer {
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					walk(embedded)
					continue
				}
			}
			if tag == "" || tag == "-" {
				t.Fatalf("%s.%s has no JSON field tag", typ.Name(), field.Name)
			}
			if fields[tag] {
				t.Fatalf("%s has duplicate JSON field %q", typ.Name(), tag)
			}
			fields[tag] = true
		}
	}
	walk(typ)
	return fields
}

func assertEventJSONRoundTrips(t *testing.T) {
	t.Helper()
	envelope := EventEnvelope{EventID: "event-json", EventType: "test", SchemaVersion: 1, RootAgentID: "agent", SubjectAgentID: "agent", ReservationID: "reservation", SourceFenceRef: "fence", Provenance: "test", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-01T00:00:00Z", WriterEpoch: "writer"}
	frame := IngressFrame{SourceID: "source", SourceEpoch: "epoch", Lane: LaneControl, SourceSequence: 1, EnvelopeHash: eventDigest("json"), CanonicalCursor: "source/epoch/control/1", Envelope: envelope}
	receipt := IngressReceipt{IngressFrame: frame, Status: "materialized", ErrorRef: ""}
	event := AgentEvent{IngressFrame: frame, Status: "materialized"}
	samples := []any{envelope, frame, receipt, event, AppendResult{Receipt: receipt, Event: &event, State: SourceState{SourceID: "source", SourceEpoch: "epoch", Lane: LaneControl, NextSequence: 2, Revision: 1, Status: "active", WriterEpoch: "writer", CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z"}}, MaterializationCursor{ProjectionID: "projection", SourceID: "source", SourceEpoch: "epoch", Lane: LaneControl, Revision: 1, Status: "active", UpdatedAt: "2026-09-01T00:00:00Z"}, Tombstone{SourceID: "source", SourceEpoch: "epoch", Lane: LaneControl, SourceSequence: 1, EnvelopeHash: eventDigest("tombstone"), EventID: "event-json", CanonicalCursor: "source/epoch/control/1", Status: "expired", CreatedAt: "2026-09-01T00:00:00Z"}, TombstoneRequest{SourceID: "source", SourceEpoch: "epoch", Lane: LaneControl, SourceSequence: 1, EnvelopeHash: eventDigest("tombstone-request"), EventID: "event-json", CanonicalCursor: "source/epoch/control/1", Status: "expired"}}
	for _, sample := range samples {
		t.Run("roundtrip/"+reflect.TypeOf(sample).Name(), func(t *testing.T) {
			encoded, err := json.Marshal(sample)
			if err != nil {
				t.Fatalf("json.Marshal(%T) error = %v", sample, err)
			}
			decoded := reflect.New(reflect.TypeOf(sample))
			if err := json.Unmarshal(encoded, decoded.Interface()); err != nil {
				t.Fatalf("json.Unmarshal(%T) error = %v", sample, err)
			}
			if !reflect.DeepEqual(decoded.Elem().Interface(), sample) {
				t.Fatalf("JSON roundtrip = %#v, want %#v", decoded.Elem().Interface(), sample)
			}
		})
	}
}

func eventStoreProductionFunctions(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating event store source")
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, name := range []string{"params.go", "mapping.go"} {
		path := filepath.Join(filepath.Dir(testFile), name)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read event store source %s: %v", path, err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Fatalf("parse event store source %s: %v", path, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name == nil {
				continue
			}
			if _, exists := functions[function.Name.Name]; exists {
				t.Fatalf("duplicate event store mapper %s", function.Name.Name)
			}
			functions[function.Name.Name] = function
		}
	}
	return functions
}

func assertEventMapperFields(t *testing.T, functions map[string]*ast.FuncDecl, mapper string, types ...reflect.Type) {
	t.Helper()
	function, ok := functions[mapper]
	if !ok || function.Body == nil {
		t.Fatalf("production mapper %s is missing", mapper)
	}
	used := make(map[string]bool)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.SelectorExpr:
			used[item.Sel.Name] = true
		case *ast.KeyValueExpr:
			if key, ok := item.Key.(*ast.Ident); ok {
				used[key.Name] = true
			}
		}
		return true
	})
	for _, typ := range types {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !used[field.Name] {
				t.Fatalf("production mapper %s does not reference %s.%s", mapper, typ.Name(), field.Name)
			}
		}
	}
}

func assertEventSchemaFields(t *testing.T, db *sql.DB, table string, typ reflect.Type) {
	t.Helper()
	want := eventDBTags(t, typ)
	got := eventTableColumns(t, db, table)
	assertEventExactFields(t, table, want, got)
}

func eventDBTags(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("sqlc row %s.%s has no database tag", typ.Name(), field.Name)
		}
		if fields[name] {
			t.Fatalf("sqlc row %s has duplicate database field %q", typ.Name(), name)
		}
		fields[name] = true
	}
	return fields
}

func eventTableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("inspect SQLite table %s: %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan SQLite table %s: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite table %s: %v", table, err)
	}
	return columns
}

func assertEventExactFields(t *testing.T, table string, want, got map[string]bool) {
	t.Helper()
	var missing, stale []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s schema field drift: missing=%v stale=%v", table, missing, stale)
	}
}
