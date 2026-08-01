package main

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type productionPromotionAuthority struct {
	store      *coordinatorStore
	state      *localci.AcceptedImageState
	accepted   *productionAcceptedImageLoader
	authority  *productionGitAuthority
	candidates *localci.PromotionCandidateStore
	signer     *productionAcceptedImageSigner
}

type productionAcceptedImageSigner struct {
	identity gatecontract.SignerIdentity
	key      ed25519.PrivateKey
}

// openProductionPromotionAuthorityDB resolves the same daemon-identity keyed
// coordinator ledger used by the owner.  It deliberately does not create an
// authority database under either legacy JSON root.
func openProductionPromotionAuthorityStore(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*coordinatorStore, error) {
	checkpoint, err := localci.ProbeDockerSchedulerAuthorityWithCapacity(ctx, config.MaxActiveCIWorkloads)
	if err != nil {
		return nil, fmt.Errorf("probe coordinator authority for promotion state: %w", err)
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// Close releases the independently opened coordinator ledger.
func (authority *productionPromotionAuthority) Close() error {
	if authority == nil || authority.store == nil {
		return errors.New("production promotion authority store is not open")
	}
	err := authority.store.close()
	authority.store = nil
	return err
}

// SignerIdentity 返回宿主 authority 身份而不暴露私钥材料。
func (signer *productionAcceptedImageSigner) SignerIdentity() gatecontract.SignerIdentity {
	if signer == nil {
		return gatecontract.SignerIdentity{}
	}
	return signer.identity
}

// SignAcceptedImage 使用宿主专属 Ed25519 authority 签署规范 payload。
func (signer *productionAcceptedImageSigner) SignAcceptedImage(ctx context.Context, payload []byte) (string, error) {
	if signer == nil || ctx == nil || len(signer.key) != ed25519.PrivateKeySize || len(payload) == 0 {
		return "", errors.New("production accepted image signer is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(signer.key, payload)), nil
}

// newProductionPromotionAuthority 组装仓库外状态、bare ref 观察与宿主签名。
func newProductionPromotionAuthority(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionPromotionAuthority, error) {
	verifier, err := newProductionSignatureVerifier(config.AcceptedImageSigners)
	if err != nil {
		return nil, err
	}
	authority, err := newProductionGitAuthority(ctx, config)
	if err != nil {
		return nil, err
	}
	store, err := openProductionPromotionAuthorityStore(ctx, config)
	if err != nil {
		return nil, err
	}
	state, err := localci.NewAcceptedImageStateSQLite(store.db, config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open accepted image state: %w", err), store.close())
	}
	candidates, err := localci.NewPromotionCandidateStoreSQLite(store.db, config.CandidateStateRoot)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open promotion candidate state: %w", err), store.close())
	}
	signer, err := newProductionAcceptedImageSigner(config)
	if err != nil {
		return nil, errors.Join(err, store.close())
	}
	return &productionPromotionAuthority{
		store: store, state: state, accepted: &productionAcceptedImageLoader{state: state, authority: authority},
		authority: authority, candidates: candidates, signer: signer,
	}, nil
}

// newProductionAcceptedImageSigner 安全读取私钥并确认其公钥属于配置的信任根。
func newProductionAcceptedImageSigner(config productionCoordinatorConfig) (*productionAcceptedImageSigner, error) {
	path, err := canonicalProductionFile("promotion private key", config.PromotionSigner.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	encoded, err := readProductionCoordinatorConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read promotion private key: %w", err)
	}
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		return nil, errors.New("promotion private key file must contain one base64 line")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(encoded[:len(encoded)-1]))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("promotion private key must be a base64 Ed25519 private key")
	}
	trustedPublic, err := configuredPromotionPublicKey(config)
	if err != nil {
		return nil, err
	}
	derivedPublic := ed25519.PrivateKey(decoded).Public().(ed25519.PublicKey)
	if subtle.ConstantTimeCompare(derivedPublic, trustedPublic) != 1 {
		return nil, errors.New("promotion private key does not match the configured trusted public key")
	}
	return &productionAcceptedImageSigner{
		identity: config.PromotionSigner.Signer, key: ed25519.PrivateKey(append([]byte(nil), decoded...)),
	}, nil
}

func configuredPromotionPublicKey(config productionCoordinatorConfig) (ed25519.PublicKey, error) {
	for _, trusted := range config.AcceptedImageSigners {
		if trusted.Signer != config.PromotionSigner.Signer {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(trusted.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("promotion signer trusted public key is invalid")
		}
		return ed25519.PublicKey(decoded), nil
	}
	return nil, errors.New("promotion signer is not present in accepted_image_signers")
}

type productionCandidateSubmissionPlanner struct {
	store      *coordinatorStore
	accepted   *productionAcceptedImageLoader
	authority  *productionGitAuthority
	candidates *localci.PromotionCandidateStore
	config     productionCoordinatorConfig
	now        func() time.Time
}

// Close releases the coordinator SQLite handle opened for submit-time planning.
func (planner *productionCandidateSubmissionPlanner) Close() error {
	if planner == nil || planner.store == nil {
		return errors.New("production candidate planner store is not open")
	}
	err := planner.store.close()
	planner.store = nil
	return err
}

func newProductionCandidateSubmissionPlanner(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionCandidateSubmissionPlanner, error) {
	verifier, err := newProductionSignatureVerifier(config.AcceptedImageSigners)
	if err != nil {
		return nil, err
	}
	authority, err := newProductionGitAuthority(ctx, config)
	if err != nil {
		return nil, err
	}
	store, err := openProductionPromotionAuthorityStore(ctx, config)
	if err != nil {
		return nil, err
	}
	state, err := localci.NewAcceptedImageStateSQLite(store.db, config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, errors.Join(err, store.close())
	}
	candidates, err := localci.NewPromotionCandidateStoreSQLite(store.db, config.CandidateStateRoot)
	if err != nil {
		return nil, errors.Join(err, store.close())
	}
	return &productionCandidateSubmissionPlanner{
		store: store, accepted: &productionAcceptedImageLoader{state: state, authority: authority}, authority: authority,
		candidates: candidates, config: config, now: time.Now,
	}, nil
}

// PlanCandidate 在 scheduler 接收 workload 前持久化不可变 build intent。
func (planner *productionCandidateSubmissionPlanner) PlanCandidate(
	ctx context.Context,
	request imageEnsureRequest,
) (localci.PromotionCandidatePlan, error) {
	if err := planner.validateConfiguration(); err != nil {
		return localci.PromotionCandidatePlan{}, err
	}
	tree, accepted, err := planner.loadCandidateTreeAndAccepted(ctx, request)
	if err != nil {
		return localci.PromotionCandidatePlan{}, err
	}
	trustedCommit, managedTreeCommit, err := planner.candidateTrustedCommit(ctx, request.RepositoryRoot, request.Plan.Source, accepted)
	if err != nil {
		return localci.PromotionCandidatePlan{}, err
	}
	if err := planner.verifyCandidateTrustedRef(ctx, accepted, trustedCommit, managedTreeCommit); err != nil {
		return localci.PromotionCandidatePlan{}, err
	}
	plan, err := planner.persistCandidatePlan(ctx, tree, accepted, trustedCommit, request.Plan.PolicyDigest)
	return plan, err
}

// validateConfiguration 仅接受完整生产依赖，避免提升路径在缺失 authority 时继续执行。
func (planner *productionCandidateSubmissionPlanner) validateConfiguration() error {
	if planner == nil || planner.accepted == nil || planner.authority == nil || planner.candidates == nil || planner.now == nil {
		return errors.New("production candidate submission planner is not configured")
	}
	return nil
}

// loadCandidateTreeAndAccepted 加载并校验请求 tree，再读取当前 accepted state。
func (planner *productionCandidateSubmissionPlanner) loadCandidateTreeAndAccepted(
	ctx context.Context,
	request imageEnsureRequest,
) (localci.ReadOnlyGitTree, gatecontract.AcceptedImageRecord, error) {
	tree, err := localci.LoadReadOnlyGitTree(ctx, request.RepositoryRoot, request.Plan.Source)
	if err != nil {
		return localci.ReadOnlyGitTree{}, gatecontract.AcceptedImageRecord{}, err
	}
	if tree.Source.SourceTreeSHA != request.JobSourceTreeSHA {
		return localci.ReadOnlyGitTree{}, gatecontract.AcceptedImageRecord{}, errors.New("candidate planner tree does not match submitted job tree")
	}
	accepted, err := planner.accepted.state.Load(ctx)
	if err != nil {
		return localci.ReadOnlyGitTree{}, gatecontract.AcceptedImageRecord{}, err
	}
	return tree, accepted, nil
}

// verifyCandidateTrustedRef 保持 tree 提升恢复分支与既有 commit-backed 校验的 fail-closed 约束。
func (planner *productionCandidateSubmissionPlanner) verifyCandidateTrustedRef(
	ctx context.Context,
	accepted gatecontract.AcceptedImageRecord,
	trustedCommit string,
	managedTreeCommit bool,
) error {
	if managedTreeCommit {
		tip, err := planner.authority.trustedTip(ctx)
		if err != nil {
			return err
		}
		if tip != accepted.TrustedCommit && tip != trustedCommit {
			return errors.New("trusted ref changed outside staged-tree promotion recovery")
		}
		return nil
	}
	tip, err := planner.authority.trustedTip(ctx)
	if err != nil {
		return err
	}
	if tip == accepted.TrustedCommit {
		return planner.authority.verifyRecord(ctx, accepted)
	}
	if tip != trustedCommit {
		return errors.New("trusted ref changed outside external promotion authority")
	}
	return nil
}

// persistCandidatePlan 以当前 accepted state 与固定时间窗创建或复用不可变 build intent。
func (planner *productionCandidateSubmissionPlanner) persistCandidatePlan(
	ctx context.Context,
	tree localci.ReadOnlyGitTree,
	accepted gatecontract.AcceptedImageRecord,
	trustedCommit string,
	policyDigest string,
) (localci.PromotionCandidatePlan, error) {
	createdAt := planner.now().UTC()
	return planner.candidates.Plan(ctx, accepted, localci.PromotionCandidatePlanRequest{
		Tree: tree, PolicyDigest: policyDigest, Platform: planner.config.Platform,
		RepoID: planner.config.RepoID, TrustedRef: planner.config.TrustedRef, TrustedCommit: trustedCommit,
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Duration(planner.config.CandidateTTLSeconds) * time.Second),
	})
}

// candidateTrustedCommit 将 staged tree 内化为 authority 受管的确定性提交；其他 source 保持既有外部 ref 合同。
func (planner *productionCandidateSubmissionPlanner) candidateTrustedCommit(
	ctx context.Context,
	repositoryRoot string,
	source gatecontract.SourceSpec,
	accepted gatecontract.AcceptedImageRecord,
) (string, bool, error) {
	if source.Kind != gatecontract.SourceKindTree {
		commit, err := promotionCommitFromSource(source)
		return commit, false, err
	}
	if source.Tree == nil {
		return "", false, errors.New("staged-tree promotion source is missing tree authority")
	}
	commit, err := planner.authority.ensureTreeBackedCommit(ctx, repositoryRoot, source.Tree.SHA, accepted.TrustedCommit)
	if err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// promotionCommitFromSource 只接受可精确绑定 trusted ref tip 的 commit-backed source。
func promotionCommitFromSource(source gatecontract.SourceSpec) (string, error) {
	switch source.Kind {
	case gatecontract.SourceKindCommit:
		if source.Commit != nil {
			return source.Commit.SHA, nil
		}
	case gatecontract.SourceKindRange:
		if source.Range != nil {
			return source.Range.HeadSHA, nil
		}
	case gatecontract.SourceKindTree:
		if source.Tree != nil && source.Tree.ParentCommitSHA != "" {
			return source.Tree.ParentCommitSHA, nil
		}
	}
	return "", errors.New("candidate promotion requires a commit-backed source")
}

type productionCandidateBuildService struct {
	promotion     *productionPromotionAuthority
	store         *localci.PromotionCandidateStore
	accepted      *productionAcceptedImageLoader
	authority     *productionGitAuthority
	builder       localci.CandidateImageBuilder
	resolver      localci.CandidateImageIdentityResolver
	promotionPoll time.Duration
}

// Close releases the promotion authority shared by the long-lived build and watcher services.
func (service *productionCandidateBuildService) Close() error {
	if service == nil || service.promotion == nil {
		return errors.New("production candidate build service promotion authority is not open")
	}
	err := service.promotion.Close()
	service.promotion = nil
	return err
}

// ExecuteBuild 持久化候选镜像后才推进 canonical trusted ref。
func (service *productionCandidateBuildService) ExecuteBuild(ctx context.Context, workloadID string) error {
	if err := service.validateConfiguration(); err != nil {
		return err
	}
	candidate, err := service.awaitingCandidate(ctx, workloadID)
	if err != nil {
		return err
	}
	if err := service.advanceBuiltCandidate(ctx, candidate); err != nil {
		return err
	}
	return service.waitForAcceptedCandidate(ctx, candidate)
}

// validateConfiguration 拒绝缺失候选构建依赖的服务实例。
func (service *productionCandidateBuildService) validateConfiguration() error {
	if service == nil || service.store == nil || service.accepted == nil || service.authority == nil ||
		service.promotionPoll <= 0 {
		return errors.New("production candidate build service is not configured")
	}
	return nil
}

// awaitingCandidate 确保构建后持久化的候选正等待 promotion。
func (service *productionCandidateBuildService) awaitingCandidate(
	ctx context.Context,
	workloadID string,
) (localci.PromotionCandidate, error) {
	candidate, err := service.store.Candidate(ctx, workloadID)
	if err != nil {
		return localci.PromotionCandidate{}, err
	}
	if candidate.Status == localci.PromotionCandidateAwaiting {
		return candidate, nil
	}
	if err := service.store.ExecuteBuild(ctx, workloadID, service.builder, service.resolver); err != nil {
		return localci.PromotionCandidate{}, err
	}
	candidate, err = service.store.Candidate(ctx, workloadID)
	if err != nil {
		return localci.PromotionCandidate{}, err
	}
	if candidate.Status != localci.PromotionCandidateAwaiting {
		return localci.PromotionCandidate{}, errors.New("candidate build did not persist an awaiting promotion artifact")
	}
	return candidate, nil
}

// waitForAcceptedCandidate 保持 scheduler 构建依赖未完成，直到 watcher 已原子发布 accepted generation。
func (service *productionCandidateBuildService) waitForAcceptedCandidate(
	ctx context.Context,
	candidate localci.PromotionCandidate,
) error {
	poll := max(service.promotionPoll, 250*time.Millisecond)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		pending, err := service.acceptedCandidatePending(ctx, candidate)
		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// acceptedCandidatePending 区分可等待的 accepted 基线、完成 promotion 与不可恢复的不匹配。
func (service *productionCandidateBuildService) acceptedCandidatePending(
	ctx context.Context,
	candidate localci.PromotionCandidate,
) (bool, error) {
	accepted, err := service.accepted.Load(ctx)
	if err != nil {
		if errors.Is(err, errProductionAcceptedTrustedRefMismatch) {
			return true, nil
		}
		return false, fmt.Errorf("load accepted image while awaiting candidate promotion: %w", err)
	}
	return acceptedCandidatePromotionPending(candidate, accepted)
}

// acceptedCandidatePromotionPending 仅允许候选精确基线继续等待 watcher 发布。
func acceptedCandidatePromotionPending(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) (bool, error) {
	promotionErr := verifyAcceptedCandidate(candidate, accepted)
	if promotionErr == nil {
		return false, nil
	}
	waiting, err := acceptedIsCandidateBase(candidate, accepted)
	if err != nil {
		return false, err
	}
	if waiting {
		return true, nil
	}
	return false, promotionErr
}

// acceptedIsCandidateBase 验证 accepted record 是否仍是候选计划绑定的精确基线。
func acceptedIsCandidateBase(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) (bool, error) {
	digest, err := gatecontract.AcceptedImageRecordDigest(accepted)
	if err != nil {
		return false, fmt.Errorf("digest accepted candidate base: %w", err)
	}
	return accepted.RepoID == candidate.RepoID && accepted.TrustedRef == candidate.TrustedRef &&
		accepted.TrustedCommit == candidate.PreviousTrustedCommit &&
		accepted.Generation == candidate.ExpectedAcceptedGeneration &&
		digest == candidate.ExpectedAcceptedRecordDigest, nil
}

// verifyAcceptedCandidate 证明新 accepted record 与构建依赖的候选完全同源。
func verifyAcceptedCandidate(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) error {
	if !acceptedCandidateAuthorityMatches(candidate, accepted) ||
		!acceptedCandidateRecordMatches(candidate, accepted) ||
		!acceptedCandidateImageMatches(candidate, accepted) {
		return errors.New("promoted candidate does not exactly match the accepted image authority")
	}
	return nil
}

// acceptedCandidateAuthorityMatches 验证 promotion authority 的仓库、ref 与源码身份。
func acceptedCandidateAuthorityMatches(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) bool {
	return accepted.RepoID == candidate.RepoID && accepted.TrustedRef == candidate.TrustedRef &&
		accepted.TrustedCommit == candidate.TrustedCommit && accepted.SourceTree == candidate.SourceTree
}

// acceptedCandidateRecordMatches 验证候选绑定的策略、输入、generation 与 runner 记录。
func acceptedCandidateRecordMatches(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) bool {
	return accepted.PolicyDigest == candidate.PolicyDigest && accepted.ImageInputDigest == candidate.ImageInputDigest &&
		accepted.Generation == candidate.ExpectedAcceptedGeneration+1 &&
		accepted.PreviousRecordDigest == candidate.ExpectedAcceptedRecordDigest && accepted.Runner == candidate.Runner
}

// acceptedCandidateImageMatches 验证 accepted record 固定的镜像身份属于候选构建产物。
func acceptedCandidateImageMatches(
	candidate localci.PromotionCandidate,
	accepted gatecontract.AcceptedImageRecord,
) bool {
	return sameProductionImageIdentity(accepted.Image, candidate.Image)
}

// sameProductionImageIdentity 比对生产镜像的 registry、OCI、平台与 rootfs 身份字段。
func sameProductionImageIdentity(left gatecontract.ImageIdentity, right gatecontract.ImageIdentity) bool {
	return left.Registry == right.Registry && left.OCIIndexDigest == right.OCIIndexDigest &&
		left.PlatformManifestDigest == right.PlatformManifestDigest && left.ConfigDigest == right.ConfigDigest &&
		left.OS == right.OS && left.Architecture == right.Architecture && left.Variant == right.Variant &&
		slices.Equal(left.RootFSDiffIDs, right.RootFSDiffIDs)
}

// advanceBuiltCandidate 仅把完整落盘且精确衔接 accepted record 的候选推进到 trusted ref。
func (service *productionCandidateBuildService) advanceBuiltCandidate(
	ctx context.Context,
	candidate localci.PromotionCandidate,
) error {
	if candidate.PromotionMode == localci.PromotionCandidateModeExternalRef {
		return nil
	}
	if candidate.PromotionMode != localci.PromotionCandidateModeManagedTree {
		return errors.New("built candidate has an unsupported promotion mode")
	}
	accepted, err := service.managedTreeAccepted(ctx, candidate)
	if err != nil {
		return err
	}
	if err := service.authority.advanceTrustedRef(
		ctx, accepted.TrustedCommit, candidate.TrustedCommit, candidate.SourceTree,
	); err != nil {
		return service.store.MarkAdvanceFailed(ctx, candidate.WorkloadID, err)
	}
	return nil
}

// managedTreeAccepted 加载 accepted record，并验证 managed-tree 候选与其精确衔接。
func (service *productionCandidateBuildService) managedTreeAccepted(
	ctx context.Context,
	candidate localci.PromotionCandidate,
) (gatecontract.AcceptedImageRecord, error) {
	accepted, err := service.accepted.Load(ctx)
	if err != nil {
		return gatecontract.AcceptedImageRecord{}, err
	}
	expectedDigest, err := gatecontract.AcceptedImageRecordDigest(accepted)
	if err != nil {
		return gatecontract.AcceptedImageRecord{}, fmt.Errorf("digest current accepted image: %w", err)
	}
	if candidate.RepoID != accepted.RepoID || candidate.TrustedRef != accepted.TrustedRef ||
		candidate.PreviousTrustedCommit != accepted.TrustedCommit ||
		candidate.ExpectedAcceptedRecordDigest != expectedDigest ||
		candidate.ExpectedAcceptedGeneration != accepted.Generation {
		return gatecontract.AcceptedImageRecord{}, errors.New("built candidate does not exactly extend the accepted image authority")
	}
	return accepted, nil
}

// ObserveTrustedRef 只读取配置的外部 bare ref 精确 tip 与 tree。
func (authority *productionGitAuthority) ObserveTrustedRef(
	ctx context.Context,
	repoID string,
	trustedRef string,
) (localci.TrustedRefObservation, error) {
	if authority == nil || repoID != authority.repoID || trustedRef != authority.trustedRef {
		return localci.TrustedRefObservation{}, errors.New("trusted ref observation does not match production authority")
	}
	commit, err := authority.trustedTip(ctx)
	if err != nil {
		return localci.TrustedRefObservation{}, err
	}
	tree, err := authority.line(ctx, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return localci.TrustedRefObservation{}, err
	}
	return localci.TrustedRefObservation{
		RepoID: repoID, TrustedRef: trustedRef, Commit: commit, SourceTree: tree,
	}, nil
}
