package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type upsertIntentDraftQuerier interface {
	UpsertPromptIntentDraft(ctx context.Context, arg sqlc.UpsertPromptIntentDraftParams) (sqlc.UpsertPromptIntentDraftRow, error)
}

type getIntentDraftQuerier interface {
	GetPromptIntentDraft(ctx context.Context, arg sqlc.GetPromptIntentDraftParams) (sqlc.GetPromptIntentDraftRow, error)
}

type listIntentDraftsQuerier interface {
	ListPromptIntentDrafts(ctx context.Context, arg sqlc.ListPromptIntentDraftsParams) ([]sqlc.ListPromptIntentDraftsRow, error)
}

type updateIntentDraftStatusQuerier interface {
	UpdatePromptIntentDraftStatus(ctx context.Context, arg sqlc.UpdatePromptIntentDraftStatusParams) (sqlc.UpdatePromptIntentDraftStatusRow, error)
}

// UpsertIntentDraft 处理upsertintentdraft。
func (s *store) UpsertIntentDraft(ctx context.Context, draft PromptIntentDraft) (*PromptIntentDraft, error) {
	if err := validatePromptIntentDraft(draft); err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_intent_drafts")
	}
	generatedCard, err := normalizePromptIntentJSON(draft.GeneratedCard, "{}")
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_intent_drafts")
	}
	issues, err := normalizePromptIntentJSON(draft.Issues, "[]")
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_intent_drafts")
	}
	q, ok := s.q.(upsertIntentDraftQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support upsert_intent_draft"), "upsert", "prompt_intent_drafts")
	}
	row, err := q.UpsertPromptIntentDraft(ctx, sqlc.UpsertPromptIntentDraftParams{
		DraftKey:      strings.TrimSpace(draft.DraftKey),
		CWD:           strings.TrimSpace(draft.CWD),
		Kind:          strings.TrimSpace(draft.Kind),
		RawInput:      strings.TrimSpace(draft.RawInput),
		SourceType:    strings.TrimSpace(draft.SourceType),
		SourceUrl:     strings.TrimSpace(draft.SourceURL),
		OriginHash:    strings.TrimSpace(draft.OriginHash),
		LicenseHint:   strings.TrimSpace(draft.LicenseHint),
		GeneratedCard: generatedCard,
		Confidence:    draft.Confidence,
		Status:        strings.TrimSpace(draft.Status),
		Scope:         normalizePromptIntentDraftScope(draft.Scope),
		Issues:        issues,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_intent_drafts")
	}
	mapped := fromSQLCPromptIntentDraft(row)
	return &mapped, nil
}

// GetIntentDraft 读取intentdraft。
func (s *store) GetIntentDraft(ctx context.Context, cwd, draftKey string) (*PromptIntentDraft, error) {
	cwd, draftKey, err := requireIntentDraftScope(cwd, draftKey)
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_intent_drafts")
	}
	q, ok := s.q.(getIntentDraftQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support get_intent_draft"), "get", "prompt_intent_drafts")
	}
	row, err := q.GetPromptIntentDraft(ctx, sqlc.GetPromptIntentDraftParams{CWD: cwd, DraftKey: draftKey})
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_intent_drafts")
	}
	mapped := fromSQLCPromptIntentDraft(row)
	return &mapped, nil
}

// ListIntentDrafts 列出intentdrafts。
func (s *store) ListIntentDrafts(ctx context.Context, filter PromptIntentDraftListFilter) ([]PromptIntentDraft, error) {
	cwd := strings.TrimSpace(filter.CWD)
	if cwd == "" {
		return nil, wrapPromptError(errors.New("prompt intent cwd is required"), "list", "prompt_intent_drafts")
	}
	if filter.Limit <= 0 {
		return nil, wrapPromptError(errors.New("prompt intent draft list limit is required"), "list", "prompt_intent_drafts")
	}
	status := strings.TrimSpace(filter.Status)
	if status != "" {
		if err := validatePromptIntentDraftStatus(status); err != nil {
			return nil, wrapPromptError(err, "list", "prompt_intent_drafts")
		}
	}
	q, ok := s.q.(listIntentDraftsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_intent_drafts"), "list", "prompt_intent_drafts")
	}
	rows, err := q.ListPromptIntentDrafts(ctx, sqlc.ListPromptIntentDraftsParams{
		CWD:        cwd,
		Status:     status,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapPromptError(err, "list", "prompt_intent_drafts")
	}
	drafts := make([]PromptIntentDraft, 0, len(rows))
	for _, row := range rows {
		drafts = append(drafts, fromSQLCPromptIntentDraft(row))
	}
	return drafts, nil
}

// UpdateIntentDraftStatus 更新intentdraft状态。
func (s *store) UpdateIntentDraftStatus(ctx context.Context, cwd, draftKey, status string) (*PromptIntentDraft, error) {
	cwd, draftKey, err := requireIntentDraftScope(cwd, draftKey)
	if err != nil {
		return nil, wrapPromptError(err, "update_status", "prompt_intent_drafts")
	}
	status = strings.TrimSpace(status)
	if err := validatePromptIntentDraftStatus(status); err != nil {
		return nil, wrapPromptError(err, "update_status", "prompt_intent_drafts")
	}
	q, ok := s.q.(updateIntentDraftStatusQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support update_intent_draft_status"), "update_status", "prompt_intent_drafts")
	}
	row, err := q.UpdatePromptIntentDraftStatus(ctx, sqlc.UpdatePromptIntentDraftStatusParams{
		CWD:      cwd,
		DraftKey: draftKey,
		Status:   status,
	})
	if err != nil {
		return nil, wrapPromptError(err, "update_status", "prompt_intent_drafts")
	}
	mapped := fromSQLCPromptIntentDraft(row)
	return &mapped, nil
}

// validatePromptIntentDraft 校验promptintentdraft。
func validatePromptIntentDraft(d PromptIntentDraft) error {
	if strings.TrimSpace(d.DraftKey) == "" {
		return errors.New("prompt intent draft_key is required")
	}
	if strings.TrimSpace(d.CWD) == "" {
		return errors.New("prompt intent cwd is required")
	}
	switch strings.TrimSpace(d.Kind) {
	case "expert", "recall", "default_rule":
	default:
		return errors.New("prompt intent kind must be expert, recall, or default_rule")
	}
	if err := validatePromptIntentDraftStatus(d.Status); err != nil {
		return err
	}
	if err := validatePromptIntentDraftScope(d.Scope); err != nil {
		return err
	}
	if strings.TrimSpace(d.RawInput) == "" {
		return errors.New("prompt intent raw_input is required")
	}
	return nil
}

func validatePromptIntentDraftStatus(status string) error {
	switch strings.TrimSpace(status) {
	case "draft", "ready_to_save", "enabled", "rejected":
		return nil
	default:
		return errors.New("prompt intent status must be draft, ready_to_save, enabled, or rejected")
	}
}

func validatePromptIntentDraftScope(scope string) error {
	switch strings.TrimSpace(scope) {
	case "", "project", "global":
		return nil
	default:
		return errors.New("prompt intent scope must be project or global")
	}
}

func normalizePromptIntentDraftScope(scope string) string {
	if strings.TrimSpace(scope) == "global" {
		return "global"
	}
	return "project"
}

func requireIntentDraftScope(cwd, draftKey string) (string, string, error) {
	cwd = strings.TrimSpace(cwd)
	draftKey = strings.TrimSpace(draftKey)
	if cwd == "" {
		return "", "", errors.New("prompt intent cwd is required")
	}
	if draftKey == "" {
		return "", "", errors.New("prompt intent draft_key is required")
	}
	return cwd, draftKey, nil
}

func normalizePromptIntentJSON(raw json.RawMessage, defaultValue string) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		trimmed = defaultValue
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, errors.New("prompt intent json field must be valid JSON")
	}
	return []byte(trimmed), nil
}

func fromSQLCPromptIntentDraft(row any) PromptIntentDraft {
	switch r := row.(type) {
	case sqlc.UpsertPromptIntentDraftRow:
		return promptIntentDraftFromFields(r.ID, r.DraftKey, r.CWD, r.Kind, r.RawInput, r.SourceType, r.SourceUrl,
			r.OriginHash, r.LicenseHint, r.GeneratedCard, r.Confidence, r.Status, r.Scope, r.Issues, r.CreatedAt, r.UpdatedAt)
	case sqlc.GetPromptIntentDraftRow:
		return promptIntentDraftFromFields(r.ID, r.DraftKey, r.CWD, r.Kind, r.RawInput, r.SourceType, r.SourceUrl,
			r.OriginHash, r.LicenseHint, r.GeneratedCard, r.Confidence, r.Status, r.Scope, r.Issues, r.CreatedAt, r.UpdatedAt)
	case sqlc.ListPromptIntentDraftsRow:
		return promptIntentDraftFromFields(r.ID, r.DraftKey, r.CWD, r.Kind, r.RawInput, r.SourceType, r.SourceUrl,
			r.OriginHash, r.LicenseHint, r.GeneratedCard, r.Confidence, r.Status, r.Scope, r.Issues, r.CreatedAt, r.UpdatedAt)
	case sqlc.UpdatePromptIntentDraftStatusRow:
		return promptIntentDraftFromFields(r.ID, r.DraftKey, r.CWD, r.Kind, r.RawInput, r.SourceType, r.SourceUrl,
			r.OriginHash, r.LicenseHint, r.GeneratedCard, r.Confidence, r.Status, r.Scope, r.Issues, r.CreatedAt, r.UpdatedAt)
	default:
		panic("unsupported prompt intent draft row type")
	}
}

func promptIntentDraftFromFields(
	id int64,
	draftKey, cwd, kind, rawInput, sourceType, sourceURL, originHash, licenseHint string,
	generatedCard []byte,
	confidence float64,
	status, scope string,
	issues []byte,
	createdAt, updatedAt int64,
) PromptIntentDraft {
	return PromptIntentDraft{
		ID:            id,
		DraftKey:      draftKey,
		CWD:           cwd,
		Kind:          kind,
		RawInput:      rawInput,
		SourceType:    sourceType,
		SourceURL:     sourceURL,
		OriginHash:    originHash,
		LicenseHint:   licenseHint,
		GeneratedCard: json.RawMessage(generatedCard),
		Confidence:    confidence,
		Status:        status,
		Scope:         normalizePromptIntentDraftScope(scope),
		Issues:        json.RawMessage(issues),
		CreatedAt:     platformdb.TimeFromMillis(createdAt),
		UpdatedAt:     platformdb.TimeFromMillis(updatedAt),
	}
}
