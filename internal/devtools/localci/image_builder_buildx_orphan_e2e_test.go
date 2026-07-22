package localci

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestDockerBuildxRunnerRecoversRealOrphanAfterRestart(t *testing.T) {
	requireLocalBuildxOrphanE2E(t)
	request := lockedBuildxOrphanE2ERequest(t)
	root := privateTempRoot(t)
	runner, err := NewDockerBuildxRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	builderName := buildxBuilderName(request.SourceTreeSHA, "candidate-orphane2e")
	if !validControlledBuildxBuilderName(builderName) {
		t.Fatalf("orphan builder name %q does not match the controlled workload format", builderName)
	}
	containerName := controlledBuildxContainerName(builderName)
	requireNoExistingControlledBuildxResources(t, runner)
	t.Cleanup(func() {
		if err := runner.removeControlledBuilder(context.Background(), builderName); err != nil {
			t.Errorf("clean up test-only buildx orphan %q: %v", builderName, err)
		}
	})

	createRealBuildxOrphan(t, runner, request, builderName, containerName)
	restarted, err := NewDockerBuildxRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverControlledBuilders(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertRealBuildxOrphanRecovered(t, restarted, builderName, containerName)
}

func lockedBuildxOrphanE2ERequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	request := validBuildxRequest(t)
	var lock toolchainLock
	if err := decodeStrictJSON(readRepoFile(t, toolchainLockPath), &lock); err != nil {
		t.Fatal(err)
	}
	if err := validateBuildKitVersion(lock.BuildKitVersion); err != nil {
		t.Fatal(err)
	}
	if err := validateBuildKitImageReference(lock.BuildKitImage); err != nil {
		t.Fatal(err)
	}
	request.BuildKitVersion = lock.BuildKitVersion
	request.BuildKitImage = lock.BuildKitImage
	return request
}

func requireLocalBuildxOrphanE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("LOCALCI_BUILDX_ORPHAN_E2E") != "1" {
		t.Skip("set LOCALCI_BUILDX_ORPHAN_E2E=1 to run the local Docker buildx orphan recovery E2E")
	}
	docker := execDockerRunner{}
	if _, err := docker.Run(context.Background(), "version", "--format", "{{.Server.Version}}"); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	if _, err := docker.Run(context.Background(), "buildx", "version"); err != nil {
		t.Skipf("Docker buildx is unavailable: %v", err)
	}
}

func requireNoExistingControlledBuildxResources(t *testing.T, runner *DockerBuildxRunner) {
	t.Helper()
	names, err := runner.controlledBuilderNamesForRecovery(context.Background())
	if err != nil {
		t.Skipf("cannot confirm isolated controlled BuildKit resources: %v", err)
	}
	if len(names) != 0 {
		t.Skipf("controlled BuildKit resources already exist: %v", names)
	}
}

func createRealBuildxOrphan(t *testing.T, runner *DockerBuildxRunner, request BuildKitBuildRequest, builderName string, containerName string) {
	t.Helper()
	ctx := context.Background()
	if err := runner.recordControlledBuilder(builderName, request); err != nil {
		t.Fatal(err)
	}
	createArgs := []string{
		"buildx", "create", "--name", builderName, "--driver", "docker-container",
		"--driver-opt=image=" + request.BuildKitImage,
		"--driver-opt=cpu-quota=" + buildxBuilderCPUQuota,
		"--driver-opt=cpu-period=" + buildxBuilderCPUPeriod,
		"--driver-opt=memory=" + buildxBuilderMemory,
	}
	if _, err := runner.executor.Run(ctx, bytes.NewReader(nil), createArgs...); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.executor.Run(ctx, bytes.NewReader(nil), "buildx", "inspect", "--builder", builderName, "--bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := runner.updateControlledBuilderPidsLimit(ctx, builderName); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.executor.Run(ctx, bytes.NewReader(nil), "container", "inspect", containerName); err != nil {
		t.Fatal(err)
	}
}

func assertRealBuildxOrphanRecovered(t *testing.T, runner *DockerBuildxRunner, builderName string, containerName string) {
	t.Helper()
	if names, err := runner.recordedControlledBuilderNames(); err != nil || len(names) != 0 {
		t.Fatalf("restart recovery retained ownership record: names=%v err=%v", names, err)
	}
	if _, err := runner.executor.Run(context.Background(), bytes.NewReader(nil), "buildx", "inspect", "--builder", builderName); err == nil {
		t.Fatalf("restart recovery retained test-only builder %q", builderName)
	}
	if _, err := runner.executor.Run(context.Background(), bytes.NewReader(nil), "container", "inspect", containerName); err == nil {
		t.Fatalf("restart recovery retained test-only BuildKit container %q", containerName)
	}
}
