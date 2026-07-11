package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type service struct {
	logger *slog.Logger
	store  Store
	now    func() time.Time
	newID  func() string
}

var _ Service = (*service)(nil)

// NewService 创建 cron 模块服务并注入持久化依赖。
// now/newID 在测试中可替换；生产路径不在 service 内补默认 store。
func NewService(logger *slog.Logger, store Store) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{
		logger: logger,
		store:  store,
		now:    time.Now,
		newID:  func() string { return uuid.NewString() },
	}
}

// defaultInitialDelay 是创建任务未显式传入 NextRunAt 时的首次触发延迟。
// 后续周期仍由 schedule_expr 计算；这个值只影响新 job 入库后的第一次扫描窗口。
const defaultInitialDelay = time.Minute

// CreateJob 创建定时任务并计算首次运行时间。
// 配置和技能列表会先规范化再入库，避免调度阶段才暴露坏 JSON。
func (s *service) CreateJob(ctx context.Context, req CreateJobRequest) (Job, error) {
	if err := s.validateCreate(&req); err != nil {
		return Job{}, err
	}
	configBytes, err := normalizeConfig(req.Config)
	if err != nil {
		return Job{}, err
	}
	skillsBytes, err := marshalSkills(req.Skills)
	if err != nil {
		return Job{}, err
	}
	now := s.now().UTC()
	nextRunAt := req.NextRunAt
	if nextRunAt.IsZero() {
		nextRunAt = now.Add(defaultInitialDelay)
	}
	scheduleType := strings.TrimSpace(req.ScheduleType)
	if scheduleType == "" {
		scheduleType = "cron"
	}
	row, err := s.store.CreateJob(ctx, CreateJobParams{
		ID:            s.newID(),
		Name:          strings.TrimSpace(req.Name),
		Prompt:        req.Prompt,
		ScheduleType:  scheduleType,
		ScheduleExpr:  strings.TrimSpace(req.ScheduleExpr),
		Timezone:      strings.TrimSpace(req.Timezone),
		Provider:      strings.TrimSpace(req.Provider),
		Model:         strings.TrimSpace(req.Model),
		CWD:           strings.TrimSpace(req.CWD),
		Config:        configBytes,
		Skills:        skillsBytes,
		NotifyChannel: strings.TrimSpace(req.NotifyChannel),
		Enabled:       req.Enabled,
		NextRunAt:     nextRunAt,
		MaxAttempts:   req.MaxAttempts,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		s.logger.Warn("cron: create job failed", slog.String("error", err.Error()))
		return Job{}, err
	}
	return toJob(row)
}

// GetJob 按 ID 读取定时任务详情，并把存储层 not found 映射为领域错误。
func (s *service) GetJob(ctx context.Context, id string) (Job, error) {
	row, err := s.store.GetJobByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrStoreJobNotFound) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	return toJob(row)
}

// ListJobs 列出全部定时任务并转换为对外 DTO。
func (s *service) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		job, err := toJob(r)
		if err != nil {
			return nil, err
		}
		out[i] = job
	}
	return out, nil
}

// UpdateJob 更新定时任务配置并重算调度时间。
func (s *service) UpdateJob(ctx context.Context, req UpdateJobRequest) (Job, error) {
	if strings.TrimSpace(req.ID) == "" {
		return Job{}, errors.New("cron: id is required")
	}
	creq := CreateJobRequest{
		Name:          req.Name,
		Prompt:        req.Prompt,
		ScheduleType:  req.ScheduleType,
		ScheduleExpr:  req.ScheduleExpr,
		Timezone:      req.Timezone,
		Provider:      req.Provider,
		Model:         req.Model,
		CWD:           req.CWD,
		Config:        req.Config,
		Skills:        req.Skills,
		NotifyChannel: req.NotifyChannel,
		Enabled:       req.Enabled,
		NextRunAt:     req.NextRunAt,
		MaxAttempts:   req.MaxAttempts,
	}
	if err := s.validateCreate(&creq); err != nil {
		return Job{}, err
	}
	configBytes, err := normalizeConfig(creq.Config)
	if err != nil {
		return Job{}, err
	}
	skillsBytes, err := marshalSkills(creq.Skills)
	if err != nil {
		return Job{}, err
	}
	now := s.now().UTC()
	nextRunAt := creq.NextRunAt
	if nextRunAt.IsZero() {
		nextRunAt = now.Add(defaultInitialDelay)
	}
	scheduleType := strings.TrimSpace(creq.ScheduleType)
	if scheduleType == "" {
		scheduleType = "cron"
	}
	if err := s.store.UpdateJobSchedule(ctx, UpdateJobScheduleParams{
		ID:            req.ID,
		Name:          strings.TrimSpace(creq.Name),
		Prompt:        creq.Prompt,
		ScheduleType:  scheduleType,
		ScheduleExpr:  strings.TrimSpace(creq.ScheduleExpr),
		Timezone:      strings.TrimSpace(creq.Timezone),
		Provider:      strings.TrimSpace(creq.Provider),
		Model:         strings.TrimSpace(creq.Model),
		CWD:           strings.TrimSpace(creq.CWD),
		Config:        configBytes,
		Skills:        skillsBytes,
		NotifyChannel: strings.TrimSpace(creq.NotifyChannel),
		Enabled:       creq.Enabled,
		NextRunAt:     nextRunAt,
		MaxAttempts:   creq.MaxAttempts,
		UpdatedAt:     now,
	}); err != nil {
		return Job{}, err
	}
	return s.GetJob(ctx, req.ID)
}

// SetJobEnabled 启用或停用定时任务。
func (s *service) SetJobEnabled(ctx context.Context, id string, enabled bool) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("cron: id is required")
	}
	err := s.store.SetJobEnabled(ctx, id, enabled, s.now().UTC())
	if errors.Is(err, ErrStoreJobNotFound) {
		return ErrNotFound
	}
	return err
}

// DeleteJob 删除定时任务，空 ID 和不存在 ID 都返回显式错误。
func (s *service) DeleteJob(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("cron: id is required")
	}
	err := s.store.DeleteJob(ctx, id)
	if errors.Is(err, ErrStoreJobNotFound) {
		return ErrNotFound
	}
	return err
}

// ListJobRuns 按 jobID 列出运行记录，limit 语义由 store 层统一处理。
func (s *service) ListJobRuns(ctx context.Context, jobID string, limit int32) ([]Run, error) {
	rows, err := s.store.ListRunsByJob(ctx, jobID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = toRun(r)
	}
	return out, nil
}

// RunOnce 只把 next_run_at 提到现在，不直接建 run 或调用 StartTurn。
// 这样手动触发仍走同一套 claim/CAS/dedupe 流程；若 job 仍有 next_retry_at，会继续尊重重试延迟。
func (s *service) RunOnce(ctx context.Context, jobID string) (Job, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Job{}, errors.New("cron: id is required")
	}
	row, err := s.store.GetJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, ErrStoreJobNotFound) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	now := s.now().UTC()
	if !row.Enabled {
		return Job{}, ErrJobDisabled
	}
	if err := s.store.PatchNextRunAt(ctx, row.ID, now, now); err != nil {
		return Job{}, err
	}
	return s.GetJob(ctx, row.ID)
}

// 下列 helper 在写入前统一清洗创建/更新请求，避免坏配置流入调度运行时。

// validateCreate 校验创建或更新定时任务的输入。
// provider 目前只允许 codex；Codex 身份配置必须能解析，避免后续调度跑到错误实例。
func (s *service) validateCreate(req *CreateJobRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return ErrMissingName
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return ErrMissingPrompt
	}
	if err := validateScheduleInput(req.ScheduleExpr, req.Timezone); err != nil {
		return err
	}
	if strings.TrimSpace(req.CWD) == "" {
		return ErrMissingCWD
	}
	if req.MaxAttempts < 0 {
		return ErrInvalidMaxAttempts
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = providerCodex
		req.Provider = provider
	}
	if provider != providerCodex {
		return fmt.Errorf("%w: got %q", ErrProviderNotSupported, req.Provider)
	}
	// 这里先挡住错误的 Codex 身份配置，避免定时任务后来跑到错误的 home/instance。
	configMap, err := decodeConfigMap(req.Config)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if _, err := contract.ResolveCodexIdentity(configMap); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return nil
}

// validateScheduleInput 同时校验必填表达式和可选时区。
// Create/Update 共享这个入口，避免调度阶段才发现坏 schedule。
func validateScheduleInput(scheduleExpr, timezone string) error {
	if strings.TrimSpace(scheduleExpr) == "" {
		return ErrMissingSchedule
	}
	_, err := ParseSchedule(scheduleExpr, timezone)
	return err
}

// decodeConfigMap 将原始 JSON 配置解析为 map[string]any。
// nil/空输入按空配置处理；语法错误必须返回给调用方，不能写入坏配置。
func decodeConfigMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// normalizeConfig 将原始 JSON 配置重新序列化为规范 JSON，拒绝语法错误的输入。
func normalizeConfig(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	// JSON 不合法就直接返回错误，不把坏配置写进库再等调度阶段失败。
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

// marshalSkills 去重、清洗技能列表并序列化为 JSON 数组。
func marshalSkills(skills []string) ([]byte, error) {
	if len(skills) == 0 {
		return []byte("[]"), nil
	}
	cleaned := make([]string, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		cleaned = append(cleaned, s)
	}
	return json.Marshal(cleaned)
}

const cronJobPayloadInvalidCode = "cron_job_payload_invalid"

type cronJobPayloadInvalidError struct {
	JobID string
	Field string
	Err   error
}

// Error 返回包含稳定错误码、job id 和损坏字段的可读错误。
func (e *cronJobPayloadInvalidError) Error() string {
	return fmt.Sprintf("%s: job %q field %s contains invalid JSON: %v", cronJobPayloadInvalidCode, e.JobID, e.Field, e.Err)
}

// Unwrap 暴露底层 JSON 解码错误，便于调用方保留原始原因。
func (e *cronJobPayloadInvalidError) Unwrap() error {
	return e.Err
}

// ErrorCode 返回 cron job payload 损坏的稳定错误码。
func (e *cronJobPayloadInvalidError) ErrorCode() string {
	return cronJobPayloadInvalidCode
}

// 下列 mapper 将 sqlc 行转换为 RPC/服务层 DTO；历史坏 JSON 必须阻断读取，避免调度继续使用损坏配置。

// toJob 将存储层 job 行转换成 cron 对外 DTO。
// config/skills JSON 损坏时返回 typed error，调用方不能把坏行当正常任务继续使用。
func toJob(row JobRecord) (Job, error) {
	var skills []string
	if len(row.Skills) > 0 {
		if err := json.Unmarshal(row.Skills, &skills); err != nil {
			return Job{}, &cronJobPayloadInvalidError{JobID: row.ID, Field: "skills", Err: err}
		}
	}
	var config any
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &config); err != nil {
			return Job{}, &cronJobPayloadInvalidError{JobID: row.ID, Field: "config", Err: err}
		}
	}
	return Job{
		ID:              row.ID,
		Name:            row.Name,
		Prompt:          row.Prompt,
		ScheduleType:    row.ScheduleType,
		ScheduleExpr:    row.ScheduleExpr,
		Timezone:        row.Timezone,
		Provider:        row.Provider,
		Model:           row.Model,
		CWD:             row.CWD,
		Config:          config,
		Skills:          skills,
		NotifyChannel:   row.NotifyChannel,
		Enabled:         row.Enabled,
		NextRunAt:       formatTime(row.NextRunAt),
		LastScheduledAt: formatTime(row.LastScheduledAt),
		LastRunAt:       formatTime(row.LastRunAt),
		ThreadID:        row.ThreadID,
		AgentID:         row.AgentID,
		ActiveTurnID:    row.ActiveTurnID,
		LastTurnID:      row.LastTurnID,
		FailureCount:    row.FailureCount,
		MaxAttempts:     row.MaxAttempts,
		LastStatus:      row.LastStatus,
		LastError:       row.LastError,
		LastErrorAt:     formatTime(row.LastErrorAt),
		CreatedAt:       formatTime(row.CreatedAt),
		UpdatedAt:       formatTime(row.UpdatedAt),
	}, nil
}

// toRun 将存储层 run 行转换成 cron 对外 DTO，时间统一输出 RFC3339 UTC 字符串。
func toRun(row RunRecord) Run {
	return Run{
		ID:             row.ID,
		JobID:          row.JobID,
		ScheduledAt:    formatTime(row.ScheduledAt),
		IdempotencyKey: row.IdempotencyKey,
		DedupeKey:      row.DedupeKey,
		ThreadID:       row.ThreadID,
		AgentID:        row.AgentID,
		TurnID:         row.TurnID,
		SubmittedAt:    formatTime(row.SubmittedAt),
		Status:         row.Status,
		Error:          row.Error,
		CreatedAt:      formatTime(row.CreatedAt),
		UpdatedAt:      formatTime(row.UpdatedAt),
	}
}

// formatTime 将 time.Time 格式化为 RFC3339 UTC 字符串，零值返回空串。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
