package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
}

type getQuerier interface {
	GetPromptTemplate(ctx context.Context, arg sqlc.GetPromptTemplateParams) (sqlc.GetPromptTemplateRow, error)
}

type deleteQuerier interface {
	DeletePromptTemplate(ctx context.Context, arg sqlc.DeletePromptTemplateParams) (int64, error)
}

type insertVersionQuerier interface {
	InsertPromptVersion(ctx context.Context, arg sqlc.InsertPromptVersionParams) (int64, error)
}

type upsertQuerier interface {
	UpsertPromptTemplate(ctx context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error)
}

type createPromptTemplateQuerier interface {
	CreatePromptTemplate(ctx context.Context, arg sqlc.CreatePromptTemplateParams) (sqlc.CreatePromptTemplateRow, error)
}

type listSectionsQuerier interface {
	ListPromptTemplateSectionsByTemplate(ctx context.Context, arg sqlc.ListPromptTemplateSectionsByTemplateParams) ([]sqlc.PromptTemplateSection, error)
}

type listSectionsByTemplatesQuerier interface {
	ListPromptTemplateSectionsByTemplates(ctx context.Context, arg sqlc.ListPromptTemplateSectionsByTemplatesParams) ([]sqlc.PromptTemplateSection, error)
}

type listRecallSectionsQuerier interface {
	ListRecallSections(ctx context.Context, arg sqlc.ListRecallSectionsParams) ([]sqlc.ListRecallSectionsRow, error)
}

type listDefaultRuleSectionsQuerier interface {
	ListDefaultRuleSections(ctx context.Context, arg sqlc.ListDefaultRuleSectionsParams) ([]sqlc.ListDefaultRuleSectionsRow, error)
}

type lockRecallTopicQuerier interface {
	LockRecallTopicInCWD(ctx context.Context, arg sqlc.LockRecallTopicInCWDParams) error
}

type upsertRecallTopicTargetQuerier interface {
	UpsertPromptRecallTopicTargetInCWD(ctx context.Context, arg sqlc.UpsertPromptRecallTopicTargetInCWDParams) error
}

type upsertSectionQuerier interface {
	UpsertPromptTemplateSection(ctx context.Context, arg sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error)
}

type deleteSectionQuerier interface {
	DeletePromptTemplateSection(ctx context.Context, arg sqlc.DeletePromptTemplateSectionParams) (int64, error)
}

type txRunner func(context.Context, func(*sqlc.Queries) error) error

type store struct {
	q       querier
	queries *sqlc.Queries
	runInTx txRunner
}

var recallTopicNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func NewStore(q *sqlc.Queries) Store {
	return newStore(q, nil)
}

func newStore(q *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: q, runInTx: runInTx}
}

func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	q, ok := s.q.(getQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support get"), "get", "prompt_template")
	}
	row, err := q.GetPromptTemplate(ctx, sqlc.GetPromptTemplateParams{PromptKey: promptKey})
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_template")
	}
	mapped := fromGetTemplate(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	cwd := strings.TrimSpace(filter.CWD)
	if cwd == "" {
		return nil, errors.New("cwd is required for prompt template list")
	}
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		AgentKey:   filter.AgentKey,
		Keyword:    filter.Keyword,
		CWD:        &cwd,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapPromptError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromListTemplate(row))
	}
	return templates, nil
}

func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	if s.runInTx == nil || s.queries == nil {
		return wrapPromptError(fn(s), "with_tx", "prompt_template")
	}
	err := s.runInTx(ctx, func(txQueries *sqlc.Queries) error {
		return fn(&store{q: txQueries, queries: txQueries, runInTx: s.runInTx})
	})
	return wrapPromptError(err, "with_tx", "prompt_template")
}

func (s *store) Delete(ctx context.Context, promptKey string) error {
	q, ok := s.q.(deleteQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support delete"), "delete", "prompt_template")
	}
	rows, err := q.DeletePromptTemplate(ctx, sqlc.DeletePromptTemplateParams{PromptKey: promptKey})
	if err != nil {
		return wrapPromptError(err, "delete", "prompt_template")
	}
	if rows == 0 {
		return wrapPromptError(platformdb.ErrNotFound, "delete", "prompt_template")
	}
	return nil
}

func (s *store) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	q, ok := s.q.(listSectionsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_sections"), "list_sections", "prompt_template_sections")
	}
	rows, err := q.ListPromptTemplateSectionsByTemplate(ctx, sqlc.ListPromptTemplateSectionsByTemplateParams{
		TemplateID: templateID,
	})
	if err != nil {
		return nil, wrapPromptError(err, "list_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListSectionRow(row))
	}
	return sections, nil
}

func (s *store) ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error) {
	if len(templateIDs) == 0 {
		return []PromptTemplateSection{}, nil
	}
	q, ok := s.q.(listSectionsByTemplatesQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_sections"), "list_sections", "prompt_template_sections")
	}
	rows, err := q.ListPromptTemplateSectionsByTemplates(ctx, sqlc.ListPromptTemplateSectionsByTemplatesParams{
		TemplateIds: templateIDs,
	})
	if err != nil {
		return nil, wrapPromptError(err, "list_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListSectionRow(row))
	}
	return sections, nil
}

func (s *store) ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return nil, wrapPromptError(err, "list_recall_sections", "prompt_template_sections")
	}
	q, ok := s.q.(listRecallSectionsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_recall_sections"), "list_recall_sections", "prompt_template_sections")
	}
	rows, err := q.ListRecallSections(ctx, sqlc.ListRecallSectionsParams{CWD: &cwd})
	if err != nil {
		return nil, wrapPromptError(err, "list_recall_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListRecallSectionRow(row))
	}
	return sections, nil
}

func (s *store) ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return nil, wrapPromptError(err, "list_default_rule_sections", "prompt_template_sections")
	}
	q, ok := s.q.(listDefaultRuleSectionsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_default_rule_sections"), "list_default_rule_sections", "prompt_template_sections")
	}
	rows, err := q.ListDefaultRuleSections(ctx, sqlc.ListDefaultRuleSectionsParams{CWD: &cwd})
	if err != nil {
		return nil, wrapPromptError(err, "list_default_rule_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListDefaultRuleSectionRow(row))
	}
	return sections, nil
}

func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	q, ok := s.q.(insertVersionQuerier)
	if !ok {
		return 0, wrapPromptError(errors.New("prompt store does not support insert_version"), "insert_version", "prompt_template_version")
	}
	id, err := q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
		PromptKey:       version.PromptKey,
		Title:           version.Title,
		AgentKey:        version.AgentKey,
		ToolName:        version.ToolName,
		PromptText:      version.PromptText,
		Variables:       version.Variables,
		Tags:            version.Tags,
		Description:     version.Description,
		Enabled:         boolToInt64(version.Enabled),
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: timePtrToInt64Ptr(version.SourceUpdatedAt),
	})
	if err != nil {
		return 0, wrapPromptError(err, "insert_version", "prompt_template_version")
	}
	return id, nil
}

func (s *store) CreatePromptTemplate(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	q, ok := s.q.(createPromptTemplateQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support create"), "create", "prompt_template")
	}
	row, err := q.CreatePromptTemplate(ctx, sqlc.CreatePromptTemplateParams{
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Variables:      template.Variables,
		Tags:           template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        boolToInt64(template.Enabled),
		ManuallyEdited: boolToInt64(template.ManuallyEdited),
		MatchWhen:      normalizePromptMatchWhen(template.MatchWhen),
		Priority:       int64(template.Priority),
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
	})
	if platformdb.IsNotFound(err) {
		err = platformdb.ErrConflict
	}
	if err != nil {
		return nil, wrapPromptError(err, "create", "prompt_template")
	}
	mapped := fromCreateTemplate(row)
	return &mapped, nil
}

func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	q, ok := s.q.(upsertQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support upsert"), "upsert", "prompt_template")
	}
	row, err := q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Variables:      template.Variables,
		Tags:           template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        boolToInt64(template.Enabled),
		ManuallyEdited: boolToInt64(template.ManuallyEdited),
		MatchWhen:      normalizePromptMatchWhen(template.MatchWhen),
		Priority:       int64(template.Priority),
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_template")
	}
	mapped := fromUpsertTemplate(row)
	return &mapped, nil
}

func fromCreateTemplate(row sqlc.CreatePromptTemplateRow) PromptTemplate {
	return promptTemplateFromFields(
		row.ID, row.PromptKey, row.Title, row.AgentKey, row.ToolName, row.PromptText,
		row.WhenToUse, row.Variables, row.Tags, row.Enabled, row.ManuallyEdited,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt, row.Description,
		row.MatchWhen, row.Priority,
	)
}

func fromGetTemplate(row sqlc.GetPromptTemplateRow) PromptTemplate {
	return promptTemplateFromFields(
		row.ID, row.PromptKey, row.Title, row.AgentKey, row.ToolName, row.PromptText,
		row.WhenToUse, row.Variables, row.Tags, row.Enabled, row.ManuallyEdited,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt, row.Description,
		row.MatchWhen, row.Priority,
	)
}

func fromListTemplate(row sqlc.ListPromptTemplatesRow) PromptTemplate {
	return promptTemplateFromFields(
		row.ID, row.PromptKey, row.Title, row.AgentKey, row.ToolName, row.PromptText,
		row.WhenToUse, row.Variables, row.Tags, row.Enabled, row.ManuallyEdited,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt, row.Description,
		row.MatchWhen, row.Priority,
	)
}

func fromUpsertTemplate(row sqlc.UpsertPromptTemplateRow) PromptTemplate {
	return promptTemplateFromFields(
		row.ID, row.PromptKey, row.Title, row.AgentKey, row.ToolName, row.PromptText,
		row.WhenToUse, row.Variables, row.Tags, row.Enabled, row.ManuallyEdited,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt, row.Description,
		row.MatchWhen, row.Priority,
	)
}

func promptTemplateFromFields(
	id int64,
	promptKey, title, agentKey, toolName, promptText, whenToUse string,
	variables, tags []byte,
	enabled, manuallyEdited int64,
	createdBy, updatedBy string,
	createdAt, updatedAt int64,
	description string,
	matchWhen []byte,
	priority int64,
) PromptTemplate {
	return PromptTemplate{
		ID:             id,
		PromptKey:      promptKey,
		Title:          title,
		AgentKey:       agentKey,
		ToolName:       toolName,
		PromptText:     promptText,
		WhenToUse:      whenToUse,
		Variables:      json.RawMessage(variables),
		Tags:           json.RawMessage(tags),
		Enabled:        enabled != 0,
		ManuallyEdited: manuallyEdited != 0,
		CreatedBy:      createdBy,
		UpdatedBy:      updatedBy,
		CreatedAt:      platformdb.TimeFromMillis(createdAt),
		UpdatedAt:      platformdb.TimeFromMillis(updatedAt),
		Description:    description,
		MatchWhen:      json.RawMessage(matchWhen),
		Priority:       int(priority),
	}
}

func (s *store) UpsertSection(ctx context.Context, section PromptTemplateSection) (*PromptTemplateSection, error) {
	q, ok := s.q.(upsertSectionQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support upsert_section"), "upsert_section", "prompt_template_sections")
	}
	region := strings.TrimSpace(strings.ToLower(section.Region))
	if region != "static" && region != "dynamic" {
		return nil, wrapPromptError(errors.New("prompt section region must be 'static' or 'dynamic'"), "upsert_section", "prompt_template_sections")
	}
	sectionKey := strings.TrimSpace(section.SectionKey)
	if sectionKey == "" {
		return nil, wrapPromptError(errors.New("prompt section section_key is required"), "upsert_section", "prompt_template_sections")
	}
	if section.TemplateID <= 0 {
		return nil, wrapPromptError(errors.New("prompt section template_id is required"), "upsert_section", "prompt_template_sections")
	}
	triggerType, err := normalizePromptSectionTriggerType(section.TriggerType)
	if err != nil {
		return nil, wrapPromptError(err, "upsert_section", "prompt_template_sections")
	}
	if err := validatePromptSectionRecallTopic(triggerType, section.RecallTopic); err != nil {
		return nil, wrapPromptError(err, "upsert_section", "prompt_template_sections")
	}
	enableWhen, err := normalizePromptSectionEnableWhen(section.EnableWhen)
	if err != nil {
		return nil, wrapPromptError(err, "upsert_section", "prompt_template_sections")
	}
	row, err := q.UpsertPromptTemplateSection(ctx, sqlc.UpsertPromptTemplateSectionParams{
		TemplateID:  section.TemplateID,
		SectionKey:  sectionKey,
		Region:      region,
		Ordinal:     int64(section.Ordinal),
		Body:        section.Body,
		EnableWhen:  enableWhen,
		Enabled:     boolToInt64(section.Enabled),
		TriggerType: triggerType,
		RecallTopic: normalizePromptSectionRecallTopic(triggerType, section.RecallTopic),
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert_section", "prompt_template_sections")
	}
	mapped := fromListSectionRow(row)
	return &mapped, nil
}

func normalizePromptSectionEnableWhen(enableWhen json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(enableWhen))
	if trimmed == "" {
		return "{}", nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", errors.New("prompt section enable_when must be valid JSON")
	}
	return trimmed, nil
}

func normalizePromptSectionRecallTopic(triggerType, value string) string {
	if triggerType != "recall" {
		return ""
	}
	return strings.TrimSpace(value)
}

func validatePromptSectionRecallTopic(triggerType, value string) error {
	if triggerType != "recall" {
		return nil
	}
	topic := strings.TrimSpace(value)
	if !validRecallTopicName(topic) {
		return errors.New("prompt section recall_topic must be lowercase dash-separated and shorter than 64 characters")
	}
	return nil
}

func normalizePromptSectionTriggerType(value string) (string, error) {
	triggerType := strings.TrimSpace(strings.ToLower(value))
	if triggerType == "" {
		return "always", nil
	}
	if triggerType != "always" && triggerType != "keyword" && triggerType != "recall" {
		return "", errors.New("prompt section trigger_type must be 'always', 'keyword', or 'recall'")
	}
	return triggerType, nil
}

func requirePromptSectionCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("prompt dynamic section cwd is required")
	}
	return cwd, nil
}

func (s *store) LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return wrapPromptError(err, "lock_recall_topic", "prompt_template_sections")
	}
	topic = strings.TrimSpace(topic)
	if !validRecallTopicName(topic) {
		return wrapPromptError(errors.New("recall topic must be lowercase dash-separated and shorter than 64 characters"), "lock_recall_topic", "prompt_template_sections")
	}
	q, ok := s.q.(lockRecallTopicQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support lock_recall_topic"), "lock_recall_topic", "prompt_template_sections")
	}
	return wrapPromptError(q.LockRecallTopicInCWD(ctx, sqlc.LockRecallTopicInCWDParams{
		CWD:   cwd,
		Topic: topic,
	}), "lock_recall_topic", "prompt_template_sections")
}

func (s *store) UpsertRecallTopicTargetInCWD(ctx context.Context, cwd, topic string, templateID int64, sectionKey string) error {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return wrapPromptError(err, "upsert_recall_topic_target", "prompt_recall_topics")
	}
	topic = strings.TrimSpace(topic)
	if !validRecallTopicName(topic) {
		return wrapPromptError(errors.New("recall topic must be lowercase dash-separated and shorter than 64 characters"), "upsert_recall_topic_target", "prompt_recall_topics")
	}
	if templateID <= 0 {
		return wrapPromptError(errors.New("prompt recall topic template_id is required"), "upsert_recall_topic_target", "prompt_recall_topics")
	}
	sectionKey = strings.TrimSpace(sectionKey)
	if sectionKey == "" {
		return wrapPromptError(errors.New("prompt recall topic section_key is required"), "upsert_recall_topic_target", "prompt_recall_topics")
	}
	q, ok := s.q.(upsertRecallTopicTargetQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support upsert_recall_topic_target"), "upsert_recall_topic_target", "prompt_recall_topics")
	}
	return wrapPromptError(q.UpsertPromptRecallTopicTargetInCWD(ctx, sqlc.UpsertPromptRecallTopicTargetInCWDParams{
		CWD:        cwd,
		Topic:      topic,
		TemplateID: templateID,
		SectionKey: sectionKey,
	}), "upsert_recall_topic_target", "prompt_recall_topics")
}

func validRecallTopicName(topic string) bool {
	return len(topic) < 64 && recallTopicNamePattern.MatchString(topic)
}

func (s *store) DeleteSection(ctx context.Context, templateID int64, sectionKey string) error {
	q, ok := s.q.(deleteSectionQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support delete_section"), "delete_section", "prompt_template_sections")
	}
	key := strings.TrimSpace(sectionKey)
	if templateID <= 0 || key == "" {
		return wrapPromptError(errors.New("prompt section template_id and section_key are required"), "delete_section", "prompt_template_sections")
	}
	rows, err := q.DeletePromptTemplateSection(ctx, sqlc.DeletePromptTemplateSectionParams{
		TemplateID: templateID,
		SectionKey: key,
	})
	if err != nil {
		return wrapPromptError(err, "delete_section", "prompt_template_sections")
	}
	if rows == 0 {
		return wrapPromptError(platformdb.ErrNotFound, "delete_section", "prompt_template_sections")
	}
	return nil
}

func fromListSectionRow(row sqlc.PromptTemplateSection) PromptTemplateSection {
	return PromptTemplateSection{
		ID:          row.ID,
		TemplateID:  row.TemplateID,
		SectionKey:  row.SectionKey,
		Region:      row.Region,
		Ordinal:     int(row.Ordinal),
		Body:        row.Body,
		EnableWhen:  json.RawMessage(row.EnableWhen),
		Enabled:     row.Enabled != 0,
		TriggerType: row.TriggerType,
		RecallTopic: row.RecallTopic,
		CreatedAt:   platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:   platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func fromListRecallSectionRow(row sqlc.ListRecallSectionsRow) PromptTemplateSection {
	return PromptTemplateSection{
		ID:                  row.ID,
		TemplateID:          row.TemplateID,
		SectionKey:          row.SectionKey,
		Region:              row.Region,
		Ordinal:             int(row.Ordinal),
		EnableWhen:          json.RawMessage(row.EnableWhen),
		Enabled:             row.Enabled != 0,
		TriggerType:         row.TriggerType,
		RecallTopic:         row.RecallTopic,
		TemplatePromptKey:   row.TemplatePromptKey,
		TemplateTitle:       row.TemplateTitle,
		TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse:   row.TemplateWhenToUse,
		TemplateTags:        json.RawMessage(row.TemplateTags),
		CreatedAt:           platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:           platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func fromListDefaultRuleSectionRow(row sqlc.ListDefaultRuleSectionsRow) PromptTemplateSection {
	return PromptTemplateSection{
		ID:                row.ID,
		TemplateID:        row.TemplateID,
		SectionKey:        row.SectionKey,
		Region:            row.Region,
		Ordinal:           int(row.Ordinal),
		Body:              row.Body,
		EnableWhen:        json.RawMessage(row.EnableWhen),
		Enabled:           row.Enabled != 0,
		TriggerType:       row.TriggerType,
		RecallTopic:       row.RecallTopic,
		TemplatePromptKey: row.TemplatePromptKey,
		TemplateTitle:     row.TemplateTitle,
		TemplateTags:      json.RawMessage(row.TemplateTags),
		CreatedAt:         platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:         platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func normalizePromptMatchWhen(matchWhen json.RawMessage) string {
	if strings.TrimSpace(string(matchWhen)) == "" {
		return "{}"
	}
	return string(matchWhen)
}

func timePtrToInt64Ptr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := platformdb.Millis(*t)
	return &v
}

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
