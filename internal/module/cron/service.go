package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type service struct {
	logger *pkglogger.Logger
	store  Store
	now    func() time.Time
	newID  func() string
}

var _ Service = (*service)(nil)

// NewService constructs a cron Service backed by the given store. now / newID
// are overridable for deterministic tests.
// NewService 创建模块服务并注入存储和运行依赖。
func NewService(logger *pkglogger.Logger, store Store) Service {
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

// defaultInitialDelay is the offset applied when a CreateJob request does
// not carry an explicit NextRunAt. Phase 2b will replace this with a real
// cron-expression parser; until then the value is a conservative "fire at
// the next full-minute tick" default.
const defaultInitialDelay = time.Minute

// CreateJob 创建定时任务并计算首次运行时间。
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
	row, err := s.store.CreateJob(ctx, cronstore.CreateJobParams{
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
		return Job{}, err
	}
	return toJob(row), nil
}

// GetJob 读取定时任务详情。
func (s *service) GetJob(ctx context.Context, id string) (Job, error) {
	row, err := s.store.GetJobByID(ctx, id)
	if err != nil {
		if errors.Is(err, cronstore.ErrJobNotFound) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	return toJob(row), nil
}

// ListJobs 按条件列出定时任务。
func (s *service) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = toJob(r)
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
	if err := s.store.UpdateJobSchedule(ctx, cronstore.UpdateJobScheduleParams{
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
	if errors.Is(err, cronstore.ErrJobNotFound) {
		return ErrNotFound
	}
	return err
}

// DeleteJob 删除定时任务。
func (s *service) DeleteJob(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("cron: id is required")
	}
	err := s.store.DeleteJob(ctx, id)
	if errors.Is(err, cronstore.ErrJobNotFound) {
		return ErrNotFound
	}
	return err
}

// ListJobRuns 列出定时任务运行记录。
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

// RunOnce schedules a manual trigger by bumping next_run_at to now.
// The scheduler tick (default 10s) picks it up at the next cycle and
// drives the job through the same three-phase atomic protocol as a
// normal due trigger — idempotency_key + dedupe_key are still
// generated at submit time, so concurrent ticks can't double-fire.
//
// Note: a job whose next_retry_at is set in the future (i.e. retry
// pending) will still wait for that delay, since
// scheduler.claimOneDueJob ranks by COALESCE(next_retry_at, next_run_at).
//
// RunOnce 只把 next_run_at 提到现在，不直接建 run 或调用 StartTurn。
// 这样手动触发仍走同一套幂等流程。
func (s *service) RunOnce(ctx context.Context, jobID string) (Job, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Job{}, errors.New("cron: id is required")
	}
	row, err := s.store.GetJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, cronstore.ErrJobNotFound) {
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

// ----- validation helpers -----

// validateCreate 校验创建定时任务的输入。
func (s *service) validateCreate(req *CreateJobRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return ErrMissingName
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return ErrMissingPrompt
	}
	if strings.TrimSpace(req.ScheduleExpr) == "" {
		return ErrMissingSchedule
	}
	if strings.TrimSpace(req.CWD) == "" {
		return ErrMissingCWD
	}
	if req.MaxAttempts < 0 {
		return ErrInvalidMaxAttempts
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = cronstore.ProviderCodex
		req.Provider = provider
	}
	if provider != cronstore.ProviderCodex {
		return fmt.Errorf("%w: got %q", ErrProviderNotSupported, req.Provider)
	}
	// v1 codex whitelist: identity triple in config must resolve. This
	// reuses the stage-0 shared helper so cron and thread/start stay on
	// one canonicalize pipeline.
	// 这里先挡住错误的 Codex 身份配置，避免定时任务后来跑到错误的 home/instance。
	configMap, err := decodeConfigMap(req.Config)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if _, err := contract.ResolveCodexIdentity(configMap); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return nil
}

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

func normalizeConfig(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	// Re-marshal through a generic map so we land on canonical JSON and
	// reject obvious garbage before the DB CHECK fires.
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

// ----- row -> DTO mappers -----

// toJob 把存储层记录转换成 cron 领域对象。
func toJob(row cronstore.Job) Job {
	var skills []string
	if len(row.Skills) > 0 {
		if err := json.Unmarshal(row.Skills, &skills); err != nil {
			pkglogger.Warn("cron: corrupt skills json in job row",
				pkglogger.String("job_id", row.ID),
				pkglogger.String("error", err.Error()),
			)
		}
	}
	var config any
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &config); err != nil {
			pkglogger.Warn("cron: corrupt config json in job row",
				pkglogger.String("job_id", row.ID),
				pkglogger.String("error", err.Error()),
			)
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
	}
}

func toRun(row cronstore.Run) Run {
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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
