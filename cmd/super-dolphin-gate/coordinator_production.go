package main

import (
	"bytes"
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
	if err := recoverInterruptedProductionCandidate(
		provisionCtx, promotion, time.Duration(config.PromotionPollMillis)*time.Millisecond,
	); err != nil {
		return nil, nil, nil, fmt.Errorf("recover interrupted production candidate: %w", err)
	}
	record, err := loadOrBootstrapProductionAcceptedImage(
		provisionCtx, config, promotion, productionBootstrapHostRuntime{},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load production accepted image within provisioning timeout: %w", err)
	}
	if err := promotion.authority.verifyRecord(provisionCtx, record); err != nil {
		return nil, nil, nil, err
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
		store: promotion.candidates, accepted: promotion.accepted, authority: promotion.authority,
		builder: builder, resolver: localci.NewDockerCandidateIdentityResolver(),
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

// recoverInterruptedProductionCandidate 仅恢复能精确证明继承 signed accepted record 的候选。
func recoverInterruptedProductionCandidate(
	ctx context.Context,
	promotion *productionPromotionAuthority,
	poll time.Duration,
) error {
	accepted, candidate, recoverable, err := promotion.interruptedCandidate(ctx)
	if err != nil || !recoverable {
		return err
	}
	switch candidate.Status {
	case localci.PromotionCandidateAwaiting:
		return promotion.promoteRecoveredCandidate(ctx, poll, candidate)
	case localci.PromotionCandidateQueued, localci.PromotionCandidateBuilding, localci.PromotionCandidateFailed:
		return promotion.authority.restoreAcceptedTrustedRef(
			ctx, accepted.TrustedCommit, candidate.TrustedCommit, candidate.SourceTree,
		)
	default:
		return errors.New("interrupted candidate has an unrecoverable promotion status")
	}
}

// interruptedCandidate 读取 trusted tip 漂移并只返回与当前 accepted record 精确衔接的候选。
func (promotion *productionPromotionAuthority) interruptedCandidate(
	ctx context.Context,
) (gatecontract.AcceptedImageRecord, localci.PromotionCandidate, bool, error) {
	if err := promotion.validateRecovery(); err != nil {
		return gatecontract.AcceptedImageRecord{}, localci.PromotionCandidate{}, false, err
	}
	accepted, present, err := promotion.acceptedForRecovery(ctx)
	if err != nil || !present {
		return gatecontract.AcceptedImageRecord{}, localci.PromotionCandidate{}, false, err
	}
	candidate, found, err := promotion.candidateForInterruptedTip(ctx, accepted)
	if err != nil || !found {
		return gatecontract.AcceptedImageRecord{}, localci.PromotionCandidate{}, false, err
	}
	if err := validateInterruptedCandidate(accepted, candidate); err != nil {
		return gatecontract.AcceptedImageRecord{}, localci.PromotionCandidate{}, false, err
	}
	return accepted, candidate, true, nil
}

// validateRecovery 确保恢复路径具备完成严格校验和原子状态变更所需的全部 authority。
func (promotion *productionPromotionAuthority) validateRecovery() error {
	if promotion == nil || promotion.state == nil || promotion.authority == nil || promotion.candidates == nil || promotion.signer == nil {
		return errors.New("production promotion recovery is not configured")
	}
	return nil
}

func (promotion *productionPromotionAuthority) acceptedForRecovery(
	ctx context.Context,
) (gatecontract.AcceptedImageRecord, bool, error) {
	accepted, err := promotion.state.Load(ctx)
	if errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		return gatecontract.AcceptedImageRecord{}, false, nil
	}
	if err != nil {
		return gatecontract.AcceptedImageRecord{}, false, err
	}
	return accepted, true, nil
}

func (promotion *productionPromotionAuthority) candidateForInterruptedTip(
	ctx context.Context,
	accepted gatecontract.AcceptedImageRecord,
) (localci.PromotionCandidate, bool, error) {
	tip, err := promotion.authority.trustedTip(ctx)
	if err != nil || tip == accepted.TrustedCommit {
		return localci.PromotionCandidate{}, false, err
	}
	candidate, err := promotion.candidates.CandidateForTrustedCommit(ctx, accepted.RepoID, accepted.TrustedRef, tip)
	if errors.Is(err, localci.ErrPromotionCandidateNotFound) {
		return localci.PromotionCandidate{}, false, nil
	}
	if err != nil {
		return localci.PromotionCandidate{}, false, err
	}
	return candidate, true, nil
}

func validateInterruptedCandidate(accepted gatecontract.AcceptedImageRecord, candidate localci.PromotionCandidate) error {
	expectedDigest, err := gatecontract.AcceptedImageRecordDigest(accepted)
	if err != nil {
		return fmt.Errorf("digest interrupted accepted image: %w", err)
	}
	if candidate.PreviousTrustedCommit != accepted.TrustedCommit ||
		candidate.ExpectedAcceptedRecordDigest != expectedDigest ||
		candidate.ExpectedAcceptedGeneration != accepted.Generation {
		return errors.New("interrupted candidate does not exactly extend the signed accepted image")
	}
	return nil
}

func (promotion *productionPromotionAuthority) promoteRecoveredCandidate(
	ctx context.Context,
	poll time.Duration,
	candidate localci.PromotionCandidate,
) error {
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, poll,
	)
	if err != nil {
		return err
	}
	return controller.PromoteCandidate(ctx, candidate)
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

const stagedTreePromotionMessage = "super-dolphin-gate staged tree promotion\n"

var stagedTreePromotionEnvironment = []string{
	"GIT_AUTHOR_NAME=Super Dolphin Gate Authority",
	"GIT_AUTHOR_EMAIL=gate-authority@super-dolphin.invalid",
	"GIT_AUTHOR_DATE=946684800 +0000",
	"GIT_COMMITTER_NAME=Super Dolphin Gate Authority",
	"GIT_COMMITTER_EMAIL=gate-authority@super-dolphin.invalid",
	"GIT_COMMITTER_DATE=946684800 +0000",
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

// ensureTreeBackedCommit 将精确 staged tree 内化到隔离 bare authority，并创建以 acceptedCommit 为父的确定性提交。
func (authority *productionGitAuthority) ensureTreeBackedCommit(
	ctx context.Context,
	repository string,
	tree string,
	acceptedCommit string,
) (string, error) {
	if authority == nil || tree == "" || acceptedCommit == "" {
		return "", errors.New("staged-tree promotion authority input is incomplete")
	}
	if err := authority.internalizeTree(ctx, repository, tree); err != nil {
		return "", err
	}
	storedTree, err := authority.line(ctx, "rev-parse", "--verify", "--end-of-options", tree+"^{tree}")
	if err != nil {
		return "", err
	}
	if storedTree != tree {
		return "", errors.New("trusted authority tree identity drifted during staged-tree import")
	}
	commit, err := authority.lineWithInput(ctx, []byte(stagedTreePromotionMessage), stagedTreePromotionEnvironment,
		"commit-tree", tree, "-p", acceptedCommit)
	if err != nil {
		return "", err
	}
	if err := authority.verifyTreeBackedCommit(ctx, commit, tree, acceptedCommit); err != nil {
		return "", err
	}
	return commit, nil
}

// internalizeTree 仅导入精确 tree 可达对象，不创建任何用户可见 ref。
func (authority *productionGitAuthority) internalizeTree(ctx context.Context, repository string, tree string) error {
	pack := authority.sourceCommand(ctx, repository, "pack-objects", "--stdout", "--revs")
	pack.Stdin = strings.NewReader(tree + "\n")
	index := authority.command(ctx, "index-pack", "--stdin", "--fix-thin")
	pipe, err := index.StdinPipe()
	if err != nil {
		return fmt.Errorf("open trusted Git object import pipe: %w", err)
	}
	var packStderr, indexStderr bytes.Buffer
	pack.Stdout, pack.Stderr, index.Stderr = pipe, &packStderr, &indexStderr
	if err := index.Start(); err != nil {
		return fmt.Errorf("start trusted Git object import: %w", err)
	}
	if err := pack.Start(); err != nil {
		_ = pipe.Close()
		_ = index.Wait()
		return fmt.Errorf("start staged-tree object export: %w", err)
	}
	packErr := pack.Wait()
	closeErr := pipe.Close()
	indexErr := index.Wait()
	if packErr != nil || closeErr != nil || indexErr != nil {
		return fmt.Errorf("internalize staged tree: export=%v import=%v close=%v export_stderr=%s import_stderr=%s",
			packErr, indexErr, closeErr, strings.TrimSpace(packStderr.String()), strings.TrimSpace(indexStderr.String()))
	}
	return nil
}

// advanceTrustedRef 以 accepted tip 为精确 CAS 推进唯一受管 ref，并支持幂等恢复。
func (authority *productionGitAuthority) advanceTrustedRef(
	ctx context.Context,
	acceptedCommit string,
	candidateCommit string,
	tree string,
) error {
	if err := authority.verifyTreeBackedCommit(ctx, candidateCommit, tree, acceptedCommit); err != nil {
		return err
	}
	tip, err := authority.trustedTip(ctx)
	if err != nil {
		return err
	}
	switch tip {
	case candidateCommit:
		return nil
	case acceptedCommit:
		if err := authority.run(ctx, "update-ref", authority.trustedRef, candidateCommit, acceptedCommit); err != nil {
			return err
		}
	default:
		return errors.New("trusted ref changed before staged-tree promotion CAS")
	}
	if tip, err := authority.trustedTip(ctx); err != nil {
		return err
	} else if tip != candidateCommit {
		return errors.New("trusted ref did not advance to staged-tree promotion commit")
	}
	return nil
}

// restoreAcceptedTrustedRef 仅以精确 CAS 回滚已持久化但未完成构建的 staged candidate。
func (authority *productionGitAuthority) restoreAcceptedTrustedRef(
	ctx context.Context,
	acceptedCommit string,
	candidateCommit string,
	tree string,
) error {
	if err := authority.verifyTreeBackedCommit(ctx, candidateCommit, tree, acceptedCommit); err != nil {
		return err
	}
	tip, err := authority.trustedTip(ctx)
	if err != nil {
		return err
	}
	switch tip {
	case acceptedCommit:
		return nil
	case candidateCommit:
		if err := authority.run(ctx, "update-ref", authority.trustedRef, acceptedCommit, candidateCommit); err != nil {
			return err
		}
	default:
		return errors.New("trusted ref changed before interrupted candidate rollback")
	}
	if tip, err := authority.trustedTip(ctx); err != nil {
		return err
	} else if tip != acceptedCommit {
		return errors.New("trusted ref did not roll back to accepted image commit")
	}
	return nil
}

// verifyTreeBackedCommit 验证候选提交的 tree 与唯一父提交均精确绑定。
func (authority *productionGitAuthority) verifyTreeBackedCommit(ctx context.Context, commit string, tree string, parent string) error {
	commitTree, err := authority.line(ctx, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return err
	}
	if commitTree != tree {
		return errors.New("staged-tree promotion commit tree does not match staged tree")
	}
	parents, err := authority.line(ctx, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return err
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != commit || fields[1] != parent {
		return errors.New("staged-tree promotion commit does not have the accepted commit as its sole parent")
	}
	return nil
}

func (authority *productionGitAuthority) line(ctx context.Context, args ...string) (string, error) {
	return authority.lineWithInput(ctx, nil, nil, args...)
}

func (authority *productionGitAuthority) lineWithInput(ctx context.Context, input []byte, environment []string, args ...string) (string, error) {
	command := authority.commandWithEnvironment(ctx, environment, args...)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("trusted Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line == "" || strings.ContainsAny(line, "\r\n\x00") || strings.TrimSpace(line) != line {
		return "", fmt.Errorf("trusted Git %s returned non-canonical output", args[0])
	}
	return line, nil
}

func (authority *productionGitAuthority) run(ctx context.Context, args ...string) error {
	output, err := authority.command(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("trusted Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	if len(output) != 0 {
		return fmt.Errorf("trusted Git %s returned unexpected output", args[0])
	}
	return nil
}

func (authority *productionGitAuthority) command(ctx context.Context, args ...string) *exec.Cmd {
	return authority.commandWithEnvironment(ctx, nil, args...)
}

func (authority *productionGitAuthority) commandWithEnvironment(ctx context.Context, environment []string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"--git-dir=" + authority.repository}, args...)
	command := exec.CommandContext(ctx, authority.gitPath, commandArgs...)
	command.Env = append([]string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C", "PATH=" + os.Getenv("PATH"),
	}, environment...)
	return command
}

func (authority *productionGitAuthority) sourceCommand(ctx context.Context, repository string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", repository}, args...)
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
