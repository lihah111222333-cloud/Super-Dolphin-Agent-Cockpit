package archtest

import (
	"path/filepath"
	"testing"
)

func TestMeasureFileMetrics_Sample(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.go")

	m := MeasureFileMetrics(path)

	// Size checks
	if m.Lines == 0 {
		t.Error("Lines should be > 0")
	}
	if m.MaxFuncLen == 0 {
		t.Error("MaxFuncLen should be > 0 (complexFunc has multiple lines)")
	}

	// Complexity
	if m.MaxNesting < 3 {
		t.Errorf("MaxNesting: got %d, want >= 3 (deepNesting has nesting 4)", m.MaxNesting)
	}
	if m.MaxComplexity < 5 {
		t.Errorf("MaxComplexity: got %d, want >= 5 (complexFunc is complex)", m.MaxComplexity)
	}
	if m.MaxParams < 8 {
		t.Errorf("MaxParams: got %d, want >= 8 (manyParams has 8)", m.MaxParams)
	}
	if m.MaxReturns < 4 {
		t.Errorf("MaxReturns: got %d, want >= 4 (manyReturns has 5)", m.MaxReturns)
	}

	// Quality
	if m.GlobalVars < 1 {
		t.Errorf("GlobalVars: got %d, want >= 1 (globalCounter)", m.GlobalVars)
	}
	if m.PanicCount < 1 {
		t.Errorf("PanicCount: got %d, want >= 1 (panicFunc)", m.PanicCount)
	}
	if m.NakedReturns < 1 {
		t.Errorf("NakedReturns: got %d, want >= 1 (nakedReturnFunc)", m.NakedReturns)
	}
	if m.EmptyFuncs < 1 {
		t.Errorf("EmptyFuncs: got %d, want >= 1 (emptyFunc)", m.EmptyFuncs)
	}
	if m.TodoCount < 2 {
		t.Errorf("TodoCount: got %d, want >= 2 (two TODO comments)", m.TodoCount)
	}
	if m.MaxStructFields < 16 {
		t.Errorf("MaxStructFields: got %d, want >= 16 (BigStruct)", m.MaxStructFields)
	}
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
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.go")
	m := MeasureFileMetrics(path)

	// metrics_sample.go 只有一个非豁免全局变量 (globalCounter)
	if m.GlobalVars != 1 {
		t.Errorf("GlobalVars: got %d, want 1 (only globalCounter should be counted)", m.GlobalVars)
	}
}
