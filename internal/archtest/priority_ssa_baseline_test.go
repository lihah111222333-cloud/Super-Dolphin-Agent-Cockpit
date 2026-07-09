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

	result, wants := assertPrioritySSANewViolation(t, fixture)
	assertPrioritySSAFreezeAccepts(t, fixture, result, wants...)
}

func assertPrioritySSANewViolation(t *testing.T, fixture prioritySSABaselineFixture) (PrioritySSABaselineResult, []PrioritySSAViolation) {
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
	cancel := PrioritySSAViolation{
		Rule:   PrioritySSAContextCancelRule,
		File:   "internal/risk/context_cancel.go",
		Line:   6,
		Detail: "ignored cancel func from context.WithCancel",
	}
	assertPrioritySSAContains(t, result.New, cancel)
	orchestration := PrioritySSAViolation{
		Rule:   PrioritySSAWidePortRule,
		File:   "internal/risk/orchestration.go",
		Line:   9,
		Detail: "parameter service in pass uses broad port contract.OrchestrationService",
	}
	assertPrioritySSAContains(t, result.New, orchestration)
	return result, []PrioritySSAViolation{want, cancel, orchestration}
}

func assertPrioritySSAFreezeAccepts(
	t *testing.T,
	fixture prioritySSABaselineFixture,
	result PrioritySSABaselineResult,
	wants ...PrioritySSAViolation,
) {
	t.Helper()
	if err := SavePrioritySSABaseline(fixture.baselinePath, result.Current); err != nil {
		t.Fatalf("freeze current priority SSA baseline: %v", err)
	}
	info, err := LoadPrioritySSABaseline(fixture.baselinePath)
	if err != nil {
		t.Fatalf("load frozen priority SSA baseline: %v", err)
	}
	for _, want := range wants {
		frozen, ok := info.Data[want.Key()]
		if !ok {
			t.Fatalf("frozen baseline missing %q; got keys %#v", want.Key(), info.Data)
		}
		if frozen.Rule != want.Rule || frozen.Detail == "" {
			t.Fatalf("frozen violation lost rule/detail metadata: %#v", frozen)
		}
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
	writePrioritySSAFile(t, root, "internal/contract/orchestration.go", `package contract

type OrchestrationService interface {
	Launch()
}
`)
	writePrioritySSAFile(t, root, "internal/risk/orchestration.go", `package risk

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

type holder struct {
	service contract.OrchestrationService
}

func pass(service contract.OrchestrationService) contract.OrchestrationService {
	return service
}
`)
	writePrioritySSAFile(t, root, "internal/risk/error_string.go", `package risk

import "strings"

func bad(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "missing")
}
`)
	writePrioritySSAFile(t, root, "internal/risk/context_cancel.go", `package risk

import "context"

func leak(parent context.Context) context.Context {
	ctx, _ := context.WithCancel(parent)
	return ctx
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
