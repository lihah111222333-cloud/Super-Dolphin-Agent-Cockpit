package rpcwire

import (
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

func TestWireJSONFieldsRemainDynamicallyEnumerable(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[AuthorityIdentity](), reflect.TypeFor[SubmitParams](), reflect.TypeFor[QueryParams](),
		reflect.TypeFor[Result](), reflect.TypeFor[Failure](),
	} {
		t.Run(typ.Name(), func(t *testing.T) {
			fields := wireJSONFields(t, typ)
			if len(fields) != typ.NumField() {
				t.Fatalf("%s enumerable fields = %d, want %d", typ.Name(), len(fields), typ.NumField())
			}
		})
	}
}

func TestSubmitAndQueryEmbedEveryDomainFieldWithoutDigestDrift(t *testing.T) {
	request := validRequest(t)
	submit := SubmitParams{SchemaVersion: SchemaVersion, Request: request}
	assertEmbeddedJSONMapping(t, request, submit.Request)
	handle := validHandle(t)
	query := QueryParams{SchemaVersion: SchemaVersion, Handle: handle}
	assertEmbeddedJSONMapping(t, handle, query.Handle)
}

func TestAuthorityReceiptIdentityMappingIsOneHot(t *testing.T) {
	authority := validAuthorityIdentity()
	handle := validHandle(t)
	terminal := validTerminalOutcome(t, authority, handle)
	identityType := reflect.TypeFor[AuthorityIdentity]()
	for index := 0; index < identityType.NumField(); index++ {
		field := identityType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			mutated := authority
			mutateWireField(t, reflect.ValueOf(&mutated).Elem().Field(index))
			result := Result{SchemaVersion: SchemaVersion, Authority: mutated, Outcome: &terminal}
			err := result.ValidateForMethod(MethodQuery)
			if field.Name == "InstanceID" {
				if err != nil {
					t.Fatalf("instance-only mutation error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("AuthorityIdentity.%s mutation did not break terminal receipt mapping", field.Name)
			}
		})
	}
}

func TestWirePublicShapeForbidsPayloadAndCredentialFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[AuthorityIdentity](), reflect.TypeFor[SubmitParams](), reflect.TypeFor[QueryParams](),
		reflect.TypeFor[Result](), reflect.TypeFor[Failure](),
	} {
		for _, path := range recursiveWireJSONPaths(t, typ, nil) {
			lower := strings.ToLower(path)
			for _, forbidden := range []string{
				"raw_error", "error_message", "path", "secret", "credential", "password", "token", "plaintext",
				"request_body", "response_body", "provider_payload", "authorization_header", "private_key",
			} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s JSON path %q contains forbidden concept %q", typ.Name(), path, forbidden)
				}
			}
		}
	}
	fields := wireJSONFields(t, reflect.TypeFor[Failure]())
	if len(fields) != 1 {
		t.Fatalf("Failure fields = %v, want only code", reflect.ValueOf(fields).MapKeys())
	}
	if _, ok := fields["code"]; !ok {
		t.Fatalf("Failure fields = %v, want code", reflect.ValueOf(fields).MapKeys())
	}
}

func TestRPCWireProductionHasNoAuthorityOrTransportDependencies(t *testing.T) {
	for path, imports := range rpcwireProductionImports(t) {
		for _, imported := range imports {
			for _, forbidden := range []string{
				"recoverycustody/sqliteauthority", "net/http", "creachadair/jrpc2/jhttp",
				"internal/mcpserver", "cmd/mcp-orch/tools",
			} {
				if strings.Contains(imported, forbidden) {
					t.Fatalf("production source %s imports forbidden dependency %q", filepath.Base(path), imported)
				}
			}
		}
	}
}

func wireJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "" || name == "-" {
			t.Fatalf("%s.%s has no public enumerable JSON field", typ.Name(), field.Name)
		}
		if _, duplicate := fields[name]; duplicate {
			t.Fatalf("%s has duplicate JSON field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func assertEmbeddedJSONMapping[T any](t *testing.T, producer, consumer T) {
	t.Helper()
	typ := reflect.TypeOf(producer)
	producerValue := reflect.ValueOf(producer)
	consumerValue := reflect.ValueOf(consumer)
	for name, field := range wireJSONFields(t, typ) {
		got := consumerValue.FieldByIndex(field.Index).Interface()
		want := producerValue.FieldByIndex(field.Index).Interface()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON field %q maps to %#v, want %#v", name, got, want)
		}
	}
}

func recursiveWireJSONPaths(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) []string {
	t.Helper()
	if seen == nil {
		seen = make(map[reflect.Type]bool)
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ.PkgPath() == "time" || seen[typ] {
		return nil
	}
	seen[typ] = true
	defer delete(seen, typ)
	var paths []string
	for name, field := range wireJSONFields(t, typ) {
		paths = append(paths, name)
		for _, child := range recursiveWireJSONPaths(t, field.Type, seen) {
			paths = append(paths, name+"."+child)
		}
	}
	return paths
}

func mutateWireField(t *testing.T, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		if len(value.String()) == 64 {
			value.SetString(strings.Repeat("f", 64))
		} else {
			value.SetString(value.String() + "-changed")
		}
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("unsupported one-hot field kind %s", value.Kind())
	}
}

func rpcwireProductionImports(t *testing.T) map[string][]string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate rpcwire test source")
	}
	directory := filepath.Dir(current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read rpcwire directory: %v", err)
	}
	imports := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		imports[path] = rpcwireImportsFromFile(t, path)
	}
	if len(imports) == 0 {
		t.Fatal("rpcwire production source set is empty")
	}
	return imports
}

func rpcwireImportsFromFile(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse production source %s: %v", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		imported, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("decode import in %s: %v", path, unquoteErr)
		}
		imports = append(imports, imported)
	}
	return imports
}
