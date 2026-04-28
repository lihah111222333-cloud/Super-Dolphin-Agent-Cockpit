package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// Cron tools wrap internal/store/cron with the same validation guards used
// by internal/module/cron.Service so an agent can create / list / delete /
// toggle scheduled jobs from a turn without going back through the host
// JSON-RPC. The store is the same row-set the host UI reads from, so jobs
// created here show up immediately in the TasksPage UI.
//
// v1 only accepts provider=codex; the codex identity triple
// (codexHome / codexInstanceKey / codexModelProvider) is validated with
// providershared.ResolveCodexIdentity to stay in lock-step with the host
// service-layer validateCreate.

const (
	cronProviderCodex      = "codex"
	cronDefaultScheduleTyp = "cron"
	cronDefaultInitialLag  = time.Minute
)

type cronCreateInput struct {
	Name          string         `json:"name"`
	Prompt        string         `json:"prompt"`
	ScheduleExpr  string         `json:"schedule_expr"`
	ScheduleType  string         `json:"schedule_type"`
	Timezone      string         `json:"timezone"`
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	CWD           string         `json:"cwd"`
	Config        map[string]any `json:"config"`
	Skills        []string       `json:"skills"`
	NotifyChannel string         `json:"notify_channel"`
	Enabled       *bool          `json:"enabled"`
	NextRunAt     string         `json:"next_run_at"`
	MaxAttempts   int32          `json:"max_attempts"`
}

type cronIDInput struct {
	ID string `json:"id"`
}

type cronSetEnabledInput struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type cronListRunsInput struct {
	JobID string `json:"job_id"`
	Limit int32  `json:"limit"`
}

type cronJobDTO struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ScheduleExpr  string   `json:"schedule_expr"`
	Provider      string   `json:"provider"`
	CWD           string   `json:"cwd"`
	Enabled       bool     `json:"enabled"`
	NextRunAt     string   `json:"next_run_at,omitempty"`
	LastRunAt     string   `json:"last_run_at,omitempty"`
	LastStatus    string   `json:"last_status,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	FailureCount  int32    `json:"failure_count"`
	MaxAttempts   int32    `json:"max_attempts"`
	Skills        []string `json:"skills,omitempty"`
	NotifyChannel string   `json:"notify_channel,omitempty"`
}

type cronRunDTO struct {
	ID          string `json:"id"`
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
	SubmittedAt string `json:"submitted_at,omitempty"`
	TurnID      string `json:"turn_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

func cronToolDefinitions(store cronstore.Store) []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"cron_job_create",
			"Create a scheduled (cron) job that re-sends `prompt` to a codex agent on the given schedule_expr. Required: name, prompt, schedule_expr, cwd, and config.codexHome (codex identity). Provider is locked to 'codex' in v1.",
			ObjectSchema(map[string]Schema{
				"name":           StringSchema("Human-readable job name."),
				"prompt":         StringSchema("Prompt re-sent to the agent on every trigger."),
				"schedule_expr":  StringSchema("Cron expression, e.g. '*/5 * * * *'."),
				"schedule_type":  StringSchema("Schedule kind. Defaults to 'cron'."),
				"timezone":       StringSchema("IANA timezone, optional (e.g. 'Asia/Shanghai')."),
				"provider":       StringSchema("Provider name. v1 only accepts 'codex'."),
				"model":          StringSchema("Model override, optional."),
				"cwd":            StringSchema("Working directory the job runs against."),
				"config":         RawObjectSchema("Provider-specific config. For codex must contain codexHome (and usually codexInstanceKey / codexModelProvider)."),
				"skills":         ArraySchema(StringSchema("Skill name."), "Skills to attach on every trigger."),
				"notify_channel": StringSchema("Notification channel for completion / failure events."),
				"enabled":        BooleanSchema("Whether the job fires immediately. Defaults to true."),
				"next_run_at":    StringSchema("Optional RFC3339 timestamp for first run. Defaults to now + 1 minute."),
				"max_attempts":   IntegerSchema("Maximum retry attempts on failure. Default 0 (no retry)."),
			}, "name", "prompt", "schedule_expr", "cwd", "config"),
			HandleCronCreate(store),
		),
		defineTool(
			"cron_job_list",
			"List all scheduled cron jobs registered on this host.",
			ObjectSchema(map[string]Schema{}),
			HandleCronList(store),
		),
		defineTool(
			"cron_job_delete",
			"Delete a scheduled cron job by id.",
			ObjectSchema(map[string]Schema{
				"id": StringSchema("Cron job id (uuid)."),
			}, "id"),
			HandleCronDelete(store),
		),
		defineTool(
			"cron_job_set_enabled",
			"Enable or disable a scheduled cron job without deleting it.",
			ObjectSchema(map[string]Schema{
				"id":      StringSchema("Cron job id (uuid)."),
				"enabled": BooleanSchema("Whether the job should fire."),
			}, "id", "enabled"),
			HandleCronSetEnabled(store),
		),
		defineTool(
			"cron_job_list_runs",
			"List recent run records for a cron job, newest first.",
			ObjectSchema(map[string]Schema{
				"job_id": StringSchema("Cron job id (uuid)."),
				"limit":  IntegerSchema("Max number of runs to return. Defaults to 50."),
			}, "job_id"),
			HandleCronListRuns(store),
		),
	)
}

func HandleCronCreate(store cronstore.Store) ToolHandler {
	return makeHandler(store, "cron store", func(ctx context.Context, in cronCreateInput) (cronJobDTO, error) {
		req, err := normalizeCronCreate(in)
		if err != nil {
			return cronJobDTO{}, err
		}
		row, err := store.CreateJob(ctx, req)
		if err != nil {
			return cronJobDTO{}, err
		}
		return cronJobToDTO(row), nil
	})
}

func HandleCronList(store cronstore.Store) ToolHandler {
	return makeHandler(store, "cron store", func(ctx context.Context, _ struct{}) (map[string]any, error) {
		rows, err := store.ListJobs(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]cronJobDTO, len(rows))
		for i, r := range rows {
			out[i] = cronJobToDTO(r)
		}
		return map[string]any{"jobs": out}, nil
	})
}

func HandleCronDelete(store cronstore.Store) ToolHandler {
	return makeHandler(store, "cron store", func(ctx context.Context, in cronIDInput) (map[string]any, error) {
		id, err := requireTrimmed(in.ID, "id")
		if err != nil {
			return nil, err
		}
		if err := store.DeleteJob(ctx, id); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"id": id, "deleted": true}), nil
	})
}

func HandleCronSetEnabled(store cronstore.Store) ToolHandler {
	return makeHandler(store, "cron store", func(ctx context.Context, in cronSetEnabledInput) (map[string]any, error) {
		id, err := requireTrimmed(in.ID, "id")
		if err != nil {
			return nil, err
		}
		if err := store.SetJobEnabled(ctx, id, in.Enabled, time.Now().UTC()); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"id": id, "enabled": in.Enabled}), nil
	})
}

func HandleCronListRuns(store cronstore.Store) ToolHandler {
	return makeHandler(store, "cron store", func(ctx context.Context, in cronListRunsInput) (map[string]any, error) {
		jobID, err := requireTrimmed(in.JobID, "job_id")
		if err != nil {
			return nil, err
		}
		limit := normalizeListLimit(int(in.Limit), 50, 500)
		rows, err := store.ListRunsByJob(ctx, jobID, int32(limit))
		if err != nil {
			return nil, err
		}
		out := make([]cronRunDTO, len(rows))
		for i, r := range rows {
			out[i] = cronRunToDTO(r)
		}
		return map[string]any{"runs": out}, nil
	})
}

// normalizeCronCreate replicates internal/module/cron/service.go:validateCreate
// and normalizeConfig so the cron tool stays in lock-step with the host
// service. Any change to the service-layer guard must also be reflected here.
func normalizeCronCreate(in cronCreateInput) (cronstore.CreateJobParams, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return cronstore.CreateJobParams{}, errors.New("name is required")
	}
	prompt := in.Prompt
	if strings.TrimSpace(prompt) == "" {
		return cronstore.CreateJobParams{}, errors.New("prompt is required")
	}
	scheduleExpr := strings.TrimSpace(in.ScheduleExpr)
	if scheduleExpr == "" {
		return cronstore.CreateJobParams{}, errors.New("schedule_expr is required")
	}
	cwd := strings.TrimSpace(in.CWD)
	if cwd == "" {
		return cronstore.CreateJobParams{}, errors.New("cwd is required")
	}
	if in.MaxAttempts < 0 {
		return cronstore.CreateJobParams{}, errors.New("max_attempts must be >= 0")
	}
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		provider = cronProviderCodex
	}
	if provider != cronProviderCodex {
		return cronstore.CreateJobParams{}, fmt.Errorf("provider %q not supported in v1 (only 'codex')", in.Provider)
	}
	cfg := in.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, err := providershared.ResolveCodexIdentity(cfg); err != nil {
		return cronstore.CreateJobParams{}, fmt.Errorf("config: %w", err)
	}
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return cronstore.CreateJobParams{}, fmt.Errorf("config: %w", err)
	}
	skillsBytes, err := marshalCronSkills(in.Skills)
	if err != nil {
		return cronstore.CreateJobParams{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now().UTC()
	nextRun := now.Add(cronDefaultInitialLag)
	if strings.TrimSpace(in.NextRunAt) != "" {
		t, perr := time.Parse(time.RFC3339, in.NextRunAt)
		if perr != nil {
			return cronstore.CreateJobParams{}, fmt.Errorf("next_run_at must be RFC3339: %w", perr)
		}
		nextRun = t
	}
	scheduleType := strings.TrimSpace(in.ScheduleType)
	if scheduleType == "" {
		scheduleType = cronDefaultScheduleTyp
	}
	return cronstore.CreateJobParams{
		ID:            uuid.NewString(),
		Name:          name,
		Prompt:        prompt,
		ScheduleType:  scheduleType,
		ScheduleExpr:  scheduleExpr,
		Timezone:      strings.TrimSpace(in.Timezone),
		Provider:      provider,
		Model:         strings.TrimSpace(in.Model),
		CWD:           cwd,
		Config:        configBytes,
		Skills:        skillsBytes,
		NotifyChannel: strings.TrimSpace(in.NotifyChannel),
		Enabled:       enabled,
		NextRunAt:     nextRun,
		MaxAttempts:   in.MaxAttempts,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func marshalCronSkills(skills []string) ([]byte, error) {
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

func cronJobToDTO(r cronstore.Job) cronJobDTO {
	skills := decodeCronSkills(r.Skills)
	dto := cronJobDTO{
		ID:            r.ID,
		Name:          r.Name,
		ScheduleExpr:  r.ScheduleExpr,
		Provider:      r.Provider,
		CWD:           r.CWD,
		Enabled:       r.Enabled,
		FailureCount:  r.FailureCount,
		MaxAttempts:   r.MaxAttempts,
		Skills:        skills,
		NotifyChannel: r.NotifyChannel,
		LastStatus:    r.LastStatus,
		LastError:     r.LastError,
	}
	if !r.NextRunAt.IsZero() {
		dto.NextRunAt = r.NextRunAt.UTC().Format(time.RFC3339)
	}
	if !r.LastRunAt.IsZero() {
		dto.LastRunAt = r.LastRunAt.UTC().Format(time.RFC3339)
	}
	return dto
}

func cronRunToDTO(r cronstore.Run) cronRunDTO {
	dto := cronRunDTO{
		ID:     r.ID,
		JobID:  r.JobID,
		Status: r.Status,
		TurnID: r.TurnID,
		Error:  r.Error,
	}
	if !r.ScheduledAt.IsZero() {
		dto.ScheduledAt = r.ScheduledAt.UTC().Format(time.RFC3339)
	}
	if !r.SubmittedAt.IsZero() {
		dto.SubmittedAt = r.SubmittedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

func decodeCronSkills(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
