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
	CandidateID int64
	ApprovedBy  string
	Reason      string
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

type Service interface {
	contract.ApprovalSource
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
	ListSkills(ctx context.Context) ([]SkillInfo, error)
	ReadLocal(ctx context.Context, path string) (any, error)
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
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
	Expand(ctx context.Context, p skillExpandParams) (skillExpandResult, error)
	// ExpandBody (P20.1 Phase 6): read SKILL.md body by name with optional
	// Markdown anchor slicing.
	ExpandBody(ctx context.Context, p ExpandBodyParams) (ExpandBodyResult, error)
	// ReadResource (P20.1 Phase 6): read a resource file from the skill
	// directory by name + relative path.
	ReadResource(ctx context.Context, p ReadResourceParams) (ReadResourceResult, error)
	// ApproveCandidate (P0b Step 5): promote a pending candidate to a
	// project-scope SKILL.md via CreateSkill. The caller ctx must carry
	// cwd (use WithCWD); CreateSkill will reject with ErrMissingCWD
	// otherwise.
	ApproveCandidate(ctx context.Context, p ApproveCandidateParams) (ApproveResult, error)
	// RejectCandidate (P0b Step 5): mark a pending candidate as rejected
	// with reviewer-supplied reason. No on-disk artifact is created.
	RejectCandidate(ctx context.Context, id int64, reason string) error
	// ListPendingCandidates (P0b Step 5): paginate candidates currently
	// awaiting review. Result excludes SkillMD (Candidate is a
	// projection).
	ListPendingCandidates(ctx context.Context, limit, offset int32) ([]Candidate, error)
	// LookupApproval (P0b Step 5): probe the approval cache by the
	// (scope, slug, content_hash, repo_fingerprint) tuple. Returns
	// (nil, nil) on miss; literal repo_fingerprint matching keeps
	// approvals isolated per project.
	LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error)
	SkillRevision() uint64
	TrustRevision() uint64
}
