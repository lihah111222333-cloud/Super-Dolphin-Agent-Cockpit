package admissionproof

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
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

// TestAdmissionProofJSONFieldsRemainEnumerable 动态锁定所有 proof wire DTO 与 custody consumer 字段。
func TestAdmissionProofJSONFieldsRemainEnumerable(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Config](), reflect.TypeFor[ContentResolveKey](),
		reflect.TypeFor[ResourceResolveKey](), reflect.TypeFor[ContentSnapshot](),
		reflect.TypeFor[ResourceSnapshot](), reflect.TypeFor[batchDigestInput](),
		reflect.TypeFor[recoverycustody.SubmissionRequest](),
	} {
		t.Run(typ.Name(), func(t *testing.T) {
			fields := admissionProofJSONFields(t, typ)
			if len(fields) != typ.NumField() {
				t.Fatalf("%s enumerable JSON fields = %d, want %d", typ.Name(), len(fields), typ.NumField())
			}
		})
	}
}

// TestAdmissionProofResolveKeysExcludeCallerOwnerSelection 禁止 caller 选择 content object/ticket 或 resource reservation/policy。
func TestAdmissionProofResolveKeysExcludeCallerOwnerSelection(t *testing.T) {
	for name := range admissionProofJSONFields(t, reflect.TypeFor[ContentResolveKey]()) {
		if forbiddenContentResolveField(name) {
			t.Fatalf("ContentResolveKey exposes caller-selected owner field %q", name)
		}
	}
	for name := range admissionProofJSONFields(t, reflect.TypeFor[ResourceResolveKey]()) {
		if strings.Contains(name, "reservation") || strings.Contains(name, "policy") {
			t.Fatalf("ResourceResolveKey exposes caller-selected owner field %q", name)
		}
	}
}

// TestAdmissionProofSnapshotDigestsCoverEveryBusinessField 对所有业务字段做 one-hot 摘要变异，仅豁免 self-digest。
func TestAdmissionProofSnapshotDigestsCoverEveryBusinessField(t *testing.T) {
	assertAdmissionProofDigestCoverage(t, ContentSnapshot{}, "SnapshotDigest", ComputeContentSnapshotDigest)
	assertAdmissionProofDigestCoverage(t, ResourceSnapshot{}, "SnapshotDigest", ComputeResourceSnapshotDigest)
}

// TestAdmissionProofGateProducerToCustodyMapping 动态比较 producer 与真实 custody request 的逐字段映射。
func TestAdmissionProofGateProducerToCustodyMapping(t *testing.T) {
	config := Config{}
	batch := batchDigestInput{}
	fillAdmissionProofStruct(t, &config)
	fillAdmissionProofStruct(t, &batch)
	record := admissionProofMappingRecord(batch)
	request, err := buildCustodyRequest(
		config, record, batch.AuthorizationDigest, batch.IdentityFenceDigest,
		batch.ContentSnapshotDigest, batch.ResourceSnapshotDigest,
	)
	if err != nil {
		t.Fatalf("buildCustodyRequest() error = %v", err)
	}
	producers := admissionProofJSONValues(t, config)
	for name, value := range admissionProofJSONValues(t, batch) {
		producers[name] = value
	}
	assertAdmissionProofCustodyMapping(t, producers, admissionProofJSONValues(t, request))
}

// TestAdmissionProofPublicFieldsExcludeAuthorityPayload 禁止 proof wire 面暴露授权或明文 effect 字段。
func TestAdmissionProofPublicFieldsExcludeAuthorityPayload(t *testing.T) {
	for path, file := range admissionProofProductionFiles(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			field, ok := node.(*ast.Field)
			if !ok {
				return true
			}
			for _, name := range admissionProofPublicFieldNames(field) {
				if forbidden := forbiddenAdmissionProofPublicField(name); forbidden != "" {
					t.Fatalf("production source %s exposes public field %q containing %q", filepath.Base(path), name, forbidden)
				}
			}
			return true
		})
	}
}

// TestAdmissionProofProductionHasNoEffectDependencies 锁定 proof gate 为无 DB/FS/Fx/provider/launcher/custody-client effect 的纯组合层。
func TestAdmissionProofProductionHasNoEffectDependencies(t *testing.T) {
	for path, file := range admissionProofProductionFiles(t) {
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", path, err)
			}
			if reason := forbiddenAdmissionProofImport(imported); reason != "" {
				t.Fatalf("production source %s imports forbidden %s dependency %q", filepath.Base(path), reason, imported)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if reason := forbiddenAdmissionProofNode(node); reason != "" {
				t.Fatalf("production source %s contains forbidden %s", filepath.Base(path), reason)
			}
			return true
		})
	}
}

func admissionProofJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "" || name == "-" {
			t.Fatalf("%s.%s has no public enumerable JSON field", typ.Name(), field.Name)
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s has duplicate JSON field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func forbiddenContentResolveField(name string) bool {
	switch name {
	case "caller_principal_ref", "scope_ref", "source_fence_ref":
		return false
	}
	return strings.Contains(name, "ref") || strings.HasPrefix(name, "content_") || strings.HasPrefix(name, "object_") ||
		strings.Contains(name, "ticket") || strings.Contains(name, "reservation") ||
		strings.Contains(name, "policy")
}

func assertAdmissionProofDigestCoverage[T any](
	t *testing.T,
	base T,
	selfDigest string,
	digest func(T) (string, error),
) {
	t.Helper()
	baseDigest, err := digest(base)
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(base)
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		t.Run(typ.Name()+"."+field.Name, func(t *testing.T) {
			mutated := mutateAdmissionProofField(t, base, field)
			got, digestErr := digest(mutated)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if field.Name == selfDigest && got != baseDigest {
				t.Fatalf("%s self-digest mutation changed digest", typ.Name())
			}
			if field.Name != selfDigest && got == baseDigest {
				t.Fatalf("%s.%s does not affect snapshot digest", typ.Name(), field.Name)
			}
		})
	}
}

func mutateAdmissionProofField[T any](t *testing.T, base T, field reflect.StructField) T {
	t.Helper()
	mutated := base
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	switch value.Kind() {
	case reflect.String:
		value.SetString(field.Name + "-one-hot")
	case reflect.Int64:
		value.SetInt(1)
	case reflect.Struct:
		if value.Type() != reflect.TypeFor[time.Time]() {
			t.Fatalf("%s has unsupported struct type %s", field.Name, value.Type())
		}
		value.Set(reflect.ValueOf(time.Unix(1, 0).UTC()))
	default:
		t.Fatalf("%s has unsupported mutation kind %s", field.Name, value.Kind())
	}
	return mutated
}

func fillAdmissionProofStruct(t *testing.T, target any) {
	t.Helper()
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		t.Fatalf("fill target must be a struct pointer, got %T", target)
	}
	value = value.Elem()
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		fieldValue := value.Field(index)
		switch fieldValue.Kind() {
		case reflect.String:
			fieldValue.SetString(field.Name + "-producer")
		case reflect.Int64:
			fieldValue.SetInt(int64(index + 1))
		default:
			t.Fatalf("%s.%s has unsupported producer kind %s", value.Type(), field.Name, fieldValue.Kind())
		}
	}
}

func admissionProofMappingRecord(batch batchDigestInput) orchestrationcommand.CompleteIntentRecord {
	return orchestrationcommand.CompleteIntentRecord{
		Command: orchestrationcommand.Record{
			Intent: orchestrationcommand.Intent{
				CommandID: batch.CommandID, IdempotencyKeyDigest: batch.IdempotencyKeyDigest,
			},
			CommandVersion: batch.CommandVersion,
		},
		Profile: orchestrationcommand.SendMessageProfile{CompleteIntentDigest: batch.CommandProfileDigest},
	}
}

func admissionProofJSONValues(t *testing.T, source any) map[string]any {
	t.Helper()
	value := reflect.ValueOf(source)
	fields := admissionProofJSONFields(t, value.Type())
	values := make(map[string]any, len(fields))
	for name, field := range fields {
		values[name] = value.FieldByIndex(field.Index).Interface()
	}
	return values
}

func assertAdmissionProofCustodyMapping(t *testing.T, producers, request map[string]any) {
	t.Helper()
	for name, source := range producers {
		destination, ok := request[name]
		if !ok {
			t.Fatalf("producer JSON field %q has no custody request consumer", name)
		}
		if !reflect.DeepEqual(source, destination) {
			t.Fatalf("producer %q maps to %#v, want %#v", name, destination, source)
		}
	}
	derived := map[string]string{
		"schema_version": "fixed custody request discriminator",
		"operation_id":   "derived from command identity and profile digest",
		"batch_digest":   "derived from the complete producer batch",
		"request_digest": "derived self-integrity digest",
	}
	for name := range request {
		if _, mapped := producers[name]; !mapped && strings.TrimSpace(derived[name]) == "" {
			t.Fatalf("custody request field %q has no producer mapping or reason", name)
		}
	}
	for name, reason := range derived {
		if _, exists := request[name]; !exists || strings.TrimSpace(reason) == "" {
			t.Fatalf("custody derived-field registry is stale at %q", name)
		}
	}
}

func admissionProofPublicFieldNames(field *ast.Field) []string {
	var names []string
	for _, name := range field.Names {
		if ast.IsExported(name.Name) {
			names = append(names, name.Name)
		}
	}
	if field.Tag != nil {
		names = append(names, field.Tag.Value)
	}
	return names
}

func forbiddenAdmissionProofPublicField(name string) string {
	lower := strings.ToLower(name)
	for _, forbidden := range []string{"accepted", "dispatch", "provider", "permit", "token", "body", "plaintext"} {
		if strings.Contains(lower, forbidden) {
			return forbidden
		}
	}
	return ""
}

func admissionProofProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate admissionproof package")
	}
	directory := filepath.Dir(current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read admissionproof package: %v", err)
	}
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors)
		if parseErr != nil {
			t.Fatalf("parse admissionproof source %s: %v", entry.Name(), parseErr)
		}
		files[path] = file
	}
	if len(files) == 0 {
		t.Fatal("admissionproof production files are missing")
	}
	return files
}

func forbiddenAdmissionProofImport(imported string) string {
	lower := strings.ToLower(imported)
	for _, item := range []struct{ token, reason string }{
		{"database/sql", "database/sql"}, {"/store/sqlc", "database/sqlc"},
		{"io/fs", "filesystem"}, {"path/filepath", "filesystem"},
		{"go.uber.org/fx", "Fx"}, {"/tools", "tools"},
		{"/provider", "provider"}, {"launcher", "launcher"},
	} {
		if strings.Contains(lower, item.token) {
			return item.reason
		}
	}
	if lower == "os" || lower == "io/ioutil" {
		return "filesystem"
	}
	return ""
}

func forbiddenAdmissionProofNode(node ast.Node) string {
	if identifier, ok := node.(*ast.Ident); ok {
		switch strings.ToLower(identifier.Name) {
		case "db", "sql", "sqlc", "fs", "filesystem", "filepath", "fx", "tools", "provider", "launcher":
			return "effect identifier " + strconv.Quote(identifier.Name)
		}
	}
	selector, ok := node.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if admissionProofIdentName(selector.X) == "recoverycustody" && selector.Sel.Name == "Client" {
		return "recoverycustody.Client dependency"
	}
	if selector.Sel.Name == "Submit" || selector.Sel.Name == "Query" {
		return "custody client call " + strconv.Quote(selector.Sel.Name)
	}
	return ""
}

func admissionProofIdentName(expression ast.Expr) string {
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}
