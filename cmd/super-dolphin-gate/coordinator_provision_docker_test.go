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

func (runtimeFixture productionProvisionDockerRuntime) VerifyRunner(
	ctx context.Context,
	identity gatecontract.ImageIdentity,
) error {
	return (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(ctx, identity)
}

func (runtimeFixture productionProvisionDockerRuntime) CloneTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	destination string,
) error {
	command := exec.CommandContext(
		ctx, "git", "clone", "-q", "--bare", "--", runtimeFixture.sourceRepository, destination,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("clone Docker E2E trusted repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := productionProvisionGitLine(ctx, destination, "remote", "set-url", "origin", root.RemoteURL); err != nil {
		return err
	}
	commit, err := productionProvisionGitLine(ctx, destination, "rev-parse", "--verify", root.TrustedRef+"^{commit}")
	if err != nil || commit != root.BaselineCommit {
		return errors.Join(errors.New("Docker E2E trusted commit drifted"), err)
	}
	tree, err := productionProvisionGitLine(ctx, destination, "rev-parse", "--verify", root.BaselineCommit+"^{tree}")
	if err != nil || tree != root.BaselineTree {
		return errors.Join(errors.New("Docker E2E trusted tree drifted"), err)
	}
	return os.Chmod(destination, 0o700)
}

func (runtimeFixture productionProvisionDockerRuntime) VerifyTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	destination string,
) error {
	return verifyProductionProvisionTrustedRepository(ctx, root, destination)
}

func TestProductionProvisionBootstrapOwnerHookDockerE2E(t *testing.T) {
	requireProductionProvisionDockerE2E(t)
	fixture := newProductionProvisionDockerFixture(t)
	result, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, fixture.runtime)
	if err != nil {
		t.Fatalf("provisionProductionWithRuntime() error = %v", err)
	}
	config, err := loadProductionCoordinatorConfigFile(result.ProductionConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	assertProductionProvisionStartsEmpty(t, config)
	t.Setenv(productionCoordinatorConfigEnv, result.ProductionConfigFile)
	dependencies, err := productionCoordinatorDependencies(context.Background())
	if err != nil {
		t.Fatalf("productionCoordinatorDependencies() bootstrap error = %v", err)
	}
	loader, accepted, err := newProductionAcceptedImageLoader(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if loader == nil || accepted.Generation != 1 || !reflect.DeepEqual(accepted.Image, fixture.candidate) {
		t.Fatalf("generation-one accepted record = %#v", accepted)
	}
	runProductionProvisionOwnerHook(t, config, dependencies, fixture.repository.sourceRepo)
}

func requireProductionProvisionDockerE2E(t *testing.T) {
	t.Helper()
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

func assertProductionProvisionStartsEmpty(t *testing.T, config productionCoordinatorConfig) {
	t.Helper()
	acceptedPath := filepath.Join(config.AcceptedImageRoot, "accepted-image.json")
	if _, err := os.Stat(acceptedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provision seeded accepted image: %v", err)
	}
}

type productionProvisionDockerFixture struct {
	manifest   productionProvisionManifest
	runtime    productionProvisionDockerRuntime
	candidate  gatecontract.ImageIdentity
	repository productionRepositoryFixture
}

func newProductionProvisionDockerFixture(t *testing.T) productionProvisionDockerFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, base)
	repository := prepareProductionRepository(t, base)
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
	manifest, root := productionProvisionDockerManifest(
		t, base, repository, runner, candidate.Registry, plan.PolicyDigest, inputs,
	)
	if root.BootstrapPublicKey == root.Ed25519PublicKey || root.BootstrapSigner == root.Signer {
		t.Fatal("Docker E2E root reused release trust as bootstrap authority")
	}
	return productionProvisionDockerFixture{
		manifest: manifest, runtime: productionProvisionDockerRuntime{sourceRepository: repository.trustedRepository},
		candidate: candidate, repository: repository,
	}
}

func buildProductionProvisionCandidateImage(
	t *testing.T,
	inputs localci.GateImageInputs,
	policyDigest string,
) gatecontract.ImageIdentity {
	t.Helper()
	contextRoot := privateProductionBootstrapDirectory(t)
	source := filepath.Join(contextRoot, "gate.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(contextRoot, "gate")
	buildProductionProvisionLinuxBinary(t, source, binary)
	dockerfile := "FROM scratch\nCOPY gate /usr/local/bin/super-dolphin-gate-executor\n" +
		"LABEL org.super-dolphin.policy-sha=" + policyDigest + "\n" +
		"LABEL org.super-dolphin.source-tree-sha=" + inputs.SubmittedSourceTree + "\n" +
		"LABEL org.super-dolphin.image-input-digest=" + inputs.ImageInputDigest + "\n" +
		"LABEL org.super-dolphin.toolchain-digest=" + inputs.ToolchainDigest + "\n" +
		"LABEL org.super-dolphin.schema-version=" + inputs.ImageSchemaVersion + "\n"
	if err := os.WriteFile(filepath.Join(contextRoot, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := productionProvisionLocalImageRepository("candidate", contextRoot)
	tag := repository + ":fixture"
	digest := buildProductionBootstrapFixtureImage(t, contextRoot, tag)
	document := inspectProductionBootstrapFixtureImage(t, tag)
	return productionBootstrapFixtureIdentity(repository, digest, document)
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
	repository productionRepositoryFixture,
	runner gatecontract.ImageIdentity,
	candidateRegistry string,
	policyDigest string,
	inputs localci.GateImageInputs,
) (productionProvisionManifest, productionBootstrapRoot) {
	t.Helper()
	releaseInputs := makePrivateDirectory(t, base, "release-inputs")
	controller := filepath.Join(releaseInputs, "super-dolphin-gate-controller")
	buildAndSignProductionProvisionController(t, controller)
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
	manifest := productionProvisionManifest{
		SchemaVersion: productionProvisionSchemaVersion, InstallRoot: filepath.Join(base, "installed"),
		LauncherPath: filepath.Join(launcherRoot, "super-dolphin-gate"), ControllerBinary: controller,
		BootstrapRootFile: rootFile, BootstrapControllerKeyFile: bootstrapKey,
		ReceiptKeyFile:     writeProductionProvisionTestAuthority(t, releaseInputs, "receipt", "provision-docker-receipt"),
		ActionGrantKeyFile: writeProductionProvisionTestAuthority(t, releaseInputs, "grant", "provision-docker-grant"),
		SeccompProfile:     writeProductionProvisionDockerSeccomp(t, releaseInputs),
		TrustedSourceRoot:  prepareProductionTrustedSourceRoot(t),
		Platform:           runner.OS + "/" + runner.Architecture, TrustedRootKeys: []productionTrustedKey{trust},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 20, ActionGrantTTLSeconds: 60,
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
) {
	t.Helper()
	checkpoint := coordinatorTestCheckpoint(t)
	owner, err := openCoordinatorOwner(context.Background(), checkpoint, dependencies)
	if err != nil {
		t.Fatalf("openCoordinatorOwner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	group := errgroup.Group{}
	group.Go(func() error { return owner.Serve(ctx) })
	t.Cleanup(func() {
		cancel()
		if err := group.Wait(); err != nil {
			t.Errorf("production provision owner Serve() error = %v", err)
		}
	})
	connector := productionProvisionHookConnector(config, checkpoint)
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = runHookWithConnector(
			[]string{"pre-commit"}, bytes.NewReader(nil), &bytes.Buffer{}, repository, connector,
		)
		if lastErr == nil {
			return
		}
		if strings.Contains(lastErr.Error(), "infra_failed") {
			t.Fatalf(
				"production provision hook infrastructure failure: %v; evidence=%s",
				lastErr, productionProvisionOwnerEvidence(checkpoint),
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("production provision hook did not pass: %v", lastErr)
}

func productionProvisionOwnerEvidence(checkpoint localci.DockerDaemonIdentityCheckpoint) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := dialCoordinator(ctx, checkpoint)
	if err != nil {
		return err.Error()
	}
	defer client.Close()
	records, err := client.store.jobs(ctx)
	if err != nil {
		return err.Error()
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func productionProvisionHookConnector(
	config productionCoordinatorConfig,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) hookCoordinatorConnector {
	return func(ctx context.Context) (hookCoordinator, error) {
		client, err := dialCoordinator(ctx, checkpoint)
		if err != nil {
			return nil, err
		}
		planner, err := newProductionCandidateSubmissionPlanner(ctx, config)
		if err != nil {
			return nil, errors.Join(err, client.Close())
		}
		client.candidatePlanner = planner
		authority, err := newProductionHookResultReceiptAuthority(ctx, config)
		if err != nil {
			return nil, errors.Join(err, client.Close())
		}
		grants, err := newProductionActionGrantService(config, client.store, authority)
		if err != nil {
			return nil, errors.Join(err, client.Close())
		}
		return &hookCoordinatorBridge{client: client, authority: authority, grants: grants}, nil
	}
}
