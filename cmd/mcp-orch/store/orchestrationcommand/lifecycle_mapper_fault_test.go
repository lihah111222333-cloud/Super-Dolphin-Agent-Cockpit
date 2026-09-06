package orchestrationcommand

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestCommandLifecycleMapperOmissionFailsGuard 从真实 mapper 副本删字段，验证同一守卫先 RED 再 GREEN。
func TestCommandLifecycleMapperOmissionFailsGuard(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate lifecycle mapper source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "journal_mapping.go"))
	if err != nil {
		t.Fatal(err)
	}
	const mapping = "UncertainPhase: string(plan.UncertainPhase),"
	if strings.Count(string(source), mapping) != 1 {
		t.Fatal("real UncertainPhase mapper branch must exist exactly once")
	}
	fixture := filepath.Join(t.TempDir(), "journal_mapping.go")
	if err := os.WriteFile(fixture, []byte(strings.Replace(string(source), mapping, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := runLifecycleMapperGuardFixture(t, fixture)
	if err == nil || !strings.Contains(output, "AppendOrchestrationCommandJournalParams.UncertainPhase") {
		t.Fatalf("mapper omission did not produce precise RED: err=%v output=%s", err, output)
	}
	t.Log("RED: removed UncertainPhase from actual appendLifecycleParams source copy")
	if err := os.WriteFile(fixture, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runLifecycleMapperGuardFixture(t, fixture); err != nil {
		t.Fatalf("restored mapper did not return GREEN: %v %s", err, output)
	}
	t.Log("GREEN: restored exact source passed the same mapper guard")
}

// TestCommandLifecycleMapperGuardFixture 仅在隔离子进程中检查指定源码副本，不重写生产文件。
func TestCommandLifecycleMapperGuardFixture(t *testing.T) {
	fixture := os.Getenv("ORCH_LIFECYCLE_MAPPER_FIXTURE")
	if fixture == "" {
		return
	}
	file, err := parser.ParseFile(token.NewFileSet(), fixture, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "appendLifecycleParams" {
			functions[function.Name.Name] = function
		}
	}
	assertCommandIntentMapperFields(t, functions, "appendLifecycleParams", reflect.TypeFor[storedb.AppendOrchestrationCommandJournalParams]())
}

func runLifecycleMapperGuardFixture(t *testing.T, fixture string) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCommandLifecycleMapperGuardFixture$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ORCH_LIFECYCLE_MAPPER_FIXTURE="+fixture)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("lifecycle mapper fixture timeout: %v", ctx.Err())
	}
	return string(output), err
}

// testLifecycleVerifierEnvelopeFields 同时检查真实 verifier 输入值与所有字段的显式生产位置。
func testLifecycleVerifierEnvelopeFields(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-verifier-envelope")
	current := prepareLifecycle(t, fixture, "envelope-prepare")
	request := lifecycleRequest(current, "envelope-accept", TriggerAccept, "", "fixture:envelope")
	applyLifecycle(t, fixture, request)
	fixture.verifier.mu.Lock()
	calls := append([]Verification(nil), fixture.verifier.calls...)
	fixture.verifier.mu.Unlock()
	want := Verification{Intent: fixture.intent, Current: current, Request: request, NextPhase: PhaseAccepted}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("verifier received %v, want exact envelope %v", calls, want)
	}
	assertLifecycleVerificationProducerFields(t)
}

func assertLifecycleVerificationProducerFields(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate coordinator source")
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(currentFile), "coordinator.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		name, ok := literal.Type.(*ast.Ident)
		if ok && name.Name == "Verification" {
			count++
			assertLifecycleVerificationLiteral(t, literal)
		}
		return true
	})
	if count != 1 {
		t.Fatalf("Verification producer count = %d, want sole explicit producer", count)
	}
}

func assertLifecycleVerificationLiteral(t *testing.T, literal *ast.CompositeLit) {
	t.Helper()
	fields := make(map[string]bool)
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			t.Fatal("Verification producer must use explicit keyed fields")
		}
		name, ok := pair.Key.(*ast.Ident)
		if !ok || fields[name.Name] {
			t.Fatal("Verification producer has invalid or duplicate field")
		}
		fields[name.Name] = true
	}
	typ := reflect.TypeFor[Verification]()
	if len(fields) != typ.NumField() {
		t.Fatal("Verification producer field count differs from runtime type")
	}
	for index := 0; index < typ.NumField(); index++ {
		if !fields[typ.Field(index).Name] {
			t.Fatalf("Verification.%s has no explicit producer", typ.Field(index).Name)
		}
	}
}
