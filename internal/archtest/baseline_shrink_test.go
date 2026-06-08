package archtest

import (
	"testing"
)

func TestShrinkBaseline_Graduated(t *testing.T) {
	t.Parallel()
	oldBL := Baseline{
		"clean.go": FileMetrics{
			SizeMetrics:    SizeMetrics{Lines: 100},
			QualityMetrics: QualityMetrics{PanicCount: 1},
		},
	}
	fileSet := map[string]bool{"clean.go": true}
	measure := func(_ string) FileMetrics {
		// 指标全绿（无违规）→ 应毕业
		return FileMetrics{SizeMetrics: SizeMetrics{Lines: 50}}
	}
	newBL, stats := ShrinkBaseline(oldBL, fileSet, measure)
	if stats.Graduated != 1 {
		t.Errorf("Graduated: got %d, want 1", stats.Graduated)
	}
	if len(newBL) != 0 {
		t.Errorf("newBL should be empty after graduation, got %d entries", len(newBL))
	}
	if !stats.Changed() {
		t.Error("Changed() should be true")
	}
}

func TestShrinkBaseline_Shrunk(t *testing.T) {
	t.Parallel()
	oldBL := Baseline{
		"improving.go": FileMetrics{
			SizeMetrics:    SizeMetrics{Lines: 500},
			QualityMetrics: QualityMetrics{PanicCount: 3, TodoCount: 5},
		},
	}
	fileSet := map[string]bool{"improving.go": true}
	measure := func(_ string) FileMetrics {
		// panic 减少但 blocked-work marker 仍有 -> 仍违规，质量指标应收紧；未违规的行数不应 churn
		return FileMetrics{
			SizeMetrics:    SizeMetrics{Lines: 400},
			QualityMetrics: QualityMetrics{PanicCount: 1, TodoCount: 2},
		}
	}
	newBL, stats := ShrinkBaseline(oldBL, fileSet, measure)
	if stats.Shrunk != 1 {
		t.Errorf("Shrunk: got %d, want 1", stats.Shrunk)
	}
	m := newBL["improving.go"]
	if m.Lines != 500 {
		t.Errorf("Lines: got %d, want 500 (clean metric should not churn)", m.Lines)
	}
	if m.PanicCount != 1 {
		t.Errorf("PanicCount: got %d, want 1 (tightened)", m.PanicCount)
	}
	if m.TodoCount != 2 {
		t.Errorf("TodoCount: got %d, want 2 (tightened)", m.TodoCount)
	}
}

func TestShrinkBaseline_ShrinksHardMetricOnlyWhileViolating(t *testing.T) {
	t.Parallel()
	oldBL := Baseline{
		"long.go": FileMetrics{
			SizeMetrics: SizeMetrics{Lines: MaxFileLines + 100},
		},
	}
	fileSet := map[string]bool{"long.go": true}
	measure := func(_ string) FileMetrics {
		return FileMetrics{
			SizeMetrics: SizeMetrics{Lines: MaxFileLines + 50},
		}
	}
	newBL, stats := ShrinkBaseline(oldBL, fileSet, measure)
	if stats.Shrunk != 1 {
		t.Errorf("Shrunk: got %d, want 1", stats.Shrunk)
	}
	m := newBL["long.go"]
	if m.Lines != MaxFileLines+50 {
		t.Errorf("Lines: got %d, want %d (still-over-limit metric should tighten)", m.Lines, MaxFileLines+50)
	}
}

func TestShrinkBaseline_Removed(t *testing.T) {
	t.Parallel()
	oldBL := Baseline{
		"deleted.go": FileMetrics{SizeMetrics: SizeMetrics{Lines: 300}},
	}
	fileSet := map[string]bool{} // 文件不在扫描列表中
	measure := func(_ string) FileMetrics {
		return FileMetrics{}
	}
	newBL, stats := ShrinkBaseline(oldBL, fileSet, measure)
	if stats.Removed != 1 {
		t.Errorf("Removed: got %d, want 1", stats.Removed)
	}
	if len(newBL) != 0 {
		t.Errorf("newBL should be empty after removal, got %d entries", len(newBL))
	}
}

func TestShrinkBaseline_NoRelax(t *testing.T) {
	t.Parallel()
	oldBL := Baseline{
		"worsening.go": FileMetrics{
			SizeMetrics:    SizeMetrics{Lines: 300},
			QualityMetrics: QualityMetrics{PanicCount: 1, TodoCount: 2},
		},
	}
	fileSet := map[string]bool{"worsening.go": true}
	measure := func(_ string) FileMetrics {
		// panic 增加（恶化）但 blocked-work marker 减少 -> 应保持 panic=1（frozen），marker=1（tightened）
		return FileMetrics{
			SizeMetrics:    SizeMetrics{Lines: 300},
			QualityMetrics: QualityMetrics{PanicCount: 5, TodoCount: 1},
		}
	}
	newBL, stats := ShrinkBaseline(oldBL, fileSet, measure)
	if stats.Shrunk != 1 {
		t.Errorf("Shrunk: got %d, want 1 (blocked-work marker was tightened)", stats.Shrunk)
	}
	m := newBL["worsening.go"]
	if m.PanicCount != 1 {
		t.Errorf("PanicCount: got %d, want 1 (should not relax)", m.PanicCount)
	}
	if m.TodoCount != 1 {
		t.Errorf("TodoCount: got %d, want 1 (should tighten)", m.TodoCount)
	}
}

func TestTightenMetrics_InitRemoval(t *testing.T) {
	t.Parallel()
	cur := FileMetrics{QualityMetrics: QualityMetrics{HasInit: false}}
	frozen := FileMetrics{QualityMetrics: QualityMetrics{HasInit: true}}
	out, changed := TightenMetrics(cur, frozen)
	if !changed {
		t.Error("init removal should be a change")
	}
	if out.HasInit {
		t.Error("HasInit should be false after tightening")
	}
}

func TestShrinkStats_NoChange(t *testing.T) {
	t.Parallel()
	s := ShrinkStats{}
	if s.Changed() {
		t.Error("empty stats should not be changed")
	}
}
