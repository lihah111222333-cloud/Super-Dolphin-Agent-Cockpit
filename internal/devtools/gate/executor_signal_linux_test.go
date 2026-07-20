//go:build linux

package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxProcessGroupQuiescedDistinguishesZombieAndRunnableMembers(t *testing.T) {
	procRoot := t.TempDir()
	writeLinuxProcessStat(t, procRoot, "101", "101 (worker one) Z 1 77 0 0")
	writeLinuxProcessStat(t, procRoot, "102", "102 (worker ) two) X 1 77 0 0")
	quiesced, err := linuxProcessGroupQuiesced(procRoot, 77)
	if err != nil || !quiesced {
		t.Fatalf("zombie-only process group quiesced = %v, error = %v", quiesced, err)
	}
	writeLinuxProcessStat(t, procRoot, "103", "103 (writer) S 1 77 0 0")
	quiesced, err = linuxProcessGroupQuiesced(procRoot, 77)
	if err != nil || quiesced {
		t.Fatalf("runnable process group quiesced = %v, error = %v", quiesced, err)
	}
}

func TestParseLinuxProcessStatRejectsMalformedProcessGroup(t *testing.T) {
	if _, _, err := parseLinuxProcessStat("101 worker Z 1 77"); err == nil {
		t.Fatal("parseLinuxProcessStat() accepted a missing command terminator")
	}
	if _, _, err := parseLinuxProcessStat("101 (worker) Z 1 invalid"); err == nil {
		t.Fatal("parseLinuxProcessStat() accepted an invalid process group")
	}
}

func TestLinuxProcessGroupQuiescedFailsClosedOnUnknownStat(t *testing.T) {
	procRoot := t.TempDir()
	writeLinuxProcessStat(t, procRoot, "201", "malformed")
	if _, err := linuxProcessGroupQuiesced(procRoot, 77); err == nil {
		t.Fatal("linuxProcessGroupQuiesced() accepted malformed process state")
	}
	processRoot := filepath.Join(procRoot, "202")
	if err := os.MkdirAll(filepath.Join(processRoot, "stat"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxProcessGroupQuiesced(procRoot, 77); err == nil {
		t.Fatal("linuxProcessGroupQuiesced() accepted unreadable process state")
	}
}

func writeLinuxProcessStat(t *testing.T, procRoot string, processID string, contents string) {
	t.Helper()
	processRoot := filepath.Join(procRoot, processID)
	if err := os.Mkdir(processRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processRoot, "stat"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
