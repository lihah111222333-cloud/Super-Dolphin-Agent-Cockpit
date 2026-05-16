package skill

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ErrMissingCWD aliases the canonical contract sentinel so errors.Is works
// regardless of whether the caller imported skill or contract.
var ErrMissingCWD = contract.ErrSkillMissingCWD
var ErrInvalidSkillScope = errors.New("invalid skill scope")
var ErrSkillSystemScopeRemoved = errors.New("skill system scope has been removed")
var ErrSkillSameNameConflict = contract.ErrSkillSameNameConflict

type SkillProvider = contract.SkillProvider
type SkillMirrorReport = contract.SkillMirrorReport
type SkillMirrorReportItem = contract.SkillMirrorReportItem

const (
	SkillProviderClaude = contract.SkillProviderClaude
	SkillProviderCodex  = contract.SkillProviderCodex
)

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

// WithCWD delegates to contract.WithSkillCWD. Kept for backward compatibility
// so existing callers (e.g. dashboard) that import skill.WithCWD keep compiling.
var WithCWD = contract.WithSkillCWD

func cwdFromContext(ctx context.Context) string {
	return contract.SkillCWDFromContext(ctx)
}

func requireCWD(ctx context.Context) (string, error) {
	return contract.RequireSkillCWD(ctx)
}

// RequireCWD delegates to contract.RequireSkillCWD.
var RequireCWD = contract.RequireSkillCWD

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
	contract.SkillMirrorReconciler
	SkillRevisionSource
	TrustRevisionSource
}

type SkillCommandExecutor interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
}

// SkillLister is a type alias for contract.SkillLister. The canonical
// definition now lives in internal/contract so that cross-module consumers
// (dashboard, prompt) do not need to import internal/module/skill.
type SkillLister = contract.SkillLister

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

// SkillHydrationSource is a type alias for contract.SkillHydrationSource.
// The canonical definition now lives in internal/contract so that the turn
// module can depend on contract instead of importing skill directly.
type SkillHydrationSource = contract.SkillHydrationSource

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
	DeleteLocal(ctx context.Context, p DeleteSkillParams) (any, error)
}

type DeleteSkillParams struct {
	Name         string
	Scope        string
	PersonalType string
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

func (s *service) ReconcileProviderMirrors(ctx context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	var report SkillMirrorReport
	if s == nil {
		return report, errors.New("skill service is not configured")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = cwdFromContext(ctx)
	}
	if cwd == "" {
		cwd = strings.TrimSpace(s.projectRoot)
	}
	if cwd == "" {
		return report, ErrMissingCWD
	}
	records, conflicts, err := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile()).EffectiveSet(ctx, cwd)
	if err != nil {
		return report, err
	}
	mirrorTargets, err := s.providerMirrorTargets(cwd, targets)
	if err != nil {
		return report, err
	}
	report, err = PublishSkillMirrors(ctx, records, mirrorTargets)
	appendCanonicalConflictReportItems(&report, mirrorTargets, conflicts)
	return report, err
}

func (s *service) providerMirrorTargets(cwd string, targets []contract.SkillProviderMirrorTarget) ([]SkillMirrorTarget, error) {
	out := make([]SkillMirrorTarget, 0, len(targets))
	for _, target := range targets {
		provider, err := normalizeProviderMirrorProvider(target.Provider)
		if err != nil {
			return nil, err
		}
		if isProjectProviderMirrorRoot(cwd, provider, target.SkillsRoot) {
			fingerprint := RepoFingerprint(cwd)
			out = append(out, SkillMirrorTarget{
				TargetID:        string(provider) + ":project:" + fingerprint,
				Provider:        provider,
				Scope:           skillScopeProject,
				Root:            strings.TrimSpace(target.SkillsRoot),
				CanonicalRootID: fingerprint,
			})
			continue
		}
		if !s.isAppManagedProviderMirrorRoot(provider, target.HomeRoot, target.SkillsRoot) {
			return nil, fmt.Errorf("provider mirror target is not app-managed: provider=%s skills_root=%s", provider, strings.TrimSpace(target.SkillsRoot))
		}
		owner, err := resolveOwnerIdentity(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
		if err != nil {
			return nil, err
		}
		out = append(out, SkillMirrorTarget{
			TargetID:        string(provider) + ":app-managed:" + owner.OwnerKey,
			Provider:        provider,
			Scope:           skillScopePersonal,
			Root:            strings.TrimSpace(target.SkillsRoot),
			CanonicalRootID: owner.OwnerKey,
		})
	}
	return uniqueMirrorTargets(out), nil
}

func normalizeProviderMirrorProvider(provider string) (SkillProvider, error) {
	normalized := SkillProvider(strings.ToLower(strings.TrimSpace(provider)))
	if !supportedSkillProvider(normalized) {
		return "", fmt.Errorf("unsupported skill mirror provider %q", provider)
	}
	return normalized, nil
}

func isProjectProviderMirrorRoot(cwd string, provider SkillProvider, skillsRoot string) bool {
	expected := filepath.Join(strings.TrimSpace(cwd), "."+string(provider), "skills")
	return sameCleanPath(expected, skillsRoot)
}

func (s *service) isAppManagedProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedHome := filepath.Join(s.resolvedSuperDolphinHome(), "providers", string(provider))
	return sameCleanPath(expectedHome, homeRoot) && sameCleanPath(filepath.Join(expectedHome, "skills"), skillsRoot)
}

func sameCleanPath(a, b string) bool {
	aa, errA := filepath.Abs(strings.TrimSpace(a))
	bb, errB := filepath.Abs(strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func appendCanonicalConflictReportItems(report *SkillMirrorReport, targets []SkillMirrorTarget, conflicts []canonicalSkillConflict) {
	if report == nil || len(conflicts) == 0 {
		return
	}
	for _, conflict := range conflicts {
		for _, target := range targets {
			report.Conflicts = append(report.Conflicts, SkillMirrorReportItem{
				TargetID:           target.TargetID,
				Provider:           target.Provider,
				Scope:              target.Scope,
				RelativeMirrorPath: skillSlug(conflict.Name),
				ConflictKind:       skillConflictSameName,
				Error:              ErrSkillSameNameConflict.Error(),
			})
		}
	}
}
