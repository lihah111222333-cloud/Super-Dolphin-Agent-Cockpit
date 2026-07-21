package main

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type productionPromotionAuthority struct {
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
	state, err := localci.NewAcceptedImageState(config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, fmt.Errorf("open accepted image state: %w", err)
	}
	candidates, err := localci.NewPromotionCandidateStore(config.CandidateStateRoot)
	if err != nil {
		return nil, fmt.Errorf("open promotion candidate state: %w", err)
	}
	signer, err := newProductionAcceptedImageSigner(config)
	if err != nil {
		return nil, err
	}
	return &productionPromotionAuthority{
		state: state, accepted: &productionAcceptedImageLoader{state: state, authority: authority},
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
	accepted   *productionAcceptedImageLoader
	authority  *productionGitAuthority
	candidates *localci.PromotionCandidateStore
	config     productionCoordinatorConfig
	now        func() time.Time
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
	state, err := localci.NewAcceptedImageState(config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, err
	}
	candidates, err := localci.NewPromotionCandidateStore(config.CandidateStateRoot)
	if err != nil {
		return nil, err
	}
	return &productionCandidateSubmissionPlanner{
		accepted: &productionAcceptedImageLoader{state: state, authority: authority}, authority: authority,
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
	if err != nil || !managedTreeCommit || !plan.BuildRequired {
		return plan, err
	}
	if err := planner.authority.advanceTrustedRef(ctx, accepted.TrustedCommit, trustedCommit, tree.Source.SourceTreeSHA); err != nil {
		return localci.PromotionCandidatePlan{}, err
	}
	return plan, nil
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
	} else if err := planner.authority.verifyRecord(ctx, accepted); err != nil {
		return err
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
	store    *localci.PromotionCandidateStore
	builder  localci.CandidateImageBuilder
	resolver localci.CandidateImageIdentityResolver
}

// ExecuteBuild 执行 scheduler 已授予 slot 的唯一 candidate build。
func (service *productionCandidateBuildService) ExecuteBuild(ctx context.Context, workloadID string) error {
	if service == nil || service.store == nil {
		return errors.New("production candidate build service is not configured")
	}
	return service.store.ExecuteBuild(ctx, workloadID, service.builder, service.resolver)
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
