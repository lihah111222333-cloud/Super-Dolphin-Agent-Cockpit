package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultAgentReportWaitTimeout = platformconfig.RPCRequestTimeout
	agentReportPollInterval       = 50 * time.Millisecond
)

type GetAgentReportInput struct {
	AgentID        string `json:"agent_id"`
	Pos            string `json:"pos,omitempty"`
	Wait           *bool  `json:"wait,omitempty"`
	RequesterID    string `json:"requester_id,omitempty"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
	AfterReportSeq *int64 `json:"after_report_seq,omitempty"`
}

type getAgentReportsInput struct {
	AgentIDs              []string         `json:"agent_ids"`
	Wait                  *bool            `json:"wait,omitempty"`
	TimeoutMS             int              `json:"timeout_ms,omitempty"`
	AfterReportSeqByAgent map[string]int64 `json:"after_report_seq_by_agent,omitempty"`
}

type agentReportsRequest struct {
	agentIDs              []string
	wait                  bool
	timeout               time.Duration
	afterReportSeqByAgent map[string]int64
}

type agentReportsResult struct {
	Status    string            `json:"status"`
	Results   []agentReportItem `json:"results"`
	Completed int               `json:"completed"`
	Pending   int               `json:"pending"`
	TimedOut  bool              `json:"timed_out"`
}

type agentReportItem struct {
	AgentID   string    `json:"agent_id"`
	State     string    `json:"state"`
	Report    string    `json:"report"`
	ReportSeq int64     `json:"report_seq"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error"`
}

// HandleGetAgentReport 处理get代理report。
func HandleGetAgentReport(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in GetAgentReportInput) (any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		if !agentReportShouldWait(in) {
			return svc.GetReport(ctx, agentID)
		}
		return waitForAgentReport(ctx, svc, in, agentID)
	})
}

// HandleGetAgentReports 批量读取或等待多个代理报告，只支持 all wait 语义。
func HandleGetAgentReports(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in getAgentReportsInput) (any, error) {
		req, err := validateAgentReportsInput(in)
		if err != nil {
			return nil, err
		}
		if !req.wait {
			return readAgentReportsSnapshot(ctx, svc, req), nil
		}
		return waitForAgentReports(ctx, svc, req), nil
	})
}

func agentReportShouldWait(in GetAgentReportInput) bool {
	return in.Wait != nil && *in.Wait
}

// validateAgentReportsInput 校验批量报告读取参数，并把动态 seq map 规范到 trim 后的 agent id。
func validateAgentReportsInput(in getAgentReportsInput) (agentReportsRequest, error) {
	if len(in.AgentIDs) == 0 {
		return agentReportsRequest{}, fmt.Errorf("agent_ids is required")
	}
	if in.TimeoutMS < 0 {
		return agentReportsRequest{}, fmt.Errorf("timeout_ms must be non-negative")
	}
	timeout := defaultAgentReportWaitTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	agentIDs := make([]string, 0, len(in.AgentIDs))
	for _, raw := range in.AgentIDs {
		agentID := strings.TrimSpace(raw)
		if agentID == "" {
			return agentReportsRequest{}, fmt.Errorf("agent_ids must not contain empty agent id")
		}
		agentIDs = append(agentIDs, agentID)
	}
	afterByAgent := make(map[string]int64, len(in.AfterReportSeqByAgent))
	for rawAgentID, seq := range in.AfterReportSeqByAgent {
		agentID := strings.TrimSpace(rawAgentID)
		if agentID == "" {
			return agentReportsRequest{}, fmt.Errorf("after_report_seq_by_agent must not contain empty agent id")
		}
		if seq < 0 {
			return agentReportsRequest{}, fmt.Errorf("after_report_seq_by_agent values must be non-negative")
		}
		afterByAgent[agentID] = seq
	}
	return agentReportsRequest{
		agentIDs:              agentIDs,
		wait:                  in.Wait != nil && *in.Wait,
		timeout:               timeout,
		afterReportSeqByAgent: afterByAgent,
	}, nil
}

func readAgentReportsSnapshot(ctx context.Context, svc contract.OrchestrationService, req agentReportsRequest) agentReportsResult {
	items := make([]agentReportItem, len(req.agentIDs))
	completed := 0
	for i, agentID := range req.agentIDs {
		report, err := svc.GetReport(ctx, agentID)
		items[i] = agentReportItemFromResult(agentID, report, err)
		if items[i].OK {
			completed++
		}
	}
	return finishAgentReportsResult(items, completed, 0, false)
}

// waitForAgentReports 用一个共享 timeout 轮询所有 pending agent，避免逐个 wait 放大总耗时。
func waitForAgentReports(ctx context.Context, svc contract.OrchestrationService, req agentReportsRequest) agentReportsResult {
	waitCtx, cancel := platformconfig.WithTimeout(ctx, req.timeout)
	defer cancel()
	ticker := time.NewTicker(agentReportPollInterval)
	defer ticker.Stop()
	items := make([]agentReportItem, len(req.agentIDs))
	latest := make([]contract.AgentReportResult, len(req.agentIDs))
	pending := make(map[int]struct{}, len(req.agentIDs))
	for i := range req.agentIDs {
		pending[i] = struct{}{}
	}
	completed := 0
	for len(pending) > 0 {
		for i := range pending {
			completedNow, done, timedOut := pollPendingAgentReport(waitCtx, svc, req, i, items, latest)
			if timedOut {
				return finishTimedOutAgentReports(req, items, latest, pending, completed)
			}
			if !done {
				continue
			}
			if completedNow {
				completed++
			}
			delete(pending, i)
		}
		if len(pending) == 0 {
			break
		}
		select {
		case <-waitCtx.Done():
			return finishTimedOutAgentReports(req, items, latest, pending, completed)
		case <-ticker.C:
		}
	}
	return finishAgentReportsResult(items, completed, 0, false)
}

// pollPendingAgentReport 读取一个 pending agent，并把“旧终态无新 report”作为 item 错误收口。
func pollPendingAgentReport(
	ctx context.Context,
	svc contract.OrchestrationService,
	req agentReportsRequest,
	i int,
	items []agentReportItem,
	latest []contract.AgentReportResult,
) (completed bool, done bool, timedOut bool) {
	agentID := req.agentIDs[i]
	report, err := svc.GetReport(ctx, agentID)
	latest[i] = report
	if err != nil {
		if ctx.Err() != nil {
			return false, false, true
		}
		items[i] = agentReportItemFromResult(agentID, report, err)
		return false, true, false
	}
	afterSeq, hasAfterSeq := req.afterReportSeqByAgent[agentID]
	completedReport, ok, terminalErr := completedBatchAgentReport(report, afterSeq, hasAfterSeq, agentID)
	if terminalErr != nil {
		items[i] = agentReportItemFromResult(agentID, report, terminalErr)
		return false, true, false
	}
	if !ok {
		return false, false, false
	}
	items[i] = agentReportItemFromResult(agentID, completedReport, nil)
	return true, true, false
}

func completedBatchAgentReport(result contract.AgentReportResult, afterSeq int64, hasAfterSeq bool, agentID string) (contract.AgentReportResult, bool, error) {
	if hasAfterSeq && result.ReportSeq <= afterSeq {
		if err := terminalAgentReportNoNewReportError(result, &afterSeq, agentID); err != nil {
			return contract.AgentReportResult{}, false, err
		}
		return contract.AgentReportResult{}, false, nil
	}
	completed, ok := completedAgentReport(result, nil)
	return completed, ok, nil
}

func agentReportItemFromResult(agentID string, result contract.AgentReportResult, err error) agentReportItem {
	item := agentReportItem{
		AgentID:   agentID,
		State:     result.State,
		Report:    result.Report,
		ReportSeq: result.ReportSeq,
		UpdatedAt: result.UpdatedAt,
		OK:        err == nil,
	}
	if item.AgentID == "" {
		item.AgentID = result.AgentID
	}
	if err != nil {
		item.Error = err.Error()
	}
	return item
}

func finishTimedOutAgentReports(
	req agentReportsRequest,
	items []agentReportItem,
	latest []contract.AgentReportResult,
	pending map[int]struct{},
	completed int,
) agentReportsResult {
	for i := range pending {
		err := agentReportWaitTimeoutError(context.Background(), req.timeout, req.agentIDs[i], afterReportSeqPtr(req, req.agentIDs[i]))
		items[i] = agentReportItemFromResult(req.agentIDs[i], latest[i], err)
	}
	return finishAgentReportsResult(items, completed, len(pending), true)
}

func afterReportSeqPtr(req agentReportsRequest, agentID string) *int64 {
	seq, ok := req.afterReportSeqByAgent[agentID]
	if !ok {
		return nil
	}
	return &seq
}

func finishAgentReportsResult(items []agentReportItem, completed, pending int, timedOut bool) agentReportsResult {
	status := "completed"
	if timedOut {
		status = "timed_out"
		if completed > 0 {
			status = "partial"
		}
	} else if completed < len(items) {
		status = "partial"
	}
	return agentReportsResult{
		Status:    status,
		Results:   items,
		Completed: completed,
		Pending:   pending,
		TimedOut:  timedOut,
	}
}

// waitForAgentReport 等待编排侧产出代理报告。
func waitForAgentReport(ctx context.Context, svc contract.OrchestrationService, in GetAgentReportInput, agentID string) (any, error) {
	timeout, requesterID, err := validateAgentReportWait(ctx, in, agentID)
	if err != nil {
		return nil, err
	}
	if requesterID != "" {
		if _, err := svc.RememberReportRequest(ctx, contract.RememberReportRequest{AgentID: agentID, RequesterID: requesterID}); err != nil {
			if !agentReportWaitableError(err) {
				return nil, err
			}
		}
	}
	return pollAgentReport(ctx, svc, agentID, timeout, in.AfterReportSeq)
}

// validateAgentReportWait 校验等待参数，after_report_seq 只允许等待更大的持久化版本。
func validateAgentReportWait(ctx context.Context, in GetAgentReportInput, agentID string) (time.Duration, string, error) {
	if in.TimeoutMS < 0 {
		return 0, "", fmt.Errorf("timeout_ms must be non-negative")
	}
	if in.AfterReportSeq != nil && *in.AfterReportSeq < 0 {
		return 0, "", fmt.Errorf("after_report_seq must be non-negative")
	}
	timeout := defaultAgentReportWaitTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	requesterID := reportWaitRequester(ctx, in.RequesterID)
	if requesterID != "" && strings.EqualFold(agentID, requesterID) {
		return 0, "", fmt.Errorf("agent_id and requester_id must differ when waiting for a report")
	}
	return timeout, requesterID, nil
}

func reportWaitRequester(ctx context.Context, requesterID string) string {
	requesterID = strings.TrimSpace(requesterID)
	if requesterID != "" {
		return requesterID
	}
	if scope, ok := mcpcommon.ToolScopeFromContext(ctx); ok {
		return shared.FirstTrimmed(scope.AgentID, scope.ThreadID)
	}
	return ""
}

// pollAgentReport 处理poll代理report。
func pollAgentReport(
	ctx context.Context,
	svc contract.OrchestrationService,
	agentID string,
	timeout time.Duration,
	afterReportSeq *int64,
) (contract.AgentReportResult, error) {
	waitCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(agentReportPollInterval)
	defer ticker.Stop()
	for {
		result, err := svc.GetReport(waitCtx, agentID)
		if completed, ok := completedAgentReport(result, afterReportSeq); ok && (err == nil || agentReportWaitableError(err)) {
			return completed, nil
		}
		if err := terminalAgentReportNoNewReportError(result, afterReportSeq, agentID); err != nil {
			return contract.AgentReportResult{}, err
		}
		if err := agentReportPollError(ctx, waitCtx, err, timeout, agentID, afterReportSeq); err != nil {
			return contract.AgentReportResult{}, err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return contract.AgentReportResult{}, ctx.Err()
			}
			return contract.AgentReportResult{}, agentReportWaitTimeoutError(ctx, timeout, agentID, afterReportSeq)
		case <-ticker.C:
		}
	}
}

func completedAgentReport(result contract.AgentReportResult, afterReportSeq *int64) (contract.AgentReportResult, bool) {
	if afterReportSeq != nil && result.ReportSeq <= *afterReportSeq {
		return contract.AgentReportResult{}, false
	}
	if strings.TrimSpace(result.Report) != "" {
		return result, true
	}
	if !terminalAgentReportState(result.State) {
		return contract.AgentReportResult{}, false
	}
	result.Report = agentReportNoReportFallback(result.State)
	return result, true
}

func terminalAgentReportState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "stopped":
		return true
	default:
		return false
	}
}

func agentReportNoReportFallback(state string) string {
	state = strings.TrimSpace(state)
	if state == "" {
		return "agent ended without producing a turn report"
	}
	return "agent ended in state '" + state + "' without producing a turn report"
}

func terminalAgentReportNoNewReportError(result contract.AgentReportResult, afterReportSeq *int64, agentID string) error {
	if afterReportSeq == nil || !terminalAgentReportState(result.State) || result.ReportSeq > *afterReportSeq {
		return nil
	}
	return fmt.Errorf("agent %q ended in state %q without a report after report_seq %d", agentID, result.State, *afterReportSeq)
}

func agentReportPollError(ctx, waitCtx context.Context, err error, timeout time.Duration, agentID string, afterReportSeq *int64) error {
	if err == nil {
		return nil
	}
	if waitCtx.Err() != nil {
		return agentReportWaitTimeoutError(ctx, timeout, agentID, afterReportSeq)
	}
	if agentReportWaitableError(err) {
		return nil
	}
	return err
}

func agentReportWaitTimeoutError(ctx context.Context, timeout time.Duration, agentID string, afterReportSeq *int64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if afterReportSeq != nil {
		return fmt.Errorf("timed out waiting %s for report from agent %q after report_seq %d", timeout, agentID, *afterReportSeq)
	}
	return fmt.Errorf("timed out waiting %s for report from agent %q", timeout, agentID)
}

func agentReportWaitableError(err error) bool {
	return errors.Is(err, contract.ErrAgentNotFound)
}
