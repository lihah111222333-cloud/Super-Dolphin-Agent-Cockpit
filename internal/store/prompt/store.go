package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

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

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return newStore(q, nil)
}

func newStore(q *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: q, runInTx: runInTx}
}

// Get 读取prompt存储。
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

// List 列出prompt存储。
func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	cwd := strings.TrimSpace(filter.CWD)
	if cwd == "" {
		return nil, errors.New("cwd is required for prompt template list")
	}
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		AgentKey:   filter.AgentKey,
		Keyword:    filter.Keyword,
		CWD:        cwd,
		LimitCount: filter.Limit,
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

// WithTx 设置tx。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	if s.runInTx == nil || s.queries == nil {
		return wrapPromptError(fn(s), "with_tx", "prompt_template")
	}
	err := s.runInTx(ctx, func(txQueries *sqlc.Queries) error {
		return fn(&store{q: txQueries, queries: txQueries, runInTx: s.runInTx})
	})
	return wrapPromptError(err, "with_tx", "prompt_template")
}

// Delete 删除prompt存储。
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

// ListSectionsByTemplateID 按templateID列出sections。
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

// ListSectionsByTemplateIDs 按templateids列出sections。
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

// ListRecallSections 列出recallsections。
func (s *store) ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return nil, wrapPromptError(err, "list_recall_sections", "prompt_template_sections")
	}
	q, ok := s.q.(listRecallSectionsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_recall_sections"), "list_recall_sections", "prompt_template_sections")
	}
	rows, err := q.ListRecallSections(ctx, sqlc.ListRecallSectionsParams{CWD: cwd})
	if err != nil {
		return nil, wrapPromptError(err, "list_recall_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListRecallSectionRow(row))
	}
	return sections, nil
}

// ListDefaultRuleSections 列出defaultrulesections。
func (s *store) ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return nil, wrapPromptError(err, "list_default_rule_sections", "prompt_template_sections")
	}
	q, ok := s.q.(listDefaultRuleSectionsQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support list_default_rule_sections"), "list_default_rule_sections", "prompt_template_sections")
	}
	rows, err := q.ListDefaultRuleSections(ctx, sqlc.ListDefaultRuleSectionsParams{CWD: cwd})
	if err != nil {
		return nil, wrapPromptError(err, "list_default_rule_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromListDefaultRuleSectionRow(row))
	}
	return sections, nil
}

// InsertVersion 插入版本。
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
		Column6:         version.Variables,
		Column7:         version.Tags,
		Description:     version.Description,
		Enabled:         version.Enabled,
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: version.SourceUpdatedAt,
	})
	if err != nil {
		return 0, wrapPromptError(err, "insert_version", "prompt_template_version")
	}
	return id, nil
}

// CreatePromptTemplate 创建prompttemplate。
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
		Column6:        template.Variables,
		Column7:        template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        template.Enabled,
		ManuallyEdited: template.ManuallyEdited,
		Column12:       []byte(template.MatchWhen),
		Priority:       int32(template.Priority),
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

// Upsert 新增或更新记录。
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
		Column6:        template.Variables,
		Column7:        template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        template.Enabled,
		ManuallyEdited: template.ManuallyEdited,
		Column12:       []byte(template.MatchWhen),
		Priority:       int32(template.Priority),
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
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int(row.Priority),
	}
}

func fromGetTemplate(row sqlc.GetPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int(row.Priority),
	}
}

func fromListTemplate(row sqlc.ListPromptTemplatesRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int(row.Priority),
	}
}

func fromUpsertTemplate(row sqlc.UpsertPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int(row.Priority),
	}
}

// UpsertSection 处理upsertsection。
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
	row, err := q.UpsertPromptTemplateSection(ctx, sqlc.UpsertPromptTemplateSectionParams{
		TemplateID:  section.TemplateID,
		SectionKey:  sectionKey,
		Region:      region,
		Ordinal:     int32(section.Ordinal),
		Body:        section.Body,
		EnableWhen:  []byte(section.EnableWhen),
		Enabled:     section.Enabled,
		TriggerType: triggerType,
		RecallTopic: normalizePromptSectionRecallTopic(triggerType, section.RecallTopic),
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert_section", "prompt_template_sections")
	}
	mapped := fromListSectionRow(row)
	return &mapped, nil
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

// LockRecallTopicInCWD 在工作目录处理锁recalltopic。
func (s *store) LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error {
	cwd, err := requirePromptSectionCWD(cwd)
	if err != nil {
		return wrapPromptError(err, "lock_recall_topic", "prompt_template_sections")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return wrapPromptError(errors.New("recall topic is required"), "lock_recall_topic", "prompt_template_sections")
	}
	q, ok := s.q.(lockRecallTopicQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support lock_recall_topic"), "lock_recall_topic", "prompt_template_sections")
	}
	return wrapPromptError(q.LockRecallTopicInCWD(ctx, sqlc.LockRecallTopicInCWDParams{CWD: cwd, Topic: topic}), "lock_recall_topic", "prompt_template_sections")
}

func validRecallTopicName(topic string) bool {
	return len(topic) < 64 && recallTopicNamePattern.MatchString(topic)
}

// DeleteSection 删除section。
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
		Enabled:     row.Enabled,
		TriggerType: row.TriggerType,
		RecallTopic: row.RecallTopic,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
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
		Enabled:             row.Enabled,
		TriggerType:         row.TriggerType,
		RecallTopic:         row.RecallTopic,
		TemplatePromptKey:   row.TemplatePromptKey,
		TemplateTitle:       row.TemplateTitle,
		TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse:   row.TemplateWhenToUse,
		TemplateTags:        json.RawMessage(row.TemplateTags),
		CreatedAt:           row.CreatedAt.Time,
		UpdatedAt:           row.UpdatedAt.Time,
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
		Enabled:           row.Enabled,
		TriggerType:       row.TriggerType,
		RecallTopic:       row.RecallTopic,
		TemplatePromptKey: row.TemplatePromptKey,
		TemplateTitle:     row.TemplateTitle,
		TemplateTags:      json.RawMessage(row.TemplateTags),
		CreatedAt:         row.CreatedAt.Time,
		UpdatedAt:         row.UpdatedAt.Time,
	}
}

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
