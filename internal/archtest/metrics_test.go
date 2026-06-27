package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMeasureFileMetrics_Sample(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.gotxt")

	m := MeasureFileMetrics(path)

	// Size checks
	require.NotZero(t, m.Lines, "Lines should be > 0")
	require.NotZero(t, m.MaxFuncLen, "MaxFuncLen should be > 0 (complexFunc has multiple lines)")

	// Complexity
	require.GreaterOrEqual(t, m.MaxNesting, 3, "MaxNesting: got %d, want >= 3 (deepNesting has nesting 4)", m.MaxNesting)
	require.GreaterOrEqual(t, m.MaxComplexity, 5, "MaxComplexity: got %d, want >= 5 (complexFunc is complex)", m.MaxComplexity)
	require.GreaterOrEqual(t, m.MaxParams, 8, "MaxParams: got %d, want >= 8 (manyParams has 8)", m.MaxParams)
	require.GreaterOrEqual(t, m.MaxReturns, 4, "MaxReturns: got %d, want >= 4 (manyReturns has 5)", m.MaxReturns)

	// Quality
	require.GreaterOrEqual(t, m.GlobalVars, 1, "GlobalVars: got %d, want >= 1 (globalCounter)", m.GlobalVars)
	require.GreaterOrEqual(t, m.PanicCount, 1, "PanicCount: got %d, want >= 1 (panicFunc)", m.PanicCount)
	require.GreaterOrEqual(t, m.NakedReturns, 1, "NakedReturns: got %d, want >= 1 (nakedReturnFunc)", m.NakedReturns)
	require.GreaterOrEqual(t, m.EmptyFuncs, 1, "EmptyFuncs: got %d, want >= 1 (emptyFunc)", m.EmptyFuncs)
	require.GreaterOrEqual(t, m.TodoCount, 2, "TodoCount: got %d, want >= 2 (two TODO comments)", m.TodoCount)
	require.GreaterOrEqual(t, m.MaxStructFields, 16, "MaxStructFields: got %d, want >= 16 (BigStruct)", m.MaxStructFields)
}

func TestMeasureFileMetrics_NotFound(t *testing.T) {
	t.Parallel()
	m := MeasureFileMetrics("/nonexistent/file.go")
	if m.Lines != 0 {
		t.Errorf("expected zero metrics for missing file, got lines=%d", m.Lines)
	}
}

func TestCountGlobalVarsV3_Exemptions(t *testing.T) {
	t.Parallel()
	// 通过 testdata 文件测试：globalCounter 应被计数，其他全局变量模式应被豁免。
	// 本测试通过 MeasureFileMetrics 间接验证。
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.gotxt")
	m := MeasureFileMetrics(path)

	// metrics_sample.gotxt 只有一个非豁免全局变量 (globalCounter)
	if m.GlobalVars != 1 {
		t.Errorf("GlobalVars: got %d, want 1 (only globalCounter should be counted)", m.GlobalVars)
	}
}

func TestCheckAllAllowsLargePackageLineTotals(t *testing.T) {
	t.Parallel()

	const retiredPackageLineLimit = 10000
	const filesInPackage = 20
	linesPerFile := MaxFileLines - 50
	if filesInPackage*linesPerFile <= retiredPackageLineLimit {
		t.Fatalf("fixture lines = %d, want above retired package line limit %d", filesInPackage*linesPerFile, retiredPackageLineLimit)
	}

	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg", "sample")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir package fixture: %v", err)
	}
	for fileIndex := 0; fileIndex < filesInPackage; fileIndex++ {
		var body strings.Builder
		body.WriteString("package sample\n\n")
		for lineIndex := 0; lineIndex < linesPerFile; lineIndex++ {
			fmt.Fprintf(&body, "var Value%dLine%d = %d\n", fileIndex, lineIndex, lineIndex)
		}
		path := filepath.Join(pkgDir, fmt.Sprintf("sample%d.go", fileIndex))
		if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
			t.Fatalf("write package fixture: %v", err)
		}
	}

	violations := CheckAll(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"pkg"},
		SkipDirs:  DefaultSkipDirs(),
	})
	if len(violations) != 0 {
		t.Fatalf("CheckAll() violations = %v, want none for package line totals", violations)
	}
}
