package room

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestRoomJSONFieldGuardEnumeratesProducerDTOs derives the producer set from
// reflection; adding an untagged/private/duplicate field fails immediately.
func TestRoomJSONFieldGuardEnumeratesProducerDTOs(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Principal](), reflect.TypeFor[Room](), reflect.TypeFor[Operation](), reflect.TypeFor[Member](), reflect.TypeFor[FanoutPlan](),
		reflect.TypeFor[AdmissionCandidate](), reflect.TypeFor[AdmissionHead](), reflect.TypeFor[AdmissionResult](),
		reflect.TypeFor[TargetReceipt](), reflect.TypeFor[FanoutResult](),
	} {
		fields := roomJSONFields(t, typ)
		if len(fields) != typ.NumField() {
			t.Fatalf("%s fields=%d want=%d", typ.Name(), len(fields), typ.NumField())
		}
	}
}

func TestRoomOperationDigestFieldGuard(t *testing.T) {
	base := operationFixture(OperationBroadcast)
	want := base.RequestDigest
	value := reflect.ValueOf(&base).Elem()
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "RequestDigest" {
			continue
		}
		mutated := base
		fv := reflect.ValueOf(&mutated).Elem().Field(i)
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(field.Name + "-changed")
		case reflect.Int64:
			fv.SetInt(fv.Int() + 1)
		case reflect.Struct:
			p := mutated.Principal
			p.PrincipalRef += "-changed"
			mutated.Principal = p
		default:
			t.Fatalf("unsupported field kind %s", fv.Kind())
		}
		got, err := ComputeOperationDigest(mutated)
		if err != nil {
			t.Fatal(err)
		}
		if got == want {
			t.Fatalf("Operation.%s is absent from request digest", field.Name)
		}
	}
}

// TestRoomProductionCodeGuard keeps the candidate transport-neutral and
// prevents accidental ownership of persistence, providers, Fx, or network.
func TestRoomProductionCodeGuard(t *testing.T) {
	files := roomProductionFiles(t)
	for path, file := range files {
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"go.uber.org/fx", "/store", "/provider", "database/sql", "sqlite", "net/http", "os/exec", "recoverycustody"} {
				if strings.Contains(imported, forbidden) {
					t.Fatalf("%s imports forbidden dependency %q", filepath.Base(path), imported)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			for _, forbidden := range []string{"sql", "Provider", "Fx", "Registry", "RecoveryCustody", "Exec", "HTTP"} {
				if ident.Name == forbidden {
					t.Fatalf("%s contains forbidden implementation identifier %q", filepath.Base(path), ident.Name)
				}
			}
			return true
		})
	}
}

func roomJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "" || name == "-" {
			t.Fatalf("%s.%s is not enumerable", typ.Name(), field.Name)
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s duplicate json field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func roomProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve package")
	}
	dir := filepath.Dir(current)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(set, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = file
	}
	if len(files) == 0 {
		t.Fatal("room production files empty")
	}
	return files
}
