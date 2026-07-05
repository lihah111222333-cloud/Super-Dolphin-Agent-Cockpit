package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardHookModeFailsOnBaselineOrFreezeDrift(t *testing.T) {
	root := findRepoRootForGuardModeTest(t)
	assertGuardModeFileContains(t, root, "scripts/code_size_guard.go",
		"SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT",
		"failIfGuardGeneratedFilesDrifted",
		"internal/archtest/baseline.json",
		"internal/archtest/baseline_test.json",
		"internal/archtest/freeze_registry.go",
	)
	assertGuardModeFileContains(t, root, ".githooks/pre-commit",
		"SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT=1",
		"./scripts/test_with_guard.sh --guard-only",
	)
	assertGuardModeFileContains(t, root, ".githooks/pre-push",
		"SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT=1",
		"./scripts/test_with_guard.sh",
	)
	assertGuardModeFileContains(t, root, ".github/workflows/ci.yml",
		"SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT: \"1\"",
	)
}

func TestRatchetCheck_NoChange(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{SizeMetrics: SizeMetrics{Lines: 500, MaxFuncLen: 60}}
	frozen := FileMetrics{SizeMetrics: SizeMetrics{Lines: 500, MaxFuncLen: 60}}
	vs := RatchetCheck("test.go", cur, frozen)
	if len(vs) != 0 {
		t.Fatalf("expected 0 violations for no change, got %d: %v", len(vs), vs)
	}
}

func TestRatchetCheck_Improvement(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{SizeMetrics: SizeMetrics{Lines: 300}}
	frozen := FileMetrics{SizeMetrics: SizeMetrics{Lines: 500}}
	vs := RatchetCheck("test.go", cur, frozen)
	if len(vs) != 0 {
		t.Fatalf("expected 0 violations for improvement, got %d: %v", len(vs), vs)
	}
}

func TestRatchetCheck_IgnoresCleanMetricGrowth(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: 132, MaxFuncLen: 40},
		ComplexityMetrics: ComplexityMetrics{MaxParams: 5, MaxReturns: 3},
		QualityMetrics:    QualityMetrics{GlobalVars: 9, MaxStructFields: 8},
	}
	frozen := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: 126, MaxFuncLen: 14},
		ComplexityMetrics: ComplexityMetrics{MaxParams: 2, MaxReturns: 1},
		QualityMetrics:    QualityMetrics{GlobalVars: 9, MaxStructFields: 4},
	}
	vs := RatchetCheck("cmd/mcp-lsp/schema.go", cur, frozen)
	if len(vs) != 0 {
		t.Fatalf("expected 0 violations for clean metric growth, got %d: %v", len(vs), vs)
	}
}

func TestRatchetCheck_Regression(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: MaxFileLines + 100, MaxFuncLen: 100},
		ComplexityMetrics: ComplexityMetrics{MaxComplexity: 15},
		QualityMetrics:    QualityMetrics{PanicCount: 3},
	}
	frozen := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: MaxFileLines - 300, MaxFuncLen: 60},
		ComplexityMetrics: ComplexityMetrics{MaxComplexity: 10},
		QualityMetrics:    QualityMetrics{PanicCount: 1},
	}
	vs := RatchetCheck("test.go", cur, frozen)
	// 从注册表动态推导预期字段，避免硬编码计数和字段名随注册表变化而失效
	wantFields := []string{"lines", "max_func_len", "max_complexity", "panic_count"}
	got := make(map[string]bool, len(vs))
	for _, v := range vs {
		got[v.Field] = true
	}
	for _, f := range wantFields {
		if !got[f] {
			t.Errorf("expected violation for field %q, got violations: %v", f, vs)
		}
	}
	if len(vs) != len(wantFields) {
		t.Errorf("expected %d violations, got %d: %v", len(wantFields), len(vs), vs)
	}
}

func TestRatchetCheck_InitRegression(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{QualityMetrics: QualityMetrics{HasInit: true}}
	frozen := FileMetrics{QualityMetrics: QualityMetrics{HasInit: false}}
	vs := RatchetCheck("test.go", cur, frozen)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation for init regression, got %d", len(vs))
	}
	if vs[0].Field != "has_init" {
		t.Errorf("expected has_init violation, got %s", vs[0].Field)
	}
}

func TestRatchetCheck_InitRemoval(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{QualityMetrics: QualityMetrics{HasInit: false}}
	frozen := FileMetrics{QualityMetrics: QualityMetrics{HasInit: true}}
	vs := RatchetCheck("test.go", cur, frozen)
	if len(vs) != 0 {
		t.Fatalf("expected 0 violations for init removal (improvement), got %d", len(vs))
	}
}

func TestHasViolation_Clean(t *testing.T) {
	t.Parallel()
	m := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: 100, MaxFuncLen: 30},
		ComplexityMetrics: ComplexityMetrics{MaxNesting: 2, MaxComplexity: 5},
	}
	if HasViolation(m) {
		t.Fatal("clean metrics should not have violations")
	}
}

func TestHasViolation_Panic(t *testing.T) {
	t.Parallel()
	m := FileMetrics{QualityMetrics: QualityMetrics{PanicCount: 1}}
	if !HasViolation(m) {
		t.Fatal("metrics with panic should have violations")
	}
}

func TestHasViolation_OverFileLimit(t *testing.T) {
	t.Parallel()
	m := FileMetrics{SizeMetrics: SizeMetrics{Lines: MaxFileLines + 1}}
	if !HasViolation(m) {
		t.Fatal("metrics over file limit should have violations")
	}
}

func TestFreezeBaselineKeepsProductionMissingDocs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "internal", "risk", "doc.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	body := `package risk

func ExportedThing() {
	println("ok")
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	bl := FreezeBaseline(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal"},
		SkipDirs:  DefaultSkipDirs(),
	})
	got, ok := bl["internal/risk/doc.go"]
	if !ok {
		t.Fatalf("FreezeBaseline() omitted production missing_docs fixture: %#v", bl)
	}
	if got.MissingDocs == 0 {
		t.Fatalf("production MissingDocs = 0, want violation recorded")
	}
}

func TestFreezeTestBaselineIgnoresMissingDocsOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "internal", "risk", "doc_test.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	body := `package risk

func TestExportedThing() {
	println("ok")
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	bl := FreezeTestBaseline(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal"},
		SkipDirs:  DefaultSkipDirs(),
	})
	if len(bl) != 0 {
		t.Fatalf("FreezeTestBaseline() = %#v, want missing_docs-only test file omitted", bl)
	}

	result := CheckWithBaseline(CheckOptions{
		RepoRoot:            root,
		ScanRoots:           []string{"internal"},
		SkipDirs:            DefaultSkipDirs(),
		BaselineTestsOnly:   true,
		EnforceFuncComments: true,
	}, Baseline{})
	if !result.OK() {
		t.Fatalf("CheckWithBaseline() reported missing_docs-only test violation: %#v", result)
	}
}

func TestFreezeTestBaselineDropsMissingDocsWhenOtherDebtRemains(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "internal", "risk", "mixed_test.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	body := `package risk

func TestExportedThing() {
	panic("boom")
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	bl := FreezeTestBaseline(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal"},
		SkipDirs:  DefaultSkipDirs(),
	})
	got, ok := bl["internal/risk/mixed_test.go"]
	if !ok {
		t.Fatalf("FreezeTestBaseline() omitted panic fixture: %#v", bl)
	}
	if got.PanicCount == 0 {
		t.Fatalf("PanicCount = 0, want real test debt retained")
	}
	if got.MissingDocs != 0 {
		t.Fatalf("MissingDocs = %d, want test missing_docs omitted from baseline", got.MissingDocs)
	}
}

func TestCheckWithBaselineFlagsNewProductionFileFullQualityDebt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "internal", "risk", "new_risk.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	body := `package risk

var mutableCounter int

func init() {}

func launch() {
	go func() {
		panic("boom")
	}()
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := CheckWithBaseline(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal"},
		SkipDirs:  DefaultSkipDirs(),
	}, Baseline{})
	if result.OK() {
		t.Fatal("CheckWithBaseline() OK for new risky production file, want NewFileViolations")
	}
	got := make([]string, 0, len(result.NewFileViolations))
	for _, v := range result.NewFileViolations {
		got = append(got, v.String())
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"global_vars", "has_init", "panic_count", "naked_goroutines"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("NewFileViolations missing %q:\n%s", want, joined)
		}
	}
}

func TestExistingNonBaselineFileGetsZeroToleranceMetrics(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "internal", "risk", "existing_risk.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	body := `package risk

var mutableCounter int

func init() {}

func launch() {
	go func() {
		panic("boom")
	}()
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runArchtestGit(t, root, "init", "-q")
	runArchtestGit(t, root, "config", "user.email", "guard@example.test")
	runArchtestGit(t, root, "config", "user.name", "Guard Test")
	runArchtestGit(t, root, "add", ".")
	runArchtestGit(t, root, "commit", "-m", "chore: add existing risk fixture")

	result := CheckWithBaseline(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal"},
		SkipDirs:  DefaultSkipDirs(),
	}, Baseline{})
	if result.OK() {
		t.Fatal("CheckWithBaseline() OK for HEAD-existing risky file absent from baseline, want NewFileViolations")
	}
	got := make([]string, 0, len(result.NewFileViolations))
	for _, v := range result.NewFileViolations {
		got = append(got, v.String())
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"global_vars", "has_init", "panic_count", "naked_goroutines"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("NewFileViolations missing %q:\n%s", want, joined)
		}
	}
}

func TestRatchetViolation_String(t *testing.T) {
	t.Parallel()
	v := RatchetViolation{File: "foo.go", Field: "lines", Frozen: 500, Current: 700}
	s := v.String()
	for _, want := range []string{"foo.go", "lines", "500", "700"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() missing %q, got: %q", want, s)
		}
	}
}

func findRepoRootForGuardModeTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		if next == wd {
			t.Fatalf("go.mod not found from %s", wd)
		}
		wd = next
	}
}

func assertGuardModeFileContains(t *testing.T, root, relPath string, wants ...string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	content := string(data)
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("%s missing %q", relPath, want)
		}
	}
}

func runArchtestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = ratchetSubprocessEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func ratchetSubprocessEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
