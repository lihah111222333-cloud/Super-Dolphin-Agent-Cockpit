//go:build !darwin

package appupdate

import (
	"testing"
)

func TestServiceNeverCallsDarwinSidecarOutsideDarwin(t *testing.T) {
	svc := newService(Config{Enabled: true, Platform: "darwin-amd64", StageDir: "/path/that/must/not/be/opened"}, nil, nil)
	if _, exists, err := svc.readPreJournalFailure(); err != nil || exists {
		t.Fatalf("readPreJournalFailure() = (_, %v, %v), want skipped", exists, err)
	}
	if err := svc.invalidatePreJournalFailure(); err != nil {
		t.Fatalf("invalidatePreJournalFailure() error = %v, want skipped", err)
	}
	generation, err := svc.beginInstallAttempt(selectedUpdate{Artifact: UpdateArtifact{Platform: "darwin-amd64"}})
	if err != nil || generation != "" {
		t.Fatalf("beginInstallAttempt() = (%q, %v), want skipped", generation, err)
	}
}
