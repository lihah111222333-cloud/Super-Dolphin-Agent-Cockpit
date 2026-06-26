package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportgc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportstore"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// GetState 返回 agent 当前状态；runtime 缺失时回退到持久化 thread 快照。
func (s *service) GetState(ctx context.Context, agentID string) (AgentStateResult, error) {
	var result AgentStateResult
	err := s.withAgentReadLockedByAgentID(ctx, agentID, func(agent *agentRuntime) error {
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

// GetReport 返回 agent 最新 report；runtime 缺失时读取磁盘持久化 report。
func (s *service) GetReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	var result AgentReportResult
	err := s.withAgentReadLockedByAgentID(ctx, agentID, func(agent *agentRuntime) error {
		result = agentReportLocked(agent)
		return nil
	})
	if errors.Is(err, errAgentNotFound) {
		return s.persistedAgentReport(ctx, agentID)
	}
	return result, err
}

// persistedAgentReport 从持久化 thread 快照定位 report 文件，并恢复展示所需的 seq 和时间戳。
func (s *service) persistedAgentReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	snapshot, err := s.persistedAgentSnapshot(ctx, agentID)
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

// RememberReportRequest 记录哪个 agent 请求了目标 agent 的最终 report。
// requester 去重在锁内完成，避免同一 requester 被重复唤醒。
func (s *service) RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error) {
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
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		rememberReportRequesterLocked(ctx, agent, requesterID)
		result = RememberReportRequestResult{Success: true, AgentID: agent.id, RequesterID: requesterID}
		return nil
	})
	return result, err
}

// HandleReportEvent 接收 provider/hook report 事件并更新 runtime 或持久化 fallback。
func (s *service) HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error) {
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
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		var applyErr error
		result, applyErr = s.applyReportEventLocked(ctx, agent, eventType, event.EventData, report)
		return applyErr
	})
	if fallback, ok := s.reportEventFallbackResult(ctx, agentID, eventType, report, err); ok {
		return fallback, nil
	}
	return result, err
}

// reportEventFallbackResult 在 runtime 已丢失但事件仍带 report 或 stop 信号时尝试持久化收口。
func (s *service) reportEventFallbackResult(ctx context.Context, agentID, eventType, report string, runtimeErr error) (ReportEventResult, bool) {
	if !errors.Is(runtimeErr, errAgentNotFound) {
		return ReportEventResult{}, false
	}
	if strings.TrimSpace(report) == "" && !isRuntimeLossStopEventType(eventType) {
		return ReportEventResult{}, false
	}
	result, ok := s.applyReportEventWithoutRuntime(ctx, agentID, eventType, report)
	if !ok {
		return ReportEventResult{}, false
	}
	return result, true
}

// applyReportEventWithoutRuntime 在 runtime 丢失时把非空 report 写回单文件，并保留 seq 水位。
func (s *service) applyReportEventWithoutRuntime(ctx context.Context, agentID, eventType, report string) (ReportEventResult, bool) {
	snapshot, err := s.persistedAgentSnapshot(ctx, agentID)
	if err != nil {
		return ReportEventResult{}, false
	}
	result := ReportEventResult{Success: true, AgentID: agentID, EventType: eventType, Report: report}
	wroteReport := strings.TrimSpace(report) != ""
	if wroteReport {
		if strings.TrimSpace(snapshot.Cwd) == "" {
			return ReportEventResult{}, false
		}
		record, err := s.nextPersistedReportRecord(ctx, snapshot, report)
		if err != nil {
			return ReportEventResult{}, false
		}
		if s.persistAgentReportFileAndGC(ctx, record) != nil {
			return ReportEventResult{}, false
		}
		result.ReportSeq = record.ReportSeq
		result.UpdatedAt = record.UpdatedAt
	}
	if s.agentThreads == nil || !isRuntimeLossStopEventType(eventType) {
		return result, wroteReport
	}
	threadID := strings.TrimSpace(platformshared.FirstNonEmpty(snapshot.ThreadID, agentID))
	err = s.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{ThreadID: threadID, Status: "stopped", UpdatedAt: resolveEventTime(ctx).Unix()})
	return result, err == nil
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

// persistAgentReportFileAndGC 原子持久化 report 文件，并清理同 cwd 下已停止 agent 的过期 report。
func (s *service) persistAgentReportFileAndGC(ctx context.Context, record reportstore.Record) error {
	if strings.TrimSpace(record.Report) == "" || strings.TrimSpace(record.Cwd) == "" {
		return nil
	}
	if err := reportstore.Persist(record); err != nil {
		return err
	}
	if s == nil || s.agentThreads == nil {
		return nil
	}
	threads, err := s.agentThreads.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("agent report gc list threads: %w", err)
	}
	return reportgc.Collect(record.Cwd, threads, func(thread PersistedThread) (string, string, string) {
		return persistedThreadAgentID(thread), thread.Cwd, thread.Status
	}, time.Now(), s.logger)
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

// nextPersistedReportRecord 基于磁盘已有 seq 生成下一版持久化 report 记录。
func (s *service) nextPersistedReportRecord(ctx context.Context, snapshot AgentSnapshot, report string) (reportstore.Record, error) {
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
