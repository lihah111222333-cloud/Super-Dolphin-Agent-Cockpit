package main

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteRunPlanningContextBindsCalibrationResource(t *testing.T) {
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	context, err := remoteRunPlanningContext(
		remoteRunOptions{ConfigPath: configPath, Calibration: true},
		remoteci.BaselineState{
			Platform: "linux/amd64", ToolchainDigest: "sha256:toolchain", ImageCacheSnapshotID: "snapshot-current",
		},
		"runner-current",
	)
	if err != nil {
		t.Fatalf("remoteRunPlanningContext() error = %v", err)
	}
	if !context.Calibration || context.CalibrationResourceClassID != "calibration" ||
		context.CalibrationResourceCPU != 4 || context.CalibrationResourceMemoryGiB != 8 {
		t.Fatalf("remoteRunPlanningContext() = %#v", context)
	}
}

func TestRemoteRunPlanningContextRejectsCalibrationWithoutConfig(t *testing.T) {
	_, err := remoteRunPlanningContext(remoteRunOptions{Calibration: true}, remoteci.BaselineState{}, "runner-current")
	if err == nil {
		t.Fatal("remoteRunPlanningContext() accepted calibration without config")
	}
}

// TestLoadRemoteRunLedgerAllowsGenerationOneBeforeCalibration 覆盖首代 normal 在 PASS 判定和自动校准前只读元数据。
func TestLoadRemoteRunLedgerAllowsGenerationOneBeforeCalibration(t *testing.T) {
	config, _, verifier := generationOneConfiguredFixture(t)
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	if err := initializeConfiguredRemoteGenerationOneWithVerifier(t.Context(), config, ledgerPath, verifier); err != nil {
		t.Fatalf("initialize configured generation one: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(ledgerPath)
	if err != nil {
		t.Fatalf("load accepted generation one: %v", err)
	}
	snapshot, store, err := loadRemoteRunLedger(remoteRunOptions{LedgerPath: ledgerPath}, state, "runner-current")
	if err != nil {
		t.Fatalf("load normal preparation ledger without calibration: %v", err)
	}
	if snapshot.Generation != 1 || snapshot.Ledger.Calibration != nil || snapshot.Ledger.ShardOverhead != nil || store.AuthorityPath() != ledgerPath {
		t.Fatalf("normal preparation snapshot = %#v, authority = %q", snapshot, store.AuthorityPath())
	}
}

func TestRemoteRunInputCalibrationResourceBindsConfiguredClass(t *testing.T) {
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	resource, err := remoteRunInputCalibrationResource(remoteRunOptions{ConfigPath: configPath, Calibration: true})
	if err != nil {
		t.Fatalf("remoteRunInputCalibrationResource() error = %v", err)
	}
	if resource.ID != "calibration" || resource.VCPU != 4 || resource.MemoryGiB != 8 {
		t.Fatalf("remoteRunInputCalibrationResource() = %#v", resource)
	}
}

func TestRemoteRunInputCalibrationResourceRejectsMissingConfig(t *testing.T) {
	_, err := remoteRunInputCalibrationResource(remoteRunOptions{Calibration: true})
	if err == nil {
		t.Fatal("remoteRunInputCalibrationResource() accepted calibration without config")
	}
}
