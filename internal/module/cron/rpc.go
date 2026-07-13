package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

// JSON-RPC 参数类型保持前端 wire shape，业务层 DTO 在 service/contract 中转换。
// next_run_at 仅接受 RFC3339 字符串；省略时由 service 根据当前调度策略计算首次运行时间。

// cronCreateParams 是 cronjob/create 的 JSON-RPC 请求参数。
type cronCreateParams struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	ScheduleType string `json:"schedule_type,omitempty"`
	ScheduleExpr string `json:"schedule_expr"`
	Timezone     string `json:"timezone,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	CWD          string `json:"cwd"`
	// provider config 是开放 JSON 包，service 负责校验后按原始 JSON 持久化。
	Config        json.RawMessage `json:"config,omitempty"`
	Skills        []string        `json:"skills,omitempty"`
	NotifyChannel string          `json:"notify_channel,omitempty"`
	Enabled       *bool           `json:"enabled,omitempty"`
	NextRunAt     string          `json:"next_run_at,omitempty"`
	MaxAttempts   int32           `json:"max_attempts,omitempty"`
}

// cronUpdateParams 是 cronjob/update 的 JSON-RPC 请求参数，复用创建字段以保持 wire 兼容。
type cronUpdateParams struct {
	ID string `json:"id"`
	cronCreateParams
}

// cronIDParams 是只携带 job ID 的 cronjob/get、delete、runOnce 请求参数。
type cronIDParams struct {
	ID string `json:"id"`
}

// cronEnabledParams 是 cronjob/setEnabled 的 JSON-RPC 请求参数。
type cronEnabledParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// cronListRunsParams 是 cronjob/listRuns 的 JSON-RPC 请求参数。
type cronListRunsParams struct {
	JobID string `json:"job_id"`
	Limit int32  `json:"limit,omitempty"`
}

// cronListParams is the strict wire contract for bounded cronjob/list requests.
type cronListParams struct {
	Limit  int32   `json:"limit"`
	Cursor *string `json:"cursor"`
}

// JSON-RPC 响应类型的 JSON tag 固定为前端既有字段名，避免破坏旧 UI 调用。

// cronListResponse 是 cronjob/list 的 JSON-RPC 响应。
type cronListResponse struct {
	Jobs       []Job  `json:"jobs"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// cronDeleteResponse 是 cronjob/delete 的 JSON-RPC 响应，返回被删除 job 的 ID。
type cronDeleteResponse struct {
	Deleted bool   `json:"deleted"`
	ID      string `json:"id"`
}

// cronSetEnabledResponse 是 cronjob/setEnabled 的 JSON-RPC 响应，回显最终启用状态。
type cronSetEnabledResponse struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// cronListRunsResponse 是 cronjob/listRuns 的 JSON-RPC 响应，runs 为空时返回空切片。
type cronListRunsResponse struct {
	Runs []Run `json:"runs"`
}

// NewHandlers 注册 cronjob/* JSON-RPC handler。
// handler 只做 wire 参数转换和错误码映射，调度持久化边界留在 service/scheduler。
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

// createHandler 构造 cronjob/create 的处理函数。
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

// updateHandler 构造 cronjob/update 的处理函数，先复用 createRequestFrom 做通用字段解析。
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

// getHandler 构造 cronjob/get 的处理函数。
func getHandler(svc Service) func(context.Context, cronIDParams) (Job, error) {
	return func(ctx context.Context, p cronIDParams) (Job, error) {
		job, err := svc.GetJob(ctx, p.ID)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

// listHandler 构造 cronjob/list 的处理函数。
func listHandler(svc Service) func(context.Context, cronListParams) (cronListResponse, error) {
	return func(ctx context.Context, p cronListParams) (cronListResponse, error) {
		if p.Cursor == nil {
			return cronListResponse{}, mapRPCError(ErrInvalidListCursor)
		}
		page, err := svc.ListJobsPage(ctx, ListJobsPageParams{Limit: p.Limit, Cursor: *p.Cursor})
		if err != nil {
			return cronListResponse{}, mapRPCError(err)
		}
		if page.Jobs == nil {
			page.Jobs = []Job{}
		}
		return cronListResponse{Jobs: page.Jobs, NextCursor: page.NextCursor, HasMore: page.HasMore}, nil
	}
}

// runOnceHandler 构造 cronjob/runOnce 的处理函数。
func runOnceHandler(svc Service) func(context.Context, cronIDParams) (Job, error) {
	return func(ctx context.Context, p cronIDParams) (Job, error) {
		job, err := svc.RunOnce(ctx, p.ID)
		if err != nil {
			return Job{}, mapRPCError(err)
		}
		return job, nil
	}
}

// deleteHandler 构造 cronjob/delete 的处理函数。
func deleteHandler(svc Service) func(context.Context, cronIDParams) (cronDeleteResponse, error) {
	return func(ctx context.Context, p cronIDParams) (cronDeleteResponse, error) {
		if err := svc.DeleteJob(ctx, p.ID); err != nil {
			return cronDeleteResponse{}, mapRPCError(err)
		}
		return cronDeleteResponse{Deleted: true, ID: p.ID}, nil
	}
}

// setEnabledHandler 构造 cronjob/setEnabled 的处理函数。
func setEnabledHandler(svc Service) func(context.Context, cronEnabledParams) (cronSetEnabledResponse, error) {
	return func(ctx context.Context, p cronEnabledParams) (cronSetEnabledResponse, error) {
		if err := svc.SetJobEnabled(ctx, p.ID, p.Enabled); err != nil {
			return cronSetEnabledResponse{}, mapRPCError(err)
		}
		return cronSetEnabledResponse(p), nil
	}
}

// listRunsHandler 构造 cronjob/listRuns 的处理函数。
func listRunsHandler(svc Service) func(context.Context, cronListRunsParams) (cronListRunsResponse, error) {
	return func(ctx context.Context, p cronListRunsParams) (cronListRunsResponse, error) {
		runs, err := svc.ListJobRuns(ctx, p.JobID, p.Limit)
		if err != nil {
			return cronListRunsResponse{}, mapRPCError(err)
		}
		if runs == nil {
			runs = []Run{}
		}
		return cronListRunsResponse{Runs: runs}, nil
	}
}

// createRequestFrom 将 JSON-RPC wire 参数转换为 service 层 CreateJobRequest。
// enabled 省略时按启用处理；next_run_at 一旦提供必须是 RFC3339，避免模糊时间落库。
func createRequestFrom(p cronCreateParams) (CreateJobRequest, error) {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	var nextRunAt time.Time
	if p.NextRunAt != "" {
		t, err := time.Parse(time.RFC3339, p.NextRunAt)
		if err != nil {
			return CreateJobRequest{}, platformrpc.ErrInvalidParams(
				fmt.Sprintf("cron: next_run_at must be RFC3339, got %q", p.NextRunAt))
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

// mapRPCError 将服务层/存储层错误映射为 jrpc2 错误码，未知错误原样透传。
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
		errors.Is(err, ErrInvalidScheduleExpr),
		errors.Is(err, ErrInvalidTimezone),
		errors.Is(err, ErrInvalidMaxAttempts),
		errors.Is(err, ErrInvalidConfig),
		errors.Is(err, ErrInvalidListLimit),
		errors.Is(err, ErrInvalidListCursor),
		errors.Is(err, ErrProviderNotSupported),
		errors.Is(err, ErrJobDisabled),
		errors.Is(err, ErrStoreEmptyID),
		errors.Is(err, ErrStoreEmptyCWD),
		errors.Is(err, ErrStoreEmptyProvider),
		errors.Is(err, ErrStoreEmptyScheduleExpr):
		return platformrpc.ErrInvalidParams(err.Error())
	}
	return err
}
