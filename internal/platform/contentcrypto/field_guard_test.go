package contentcrypto

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestContentCryptoJSONFieldRegistriesMatchProductionTypes(t *testing.T) {
	registries := []struct {
		name      string
		producer  reflect.Type
		consumers map[string]string
	}{
		{"SealRequest", reflect.TypeFor[SealRequest](), map[string]string{
			"operation_id": "validate,digest,key-create,payload", "ticket_id": "validate,digest",
			"object_id": "validate,digest,key-create", "object_version": "validate,digest,key-create",
			"source_fence_ref": "validate,digest", "media_type": "validate,digest",
			"aad_profile": "validate,digest", "content_intent_digest": "validate,digest",
		}},
		{"SealedPayload", reflect.TypeFor[SealedPayload](), map[string]string{
			"operation_id": "request", "algorithm": "key-metadata",
			"key_wrap_profile": "key-metadata", "key_handle_ref": "key-metadata", "key_receipt_ref": "key-metadata",
			"key_receipt_digest": "key-metadata",
			"nonce":              "random", "aad_digest": "aad-hash",
			"ciphertext_hash": "ciphertext-hash", "plaintext_size": "request-size", "ciphertext_size": "sealed-size",
		}},
		{"KeyMetadata", reflect.TypeFor[KeyMetadata](), map[string]string{
			"algorithm": "sealed.algorithm", "key_wrap_profile": "sealed.key_wrap_profile",
			"key_handle_ref": "sealed.key_handle_ref", "key_receipt_ref": "sealed.key_receipt_ref",
			"key_receipt_digest": "sealed.key_receipt_digest", "nonce": "sealed.nonce",
		}},
		{"KeyCreateRequest", reflect.TypeFor[KeyCreateRequest](), map[string]string{
			"operation_id": "seal.operation_id", "object_id": "seal.object_id",
			"object_version": "seal.object_version", "algorithm": "aes-256-gcm",
		}},
	}
	for _, registry := range registries {
		t.Run(registry.name, func(t *testing.T) {
			fields := contentCryptoJSONFields(t, registry.producer)
			missing, stale := contentCryptoRegistryDrift(fields, registry.consumers)
			if len(missing) > 0 || len(stale) > 0 {
				t.Fatalf("%s JSON field registry drifted: missing=%v stale=%v", registry.name, missing, stale)
			}
		})
	}
}

func TestContentCryptoSecretFieldsNeverExposeRawMaterial(t *testing.T) {
	requestType := reflect.TypeFor[SealRequest]()
	excluded := make(map[string]bool)
	for field := range requestType.Fields() {
		if field.Tag.Get("json") == "-" {
			excluded[field.Name] = true
		}
	}
	if !reflect.DeepEqual(excluded, map[string]bool{"Plaintext": true, "AAD": true}) {
		t.Fatalf("SealRequest secret JSON exclusions drifted: %v", excluded)
	}
	assertJSONExclusions(t, reflect.TypeFor[SealedPayload](), map[string]bool{"RequestDigest": true, "Ciphertext": true})
	assertJSONExclusions(t, reflect.TypeFor[KeyCreateRequest](), map[string]bool{"RequestDigest": true})
	for _, typ := range []reflect.Type{requestType, reflect.TypeFor[SealedPayload](), reflect.TypeFor[KeyMetadata](), reflect.TypeFor[KeyCreateRequest]()} {
		assertNoRawMaterialJSONField(t, typ)
	}
	assertLeaseRawKeyIsPrivate(t)
	request := validSealRequestFixture(t, []byte("plaintext-secret-sentinel"))
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal(SealRequest) error = %v", err)
	}
	if bytesContainAny(encoded, request.Plaintext, request.AAD) {
		t.Fatalf("SealRequest JSON exposed plaintext or AAD: %s", encoded)
	}
}

func assertJSONExclusions(t *testing.T, typ reflect.Type, want map[string]bool) {
	t.Helper()
	got := make(map[string]bool)
	for field := range typ.Fields() {
		if field.Tag.Get("json") == "-" {
			got[field.Name] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s JSON exclusions = %v, want %v", typ, got, want)
	}
}

func TestContentCryptoProductionImportsStayInfrastructureNeutral(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not resolve contentcrypto package directory")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("discover contentcrypto production files: files=%v error=%v", files, err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		assertProductionFileImports(t, path)
	}
}

func contentCryptoJSONFields(t *testing.T, producer reflect.Type) map[string]bool {
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

func contentCryptoRegistryDrift(fields map[string]bool, consumers map[string]string) ([]string, []string) {
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

func assertNoRawMaterialJSONField(t *testing.T, typ reflect.Type) {
	t.Helper()
	for field := range typ.Fields() {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name != "-" && rawMaterialName(field.Name+" "+name) {
			t.Fatalf("%s.%s exposes raw secret material as JSON field %q", typ, field.Name, name)
		}
	}
}

func assertLeaseRawKeyIsPrivate(t *testing.T) {
	t.Helper()
	typ := reflect.TypeFor[EphemeralKeyLease]()
	found := false
	for field := range typ.Fields() {
		if !rawMaterialName(field.Name) {
			continue
		}
		found = true
		if field.IsExported() || field.Tag.Get("json") != "" {
			t.Fatalf("%s.%s must remain unexported and without a JSON tag", typ, field.Name)
		}
	}
	if !found {
		t.Fatal("EphemeralKeyLease raw-key field was not found; update the secret-field guard")
	}
}

func rawMaterialName(name string) bool {
	normalized := strings.Map(func(item rune) rune {
		if unicode.IsLetter(item) || unicode.IsDigit(item) {
			return unicode.ToLower(item)
		}
		return -1
	}, name)
	if normalized == "key" || normalized == "dek" || normalized == "plaintext" || normalized == "plaintextbytes" {
		return true
	}
	for _, forbidden := range []string{"rawkey", "keybytes", "secret", "rawdek"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func bytesContainAny(encoded []byte, forbidden ...[]byte) bool {
	text := string(encoded)
	for _, value := range forbidden {
		if len(value) > 0 && strings.Contains(text, string(value)) {
			return true
		}
	}
	return false
}

func assertProductionFileImports(t *testing.T, path string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse production imports %s: %v", path, err)
	}
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", path, err)
		}
		if forbiddenContentCryptoImport(importPath) {
			t.Fatalf("production contentcrypto file %s imports forbidden infrastructure %q", filepath.Base(path), importPath)
		}
	}
}

func forbiddenContentCryptoImport(importPath string) bool {
	for _, forbidden := range []string{"go.uber.org/fx", "database/sql", "io/fs", "os", "path/filepath"} {
		if importPath == forbidden {
			return true
		}
	}
	for _, segment := range strings.Split(importPath, "/") {
		if segment == "store" || segment == "db" || segment == "provider" || segment == "fs" {
			return true
		}
	}
	return false
}
