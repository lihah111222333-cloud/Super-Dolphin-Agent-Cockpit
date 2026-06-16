package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/reportgc"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/reportstore"
)

// GetState 读取状态。
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

// GetReport 读取report。
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

func (s *service) persistedAgentReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	snapshot, err := s.persistedAgentSnapshot(ctx, agentID)
	if err != nil {
		return AgentReportResult{}, err
	}
	report, err := reportstore.ReadPersisted(agentReportFileRecordFromSnapshot(snapshot))
	if err != nil {
		if errors.Is(err, reportstore.ErrNotFound) {
			return AgentReportResult{AgentID: snapshot.AgentID, State: snapshot.State}, fmt.Errorf("%w: persisted report missing for %s", errAgentNotFound, snapshot.AgentID)
		}
		return AgentReportResult{}, err
	}
	return AgentReportResult{AgentID: snapshot.AgentID, Report: normalizeDisplayReportText(report), State: snapshot.State}, nil
}

// RememberReportRequest 处理rememberreport请求。
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

// HandleReportEvent 处理report事件。
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

func (s *service) reportEventFallbackResult(ctx context.Context, agentID, eventType, report string, runtimeErr error) (ReportEventResult, bool) {
	if !errors.Is(runtimeErr, errAgentNotFound) {
		return ReportEventResult{}, false
	}
	if strings.TrimSpace(report) == "" && !isRuntimeLossStopEventType(eventType) {
		return ReportEventResult{}, false
	}
	if !s.applyReportEventWithoutRuntime(ctx, agentID, eventType, report) {
		return ReportEventResult{}, false
	}
	return ReportEventResult{
		Success:   true,
		AgentID:   agentID,
		EventType: eventType,
		Report:    report,
	}, true
}

// applyReportEventWithoutRuntime 应用report事件without运行时。
func (s *service) applyReportEventWithoutRuntime(ctx context.Context, agentID, eventType, report string) bool {
	snapshot, err := s.persistedAgentSnapshot(ctx, agentID)
	if err != nil {
		return false
	}
	wroteReport := strings.TrimSpace(report) != ""
	if wroteReport {
		if strings.TrimSpace(snapshot.Cwd) == "" {
			return false
		}
		record := agentReportFileRecordFromSnapshot(snapshot)
		record.Report = report
		if s.persistAgentReportFileAndGC(ctx, record) != nil {
			return false
		}
	}
	if s.agentThreads == nil || !isRuntimeLossStopEventType(eventType) {
		return wroteReport
	}
	threadID := strings.TrimSpace(platformshared.FirstNonEmpty(snapshot.ThreadID, agentID))
	err = s.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{ThreadID: threadID, Status: "stopped", UpdatedAt: resolveEventTime(ctx).Unix()})
	return err == nil
}

func setReportLocked(ctx context.Context, agent *agentRuntime, report string) {
	agent.lastReport = strings.TrimSpace(report)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
}

// persistAgentReportFileAndGC 持久化代理report文件gc。
func (s *service) persistAgentReportFileAndGC(ctx context.Context, record reportstore.Record) error {
	if err := reportstore.Persist(record); err != nil || strings.TrimSpace(record.Report) == "" || strings.TrimSpace(record.Cwd) == "" || s == nil || s.agentThreads == nil {
		return err
	}
	threads, err := s.agentThreads.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("agent report gc list threads: %w", err)
	}
	return reportgc.Collect(record.Cwd, threads, func(thread PersistedThread) (string, string, string) {
		return persistedThreadAgentID(thread), thread.Cwd, thread.Status
	}, time.Now(), s.logger)
}

func agentReportFileRecordFromRuntime(agent *agentRuntime) reportstore.Record {
	if agent == nil {
		return reportstore.Record{}
	}
	return reportstore.Record{
		AgentID: agent.id,
		Name:    agent.name,
		Cwd:     agent.cwd,
		Report:  agent.lastReport,
	}
}

func agentReportFileRecordFromSnapshot(snapshot AgentSnapshot) reportstore.Record {
	return reportstore.Record{
		AgentID: snapshot.AgentID,
		Name:    snapshot.Name,
		Cwd:     snapshot.Cwd,
		Report:  snapshot.LastReport,
	}
}

// normalizeDisplayReportText 规范化显示report文本。
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

// isSimpleDisplayReportToken 判断simple显示report令牌是否可用。
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

func agentReportLocked(agent *agentRuntime) AgentReportResult {
	requesters := append([]string(nil), agent.reportRequesters...)
	var metadata *AgentReportMetadata
	if len(requesters) > 0 {
		metadata = &AgentReportMetadata{RequesterIDs: requesters}
	}
	return AgentReportResult{AgentID: agent.id, Report: normalizeDisplayReportText(agent.lastReport), State: string(agent.state), Metadata: metadata}
}

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

func drainReportRequestersLocked(ctx context.Context, agent *agentRuntime) []string {
	requesters := append([]string(nil), agent.reportRequesters...)
	agent.reportRequesters = nil
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return requesters
}

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

// reportTextFromPayload 从载荷报告文本。
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

func firstReportTextPayloadString(payload map[string]any) string {
	return platformshared.FirstPayloadString(payload, "report", "summary", "uiText", "text", "message", "output", "result")
}

type reportEventTypeFlag uint8

const (
	reportEventTypeTerminal reportEventTypeFlag = 1 << iota
	reportEventTypeRuntimeLossStop
)

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

func isTerminalReportEventType(eventType string) bool {
	return reportEventTypeFlags(eventType)&reportEventTypeTerminal != 0
}

func isTerminalThreadStatusValue(status string) bool {
	switch status {
	case "error", "idle", "not_loaded", "notloaded", "system_error", "systemerror":
		return true
	default:
		return false
	}
}

func isRuntimeLossStopEventType(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	return reportEventTypeFlags(eventType)&reportEventTypeRuntimeLossStop != 0
}

func isTerminalReportEvent(eventType string, raw json.RawMessage) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == ReportEventTypeThreadStatusChanged {
		return isTerminalThreadStatus(raw)
	}
	return isTerminalReportEventType(eventType)
}

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
