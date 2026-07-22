//go:build !darwin

package appupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
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
	if generation != "" || !errors.Is(err, appupdatefailure.ErrUnsupported) {
		t.Fatalf("beginInstallAttempt() = (%q, %v), want ErrUnsupported", generation, err)
	}
}

func TestInstallRejectsDarwinArtifactOutsideDarwin(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 0)
	quitCalled := false
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: filepath.Join(stageDir, "Super Dolphin.app"),
		Platform:      "darwin-arm64",
	}, nil, func() {
		quitCalled = true
	})
	writeSelectedInstallFixture(t, svc)

	_, err := svc.Install(context.Background())
	if !errors.Is(err, appupdatefailure.ErrUnsupported) {
		t.Fatalf("Install() error = %v, want ErrUnsupported", err)
	}
	if quitCalled {
		t.Fatal("Install() called RequestQuit on an unsupported host")
	}
	for _, path := range []string{marker, filepath.Join(stageDir, appupdatefailure.Filename)} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected install side effect %s: %v", path, statErr)
		}
	}
}
