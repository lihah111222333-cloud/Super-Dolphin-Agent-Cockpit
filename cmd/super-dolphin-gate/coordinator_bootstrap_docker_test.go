package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestProductionBootstrapDockerFixtureSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker fixture smoke is disabled in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is unavailable")
	}
	identity, immutable, localReference, candidateRegistry, hasImmutable := buildProductionBootstrapDockerFixture(t)
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
	if hasImmutable {
		assertProductionBootstrapControllerDockerE2E(t, identity, candidateRegistry)
	}
}

func buildProductionBootstrapDockerFixture(
	t *testing.T,
) (gatecontract.ImageIdentity, string, string, string, bool) {
	t.Helper()
	contextRoot := t.TempDir()
	binary := filepath.Join(contextRoot, "bootstrap-runner")
	source := filepath.Join(contextRoot, "runner.go")
	fixtureSource := `package main
import ("encoding/base64"; "encoding/json"; "os")
func main() {
	data, err := base64.RawStdEncoding.DecodeString(os.Getenv("SUPER_DOLPHIN_BOOTSTRAP_REQUEST_B64")); if err != nil { panic(err) }
	var request struct { CandidateRegistry string ` + "`json:\"candidate_registry\"`" + `; Runner map[string]any ` + "`json:\"runner\"`" + ` }
	if err := json.Unmarshal(data, &request); err != nil { panic(err) }
	request.Runner["registry"] = request.CandidateRegistry
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"schema_version": 1, "image": request.Runner}); err != nil { panic(err) }
}
`
	if err := os.WriteFile(source, []byte(fixtureSource), 0o600); err != nil {
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
	nameDigest := sha256.Sum256([]byte(contextRoot))
	suffix := hex.EncodeToString(nameDigest[:6])
	repository := "local/super-dolphin-bootstrap-smoke-" + suffix
	tag := repository + ":fixture"
	_ = buildProductionBootstrapFixtureImage(t, contextRoot, tag)
	document := inspectProductionBootstrapFixtureImage(t, tag)
	immutable := productionBootstrapFixtureImmutableReference(t, repository, document.RepoDigests)
	manifestDigest := strings.TrimPrefix(immutable, repository+"@")
	identity := productionBootstrapFixtureIdentity(repository, manifestDigest, document)
	candidateRegistry := "local/bootstrap-candidate-" + suffix
	runBootstrapDocker(t, "tag", tag, candidateRegistry+":fixture")
	candidate := inspectProductionBootstrapFixtureImage(t, candidateRegistry+":fixture")
	hasImmutable := slices.Contains(document.RepoDigests, immutable) &&
		slices.Contains(candidate.RepoDigests, candidateRegistry+"@"+manifestDigest)
	return identity, immutable, document.ID, candidateRegistry, hasImmutable
}

func productionBootstrapFixtureImmutableReference(t *testing.T, repository string, repoDigests []string) string {
	t.Helper()
	for _, reference := range repoDigests {
		if strings.HasPrefix(reference, repository+"@sha256:") {
			return reference
		}
	}
	t.Fatalf("pushed bootstrap fixture lacks immutable RepoDigest: %v", repoDigests)
	return ""
}

func assertProductionBootstrapControllerDockerE2E(
	t *testing.T,
	identity gatecontract.ImageIdentity,
	candidateRegistry string,
) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := gatecontract.SignerIdentity{
		KeyID: "docker-controller-e2e", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	request := productionBootstrapDockerRequest(identity)
	request.CandidateRegistry = candidateRegistry
	request.Challenge = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request.Controller = productionBootstrapControllerIdentity{
		BinaryDigest: productionDigest("d"), DesignatedRequirement: `identifier "docker-controller-e2e"`, Signer: signer,
	}
	request.BootstrapSigner = signer
	request.BootstrapPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := productionBootstrapJSONDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := executeProductionBootstrapController(
		context.Background(), request, requestData, requestDigest, privateKey,
	)
	if err != nil {
		t.Fatalf("executeProductionBootstrapController() error = %v", err)
	}
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	root.RepoID = request.RepoID
	root.RemoteURL = request.RemoteURL
	root.TrustedRef = request.TrustedRef
	root.ObjectFormat = request.ObjectFormat
	root.BaselineCommit = request.BaselineCommit
	root.BaselineTree = request.BaselineTree
	root.PolicyDigest = request.PolicyDigest
	root.ImageInputDigest = request.ImageInputDigest
	root.ToolchainDigest = request.ToolchainDigest
	root.ImageSchemaVersion = request.ImageSchemaVersion
	root.Runner = identity
	root.CandidateRegistry = request.CandidateRegistry
	root.Controller = request.Controller
	root.BootstrapSigner = signer
	root.BootstrapPublicKey = request.BootstrapPublicKey
	config := productionCoordinatorConfig{
		Platform:             request.Platform,
		AcceptedImageSigners: []productionTrustedKey{{Signer: signer, PublicKey: request.BootstrapPublicKey}},
		TrustedRepository:    privateProductionBootstrapDirectory(t), TrustedSourceRoot: privateProductionBootstrapDirectory(t),
		CandidateBuildRoot: privateProductionBootstrapDirectory(t),
	}
	if err := verifyProductionBootstrapAttestation(config, root, request, requestDigest, attestation); err != nil {
		t.Fatalf("verifyProductionBootstrapAttestation() error = %v", err)
	}
	if err := (productionBootstrapHostRuntime{}).VerifyAndRemoveContainer(
		context.Background(), config, root, request, attestation,
	); err != nil {
		t.Fatalf("VerifyAndRemoveContainer() error = %v", err)
	}
	if err := exec.Command("docker", "inspect", attestation.ContainerID).Run(); err == nil {
		t.Fatal("controller E2E container was not removed")
	}
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
		ObjectFormat: gatecontract.GitObjectFormatSHA1,
		PolicyDigest: productionDigest("2"), ImageInputDigest: productionDigest("3"), Platform: "linux/" + runtime.GOARCH,
		ToolchainDigest: productionDigest("4"), ImageSchemaVersion: "1",
		CandidateRegistry: "registry.example.invalid/bootstrap-candidate",
		Runner:            identity,
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
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--env="+productionBootstrapRunnerRequestEnv+"="+base64.RawStdEncoding.EncodeToString(requestData))
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
	ctx, cancel := context.WithTimeout(context.Background(), coordinatorTimeout(gatecontract.ProfileLocalFast))
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("docker stage %q exceeded normal profile deadline %s: %s", args[0], coordinatorTimeout(gatecontract.ProfileLocalFast), strings.TrimSpace(string(output)))
		}
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func TestProductionHostDockerCommandPreservesExplicitConfigAcrossHomeIsolation(t *testing.T) {
	home, configDir, path := configureProductionHostDockerCommandTest(t)
	command, err := productionHostDockerCommand(context.Background(), "context", "show")
	if err != nil {
		t.Fatalf("productionHostDockerCommand() error = %v", err)
	}
	want := []string{"HOME=" + home, "PATH=" + path, "LC_ALL=C", "DOCKER_CONFIG=" + configDir}
	if !slices.Equal(command.Env, want) {
		t.Fatalf("Docker command environment = %#v, want %#v", command.Env, want)
	}
}

func TestProductionHostDockerCommandResolvesDefaultConfigFromHome(t *testing.T) {
	home, _, path := configureProductionHostDockerCommandTest(t)
	configDir := filepath.Join(home, ".docker")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unsetProductionHostDockerTestEnvironment(t, "DOCKER_CONFIG")
	command, err := productionHostDockerCommand(context.Background(), "context", "show")
	if err != nil {
		t.Fatalf("productionHostDockerCommand() error = %v", err)
	}
	want := []string{"HOME=" + home, "PATH=" + path, "LC_ALL=C", "DOCKER_CONFIG=" + configDir}
	if !slices.Equal(command.Env, want) {
		t.Fatalf("Docker command environment = %#v, want %#v", command.Env, want)
	}
}

func TestProductionHostDockerCommandRejectsDangerousOverrides(t *testing.T) {
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name  string
				value string
			}{
				{name: "empty", value: ""},
				{name: "non-empty", value: "forbidden"},
			} {
				t.Run(test.name, func(t *testing.T) {
					configureProductionHostDockerCommandTest(t)
					t.Setenv(name, test.value)
					if _, err := productionHostDockerCommand(context.Background(), "version"); err == nil ||
						!strings.Contains(err.Error(), name+" override is not allowed") {
						t.Fatalf("productionHostDockerCommand() error = %v", err)
					}
				})
			}
		})
	}
}

func TestProductionHostDockerCommandRejectsInvalidConfig(t *testing.T) {
	target := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(canonicalProductionHostDockerTestDirectory(t, t.TempDir()), "docker-config")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	public := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	if err := os.Chmod(public, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		config string
		want   string
	}{
		{name: "empty", config: "", want: "must not be empty"},
		{name: "relative", config: "relative/.docker", want: "canonical and absolute"},
		{name: "non canonical", config: target + "/../" + filepath.Base(target), want: "canonical and absolute"},
		{name: "symlink", config: symlink, want: "must not traverse symlinks"},
		{name: "public", config: public, want: "must be private"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configureProductionHostDockerCommandTest(t)
			t.Setenv("DOCKER_CONFIG", test.config)
			if _, err := productionHostDockerCommand(context.Background(), "version"); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("productionHostDockerCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProductionHostDockerCallersRejectDangerousOverrides(t *testing.T) {
	configureProductionHostDockerCommandTest(t)
	t.Setenv("DOCKER_HOST", "unix:///private/forbidden.sock")
	identity := gatecontract.ImageIdentity{
		Registry:       "registry.example.invalid/bootstrap-runner",
		OCIIndexDigest: productionDigest("1"), PlatformManifestDigest: productionDigest("2"),
		ConfigDigest: productionDigest("3"), RootFSDiffIDs: []string{productionDigest("4")},
		OS: "linux", Architecture: "arm64",
	}
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{
			name: "verify runner",
			run: func() error {
				return (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(context.Background(), identity)
			},
		},
		{
			name: "bootstrap docker",
			run: func() error {
				_, err := productionBootstrapDocker(context.Background(), "version")
				return err
			},
		},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.run(); err == nil || !strings.Contains(err.Error(), "DOCKER_HOST override is not allowed") {
				t.Fatalf("production Docker caller error = %v", err)
			}
		})
	}
}

func TestProductionBootstrapControllerCommandPreservesExplicitDockerConfig(t *testing.T) {
	home, configDir, path := configureProductionHostDockerCommandTest(t)
	privateKeyFile := filepath.Join(home, "bootstrap-controller-key.json")
	command, err := productionBootstrapControllerCommand(
		context.Background(), filepath.Join(home, "controller"), privateKeyFile, []byte("{}"),
	)
	if err != nil {
		t.Fatalf("productionBootstrapControllerCommand() error = %v", err)
	}
	want := []string{
		"HOME=" + home,
		"PATH=" + path,
		"LC_ALL=C",
		"DOCKER_CONFIG=" + configDir,
		productionBootstrapControllerKeyEnv + "=" + privateKeyFile,
	}
	if !slices.Equal(command.Env, want) {
		t.Fatalf("controller command environment = %#v, want %#v", command.Env, want)
	}
}

func TestProductionBootstrapControllerCommandRejectsDockerEnvironmentBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*testing.T)
		want      string
	}{
		{
			name: "empty dangerous override",
			configure: func(t *testing.T) {
				t.Setenv("DOCKER_HOST", "")
			},
			want: "DOCKER_HOST override is not allowed",
		},
		{
			name: "invalid config",
			configure: func(t *testing.T) {
				t.Setenv("DOCKER_CONFIG", "relative/.docker")
			},
			want: "validate production host Docker config",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, _, _ := configureProductionHostDockerCommandTest(t)
			test.configure(t)
			privateKeyFile := filepath.Join(home, "private-key-must-not-leak")
			_, err := runProductionBootstrapControllerCommand(
				context.Background(), filepath.Join(home, "missing-controller"), privateKeyFile, []byte("{}"),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runProductionBootstrapControllerCommand() error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), privateKeyFile) {
				t.Fatalf("controller preparation error leaked private key path: %v", err)
			}
		})
	}
}

func configureProductionHostDockerCommandTest(t *testing.T) (string, string, string) {
	t.Helper()
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		unsetProductionHostDockerTestEnvironment(t, name)
	}
	home := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	configDir := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	if path == "" {
		t.Fatal("PATH is required")
	}
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONFIG", configDir)
	return home, configDir, path
}

func canonicalProductionHostDockerTestDirectory(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func unsetProductionHostDockerTestEnvironment(t *testing.T, name string) {
	t.Helper()
	value, exists := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if exists {
			err = os.Setenv(name, value)
		} else {
			err = os.Unsetenv(name)
		}
		if err != nil {
			t.Errorf("restore %s: %v", name, err)
		}
	})
}
