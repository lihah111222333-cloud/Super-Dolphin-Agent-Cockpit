package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// productionCoordinatorDependencies 装配签名 accepted image、Git object source 与一次性 Docker runner。
func productionCoordinatorDependencies(ctx context.Context) (coordinatorDependencies, error) {
	if ctx == nil {
		return coordinatorDependencies{}, fmt.Errorf("%w: production context is required", errCoordinatorDependency)
	}
	if err := validateProductionGitEnvironment(); err != nil {
		return coordinatorDependencies{}, err
	}
	config, err := loadProductionCoordinatorConfig()
	if err != nil {
		return coordinatorDependencies{}, err
	}
	if err := validateProductionRuntimeRoot(config.TrustedSourceRoot); err != nil {
		return coordinatorDependencies{}, err
	}
	imageEnsurer, candidateBuilder, watcher, err := newProductionImageServices(ctx, config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	sourceMaterializer, freshRunner, err := newProductionExecutionAdapters(config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	receiptSigner, err := newProductionResultReceiptSigner(config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	dependencies := coordinatorDependencies{
		ImageEnsurer: imageEnsurer, CandidateBuilder: candidateBuilder, PromotionWatcher: watcher,
		SourceMaterializer: sourceMaterializer, FreshRunner: freshRunner, RecoveryRunner: freshRunner,
		ReceiptSigner: receiptSigner,
	}
	return dependencies, dependencies.validate()
}

// newProductionImageServices 组装 accepted 读取、调度构建与宿主晋升控制器。
func newProductionImageServices(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionImageEnsurer, *productionCandidateBuildService, *localci.PromotionController, error) {
	promotion, err := newProductionPromotionAuthority(ctx, config)
	if err != nil {
		return nil, nil, nil, err
	}
	provisionCtx, cancel := localci.BoundedOperationContext(ctx, coordinatorProvisioningTimeout)
	defer cancel()
	record, err := loadOrBootstrapProductionAcceptedImage(
		provisionCtx, config, promotion, productionBootstrapHostRuntime{},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load production accepted image within provisioning timeout: %w", err)
	}
	if err := validateAcceptedPlatform(record.Image, config.Platform); err != nil {
		return nil, nil, nil, err
	}
	buildx, err := localci.NewDockerBuildxRunner(config.CandidateBuildRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	builder, err := localci.NewImageBuilder(buildx)
	if err != nil {
		return nil, nil, nil, err
	}
	truth, err := localci.NewTruthImageEnsurer(promotion.accepted, promotion.candidates)
	if err != nil {
		return nil, nil, nil, err
	}
	buildService := &productionCandidateBuildService{
		store: promotion.candidates, builder: builder, resolver: localci.NewDockerCandidateIdentityResolver(),
	}
	watcher, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer,
		time.Duration(config.PromotionPollMillis)*time.Millisecond,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return &productionImageEnsurer{truth: truth, platform: config.Platform}, buildService, watcher, nil
}

func newProductionAcceptedImageLoader(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionAcceptedImageLoader, gatecontract.AcceptedImageRecord, error) {
	verifier, err := newProductionSignatureVerifier(config.AcceptedImageSigners)
	if err != nil {
		return nil, gatecontract.AcceptedImageRecord{}, err
	}
	authority, err := newProductionGitAuthority(ctx, config)
	if err != nil {
		return nil, gatecontract.AcceptedImageRecord{}, err
	}
	state, err := localci.NewAcceptedImageState(config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, gatecontract.AcceptedImageRecord{}, fmt.Errorf("open accepted image state: %w", err)
	}
	accepted := &productionAcceptedImageLoader{state: state, authority: authority}
	record, err := accepted.Load(ctx)
	if err != nil {
		return nil, gatecontract.AcceptedImageRecord{}, fmt.Errorf("load production accepted image: %w", err)
	}
	return accepted, record, nil
}

// newProductionExecutionAdapters 组装 Git bundle 快照与 Docker 一次性容器边界。
func newProductionExecutionAdapters(
	config productionCoordinatorConfig,
) (*productionSourceMaterializer, *productionFreshContainerRunner, error) {
	fresh, err := localci.NewFreshContainerRunner(config.SeccompProfile, config.TrustedSourceRoot)
	if err != nil {
		return nil, nil, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve source materializer Git executable: %w", err)
	}
	return &productionSourceMaterializer{gitPath: gitPath}, &productionFreshContainerRunner{runner: fresh}, nil
}

func validateProductionGitEnvironment() error {
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT",
	} {
		if _, exists := os.LookupEnv(name); exists {
			return fmt.Errorf("production coordinator rejects inherited %s", name)
		}
	}
	return nil
}

func validateProductionRuntimeRoot(trustedSourceRoot string) error {
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		return err
	}
	if !productionPathContains(trustedSourceRoot, runtimeRoot) {
		return errors.New("trusted_source_root does not contain the coordinator runtime root")
	}
	return nil
}

func validateAcceptedPlatform(identity gatecontract.ImageIdentity, platform string) error {
	acceptedPlatform := identity.OS + "/" + identity.Architecture
	if identity.Variant != "" {
		acceptedPlatform += "/" + identity.Variant
	}
	if acceptedPlatform != strings.TrimSpace(platform) {
		return fmt.Errorf("accepted image platform %q does not match configured platform %q", acceptedPlatform, platform)
	}
	return nil
}

type productionSignerKey struct {
	keyID     string
	keyEpoch  uint64
	algorithm gatecontract.SignatureAlgorithm
}

type productionSignatureVerifier struct {
	keys map[productionSignerKey]ed25519.PublicKey
}

// newProductionSignatureVerifier 解码唯一 Ed25519 公钥集合且拒绝重复 signer epoch。
func newProductionSignatureVerifier(keys []productionTrustedKey) (*productionSignatureVerifier, error) {
	verifier := &productionSignatureVerifier{keys: make(map[productionSignerKey]ed25519.PublicKey, len(keys))}
	for _, configured := range keys {
		if err := configured.Signer.Validate(); err != nil {
			return nil, fmt.Errorf("validate accepted image signer: %w", err)
		}
		decoded, err := base64.StdEncoding.DecodeString(configured.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("accepted image signer public_key must be base64 Ed25519 public key")
		}
		key := productionSignerKey{
			keyID: configured.Signer.KeyID, keyEpoch: configured.Signer.KeyEpoch, algorithm: configured.Signer.Algorithm,
		}
		if _, exists := verifier.keys[key]; exists {
			return nil, fmt.Errorf("accepted image signer %q epoch %d is duplicated", key.keyID, key.keyEpoch)
		}
		verifier.keys[key] = ed25519.PublicKey(append([]byte(nil), decoded...))
	}
	return verifier, nil
}

// VerifyAcceptedImage 对规范 payload 执行真实 Ed25519 验签。
func (verifier *productionSignatureVerifier) VerifyAcceptedImage(
	ctx context.Context,
	signer gatecontract.SignerIdentity,
	payload []byte,
	signature string,
) error {
	if verifier == nil || ctx == nil || len(verifier.keys) == 0 {
		return errors.New("accepted image signature verifier is not configured")
	}
	if err := errors.Join(ctx.Err(), signer.Validate()); err != nil {
		return err
	}
	key := productionSignerKey{keyID: signer.KeyID, keyEpoch: signer.KeyEpoch, algorithm: signer.Algorithm}
	publicKey, ok := verifier.keys[key]
	if !ok {
		return fmt.Errorf("accepted image signer %q epoch %d is not trusted", signer.KeyID, signer.KeyEpoch)
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return errors.New("accepted image signature must be base64 Ed25519 signature")
	}
	if !ed25519.Verify(publicKey, payload, decoded) {
		return errors.New("accepted image signature verification failed")
	}
	return nil
}

type productionGitAuthority struct {
	gitPath    string
	repository string
	repoID     string
	trustedRef string
}

// newProductionGitAuthority 打开仓库外自包含 bare mirror 并确认 trusted ref 存在。
func newProductionGitAuthority(ctx context.Context, config productionCoordinatorConfig) (*productionGitAuthority, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("resolve production Git executable: %w", err)
	}
	authority := &productionGitAuthority{
		gitPath: gitPath, repository: config.TrustedRepository, repoID: config.RepoID, trustedRef: config.TrustedRef,
	}
	if err := rejectTrustedRepositoryIndirection(config.TrustedRepository); err != nil {
		return nil, err
	}
	bare, err := authority.line(ctx, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "true" {
		return nil, errors.Join(errors.New("trusted_repository must be an external bare Git repository"), err)
	}
	if _, err := authority.trustedTip(ctx); err != nil {
		return nil, err
	}
	return authority, nil
}

// rejectTrustedRepositoryIndirection 禁止 bare mirror 通过 alternates 等机制读取候选对象库。
func rejectTrustedRepositoryIndirection(repository string) error {
	for _, relative := range []string{"objects/info/alternates", "commondir", "gitdir"} {
		path := filepath.Join(repository, filepath.FromSlash(relative))
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("trusted_repository must not use %s indirection", relative)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect trusted_repository %s: %w", relative, err)
		}
	}
	return nil
}

// IsAncestor 只在配置的 repository/ref 上执行 Git ancestry 判定。
func (authority *productionGitAuthority) IsAncestor(
	ctx context.Context,
	repoID string,
	trustedRef string,
	previous string,
	next string,
) (bool, error) {
	if authority == nil || repoID != authority.repoID || trustedRef != authority.trustedRef {
		return false, errors.New("accepted image ancestry request does not match trusted repository identity")
	}
	command := authority.command(ctx, "merge-base", "--is-ancestor", previous, next)
	output, err := command.CombinedOutput()
	if err == nil {
		if len(output) != 0 {
			return false, errors.New("git merge-base returned unexpected output")
		}
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("verify accepted image ancestry: %w: %s", err, strings.TrimSpace(string(output)))
}

// verifyRecord 将已验签记录绑定到 configured trusted ref 的精确 tip 与 tree。
func (authority *productionGitAuthority) verifyRecord(ctx context.Context, record gatecontract.AcceptedImageRecord) error {
	if record.RepoID != authority.repoID || record.TrustedRef != authority.trustedRef {
		return errors.New("accepted image record does not match configured repo_id and trusted_ref")
	}
	tip, err := authority.trustedTip(ctx)
	if err != nil {
		return err
	}
	if tip != record.TrustedCommit {
		return errors.New("accepted image trusted_commit is not the exact configured trusted_ref tip")
	}
	tree, err := authority.line(ctx, "rev-parse", "--verify", "--end-of-options", record.TrustedCommit+"^{tree}")
	if err != nil {
		return err
	}
	if tree != record.SourceTree {
		return errors.New("accepted image source_tree does not match trusted commit")
	}
	return nil
}

func (authority *productionGitAuthority) trustedTip(ctx context.Context) (string, error) {
	return authority.line(ctx, "rev-parse", "--verify", "--end-of-options", authority.trustedRef+"^{commit}")
}

func (authority *productionGitAuthority) line(ctx context.Context, args ...string) (string, error) {
	output, err := authority.command(ctx, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("trusted Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line == "" || strings.ContainsAny(line, "\r\n\x00") || strings.TrimSpace(line) != line {
		return "", fmt.Errorf("trusted Git %s returned non-canonical output", args[0])
	}
	return line, nil
}

func (authority *productionGitAuthority) command(ctx context.Context, args ...string) *exec.Cmd {
	commandArgs := append([]string{"--git-dir=" + authority.repository}, args...)
	command := exec.CommandContext(ctx, authority.gitPath, commandArgs...)
	command.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C", "PATH=" + os.Getenv("PATH"),
	}
	return command
}

type productionAcceptedImageLoader struct {
	state     *localci.AcceptedImageState
	authority *productionGitAuthority
}

// Load 先读取并验签 accepted state，再复核外部 bare trusted-ref authority。
func (loader *productionAcceptedImageLoader) Load(ctx context.Context) (gatecontract.AcceptedImageRecord, error) {
	if loader == nil || loader.state == nil || loader.authority == nil {
		return gatecontract.AcceptedImageRecord{}, errors.New("production accepted image loader is not configured")
	}
	record, err := loader.state.Load(ctx)
	if err != nil {
		return gatecontract.AcceptedImageRecord{}, err
	}
	if err := loader.authority.verifyRecord(ctx, record); err != nil {
		return gatecontract.AcceptedImageRecord{}, err
	}
	return record, nil
}
