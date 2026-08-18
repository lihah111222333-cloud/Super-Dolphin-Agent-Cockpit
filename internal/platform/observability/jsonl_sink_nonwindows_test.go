//go:build !windows

package observability

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceDirectoryAndFilePermissionsOwnerOnly(t *testing.T) {
	cfg := enabledTestConfig(t)
	dir := t.TempDir()
	sink, err := NewJSONLSinkInDir(dir, cfg)
	if err != nil {
		t.Fatalf("NewJSONLSinkInDir: %v", err)
	}
	sink.now = func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) }
	if err := sink.Append(context.Background(), TraceEvent{TraceID: "trace", Status: StatusOK}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertPerm(t, dir, 0o700)
	assertPerm(t, filepath.Join(dir, "trace-2026-06-02.jsonl"), 0o600)
}

// TestChmodOwnerOnlyNonWindowsAppliesMode 锁定非 Windows 的 owner-only mode 行为。
func TestChmodOwnerOnlyNonWindowsAppliesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(path, []byte("trace\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := chmodOwnerOnly(path, traceFilePerm); err != nil {
		t.Fatalf("chmodOwnerOnly() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != traceFilePerm {
		t.Fatalf("mode = %#o, want %#o", got, traceFilePerm)
	}
}

