package archtest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBaseline_NotFoundFailsFast(t *testing.T) {
	t.Parallel()
	_, err := LoadBaseline("/nonexistent/path/baseline.json")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file error = %v, want not-exist error", err)
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

func TestLoadBaseline_EmptyMapAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("{}"), baselineFileMode); err != nil {
		t.Fatalf("write baseline fixture: %v", err)
	}
	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("LoadBaseline(empty map) error = %v, want nil", err)
	}
	if len(loaded.Data) != 0 {
		t.Fatalf("loaded baseline entries = %d, want 0", len(loaded.Data))
	}
}

func TestLoadBaseline_NullFailsFast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "null.json")
	if err := os.WriteFile(path, []byte("null"), baselineFileMode); err != nil {
		t.Fatalf("write baseline fixture: %v", err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), "null") {
		t.Fatalf("LoadBaseline(null) error = %v, want null baseline error", err)
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
