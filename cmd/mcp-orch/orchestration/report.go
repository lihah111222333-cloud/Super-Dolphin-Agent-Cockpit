package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/reportgc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/reportstore"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// GetState 返回 agent 当前状态；runtime 缺失时回退到持久化 thread 快照。
func (s *service) GetState(ctx context.Context, agentID string) (AgentStateResult, error) {
	var result AgentStateResult
	err := s.registry.withAgentReadLockedByAgentID(ctx, agentID, func(agent *agentRuntime) error {
		result = AgentStateResult{AgentID: agent.id, State: string(agent.state)}
		return nil
	})
	if errors.Is(err, errAgentNotFound) {
		snapshot, snapshotErr := s.persistedAgentSnapshot(ctx, agentID)
		if snapshotErr != nil {
			return AgentStateResult{}, snapshotErr
		}
		return AgentStateResult{AgentID: snapshot.AgentID, State: snapshot.State}, nil
	}
	return result, err
}

type reportControllerDeps struct {
	registry     *agentRegistry
	agentThreads AgentThreadStore
	logger       *slog.Logger
}

// reportController owns report reads, report events, requester drain, fallback reports, and report file persistence.
type reportController struct {
	registry     *agentRegistry
	agentThreads AgentThreadStore
	logger       *slog.Logger
}

type reportApplier interface {
	applyReportEventLocked(ctx context.Context, agent *agentRuntime, eventType string, data json.RawMessage, report string) (ReportEventResult, error)
}

func newReportController(deps reportControllerDeps) *reportController {
	return &reportController{
		registry:     deps.registry,
		agentThreads: deps.agentThreads,
		logger:       deps.logger,
	}
}

// GetReport 返回 agent 最新 report；runtime 缺失时读取磁盘持久化 report。
func (s *service) GetReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	reports, err := s.configuredReportController()
	if err != nil {
		return AgentReportResult{}, err
	}
	return reports.GetReport(ctx, agentID)
}

// RememberReportRequest 记录哪个 agent 请求了目标 agent 的最终 report。
func (s *service) RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error) {
	reports, err := s.configuredReportController()
	if err != nil {
		return RememberReportRequestResult{}, err
	}
	return reports.RememberReportRequest(ctx, req)
}

// HandleReportEvent 接收 provider/hook report 事件并更新 runtime 或持久化 fallback。
func (s *service) HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error) {
	reports, err := s.configuredReportController()
	if err != nil {
		return ReportEventResult{}, err
	}
	return reports.HandleReportEvent(ctx, event)
}

// configuredReportController 返回已接线的 report controller，缺失时立即报错。
func (s *service) configuredReportController() (*reportController, error) {
	if s == nil || s.reports == nil {
		return nil, errors.New("report controller is not configured")
	}
	if s.lifecycle != nil && s.reports.agentThreads == nil && s.lifecycle.agentThreads != nil {
		s.reports.agentThreads = s.lifecycle.agentThreads
	}
	return s.reports, nil
}

func (s *service) configuredReportApplier() (reportApplier, error) {
	return s.configuredReportController()
}

func (s *service) setStateChangedFallbackReportLocked(ctx context.Context, agent *agentRuntime, nextState string) {
	reports, err := s.configuredReportController()
	if err != nil {
		loggerOrDefault(nil).Warn("orchestration: state-change fallback report controller unavailable",
			"agent_id", eventAgentID(agent), "state", nextState, "error", err)
		return
	}
	reports.setStateChangedFallbackReportLocked(ctx, agent, nextState)
}

func (s *service) setStoppedFallbackReportLocked(ctx context.Context, agent *agentRuntime) {
	reports, err := s.configuredReportController()
	if err != nil {
		loggerOrDefault(nil).Warn("orchestration: stopped fallback report controller unavailable",
			"agent_id", eventAgentID(agent), "error", err)
		return
	}
	reports.setStoppedFallbackReportLocked(ctx, agent)
}

// GetReport 读取内存 runtime report；runtime 缺失时只按 agent_id 回读持久化 report 文件。
func (c *reportController) GetReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	var result AgentReportResult
	err := c.withAgentReadLockedByAgentID(ctx, agentID, func(agent *agentRuntime) error {
		result = agentReportLocked(agent)
		return nil
	})
	if errors.Is(err, errAgentNotFound) {
		return c.persistedAgentReport(ctx, agentID)
	}
	return result, err
}

// persistedAgentReport 从持久化 thread 快照定位 report 文件，并恢复展示所需的 seq 和时间戳。
func (c *reportController) persistedAgentReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	snapshot, err := persistedAgentSnapshotFromStore(ctx, c.agentThreads, agentID)
	if err != nil {
		return AgentReportResult{}, err
	}
	report, err := reportstore.ReadPersistedRecord(agentReportFileRecordFromSnapshot(snapshot))
	if err != nil {
		if errors.Is(err, reportstore.ErrNotFound) {
			return AgentReportResult{AgentID: snapshot.AgentID, State: snapshot.State}, fmt.Errorf("%w: persisted report missing for %s", errAgentNotFound, snapshot.AgentID)
		}
		return AgentReportResult{}, err
	}
	return AgentReportResult{
		AgentID:   snapshot.AgentID,
		Report:    normalizeDisplayReportText(report.Report),
		ReportSeq: report.ReportSeq,
		UpdatedAt: report.UpdatedAt,
		State:     snapshot.State,
	}, nil
}

// RememberReportRequest 在 registry 锁内记录等待最终 report 的 requester，并保持大小写不敏感去重。
func (c *reportController) RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error) {
	agentID := strings.TrimSpace(req.AgentID)
	requesterID := strings.TrimSpace(req.RequesterID)
	if agentID == "" {
		return RememberReportRequestResult{}, errors.New("agent id is required")
	}
	if requesterID == "" {
		return RememberReportRequestResult{}, errors.New("requester id is required")
	}
	if strings.EqualFold(agentID, requesterID) {
		return RememberReportRequestResult{}, errors.New("agent id and requester id must differ")
	}
	result := RememberReportRequestResult{}
	err := c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		rememberReportRequesterLocked(ctx, agent, requesterID)
		result = RememberReportRequestResult{Success: true, AgentID: agent.id, RequesterID: requesterID}
		return nil
	})
	return result, err
}

// HandleReportEvent 归一化 provider/hook report 事件，并在 runtime 缺失时走持久化兜底路径。
func (c *reportController) HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error) {
	agentID := strings.TrimSpace(event.AgentID)
	if agentID == "" {
		return ReportEventResult{}, errors.New("agent id is required")
	}
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		return ReportEventResult{}, errors.New("event type is required")
	}
	report := strings.TrimSpace(event.Report)
	if report == "" {
		report = extractReportFromEventData(event.EventData)
	}
	result := ReportEventResult{}
	err := c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		var applyErr error
		result, applyErr = c.applyReportEventLocked(ctx, agent, eventType, event.EventData, report)
		return applyErr
	})
	if fallback, ok := c.reportEventFallbackResult(ctx, agentID, eventType, report, err); ok {
		return fallback, nil
	}
	return result, err
}

// reportEventFallbackResult 在 runtime 已丢失但事件仍带 report 或 stop 信号时尝试持久化收口。
func (c *reportController) reportEventFallbackResult(ctx context.Context, agentID, eventType, report string, runtimeErr error) (ReportEventResult, bool) {
	if !errors.Is(runtimeErr, errAgentNotFound) {
		return ReportEventResult{}, false
	}
	if strings.TrimSpace(report) == "" && !isRuntimeLossStopEventType(eventType) {
		return ReportEventResult{}, false
	}
	result, ok := c.applyReportEventWithoutRuntime(ctx, agentID, eventType, report)
	if !ok {
		return ReportEventResult{}, false
	}
	return result, true
}

// applyReportEventWithoutRuntime 在 runtime 丢失时把非空 report 写回单文件，并保留 seq 水位。
func (c *reportController) applyReportEventWithoutRuntime(ctx context.Context, agentID, eventType, report string) (ReportEventResult, bool) {
	snapshot, err := persistedAgentSnapshotFromStore(ctx, c.agentThreads, agentID)
	if err != nil {
		return ReportEventResult{}, false
	}
	result := ReportEventResult{Success: true, AgentID: agentID, EventType: eventType, Report: report}
	wroteReport := strings.TrimSpace(report) != ""
	if wroteReport {
		if strings.TrimSpace(snapshot.Cwd) == "" {
			return ReportEventResult{}, false
		}
		record, err := c.nextPersistedReportRecord(ctx, snapshot, report)
		if err != nil {
			return ReportEventResult{}, false
		}
		if c.persistAgentReportFileAndGC(ctx, record) != nil {
			return ReportEventResult{}, false
		}
		result.ReportSeq = record.ReportSeq
		result.UpdatedAt = record.UpdatedAt
	}
	if c.agentThreads == nil || !isRuntimeLossStopEventType(eventType) {
		return result, wroteReport
	}
	threadID := strings.TrimSpace(platformshared.FirstNonEmpty(snapshot.ThreadID, agentID))
	err = c.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{ThreadID: threadID, Status: "stopped", UpdatedAt: resolveEventTime(ctx).Unix()})
	return result, err == nil
}

// setNoReportFallbackLocked 在持有 registry lock 时为无 report 的终态 agent 写入兜底说明。
func (c *reportController) setNoReportFallbackLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || strings.TrimSpace(agent.lastReport) != "" {
		return nil
	}
	setReportLocked(ctx, agent, noReportFallbackText(string(agent.state), agent.lastError))
	if err := c.persistAgentReportFileAndGC(ctx, agentReportFileRecordFromRuntime(agent)); err != nil {
		return err
	}
	drainReportRequestersLocked(ctx, agent)
	return nil
}

// applyReportEventLocked 应用 report 事件；调用方必须已持有 registry lock。
func (c *reportController) applyReportEventLocked(ctx context.Context, agent *agentRuntime, eventType string, data json.RawMessage, report string) (ReportEventResult, error) {
	terminal := isTerminalReportEvent(eventType, data)
	if report == "" && terminal && strings.TrimSpace(agent.lastReport) == "" {
		report = noReportFallbackText(string(agent.state), agent.lastError)
	}
	if report != "" {
		setReportLocked(ctx, agent, report)
		if err := c.persistAgentReportFileAndGC(ctx, agentReportFileRecordFromRuntime(agent)); err != nil {
			return ReportEventResult{}, err
		}
	}
	if report == "" {
		report = strings.TrimSpace(agent.lastReport)
	}
	notified := []string(nil)
	if report != "" || terminal {
		notified = drainReportRequestersLocked(ctx, agent)
	}
	return ReportEventResult{
		Success:              true,
		AgentID:              agent.id,
		EventType:            eventType,
		Report:               report,
		ReportSeq:            agent.lastReportSeq,
		UpdatedAt:            agent.lastReportUpdatedAt,
		NotifiedRequesterIDs: notified,
	}, nil
}

// setProcessExitFallbackReportLocked 在持有 registry lock 时为进程退出终态补写 report。
func (c *reportController) setProcessExitFallbackReportLocked(ctx context.Context, agent *agentRuntime, launchSeq uint64, shouldRecover bool) {
	if shouldRecover {
		return
	}
	if fallbackErr := c.setNoReportFallbackLocked(ctx, agent); fallbackErr != nil {
		c.warn("orchestration: process exit fallback report persist failed",
			"agent_id", agent.id, "launch_seq", launchSeq, "error", fallbackErr)
	}
}

// setStateChangedFallbackReportLocked 在持有 registry lock 时处理状态变更终态兜底 report。
func (c *reportController) setStateChangedFallbackReportLocked(ctx context.Context, agent *agentRuntime, nextState string) {
	if agent == nil || !terminalFailedOrStopped(agent.state) || !terminalFailedOrStoppedString(nextState) {
		return
	}
	if err := c.setNoReportFallbackLocked(ctx, agent); err != nil {
		c.warn("orchestration: state-change fallback report persist failed",
			"agent_id", agent.id, "thread_id", agent.remoteThreadID, "state", nextState, "error", err)
	}
}

// setStoppedFallbackReportLocked 在持有 registry lock 时处理 stopped hook 兜底 report。
func (c *reportController) setStoppedFallbackReportLocked(ctx context.Context, agent *agentRuntime) {
	if err := c.setNoReportFallbackLocked(ctx, agent); err != nil {
		c.warn("orchestration: stopped hook fallback report persist failed",
			"agent_id", agent.id, "thread_id", agent.remoteThreadID, "error", err)
	}
}

// persistAgentReportFileAndGC 原子写入 report 文件，并基于持久化 thread 快照做保守 GC。
func (c *reportController) persistAgentReportFileAndGC(ctx context.Context, record reportstore.Record) error {
	if strings.TrimSpace(record.Report) == "" || strings.TrimSpace(record.Cwd) == "" {
		return nil
	}
	if err := reportstore.Persist(record); err != nil {
		return err
	}
	if c == nil || c.agentThreads == nil {
		return nil
	}
	threads, err := c.agentThreads.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("agent report gc list threads: %w", err)
	}
	return reportgc.Collect(record.Cwd, threads, func(thread PersistedThread) (string, string, string) {
		return persistedThreadAgentID(thread), thread.Cwd, thread.Status
	}, time.Now(), c.logger)
}

// nextPersistedReportRecord 基于磁盘已有 seq 生成下一版持久化 report 记录。
func (c *reportController) nextPersistedReportRecord(ctx context.Context, snapshot AgentSnapshot, report string) (reportstore.Record, error) {
	record := agentReportFileRecordFromSnapshot(snapshot)
	persisted, err := reportstore.ReadPersistedRecord(record)
	if err != nil && !errors.Is(err, reportstore.ErrNotFound) {
		return reportstore.Record{}, err
	}
	record.Report = report
	record.ReportSeq = persisted.ReportSeq + 1
	record.UpdatedAt = resolveEventTime(ctx)
	return record, nil
}

func (c *reportController) withAgentLocked(agentID string, fn func(*agentRuntime) error) error {
	if c == nil || c.registry == nil {
		return errAgentNotFound
	}
	return c.registry.withAgentLocked(agentID, fn)
}

func (c *reportController) withAgentReadLockedByAgentID(ctx context.Context, agentID string, fn func(*agentRuntime) error) error {
	if c == nil || c.registry == nil {
		return errAgentNotFound
	}
	return c.registry.withAgentReadLockedByAgentID(ctx, agentID, fn)
}

func (c *reportController) warn(msg string, attrs ...any) {
	if c != nil && c.logger != nil {
		c.logger.Warn(msg, attrs...)
	}
}

// setReportLocked 在持有 agent 锁时更新内存 report 和 seq 水位。
func setReportLocked(ctx context.Context, agent *agentRuntime, report string) {
	report = strings.TrimSpace(report)
	if agent == nil || report == "" {
		return
	}
	updatedAt := resolveEventTime(ctx)
	agent.lastReport = report
	agent.lastReportSeq++
	agent.lastReportUpdatedAt = updatedAt
	agent.updatedAt = updatedAt
}

// agentReportFileRecordFromRuntime 从内存 runtime 快照提取 reportstore 写入记录。
func agentReportFileRecordFromRuntime(agent *agentRuntime) reportstore.Record {
	if agent == nil {
		return reportstore.Record{}
	}
	return reportstore.Record{
		AgentID:   agent.id,
		Name:      agent.name,
		Cwd:       agent.cwd,
		Report:    agent.lastReport,
		ReportSeq: agent.lastReportSeq,
		UpdatedAt: agent.lastReportUpdatedAt,
	}
}

// agentReportFileRecordFromSnapshot 从持久化 snapshot 提取 reportstore 读取定位信息。
func agentReportFileRecordFromSnapshot(snapshot AgentSnapshot) reportstore.Record {
	return reportstore.Record{
		AgentID: snapshot.AgentID,
		Name:    snapshot.Name,
		Cwd:     snapshot.Cwd,
		Report:  snapshot.LastReport,
	}
}

// normalizeDisplayReportText 清理 report 换行和空白；短 token 多行会折叠为一行展示。
func normalizeDisplayReportText(raw string) string {
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n"))
	if text == "" {
		return ""
	}
	lines, trimmed, nonEmpty, prevBlank := strings.Split(text, "\n"), make([]string, 0), make([]string, 0), false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(trimmed) == 0 || prevBlank {
				continue
			}
			trimmed = append(trimmed, "")
			prevBlank = true
			continue
		}
		trimmed = append(trimmed, line)
		nonEmpty = append(nonEmpty, line)
		prevBlank = false
	}
	if len(trimmed) == 0 {
		return ""
	}
	if shouldCollapseDisplayReportLines(nonEmpty) {
		return strings.Join(nonEmpty, " ")
	}
	return strings.Join(trimmed, "\n")
}

// shouldCollapseDisplayReportLines 判断多行短 token 是否更适合折叠展示。
func shouldCollapseDisplayReportLines(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	for _, line := range lines {
		if !isSimpleDisplayReportToken(line) {
			return false
		}
	}
	return true
}

// isSimpleDisplayReportToken 判断单行是否只是短 token，而不是自然语言段落。
func isSimpleDisplayReportToken(line string) bool {
	runes := []rune(strings.TrimSpace(line))
	if len(runes) == 0 || len(runes) > 24 {
		return false
	}
	for _, r := range runes {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case strings.ContainsRune("._-+/=", r):
		default:
			return false
		}
	}
	return true
}

// agentReportLocked 在持锁状态下组装 report 响应，并复制 requester 切片防止外部修改。
func agentReportLocked(agent *agentRuntime) AgentReportResult {
	requesters := append([]string(nil), agent.reportRequesters...)
	var metadata *AgentReportMetadata
	if len(requesters) > 0 {
		metadata = &AgentReportMetadata{RequesterIDs: requesters}
	}
	return AgentReportResult{
		AgentID:   agent.id,
		Report:    normalizeDisplayReportText(agent.lastReport),
		ReportSeq: agent.lastReportSeq,
		UpdatedAt: agent.lastReportUpdatedAt,
		State:     string(agent.state),
		Metadata:  metadata,
	}
}

// rememberReportRequesterLocked 在持锁状态下记录 requester，大小写不敏感地去重。
func rememberReportRequesterLocked(ctx context.Context, agent *agentRuntime, requesterID string) {
	for _, existing := range agent.reportRequesters {
		if strings.EqualFold(existing, requesterID) {
			agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
			return
		}
	}
	agent.reportRequesters = append(agent.reportRequesters, requesterID)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
}

// drainReportRequestersLocked 取出并清空等待 report 的 requester 列表。
func drainReportRequestersLocked(ctx context.Context, agent *agentRuntime) []string {
	requesters := append([]string(nil), agent.reportRequesters...)
	agent.reportRequesters = nil
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return requesters
}

// turnCompletedReportText 从 turn.completed 事件中提取适合展示的 report 文本。
func turnCompletedReportText(ev turndto.TurnCompleted) string {
	if text := platformshared.FirstTrimmed(ev.Result, ev.Summary, ev.Message); text != "" {
		return text
	}
	if ev.Success {
		return ""
	}
	if detail := platformshared.FirstTrimmed(ev.Error, ev.Reason, ev.StopReason); detail != "" {
		return "turn failed: " + detail
	}
	return "turn failed without detail"
}

// extractReportFromEventData 从 hook event JSON 里递归提取 report/summary/text 字段。
func extractReportFromEventData(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return reportTextFromPayload(payload)
}

// reportTextFromPayload 在事件 payload 和常见嵌套字段中查找可展示 report。
func reportTextFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if report := firstReportTextPayloadString(payload); report != "" {
		return report
	}
	for _, key := range []string{"item", "payload"} {
		if nested, ok := payload[key].(map[string]any); ok {
			if report := reportTextFromPayload(nested); report != "" {
				return report
			}
		}
	}
	return ""
}

// firstReportTextPayloadString 按 provider 常见字段优先级提取第一段 report 文本。
func firstReportTextPayloadString(payload map[string]any) string {
	return platformshared.FirstPayloadString(payload, "report", "summary", "uiText", "text", "message", "output", "result")
}

// reportEventTypeFlag 标记 report event 是否代表终态或 runtime 丢失。
type reportEventTypeFlag uint8

// report event 分类位。
const (
	reportEventTypeTerminal reportEventTypeFlag = 1 << iota
	reportEventTypeRuntimeLossStop
)

// reportEventTypeFlags 将多种 provider 事件命名归一到终态和 runtime-loss 标记。
func reportEventTypeFlags(eventType string) reportEventTypeFlag {
	switch eventType {
	case "connection.dead",
		"connection_dead",
		"error",
		"shutdown.complete",
		"shutdown_complete",
		"stream.error",
		"stream_error",
		"turn.aborted",
		"turn_aborted":
		return reportEventTypeTerminal | reportEventTypeRuntimeLossStop
	case "agent/event/task_complete",
		"completed",
		"completion",
		"idle",
		"turn.completed",
		"turn/completed",
		"turn_complete":
		return reportEventTypeTerminal
	case "turn/aborted":
		return reportEventTypeRuntimeLossStop
	default:
		return 0
	}
}

// isTerminalReportEventType 判断 event type 是否表示 turn/report 已终结。
func isTerminalReportEventType(eventType string) bool {
	return reportEventTypeFlags(eventType)&reportEventTypeTerminal != 0
}

// isTerminalThreadStatusValue 判断 thread status 是否已经进入终态。
func isTerminalThreadStatusValue(status string) bool {
	switch status {
	case "error", "idle", "not_loaded", "notloaded", "system_error", "systemerror":
		return true
	default:
		return false
	}
}

// isRuntimeLossStopEventType 判断事件是否代表 runtime 断开并需要持久化 stopped。
func isRuntimeLossStopEventType(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	return reportEventTypeFlags(eventType)&reportEventTypeRuntimeLossStop != 0
}

// isTerminalReportEvent 结合事件类型和 thread status payload 判断 report 是否终态。
func isTerminalReportEvent(eventType string, raw json.RawMessage) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == ReportEventTypeThreadStatusChanged {
		return isTerminalThreadStatus(raw)
	}
	return isTerminalReportEventType(eventType)
}

// isTerminalThreadStatus 从 thread status event payload 中解析终态状态。
func isTerminalThreadStatus(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	statusValue, ok := payload["status"]
	if !ok {
		return false
	}
	status := platformshared.FirstPayloadString(map[string]any{"status": statusValue}, "status")
	if status == "" {
		return false
	}
	return isTerminalThreadStatusValue(strings.ToLower(status))
}
