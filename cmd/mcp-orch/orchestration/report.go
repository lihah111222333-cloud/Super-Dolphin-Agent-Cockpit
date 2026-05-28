package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	ReportMethodReportEvent            = "agent/reportEvent"
	ReportMethodRememberReportRequest  = "agent/rememberReportRequest"
	ReportEventTypeThreadStatusChanged = "thread/status/changed"
)

var terminalReportEventTypesList = []string{
	"agent/event/task_complete",
	"completed",
	"completion",
	"connection.dead",
	"connection_dead",
	"error",
	"idle",
	"shutdown.complete",
	"shutdown_complete",
	"stream.error",
	"stream_error",
	"turn.completed",
	"turn/completed",
	"turn.aborted",
	"turn_aborted",
	"turn_complete",
}

var terminalThreadStatusesList = []string{
	"error",
	"idle",
	"not_loaded",
	"notloaded",
	"system_error",
	"systemerror",
}

var reportTextPayloadKeys = []string{"report", "summary", "uiText", "text", "message", "output", "result"}
var reportTextNestedKeys = []string{"item", "payload"}
var terminalReportEventTypes = buildStringSet(terminalReportEventTypesList)
var terminalThreadStatuses = buildStringSet(terminalThreadStatusesList)

func buildStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

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
	report, err := readPersistedAgentReportFile(agentReportFileRecordFromSnapshot(snapshot))
	if err != nil {
		if errors.Is(err, errAgentReportNotFound) {
			return AgentReportResult{AgentID: snapshot.AgentID, State: snapshot.State}, fmt.Errorf("%w: persisted report missing for %s", errAgentNotFound, snapshot.AgentID)
		}
		return AgentReportResult{}, err
	}
	return AgentReportResult{AgentID: snapshot.AgentID, Report: normalizeDisplayReportText(report), State: snapshot.State}, nil
}

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
	if !errors.Is(runtimeErr, errAgentNotFound) || !s.persistReportWithoutRuntime(ctx, agentID, report) {
		return ReportEventResult{}, false
	}
	return ReportEventResult{Success: true, AgentID: agentID, EventType: eventType, Report: report}, true
}

func (s *service) persistReportWithoutRuntime(ctx context.Context, agentID, report string) bool {
	if strings.TrimSpace(report) == "" {
		return false
	}
	snapshot, err := s.persistedAgentSnapshot(ctx, agentID)
	if err != nil || strings.TrimSpace(snapshot.Cwd) == "" {
		return false
	}
	record := agentReportFileRecordFromSnapshot(snapshot)
	record.Report = report
	return persistAgentReportFile(record) == nil
}

func setReportLocked(ctx context.Context, agent *agentRuntime, report string) {
	agent.lastReport = strings.TrimSpace(report)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
}

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

func resolveReportText(event ReportEvent) string {
	if report := strings.TrimSpace(event.Report); report != "" {
		return report
	}
	return extractReportFromEventData(event.EventData)
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

func reportTextFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if report := platformshared.FirstPayloadString(payload, reportTextPayloadKeys...); report != "" {
		return report
	}
	for _, key := range reportTextNestedKeys {
		nested, ok := payload[key].(map[string]any)
		if ok {
			if report := reportTextFromPayload(nested); report != "" {
				return report
			}
		}
	}
	return ""
}

func isTerminalReportEvent(eventType string, raw json.RawMessage) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == ReportEventTypeThreadStatusChanged {
		return isTerminalThreadStatus(raw)
	}
	_, ok := terminalReportEventTypes[eventType]
	return ok
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
	_, ok = terminalThreadStatuses[strings.ToLower(status)]
	return ok
}
