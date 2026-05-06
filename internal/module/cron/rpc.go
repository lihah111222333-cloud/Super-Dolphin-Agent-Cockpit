package cron

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// Host RPC parameter types. These are the on-the-wire shapes; service-level
// DTOs are in contract.go. next_run_at is supplied as RFC3339 string; when
// omitted, the service falls back to now + 1 minute (phase 2b will replace
// this with real cron-expression parsing).

type cronCreateParams struct {
	Name          string          `json:"name"`
	Prompt        string          `json:"prompt"`
	ScheduleType  string          `json:"schedule_type,omitempty"`
	ScheduleExpr  string          `json:"schedule_expr"`
	Timezone      string          `json:"timezone,omitempty"`
	Provider      string          `json:"provider,omitempty"`
	Model         string          `json:"model,omitempty"`
	CWD           string          `json:"cwd"`
	Config        json.RawMessage `json:"config,omitempty"`
	Skills        []string        `json:"skills,omitempty"`
	NotifyChannel string          `json:"notify_channel,omitempty"`
	Enabled       *bool           `json:"enabled,omitempty"`
	NextRunAt     string          `json:"next_run_at,omitempty"`
	MaxAttempts   int32           `json:"max_attempts,omitempty"`
}

type cronUpdateParams struct {
	ID string `json:"id"`
	cronCreateParams
}

type cronIDParams struct {
	ID string `json:"id"`
}

type cronEnabledParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type cronListRunsParams struct {
	JobID string `json:"job_id"`
	Limit int32  `json:"limit,omitempty"`
}

// NewHandlers wires the cronjob/* host RPC methods. It is registered via
// the Fx platformrpc.HandlerMapResult aggregate.
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"cronjob/create":     platformrpc.StrictHandler(createHandler(svc)),
		"cronjob/update":     platformrpc.StrictHandler(updateHandler(svc)),
		"cronjob/get":        platformrpc.StrictHandler(getHandler(svc)),
		"cronjob/list":       platformrpc.StrictHandler(listHandler(svc)),
		"cronjob/delete":     platformrpc.StrictHandler(deleteHandler(svc)),
		"cronjob/setEnabled": platformrpc.StrictHandler(setEnabledHandler(svc)),
		"cronjob/listRuns":   platformrpc.StrictHandler(listRunsHandler(svc)),
		"cronjob/runOnce":    platformrpc.StrictHandler(runOnceHandler(svc)),
	}}
}

func createHandler(svc Service) func(context.Context, cronCreateParams) (Job, error) {
	return func(ctx context.Context, p cronCreateParams) (Job, error) {
		req, err := createRequestFrom(p)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		job, err := svc.CreateJob(ctx, req)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

func updateHandler(svc Service) func(context.Context, cronUpdateParams) (Job, error) {
	return func(ctx context.Context, p cronUpdateParams) (Job, error) {
		base, err := createRequestFrom(p.cronCreateParams)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		req := UpdateJobRequest{
			ID:            p.ID,
			Name:          base.Name,
			Prompt:        base.Prompt,
			ScheduleType:  base.ScheduleType,
			ScheduleExpr:  base.ScheduleExpr,
			Timezone:      base.Timezone,
			Provider:      base.Provider,
			Model:         base.Model,
			CWD:           base.CWD,
			Config:        base.Config,
			Skills:        base.Skills,
			NotifyChannel: base.NotifyChannel,
			Enabled:       base.Enabled,
			NextRunAt:     base.NextRunAt,
			MaxAttempts:   base.MaxAttempts,
		}
		job, err := svc.UpdateJob(ctx, req)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

func getHandler(svc Service) func(context.Context, cronIDParams) (Job, error) {
	return func(ctx context.Context, p cronIDParams) (Job, error) {
		job, err := svc.GetJob(ctx, p.ID)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

func listHandler(svc Service) func(context.Context, struct{}) (map[string]any, error) {
	return func(ctx context.Context, _ struct{}) (map[string]any, error) {
		jobs, err := svc.ListJobs(ctx)
		if err != nil {
			return nil, mapRPCError(err)
		}
		if jobs == nil {
			jobs = []Job{}
		}
		return map[string]any{"jobs": jobs}, nil
	}
}

func runOnceHandler(svc Service) func(context.Context, cronIDParams) (Job, error) {
	return func(ctx context.Context, p cronIDParams) (Job, error) {
		job, err := svc.RunOnce(ctx, p.ID)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

func deleteHandler(svc Service) func(context.Context, cronIDParams) (map[string]any, error) {
	return func(ctx context.Context, p cronIDParams) (map[string]any, error) {
		if err := svc.DeleteJob(ctx, p.ID); err != nil {
			return nil, mapRPCError(err)
		}
		return map[string]any{"deleted": true, "id": p.ID}, nil
	}
}

func setEnabledHandler(svc Service) func(context.Context, cronEnabledParams) (map[string]any, error) {
	return func(ctx context.Context, p cronEnabledParams) (map[string]any, error) {
		if err := svc.SetJobEnabled(ctx, p.ID, p.Enabled); err != nil {
			return nil, mapRPCError(err)
		}
		return map[string]any{"id": p.ID, "enabled": p.Enabled}, nil
	}
}

func listRunsHandler(svc Service) func(context.Context, cronListRunsParams) (map[string]any, error) {
	return func(ctx context.Context, p cronListRunsParams) (map[string]any, error) {
		runs, err := svc.ListJobRuns(ctx, p.JobID, p.Limit)
		if err != nil {
			return nil, mapRPCError(err)
		}
		if runs == nil {
			runs = []Run{}
		}
		return map[string]any{"runs": runs}, nil
	}
}

// createRequestFrom normalizes the RPC wire shape into the service-level
// CreateJobRequest. It parses next_run_at from RFC3339 when supplied and
// defaults Enabled to true when the caller omits the field.
func createRequestFrom(p cronCreateParams) (CreateJobRequest, error) {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	var nextRunAt time.Time
	if p.NextRunAt != "" {
		t, err := time.Parse(time.RFC3339, p.NextRunAt)
		if err != nil {
			return CreateJobRequest{}, jrpc2.Errorf(jrpc2.InvalidParams,
				"cron: next_run_at must be RFC3339, got %q", p.NextRunAt)
		}
		nextRunAt = t
	}
	return CreateJobRequest{
		Name:          p.Name,
		Prompt:        p.Prompt,
		ScheduleType:  p.ScheduleType,
		ScheduleExpr:  p.ScheduleExpr,
		Timezone:      p.Timezone,
		Provider:      p.Provider,
		Model:         p.Model,
		CWD:           p.CWD,
		Config:        p.Config,
		Skills:        p.Skills,
		NotifyChannel: p.NotifyChannel,
		Enabled:       enabled,
		NextRunAt:     nextRunAt,
		MaxAttempts:   p.MaxAttempts,
	}, nil
}

// mapRPCError classifies service / store errors into jrpc2 codes. Validation
// and identity errors map to InvalidParams; not-found maps to jrpc2's
// dedicated not-found via platformrpc.ErrNotFound; everything else propagates as
// the raw error so the transport layer handles it as an internal error.
func mapRPCError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, ErrMissingCWD),
		errors.Is(err, ErrMissingName),
		errors.Is(err, ErrMissingPrompt),
		errors.Is(err, ErrMissingSchedule),
		errors.Is(err, ErrInvalidMaxAttempts),
		errors.Is(err, ErrInvalidConfig),
		errors.Is(err, ErrProviderNotSupported),
		errors.Is(err, ErrJobDisabled),
		errors.Is(err, cronstore.ErrEmptyID),
		errors.Is(err, cronstore.ErrEmptyCWD),
		errors.Is(err, cronstore.ErrEmptyProvider),
		errors.Is(err, cronstore.ErrEmptyScheduleExpr):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	}
	return err
}
