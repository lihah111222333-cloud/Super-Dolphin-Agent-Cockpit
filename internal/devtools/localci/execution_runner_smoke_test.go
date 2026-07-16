package localci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	freshContainerSmokeSwitch = "SUPER_DOLPHIN_LOCALCI_DOCKER_SMOKE"
	freshContainerSmokeTag    = "super-dolphin-fresh-container-smoke:fixture"
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
	configuration := buildFreshContainerSmokeFixture(t)
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
	t.Logf("container_ids=%s,%s removed=%t,%t removal_proofs=%s,%s", first.Container.ContainerID, second.Container.ContainerID, first.Container.Removed, second.Container.Removed, first.RemovalProofDigest, second.RemovalProofDigest)
}

func buildFreshContainerSmokeFixture(t *testing.T) freshContainerSmokeConfiguration {
	t.Helper()
	fixtureDirectory := canonicalSmokePath(t, "testdata/fresh-container-smoke")
	request := BuildKitBuildRequest{
		SourceTreeSHA: strings.Repeat("a", 40), PolicyDigest: digest("8"), ImageSchemaVersion: imageInputSchemaVersion,
		ContextDigest: digest("1"), InputDigest: digest("3"), ToolchainDigest: digest("4"),
		DockerfileDigest: digest("2"), Platform: "linux/arm64",
	}
	args := []string{"buildx", "build", "--load", "--provenance=false", "--network=none", "--platform=" + request.Platform, "--file=" + filepath.Join(fixtureDirectory, "Dockerfile"), "--tag=" + freshContainerSmokeTag}
	for _, label := range sortedBuildxBindingLabels(request) {
		args = append(args, "--label="+label)
	}
	args = append(args, fixtureDirectory)
	if _, err := (execDockerRunner{}).Run(context.Background(), args...); err != nil {
		t.Fatalf("build real Docker smoke fixture: %v", err)
	}
	t.Cleanup(func() {
		if _, err := (execDockerRunner{}).Run(context.Background(), "image", "rm", "--force", freshContainerSmokeTag); err != nil {
			t.Errorf("remove real Docker smoke fixture: %v", err)
		}
	})
	return inspectFreshContainerSmokeFixture(t, fixtureDirectory, request)
}

func inspectFreshContainerSmokeFixture(t *testing.T, fixtureDirectory string, request BuildKitBuildRequest) freshContainerSmokeConfiguration {
	t.Helper()
	output, err := (execDockerRunner{}).Run(context.Background(), "image", "inspect", freshContainerSmokeTag)
	if err != nil {
		t.Fatal(err)
	}
	var document imageInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		t.Fatal(err)
	}
	identity := smokeImageIdentity(t, document)
	root := canonicalSmokePath(t, t.TempDir())
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	return freshContainerSmokeConfiguration{
		Image: identity,
		ImageTruth: FreshContainerImageTruth{
			PolicyDigest: request.PolicyDigest, InputDigest: request.InputDigest,
			ToolchainDigest: request.ToolchainDigest, SchemaVersion: request.ImageSchemaVersion,
		},
		SourceTreeSHA: request.SourceTreeSHA, SourceSnapshotDir: source,
		SeccompPath: canonicalSmokePath(t, filepath.Join(fixtureDirectory, "seccomp.json")), TrustedSourceRoot: root,
	}
}

func smokeImageIdentity(t *testing.T, document imageInspectDocument) gate.ImageIdentity {
	t.Helper()
	if document.Descriptor == nil || document.RootFS == nil || document.Config == nil || len(document.RepoDigests) != 1 {
		t.Fatal("real Docker smoke image inspect omitted identity fields")
	}
	registry, manifestDigest, found := strings.Cut(document.RepoDigests[0], "@")
	if !found || manifestDigest != document.Descriptor.Digest {
		t.Fatal("real Docker smoke manifest reference drifted")
	}
	configDigest := document.Descriptor.Annotations["config.digest"]
	if err := validateDigest("smoke config digest", configDigest); err != nil {
		t.Fatal(err)
	}
	return gate.ImageIdentity{
		Registry: registry, OCIIndexDigest: manifestDigest, PlatformManifestDigest: manifestDigest,
		ConfigDigest: configDigest, RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS: document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
}

func canonicalSmokePath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
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
