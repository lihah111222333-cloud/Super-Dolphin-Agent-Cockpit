package managedlaunch

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

func TestManagedLaunchJSONSchemasAreExact(t *testing.T) {
	tests := []struct {
		typeOf reflect.Type
		fields []string
	}{
		{reflect.TypeFor[OperationKey](), []string{
			"schema_version", "command_operation_id", "command_operation_digest", "attempt_operation_id",
			"attempt_operation_digest", "idempotency_key_digest", "bootstrap_request_digest",
		}},
		{reflect.TypeFor[BootstrapRequest](), []string{
			"schema_version", "command_operation_id", "command_operation_digest", "attempt_operation_id",
			"attempt_operation_digest", "agent_id", "agent_digest", "provider_id", "provider_digest",
			"model_digest", "cwd_digest", "reservation_id", "reservation_generation", "source_fence_ref",
			"source_fence_digest", "target_fence_ref", "target_fence_digest", "credential_lease_id",
			"credential_lease_digest", "idempotency_key_digest", "bootstrap_request_digest",
		}},
		{reflect.TypeFor[BootstrapResult](), []string{
			"schema_version", "operation", "status", "opaque_handle", "provider_thread_identity",
			"provider_session_identity", "runtime_epoch", "external_epoch", "process_generation",
			"active_process_receipt_ref", "active_process_receipt_digest", "runtime_owner_id",
			"runtime_owner_generation", "executable_digest", "argv_digest", "env_digest", "cwd_digest", "result_digest",
		}},
		{reflect.TypeFor[CommitRequest](), []string{
			"schema_version", "operation", "prepared_request_digest", "prepared_result_digest", "active_current_binding_digest",
			"active_current_binding_version", "active_current_binding_incarnation_id",
			"activated_credential_digest", "commit_operation_digest",
		}},
		{reflect.TypeFor[AbortRequest](), []string{
			"schema_version", "operation", "prepared_request_digest", "prepared_result_digest", "cleanup_operation_id",
			"cleanup_operation_digest", "cleanup_evidence_ref", "cleanup_evidence_digest", "abort_operation_digest",
		}},
		{reflect.TypeFor[CommitResult](), []string{
			"schema_version", "operation", "prepared_request_digest", "prepared_result_digest", "active_current_binding_digest",
			"active_current_binding_version", "active_current_binding_incarnation_id",
			"activated_credential_digest", "commit_operation_digest",
		}},
		{reflect.TypeFor[AbortResult](), []string{
			"schema_version", "operation", "prepared_request_digest", "prepared_result_digest", "cleanup_operation_id",
			"cleanup_operation_digest", "cleanup_evidence_ref", "cleanup_evidence_digest", "abort_operation_digest",
		}},
		{reflect.TypeFor[OutcomeUnknownResult](), []string{
			"schema_version", "operation", "reason_code", "evidence_ref", "evidence_digest",
		}},
		{reflect.TypeFor[KnownOutcome](), []string{
			"schema_version", "operation", "status", "prepared", "committed", "aborted",
		}},
		{reflect.TypeFor[QueryOutcome](), []string{
			"schema_version", "operation", "status", "prepared", "committed", "aborted", "outcome_unknown",
		}},
	}
	for _, test := range tests {
		t.Run(test.typeOf.Name(), func(t *testing.T) {
			got := sortedManagedLaunchFieldNames(t, test.typeOf)
			want := append([]string(nil), test.fields...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON fields = %v, want exact set %v", got, want)
			}
		})
	}
}

func TestManagedLaunchOutcomeJSONPresenceIsOneHot(t *testing.T) {
	prepared := bootstrapResultFixture(t)
	outcome := QueryOutcome{
		SchemaVersion: SchemaVersion, Operation: prepared.Operation, Status: StatusPrepared, Prepared: &prepared,
	}
	encoded, err := json.Marshal(outcome)
	assertNoError(t, err)
	var object map[string]json.RawMessage
	assertNoError(t, json.Unmarshal(encoded, &object))
	for _, absent := range []string{"committed", "aborted", "outcome_unknown"} {
		if _, exists := object[absent]; exists {
			t.Fatalf("prepared outcome JSON unexpectedly contains %q", absent)
		}
	}
	if _, exists := object["prepared"]; !exists {
		t.Fatal("prepared outcome JSON omits prepared branch")
	}
}

func TestManagedLaunchPublicDTOsExcludeEffectPayloadAndPID(t *testing.T) {
	for _, typeOf := range []reflect.Type{
		reflect.TypeFor[OperationKey](), reflect.TypeFor[BootstrapRequest](), reflect.TypeFor[BootstrapResult](),
		reflect.TypeFor[CommitRequest](), reflect.TypeFor[AbortRequest](), reflect.TypeFor[CommitResult](),
		reflect.TypeFor[AbortResult](), reflect.TypeFor[OutcomeUnknownResult](), reflect.TypeFor[KnownOutcome](),
		reflect.TypeFor[QueryOutcome](),
	} {
		for _, path := range managedLaunchRecursiveJSONPaths(t, typeOf, nil) {
			if reason := forbiddenManagedLaunchJSONPath(path); reason != "" {
				t.Fatalf("%s exposes forbidden %s field %q", typeOf.Name(), reason, path)
			}
		}
	}
}

func TestManagedLaunchProductionHasNoImplementationDependencies(t *testing.T) {
	for path, file := range managedLaunchProductionFiles(t) {
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", path, err)
			}
			if reason := forbiddenManagedLaunchImport(imported); reason != "" {
				t.Fatalf("production source %s imports forbidden %s dependency %q", filepath.Base(path), reason, imported)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			function, isFunction := node.(*ast.FuncDecl)
			if isFunction && function.Recv != nil && isManagedLaunchBackendMethod(function.Name.Name) {
				t.Fatalf("production source %s implements forbidden Backend method %q", filepath.Base(path), function.Name.Name)
			}
			identifier, ok := node.(*ast.Ident)
			if ok && forbiddenManagedLaunchIdentifier(identifier.Name) {
				t.Fatalf("production source %s contains forbidden effect identifier %q", filepath.Base(path), identifier.Name)
			}
			return true
		})
	}
}

func TestManagedLaunchRejectsSiblingManagedRuntimeImport(t *testing.T) {
	forbidden := []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime",
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime/contract",
	}
	for _, imported := range forbidden {
		if reason := forbiddenManagedLaunchImport(imported); reason == "" {
			t.Fatalf("forbidden sibling managedruntime import %q was not rejected", imported)
		}
	}
}

func isManagedLaunchBackendMethod(name string) bool {
	switch name {
	case "Bootstrap", "CommitBootstrap", "AbortBootstrap", "QueryOperation":
		return true
	default:
		return false
	}
}

func managedLaunchJSONFields(t *testing.T, typeOf reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "" || name == "-" {
			t.Fatalf("%s.%s is not an enumerable public JSON field", typeOf.Name(), field.Name)
		}
		if _, duplicate := fields[name]; duplicate {
			t.Fatalf("%s has duplicate JSON field %q", typeOf.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func sortedManagedLaunchFieldNames(t *testing.T, typeOf reflect.Type) []string {
	t.Helper()
	fields := managedLaunchJSONFields(t, typeOf)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func managedLaunchRecursiveJSONPaths(t *testing.T, typeOf reflect.Type, stack map[reflect.Type]bool) []string {
	t.Helper()
	if stack == nil {
		stack = make(map[reflect.Type]bool)
	}
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if typeOf.Kind() != reflect.Struct || stack[typeOf] {
		return nil
	}
	stack[typeOf] = true
	defer delete(stack, typeOf)
	var paths []string
	for name, field := range managedLaunchJSONFields(t, typeOf) {
		paths = append(paths, name)
		for _, child := range managedLaunchRecursiveJSONPaths(t, field.Type, stack) {
			paths = append(paths, name+"."+child)
		}
	}
	return paths
}

func forbiddenManagedLaunchJSONPath(path string) string {
	lower := strings.ToLower(path)
	for _, forbidden := range []struct{ token, reason string }{
		{"submit_turn", "turn submission"}, {"initial_prompt", "initial prompt"}, {"prompt", "prompt payload"},
		{"body", "body payload"}, {"plaintext", "plaintext payload"}, {"credential_bytes", "credential bytes"},
		{"raw_token", "raw token"}, {"access_token", "access token"}, {"refresh_token", "refresh token"},
		{"process_id", "PID-only identity"}, {"_pid", "PID-only identity"},
	} {
		if strings.Contains(lower, forbidden.token) {
			return forbidden.reason
		}
	}
	return ""
}

func managedLaunchProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve managedlaunch package directory")
	}
	directory := filepath.Dir(currentFile)
	entries, err := os.ReadDir(directory)
	assertNoError(t, err)
	files := make(map[string]*ast.File)
	set := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, parseErr := parser.ParseFile(set, path, nil, parser.AllErrors)
		if parseErr != nil {
			t.Fatalf("parse production source %s: %v", path, parseErr)
		}
		files[path] = file
	}
	if len(files) == 0 {
		t.Fatal("managedlaunch production source set is empty")
	}
	return files
}

func forbiddenManagedLaunchImport(imported string) string {
	for _, forbidden := range []struct{ token, reason string }{
		{"go.uber.org/fx", "Fx"}, {"/store", "store"}, {"/provider", "provider"},
		{"/execution", "execution"}, {"launcher", "launcher"}, {"os/exec", "process execution"},
		{"syscall", "process control"}, {"net", "network"}, {"/standalone/managedruntime", "sibling managedruntime"},
	} {
		if imported == forbidden.token || strings.Contains(imported, forbidden.token) {
			return forbidden.reason
		}
	}
	return ""
}

func forbiddenManagedLaunchIdentifier(name string) bool {
	switch name {
	case "SubmitTurn", "InitialPrompt", "Prompt", "Body", "Plaintext", "CredentialBytes",
		"RawToken", "AccessToken", "RefreshToken", "PID", "ProcessID":
		return true
	default:
		return false
	}
}
