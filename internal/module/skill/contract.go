package skill

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type skillCWDContextKey struct{}

var ErrMissingCWD = errors.New("cwd is required")
var ErrInvalidSkillScope = errors.New("invalid skill scope")

// P0b Step 5 sentinels for the candidate review gate. These are mapped to
// jrpc2.InvalidParams by skillRPCError so the host layer can surface the
// reason without leaking internal state details.
var (
	ErrCandidateNotPending         = errors.New("skill candidate is not pending review")
	ErrCandidateMissingFingerprint = errors.New("project-scope candidate requires repo fingerprint")
	ErrCandidateApprovedByRequired = errors.New("approved_by is required")
	ErrCallerFingerprintRequired   = errors.New("caller repo fingerprint is required")
	ErrRepoFingerprintMismatch     = errors.New("candidate repo fingerprint does not match caller")
)

// WithCWD scopes a skill request to a specific cwd. Empty cwd is a no-op so
// downstream callers can detect the missing scope explicitly.
func WithCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ctx
	}
	return context.WithValue(ctx, skillCWDContextKey{}, cwd)
}

func cwdFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(skillCWDContextKey{}).(string)
	return strings.TrimSpace(value)
}

func requireCWD(ctx context.Context) (string, error) {
	cwd := cwdFromContext(ctx)
	if cwd == "" {
		return "", ErrMissingCWD
	}
	return cwd, nil
}

func RequireCWD(ctx context.Context) (string, error) {
	return requireCWD(ctx)
}

// ApproveCandidateParams drives Service.ApproveCandidate. ApprovedBy is
// required (sentinel ErrCandidateApprovedByRequired); Reason is free-form
// reviewer text persisted alongside the approval.
type ApproveCandidateParams struct {
	CandidateID           int64
	ApprovedBy            string
	Reason                string
	CallerRepoFingerprint string
}

type RejectCandidateParams struct {
	CandidateID           int64
	Reason                string
	CallerRepoFingerprint string
}

// ApproveResult is the return shape for Service.ApproveCandidate. OK
// reflects the full approve+promote pipeline; SkillPath is populated as
// soon as CreateSkill writes the SKILL.md, even if MarkPromoted later
// fails (so callers can locate the on-disk artifact).
type ApproveResult struct {
	OK        bool   `json:"ok"`
	SkillPath string `json:"skill_path"`
}

// Candidate is the RPC-safe projection of skillcandidate.Candidate. It
// deliberately omits SkillMD: the full SKILL.md text is internal to the
// review gate and must never appear in list / lookup responses.
type Candidate struct {
	ID              int64      `json:"id"`
	Scope           string     `json:"scope"`
	Slug            string     `json:"slug"`
	ContentHash     string     `json:"content_hash"`
	RepoFingerprint string     `json:"repo_fingerprint"`
	Status          string     `json:"status"`
	ApprovedBy      string     `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	RedactedSample  string     `json:"redacted_sample,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type CandidateListItem struct {
	ID              int64      `json:"id"`
	Scope           string     `json:"scope"`
	Slug            string     `json:"slug"`
	ContentHash     string     `json:"content_hash"`
	RepoFingerprint string     `json:"repo_fingerprint"`
	Status          string     `json:"status"`
	ApprovedBy      string     `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Service is the backwards-compatible aggregate for the skill module itself
// and the RPC handler surface. Cross-module consumers should depend on the
// narrow ports below instead of this full method set.
type Service interface {
	contract.ApprovalSource
	SkillCommandExecutor
	SkillLister
	SkillHydrationSource
	skillLocalMutationStore
	skillRemoteStore
	skillConfigStore
	skillPreviewer
	skillLegacyExpander
	skillCandidateReviewer
	SkillRevisionSource
	TrustRevisionSource
}

type SkillCommandExecutor interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
}

// SkillLister is the read-only skill catalog port used by dashboard and prompt
// consumers that only need skill metadata.
type SkillLister interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
}

type SkillRevisionSource interface {
	SkillRevision() uint64
}

type TrustRevisionSource interface {
	TrustRevision() uint64
}

// SkillCatalogSource is the prompt catalog provider's complete skill-side
// dependency: metadata listing, approval probing, and revision invalidation.
type SkillCatalogSource interface {
	SkillLister
	contract.ApprovalSource
	SkillRevisionSource
	TrustRevisionSource
}

// SkillHydrationSource is the turn service's minimal dependency for resolving
// name-only skill references before provider submission.
type SkillHydrationSource interface {
	SkillLister
	ReadLocal(ctx context.Context, path string) (any, error)
}

type skillLocalMutationStore interface {
	ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error)
	WriteLocal(ctx context.Context, path, content string, scope ...string) (any, error)
	// CreateSkill is the host-side project-scope self-learning entry point.
	// It is a thin wrapper over WriteLocal(..., scope=project) and rejects
	// requests missing cwd with ErrMissingCWD.
	CreateSkill(ctx context.Context, p createSkillParams) (any, error)
	// ImportLocalDir supports mode=auto|single|batch; auto preserves single
	// skill imports and expands container directories into direct child skills.
	ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error)
	DeleteLocal(ctx context.Context, name string) (any, error)
}

type skillRemoteStore interface {
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
}

type skillConfigStore interface {
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
}

type skillPreviewer interface {
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
}

type skillLegacyExpander interface {
	Expand(ctx context.Context, p skillExpandParams) (skillExpandResult, error)
}

type skillCandidateReviewer interface {
	// ApproveCandidate (P0b Step 5): promote a pending candidate to a
	// project-scope SKILL.md via CreateSkill. The caller ctx must carry
	// cwd (use WithCWD); CreateSkill will reject with ErrMissingCWD
	// otherwise.
	ApproveCandidate(ctx context.Context, p ApproveCandidateParams) (ApproveResult, error)
	// RejectCandidate (P0b Step 5): mark a pending candidate as rejected
	// with reviewer-supplied reason. No on-disk artifact is created.
	RejectCandidate(ctx context.Context, p RejectCandidateParams) error
	// ListPendingCandidates (P0b Step 5): paginate candidates currently
	// awaiting review. Result excludes SkillMD (Candidate is a
	// projection).
	ListPendingCandidates(ctx context.Context, callerRepoFingerprint string, limit, offset int32) ([]CandidateListItem, error)
	// LookupApproval (P0b Step 5): probe the approval cache by the
	// (scope, slug, content_hash, repo_fingerprint) tuple. Returns
	// (nil, nil) on miss; literal repo_fingerprint matching keeps
	// approvals isolated per project.
	LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*CandidateListItem, error)
	GetCandidateByID(ctx context.Context, id int64) (*Candidate, error)
}
