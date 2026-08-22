package main

import (
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
