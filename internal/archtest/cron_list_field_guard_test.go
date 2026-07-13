package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	modulecron "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/cron"
	storecron "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/cron"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

func TestCronListPageFieldGuard(t *testing.T) {
	want := map[string]bool{"Limit": true, "Cursor": true}
	for _, typ := range []reflect.Type{reflect.TypeFor[modulecron.ListJobsPageParams](), reflect.TypeFor[storecron.ListJobsPageParams]()} {
		got := map[string]bool{}
		for i := range typ.NumField() {
			got[typ.Field(i).Name] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s fields=%v want=%v", typ, got, want)
		}
	}
	pageWant := map[string]bool{"Jobs": true, "NextCursor": true, "HasMore": true}
	for _, typ := range []reflect.Type{reflect.TypeFor[modulecron.JobPage](), reflect.TypeFor[storecron.JobPage]()} {
		got := map[string]bool{}
		for i := range typ.NumField() {
			got[typ.Field(i).Name] = true
		}
		if !reflect.DeepEqual(got, pageWant) {
			t.Fatalf("%s fields=%v want=%v", typ, got, pageWant)
		}
	}
	assertCronListSQLCFields(t)
	assertCronListRPCWireFields(t)
	assertCronListMapperCoverage(t)
	assertCronListRPCRejectsMissingCursor(t)
}

func assertCronListSQLCFields(t *testing.T) {
	t.Helper()
	want := map[string]bool{"CursorCreatedAt": true, "CursorID": true, "LimitPlusOne": true}
	got := structFieldSet(reflect.TypeFor[sqlc.ListCronJobsPageParams]())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sqlc ListCronJobsPageParams fields=%v want=%v", got, want)
	}
	row := structFieldSet(reflect.TypeFor[sqlc.CronJob]())
	for _, required := range []string{"ID", "CreatedAt"} {
		if !row[required] {
			t.Fatalf("sqlc CronJob producer missing sort field %q: %v", required, row)
		}
	}
}

func assertCronListRPCWireFields(t *testing.T) {
	t.Helper()
	file := cronListParseGo(t, "internal/module/cron/rpc.go")
	if got, want := cronListStructJSONFields(t, file, "cronListParams"), map[string]string{"Limit": "limit", "Cursor": "cursor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cronListParams fields=%v want=%v", got, want)
	}
	if got, want := cronListStructJSONFields(t, file, "cronListResponse"), map[string]string{"Jobs": "jobs", "NextCursor": "next_cursor", "HasMore": "has_more"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cronListResponse fields=%v want=%v", got, want)
	}
}

func assertCronListMapperCoverage(t *testing.T) {
	t.Helper()
	for _, check := range []struct {
		path     string
		typeName string
		want     map[string]bool
	}{
		{"internal/module/cron/service.go", "JobPage", map[string]bool{"Jobs": true, "NextCursor": true, "HasMore": true}},
		{"internal/app/storeadapter/cron/adapter.go", "cronstore.ListJobsPageParams", map[string]bool{"Limit": true, "Cursor": true}},
		{"internal/app/storeadapter/cron/adapter.go", "cron.JobRecordPage", map[string]bool{"Jobs": true, "NextCursor": true, "HasMore": true}},
		{"internal/module/cron/rpc.go", "ListJobsPageParams", map[string]bool{"Limit": true, "Cursor": true}},
		{"internal/module/cron/rpc.go", "cronListResponse", map[string]bool{"Jobs": true, "NextCursor": true, "HasMore": true}},
		{"internal/store/cron/list_page.go", "sqlc.ListCronJobsPageParams", map[string]bool{"CursorCreatedAt": true, "CursorID": true, "LimitPlusOne": true}},
		{"internal/store/cron/list_page.go", "JobPage", map[string]bool{"Jobs": true, "NextCursor": true, "HasMore": true}},
	} {
		assertCronListCompositeFields(t, cronListParseGo(t, check.path), check.typeName, check.want)
	}
}

func assertCronListCompositeFields(t *testing.T, file *ast.File, typeName string, want map[string]bool) {
	t.Helper()
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || cronListExprName(lit.Type) != typeName {
			return true
		}
		if len(lit.Elts) == 0 {
			return true
		}
		got := map[string]bool{}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				t.Fatalf("%s composite literal in %s is positional", typeName, file.Name.Name)
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				t.Fatalf("%s composite literal has non-identifier key", typeName)
			}
			got[key.Name] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s mapper fields=%v want=%v", typeName, got, want)
		}
		found = true
		return false
	})
	if !found {
		t.Fatalf("missing %s composite mapper", typeName)
	}
}

func cronListExprName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		if prefix, ok := value.X.(*ast.Ident); ok {
			return prefix.Name + "." + value.Sel.Name
		}
	}
	return ""
}

// Cursor must be present on the wire even though its only legal first-page value
// is "". A plain string silently turns an omitted JSON key into that value.
func assertCronListRPCRejectsMissingCursor(t *testing.T) {
	t.Helper()
	source := string(cronListReadFile(t, "internal/module/cron/rpc.go"))
	if !strings.Contains(source, "Cursor *string") && !strings.Contains(source, "func (cronListParams) UnmarshalJSON") {
		t.Fatal("cronjob/list cursor presence is not guarded: missing cursor decodes as first-page empty string")
	}
}

func structFieldSet(typ reflect.Type) map[string]bool {
	got := map[string]bool{}
	for i := range typ.NumField() {
		got[typ.Field(i).Name] = true
	}
	return got
}

func cronListParseGo(t *testing.T, rel string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), rel, cronListReadFile(t, rel), parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return file
}

func cronListStructJSONFields(t *testing.T, file *ast.File, name string) map[string]string {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != name {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is not struct", name)
			}
			out := map[string]string{}
			for _, field := range structType.Fields.List {
				if len(field.Names) != 1 || field.Tag == nil {
					t.Fatalf("%s has untagged field", name)
				}
				out[field.Names[0].Name] = strings.Trim(field.Tag.Value, "`")[len("json:\"") : len(strings.Trim(field.Tag.Value, "`"))-1]
			}
			return out
		}
	}
	t.Fatalf("missing %s", name)
	return nil
}

func cronListReadFile(t *testing.T, rel string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for range 8 {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = filepath.Dir(root)
	}
	contents, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return contents
}
