package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type productionTestFixture struct {
	config              productionCoordinatorConfig
	configPath          string
	signer              gatecontract.SignerIdentity
	privateKey          ed25519.PrivateKey
	bootstrapRootKey    ed25519.PrivateKey
	receiptKey          ed25519.PrivateKey
	grantKey            ed25519.PrivateKey
	commit              string
	tree                string
	acceptedInputDigest string
	sourceRepo          string
}

type productionCandidatePromotionFixture struct {
	production      *productionTestFixture
	authority       *productionPromotionAuthority
	controller      *localci.PromotionController
	tree            localci.ReadOnlyGitTree
	accepted        gatecontract.AcceptedImageRecord
	candidateCommit string
}

type productionRepositoryFixture struct {
	sourceRepo        string
	trustedRepository string
	commit            string
	tree              string
}

type productionAuthorityFixture struct {
	signer            gatecontract.SignerIdentity
	publicKey         ed25519.PublicKey
	privateKey        ed25519.PrivateKey
	receiptSigner     gatecontract.SignerIdentity
	receiptPublicKey  ed25519.PublicKey
	receiptPrivateKey ed25519.PrivateKey
	receiptKeyPath    string
	grantSigner       gatecontract.SignerIdentity
	grantPublicKey    ed25519.PublicKey
	grantPrivateKey   ed25519.PrivateKey
	grantKeyPath      string
	promotionKeyPath  string
}

type capturingTruthImageEnsurer struct {
	request localci.TruthImageEnsureRequest
	result  localci.TruthImageEnsureResult
}

func (ensurer *capturingTruthImageEnsurer) EnsureImage(
	_ context.Context,
	request localci.TruthImageEnsureRequest,
) (localci.TruthImageEnsureResult, error) {
	ensurer.request = request
	return ensurer.result, nil
}

type capturingFreshContainerRunner struct {
	request localci.FreshContainerRequest
}

type productionBuildxRunnerStub struct {
	recovered bool
}

func (*productionBuildxRunnerStub) Build(context.Context, localci.BuildKitBuildRequest) (localci.BuildKitResult, error) {
	return localci.BuildKitResult{}, errors.New("unexpected candidate image build")
}

func (runner *productionBuildxRunnerStub) RecoverControlledBuilders(context.Context) error {
	runner.recovered = true
	return nil
}

type productionBuildKitRunnerStub struct {
	digest string
	err    error
}

func (runner productionBuildKitRunnerStub) Build(
	context.Context,
	localci.BuildKitBuildRequest,
) (localci.BuildKitResult, error) {
	return localci.BuildKitResult{PlatformManifestDigest: runner.digest, ConfigDigest: productionDigest("9")}, runner.err
}

type productionCandidateIdentityResolverStub struct{}

func (productionCandidateIdentityResolverStub) ResolveCandidateIdentity(
	_ context.Context,
	_ localci.PromotionCandidate,
	result localci.CandidateResult,
) (gatecontract.ImageIdentity, error) {
	return gatecontract.ImageIdentity{
		Registry: "docker.io/library/super-dolphin-gate-local", OCIIndexDigest: result.ImageDigest,
		PlatformManifestDigest: result.ImageDigest, ConfigDigest: result.ConfigDigest,
		RootFSDiffIDs: []string{productionDigest("a")}, OS: "linux", Architecture: "arm64",
	}, nil
}

func (runner *capturingFreshContainerRunner) RunFreshContainer(
	_ context.Context,
	request localci.FreshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.request = request
	return localci.FreshContainerResult{Status: gatecontract.ResultStatusPassed}, nil
}

func (*capturingFreshContainerRunner) RecoverFreshContainer(
	context.Context,
	localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerResult, error) {
	return localci.FreshContainerResult{}, errors.New("unexpected container recovery")
}

func (*capturingFreshContainerRunner) ProbeFreshContainerRecovery(
	context.Context,
	localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerRecoveryObservation, error) {
	return localci.FreshContainerRecoveryObservation{}, errors.New("unexpected container recovery probe")
}

func (*capturingFreshContainerRunner) CleanupUnprovedFreshContainer(
	context.Context,
	localci.FreshContainerCleanupRequest,
) (localci.FreshContainerResult, error) {
	return localci.FreshContainerResult{}, errors.New("unexpected container recovery cleanup")
}

func TestProductionCoordinatorDependenciesAssembleRealAdapters(t *testing.T) {
	fixture := newProductionTestFixture(t)
	t.Setenv(productionCoordinatorConfigEnv, fixture.configPath)
	buildx := &productionBuildxRunnerStub{}

	dependencies, err := productionCoordinatorDependenciesWithBuildx(
		context.Background(),
		func(root string) (productionBuildxRunner, error) {
			if root != fixture.config.CandidateBuildRoot {
				t.Fatalf("candidate build root = %q", root)
			}
			return buildx, nil
		},
	)
	if err != nil {
		t.Fatalf("productionCoordinatorDependencies() error = %v", err)
	}
	if !buildx.recovered {
		t.Fatal("controlled buildx recovery was not required before adapter assembly")
	}
	if _, ok := dependencies.ImageEnsurer.(*productionImageEnsurer); !ok {
		t.Fatalf("ImageEnsurer = %T", dependencies.ImageEnsurer)
	}
	if _, ok := dependencies.CandidateBuilder.(*productionCandidateBuildService); !ok {
		t.Fatalf("CandidateBuilder = %T", dependencies.CandidateBuilder)
	}
	if _, ok := dependencies.PromotionWatcher.(*localci.PromotionController); !ok {
		t.Fatalf("PromotionWatcher = %T", dependencies.PromotionWatcher)
	}
	if _, ok := dependencies.SourceMaterializer.(*productionSourceMaterializer); !ok {
		t.Fatalf("SourceMaterializer = %T", dependencies.SourceMaterializer)
	}
	if _, ok := dependencies.FreshRunner.(*productionFreshContainerRunner); !ok {
		t.Fatalf("FreshRunner = %T", dependencies.FreshRunner)
	}
	if _, ok := dependencies.ReceiptSigner.(*ed25519ResultReceiptSigner); !ok {
		t.Fatalf("ReceiptSigner = %T", dependencies.ReceiptSigner)
	}
}

func TestProductionSignatureVerifierChecksRealEd25519Signature(t *testing.T) {
	fixture := newProductionTestFixture(t)
	verifier, err := newProductionSignatureVerifier(fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatalf("newProductionSignatureVerifier() error = %v", err)
	}
	payload := []byte("accepted image payload")
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, payload))
	if err := verifier.VerifyAcceptedImage(context.Background(), fixture.signer, payload, signature); err != nil {
		t.Fatalf("VerifyAcceptedImage() error = %v", err)
	}
	if err := verifier.VerifyAcceptedImage(context.Background(), fixture.signer, []byte("tampered"), signature); err == nil {
		t.Fatal("VerifyAcceptedImage() accepted tampered payload")
	}
}

func TestProductionResultReceiptPrivateKeyIsOwnerOnly(t *testing.T) {
	fixture := newProductionTestFixture(t)
	signer, err := newProductionResultReceiptSigner(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	request := testHookSubmitRequest(t)
	receiptFixture := newHookReceiptFixture(t, request, "production-key-job")
	signed, err := signer.SignResultReceipt(receiptFixture.receipt)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := newProductionResultReceiptVerifier(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyResultReceipt(signed); err != nil {
		t.Fatalf("VerifyResultReceipt() error = %v", err)
	}

	privatePath := fixture.config.ResultReceiptAuthority.PrivateKeyFile
	if err := os.Remove(privatePath); err != nil {
		t.Fatal(err)
	}
	publicOnly, err := newProductionResultReceiptVerifier(fixture.config)
	if err != nil {
		t.Fatalf("public verifier tried to read private key: %v", err)
	}
	if err := publicOnly.VerifyResultReceipt(signed); err != nil {
		t.Fatalf("public verifier failed after private key removal: %v", err)
	}
	if _, err := newProductionResultReceiptSigner(fixture.config); err == nil {
		t.Fatal("production signer loaded without the owner private key file")
	}
}

func TestProductionResultReceiptPrivateKeyRequiresExactModeAndMatchingPublicKey(t *testing.T) {
	fixture := newProductionTestFixture(t)
	privatePath := fixture.config.ResultReceiptAuthority.PrivateKeyFile
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionCoordinatorConfigFile(fixture.configPath); err == nil {
		t.Fatal("production config accepted a non-0600 receipt private key")
	}
	if err := os.Chmod(privatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	mismatched := fixture.config
	mismatched.ResultReceiptAuthority.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
	if _, err := newProductionResultReceiptSigner(mismatched); err == nil {
		t.Fatal("production signer accepted mismatched Ed25519 key material")
	}
}

func TestProductionGitAuthorityRequiresExactTrustedRefTip(t *testing.T) {
	fixture := newProductionTestFixture(t)
	authority, err := newProductionGitAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatalf("newProductionGitAuthority() error = %v", err)
	}
	record := productionAcceptedRecord(t, fixture)
	if err := authority.verifyRecord(context.Background(), record); err != nil {
		t.Fatalf("verifyRecord() error = %v", err)
	}
	record.TrustedCommit = strings.Repeat("f", len(record.TrustedCommit))
	if err := authority.verifyRecord(context.Background(), record); err == nil {
		t.Fatal("verifyRecord() accepted a non-tip trusted commit")
	}
}

func TestProductionBareTrustedRefPromotesBuiltCandidateWithRealEd25519(t *testing.T) {
	fixture := newProductionTestFixture(t)
	candidateCommit, tree := commitProductionCandidate(t, fixture)
	promotion := prepareProductionCandidatePromotion(t, &fixture, candidateCommit, tree)
	advanceAndPromoteProductionCandidate(t, promotion)
	assertProductionCandidatePromoted(t, promotion)
}

func TestProductionStagedTreePromotionAdvancesGenerationAndRemainsExecutable(t *testing.T) {
	fixture := newProductionTestFixture(t)
	staged := planProductionStagedTreePromotion(t, fixture)
	assertProductionStagedTreeCandidate(t, fixture, &staged)
	assertProductionStagedTreeRecovery(t, fixture, staged)
	promotion, accepted := promoteProductionStagedTreeCandidate(t, fixture, staged.queued)
	assertProductionStagedTreePromoted(t, accepted, staged)
	assertProductionStagedTreeExecutable(t, fixture, promotion, accepted, staged)
}

func commitProductionCandidate(t *testing.T, fixture productionTestFixture) (string, localci.ReadOnlyGitTree) {
	t.Helper()
	changeProductionBuildInput(t, fixture.sourceRepo, "go.mod", "module example.invalid/promoted\n")
	runProductionGit(t, fixture.sourceRepo, "add", "--", "go.mod", "build/gate/runtime-deps.lock")
	runProductionGit(t, fixture.sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
	candidateCommit := productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{commit}")
	candidateTree := productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{tree}")
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: candidateCommit}, SourceTreeSHA: candidateTree,
	}
	tree, err := localci.LoadReadOnlyGitTree(context.Background(), fixture.sourceRepo, source)
	if err != nil {
		t.Fatal(err)
	}
	return candidateCommit, tree
}

func prepareProductionCandidatePromotion(
	t *testing.T,
	fixture *productionTestFixture,
	candidateCommit string,
	tree localci.ReadOnlyGitTree,
) productionCandidatePromotionFixture {
	t.Helper()
	promotion, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := promotion.state.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	plan, err := promotion.candidates.Plan(context.Background(), accepted, localci.PromotionCandidatePlanRequest{
		Tree: tree, PolicyDigest: accepted.PolicyDigest, Platform: fixture.config.Platform,
		RepoID: fixture.config.RepoID, TrustedRef: fixture.config.TrustedRef, TrustedCommit: candidateCommit,
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Hour),
	})
	if err != nil || !plan.BuildRequired {
		t.Fatalf("Plan() = %+v, err=%v", plan, err)
	}
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	if err := promotion.candidates.ExecuteBuild(
		context.Background(), plan.WorkloadID, builder, productionCandidateIdentityResolverStub{},
	); err != nil {
		t.Fatal(err)
	}
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, 20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	return productionCandidatePromotionFixture{
		production: fixture, authority: promotion, controller: controller,
		tree: tree, accepted: accepted, candidateCommit: candidateCommit,
	}
}

func advanceAndPromoteProductionCandidate(t *testing.T, fixture productionCandidatePromotionFixture) {
	t.Helper()
	controller := fixture.controller
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(old trusted tip) error = %v", err)
	}
	config := fixture.production.config
	runProductionGit(t, "", "--git-dir="+config.TrustedRepository, "fetch", "-q", "--no-tags", "--", fixture.production.sourceRepo, fixture.candidateCommit)
	runProductionGit(t, "", "--git-dir="+config.TrustedRepository, "update-ref", config.TrustedRef, fixture.candidateCommit, fixture.production.commit)
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(candidate trusted tip) error = %v", err)
	}
}

func assertProductionCandidatePromoted(t *testing.T, fixture productionCandidatePromotionFixture) {
	t.Helper()
	promotion := fixture.authority
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Generation != 2 || promoted.TrustedCommit != fixture.candidateCommit ||
		promoted.Image.PlatformManifestDigest != productionDigest("8") {
		t.Fatalf("promoted accepted record = %+v", promoted)
	}
	truth, err := localci.NewTruthImageEnsurer(promotion.accepted, promotion.candidates)
	if err != nil {
		t.Fatal(err)
	}
	result, err := truth.EnsureImage(context.Background(), localci.TruthImageEnsureRequest{
		Tree: fixture.tree, PolicyDigest: fixture.accepted.PolicyDigest, Platform: fixture.production.config.Platform,
	})
	if err != nil || result.Status != localci.TruthImageEnsureAccepted ||
		result.Image.PlatformManifestDigest != productionDigest("8") {
		t.Fatalf("EnsureImage(after promotion) = %+v, err=%v", result, err)
	}
}

func TestProductionImageEnsurerKeepsJobAndAcceptedBuildTreesSeparate(t *testing.T) {
	fixture := newProductionTestFixture(t)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, productionSourceSpec(fixture))
	if err != nil {
		t.Fatal(err)
	}
	acceptedTree := strings.Repeat("f", len(fixture.tree))
	accepted := productionAcceptedRecord(t, fixture)
	accepted.SourceTree = acceptedTree
	accepted.PolicyDigest = plan.PolicyDigest
	accepted.Runner.PolicyDigest = plan.PolicyDigest
	truth := &capturingTruthImageEnsurer{result: localci.TruthImageEnsureResult{
		Status: localci.TruthImageEnsureAccepted, SubmittedJobSourceTree: fixture.tree,
		AcceptedImageBuildSourceTree: acceptedTree, PolicyDigest: plan.PolicyDigest, ImageSchemaVersion: "1",
		ImageInputDigest: accepted.ImageInputDigest, ToolchainDigest: productionDigest("9"),
		AcceptedRecord: accepted, Image: accepted.Image,
	}}
	ensurer := &productionImageEnsurer{truth: truth, platform: "linux/arm64"}
	result, err := ensurer.EnsureImage(context.Background(), imageEnsureRequest{
		RepositoryRoot: fixture.sourceRepo, Plan: plan, JobSourceTreeSHA: fixture.tree,
	})
	if err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if truth.request.Tree.Source.SourceTreeSHA != fixture.tree {
		t.Fatalf("truth input tree = %s", truth.request.Tree.Source.SourceTreeSHA)
	}
	if result.ImageProvenanceSourceTreeSHA != acceptedTree || result.Truth.BuildSourceTreeSHA != acceptedTree {
		t.Fatalf("ensured image provenance = %#v", result)
	}
	if result.ImageProvenanceSourceTreeSHA == fixture.tree {
		t.Fatal("production image adapter collapsed accepted build provenance into submitted job tree")
	}
}

func TestProductionFreshRunnerForwardsOnlyMatchingAcceptedProvenance(t *testing.T) {
	capture := &capturingFreshContainerRunner{}
	runner := &productionFreshContainerRunner{runner: capture}
	acceptedTree := strings.Repeat("a", 40)
	jobTree := strings.Repeat("b", 40)
	request := freshContainerRequest{
		ImageProvenanceSourceTreeSHA: acceptedTree, JobSourceTreeSHA: jobTree,
		ImageTruth:        localci.FreshContainerImageTruth{BuildSourceTreeSHA: acceptedTree},
		SourceSnapshotDir: "/private/source", GateID: gatecontract.GateIDWhitespaceCheck,
	}
	if _, err := runner.RunFreshContainer(context.Background(), request); err != nil {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	if capture.request.SourceTreeSHA != jobTree || capture.request.ImageTruth.BuildSourceTreeSHA != acceptedTree {
		t.Fatalf("forwarded request = %#v", capture.request)
	}
	request.ImageProvenanceSourceTreeSHA = jobTree
	if _, err := runner.RunFreshContainer(context.Background(), request); err == nil {
		t.Fatal("RunFreshContainer() accepted mismatched image provenance")
	}
}

func TestProductionCoordinatorConfigFieldRegistryIsComplete(t *testing.T) {
	assertProductionCoordinatorConfigFields(t)
	assertProductionBootstrapFields(t)
}

func assertProductionCoordinatorConfigFields(t *testing.T) {
	t.Helper()
	assertProductionFields(t, reflect.TypeFor[productionCoordinatorConfig](), map[string]string{
		"AcceptedImageRoot": "signed state", "BootstrapRootFile": "signed generation-one authority",
		"BootstrapControllerFile":    "signed external execution closure",
		"BootstrapControllerKeyFile": "owner-only generation-one signing material",
		"CandidateStateRoot":         "durable candidate authority",
		"CandidateBuildRoot":         "candidate isolation",
		"TrustedSourceRoot":          "snapshot mount boundary", "SeccompProfile": "container policy",
		"Platform": "image platform", "RepoID": "repository identity", "TrustedRef": "admission ref",
		"TrustedRepository": "external bare mirror", "AcceptedImageSigners": "signature trust root",
		"ResultReceiptAuthority": "result receipt authority",
		"ActionGrantAuthority":   "single-use action authority",
		"PromotionSigner":        "host signing authority", "CandidateTTLSeconds": "candidate expiry",
		"PromotionPollMillis": "watcher cadence",
	})
	assertProductionFields(t, reflect.TypeFor[productionTrustedKey](), map[string]string{
		"Signer": "key identity", "PublicKey": "Ed25519 verification material",
	})
	assertProductionFields(t, reflect.TypeFor[productionResultReceiptAuthorityConfig](), map[string]string{
		"Signer": "receipt signer identity", "PublicKey": "receipt verification material",
		"PrivateKeyFile": "owner-only receipt signing material",
	})
	assertProductionFields(t, reflect.TypeFor[productionResultReceiptPrivateKey](), map[string]string{
		"PrivateKey": "owner-only Ed25519 private key",
	})
	assertProductionFields(t, reflect.TypeFor[productionPromotionKey](), map[string]string{
		"Signer": "host key identity", "PrivateKeyFile": "repository-external signing key",
	})
}

func assertProductionBootstrapFields(t *testing.T) {
	t.Helper()
	assertProductionFields(t, reflect.TypeFor[productionBootstrapRoot](), map[string]string{
		"SchemaVersion": "strict root schema", "RepoID": "repository identity", "RemoteURL": "canonical remote", "TrustedRef": "admission ref",
		"ObjectFormat":   "Git object identity format",
		"BaselineCommit": "fixed bootstrap commit", "BaselineTree": "fixed bootstrap tree",
		"PolicyDigest": "fixed baseline policy", "ImageInputDigest": "fixed baseline input closure",
		"ToolchainDigest": "fixed toolchain closure", "ImageSchemaVersion": "gate image label schema",
		"CandidateRegistry": "signed immutable candidate publication target",
		"Runner":            "immutable OCI runner", "Controller": "external execution authority",
		"Signer": "installation signer", "Ed25519PublicKey": "verification material",
		"BootstrapSigner": "generation-one signer", "BootstrapPublicKey": "generation-one verification material",
		"VerifierVersion": "host verifier contract", "Signature": "root authenticity",
	})
	assertProductionFields(t, reflect.TypeFor[productionBootstrapControllerIdentity](), map[string]string{
		"BinaryDigest": "controller executable identity", "DesignatedRequirement": "macOS code-signing identity",
		"Signer": "controller attestation identity",
	})
}

func TestProductionCoordinatorConfigRejectsUnknownFieldsAndWorktreeRoots(t *testing.T) {
	fixture := newProductionTestFixture(t)
	data, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["fallback_to_host"] = true
	unknownPath := filepath.Join(filepath.Dir(fixture.configPath), "unknown.json")
	writePrivateJSON(t, unknownPath, object)
	if _, err := loadProductionCoordinatorConfigFile(unknownPath); err == nil {
		t.Fatal("production config accepted an unknown fallback field")
	}

	worktree := filepath.Join(t.TempDir(), "candidate")
	runProductionGit(t, "", "init", "-q", "--", worktree)
	worktreeConfig := filepath.Join(worktree, "production.json")
	writePrivateJSON(t, worktreeConfig, fixture.config)
	if _, err := loadProductionCoordinatorConfigFile(worktreeConfig); err == nil {
		t.Fatal("production config accepted a candidate worktree trust path")
	}
}

func TestLoadProductionCoordinatorConfigFileValidatesDecodedConfig(t *testing.T) {
	fixture := newProductionTestFixture(t)
	tests := []struct {
		name   string
		want   string
		mutate func(*productionCoordinatorConfig)
	}{
		{name: "repo_id", want: "repo_id is required", mutate: func(config *productionCoordinatorConfig) {
			config.RepoID = ""
		}},
		{name: "trusted_ref", want: "trusted_ref must be a canonical full ref", mutate: func(config *productionCoordinatorConfig) {
			config.TrustedRef = "main"
		}},
		{name: "platform", want: "platform must be explicit", mutate: func(config *productionCoordinatorConfig) {
			config.Platform = "linux"
		}},
		{name: "bootstrap_root", want: "bootstrap trust root", mutate: func(config *productionCoordinatorConfig) {
			config.BootstrapRootFile = ""
		}},
		{name: "bootstrap_controller", want: "bootstrap controller", mutate: func(config *productionCoordinatorConfig) {
			config.BootstrapControllerFile = ""
		}},
		{name: "bootstrap_controller_key", want: "bootstrap controller private key", mutate: func(config *productionCoordinatorConfig) {
			config.BootstrapControllerKeyFile = ""
		}},
		{name: "promotion_poll", want: "promotion_poll_millis must be within 5000..60000", mutate: func(config *productionCoordinatorConfig) {
			config.PromotionPollMillis = 4_999
		}},
		{name: "root_overlap", want: "roots must not overlap", mutate: func(config *productionCoordinatorConfig) {
			config.CandidateBuildRoot = config.AcceptedImageRoot
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.config
			test.mutate(&config)
			path := filepath.Join(filepath.Dir(fixture.configPath), test.name+".json")
			writePrivateJSON(t, path, config)
			if _, err := loadProductionCoordinatorConfigFile(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadProductionCoordinatorConfigFile() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestProductionCoordinatorRejectsGitObjectEnvironmentOverrides(t *testing.T) {
	t.Setenv("GIT_OBJECT_DIRECTORY", "/candidate/objects")
	if _, err := productionCoordinatorDependencies(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "GIT_OBJECT_DIRECTORY") {
		t.Fatalf("productionCoordinatorDependencies() error = %v", err)
	}
}

// prepareProductionRepository 创建 trusted source 与对应 bare authority repository。
func prepareProductionRepository(t *testing.T, root string) productionRepositoryFixture {
	t.Helper()
	sourceRepo := filepath.Join(root, "source")
	runProductionGit(t, "", "init", "-q", "-b", "main", "--", sourceRepo)
	if err := os.WriteFile(filepath.Join(sourceRepo, "trusted.txt"), []byte("trusted object content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeProductionBuildInputs(t, sourceRepo)
	runProductionGit(t, sourceRepo, "add", "--", ".")
	runProductionGit(t, sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "trusted")
	trustedRepository := filepath.Join(root, "trusted.git")
	runProductionGit(t, "", "clone", "-q", "--bare", "--", sourceRepo, trustedRepository)
	chmodPrivate(t, trustedRepository)
	return productionRepositoryFixture{
		sourceRepo: sourceRepo, trustedRepository: trustedRepository,
		commit: productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{commit}"),
		tree:   productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{tree}"),
	}
}

// prepareProductionAuthority 创建 promotion 与 receipt 各自独立的测试 Ed25519 密钥。
func prepareProductionAuthority(t *testing.T, root string) productionAuthorityFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receiptPublicKey, receiptPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	grantPublicKey, grantPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := productionAuthorityFixture{
		signer: gatecontract.SignerIdentity{
			KeyID: "production-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
		},
		publicKey: publicKey, privateKey: privateKey,
		receiptSigner: gatecontract.SignerIdentity{
			KeyID: "receipt-production-test", KeyEpoch: 1,
			Algorithm: gatecontract.SignatureAlgorithmEd25519,
		},
		receiptPublicKey: receiptPublicKey, receiptPrivateKey: receiptPrivateKey,
		receiptKeyPath: filepath.Join(root, "receipt-key.json"),
		grantSigner: gatecontract.SignerIdentity{
			KeyID: "grant-production-test", KeyEpoch: 1,
			Algorithm: gatecontract.SignatureAlgorithmEd25519,
		},
		grantPublicKey: grantPublicKey, grantPrivateKey: grantPrivateKey,
		grantKeyPath:     filepath.Join(root, "grant-key.json"),
		promotionKeyPath: filepath.Join(root, "promotion-private.key"),
	}
	writePrivateJSON(t, fixture.receiptKeyPath, productionResultReceiptPrivateKey{
		PrivateKey: base64.StdEncoding.EncodeToString(receiptPrivateKey),
	})
	writePrivateJSON(t, fixture.grantKeyPath, productionActionGrantPrivateKey{
		PrivateKey: base64.StdEncoding.EncodeToString(grantPrivateKey),
	})
	privateKeyData := base64.StdEncoding.EncodeToString(privateKey) + "\n"
	if err := os.WriteFile(fixture.promotionKeyPath, []byte(privateKeyData), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// prepareProductionTrustedSourceRoot 准备生产适配器允许的宿主缓存根。
func prepareProductionTrustedSourceRoot(t *testing.T) string {
	t.Helper()
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Clean(cacheRoot), "super-dolphin", "localci")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, root)
	return root
}

// writeProductionSeccompProfile 写入 production fixture 的最小 seccomp 配置。
func writeProductionSeccompProfile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "seccomp.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bootstrapAcceptedState(t *testing.T, fixture productionTestFixture) {
	t.Helper()
	verifier, err := newProductionSignatureVerifier(fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newProductionGitAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	state, err := localci.NewAcceptedImageState(fixture.config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		t.Fatal(err)
	}
	record := productionAcceptedRecord(t, fixture)
	if err := state.Bootstrap(context.Background(), record); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
}

func productionAcceptedRecord(t *testing.T, fixture productionTestFixture) gatecontract.AcceptedImageRecord {
	t.Helper()
	policyDigest := productionDigest("1")
	record := gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        fixture.config.RepoID, TrustedRef: fixture.config.TrustedRef,
		TrustedCommit: fixture.commit, SourceTree: fixture.tree,
		PolicyDigest: policyDigest, ImageInputDigest: fixture.acceptedInputDigest,
		Image: gatecontract.ImageIdentity{
			Registry:       "registry.example.invalid/super-dolphin/gate",
			OCIIndexDigest: productionDigest("3"), PlatformManifestDigest: productionDigest("4"),
			ConfigDigest: productionDigest("5"), RootFSDiffIDs: []string{productionDigest("6")},
			OS: "linux", Architecture: "arm64",
		},
		Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: productionDigest("7"), Signer: fixture.signer, PolicyDigest: policyDigest,
		},
		Generation: 1, AcceptedAt: time.Now().UTC(), Signer: fixture.signer,
	}
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, payload))
	return record
}

func productionSourceSpec(fixture productionTestFixture) gatecontract.SourceSpec {
	return gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: fixture.commit}, SourceTreeSHA: fixture.tree,
	}
}

func assertProductionFields(t *testing.T, producer reflect.Type, registry map[string]string) {
	t.Helper()
	for index := range producer.NumField() {
		field := producer.Field(index).Name
		if registry[field] == "" {
			t.Fatalf("%s field %q is not registered", producer.Name(), field)
		}
		delete(registry, field)
	}
	if len(registry) != 0 {
		t.Fatalf("%s field registry has stale entries: %v", producer.Name(), registry)
	}
}

func makePrivateDirectory(t *testing.T, root string, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func chmodPrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writePrivateJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func productionGitLine(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runProductionGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string(nil), args...)
	if directory != "" {
		commandArgs = append([]string{"-C", directory}, commandArgs...)
	}
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func productionDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
