package localci

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	freshContainerSmokeSwitch = "SUPER_DOLPHIN_LOCALCI_DOCKER_SMOKE"
	freshContainerSmokeConfig = "SUPER_DOLPHIN_LOCALCI_DOCKER_SMOKE_CONFIG"
)

type freshContainerSmokeConfiguration struct {
	Image             gate.ImageIdentity
	ImageTruth        FreshContainerImageTruth
	SourceTreeSHA     string
	SourceSnapshotDir string
	SeccompPath       string
	TrustedSourceRoot string
}

func TestRunFreshContainerDockerSmokeCreatesDistinctContainers(t *testing.T) {
	if os.Getenv(freshContainerSmokeSwitch) != "1" {
		t.Skip("real Docker smoke is disabled")
	}
	configuration := readFreshContainerSmokeConfiguration(t)
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: configuration.SourceTreeSHA}, SourceTreeSHA: configuration.SourceTreeSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewFreshContainerRunner(configuration.SeccompPath, configuration.TrustedSourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	request := FreshContainerRequest{
		Image: configuration.Image, ImageTruth: configuration.ImageTruth,
		SourceTreeSHA: configuration.SourceTreeSHA, SourceSnapshotDir: configuration.SourceSnapshotDir,
		Profile: gate.ProfileLocalFast, Plan: plan, GateID: gate.GateIDWhitespaceCheck,
	}
	first := runFreshContainerSmoke(t, runner, request)
	second := runFreshContainerSmoke(t, runner, request)
	if first.Container.ContainerID == second.Container.ContainerID {
		t.Fatalf("Docker reused container ID %s", first.Container.ContainerID)
	}
}

func readFreshContainerSmokeConfiguration(t *testing.T) freshContainerSmokeConfiguration {
	t.Helper()
	path := os.Getenv(freshContainerSmokeConfig)
	if path == "" {
		t.Fatalf("%s is required when %s=1", freshContainerSmokeConfig, freshContainerSmokeSwitch)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration freshContainerSmokeConfiguration
	decoderErr := json.Unmarshal(data, &configuration)
	if decoderErr != nil {
		t.Fatal(decoderErr)
	}
	return configuration
}

func runFreshContainerSmoke(t *testing.T, runner *FreshContainerRunner, request FreshContainerRequest) FreshContainerResult {
	t.Helper()
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("RunFreshContainer() real Docker smoke: %v", err)
	}
	if result.Status != gate.ResultStatusPassed || !result.Container.Removed || result.RemovalProofDigest == "" {
		t.Fatalf("real Docker smoke result = %#v", result)
	}
	return result
}
