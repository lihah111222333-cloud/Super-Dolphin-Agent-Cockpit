package skill

import (
	"context"
	"errors"
	"strings"
	"time"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// errCandidateStoreUnavailable is returned when the review-gate methods
// are called but no candidate store has been wired (skipped optional
// dependency in tests / migrations not applied). Surfacing it as a
// specific error rather than nil-deref makes the misconfiguration
// visible at the RPC boundary.
var errCandidateStoreUnavailable = errors.New("skill candidate store is not configured")

// setCandidateStore is the fx setter used by registerCandidateStores
// (module.go). Kept package-private so tests in the same package can
// also use it; production code must go through fx.
func (s *service) setCandidateStore(c skillcandidate.Store) {
	if s == nil {
		return
	}
	s.candidateStore = c
}

// setAuditStore mirrors setCandidateStore for the auditlog dependency.
// auditStore is optional; nil disables audit emission entirely.
func (s *service) setAuditStore(a auditstore.Store) {
	if s == nil {
		return
	}
	s.auditStore = a
}

// ListPendingCandidates is a thin pass-through that projects each row
// through candidateFromStore so SkillMD never escapes the service.
func (s *service) ListPendingCandidates(ctx context.Context, limit, offset int32) ([]Candidate, error) {
	if s == nil || s.candidateStore == nil {
		return nil, errCandidateStoreUnavailable
	}
	rows, err := s.candidateStore.ListPending(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, candidateFromStore(r))
	}
	return out, nil
}

// LookupApproval matches the approval cache by the four-tuple (scope,
// slug, content_hash, repo_fingerprint). Every field is matched
// literally - including empty repo_fingerprint - so cross-project /
// cross-scope decisions never bleed into one another. Step 5 deliberately
// does NOT write an audit row for lookups (high-frequency observation
// noise; lookup is read-only).
func (s *service) LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error) {
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
	c := candidateFromStore(*raw)
	return &c, nil
}

// RejectCandidate records the reviewer rejection. The store enforces the
// pending_review status guard, so a non-pending row surfaces as the
// underlying conflict error rather than a silent no-op. Audit row is
// emitted on success.
func (s *service) RejectCandidate(ctx context.Context, id int64, reason string) error {
	if s == nil || s.candidateStore == nil {
		return errCandidateStoreUnavailable
	}
	rejected, err := s.candidateStore.Reject(ctx, id, reason)
	if err != nil {
		return err
	}
	s.writeCandidateAudit(ctx, "reject", rejected, "", reason)
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
	if err := validateCandidateApproval(raw); err != nil {
		return skillcandidate.Candidate{}, err
	}
	approvedAt := time.Now().UTC()
	return s.candidateStore.Approve(ctx, p.CandidateID, p.ApprovedBy, p.Reason, approvedAt)
}

func validateCandidateApproval(raw skillcandidate.Candidate) error {
	if raw.Scope == skillcandidate.ScopeProject && strings.TrimSpace(raw.RepoFingerprint) == "" {
		return ErrCandidateMissingFingerprint
	}
	if raw.Status != skillcandidate.StatusPendingReview {
		return ErrCandidateNotPending
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
	return Candidate{
		ID:              c.ID,
		Scope:           c.Scope,
		Slug:            c.Slug,
		ContentHash:     c.ContentHash,
		RepoFingerprint: c.RepoFingerprint,
		Status:          c.Status,
		ApprovedBy:      c.ApprovedBy,
		ApprovedAt:      c.ApprovedAt,
		Reason:          c.Reason,
		RedactedSample:  c.RedactedSample,
		CreatedAt:       c.CreatedAt,
	}
}
