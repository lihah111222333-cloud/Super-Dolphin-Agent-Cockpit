package archtest

import (
	"path/filepath"
	"testing"
)

func TestLoadBaseline_NotFound(t *testing.T) {
	t.Parallel()
	info, err := LoadBaseline("/nonexistent/path/baseline.json")
	if err != nil {
		t.Fatalf("missing file should return empty baseline, got error: %v", err)
	}
	if len(info.Data) != 0 {
		t.Fatalf("expected empty baseline, got %d entries", len(info.Data))
	}
	if !info.ModTime.IsZero() {
		t.Fatalf("expected zero modtime for missing file, got %v", info.ModTime)
	}
}

func TestSaveAndLoadBaseline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	bl := Baseline{
		"internal/foo/bar.go": FileMetrics{
			SizeMetrics:       SizeMetrics{Lines: 100, MaxFuncLen: 30},
			ComplexityMetrics: ComplexityMetrics{MaxNesting: 2, MaxComplexity: 5},
			QualityMetrics:    QualityMetrics{PanicCount: 1, TodoCount: 3},
		},
	}
	if err := SaveBaseline(path, bl); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Data) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded.Data))
	}
	if loaded.ModTime.IsZero() {
		t.Fatal("expected non-zero modtime after save")
	}

	m := loaded.Data["internal/foo/bar.go"]
	if m.Lines != 100 {
		t.Errorf("lines: got %d, want 100", m.Lines)
	}
	if m.MaxFuncLen != 30 {
		t.Errorf("max_func_len: got %d, want 30", m.MaxFuncLen)
	}
	if m.MaxNesting != 2 {
		t.Errorf("max_nesting: got %d, want 2", m.MaxNesting)
	}
	if m.PanicCount != 1 {
		t.Errorf("panic_count: got %d, want 1", m.PanicCount)
	}
	if m.TodoCount != 3 {
		t.Errorf("todo_count: got %d, want 3", m.TodoCount)
	}
}

func TestSaveBaseline_OmitemptyFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	bl := Baseline{
		"test.go": FileMetrics{
			SizeMetrics: SizeMetrics{Lines: 50},
			// QualityMetrics 全零值，omitempty 字段不应出现在 JSON 中
		},
	}
	if err := SaveBaseline(path, bl); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := loaded.Data["test.go"]
	if m.HasInit {
		t.Error("has_init should be false")
	}
	if m.MaxStructFields != 0 {
		t.Errorf("max_struct_fields should be 0, got %d", m.MaxStructFields)
	}
	if m.NakedGoroutines != 0 {
		t.Errorf("naked_goroutines should be 0, got %d", m.NakedGoroutines)
	}
}
