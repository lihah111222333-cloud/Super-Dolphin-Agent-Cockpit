package recoverycustody

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecoveryCustodyJSONFieldsRemainEnumerable(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[SubmissionRequest](),
		reflect.TypeFor[SubmissionHandle](),
		reflect.TypeFor[ExternalReceipt](),
		reflect.TypeFor[Outcome](),
	} {
		t.Run(typ.Name(), func(t *testing.T) {
			fields := recoveryCustodyJSONFields(t, typ)
			if len(fields) != typ.NumField() {
				t.Fatalf("%s enumerable JSON fields = %d, want all %d fields", typ.Name(), len(fields), typ.NumField())
			}
		})
	}
}

func TestSubmissionRequestEveryBusinessFieldAffectsDigest(t *testing.T) {
	request := recoveryCustodyRequestFixture(t)
	requestType := reflect.TypeFor[SubmissionRequest]()
	for index := 0; index < requestType.NumField(); index++ {
		field := requestType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			mutated := mutateRecoveryCustodyRequestField(t, request, field)
			got, err := ComputeRequestDigest(mutated)
			if err != nil {
				t.Fatalf("ComputeRequestDigest(%s mutation) error = %v", field.Name, err)
			}
			if field.Name == "RequestDigest" {
				if got != request.RequestDigest {
					t.Fatalf("RequestDigest self-reference exemption changed digest: got %s want %s", got, request.RequestDigest)
				}
				return
			}
			if got == request.RequestDigest {
				t.Fatalf("SubmissionRequest.%s does not affect canonical request digest", field.Name)
			}
		})
	}
}

func TestExternalReceiptEveryClaimAffectsDigest(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	receipt := validReceipt(t, handle, ReceiptAvailability, "receipt-field-guard")
	receiptType := reflect.TypeFor[ExternalReceipt]()
	for index := 0; index < receiptType.NumField(); index++ {
		field := receiptType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			mutated := mutateRecoveryCustodyReceiptField(t, receipt, field)
			got, err := ComputeReceiptDigest(mutated)
			if err != nil {
				t.Fatalf("ComputeReceiptDigest(%s mutation) error = %v", field.Name, err)
			}
			_, exempt := receiptDigestExemptions()[recoveryCustodyJSONFieldName(t, field)]
			if exempt && got != receipt.ReceiptDigest {
				t.Fatalf("excluded ExternalReceipt.%s changed canonical digest", field.Name)
			}
			if !exempt && got == receipt.ReceiptDigest {
				t.Fatalf("ExternalReceipt.%s does not affect canonical receipt digest", field.Name)
			}
		})
	}
}

func TestExternalReceiptDigestPayloadHasReasonedCoverage(t *testing.T) {
	receiptFields := recoveryCustodyJSONFields(t, reflect.TypeFor[ExternalReceipt]())
	payloadFields := recoveryCustodyJSONFields(t, reflect.TypeFor[receiptDigestPayload]())
	exemptions := receiptDigestExemptions()
	for name := range receiptFields {
		if _, mapped := payloadFields[name]; mapped {
			continue
		}
		if strings.TrimSpace(exemptions[name]) == "" {
			t.Fatalf("ExternalReceipt JSON field %q is absent from receipt digest payload without a reason", name)
		}
	}
	for name := range payloadFields {
		if _, exists := receiptFields[name]; !exists {
			t.Fatalf("receipt digest payload field %q has no ExternalReceipt producer", name)
		}
	}
	for name, reason := range exemptions {
		if _, exists := receiptFields[name]; !exists || strings.TrimSpace(reason) == "" {
			t.Fatalf("receipt digest exemption %q is stale or unreasoned", name)
		}
		if _, mapped := payloadFields[name]; mapped {
			t.Fatalf("receipt digest exemption %q is also mapped into canonical payload", name)
		}
	}
}

func receiptDigestExemptions() map[string]string {
	// Quorum/signature proof metadata is security-sensitive and must remain in
	// the canonical payload; only the derived receipt digest is self-exempt.
	return map[string]string{
		"receipt_digest": "derived by recoverycustody ComputeReceiptDigest and excluded to prevent self-reference",
	}
}

func TestSubmissionRequestToHandleMappingHasReasonedCoverage(t *testing.T) {
	request := recoveryCustodyRequestFixture(t)
	handle, err := NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	requestFields := recoveryCustodyJSONFields(t, reflect.TypeFor[SubmissionRequest]())
	handleFields := recoveryCustodyJSONFields(t, reflect.TypeFor[SubmissionHandle]())
	aliases := map[string]struct{ destination, reason string }{}
	requestOnly := map[string]string{
		"command_profile_digest":   "bound transitively by request_digest",
		"authorization_digest":     "bound transitively by request_digest",
		"identity_fence_digest":    "bound transitively by request_digest",
		"content_snapshot_digest":  "bound transitively by request_digest",
		"resource_snapshot_digest": "bound transitively by request_digest",
		"idempotency_key_digest":   "bound transitively by request_digest",
	}
	handleOnly := map[string]string{"handle_digest": "derived self-integrity digest"}
	assertRecoveryCustodyMapping(t, request, handle, requestFields, handleFields, aliases, requestOnly, handleOnly)
}

func TestRecoveryCustodyReceiptAndOutcomeExcludeAuthorityPayloadFields(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[ExternalReceipt](), reflect.TypeFor[Outcome]()} {
		t.Run(typ.Name(), func(t *testing.T) {
			for _, path := range recoveryCustodyRecursiveJSONPaths(t, typ) {
				lower := strings.ToLower(path)
				for _, forbidden := range []string{
					"accepted", "dispatch", "provider", "attempt", "journal", "permit", "token", "body", "plaintext",
				} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("%s JSON path %q contains forbidden custody concept %q", typ.Name(), path, forbidden)
					}
				}
			}
		})
	}
}

func TestRecoveryCustodyProductionHasNoEffectDependencies(t *testing.T) {
	for path, file := range recoveryCustodyProductionFiles(t) {
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", path, err)
			}
			if reason := forbiddenRecoveryCustodyImport(imported); reason != "" {
				t.Fatalf("production source %s imports forbidden %s dependency %q", filepath.Base(path), reason, imported)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && forbiddenRecoveryCustodyIdentifier(identifier.Name) {
				t.Fatalf("production source %s contains forbidden effect identifier %q", filepath.Base(path), identifier.Name)
			}
			return true
		})
	}
}

func recoveryCustodyJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
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

func recoveryCustodyJSONFieldName(t *testing.T, field reflect.StructField) string {
	t.Helper()
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if field.PkgPath != "" || name == "" || name == "-" {
		t.Fatalf("%s has no public enumerable JSON field", field.Name)
	}
	return name
}

func recoveryCustodyRequestFixture(t *testing.T) SubmissionRequest {
	t.Helper()
	request := SubmissionRequest{
		SchemaVersion: 1, OperationID: "operation-field-guard", SecurityDomainID: "security-field-guard",
		AuthorityProfile: "authority-field-guard", CommandID: "command-field-guard", CommandVersion: 1,
		CommandProfileDigest: strings.Repeat("1", 64), AuthorizationDigest: strings.Repeat("2", 64),
		IdentityFenceDigest: strings.Repeat("3", 64), ContentSnapshotDigest: strings.Repeat("4", 64),
		ResourceSnapshotDigest: strings.Repeat("5", 64), BatchDigest: strings.Repeat("6", 64),
		IdempotencyKeyDigest: strings.Repeat("7", 64),
	}
	finalized, err := FinalizeRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest() error = %v", err)
	}
	if err := finalized.Validate(); err != nil {
		t.Fatalf("fixture request validation error = %v", err)
	}
	return finalized
}

func mutateRecoveryCustodyRequestField(t *testing.T, request SubmissionRequest, field reflect.StructField) SubmissionRequest {
	t.Helper()
	mutated := request
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	switch value.Kind() {
	case reflect.String:
		if field.Name == "RequestDigest" {
			value.SetString(strings.Repeat("f", 64))
		} else {
			value.SetString(value.String() + "x")
		}
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("SubmissionRequest.%s has unsupported digest mutation kind %s", field.Name, value.Kind())
	}
	return mutated
}

func mutateRecoveryCustodyReceiptField(t *testing.T, receipt ExternalReceipt, field reflect.StructField) ExternalReceipt {
	t.Helper()
	mutated := receipt
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "x")
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	case reflect.Struct:
		if value.Type() != reflect.TypeFor[time.Time]() {
			t.Fatalf("ExternalReceipt.%s has unsupported digest mutation type %s", field.Name, value.Type())
		}
		value.Set(reflect.ValueOf(value.Interface().(time.Time).Add(time.Second)))
	default:
		t.Fatalf("ExternalReceipt.%s has unsupported digest mutation kind %s", field.Name, value.Kind())
	}
	return mutated
}

func assertRecoveryCustodyMapping(
	t *testing.T,
	request SubmissionRequest,
	handle SubmissionHandle,
	requestFields, handleFields map[string]reflect.StructField,
	aliases map[string]struct{ destination, reason string },
	requestOnly, handleOnly map[string]string,
) {
	t.Helper()
	usedHandleFields := make(map[string]bool, len(handleFields))
	for name, sourceField := range requestFields {
		destination := name
		if alias, ok := aliases[name]; ok {
			if strings.TrimSpace(alias.reason) == "" {
				t.Fatalf("request-to-handle alias %q -> %q has no reason", name, alias.destination)
			}
			destination = alias.destination
		}
		destinationField, mapped := handleFields[destination]
		if !mapped {
			if strings.TrimSpace(requestOnly[name]) == "" {
				t.Fatalf("SubmissionRequest JSON field %q is unmapped without a reason", name)
			}
			continue
		}
		usedHandleFields[destination] = true
		assertRecoveryCustodyMappedValue(t, request, handle, sourceField, destinationField)
	}
	assertRecoveryCustodyHandleOnlyReasons(t, handleFields, usedHandleFields, handleOnly)
	assertRecoveryCustodyReasonRegistries(t, requestFields, handleFields, aliases, requestOnly, handleOnly)
}

func assertRecoveryCustodyMappedValue(
	t *testing.T,
	request SubmissionRequest,
	handle SubmissionHandle,
	sourceField, destinationField reflect.StructField,
) {
	t.Helper()
	source := reflect.ValueOf(request).FieldByIndex(sourceField.Index).Interface()
	destination := reflect.ValueOf(handle).FieldByIndex(destinationField.Index).Interface()
	if !reflect.DeepEqual(source, destination) {
		t.Fatalf("request-to-handle mapping %s -> %s = %#v, want %#v", sourceField.Name, destinationField.Name, destination, source)
	}
}

func assertRecoveryCustodyHandleOnlyReasons(
	t *testing.T,
	handleFields map[string]reflect.StructField,
	used map[string]bool,
	reasons map[string]string,
) {
	t.Helper()
	for name := range handleFields {
		if !used[name] && strings.TrimSpace(reasons[name]) == "" {
			t.Fatalf("SubmissionHandle JSON field %q has no request mapping or reason", name)
		}
	}
}

func assertRecoveryCustodyReasonRegistries(
	t *testing.T,
	requestFields, handleFields map[string]reflect.StructField,
	aliases map[string]struct{ destination, reason string },
	requestOnly, handleOnly map[string]string,
) {
	t.Helper()
	stale := staleRecoveryCustodyAliases(requestFields, handleFields, aliases)
	stale = append(stale, staleRecoveryCustodyReasons("request-only", requestFields, requestOnly)...)
	stale = append(stale, staleRecoveryCustodyReasons("handle-only", handleFields, handleOnly)...)
	if len(stale) != 0 {
		sort.Strings(stale)
		t.Fatalf("request-to-handle reason registry is stale or unreasoned: %v", stale)
	}
}

func staleRecoveryCustodyAliases(
	requestFields, handleFields map[string]reflect.StructField,
	aliases map[string]struct{ destination, reason string },
) []string {
	var stale []string
	for source, alias := range aliases {
		if _, sourceExists := requestFields[source]; !sourceExists || strings.TrimSpace(alias.reason) == "" {
			stale = append(stale, "alias:"+source)
		} else if _, destinationExists := handleFields[alias.destination]; !destinationExists {
			stale = append(stale, "alias:"+source+"->"+alias.destination)
		}
	}
	return stale
}

func staleRecoveryCustodyReasons(prefix string, fields map[string]reflect.StructField, reasons map[string]string) []string {
	var stale []string
	for name, reason := range reasons {
		if _, exists := fields[name]; !exists || strings.TrimSpace(reason) == "" {
			stale = append(stale, prefix+":"+name)
		}
	}
	return stale
}

func recoveryCustodyRecursiveJSONPaths(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	paths := make(map[string]bool)
	collectRecoveryCustodyJSONPaths(t, typ, "", paths, make(map[reflect.Type]bool))
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func collectRecoveryCustodyJSONPaths(
	t *testing.T,
	typ reflect.Type,
	prefix string,
	paths map[string]bool,
	active map[reflect.Type]bool,
) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeFor[time.Time]() || active[typ] {
		return
	}
	active[typ] = true
	defer delete(active, typ)
	for name, field := range recoveryCustodyJSONFields(t, typ) {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		paths[path] = true
		collectRecoveryCustodyJSONPaths(t, field.Type, path, paths, active)
	}
}

func recoveryCustodyProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate recovery custody package")
	}
	directory := filepath.Dir(current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read recovery custody package: %v", err)
	}
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors)
		if parseErr != nil {
			t.Fatalf("parse recovery custody source %s: %v", entry.Name(), parseErr)
		}
		files[path] = file
	}
	if len(files) == 0 {
		t.Fatal("recovery custody production files are missing")
	}
	return files
}

func forbiddenRecoveryCustodyImport(imported string) string {
	lower := strings.ToLower(imported)
	for _, item := range []struct{ token, reason string }{
		{"database/sql", "database/sql"}, {"/store/sqlc", "database/sqlc"},
		{"io/fs", "filesystem"}, {"path/filepath", "filesystem"},
		{"heteroci", "heteroci"}, {"appupdaterecovery", "app update recovery"},
		{"mcpcontrol", "MCP control"}, {"/internal/provider", "provider"}, {"go.uber.org/fx", "Fx"},
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

func forbiddenRecoveryCustodyIdentifier(name string) bool {
	switch strings.ToLower(name) {
	case "db", "sql", "sqlc", "filesystem", "file", "filepath", "heteroci", "appupdaterecovery", "mcpcontrol", "provider", "fx":
		return true
	default:
		return false
	}
}
