package orchestrationcommand

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCommandIntentFieldGuardRejectsRealMapperOmission 从真实读写 mapper 注入缺字段，子进程必须 RED，恢复后 GREEN。
func TestCommandIntentFieldGuardRejectsRealMapperOmission(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate command intent source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mapping string
	}{
		{name: "write", mapping: "CallerPrincipalRef: intent.CallerPrincipalRef,"},
		{name: "read", mapping: "CallerPrincipalRef: row.CallerPrincipalRef,"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertCommandIntentMapperOmission(t, source, tc.mapping) })
	}
}

func assertCommandIntentMapperOmission(t *testing.T, source []byte, mapping string) {
	t.Helper()
	if strings.Count(string(source), mapping) != 1 {
		t.Fatalf("real caller principal mapper branch %q must exist exactly once", mapping)
	}
	fixture := filepath.Join(t.TempDir(), "store.go")
	if err := os.WriteFile(fixture, []byte(strings.Replace(string(source), mapping, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := runCommandIntentFieldGuardFixture(t, fixture)
	if err == nil || !strings.Contains(output, "Intent.CallerPrincipalRef") {
		t.Fatalf("mapper omission did not produce precise RED: err=%v output=%s", err, output)
	}
	t.Log("RED: omitted real CallerPrincipalRef mapper; field guard reported Intent.CallerPrincipalRef")
	if err := os.WriteFile(fixture, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runCommandIntentFieldGuardFixture(t, fixture); err != nil {
		t.Fatalf("restored mapper did not return GREEN: %v %s", err, output)
	}
	t.Log("GREEN: restored exact source passed the same field guard")
}

// TestCommandIntentFieldGuardFixtureHelper 只供上方隔离子进程执行，不修改生产源码。
func TestCommandIntentFieldGuardFixtureHelper(t *testing.T) {
	fixture := os.Getenv("ORCH_INTENT_FIELD_GUARD_FIXTURE")
	if fixture == "" {
		return
	}
	functions := parseCommandIntentProductionFunctions(t, fixture)
	assertCommandIntentMapperFields(t, functions, "intentParams", reflect.TypeFor[Intent]())
	assertCommandIntentMapperFields(t, functions, "recordFromRow", reflect.TypeFor[Intent]())
	assertCommandIntentMapperFields(t, functions, "recordFromRow", reflect.TypeFor[Record]())
}

func runCommandIntentFieldGuardFixture(t *testing.T, fixture string) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCommandIntentFieldGuardFixtureHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ORCH_INTENT_FIELD_GUARD_FIXTURE="+fixture)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("field guard fixture timeout: %v", ctx.Err())
	}
	return string(output), err
}
