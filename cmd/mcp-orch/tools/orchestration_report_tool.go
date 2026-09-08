package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	// 默认等待窗口复用 RPC 超时，避免报告轮询比一次工具调用活得更久。
	defaultAgentReportWaitTimeout = platformconfig.RPCRequestTimeout
	agentReportPollInterval       = 50 * time.Millisecond
)

// GetAgentReportInput 是 get_agent_report 工具的单 agent 入参。
// wait=true 必须显式提供 AfterReportSeq；首次为 0，后续为已观察到的持久化序号。
type GetAgentReportInput struct {
	AgentID        string `json:"agent_id"`
	Pos            string `json:"pos,omitempty"`
	Wait           *bool  `json:"wait,omitempty"`
	RequesterID    string `json:"requester_id,omitempty"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
	AfterReportSeq *int64 `json:"after_report_seq,omitempty"`
}

// getAgentReportsInput 是批量报告读取/等待工具的入参。
type getAgentReportsInput struct {
	AgentIDs              []string         `json:"agent_ids"`
	Wait                  *bool            `json:"wait,omitempty"`
	TimeoutMS             int              `json:"timeout_ms,omitempty"`
	AfterReportSeqByAgent map[string]int64 `json:"after_report_seq_by_agent,omitempty"`
}

// agentReportsRequest 是校验后的批量报告请求，agent id 已 trim 且 timeout 已转成 duration。
type agentReportsRequest struct {
	agentIDs              []string
	wait                  bool
	timeout               time.Duration
	afterReportSeqByAgent map[string]int64
}

// agentReportsResult 是批量报告工具统一返回 envelope。
type agentReportsResult struct {
	Status    string            `json:"status"`
	Results   []agentReportItem `json:"results"`
	Completed int               `json:"completed"`
	Pending   int               `json:"pending"`
	TimedOut  bool              `json:"timed_out"`
}

// agentReportItem 表示单个 agent 报告读取结果；OK=false 时 Error 带可展示原因。
type agentReportItem struct {
	AgentID   string    `json:"agent_id"`
	State     string    `json:"state"`
	Report    string    `json:"report"`
	ReportSeq int64     `json:"report_seq"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error"`
}

type agentReportPort interface {
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
	RememberReportRequest(ctx context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error)
}

// HandleGetAgentReport 读取单 agent 报告；wait=true 时进入 after_seq 感知的轮询路径。
func HandleGetAgentReport(reports agentReportPort) ToolHandler {
	return makeHandler(reports, "agent report port", func(ctx context.Context, in GetAgentReportInput) (any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		if !agentReportShouldWait(in) {
			return reports.GetReport(ctx, agentID)
		}
		if in.AfterReportSeq == nil {
			return nil, fmt.Errorf("after_report_seq is required when wait=true; use 0 for the first report or the last observed report_seq")
		}
		return waitForAgentReport(ctx, reports, in, agentID)
	})
}

// HandleGetAgentReports 批量读取或等待多个代理报告，只支持 all wait 语义。
func HandleGetAgentReports(reports agentReportPort) ToolHandler {
	return makeHandler(reports, "agent report port", func(ctx context.Context, in getAgentReportsInput) (any, error) {
		req, err := validateAgentReportsInput(in)
		if err != nil {
			return nil, err
		}
		if !req.wait {
			return readAgentReportsSnapshot(ctx, reports, req), nil
		}
		return waitForAgentReports(ctx, reports, req), nil
	})
}

// agentReportShouldWait 判断单 agent 报告工具是否进入轮询等待路径。
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
	agentIDs, err := normalizeAgentReportTargets(in.AgentIDs)
	if err != nil {
		return agentReportsRequest{}, err
	}
	afterByAgent, err := normalizeAgentReportCursors(in.AfterReportSeqByAgent)
	if err != nil {
		return agentReportsRequest{}, err
	}
	wait := in.Wait != nil && *in.Wait
	if wait {
		if err := requireAgentReportCursors(agentIDs, afterByAgent); err != nil {
			return agentReportsRequest{}, err
		}
	}
	return agentReportsRequest{
		agentIDs:              agentIDs,
		wait:                  wait,
		timeout:               timeout,
		afterReportSeqByAgent: afterByAgent,
	}, nil
}

// normalizeAgentReportTargets 规范化批量目标并拒绝同一身份重复计数。
func normalizeAgentReportTargets(rawIDs []string) ([]string, error) {
	ids := make([]string, 0, len(rawIDs))
	seen := make(map[string]bool, len(rawIDs))
	for _, raw := range rawIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("agent_ids must not contain empty agent id")
		}
		if seen[id] {
			return nil, fmt.Errorf("agent_ids contains duplicate normalized agent %q", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// normalizeAgentReportCursors 校验并规范化调用方提供的游标键，拒绝重复身份。
func normalizeAgentReportCursors(cursors map[string]int64) (map[string]int64, error) {
	afterByAgent := make(map[string]int64, len(cursors))
	for rawAgentID, seq := range cursors {
		agentID := strings.TrimSpace(rawAgentID)
		if agentID == "" {
			return nil, fmt.Errorf("after_report_seq_by_agent must not contain empty agent id")
		}
		if seq < 0 {
			return nil, fmt.Errorf("after_report_seq_by_agent values must be non-negative")
		}
		if _, exists := afterByAgent[agentID]; exists {
			return nil, fmt.Errorf("after_report_seq_by_agent contains duplicate normalized agent id %q", agentID)
		}
		afterByAgent[agentID] = seq
	}
	return afterByAgent, nil
}

// requireAgentReportCursors 要求批量等待游标完整覆盖目标且没有多余键。
func requireAgentReportCursors(agentIDs []string, afterByAgent map[string]int64) error {
	targets := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		targets[agentID] = struct{}{}
		if _, exists := afterByAgent[agentID]; !exists {
			return fmt.Errorf("after_report_seq_by_agent is required for agent %q when wait=true; use 0 for the first report or the last observed report_seq", agentID)
		}
	}
	for agentID := range afterByAgent {
		if _, exists := targets[agentID]; !exists {
			return fmt.Errorf("after_report_seq_by_agent contains unrequested agent %q", agentID)
		}
	}
	return nil
}

// readAgentReportsSnapshot 读取批量报告即时快照，不等待 pending agent 完成。
func readAgentReportsSnapshot(ctx context.Context, reports agentReportPort, req agentReportsRequest) agentReportsResult {
	items := make([]agentReportItem, len(req.agentIDs))
	completed := 0
	for i, agentID := range req.agentIDs {
		report, err := reports.GetReport(ctx, agentID)
		items[i] = agentReportItemFromResult(agentID, report, err)
		if items[i].OK {
			completed++
		}
	}
	return finishAgentReportsResult(items, completed, 0, false)
}

// waitForAgentReports 用一个共享 timeout 轮询所有 pending agent，避免逐个 wait 放大总耗时。
func waitForAgentReports(ctx context.Context, reports agentReportPort, req agentReportsRequest) agentReportsResult {
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
			completedNow, done, timedOut := pollPendingAgentReport(waitCtx, reports, req, i, items, latest)
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
	reports agentReportPort,
	req agentReportsRequest,
	i int,
	items []agentReportItem,
	latest []contract.AgentReportResult,
) (completed bool, done bool, timedOut bool) {
	agentID := req.agentIDs[i]
	report, err := reports.GetReport(ctx, agentID)
	latest[i] = report
	if err != nil {
		if ctx.Err() != nil {
			return false, false, true
		}
		items[i] = agentReportItemFromResult(agentID, report, err)
		return false, true, false
	}
	afterSeq := req.afterReportSeqByAgent[agentID]
	completedReport, ok, terminalErr := completedBatchAgentReport(report, afterSeq, agentID)
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

// completedBatchAgentReport 判定批量等待中的单个报告是否已经满足完成条件。
// after_seq fence 未越过时继续等待；若 agent 已终态但没有新报告则返回 item 级错误。
func completedBatchAgentReport(result contract.AgentReportResult, afterSeq int64, agentID string) (contract.AgentReportResult, bool, error) {
	if err := terminalAgentReportNoNewReportError(result, &afterSeq, agentID); err != nil {
		return contract.AgentReportResult{}, false, err
	}
	completed, ok := completedAgentReport(result, &afterSeq)
	return completed, ok, nil
}

// agentReportItemFromResult 把服务层报告和错误折叠成批量工具的单项输出。
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

// waitForAgentReport 注册可选 requester 后轮询单 agent 报告。
// requester 只用于服务端唤醒/关联，等待判定仍由 after_report_seq 和报告内容决定。
func waitForAgentReport(ctx context.Context, reports agentReportPort, in GetAgentReportInput, agentID string) (any, error) {
	timeout, requesterID, err := validateAgentReportWait(ctx, in, agentID)
	if err != nil {
		return nil, err
	}
	if requesterID != "" {
		if _, err := reports.RememberReportRequest(ctx, contract.RememberReportRequest{AgentID: agentID, RequesterID: requesterID}); err != nil {
			if !agentReportWaitableError(err) {
				return nil, err
			}
		}
	}
	return pollAgentReport(ctx, reports, agentID, timeout, in.AfterReportSeq)
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

// reportWaitRequester 优先使用显式 requester_id，缺省时从可信 MCP scope 推导。
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

// pollAgentReport 按固定间隔读取报告，直到出现新报告、终态无新报告或超时。
func pollAgentReport(
	ctx context.Context,
	reports agentReportPort,
	agentID string,
	timeout time.Duration,
	afterReportSeq *int64,
) (contract.AgentReportResult, error) {
	waitCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(agentReportPollInterval)
	defer ticker.Stop()
	for {
		result, err := reports.GetReport(waitCtx, agentID)
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

// completedAgentReport 判定单次读取结果是否可作为完成报告返回。
// 只返回真实报告文本；终态空报告由终态错误判定显式阻断。
func completedAgentReport(result contract.AgentReportResult, afterReportSeq *int64) (contract.AgentReportResult, bool) {
	if afterReportSeq != nil && result.ReportSeq <= *afterReportSeq {
		return contract.AgentReportResult{}, false
	}
	if strings.TrimSpace(result.Report) != "" {
		return result, true
	}
	return contract.AgentReportResult{}, false
}

// terminalAgentReportState 判断没有新报告时是否应停止等待并返回终态错误。
func terminalAgentReportState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "stopped":
		return true
	default:
		return false
	}
}

// terminalAgentReportNoNewReportError 在终态报告缺失或 report_seq 未推进时生成明确错误。
func terminalAgentReportNoNewReportError(result contract.AgentReportResult, afterReportSeq *int64, agentID string) error {
	if !terminalAgentReportState(result.State) {
		return nil
	}
	if strings.TrimSpace(result.Report) == "" {
		return fmt.Errorf("agent %q ended in state %q without producing a turn report", agentID, result.State)
	}
	if afterReportSeq == nil || result.ReportSeq > *afterReportSeq {
		return nil
	}
	return fmt.Errorf("agent %q ended in state %q without a report after report_seq %d", agentID, result.State, *afterReportSeq)
}

// agentReportPollError 区分可等待错误、真实服务错误和等待上下文超时。
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

// agentReportWaitTimeoutError 生成包含 after_seq fence 的报告等待超时错误。
func agentReportWaitTimeoutError(ctx context.Context, timeout time.Duration, agentID string, afterReportSeq *int64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if afterReportSeq != nil {
		return fmt.Errorf("timed out waiting %s for report from agent %q after report_seq %d", timeout, agentID, *afterReportSeq)
	}
	return fmt.Errorf("timed out waiting %s for report from agent %q", timeout, agentID)
}

// agentReportWaitableError 表示报告等待期间可继续轮询的临时错误。
func agentReportWaitableError(err error) bool {
	return errors.Is(err, contract.ErrAgentNotFound)
}
