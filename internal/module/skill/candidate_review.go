package skill

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// errCandidateStoreUnavailable is returned when the review-gate methods
// are called but no candidate store has been wired (skipped optional
// dependency in tests / migrations not applied). Surfacing it as a
// specific error rather than nil-deref makes the misconfiguration
// visible at the RPC boundary.
var errCandidateStoreUnavailable = errors.New("skill candidate store is not configured")

// ListPendingCandidates is a thin pass-through that projects each row
// through candidateFromStore so SkillMD never escapes the service.
func (s *service) ListPendingCandidates(ctx context.Context, callerRepoFingerprint string, limit, offset int32) ([]CandidateListItem, error) {
	if s == nil || s.candidateStore == nil {
		return nil, errCandidateStoreUnavailable
	}
	callerRepoFingerprint = strings.TrimSpace(callerRepoFingerprint)
	if !skillcandidate.IsValidRepoFingerprint(callerRepoFingerprint) {
		return nil, ErrCallerFingerprintRequired
	}
	rows, err := s.candidateStore.ListPending(ctx, callerRepoFingerprint, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]CandidateListItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, candidateListItemFromStore(r))
	}
	return out, nil
}

// LookupApproval matches the approval cache by the four-tuple (scope,
// slug, content_hash, repo_fingerprint). Every field is matched
// literally - including empty repo_fingerprint - so cross-project /
// cross-scope decisions never bleed into one another. Step 5 deliberately
// does NOT write an audit row for lookups (high-frequency observation
// noise; lookup is read-only).
func (s *service) LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*CandidateListItem, error) {
	if s == nil || s.candidateStore == nil {
		return nil, errCandidateStoreUnavailable
	}
	raw, err := s.candidateStore.LookupApproval(ctx, scope, slug, contentHash, repoFingerprint)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	c := candidateListItemFromStore(*raw)
	return &c, nil
}

func (s *service) GetCandidateByID(ctx context.Context, id int64) (*Candidate, error) {
	if s == nil || s.candidateStore == nil {
		return nil, errCandidateStoreUnavailable
	}
	raw, err := s.candidateStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	c := candidateFromStore(raw)
	return &c, nil
}

// RejectCandidate records the reviewer rejection. The store enforces the
// pending_review status guard, so a non-pending row surfaces as the
// underlying conflict error rather than a silent no-op. Audit row is
// emitted on success.
func (s *service) RejectCandidate(ctx context.Context, p RejectCandidateParams) error {
	if s == nil || s.candidateStore == nil {
		return errCandidateStoreUnavailable
	}
	raw, err := s.candidateStore.GetByID(ctx, p.CandidateID)
	if err != nil {
		return err
	}
	if err := validateCandidateCaller(raw, p.CallerRepoFingerprint); err != nil {
		return err
	}
	rejected, err := s.candidateStore.Reject(ctx, p.CandidateID, p.Reason)
	if err != nil {
		return err
	}
	s.writeCandidateAudit(ctx, "reject", rejected, "", p.Reason)
	return nil
}

// ApproveCandidate runs the full approve -> CreateSkill -> MarkPromoted
// pipeline. Failures stop in place: if CreateSkill fails the row stays
// at status=approved (not promoted), preserving the review decision so
// the operator can retry without re-approving. SkillPath is populated
// whenever CreateSkill succeeded, even if MarkPromoted later errors -
// callers can locate the on-disk artifact via that field.
func (s *service) ApproveCandidate(ctx context.Context, p ApproveCandidateParams) (ApproveResult, error) {
	approved, err := s.approveCandidateForPromotion(ctx, p)
	if err != nil {
		return ApproveResult{}, err
	}

	// Land the SKILL.md via CreateSkill. The caller ctx must carry cwd;
	// CreateSkill enforces ErrMissingCWD otherwise. CreateSkill is the single
	// writer for project-scope skills (P21 P0a invariant).
	skillPath, createErr := s.promoteCandidateToSkill(ctx, approved)
	if createErr != nil {
		s.writeCandidateAudit(ctx, "approve_promote_failed", approved, p.ApprovedBy, p.Reason)
		return ApproveResult{}, createErr
	}

	return s.markCandidatePromoted(ctx, p, approved, skillPath)
}

func (s *service) approveCandidateForPromotion(ctx context.Context, p ApproveCandidateParams) (skillcandidate.Candidate, error) {
	if s == nil || s.candidateStore == nil {
		return skillcandidate.Candidate{}, errCandidateStoreUnavailable
	}
	if strings.TrimSpace(p.ApprovedBy) == "" {
		return skillcandidate.Candidate{}, ErrCandidateApprovedByRequired
	}
	raw, err := s.candidateStore.GetByID(ctx, p.CandidateID)
	if err != nil {
		return skillcandidate.Candidate{}, err
	}
	if err := validateCandidateApproval(raw, p.CallerRepoFingerprint); err != nil {
		return skillcandidate.Candidate{}, err
	}
	approvedAt := time.Now().UTC()
	return s.candidateStore.Approve(ctx, p.CandidateID, p.ApprovedBy, p.Reason, approvedAt)
}

func validateCandidateApproval(raw skillcandidate.Candidate, callerRepoFingerprint string) error {
	if err := validateCandidateCaller(raw, callerRepoFingerprint); err != nil {
		return err
	}
	if raw.Status != skillcandidate.StatusPendingReview {
		return ErrCandidateNotPending
	}
	return nil
}

func validateCandidateCaller(raw skillcandidate.Candidate, callerRepoFingerprint string) error {
	callerRepoFingerprint = strings.TrimSpace(callerRepoFingerprint)
	if !skillcandidate.IsValidRepoFingerprint(callerRepoFingerprint) {
		return ErrCallerFingerprintRequired
	}
	if raw.Scope == skillcandidate.ScopeProject && strings.TrimSpace(raw.RepoFingerprint) == "" {
		return ErrCandidateMissingFingerprint
	}
	if strings.TrimSpace(raw.RepoFingerprint) != callerRepoFingerprint {
		return ErrRepoFingerprintMismatch
	}
	return nil
}

func (s *service) markCandidatePromoted(
	ctx context.Context,
	p ApproveCandidateParams,
	approved skillcandidate.Candidate,
	skillPath string,
) (ApproveResult, error) {
	if _, err := s.candidateStore.MarkPromoted(ctx, p.CandidateID); err != nil {
		s.writeCandidateAudit(ctx, "approve_promote_failed", approved, p.ApprovedBy, p.Reason)
		return ApproveResult{OK: false, SkillPath: skillPath}, err
	}
	s.writeCandidateAudit(ctx, "approve_succeeded", approved, p.ApprovedBy, p.Reason)
	return ApproveResult{OK: true, SkillPath: skillPath}, nil
}

// promoteCandidateToSkill is the single landing path for an approved
// candidate. It MUST go through CreateSkill so the WriteLocal-only
// writer rule for project-scope skills (P21 P0a) holds. CWD is supplied
// by the caller's ctx (RPC layer wraps with scopedSkillContext); this
// helper deliberately does no cwd injection of its own.
func (s *service) promoteCandidateToSkill(ctx context.Context, c skillcandidate.Candidate) (string, error) {
	result, err := s.CreateSkill(ctx, createSkillParams{
		Name:    c.Slug,
		Content: c.SkillMD,
	})
	if err != nil {
		return "", err
	}
	if m, ok := result.(map[string]any); ok {
		if path, ok := m["path"].(string); ok {
			return path, nil
		}
	}
	return "", nil
}

// candidateFromStore is the projection that strips SkillMD before the
// row leaves the package. SkillMD is needed inside ApproveCandidate
// (passed straight to CreateSkill) but must never appear in any
// list / lookup RPC response.
func candidateFromStore(c skillcandidate.Candidate) Candidate {
	item := candidateListItemFromStore(c)
	return Candidate{
		ID:              item.ID,
		Scope:           item.Scope,
		Slug:            item.Slug,
		ContentHash:     item.ContentHash,
		RepoFingerprint: item.RepoFingerprint,
		Status:          item.Status,
		ApprovedBy:      item.ApprovedBy,
		ApprovedAt:      item.ApprovedAt,
		Reason:          item.Reason,
		RedactedSample:  c.RedactedSample,
		CreatedAt:       item.CreatedAt,
	}
}

func candidateListItemFromStore(c skillcandidate.Candidate) CandidateListItem {
	return CandidateListItem{
		ID:              c.ID,
		Scope:           c.Scope,
		Slug:            c.Slug,
		ContentHash:     c.ContentHash,
		RepoFingerprint: c.RepoFingerprint,
		Status:          c.Status,
		ApprovedBy:      c.ApprovedBy,
		ApprovedAt:      c.ApprovedAt,
		Reason:          c.Reason,
		CreatedAt:       c.CreatedAt,
	}
}
