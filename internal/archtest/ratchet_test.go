package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		SizeMetrics:       SizeMetrics{Lines: 700, MaxFuncLen: 100},
		ComplexityMetrics: ComplexityMetrics{MaxComplexity: 15},
		QualityMetrics:    QualityMetrics{PanicCount: 3},
	}
	frozen := FileMetrics{
		SizeMetrics:       SizeMetrics{Lines: 500, MaxFuncLen: 60},
		ComplexityMetrics: ComplexityMetrics{MaxComplexity: 10},
		QualityMetrics:    QualityMetrics{PanicCount: 1},
	}
	vs := RatchetCheck("test.go", cur, frozen)
	if len(vs) != 4 {
		t.Fatalf("expected 4 violations (lines, max_func_len, max_complexity, panic_count), got %d: %v", len(vs), vs)
	}
	fields := make(map[string]bool)
	for _, v := range vs {
		fields[v.Field] = true
	}
	for _, expected := range []string{"lines", "max_func_len", "max_complexity", "panic_count"} {
		if !fields[expected] {
			t.Errorf("expected violation for field %q", expected)
		}
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

func TestRatchetViolation_String(t *testing.T) {
	t.Parallel()
	v := RatchetViolation{File: "foo.go", Field: "lines", Frozen: 500, Current: 700}
	s := v.String()
	if s == "" {
		t.Fatal("String() should not be empty")
	}
}
