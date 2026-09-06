package contentobject

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestContentObjectJSONFieldRegistriesMatchProductionTypes(t *testing.T) {
	registries := []struct {
		name      string
		producer  reflect.Type
		consumers map[string]string
	}{
		{"Config", reflect.TypeFor[Config](), map[string]string{
			"state_dir": "private-root", "workspace_root": "overlap-guard", "store_id": "receipt,object-ref",
		}},
		{"PutRequest", reflect.TypeFor[PutRequest](), map[string]string{
			"schema_version": "validate,digest", "operation_id": "validate,digest,receipt", "request_digest": "validate,receipt",
			"object_id": "validate,digest,path,receipt", "object_version": "validate,digest,path,receipt",
			"ciphertext_hash": "validate,digest,receipt", "ciphertext_size": "validate,digest,receipt",
		}},
		{"QueryRequest", reflect.TypeFor[QueryRequest](), map[string]string{
			"schema_version": "validate", "operation_id": "validate,receipt-match", "request_digest": "validate,receipt-match",
			"object_id": "validate,path,receipt-match", "object_version": "validate,path,receipt-match",
		}},
		{"PutReceipt", reflect.TypeFor[PutReceipt](), map[string]string{
			"schema_version": "validate,digest", "store_id": "validate,digest,store-match", "operation_id": "validate,digest,replay",
			"request_digest": "validate,digest,replay", "object_id": "validate,digest,replay", "object_version": "validate,digest,replay",
			"object_ref": "validate,digest,path-match", "receipt_ref": "validate,digest,receipt-lookup", "ciphertext_hash": "validate,digest,payload-match",
			"ciphertext_size": "validate,digest,payload-match", "receipt_digest": "validate", "recorded_at": "validate,digest",
		}},
	}
	for _, registry := range registries {
		t.Run(registry.name, func(t *testing.T) {
			fields := contentObjectJSONFields(t, registry.producer)
			missing, stale := contentObjectRegistryDrift(fields, registry.consumers)
			if len(missing) != 0 || len(stale) != 0 {
				t.Fatalf("%s JSON registry drifted: missing=%v stale=%v", registry.name, missing, stale)
			}
		})
	}
}

func TestContentObjectRequestDigestOneHotFieldBinding(t *testing.T) {
	base := contentObjectRequest(t, "operation-digest", "object-digest", []byte("opaque-digest-ciphertext"))
	mutations := map[string]func(*PutRequest){
		"SchemaVersion":  func(value *PutRequest) { value.SchemaVersion++ },
		"OperationID":    func(value *PutRequest) { value.OperationID += "-other" },
		"ObjectID":       func(value *PutRequest) { value.ObjectID += "-other" },
		"ObjectVersion":  func(value *PutRequest) { value.ObjectVersion++ },
		"CiphertextHash": func(value *PutRequest) { value.CiphertextHash = strings.Repeat("b", 64) },
		"CiphertextSize": func(value *PutRequest) { value.CiphertextSize++ },
	}
	contentObjectAssertMutationRegistry(t, reflect.TypeFor[PutRequest](), mutations, "RequestDigest", "Ciphertext")
	baseDigest, err := ComputePutRequestDigest(base)
	contentObjectNoError(t, err)
	for field, mutate := range mutations {
		t.Run(field, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got, digestErr := ComputePutRequestDigest(changed)
			contentObjectNoError(t, digestErr)
			if got == baseDigest {
				t.Fatalf("ComputePutRequestDigest ignored one-hot mutation of %s", field)
			}
		})
	}
	contentObjectAssertRequestDigestExclusions(t, base, baseDigest)
}

func TestContentObjectReceiptDigestOneHotFieldBinding(t *testing.T) {
	base := PutReceipt{SchemaVersion: 1, StoreID: "store-1", OperationID: "operation-1", RequestDigest: strings.Repeat("a", 64),
		ObjectID: "object-1", ObjectVersion: 1, ObjectRef: "content-object://v1/store/object/1",
		ReceiptRef: "content-object-receipt://v1/store/operation", CiphertextHash: strings.Repeat("b", 64),
		CiphertextSize: 17, RecordedAt: time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)}
	mutations := map[string]func(*PutReceipt){
		"SchemaVersion":  func(value *PutReceipt) { value.SchemaVersion++ },
		"StoreID":        func(value *PutReceipt) { value.StoreID += "-other" },
		"OperationID":    func(value *PutReceipt) { value.OperationID += "-other" },
		"RequestDigest":  func(value *PutReceipt) { value.RequestDigest = strings.Repeat("c", 64) },
		"ObjectID":       func(value *PutReceipt) { value.ObjectID += "-other" },
		"ObjectVersion":  func(value *PutReceipt) { value.ObjectVersion++ },
		"ObjectRef":      func(value *PutReceipt) { value.ObjectRef += "-other" },
		"ReceiptRef":     func(value *PutReceipt) { value.ReceiptRef += "-other" },
		"CiphertextHash": func(value *PutReceipt) { value.CiphertextHash = strings.Repeat("d", 64) },
		"CiphertextSize": func(value *PutReceipt) { value.CiphertextSize++ },
		"RecordedAt":     func(value *PutReceipt) { value.RecordedAt = value.RecordedAt.Add(time.Second) },
	}
	contentObjectAssertMutationRegistry(t, reflect.TypeFor[PutReceipt](), mutations, "ReceiptDigest")
	baseDigest, err := computeReceiptDigest(base)
	contentObjectNoError(t, err)
	for field, mutate := range mutations {
		t.Run(field, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got, digestErr := computeReceiptDigest(changed)
			contentObjectNoError(t, digestErr)
			if got == baseDigest {
				t.Fatalf("computeReceiptDigest ignored one-hot mutation of %s", field)
			}
		})
	}
	base.ReceiptDigest = strings.Repeat("e", 64)
	got, err := computeReceiptDigest(base)
	contentObjectNoError(t, err)
	if got != baseDigest {
		t.Fatal("computeReceiptDigest included its ReceiptDigest output field")
	}
}

func TestContentObjectProductionForbidsSharedFileAndPlaintextRecoveryAPI(t *testing.T) {
	files := contentObjectProductionFiles(t)
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse production file %s: %v", path, err)
		}
		contentObjectAssertImports(t, path, parsed)
		contentObjectAssertDeclarations(t, path, parsed)
	}
	storeType := reflect.TypeFor[Store]()
	methods := make([]string, 0, storeType.NumMethod())
	for index := range storeType.NumMethod() {
		methods = append(methods, storeType.Method(index).Name)
	}
	if !reflect.DeepEqual(methods, []string{"Put", "QueryPut", "Validate"}) {
		t.Fatalf("Store API drifted to forbidden recovery/lifecycle surface: %v", methods)
	}
}

func contentObjectAssertRequestDigestExclusions(t *testing.T, base PutRequest, want string) {
	t.Helper()
	base.RequestDigest = strings.Repeat("c", 64)
	got, err := ComputePutRequestDigest(base)
	contentObjectNoError(t, err)
	if got != want {
		t.Fatal("request digest included its RequestDigest output field")
	}
	base.Ciphertext = []byte("different-untrusted-bytes")
	got, err = ComputePutRequestDigest(base)
	contentObjectNoError(t, err)
	if got != want {
		t.Fatal("request digest directly included ciphertext bytes instead of hash and size")
	}
}

func contentObjectAssertMutationRegistry[T any](t *testing.T, typ reflect.Type, mutations map[string]func(*T), excluded ...string) {
	t.Helper()
	want := make(map[string]bool)
	for field := range typ.Fields() {
		want[field.Name] = true
	}
	for _, field := range excluded {
		delete(want, field)
	}
	got := make(map[string]bool)
	for field := range mutations {
		got[field] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digest mutation registry drifted: got=%v want=%v", contentObjectSortedKeys(got), contentObjectSortedKeys(want))
	}
}

func contentObjectJSONFields(t *testing.T, producer reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool)
	for field := range producer.Fields() {
		name := strings.TrimSpace(strings.SplitN(field.Tag.Get("json"), ",", 2)[0])
		if name == "-" {
			continue
		}
		if name == "" || fields[name] {
			t.Fatalf("%s.%s has missing or duplicate JSON name %q", producer, field.Name, name)
		}
		fields[name] = true
	}
	return fields
}

func contentObjectRegistryDrift(fields map[string]bool, consumers map[string]string) ([]string, []string) {
	var missing, stale []string
	for field := range fields {
		if strings.TrimSpace(consumers[field]) == "" {
			missing = append(missing, field)
		}
	}
	for field := range consumers {
		if !fields[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}

func contentObjectProductionFiles(t *testing.T) []string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not resolve contentobject package")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("discover contentobject production files: %v", err)
	}
	production := files[:0]
	for _, path := range files {
		if !strings.HasSuffix(path, "_test.go") {
			production = append(production, path)
		}
	}
	if len(production) == 0 {
		t.Fatal("no contentobject production files found")
	}
	return production
}

func contentObjectAssertImports(t *testing.T, path string, file *ast.File) {
	t.Helper()
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", path, err)
		}
		if strings.Contains(strings.ToLower(importPath), "sharedfile") {
			t.Fatalf("production file %s imports forbidden sharedfile package %q", filepath.Base(path), importPath)
		}
	}
}

func contentObjectAssertDeclarations(t *testing.T, path string, file *ast.File) {
	t.Helper()
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if ast.IsExported(value.Name.Name) {
				contentObjectAssertAllowedAPIName(t, path, value.Name.Name)
			}
		case *ast.GenDecl:
			contentObjectAssertTypeDeclarations(t, path, value)
		}
	}
}

func contentObjectAssertTypeDeclarations(t *testing.T, path string, declaration *ast.GenDecl) {
	t.Helper()
	for _, spec := range declaration.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		contentObjectAssertAllowedAPIName(t, path, typeSpec.Name.Name)
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range structType.Fields.List {
			contentObjectAssertFieldNames(t, path, field)
		}
	}
}

func contentObjectAssertFieldNames(t *testing.T, path string, field *ast.Field) {
	t.Helper()
	for _, name := range field.Names {
		contentObjectAssertAllowedAPIName(t, path, name.Name)
	}
	if field.Tag == nil {
		return
	}
	tag, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		t.Fatalf("decode struct tag in %s: %v", path, err)
	}
	contentObjectAssertAllowedAPIName(t, path, reflect.StructTag(tag).Get("json"))
}

func contentObjectAssertAllowedAPIName(t *testing.T, path, name string) {
	t.Helper()
	normalized := strings.ToLower(strings.Map(func(item rune) rune {
		if item >= 'a' && item <= 'z' || item >= 'A' && item <= 'Z' {
			return item
		}
		return -1
	}, name))
	for _, forbidden := range []string{"sharedfile", "decrypt", "delete", "plaintext"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("production contentobject file %s exposes forbidden field/API %q", filepath.Base(path), name)
		}
	}
}

func contentObjectSortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
