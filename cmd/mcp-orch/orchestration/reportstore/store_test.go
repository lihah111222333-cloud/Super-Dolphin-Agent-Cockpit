package reportstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistWritesAndReadsReportMetadata(t *testing.T) {
	cwd := t.TempDir()
	updatedAt := time.Date(2026, 6, 18, 10, 30, 0, 123, time.UTC)

	err := Persist(Record{
		AgentID:   "agent-1",
		Name:      "display one",
		Cwd:       cwd,
		Report:    "done\n\nevidence: ok",
		ReportSeq: 4,
		UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}

	got, err := ReadPersistedRecord(Record{AgentID: "agent-1", Name: "display one", Cwd: cwd})
	if err != nil {
		t.Fatalf("ReadPersistedRecord() error = %v", err)
	}
	if got.Report != "done\n\nevidence: ok" || got.ReportSeq != 4 || !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("ReadPersistedRecord() = %#v, want body, seq, updated_at", got)
	}
	raw := readPersistedReportFile(t, cwd, "agent-1", "display one")
	if !strings.HasPrefix(raw, "---\nreport_seq: 4\nupdated_at: \"2026-06-18T10:30:00.000000123Z\"\n---\n\n") {
		t.Fatalf("persisted report raw = %q, want markdown front matter", raw)
	}
}

func TestReadPersistedRecordTreatsPlainTextAsSeqZero(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".agnet", "report", "agent-1+display one")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("legacy body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ReadPersistedRecord(Record{AgentID: "agent-1", Name: "display one", Cwd: cwd})
	if err != nil {
		t.Fatalf("ReadPersistedRecord() error = %v", err)
	}
	if got.Report != "legacy body" || got.ReportSeq != 0 || !got.UpdatedAt.IsZero() {
		t.Fatalf("ReadPersistedRecord() = %#v, want legacy body with seq zero", got)
	}
}

func TestReadPersistedRecordRejectsIncompleteMetadata(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".agnet", "report", "agent-1+display one")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("---\nreport_seq: 4\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := ReadPersistedRecord(Record{AgentID: "agent-1", Name: "display one", Cwd: cwd})
	if err == nil || !strings.Contains(err.Error(), "incomplete report metadata") {
		t.Fatalf("ReadPersistedRecord() error = %v, want incomplete metadata error", err)
	}
}

func readPersistedReportFile(t *testing.T, cwd, agentID, name string) string {
	t.Helper()
	path, err := agentReportFilePath(Record{AgentID: agentID, Name: name, Cwd: cwd})
	if err != nil {
		t.Fatalf("agentReportFilePath() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(raw)
}
