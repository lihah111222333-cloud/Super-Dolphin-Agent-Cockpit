package observability

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionDeletesOnlyExactTraceJSONLFilesInTraceDirectory(t *testing.T) {
	cfg := enabledTestConfig(t)
	cfg.JSONLRetentionDays = 7
	cfg.JSONLRetentionMaxMB = 512
	parent := t.TempDir()
	dir := filepath.Join(parent, "traces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir traces: %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	oldTrace := writeRetentionFixture(t, dir, "trace-2026-05-01.jsonl", "old\n", now.Add(-30*24*time.Hour))
	keepTrace := writeRetentionFixture(t, dir, "trace-2026-06-10.jsonl", "new\n", now)
	keepNonTrace := writeRetentionFixture(t, dir, "trace-2026-05-01.jsonl.bak", "old\n", now.Add(-30*24*time.Hour))
	keepPrefix := writeRetentionFixture(t, dir, "not-trace-2026-05-01.jsonl", "old\n", now.Add(-30*24*time.Hour))
	parentFile := writeRetentionFixture(t, parent, "trace-2026-05-01.jsonl", "parent\n", now.Add(-30*24*time.Hour))

	if err := ApplyTraceRetention(dir, cfg, now); err != nil {
		t.Fatalf("ApplyTraceRetention: %v", err)
	}
	assertMissing(t, oldTrace)
	assertExists(t, keepTrace)
	assertExists(t, keepNonTrace)
	assertExists(t, keepPrefix)
	assertExists(t, parentFile)
}

func TestRetentionPrunesOldestTraceFilesUntilWithinMaxBytes(t *testing.T) {
	cfg := enabledTestConfig(t)
	cfg.JSONLRetentionDays = 365
	cfg.JSONLRetentionMaxMB = 1
	parent := t.TempDir()
	dir := filepath.Join(parent, "traces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir traces: %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	oldest := writeRetentionFixture(t, dir, "trace-2026-06-01.jsonl", string(make([]byte, 700*1024)), now.Add(-3*time.Hour))
	middle := writeRetentionFixture(t, dir, "trace-2026-06-02.jsonl", string(make([]byte, 700*1024)), now.Add(-2*time.Hour))
	newest := writeRetentionFixture(t, dir, "trace-2026-06-03.jsonl", string(make([]byte, 200*1024)), now.Add(-1*time.Hour))

	if err := ApplyTraceRetention(dir, cfg, now); err != nil {
		t.Fatalf("ApplyTraceRetention: %v", err)
	}
	assertMissing(t, oldest)
	assertExists(t, middle)
	assertExists(t, newest)
}

func TestRetentionRejectsNonTracesDirectory(t *testing.T) {
	cfg := enabledTestConfig(t)
	if err := ApplyTraceRetention(t.TempDir(), cfg, time.Now()); err == nil {
		t.Fatalf("ApplyTraceRetention accepted non-traces directory")
	}
}

func writeRetentionFixture(t *testing.T, dir, name, content string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
	return path
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err=%v", path, err)
	}
}
