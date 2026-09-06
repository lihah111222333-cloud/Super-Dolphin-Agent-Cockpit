package managedruntime

import (
	"encoding/json"
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
)

func TestManagedRuntimeJSONFieldRegistry(t *testing.T) {
	registrations := map[reflect.Type][]string{
		reflect.TypeFor[PrepareRequest](): {
			"schema_version", "operation_id", "bootstrap_operation_id",
			"bootstrap_operation_digest",
			"provider_digest", "executable_digest", "working_directory_digest", "argument_vector_digest",
			"environment_digest", "expected_runtime_writer_epoch", "request_digest",
		},
		reflect.TypeFor[StableProcessIdentity](): {
			"pid", "process_start_token", "executable_identity", "executable_digest",
		},
		reflect.TypeFor[PreparedReceipt](): {
			"schema_version", "operation_id", "request_digest", "state", "opaque_handle_ref", "opaque_handle_digest",
			"process", "provider_thread_id", "provider_session_id", "external_epoch", "runtime_owner_id",
			"runtime_generation", "process_generation", "runtime_writer_epoch", "receipt_ref", "receipt_digest",
		},
		reflect.TypeFor[CommitRequest](): {
			"schema_version", "operation_id", "prepared_operation_id", "prepared_request_digest",
			"expected_prepared_receipt_ref", "expected_prepared_receipt_digest", "active_binding_digest",
			"credential_digest", "request_digest",
		},
		reflect.TypeFor[AbortRequest](): {
			"schema_version", "operation_id", "prepared_operation_id", "prepared_request_digest",
			"expected_prepared_receipt_ref", "expected_prepared_receipt_digest", "cleanup_evidence_ref",
			"cleanup_evidence_digest", "request_digest",
		},
		reflect.TypeFor[ClosedReceipt](): {
			"schema_version", "prepared_operation_id", "prepared_request_digest", "operation_id", "operation_digest",
			"state", "prepared_receipt_ref", "prepared_receipt_digest", "closure_evidence_ref",
			"closure_evidence_digest", "receipt_ref", "receipt_digest",
		},
		reflect.TypeFor[QueryRequest](): {
			"schema_version", "operation_id", "operation_digest",
		},
		reflect.TypeFor[QueryResult](): {
			"schema_version", "operation_id", "operation_digest", "state", "prepared_receipt_ref",
			"prepared_receipt_digest", "closed_receipt_ref", "closed_receipt_digest", "uncertainty_receipt_ref",
			"uncertainty_digest", "result_digest",
		},
		reflect.TypeFor[DependencyEvidence](): {
			"ref", "digest",
		},
		reflect.TypeFor[ProductionPrerequisites](): {
			"schema_version", "real_helper", "reconnectable_endpoint", "durable_store", "signature_verifier",
		},
	}
	for valueType, registered := range registrations {
		assertExactStringSet(t, valueType.Name()+" JSON fields", reflectedJSONFields(t, valueType), registered)
	}
}

func TestManagedRuntimeJSONRoundTrip(t *testing.T) {
	prepare := validPrepareRequest(t, "json")
	prepared := validPreparedReceipt(t, prepare, "json")
	commit := validCommitRequest(t, prepared, "json")
	abort := validAbortRequest(t, prepared, "json")
	closed := validClosedReceipt(t, prepared, commit.OperationID, commit.RequestDigest, StateCommitted, "json")
	query := mustFinalizeQueryResult(t, QueryResult{
		SchemaVersion: SchemaVersion, OperationID: prepare.OperationID, OperationDigest: prepare.RequestDigest,
		State: StateCommitted, PreparedReceiptRef: prepared.ReceiptRef, PreparedReceiptDigest: prepared.ReceiptDigest,
		ClosedReceiptRef: closed.ReceiptRef, ClosedReceiptDigest: closed.ReceiptDigest,
	})
	for _, value := range []any{prepare, prepared, commit, abort, closed, QueryRequest{
		SchemaVersion: SchemaVersion, OperationID: prepare.OperationID, OperationDigest: prepare.RequestDigest,
	}, query, validProductionPrerequisites()} {
		assertJSONRoundTrip(t, value)
	}
}

func TestManagedRuntimeContractRejectsRawOrAuthorizingFields(t *testing.T) {
	forbiddenGoNames := stringSet("Prompt", "Body", "RawEnv", "Argv", "Token", "Credential", "ProcessHandle", "IsRunning")
	forbiddenJSONNames := stringSet("prompt", "body", "raw_env", "argv", "token", "credential", "process_handle", "is_running")
	for _, valueType := range managedRuntimeDTOs() {
		for index := range valueType.NumField() {
			field := valueType.Field(index)
			jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
			if forbiddenGoNames[field.Name] || forbiddenJSONNames[jsonName] {
				t.Errorf("%s contains forbidden raw/authorizing field %s (%s)", valueType.Name(), field.Name, jsonName)
			}
			switch field.Type.Kind() {
			case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface:
				t.Errorf("%s.%s can carry unbounded or opaque raw values", valueType.Name(), field.Name)
			}
		}
	}
}

func TestManagedRuntimeProductionImportGuard(t *testing.T) {
	allowed := stringSet("context", "crypto/sha256", "encoding/hex", "encoding/json", "errors", "fmt", "reflect", "runtime", "strings", "sync", "unicode", "unicode/utf8",
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry")
	for _, path := range managedRuntimeProductionFiles(t) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports from %s: %v", path, err)
		}
		for _, imported := range file.Imports {
			name, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import %s: %v", imported.Path.Value, unquoteErr)
			}
			if !allowed[name] {
				t.Errorf("production contract imports forbidden implementation package %q", name)
			}
		}
	}
}

func reflectedJSONFields(t *testing.T, valueType reflect.Type) []string {
	t.Helper()
	fields := make([]string, 0, valueType.NumField())
	for index := range valueType.NumField() {
		field := valueType.Field(index)
		tag := field.Tag.Get("json")
		if tag == "" || strings.Contains(tag, ",") {
			t.Fatalf("%s.%s must have one exact non-omitempty JSON tag", valueType.Name(), field.Name)
		}
		fields = append(fields, tag)
	}
	sort.Strings(fields)
	return fields
}

func managedRuntimeDTOs() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[PrepareRequest](), reflect.TypeFor[StableProcessIdentity](), reflect.TypeFor[PreparedReceipt](),
		reflect.TypeFor[CommitRequest](), reflect.TypeFor[AbortRequest](), reflect.TypeFor[ClosedReceipt](),
		reflect.TypeFor[QueryRequest](), reflect.TypeFor[QueryResult](), reflect.TypeFor[DependencyEvidence](),
		reflect.TypeFor[ProductionPrerequisites](),
	}
}

func managedRuntimeProductionFiles(t *testing.T) []string {
	t.Helper()
	_, testPath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve managedruntime test path")
	}
	directory := filepath.Dir(testPath)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read managedruntime directory: %v", err)
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	if len(paths) == 0 {
		t.Fatal("managedruntime production files are missing")
	}
	sort.Strings(paths)
	return paths
}

func assertJSONRoundTrip(t *testing.T, want any) {
	t.Helper()
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal %T: %v", want, err)
	}
	got := reflect.New(reflect.TypeOf(want))
	if err := json.Unmarshal(encoded, got.Interface()); err != nil {
		t.Fatalf("unmarshal %T: %v", want, err)
	}
	if !reflect.DeepEqual(got.Elem().Interface(), want) {
		t.Fatalf("JSON roundtrip %T = %+v, want %+v", want, got.Elem().Interface(), want)
	}
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func assertExactStringSet(t *testing.T, name string, produced, registered []string) {
	t.Helper()
	sort.Strings(registered)
	missing := stringDifference(produced, registered)
	stale := stringDifference(registered, produced)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s drift: missing=%v stale=%v", name, missing, stale)
	}
}

func stringDifference(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	difference := make([]string, 0)
	for _, value := range left {
		if _, exists := rightSet[value]; !exists {
			difference = append(difference, value)
		}
	}
	return difference
}

var _ ast.Node = (*ast.File)(nil)
