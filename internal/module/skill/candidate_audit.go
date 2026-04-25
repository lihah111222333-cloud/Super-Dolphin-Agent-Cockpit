package skill

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// candidateAuditEvent is the event_type written into auditlog for the
// review gate. Action names below are the second axis (event in the
// brief) used to distinguish approve_succeeded / approve_promote_failed
// / reject without grepping detail blobs.
const candidateAuditEventType = "skill_candidate_review"

// writeCandidateAudit appends a row to auditlog describing a review-gate
// transition. The auditlog is a best-effort observability sink: when it
// fails (e.g. db down) we log a warning and swallow the error so we do
// not regress the primary approve / reject flow. The audit row is NOT a
// transaction boundary.
//
// approvedBy is empty for reject events (reviewer identity is not
// captured by Store.Reject); reason carries the reviewer-supplied
// justification when set.
func (s *service) writeCandidateAudit(ctx context.Context, event string, c skillcandidate.Candidate, approvedBy, reason string) {
	if s == nil || s.auditStore == nil {
		return
	}
	event = strings.TrimSpace(event)
	if event == "" {
		return
	}
	extra := candidateAuditExtra(event, c, approvedBy, reason)
	params := auditstore.InsertParams{
		EventType: candidateAuditEventType,
		Action:    event,
		Result:    candidateAuditResult(event),
		Actor:     strings.TrimSpace(approvedBy),
		Target:    candidateAuditTarget(c),
		Detail:    candidateAuditDetail(c, reason),
		Level:     candidateAuditLevel(event),
		Extra:     extra,
	}
	if err := s.auditStore.Insert(ctx, params); err != nil {
		// Audit writes are observation-only; never fail the caller.
		pkglogger.Warnw("skill candidate audit insert failed",
			"event", event,
			"candidate_id", c.ID,
			"error", err,
		)
	}
}

func candidateAuditExtra(event string, c skillcandidate.Candidate, approvedBy, reason string) json.RawMessage {
	payload := map[string]any{
		"event":            event,
		"candidate_id":     c.ID,
		"scope":            c.Scope,
		"slug":             c.Slug,
		"content_hash":     c.ContentHash,
		"repo_fingerprint": c.RepoFingerprint,
		"status":           c.Status,
	}
	if approvedBy = strings.TrimSpace(approvedBy); approvedBy != "" {
		payload["approved_by"] = approvedBy
	}
	if c.ApprovedAt != nil {
		payload["approved_at"] = c.ApprovedAt.UTC().Format(time.RFC3339Nano)
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		payload["reason"] = reason
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

func candidateAuditResult(event string) string {
	switch event {
	case "approve_succeeded":
		return "success"
	case "approve_promote_failed":
		return "failure"
	case "reject":
		return "rejected"
	default:
		return ""
	}
}

func candidateAuditLevel(event string) string {
	if event == "approve_promote_failed" {
		return "warn"
	}
	return "info"
}

func candidateAuditTarget(c skillcandidate.Candidate) string {
	slug := strings.TrimSpace(c.Slug)
	if slug == "" {
		return ""
	}
	scope := strings.TrimSpace(c.Scope)
	if scope == "" {
		return slug
	}
	return scope + "/" + slug
}

func candidateAuditDetail(c skillcandidate.Candidate, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return reason
	}
	if hash := strings.TrimSpace(c.ContentHash); hash != "" {
		return "content_hash=" + hash
	}
	return ""
}
