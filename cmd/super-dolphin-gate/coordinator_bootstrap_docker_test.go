package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestProductionBootstrapDockerFixtureSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker fixture smoke is disabled in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is unavailable")
	}
	identity, immutable, localReference, hasImmutable := buildProductionBootstrapDockerFixture(t)
	verifyErr := (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(context.Background(), identity)
	if hasImmutable && verifyErr != nil {
		t.Fatalf("VerifyRunner() error = %v", verifyErr)
	}
	if !hasImmutable && verifyErr == nil {
		t.Fatal("VerifyRunner() accepted a Docker store without immutable RepoDigest")
	}
	if hasImmutable {
		localReference = immutable
	}
	assertProductionBootstrapObservedContainerFixture(t, identity, localReference, hasImmutable)
}

func buildProductionBootstrapDockerFixture(t *testing.T) (gatecontract.ImageIdentity, string, string, bool) {
	t.Helper()
	contextRoot := t.TempDir()
	binary := filepath.Join(contextRoot, "bootstrap-runner")
	source := filepath.Join(contextRoot, "runner.go")
	if err := os.WriteFile(source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"bootstrap fixture\")}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-trimpath", "-o", binary, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap runner fixture: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.WriteFile(filepath.Join(contextRoot, "Dockerfile"), []byte(
		"FROM scratch\nCOPY bootstrap-runner /usr/local/bin/super-dolphin-bootstrap\nENTRYPOINT [\"/usr/local/bin/super-dolphin-bootstrap\"]\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := "docker.io/library/super-dolphin-bootstrap-smoke"
	tag := repository + ":fixture"
	manifestDigest := buildProductionBootstrapFixtureImage(t, contextRoot, tag)
	document := inspectProductionBootstrapFixtureImage(t, tag)
	identity := productionBootstrapFixtureIdentity(repository, manifestDigest, document)
	immutable := repository + "@" + manifestDigest
	return identity, immutable, document.ID, slices.Contains(document.RepoDigests, immutable)
}

func buildProductionBootstrapFixtureImage(t *testing.T, contextRoot string, tag string) string {
	t.Helper()
	metadataPath := filepath.Join(contextRoot, "metadata.json")
	runBootstrapDocker(t, "buildx", "build", "--load", "--provenance=false", "--network=none", "--tag="+tag, "--metadata-file="+metadataPath, contextRoot)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := json.Unmarshal(metadata["containerimage.digest"], &digest); err != nil {
		t.Fatalf("read fixture manifest digest: %v", err)
	}
	return digest
}

func inspectProductionBootstrapFixtureImage(t *testing.T, tag string) productionBootstrapImageInspect {
	t.Helper()
	output := runBootstrapDocker(t, "image", "inspect", tag)
	document, err := decodeProductionBootstrapInspect([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if document.Descriptor == nil || document.RootFS == nil {
		t.Skip("Docker image store does not expose descriptor/rootfs identity")
	}
	return document
}

func productionBootstrapFixtureIdentity(
	repository string,
	manifestDigest string,
	document productionBootstrapImageInspect,
) gatecontract.ImageIdentity {
	return gatecontract.ImageIdentity{
		Registry: repository, OCIIndexDigest: manifestDigest, PlatformManifestDigest: manifestDigest,
		ConfigDigest:  document.Descriptor.Annotations["config.digest"],
		RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS:            document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
}

func assertProductionBootstrapObservedContainerFixture(
	t *testing.T,
	identity gatecontract.ImageIdentity,
	imageReference string,
	hasImmutable bool,
) {
	t.Helper()
	request := productionBootstrapDockerRequest(identity)
	requestDigest, err := productionBootstrapJSONDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	containerID := createProductionBootstrapDockerContainer(t, request, requestDigest, imageReference)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", containerID).Run() })
	runBootstrapDocker(t, "start", "-a", containerID)
	document, attestation := productionBootstrapDockerEvidence(t, containerID, requestDigest)
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	root.Runner = identity
	config := productionCoordinatorConfig{
		TrustedRepository:  privateProductionBootstrapDirectory(t),
		TrustedSourceRoot:  privateProductionBootstrapDirectory(t),
		CandidateBuildRoot: privateProductionBootstrapDirectory(t),
	}
	verifyProductionBootstrapDockerContainer(t, config, root, request, attestation, document, hasImmutable)
	if err := exec.Command("docker", "inspect", containerID).Run(); err == nil {
		t.Fatal("verified bootstrap container was not removed")
	}
}

func productionBootstrapDockerRequest(identity gatecontract.ImageIdentity) productionBootstrapRequest {
	return productionBootstrapRequest{
		SchemaVersion: productionBootstrapProtocolVersion, Challenge: "docker-smoke-challenge",
		RootDigest: productionDigest("1"), RepoID: "docker/smoke", RemoteURL: "https://example.invalid/smoke.git",
		TrustedRef: "refs/heads/main", BaselineCommit: strings.Repeat("1", 40), BaselineTree: strings.Repeat("2", 40),
		PolicyDigest: productionDigest("2"), ImageInputDigest: productionDigest("3"), Platform: "linux/" + runtime.GOARCH,
		Runner: identity,
	}
}

func createProductionBootstrapDockerContainer(
	t *testing.T,
	request productionBootstrapRequest,
	requestDigest string,
	imageReference string,
) string {
	t.Helper()
	args := []string{"create", "--network=bridge", "--read-only", "--cpus=4", "--memory=8g", "--cap-drop=ALL", "--security-opt=no-new-privileges:true"}
	for key, value := range productionBootstrapContainerLabels(request, requestDigest) {
		args = append(args, "--label="+key+"="+value)
	}
	args = append(args, imageReference, "--protocol-version=1", "--request-digest="+requestDigest)
	return strings.TrimSpace(runBootstrapDocker(t, args...))
}

func productionBootstrapDockerEvidence(
	t *testing.T,
	containerID string,
	requestDigest string,
) (productionBootstrapContainerInspect, productionBootstrapAttestation) {
	t.Helper()
	document, err := decodeProductionBootstrapContainerInspect([]byte(runBootstrapDocker(t, "inspect", containerID)))
	if err != nil {
		t.Fatal(err)
	}
	canonicalInspect, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	inspectSum := sha256.Sum256(canonicalInspect)
	logSum := sha256.Sum256([]byte(runBootstrapDocker(t, "logs", containerID)))
	argvDigest, err := productionBootstrapJSONDigest(productionBootstrapContainerArgv(requestDigest))
	if err != nil {
		t.Fatal(err)
	}
	return document, productionBootstrapAttestation{
		RequestDigest: requestDigest, ContainerID: containerID, ContainerArgvDigest: argvDigest,
		ContainerLogDigest:     "sha256:" + hex.EncodeToString(logSum[:]),
		ContainerInspectDigest: "sha256:" + hex.EncodeToString(inspectSum[:]),
	}
}

func verifyProductionBootstrapDockerContainer(
	t *testing.T,
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	attestation productionBootstrapAttestation,
	document productionBootstrapContainerInspect,
	hasImmutable bool,
) {
	t.Helper()
	if hasImmutable {
		if err := (productionBootstrapHostRuntime{}).VerifyAndRemoveContainer(
			context.Background(), config, root, request, attestation,
		); err != nil {
			t.Fatalf("VerifyAndRemoveContainer() error = %v", err)
		}
		return
	}
	if err := validateProductionBootstrapContainerPolicy(config, request, attestation, document); err != nil {
		t.Fatalf("validateProductionBootstrapContainerPolicy() error = %v", err)
	}
	if err := removeProductionBootstrapContainer(context.Background(), attestation.ContainerID); err != nil {
		t.Fatal(err)
	}
}

func runBootstrapDocker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}
