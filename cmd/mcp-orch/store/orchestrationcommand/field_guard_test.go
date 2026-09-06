package orchestrationcommand

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
	"time"
	"unicode"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestCommandIntentWireFieldsUseDynamicSnakeCaseAndRoundTrip(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[Intent](), reflect.TypeFor[Record](), reflect.TypeFor[Key]()} {
		t.Run(typ.Name(), func(t *testing.T) {
			jsonNames := reflectedCommandIntentJSONFields(t, typ)
			if len(jsonNames) != typ.NumField() {
				t.Fatalf("%s JSON field count = %d, want %d", typ.Name(), len(jsonNames), typ.NumField())
			}
		})
	}

	samples := []any{
		commandIntentFixture("json-command"),
		Record{
			Intent:         commandIntentFixture("json-record-command"),
			SchemaVersion:  1,
			CommandVersion: 1,
			RecordedAt:     time.Date(2026, time.September, 1, 12, 34, 56, 789000000, time.UTC),
		},
		Key{Namespace: "json-namespace", Digest: strings.Repeat("e", 64)},
	}
	for _, sample := range samples {
		t.Run(reflect.TypeOf(sample).Name(), func(t *testing.T) {
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
			assertCommandIntentJSONObjectFields(t, encoded, reflect.TypeOf(sample))
			assertCommandIntentJSONHasNoExecutionFields(t, encoded)
		})
	}
}

func TestCommandIntentMappersCoverEveryReflectedField(t *testing.T) {
	functions := commandIntentProductionFunctions(t)
	assertCommandIntentMapperFields(t, functions, "intentParams", reflect.TypeFor[Intent]())
	assertCommandIntentMapperFields(t, functions, "recordFromRow", reflect.TypeFor[Intent]())
	assertCommandIntentMapperFields(t, functions, "recordFromRow", reflect.TypeFor[Record]())
}

func TestCommandIntentFreshWriteOneHotPreservesEveryIntentField(t *testing.T) {
	intentType := reflect.TypeFor[Intent]()
	for fieldIndex := 0; fieldIndex < intentType.NumField(); fieldIndex++ {
		field := intentType.Field(fieldIndex)
		t.Run(field.Name, func(t *testing.T) { assertCommandIntentOneHotField(t, field) })
	}
}

func assertCommandIntentOneHotField(t *testing.T, field reflect.StructField) {
	t.Helper()
	db := openCommandIntentTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	base := commandIntentFixture("one-hot-" + field.Name)
	intent, _ := mutateCommandIntentField(t, base, field)
	got, err := store.Reserve(t.Context(), intent)
	if err != nil {
		t.Fatalf("Reserve(%s one-hot) error = %v", field.Name, err)
	}
	if !reflect.DeepEqual(got.Intent, intent) {
		t.Fatalf("Reserve(%s) intent = %#v, want %#v", field.Name, got.Intent, intent)
	}
	byID, err := store.Get(t.Context(), intent.CommandID)
	if err != nil {
		t.Fatalf("Get(%s one-hot) error = %v", field.Name, err)
	}
	byKey, err := store.GetByKey(t.Context(), Key{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest})
	if err != nil {
		t.Fatalf("GetByKey(%s one-hot) error = %v", field.Name, err)
	}
	if !reflect.DeepEqual(byID.Intent, intent) || !reflect.DeepEqual(byKey.Intent, intent) {
		t.Fatalf("%s one-hot reads lost fields: byID=%#v byKey=%#v want=%#v", field.Name, byID.Intent, byKey.Intent, intent)
	}
	assertCommandIntentRowCount(t, db, 1)
}

func TestCommandIntentRecordFromRowUsesFixedValuesForEveryField(t *testing.T) {
	recordedAt := time.Date(2026, time.September, 1, 14, 15, 16, 123456789, time.UTC)
	want, row := commandIntentRecordAndSQLCRow(t, commandIntentFixture("row-mapper-command"), recordedAt)
	got, err := recordFromRow(row)
	if err != nil {
		t.Fatalf("recordFromRow() error = %v", err)
	}
	assertCommandIntentRecordEqual(t, got, want)
}

func TestCommandIntentSchemaAndSQLCFieldsStayComplete(t *testing.T) {
	wantColumns := commandIntentPersistenceFields(t)
	gotSQLCColumns := commandIntentDBTags(t, reflect.TypeFor[storedb.OrchestrationCommandIntent]())
	assertCommandIntentExactFieldSet(t, "sqlc orchestration command intent row", wantColumns, gotSQLCColumns)

	db := openCommandIntentTestDB(t)
	gotSchemaColumns := commandIntentSQLiteColumns(t, db, commandIntentTableName)
	assertCommandIntentExactFieldSet(t, "SQLite orchestration command intent table", wantColumns, gotSchemaColumns)
	for column := range gotSchemaColumns {
		lower := strings.ToLower(column)
		for _, forbidden := range []string{"prompt", "accepted", "dispatch", "authorization", "permission", "status", "execute"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("SQLite schema column %q contains forbidden execution/prompt concept %q", column, forbidden)
			}
		}
	}
}

func reflectedCommandIntentJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for fieldIndex := 0; fieldIndex < typ.NumField(); fieldIndex++ {
		field := typ.Field(fieldIndex)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no enumerable JSON tag", typ.Name(), field.Name)
		}
		if name != commandIntentSnakeCase(field.Name) {
			t.Fatalf("%s.%s JSON name = %q, want snake_case %q", typ.Name(), field.Name, name, commandIntentSnakeCase(field.Name))
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s has duplicate JSON field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func commandIntentSnakeCase(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	for index, r := range runes {
		if unicode.IsUpper(r) {
			previousLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			acronymBoundary := index > 0 && unicode.IsUpper(runes[index-1]) && index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if previousLowerOrDigit || acronymBoundary {
				builder.WriteByte('_')
			}
			builder.WriteRune(unicode.ToLower(r))
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func assertCommandIntentJSONObjectFields(t *testing.T, encoded []byte, typ reflect.Type) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode %s JSON object: %v", typ.Name(), err)
	}
	want := reflectedCommandIntentJSONFields(t, typ)
	if len(object) != len(want) {
		t.Fatalf("%s JSON keys = %v, want exactly %v", typ.Name(), sortedCommandIntentMapKeys(object), sortedCommandIntentMapKeys(want))
	}
	for name := range want {
		if _, ok := object[name]; !ok {
			t.Fatalf("%s JSON object is missing %q", typ.Name(), name)
		}
	}
	if typ != reflect.TypeFor[Record]() {
		return
	}
	intentRaw, ok := object["intent"]
	if !ok {
		t.Fatal("Record JSON object is missing nested intent")
	}
	assertCommandIntentJSONObjectFields(t, intentRaw, reflect.TypeFor[Intent]())
}

func assertCommandIntentJSONHasNoExecutionFields(t *testing.T, encoded []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("decode JSON for forbidden field check: %v", err)
	}
	var walk func(any)
	walk = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				lower := strings.ToLower(key)
				for _, forbidden := range []string{"prompt", "accepted", "dispatch", "authorization", "permission", "status", "execute"} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("wire field %q contains forbidden execution/prompt concept %q", key, forbidden)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
}

func sortedCommandIntentMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func commandIntentProductionFunctions(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating command intent source")
	}
	return parseCommandIntentProductionFunctions(t, filepath.Join(filepath.Dir(testFile), "store.go"))
}

func commandIntentRecordAndSQLCRow(t *testing.T, intent Intent, recordedAt time.Time) (Record, storedb.OrchestrationCommandIntent) {
	t.Helper()
	intentValue := reflect.ValueOf(intent)
	rowValue := reflect.New(reflect.TypeFor[storedb.OrchestrationCommandIntent]()).Elem()
	for fieldIndex := 0; fieldIndex < intentValue.NumField(); fieldIndex++ {
		field := intentValue.Type().Field(fieldIndex)
		rowField := rowValue.FieldByName(field.Name)
		if !rowField.IsValid() || rowField.Type() != field.Type {
			t.Fatalf("sqlc row is missing compatible Intent.%s", field.Name)
		}
		rowField.Set(intentValue.Field(fieldIndex))
	}
	recordValue := reflect.New(reflect.TypeFor[Record]()).Elem()
	recordValue.FieldByName("Intent").Set(intentValue)
	for fieldIndex := 0; fieldIndex < recordValue.NumField(); fieldIndex++ {
		field := recordValue.Type().Field(fieldIndex)
		if field.Name == "Intent" {
			continue
		}
		recordField := recordValue.Field(fieldIndex)
		rowField := rowValue.FieldByName(field.Name)
		if !rowField.IsValid() {
			t.Fatalf("sqlc row is missing Record.%s", field.Name)
		}
		setCommandIntentRecordFixtureField(t, field, recordField, rowField, recordedAt)
	}
	return recordValue.Interface().(Record), rowValue.Interface().(storedb.OrchestrationCommandIntent)
}

func setCommandIntentRecordFixtureField(t *testing.T, field reflect.StructField, recordField, rowField reflect.Value, recordedAt time.Time) {
	t.Helper()
	switch {
	case recordField.Kind() == reflect.Int64 && rowField.Kind() == reflect.Int64:
		recordField.SetInt(1)
		rowField.SetInt(1)
	case recordField.Type() == reflect.TypeFor[time.Time]() && rowField.Kind() == reflect.String:
		recordField.Set(reflect.ValueOf(recordedAt))
		rowField.SetString(recordedAt.Format(time.RFC3339Nano))
	default:
		t.Fatalf("unsupported Record.%s/sqlc row fixture types: %s/%s", field.Name, recordField.Type(), rowField.Type())
	}
}

func parseCommandIntentProductionFunctions(t *testing.T, sourcePath string) map[string]*ast.FuncDecl {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read command intent source %s: %v", sourcePath, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, source, 0)
	if err != nil {
		t.Fatalf("parse command intent source %s: %v", sourcePath, err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil || !isCommandIntentMapper(function.Name.Name) {
			continue
		}
		if _, exists := functions[function.Name.Name]; exists {
			t.Fatalf("duplicate production mapper %s", function.Name.Name)
		}
		functions[function.Name.Name] = function
	}
	return functions
}

func isCommandIntentMapper(name string) bool {
	return name == "intentParams" || name == "recordFromRow"
}

func assertCommandIntentMapperFields(t *testing.T, functions map[string]*ast.FuncDecl, mapperName string, typ reflect.Type) {
	t.Helper()
	function, ok := functions[mapperName]
	if !ok || function.Body == nil {
		t.Fatalf("production mapper %s is missing", mapperName)
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
	for fieldIndex := 0; fieldIndex < typ.NumField(); fieldIndex++ {
		field := typ.Field(fieldIndex)
		if !used[field.Name] {
			t.Fatalf("production mapper %s does not reference %s.%s", mapperName, typ.Name(), field.Name)
		}
	}
}

func commandIntentPersistenceFields(t *testing.T) map[string]bool {
	t.Helper()
	fields := make(map[string]bool)
	for _, typ := range []reflect.Type{reflect.TypeFor[Intent](), reflect.TypeFor[Record]()} {
		for fieldIndex := 0; fieldIndex < typ.NumField(); fieldIndex++ {
			field := typ.Field(fieldIndex)
			if typ == reflect.TypeFor[Record]() && field.Name == "Intent" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				t.Fatalf("persistence field %s.%s has no JSON name", typ.Name(), field.Name)
			}
			if fields[name] {
				t.Fatalf("duplicate persistence field %q", name)
			}
			fields[name] = true
		}
	}
	return fields
}

func commandIntentDBTags(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, typ.NumField())
	for fieldIndex := 0; fieldIndex < typ.NumField(); fieldIndex++ {
		field := typ.Field(fieldIndex)
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

func commandIntentSQLiteColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
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

func assertCommandIntentExactFieldSet(t *testing.T, name string, want, got map[string]bool) {
	t.Helper()
	var missing, stale []string
	for field := range want {
		if !got[field] {
			missing = append(missing, field)
		}
	}
	for field := range got {
		if !want[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s field drift: missing=%v stale=%v", name, missing, stale)
	}
}
