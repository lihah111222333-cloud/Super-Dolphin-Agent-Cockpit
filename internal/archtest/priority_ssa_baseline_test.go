package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type prioritySSABaselineFixture struct {
	opts         CheckOptions
	baselinePath string
}

func TestPrioritySSABaselineFlagsNewSSAThenFreezeAccepts(t *testing.T) {
	root := writePrioritySSAFixtureRepo(t)
	fixture := prioritySSABaselineFixture{
		opts: CheckOptions{
			RepoRoot:  root,
			ScanRoots: []string{"internal", "cmd"},
			SkipDirs:  DefaultSkipDirs(),
		},
		baselinePath: filepath.Join(t.TempDir(), "priority_ssa_fixture.json"),
	}
	if err := SavePrioritySSABaseline(fixture.baselinePath, nil); err != nil {
		t.Fatalf("save empty priority SSA baseline: %v", err)
	}

	result, want := assertPrioritySSANewViolation(t, fixture)
	assertPrioritySSAFreezeAccepts(t, fixture, result, want)
}

func assertPrioritySSANewViolation(t *testing.T, fixture prioritySSABaselineFixture) (PrioritySSABaselineResult, PrioritySSAViolation) {
	t.Helper()
	result, err := CheckPrioritySSABaseline(fixture.opts, fixture.baselinePath)
	if err != nil {
		t.Fatalf("check empty priority SSA baseline: %v", err)
	}
	if result.OK() {
		t.Fatal("CheckPrioritySSABaseline() OK for new SSA violation, want New violation")
	}
	want := PrioritySSAViolation{
		Rule:   PrioritySSAErrorStringRule,
		File:   "internal/risk/error_string.go",
		Line:   7,
		Detail: "error string match strings.Contains",
	}
	assertPrioritySSAContains(t, result.New, want)
	return result, want
}

func assertPrioritySSAFreezeAccepts(
	t *testing.T,
	fixture prioritySSABaselineFixture,
	result PrioritySSABaselineResult,
	want PrioritySSAViolation,
) {
	t.Helper()
	if err := SavePrioritySSABaseline(fixture.baselinePath, result.Current); err != nil {
		t.Fatalf("freeze current priority SSA baseline: %v", err)
	}
	info, err := LoadPrioritySSABaseline(fixture.baselinePath)
	if err != nil {
		t.Fatalf("load frozen priority SSA baseline: %v", err)
	}
	frozen, ok := info.Data[want.Key()]
	if !ok {
		t.Fatalf("frozen baseline missing %q; got keys %#v", want.Key(), info.Data)
	}
	if frozen.Rule != want.Rule || frozen.Detail == "" {
		t.Fatalf("frozen violation lost rule/detail metadata: %#v", frozen)
	}

	result, err = CheckPrioritySSABaseline(fixture.opts, fixture.baselinePath)
	if err != nil {
		t.Fatalf("check frozen priority SSA baseline: %v", err)
	}
	if len(result.New) != 0 {
		t.Fatalf("frozen priority SSA baseline still reports new violations:\n%s",
			strings.Join(PrioritySSAViolationStrings(result.New), "\n"))
	}
}

func writePrioritySSAFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writePrioritySSAFile(t, root, "go.mod", "module github.com/anthropic-ai/super-agent-v3\n\ngo 1.25.7\n")
	writePrioritySSAFile(t, root, "cmd/mcp-orch/store/taskdag/store.go", `package taskdag

type Store interface {
	Save()
}
`)
	writePrioritySSAFile(t, root, "internal/module/skill/service.go", `package skill

type Service interface {
	Run()
}
`)
	writePrioritySSAFile(t, root, "internal/risk/error_string.go", `package risk

import "strings"

func bad(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "missing")
}
`)
	return root
}

func writePrioritySSAFile(t *testing.T, root, relPath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func assertPrioritySSAContains(t *testing.T, got []PrioritySSAViolation, want PrioritySSAViolation) {
	t.Helper()
	for _, violation := range got {
		if violation.Key() == want.Key() {
			return
		}
	}
	t.Fatalf("missing priority SSA violation %q; got:\n%s",
		want.Key(), strings.Join(PrioritySSAViolationStrings(got), "\n"))
}
