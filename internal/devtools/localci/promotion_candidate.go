package localci

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	promotionCandidateSchemaVersion uint32 = 1
	promotionCandidateStateName            = "promotion-candidates.json"
	promotionCandidateLockName             = "promotion-candidates.lock"
	// SourceEntries use JSON base64 encoding, so the durable snapshot needs headroom above the raw tree-byte limit exercised by this repository.
	promotionCandidateMaxBytes          = 128 << 20
	promotionCandidateFailureWriteLimit = 30 * time.Second
)

var (
	ErrPromotionCandidateNotFound = errors.New("promotion candidate does not exist")
	ErrPromotionCandidateState    = errors.New("invalid promotion candidate state")
	ErrPromotionCandidateExpired  = errors.New("promotion candidate expired")
)

// PromotionCandidateStatus separates scheduler intent, build execution, awaiting trust, and terminal promotion.
type PromotionCandidateStatus string

const (
	PromotionCandidateQueued        PromotionCandidateStatus = "queued"
	PromotionCandidateBuilding      PromotionCandidateStatus = "building"
	PromotionCandidateFailed        PromotionCandidateStatus = "build_failed"
	PromotionCandidateAdvanceFailed PromotionCandidateStatus = "trusted_ref_advance_failed"
	PromotionCandidateAwaiting      PromotionCandidateStatus = "awaiting_trusted_ref"
	PromotionCandidatePromoted      PromotionCandidateStatus = "promoted"
)

// PromotionCandidateMode records whether the coordinator or an external authority advances trusted_ref.
type PromotionCandidateMode string

const (
	PromotionCandidateModeManagedTree PromotionCandidateMode = "managed_tree"
	PromotionCandidateModeExternalRef PromotionCandidateMode = "external_trusted_ref"
)

// PromotionCandidate is the owner-private durable authority for one non-runnable image candidate.
type PromotionCandidate struct {
	SchemaVersion                uint32                     `json:"schema_version"`
	CandidateID                  string                     `json:"candidate_id"`
	WorkloadID                   string                     `json:"workload_id"`
	RepoID                       string                     `json:"repo_id"`
	TrustedRef                   string                     `json:"trusted_ref"`
	TrustedCommit                string                     `json:"trusted_commit"`
	SourceTree                   string                     `json:"source_tree"`
	PreviousTrustedCommit        string                     `json:"previous_trusted_commit"`
	PolicyDigest                 string                     `json:"policy_digest"`
	Platform                     string                     `json:"platform"`
	ImageSchemaVersion           string                     `json:"image_schema_version"`
	ImageInputDigest             string                     `json:"image_input_digest"`
	ContextDigest                string                     `json:"context_digest"`
	InputManifestDigest          string                     `json:"input_manifest_digest"`
	ToolchainDigest              string                     `json:"toolchain_digest"`
	DockerfileDigest             string                     `json:"dockerfile_digest"`
	PlatformManifestDigest       string                     `json:"platform_manifest_digest,omitempty"`
	Image                        gate.ImageIdentity         `json:"image"`
	Runner                       gate.TrustedRunnerIdentity `json:"runner"`
	ExpectedAcceptedRecordDigest string                     `json:"expected_accepted_record_digest"`
	ExpectedAcceptedGeneration   uint64                     `json:"expected_accepted_generation"`
	BuildRequest                 CandidateRequest           `json:"build_request"`
	PromotionMode                PromotionCandidateMode     `json:"promotion_mode"`
	CreatedAt                    time.Time                  `json:"created_at"`
	ExpiresAt                    time.Time                  `json:"expires_at"`
	PromotionAcceptedAt          *time.Time                 `json:"promotion_accepted_at,omitempty"`
	Status                       PromotionCandidateStatus   `json:"status"`
}

type promotionCandidateSnapshot struct {
	SchemaVersion uint32               `json:"schema_version"`
	Candidates    []PromotionCandidate `json:"candidates"`
	Revision      uint64               `json:"-"`
}

// PromotionCandidatePlanRequest binds a submitted immutable tree to production trust configuration.
type PromotionCandidatePlanRequest struct {
	Tree          ReadOnlyGitTree
	PolicyDigest  string
	Platform      string
	RepoID        string
	TrustedRef    string
	TrustedCommit string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// PromotionCandidatePlan tells the scheduler whether the job needs a durable build dependency.
type PromotionCandidatePlan struct {
	BuildRequired bool
	WorkloadID    string
}

// CandidateImageIdentityResolver resolves the complete immutable identity of a host-built candidate.
type CandidateImageIdentityResolver interface {
	ResolveCandidateIdentity(context.Context, PromotionCandidate, CandidateResult) (gate.ImageIdentity, error)
}

// PromotionCandidateStore owns the canonical candidate file and its cross-process lock.
type PromotionCandidateStore struct {
	db           *sql.DB
	authorityKey string
	root         string
	statePath    string
	lockPath     string
	ownerUID     int
	now          func() time.Time
	beforeSave   func() error
	afterSave    func() error
}

// NewPromotionCandidateStoreSQLite uses the coordinator-owned SQLite ledger.
// root is retained only to perform the strictly one-time legacy JSON import.
func NewPromotionCandidateStoreSQLite(db *sql.DB, root string) (*PromotionCandidateStore, error) {
	store, err := NewPromotionCandidateStore(root)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("promotion candidate SQLite database is required")
	}
	store.db = db
	store.authorityKey = root
	return store, store.importLegacyPromotionCandidates(context.Background())
}

// NewPromotionCandidateStore 打开 owner-private 且仓库外的 candidate authority 根。
func NewPromotionCandidateStore(root string) (*PromotionCandidateStore, error) {
	ownerUID, err := currentSchedulerOwnerUID()
	if err != nil {
		return nil, fmt.Errorf("resolve promotion candidate owner: %w", err)
	}
	store := &PromotionCandidateStore{
		root: root, statePath: filepath.Join(root, promotionCandidateStateName),
		lockPath: filepath.Join(root, promotionCandidateLockName), ownerUID: ownerUID, now: time.Now,
	}
	if err := store.validateRoot(); err != nil {
		return nil, err
	}
	return store, nil
}

// Plan 解析规范镜像输入并持久创建或复用 build intent。
func (s *PromotionCandidateStore) Plan(
	ctx context.Context,
	accepted gate.AcceptedImageRecord,
	request PromotionCandidatePlanRequest,
) (PromotionCandidatePlan, error) {
	if err := s.validateCall(ctx); err != nil {
		return PromotionCandidatePlan{}, err
	}
	if err := validatePromotionCandidatePlanRequest(accepted, request); err != nil {
		return PromotionCandidatePlan{}, err
	}
	candidate, buildRequired, err := plannedPromotionCandidate(accepted, request)
	if err != nil {
		return PromotionCandidatePlan{}, err
	}
	if !buildRequired {
		return PromotionCandidatePlan{}, nil
	}
	stored, err := s.createOrReuse(ctx, candidate)
	if err != nil {
		return PromotionCandidatePlan{}, err
	}
	return PromotionCandidatePlan{BuildRequired: true, WorkloadID: stored.WorkloadID}, nil
}

// plannedPromotionCandidate 解析变更输入并组装持久化前不可变候选。
func plannedPromotionCandidate(
	accepted gate.AcceptedImageRecord,
	request PromotionCandidatePlanRequest,
) (PromotionCandidate, bool, error) {
	inputs, err := ResolveGateImageInputs(request.Tree, request.PolicyDigest, request.Platform)
	if err != nil {
		return PromotionCandidate{}, false, err
	}
	if inputs.ImageInputDigest == accepted.ImageInputDigest && request.PolicyDigest == accepted.PolicyDigest {
		return PromotionCandidate{}, false, nil
	}
	if request.TrustedCommit == accepted.TrustedCommit {
		return PromotionCandidate{}, false, errors.New("changed image inputs require a trusted commit distinct from accepted state")
	}
	buildRequest := candidateRequestFromInputs(inputs, accepted)
	prepared, err := prepareCandidate(buildRequest)
	if err != nil {
		return PromotionCandidate{}, false, err
	}
	promotionMode, err := promotionCandidateMode(request.Tree)
	if err != nil {
		return PromotionCandidate{}, false, err
	}
	expectedDigest, err := gate.AcceptedImageRecordDigest(accepted)
	if err != nil {
		return PromotionCandidate{}, false, fmt.Errorf("digest expected accepted image: %w", err)
	}
	runner := accepted.Runner
	runner.PolicyDigest = inputs.PolicyDigest
	return PromotionCandidate{
		SchemaVersion: promotionCandidateSchemaVersion, RepoID: request.RepoID, TrustedRef: request.TrustedRef,
		TrustedCommit: request.TrustedCommit, SourceTree: inputs.SubmittedSourceTree,
		PreviousTrustedCommit: accepted.TrustedCommit, PolicyDigest: inputs.PolicyDigest, Platform: inputs.Platform,
		ImageSchemaVersion: inputs.ImageSchemaVersion, ImageInputDigest: prepared.result.InputDigest,
		ContextDigest: prepared.result.ContextDigest, InputManifestDigest: prepared.result.InputManifestDigest,
		ToolchainDigest: prepared.result.ToolchainDigest, DockerfileDigest: prepared.result.DockerfileDigest,
		Runner: runner, ExpectedAcceptedRecordDigest: expectedDigest,
		ExpectedAcceptedGeneration: accepted.Generation, BuildRequest: buildRequest,
		PromotionMode: promotionMode,
		CreatedAt:     request.CreatedAt, ExpiresAt: request.ExpiresAt, Status: PromotionCandidateQueued,
	}, true, nil
}

// ExecuteBuild 运行一次 scheduler 已预留的构建并持久化完整 awaiting artifact。
func (s *PromotionCandidateStore) ExecuteBuild(
	ctx context.Context,
	workloadID string,
	builder CandidateImageBuilder,
	resolver CandidateImageIdentityResolver,
) error {
	if interfaceValueIsNil(builder) || interfaceValueIsNil(resolver) {
		return errors.New("candidate builder and identity resolver are required")
	}
	candidate, err := s.beginBuild(ctx, workloadID)
	if err != nil {
		return err
	}
	result, err := builder.EnsureCandidate(ctx, candidate.BuildRequest)
	if err != nil {
		return s.failBuild(ctx, workloadID, fmt.Errorf("build scheduled promotion candidate: %w", err))
	}
	if !result.Built {
		return s.failBuild(ctx, workloadID, errors.New("scheduled promotion candidate unexpectedly reused an accepted image"))
	}
	identity, err := resolver.ResolveCandidateIdentity(ctx, candidate, result)
	if err != nil {
		return s.failBuild(ctx, workloadID, err)
	}
	return s.completeBuild(ctx, workloadID, result, identity)
}

// EnsureCandidate 为 job 侧只读解析 awaiting candidate，绝不执行构建。
func (s *PromotionCandidateStore) EnsureCandidate(ctx context.Context, request CandidateRequest) (CandidateResult, error) {
	if err := s.validateCall(ctx); err != nil {
		return CandidateResult{}, err
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return CandidateResult{}, err
	}
	defer lock.close()
	snapshot, err := s.loadLocked()
	if err != nil {
		return CandidateResult{}, err
	}
	for _, candidate := range snapshot.Candidates {
		if reflect.DeepEqual(candidate.BuildRequest, request) && candidate.Status == PromotionCandidateAwaiting {
			if !candidate.ExpiresAt.After(s.now().UTC()) {
				return CandidateResult{}, ErrPromotionCandidateExpired
			}
			return candidate.result(), nil
		}
	}
	return CandidateResult{}, ErrPromotionCandidateNotFound
}

// Awaiting 返回可供 trusted-ref watcher 观察的稳定深拷贝。
func (s *PromotionCandidateStore) Awaiting(ctx context.Context) ([]PromotionCandidate, error) {
	if err := s.validateCall(ctx); err != nil {
		return nil, err
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.close()
	snapshot, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	result := make([]PromotionCandidate, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		if candidate.Status == PromotionCandidateAwaiting {
			result = append(result, clonePromotionCandidate(candidate))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
		return result[i].CandidateID < result[j].CandidateID
	})
	return result, nil
}

// createOrReuse 在跨进程锁内复用同一 active intent 或创建新身份。
func (s *PromotionCandidateStore) createOrReuse(ctx context.Context, candidate PromotionCandidate) (PromotionCandidate, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return PromotionCandidate{}, err
	}
	defer lock.close()
	snapshot, err := s.loadLocked()
	if err != nil {
		return PromotionCandidate{}, err
	}
	for _, existing := range snapshot.Candidates {
		if samePromotionCandidateIntent(existing, candidate) && existing.statusIsActive() &&
			existing.ExpiresAt.After(candidate.CreatedAt) {
			return clonePromotionCandidate(existing), nil
		}
	}
	snapshot.Candidates = retainLivePromotionCandidates(snapshot.Candidates, candidate.CreatedAt)
	id, err := newPromotionCandidateID()
	if err != nil {
		return PromotionCandidate{}, err
	}
	candidate.CandidateID = id
	candidate.WorkloadID = "build-" + id
	if err := candidate.Validate(); err != nil {
		return PromotionCandidate{}, err
	}
	snapshot.Candidates = append(snapshot.Candidates, candidate)
	if err := s.saveLocked(snapshot); err != nil {
		return PromotionCandidate{}, err
	}
	return clonePromotionCandidate(candidate), nil
}

// retainLivePromotionCandidates drops terminal and expired build payloads before a replacement is persisted.
func retainLivePromotionCandidates(candidates []PromotionCandidate, now time.Time) []PromotionCandidate {
	live := make([]PromotionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.statusIsActive() && candidate.ExpiresAt.After(now) {
			live = append(live, candidate)
		}
	}
	return live
}

func (s *PromotionCandidateStore) beginBuild(ctx context.Context, workloadID string) (PromotionCandidate, error) {
	var result PromotionCandidate
	err := s.mutate(ctx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status != PromotionCandidateQueued && candidate.Status != PromotionCandidateBuilding {
			return fmt.Errorf("%w: candidate %q cannot build from %q", ErrPromotionCandidateState, candidate.CandidateID, candidate.Status)
		}
		if !candidate.ExpiresAt.After(s.now().UTC()) {
			return ErrPromotionCandidateExpired
		}
		candidate.Status = PromotionCandidateBuilding
		result = clonePromotionCandidate(*candidate)
		return nil
	})
	return result, err
}

func (s *PromotionCandidateStore) completeBuild(
	ctx context.Context,
	workloadID string,
	result CandidateResult,
	identity gate.ImageIdentity,
) error {
	return s.mutate(ctx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status != PromotionCandidateBuilding {
			return fmt.Errorf("%w: candidate %q is not building", ErrPromotionCandidateState, candidate.CandidateID)
		}
		if err := validateBuiltPromotionCandidate(*candidate, result, identity); err != nil {
			return err
		}
		candidate.PlatformManifestDigest = result.ImageDigest
		candidate.Image = cloneImageIdentity(identity)
		candidate.Status = PromotionCandidateAwaiting
		return nil
	})
}

func (s *PromotionCandidateStore) setPromotionAcceptedAt(
	ctx context.Context,
	workloadID string,
	acceptedAt time.Time,
) (PromotionCandidate, error) {
	var result PromotionCandidate
	err := s.mutate(ctx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status != PromotionCandidateAwaiting {
			return fmt.Errorf("%w: candidate %q is not awaiting promotion", ErrPromotionCandidateState, candidate.CandidateID)
		}
		if candidate.PromotionAcceptedAt == nil {
			candidate.PromotionAcceptedAt = timePointer(acceptedAt)
		} else if !candidate.PromotionAcceptedAt.Equal(acceptedAt) {
			acceptedAt = *candidate.PromotionAcceptedAt
		}
		result = clonePromotionCandidate(*candidate)
		return nil
	})
	return result, err
}

func (s *PromotionCandidateStore) markPromoted(ctx context.Context, workloadID string) error {
	return s.mutate(ctx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status == PromotionCandidatePromoted {
			return nil
		}
		if candidate.Status != PromotionCandidateAwaiting || candidate.PromotionAcceptedAt == nil {
			return fmt.Errorf("%w: candidate %q cannot become promoted", ErrPromotionCandidateState, candidate.CandidateID)
		}
		candidate.Status = PromotionCandidatePromoted
		candidate.compactTerminalBuildPayload()
		return nil
	})
}

// MarkAdvanceFailed 将 managed trusted-ref CAS 未完成的 awaiting artifact 固化为终态。
func (s *PromotionCandidateStore) MarkAdvanceFailed(ctx context.Context, workloadID string, advanceErr error) error {
	if advanceErr == nil {
		return errors.New("trusted-ref advance failure is required")
	}
	cleanupCtx, cancel := BoundedCleanupContext(ctx, promotionCandidateFailureWriteLimit)
	defer cancel()
	err := s.mutate(cleanupCtx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status != PromotionCandidateAwaiting {
			return fmt.Errorf("%w: candidate %q cannot fail trusted-ref advance from %q", ErrPromotionCandidateState, candidate.CandidateID, candidate.Status)
		}
		candidate.Status = PromotionCandidateAdvanceFailed
		candidate.compactTerminalBuildPayload()
		return nil
	})
	if err != nil {
		return errors.Join(advanceErr, fmt.Errorf("persist failed trusted-ref advance candidate: %w", err))
	}
	return advanceErr
}

// compactTerminalBuildPayload drops source bytes after the workload can no longer build.
// Digests, Git identity, image identity, and recovery authority remain durable.
func (candidate *PromotionCandidate) compactTerminalBuildPayload() {
	if candidate == nil || candidate.statusIsActive() {
		return
	}
	candidate.BuildRequest.SourceEntries = nil
}

// mutate 在规范快照中按 workload ID 原子提交单个 candidate 状态变更。
func (s *PromotionCandidateStore) mutate(
	ctx context.Context,
	workloadID string,
	mutation func(*PromotionCandidate) error,
) error {
	if err := s.validateCall(ctx); err != nil {
		return err
	}
	if workloadID == "" || mutation == nil {
		return errors.New("promotion candidate workload and mutation are required")
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer lock.close()
	snapshot, err := s.loadLocked()
	if err != nil {
		return err
	}
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].WorkloadID != workloadID {
			continue
		}
		if err := mutation(&snapshot.Candidates[index]); err != nil {
			return err
		}
		if err := snapshot.Candidates[index].Validate(); err != nil {
			return err
		}
		return s.saveLocked(snapshot)
	}
	return ErrPromotionCandidateNotFound
}

// validateRoot 拒绝非规范、共享、链接或非当前 owner 的 authority 根。
func (s *PromotionCandidateStore) validateRoot() error {
	if s == nil || s.root == "" || !filepath.IsAbs(s.root) || filepath.Clean(s.root) != s.root {
		return errors.New("promotion candidate root must be canonical and absolute")
	}
	canonical, err := filepath.EvalSymlinks(s.root)
	if err != nil || canonical != s.root {
		return errors.Join(errors.New("promotion candidate root must not contain symlinks"), err)
	}
	info, err := os.Lstat(s.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("promotion candidate root must be a real directory"), err)
	}
	return validatePrivateOwnerAndMode(info, s.ownerUID, true)
}

func (s *PromotionCandidateStore) validateCall(ctx context.Context) error {
	if s == nil || ctx == nil || s.now == nil {
		return errors.New("promotion candidate store and context are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.db != nil {
		return nil
	}
	return s.validateRoot()
}

// Validate 拒绝不完整绑定以及 awaiting 前出现的任何可运行 artifact。
func (candidate PromotionCandidate) Validate() error {
	validators := []func() error{
		candidate.validateIdentity, candidate.validateDigests, candidate.validateLifetimeAndRunner,
		candidate.validateBuildBinding, candidate.validatePromotionMode, candidate.validateStatus,
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateIdentity 校验 schema、candidate/workload、repository 与 Git 对象身份。
func (candidate PromotionCandidate) validateIdentity() error {
	if candidate.SchemaVersion != promotionCandidateSchemaVersion {
		return errors.New("promotion candidate schema version is unsupported")
	}
	if candidate.CandidateID == "" || candidate.WorkloadID != "build-"+candidate.CandidateID {
		return errors.New("promotion candidate identity is not canonical")
	}
	if candidate.RepoID == "" || !strings.HasPrefix(candidate.TrustedRef, "refs/") {
		return errors.New("promotion candidate repository and trusted ref are required")
	}
	for name, value := range map[string]string{
		"trusted commit": candidate.TrustedCommit, "source tree": candidate.SourceTree,
		"previous trusted commit": candidate.PreviousTrustedCommit,
	} {
		if !gitObjectPattern.MatchString(value) {
			return fmt.Errorf("promotion candidate %s is not a canonical Git object", name)
		}
	}
	return nil
}

// validateDigests 校验构建闭包与 expected accepted digest/generation。
func (candidate PromotionCandidate) validateDigests() error {
	for name, value := range map[string]string{
		"policy": candidate.PolicyDigest, "input": candidate.ImageInputDigest,
		"context": candidate.ContextDigest, "manifest": candidate.InputManifestDigest,
		"toolchain": candidate.ToolchainDigest, "Dockerfile": candidate.DockerfileDigest,
		"expected accepted record": candidate.ExpectedAcceptedRecordDigest,
	} {
		if err := validateDigest("promotion candidate "+name+" digest", value); err != nil {
			return err
		}
	}
	if candidate.ExpectedAcceptedGeneration == 0 || candidate.Platform == "" || candidate.ImageSchemaVersion == "" {
		return errors.New("promotion candidate generation, platform, and image schema are required")
	}
	return nil
}

// validateLifetimeAndRunner 校验 candidate 生命周期和 runner policy 绑定。
func (candidate PromotionCandidate) validateLifetimeAndRunner() error {
	if candidate.CreatedAt.IsZero() || candidate.ExpiresAt.IsZero() ||
		candidate.CreatedAt.Location() != time.UTC || candidate.ExpiresAt.Location() != time.UTC ||
		!candidate.ExpiresAt.After(candidate.CreatedAt) {
		return errors.New("promotion candidate lifetime must be ordered non-zero UTC timestamps")
	}
	if err := candidate.Runner.Validate(); err != nil {
		return fmt.Errorf("promotion candidate runner: %w", err)
	}
	if candidate.Runner.PolicyDigest != candidate.PolicyDigest {
		return errors.New("promotion candidate runner policy does not match candidate policy")
	}
	return nil
}

// validateBuildBinding 校验 durable build request 未脱离 candidate identity。
func (candidate PromotionCandidate) validateBuildBinding() error {
	if candidate.BuildRequest.SourceTreeSHA != candidate.SourceTree ||
		candidate.BuildRequest.PolicyDigest != candidate.PolicyDigest || candidate.BuildRequest.Platform != candidate.Platform {
		return errors.New("promotion candidate build request identity drifted")
	}
	return nil
}

func (candidate PromotionCandidate) validatePromotionMode() error {
	switch candidate.PromotionMode {
	case PromotionCandidateModeManagedTree, PromotionCandidateModeExternalRef:
		return nil
	default:
		return fmt.Errorf("unsupported promotion candidate mode %q", candidate.PromotionMode)
	}
}

func promotionCandidateMode(tree ReadOnlyGitTree) (PromotionCandidateMode, error) {
	switch tree.Source.Kind {
	case gate.SourceKindTree:
		return PromotionCandidateModeManagedTree, nil
	case gate.SourceKindCommit, gate.SourceKindRange:
		return PromotionCandidateModeExternalRef, nil
	default:
		return "", fmt.Errorf("unsupported promotion candidate source kind %q", tree.Source.Kind)
	}
}

// validateStatus 阻断 awaiting 前的 runnable identity 与非法晋升状态。
func (candidate PromotionCandidate) validateStatus() error {
	switch candidate.Status {
	case PromotionCandidateQueued, PromotionCandidateBuilding, PromotionCandidateFailed:
		return candidate.validateUnbuiltStatus()
	case PromotionCandidateAdvanceFailed, PromotionCandidateAwaiting, PromotionCandidatePromoted:
		return candidate.validateBuiltStatus()
	default:
		return fmt.Errorf("unsupported promotion candidate status %q", candidate.Status)
	}
}

// statusIsActive 仅允许仍可由既有 scheduler identity 处理的候选被同输入复用。
func (candidate PromotionCandidate) statusIsActive() bool {
	return candidate.Status == PromotionCandidateQueued || candidate.Status == PromotionCandidateBuilding ||
		candidate.Status == PromotionCandidateAwaiting
}

// validateUnbuiltStatus 阻断 queued、building 或 build_failed candidate 携带任何 artifact 或签名时间。
func (candidate PromotionCandidate) validateUnbuiltStatus() error {
	if candidate.PlatformManifestDigest != "" || candidate.Image.Registry != "" || candidate.PromotionAcceptedAt != nil {
		return errors.New("unbuilt promotion candidate contains runnable or promotion state")
	}
	return nil
}

// validateBuiltStatus 校验 awaiting/promoted candidate 的完整 artifact 与签名时间。
func (candidate PromotionCandidate) validateBuiltStatus() error {
	if err := validateDigest("promotion candidate platform manifest digest", candidate.PlatformManifestDigest); err != nil {
		return err
	}
	if err := candidate.Image.Validate(); err != nil {
		return fmt.Errorf("promotion candidate image: %w", err)
	}
	if candidate.Image.PlatformManifestDigest != candidate.PlatformManifestDigest {
		return errors.New("promotion candidate image manifest digest drifted")
	}
	if candidate.Status == PromotionCandidatePromoted && candidate.PromotionAcceptedAt == nil {
		return errors.New("promoted candidate is missing accepted timestamp")
	}
	if candidate.PromotionAcceptedAt != nil && (candidate.PromotionAcceptedAt.IsZero() || candidate.PromotionAcceptedAt.Location() != time.UTC) {
		return errors.New("promotion accepted timestamp must be non-zero UTC")
	}
	return nil
}

// Validate 校验 snapshot schema、candidate 合同与身份唯一性。
func (snapshot promotionCandidateSnapshot) Validate() error {
	if snapshot.SchemaVersion != promotionCandidateSchemaVersion || snapshot.Candidates == nil {
		return errors.New("promotion candidate snapshot is invalid")
	}
	ids := make(map[string]struct{}, len(snapshot.Candidates))
	workloads := make(map[string]struct{}, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		if err := candidate.Validate(); err != nil {
			return err
		}
		if _, exists := ids[candidate.CandidateID]; exists {
			return errors.New("promotion candidate snapshot repeats candidate identity")
		}
		if _, exists := workloads[candidate.WorkloadID]; exists {
			return errors.New("promotion candidate snapshot repeats workload identity")
		}
		ids[candidate.CandidateID] = struct{}{}
		workloads[candidate.WorkloadID] = struct{}{}
	}
	return nil
}

// validatePromotionCandidatePlanRequest 校验 plan 与当前 accepted authority 精确绑定。
func validatePromotionCandidatePlanRequest(accepted gate.AcceptedImageRecord, request PromotionCandidatePlanRequest) error {
	if err := accepted.Validate(); err != nil {
		return err
	}
	if request.RepoID != accepted.RepoID || request.TrustedRef != accepted.TrustedRef {
		return errors.New("promotion candidate plan does not match accepted repository authority")
	}
	if !gitObjectPattern.MatchString(request.TrustedCommit) {
		return errors.New("promotion candidate requires a canonical trusted commit")
	}
	if request.CreatedAt.IsZero() || request.CreatedAt.Location() != time.UTC ||
		request.ExpiresAt.Location() != time.UTC || !request.ExpiresAt.After(request.CreatedAt) {
		return errors.New("promotion candidate plan lifetime is invalid")
	}
	return nil
}

// validateBuiltPromotionCandidate 复核构建摘要闭包和完整不可变镜像身份。
func validateBuiltPromotionCandidate(candidate PromotionCandidate, result CandidateResult, identity gate.ImageIdentity) error {
	if !candidateResultMatches(candidate, result) {
		return errors.New("built promotion candidate digest closure drifted")
	}
	if !result.Built || result.ImageDigest == "" || result.ConfigDigest == "" {
		return errors.New("promotion candidate build did not produce a new immutable artifact")
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	if identity.PlatformManifestDigest != result.ImageDigest || identity.ConfigDigest != result.ConfigDigest {
		return errors.New("resolved promotion image does not match BuildKit manifest and config digests")
	}
	return nil
}

// candidateResultMatches 比较 builder 输出与 durable 输入闭包的全部摘要。
func candidateResultMatches(candidate PromotionCandidate, result CandidateResult) bool {
	return result.SourceTreeSHA == candidate.SourceTree && result.InputDigest == candidate.ImageInputDigest &&
		result.ContextDigest == candidate.ContextDigest && result.InputManifestDigest == candidate.InputManifestDigest &&
		result.ToolchainDigest == candidate.ToolchainDigest && result.DockerfileDigest == candidate.DockerfileDigest
}

// samePromotionCandidateIntent 只复用 expected accepted CAS 状态也完全一致的 intent。
func samePromotionCandidateIntent(left, right PromotionCandidate) bool {
	return left.RepoID == right.RepoID && left.TrustedRef == right.TrustedRef &&
		left.TrustedCommit == right.TrustedCommit && left.SourceTree == right.SourceTree &&
		left.PolicyDigest == right.PolicyDigest && left.Platform == right.Platform &&
		left.ImageInputDigest == right.ImageInputDigest &&
		left.ExpectedAcceptedRecordDigest == right.ExpectedAcceptedRecordDigest &&
		left.ExpectedAcceptedGeneration == right.ExpectedAcceptedGeneration
}

func (candidate PromotionCandidate) result() CandidateResult {
	return CandidateResult{
		SourceTreeSHA: candidate.SourceTree, InputDigest: candidate.ImageInputDigest,
		ContextDigest: candidate.ContextDigest, InputManifestDigest: candidate.InputManifestDigest,
		ToolchainDigest: candidate.ToolchainDigest, DockerfileDigest: candidate.DockerfileDigest,
		ImageDigest: candidate.PlatformManifestDigest, ConfigDigest: candidate.Image.ConfigDigest, Built: true,
	}
}

func clonePromotionCandidate(candidate PromotionCandidate) PromotionCandidate {
	candidate.Image = cloneImageIdentity(candidate.Image)
	candidate.BuildRequest.SourceEntries = cloneTreeEntries(candidate.BuildRequest.SourceEntries)
	if candidate.PromotionAcceptedAt != nil {
		candidate.PromotionAcceptedAt = timePointer(*candidate.PromotionAcceptedAt)
	}
	return candidate
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func newPromotionCandidateID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate promotion candidate ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

// validateLockedImageArgumentDefaults 确保 Dockerfile 和工具链锁不会形成两个漂移的参数真值。
func validateLockedImageArgumentDefaults(lines []string, locked map[string]string) error {
	declared := make(map[string]struct{}, len(locked))
	for _, line := range lines {
		name, reachedFrom, err := lockedImageArgumentDefault(line, locked)
		if err != nil {
			return err
		}
		if reachedFrom {
			break
		}
		if name == "" {
			continue
		}
		if _, duplicate := declared[name]; duplicate {
			return fmt.Errorf("Dockerfile ARG %q is declared more than once before FROM", name)
		}
		declared[name] = struct{}{}
	}
	for name := range locked {
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("Dockerfile must declare locked ARG %q with a default before FROM", name)
		}
	}
	return nil
}

// lockedImageArgumentDefault 解析首个 FROM 之前受锁约束的镜像参数默认值。
func lockedImageArgumentDefault(line string, locked map[string]string) (string, bool, error) {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", false, nil
	}
	instruction := fields[0]
	body := strings.TrimSpace(trimmed[len(instruction):])
	if strings.EqualFold(instruction, "FROM") {
		return "", true, nil
	}
	if !strings.EqualFold(instruction, "ARG") {
		return "", false, nil
	}
	name, value, hasDefault := strings.Cut(strings.TrimSpace(body), "=")
	expected, isLocked := locked[name]
	if !isLocked {
		return "", false, nil
	}
	if !hasDefault || value == "" {
		return "", false, fmt.Errorf("Dockerfile ARG %q must default to its toolchain lock value", name)
	}
	if value != expected {
		return "", false, fmt.Errorf("Dockerfile ARG %q default does not match the toolchain lock", name)
	}
	return name, false, nil
}
