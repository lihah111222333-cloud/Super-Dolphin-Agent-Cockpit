package appupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
)

func mutateSelectedInstallFixture(t *testing.T, svc *service, mutate func(*selectedUpdate)) {
	t.Helper()
	stage, err := openStageBoundary(svc.cfg.StageDir)
	if err != nil {
		t.Fatalf("openStageBoundary() error = %v", err)
	}
	staged, readErr := readSelectedUpdate(stage)
	if readErr == nil {
		mutate(&staged)
		readErr = writeSelectedUpdate(stage, staged)
	}
	closeErr := stage.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("mutate selected update errors = %v", errors.Join(readErr, closeErr))
	}
}

func TestInstallRejectsTamperedArtifact(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    "/bin/echo",
		TargetAppPath: "/Applications/Super Dolphin.app",
		Platform:      "darwin-arm64",
	}, nil, func() {})
	writeSelectedInstallFixture(t, svc)
	dmgPath := filepath.Join(stageDir, dmgFilename)
	if err := os.WriteFile(dmgPath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile(tampered artifact) error = %v", err)
	}

	_, err := svc.Install(context.Background())
	if !errors.Is(err, contract.ErrUpdateIntegrityInvalid) {
		t.Fatalf("Install() error = %v, want sha256 mismatch rejection", err)
	}
}

func TestInstallRejectsSelectedPlatformMismatchBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(stageDir, dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-amd64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("Install() error = %v, want selected platform mismatch", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsSelectedArtifactOutsideStageBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(appUpdateRealTempDir(t), dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "artifact path") {
		t.Fatalf("Install() error = %v, want staged artifact identity mismatch", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsNonCleanSelectedArtifactAliasBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := stageDir + "/./" + dmgFilename
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "artifact path") {
		t.Fatalf("Install() error = %v, want non-clean staged artifact rejection", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsDivergentDarwinDMGPathBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(stageDir, dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)
	divergentPath := filepath.Join(appUpdateRealTempDir(t), dmgFilename)
	mutateSelectedInstallFixture(t, svc, func(staged *selectedUpdate) {
		staged.DMGPath = divergentPath
	})

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "dmg path") {
		t.Fatalf("Install() error = %v, want divergent Darwin dmg_path rejection", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func assertInstallAttemptNotStarted(t *testing.T, stageDir, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v, want helper not started", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, appupdatefailure.Filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-journal stat error = %v, want generation not begun", err)
	}
}
