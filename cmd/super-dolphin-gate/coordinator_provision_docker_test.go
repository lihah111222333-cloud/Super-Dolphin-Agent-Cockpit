package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type productionProvisionDockerRuntime struct {
	sourceRepository string
}

const (
	productionDockerE2EOptInEnv                  = "SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E"
	productionProvisionFailureLogDisplayMaxBytes = 16 * 1024
)

const productionProvisionFailureLogTailMarker = "\n[... earlier gate log output omitted; key failure and tail follow ...]\n"

func (runtimeFixture productionProvisionDockerRuntime) VerifyRunner(
	ctx context.Context,
	identity gatecontract.ImageIdentity,
) error {
	return (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(ctx, identity)
}

func (runtimeFixture productionProvisionDockerRuntime) ResolveGitExecutable() (string, error) {
	return resolveProductionGitExecutable()
}

func (runtimeFixture productionProvisionDockerRuntime) CloneTrustedRepository(
	ctx context.Context,
	gitExecutable string,
	root productionBootstrapRoot,
	destination string,
) error {
	command := exec.CommandContext(
		ctx, gitExecutable, "clone", "-q", "--bare", "--", runtimeFixture.sourceRepository, destination,
	)
	command.Env = controlledProductionGitEnvironment(gitExecutable)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("clone Docker E2E trusted repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := productionProvisionGitLine(
		ctx, gitExecutable, destination, "remote", "set-url", "origin", root.RemoteURL,
	); err != nil {
		return err
	}
	commit, err := productionProvisionGitLine(
		ctx, gitExecutable, destination, "rev-parse", "--verify", root.TrustedRef+"^{commit}",
	)
	if err != nil || commit != root.BaselineCommit {
		return errors.Join(errors.New("Docker E2E trusted commit drifted"), err)
	}
	tree, err := productionProvisionGitLine(
		ctx, gitExecutable, destination, "rev-parse", "--verify", root.BaselineCommit+"^{tree}",
	)
	if err != nil || tree != root.BaselineTree {
		return errors.Join(errors.New("Docker E2E trusted tree drifted"), err)
	}
	return os.Chmod(destination, 0o700)
}

func (runtimeFixture productionProvisionDockerRuntime) VerifyTrustedRepository(
	ctx context.Context,
	gitExecutable string,
	root productionBootstrapRoot,
	destination string,
) error {
	return verifyProductionProvisionTrustedRepository(ctx, gitExecutable, root, destination)
}

// super-dolphin-ci: platform=darwin
func TestProductionProvisionBootstrapOwnerHookDockerE2E(t *testing.T) {
	requireProductionProvisionDockerE2E(t)
	execution := prepareProductionProvisionDockerExecution(t)
	runProductionProvisionOwnerHook(
		t, execution.config, execution.dependencies, execution.fixture.repository.sourceRepo, execution.accepted,
	)
}

func requireProductionProvisionDockerE2E(t *testing.T) {
	t.Helper()
	if !productionProvisionDockerE2EEnabled() {
		t.Skip("production provision Docker E2E requires SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E=1")
	}
	if testing.Short() {
		t.Skip("production provision Docker E2E is disabled in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is unavailable")
	}
	if _, err := os.Stat("/usr/bin/codesign"); err != nil {
		t.Skip("macOS codesign is unavailable")
	}
}

func productionProvisionDockerE2EEnabled() bool {
	return os.Getenv(productionDockerE2EOptInEnv) == "1"
}

func TestProductionProvisionTruthRepositoryPreservesStagedTree(t *testing.T) {
	root := productionProvisionRepositoryRoot(t)
	want := productionGitLine(t, root, "write-tree")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, base)
	repository := prepareProductionProvisionTruthRepository(t, base)
	if repository.tree != want {
		t.Fatalf("fixture tree = %s, want staged tree %s", repository.tree, want)
	}
}

func assertProductionProvisionStartsEmpty(t *testing.T, config productionCoordinatorConfig) {
	t.Helper()
	acceptedPath := filepath.Join(config.AcceptedImageRoot, "accepted-image.json")
	if _, err := os.Stat(acceptedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provision seeded accepted image: %v", err)
	}
}

type productionProvisionDockerFixture struct {
	base       string
	controller string
	runtime    productionProvisionDockerRuntime
	candidate  gatecontract.ImageIdentity
	repository productionRepositoryFixture
	runner     gatecontract.ImageIdentity
	policy     string
	inputs     localci.GateImageInputs
}

func newProductionProvisionDockerFixture(t *testing.T) productionProvisionDockerFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, base)
	repository := prepareProductionProvisionTruthRepository(t, base)
	platform := "linux/" + runtime.GOARCH
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: repository.commit}, SourceTreeSHA: repository.tree,
	}
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, source)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := localci.LoadReadOnlyGitTree(context.Background(), repository.sourceRepo, source)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := localci.ResolveGateImageInputs(tree, plan.PolicyDigest, platform)
	if err != nil {
		t.Fatal(err)
	}
	candidate := buildProductionProvisionCandidateImage(t, inputs, plan.PolicyDigest)
	runner := buildProductionProvisionRunnerImage(t, candidate)
	controller := filepath.Join(makePrivateDirectory(t, base, "controller"), "super-dolphin-gate-controller")
	buildAndSignProductionProvisionController(t, controller)
	return productionProvisionDockerFixture{
		base: base, controller: controller, runtime: productionProvisionDockerRuntime{sourceRepository: repository.trustedRepository},
		candidate: candidate, repository: repository, runner: runner, policy: plan.PolicyDigest, inputs: inputs,
	}
}

func prepareProductionProvisionTruthRepository(t *testing.T, base string) productionRepositoryFixture {
	t.Helper()
	root := productionProvisionRepositoryRoot(t)
	tree := productionGitLine(t, root, "write-tree")
	parent := productionGitLine(t, root, "rev-parse", "HEAD^{commit}")
	// The candidate commit must inherit the trusted worktree head. Release whitespace checks
	// therefore audit the intended parent-to-candidate delta rather than the entire history.
	commit := productionGitLine(
		t, root, "-c", "user.name=Provision E2E", "-c", "user.email=provision-e2e@example.invalid",
		"commit-tree", tree, "-p", parent, "-m", "production provision Docker E2E",
	)
	sourceRepo := filepath.Join(base, "source")
	runProductionGit(t, "", "init", "-q", "-b", "main", "--", sourceRepo)
	runProductionGit(t, sourceRepo, "fetch", "-q", "--", root, commit)
	runProductionGit(t, sourceRepo, "checkout", "-q", "-B", "main", commit)
	checkedOutTree := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{tree}")
	if checkedOutTree != tree {
		t.Fatalf("Docker E2E checkout tree = %s, want staged tree %s", checkedOutTree, tree)
	}
	if checkedOutParent := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^"); checkedOutParent != parent {
		t.Fatalf("Docker E2E checkout parent = %s, want trusted parent %s", checkedOutParent, parent)
	}
	trustedRepository := filepath.Join(base, "trusted.git")
	runProductionGit(t, "", "clone", "-q", "--bare", "--", sourceRepo, trustedRepository)
	chmodPrivate(t, trustedRepository)
	return productionRepositoryFixture{
		sourceRepo: sourceRepo, trustedRepository: trustedRepository,
		commit: productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{commit}"),
		tree:   productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{tree}"),
	}
}

func buildProductionProvisionCandidateImage(
	t *testing.T,
	inputs localci.GateImageInputs,
	policyDigest string,
) gatecontract.ImageIdentity {
	t.Helper()
	contextRoot := privateProductionBootstrapDirectory(t)
	repository := productionProvisionLocalImageRepository("candidate", contextRoot)
	tag := repository + ":fixture"
	root := productionProvisionRepositoryRoot(t)
	metadataPath := filepath.Join(contextRoot, "metadata.json")
	runtimeDepsLayout := buildProductionRuntimeDepsOCI(t, contextRoot, root, inputs)
	runBootstrapDocker(t, productionProvisionCandidateBuildArguments(
		root, tag, metadataPath, runtimeDepsLayout, policyDigest, inputs,
	)...)
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
		t.Fatalf("read production candidate manifest digest: %v", err)
	}
	document := inspectProductionBootstrapFixtureImage(t, tag)
	return productionBootstrapFixtureIdentity(repository, digest, document)
}

func productionProvisionCandidateBuildArguments(
	repositoryRoot string,
	tag string,
	metadataPath string,
	runtimeDepsLayout string,
	policyDigest string,
	inputs localci.GateImageInputs,
) []string {
	return []string{
		"buildx", "build", "--load", "--provenance=false", "--network=none",
		"--platform=" + inputs.Platform, "--file=" + filepath.Join(repositoryRoot, "build/gate/Dockerfile"),
		"--tag=" + tag, "--metadata-file=" + metadataPath,
		"--build-context=runtime-deps=oci-layout://" + runtimeDepsLayout,
		"--build-arg=RUNTIME_DEPS_IMAGE=runtime-deps",
		"--label=org.super-dolphin.policy-sha=" + policyDigest,
		"--label=org.super-dolphin.source-tree-sha=" + inputs.SubmittedSourceTree,
		"--label=org.super-dolphin.image-input-digest=" + inputs.ImageInputDigest,
		"--label=org.super-dolphin.toolchain-digest=" + inputs.ToolchainDigest,
		"--label=org.super-dolphin.schema-version=" + inputs.ImageSchemaVersion,
		repositoryRoot,
	}
}

func TestProductionProvisionCandidateBuildUsesNodeLocalRuntimeDeps(t *testing.T) {
	inputs := localci.GateImageInputs{
		Platform: "linux/arm64", SubmittedSourceTree: "tree", ImageInputDigest: "inputs",
		ToolchainDigest: "toolchain", ImageSchemaVersion: "1",
	}
	arguments := productionProvisionCandidateBuildArguments(
		"/private/source", "candidate:fixture", "/private/metadata.json", "/private/runtime-deps.oci", "policy", inputs,
	)
	for _, want := range []string{
		"--build-context=runtime-deps=oci-layout:///private/runtime-deps.oci",
		"--build-arg=RUNTIME_DEPS_IMAGE=runtime-deps",
	} {
		if !slices.Contains(arguments, want) {
			t.Fatalf("candidate build arguments missing %q: %#v", want, arguments)
		}
	}
	for _, argument := range arguments {
		if strings.Contains(argument, "ghcr.io/") || strings.Contains(argument, "RUNTIME_DEPS_IMAGE=@") {
			t.Fatalf("candidate build retained remote runtime dependency reference: %q", argument)
		}
	}
}

func buildProductionRuntimeDepsOCI(
	t *testing.T,
	contextRoot string,
	repositoryRoot string,
	inputs localci.GateImageInputs,
) string {
	t.Helper()
	layout := filepath.Join(contextRoot, "runtime-deps.oci")
	arguments := []string{
		"buildx", "build", "--provenance=false", "--network=default",
		"--platform=" + inputs.Platform, "--file=" + filepath.Join(repositoryRoot, "build/gate/runtime-deps.Dockerfile"),
		"--output=type=oci,dest=" + layout + ",tar=false",
	}
	arguments = append(arguments, productionRuntimeDepsBuildArguments(t, inputs)...)
	arguments = append(arguments, repositoryRoot)
	runBootstrapDocker(t, arguments...)
	return layout
}

func productionRuntimeDepsBuildArguments(t *testing.T, inputs localci.GateImageInputs) []string {
	t.Helper()
	var data []byte
	for _, entry := range inputs.SourceEntries {
		if entry.Path == "build/gate/toolchain.lock" {
			if data != nil {
				t.Fatal("toolchain lock appears more than once in gate image inputs")
			}
			data = entry.Data
		}
	}
	if data == nil {
		t.Fatal("toolchain lock is missing from gate image inputs")
	}
	var lock struct {
		BaseImages []struct {
			Name      string `json:"name"`
			Reference string `json:"reference"`
		} `json:"base_images"`
		RuntimeTools struct {
			SqruffArtifacts []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
				SHA256   string `json:"sha256"`
			} `json:"sqruff_artifacts"`
		} `json:"runtime_tools"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("decode toolchain lock: %v", err)
	}
	arguments := make([]string, 0, len(lock.BaseImages)+4)
	for _, image := range lock.BaseImages {
		arguments = append(arguments, "--build-arg="+image.Name+"="+image.Reference)
	}
	for _, artifact := range lock.RuntimeTools.SqruffArtifacts {
		switch artifact.Platform {
		case "linux/amd64":
			arguments = append(arguments, "--build-arg=SQRUFF_ARCHIVE_URL_AMD64="+artifact.URL, "--build-arg=SQRUFF_ARCHIVE_SHA256_AMD64="+artifact.SHA256)
		case "linux/arm64":
			arguments = append(arguments, "--build-arg=SQRUFF_ARCHIVE_URL_ARM64="+artifact.URL, "--build-arg=SQRUFF_ARCHIVE_SHA256_ARM64="+artifact.SHA256)
		default:
			t.Fatalf("unsupported Sqruff artifact platform %q", artifact.Platform)
		}
	}
	return arguments
}

func productionProvisionRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(directory, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildProductionProvisionRunnerImage(
	t *testing.T,
	candidate gatecontract.ImageIdentity,
) gatecontract.ImageIdentity {
	t.Helper()
	contextRoot := privateProductionBootstrapDirectory(t)
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	sourceText := `package main
import ("encoding/base64"; "encoding/json"; "os")
func main() {
	data, err := base64.RawStdEncoding.DecodeString(os.Getenv("SUPER_DOLPHIN_BOOTSTRAP_REQUEST_B64")); if err != nil { panic(err) }
	var request struct { CandidateRegistry string ` + "`json:\"candidate_registry\"`" + ` }; if json.Unmarshal(data, &request) != nil { panic("request") }
	var image map[string]any; if json.Unmarshal([]byte(` + strconv.Quote(string(candidateJSON)) + `), &image) != nil { panic("image") }
	if image["registry"] != request.CandidateRegistry { panic("registry") }
	if json.NewEncoder(os.Stdout).Encode(map[string]any{"schema_version": 1, "image": image}) != nil { panic("result") }
}
`
	source := filepath.Join(contextRoot, "runner.go")
	if err := os.WriteFile(source, []byte(sourceText), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(contextRoot, "bootstrap-runner")
	buildProductionProvisionLinuxBinary(t, source, binary)
	if err := os.WriteFile(filepath.Join(contextRoot, "Dockerfile"), []byte(
		"FROM scratch\nCOPY bootstrap-runner /usr/local/bin/super-dolphin-bootstrap\n"+
			"ENTRYPOINT [\"/usr/local/bin/super-dolphin-bootstrap\"]\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := productionProvisionLocalImageRepository("runner", contextRoot)
	tag := repository + ":fixture"
	digest := buildProductionBootstrapFixtureImage(t, contextRoot, tag)
	document := inspectProductionBootstrapFixtureImage(t, tag)
	return productionBootstrapFixtureIdentity(repository, digest, document)
}

func buildProductionProvisionLinuxBinary(t *testing.T, source string, destination string) {
	t.Helper()
	command := exec.Command("go", "build", "-trimpath", "-o", destination, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Linux Docker E2E binary: %v: %s", err, strings.TrimSpace(string(output)))
	}
}

func productionProvisionLocalImageRepository(kind string, root string) string {
	digest := sha256.Sum256([]byte(root))
	return "local/super-dolphin-provision-" + kind + "-" + hex.EncodeToString(digest[:6])
}

func productionProvisionDockerManifest(
	t *testing.T,
	base string,
	controller string,
	repository productionRepositoryFixture,
	runner gatecontract.ImageIdentity,
	candidateRegistry string,
	policyDigest string,
	inputs localci.GateImageInputs,
) (productionProvisionManifest, productionBootstrapRoot) {
	t.Helper()
	releaseInputs := makePrivateDirectory(t, base, "release-inputs")
	bootstrapPublic, bootstrapPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapSigner := gatecontract.SignerIdentity{
		KeyID: "provision-docker-bootstrap", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	root, trust, releasePrivate := productionBootstrapRootForFixture(
		t,
		productionCoordinatorConfig{
			RepoID: "example/repository", TrustedRef: "refs/heads/main", BootstrapControllerFile: controller,
		},
		repository.commit, repository.tree, bootstrapSigner, bootstrapPublic,
	)
	root.PolicyDigest = policyDigest
	root.ImageInputDigest = inputs.ImageInputDigest
	root.ToolchainDigest = inputs.ToolchainDigest
	root.ImageSchemaVersion = inputs.ImageSchemaVersion
	root.CandidateRegistry = candidateRegistry
	root.Runner = runner
	root.Controller.BinaryDigest = productionBootstrapFileDigest(t, controller)
	root.Controller.DesignatedRequirement = productionBootstrapDesignatedRequirement(t, controller)
	rootFile := filepath.Join(releaseInputs, "bootstrap-root.json")
	writeProductionBootstrapRootFixture(t, rootFile, root, releasePrivate)
	bootstrapKey := filepath.Join(releaseInputs, "bootstrap-key.json")
	writePrivateJSON(t, bootstrapKey, productionBootstrapControllerPrivateKey{
		Signer: bootstrapSigner, PrivateKey: base64.StdEncoding.EncodeToString(bootstrapPrivate),
	})
	launcherRoot := makePrivateDirectory(t, base, "bin")
	if err := os.Chmod(launcherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(launcherRoot, "super-dolphin-gate")
	manifest := productionProvisionManifest{
		SchemaVersion: productionProvisionSchemaVersion, InstallRoot: filepath.Join(base, "installed"),
		LauncherPath: launcherPath, ControllerBinary: controller,
		BootstrapRootFile: rootFile, BootstrapControllerKeyFile: bootstrapKey,
		ReceiptKeyFile:     writeProductionProvisionTestAuthority(t, releaseInputs, "receipt", "provision-docker-receipt"),
		ActionGrantKeyFile: writeProductionProvisionTestAuthority(t, releaseInputs, "grant", "provision-docker-grant"),
		SeccompProfile:     writeProductionProvisionDockerSeccomp(t, releaseInputs),
		TrustedSourceRoot:  prepareProductionTrustedSourceRoot(t),
		Platform:           runner.OS + "/" + runner.Architecture, TrustedRootKeys: []productionTrustedKey{trust},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 5_000, ActionGrantTTLSeconds: 60,
		ShardsPerJob: 3, MaxActiveCIWorkloads: 3,
	}
	return manifest, root
}

func buildAndSignProductionProvisionController(t *testing.T, destination string) {
	t.Helper()
	command := exec.Command("go", "build", "-trimpath", "-o", destination, ".")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build production provision controller: %v: %s", err, strings.TrimSpace(string(output)))
	}
	command = exec.Command(
		"/usr/bin/codesign", "--force", "--sign", "-", "--identifier",
		"com.super-dolphin.bootstrap.provision-e2e", destination,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("codesign production provision controller: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Chmod(destination, 0o500); err != nil {
		t.Fatal(err)
	}
}

func writeProductionProvisionDockerSeccomp(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "seccomp.json")
	if err := os.WriteFile(path, []byte("{\"defaultAction\":\"SCMP_ACT_ALLOW\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runProductionProvisionOwnerHook(
	t *testing.T,
	config productionCoordinatorConfig,
	dependencies coordinatorDependencies,
	repository string,
	accepted gatecontract.AcceptedImageRecord,
) {
	t.Helper()
	checkpoint := coordinatorTestCheckpoint(t)
	serveResult := startProductionProvisionOwner(t, checkpoint, dependencies)
	connector := productionProvisionHookConnector(config, checkpoint)
	tree := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	hookErr := runHookWithConnector(
		[]string{"pre-commit", "--tree", tree}, bytes.NewReader(nil), &bytes.Buffer{}, repository, connector,
	)
	if hookErr == nil {
		assertSuccessfulProductionProvisionHook(t, config, checkpoint, accepted)
		return
	}
	waitProductionProvisionReservation(t, config, checkpoint, accepted, hookErr, serveResult)
}

func startProductionProvisionOwner(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	dependencies coordinatorDependencies,
) <-chan error {
	t.Helper()
	owner, err := openCoordinatorOwner(context.Background(), checkpoint, dependencies)
	if err != nil {
		t.Fatalf("openCoordinatorOwner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	group := errgroup.Group{}
	serveResult := make(chan error, 1)
	group.Go(func() error {
		err := owner.Serve(ctx)
		serveResult <- err
		return err
	})
	t.Cleanup(func() {
		cancel()
		if err := group.Wait(); err != nil {
			t.Errorf("production provision owner Serve() error = %v", err)
		}
	})
	return serveResult
}

func assertSuccessfulProductionProvisionHook(
	t *testing.T,
	config productionCoordinatorConfig,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	accepted gatecontract.AcceptedImageRecord,
) {
	t.Helper()
	status, err := productionProvisionOwnerStatus(checkpoint)
	if err != nil {
		t.Fatalf("read passed production provision owner status: %v; evidence=%s", err, productionProvisionOwnerEvidence(checkpoint))
	}
	if status.State != jobStatePassed || !status.Terminal {
		t.Fatalf("production provision hook returned success before terminal pass: %#v; evidence=%s", status, productionProvisionOwnerEvidence(checkpoint))
	}
	_ = assertProductionProvisionOwnerTerminal(
		t, config, checkpoint, status.JobID, accepted, gatecontract.ProfileLocalFast,
	)
}

func waitProductionProvisionReservation(
	t *testing.T,
	config productionCoordinatorConfig,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	accepted gatecontract.AcceptedImageRecord,
	hookErr error,
	serveResult <-chan error,
) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := productionProvisionOwnerStatus(checkpoint)
		if err != nil {
			t.Fatalf(
				"read production provision owner status: %v; hook=%v; evidence=%s",
				err, hookErr, productionProvisionOwnerEvidence(checkpoint),
			)
		}
		if status.State == jobStateStarted || status.State == jobStatePassed {
			_ = assertProductionProvisionOwnerTerminal(
				t, config, checkpoint, status.JobID, accepted, gatecontract.ProfileLocalFast,
			)
			return
		}
		if status.Terminal {
			t.Fatalf("production provision owner reached %s: %v; evidence=%s", status.State, hookErr, productionProvisionOwnerEvidence(checkpoint))
		}
		select {
		case err := <-serveResult:
			t.Fatalf("production provision owner exited before reservation: %v; hook=%v", err, hookErr)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"production provision owner did not reserve submitted hook job: %v; evidence=%s",
		hookErr, productionProvisionOwnerEvidence(checkpoint),
	)
}

func assertProductionProvisionOwnerTerminal(
	t *testing.T,
	config productionCoordinatorConfig,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	jobID string,
	accepted gatecontract.AcceptedImageRecord,
	profile gatecontract.Profile,
) coordinatorJobRecord {
	t.Helper()
	connectCtx, connectCancel := context.WithTimeout(context.Background(), time.Second)
	defer connectCancel()
	metadataClient, err := dialCoordinator(connectCtx, checkpoint)
	if err != nil {
		t.Fatalf("dial production provision owner for terminal wait: %v", err)
	}
	record := waitProductionProvisionExecutionDeadline(t, metadataClient, checkpoint, jobID, profile)
	if err := metadataClient.Close(); err != nil {
		t.Fatalf("close production provision metadata client: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), record.Deadline.Add(30*time.Second))
	defer cancel()
	client, err := dialCoordinator(ctx, checkpoint)
	if err != nil {
		t.Fatalf("redial production provision owner for terminal wait: %v", err)
	}
	defer client.Close()
	terminal, err := client.Wait(ctx, jobID)
	assertProductionProvisionTerminalStatus(t, checkpoint, client, terminal, err)
	record, err = client.store.job(ctx, jobID)
	if err != nil {
		t.Fatalf("load production provision terminal record: %v", err)
	}
	assertProductionProvisionReceiptCoverage(t, record, terminal)
	assertProductionProvisionReceiptIdentity(t, record, accepted)
	authority, err := newProductionHookResultReceiptAuthority(ctx, config)
	if err != nil {
		t.Fatalf("open production result receipt authority: %v", err)
	}
	if err := authority.VerifyCurrentResultReceipt(ctx, *record.Receipt); err != nil {
		t.Fatalf("verify production result receipt: %v", err)
	}
	return record
}

func assertProductionProvisionTerminalStatus(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	client *coordinatorTransportClient,
	terminal jobStatus,
	err error,
) {
	t.Helper()
	if err != nil || terminal.State != jobStatePassed || !terminal.Terminal {
		failureLogs := productionProvisionFailureLogs(t, client, terminal)
		t.Fatalf(
			"production provision owner terminal status=%#v error=%v; gate_logs=%#v; evidence=%s",
			terminal, err, failureLogs, productionProvisionOwnerEvidence(checkpoint),
		)
	}
}

func assertProductionProvisionReceiptCoverage(t *testing.T, record coordinatorJobRecord, terminal jobStatus) {
	t.Helper()
	if record.Receipt == nil || record.ReceiptID == "" || record.ReceiptID != terminal.ReceiptID ||
		len(record.GateResults) != len(record.Plan.Gates) || len(record.Receipt.GateResults) != len(record.Plan.Gates) {
		t.Fatalf("production provision terminal receipt or gate coverage is incomplete: %#v", record)
	}
}

func assertProductionProvisionReceiptIdentity(
	t *testing.T,
	record coordinatorJobRecord,
	accepted gatecontract.AcceptedImageRecord,
) {
	t.Helper()
	if record.Receipt.Generation != accepted.Generation || !reflect.DeepEqual(record.Receipt.Image, accepted.Image) ||
		!reflect.DeepEqual(record.Receipt.Runner, accepted.Runner) ||
		!record.Receipt.Container.Removed || !record.Receipt.Container.NetworkRemoved {
		t.Fatalf("production provision receipt drifted from accepted execution identity: %#v", record.Receipt)
	}
}

func waitProductionProvisionExecutionDeadline(
	t *testing.T,
	client *coordinatorTransportClient,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	jobID string,
	profile gatecontract.Profile,
) coordinatorJobRecord {
	t.Helper()
	deadline := time.Now().Add(coordinatorTimeout(profile))
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		record, err := client.store.job(ctx, jobID)
		cancel()
		if err != nil {
			t.Fatalf("load started production provision record: %v", err)
		}
		ready, err := productionProvisionExecutionDeadlineReady(record, profile)
		if err != nil {
			t.Fatalf("production provision normal timeout contract drifted: %v; record=%#v", err, record)
		}
		if ready {
			return record
		}
		if record.State.terminal() {
			t.Fatalf("production provision owner reached %s before container execution; evidence=%s", record.State, productionProvisionOwnerEvidence(checkpoint))
		}
		if time.Now().After(deadline) {
			t.Fatalf("production provision execution did not start within the normal post-reservation budget; evidence=%s", productionProvisionOwnerEvidence(checkpoint))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func productionProvisionExecutionDeadlineReady(
	record coordinatorJobRecord,
	profile gatecontract.Profile,
) (bool, error) {
	if record.StartedAt == nil && record.Deadline == nil {
		return false, nil
	}
	if record.StartedAt == nil || record.Deadline == nil {
		return false, errors.New("started_at and deadline must be persisted together")
	}
	if !record.Deadline.Equal(record.StartedAt.Add(coordinatorTimeout(profile))) {
		return false, fmt.Errorf("deadline does not match the %s timeout", profile)
	}
	return true, nil
}

func TestProductionProvisionExecutionDeadlineObservation(t *testing.T) {
	started := time.Now().UTC()
	deadline := started.Add(coordinatorTimeout(gatecontract.ProfileLocalFast))
	releaseDeadline := started.Add(coordinatorTimeout(gatecontract.ProfileRelease))
	for _, test := range []struct {
		name    string
		record  coordinatorJobRecord
		profile gatecontract.Profile
		ready   bool
		wantErr bool
	}{
		{name: "reserved before container start", record: coordinatorJobRecord{State: jobStateStarted}, profile: gatecontract.ProfileLocalFast},
		{name: "partial lifecycle evidence", record: coordinatorJobRecord{State: jobStateStarted, StartedAt: &started}, profile: gatecontract.ProfileLocalFast, wantErr: true},
		{name: "normal deadline", record: coordinatorJobRecord{State: jobStateStarted, StartedAt: &started, Deadline: &deadline}, profile: gatecontract.ProfileLocalFast, ready: true},
		{
			name: "release deadline",
			record: coordinatorJobRecord{
				State: jobStateStarted, StartedAt: &started,
				Deadline: &releaseDeadline,
			},
			profile: gatecontract.ProfileRelease, ready: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ready, err := productionProvisionExecutionDeadlineReady(test.record, test.profile)
			if ready != test.ready || (err != nil) != test.wantErr {
				t.Fatalf("productionProvisionExecutionDeadlineReady() = (%v, %v), want (%v, error=%v)", ready, err, test.ready, test.wantErr)
			}
		})
	}
}
