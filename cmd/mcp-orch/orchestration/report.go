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

// Terminal event-type and thread-status tables plus the
// report-text payload scan order now live in report_protocol.go.
// See P22 P4 §64 / §122 / §283.

func (s *service) GetState(ctx context.Context, agentID string) (AgentStateResult, error) {
	var result AgentStateResult
	err := s.withAgentReadLockedByAgentID(agentID, func(agent *agentRuntime) error {
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
	err := s.withAgentReadLockedByAgentID(agentID, func(agent *agentRuntime) error {
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
	return AgentReportResult{
		AgentID: snapshot.AgentID,
		Report:  normalizeDisplayReportText(report),
		State:   snapshot.State,
	}, nil
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

// TODO(p2-b15): deliver drained requester notifications into the UI timeline once a stable V3 delivery path exists.
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
		if report != "" {
			setReportLocked(ctx, agent, report)
			if err := persistAgentReportFile(agentReportFileRecordFromRuntime(agent)); err != nil {
				return err
			}
		}
		notified := []string(nil)
		if report != "" || isTerminalReportEvent(eventType, event.EventData) {
			notified = drainReportRequestersLocked(ctx, agent)
		}
		if report == "" {
			report = strings.TrimSpace(agent.lastReport)
		}
		result = ReportEventResult{
			Success:              true,
			AgentID:              agent.id,
			EventType:            eventType,
			Report:               report,
			NotifiedRequesterIDs: notified,
		}
		return nil
	})
	if fallback, ok := s.reportEventFallbackResult(ctx, agentID, eventType, report, err); ok {
		return fallback, nil
	}
	return result, err
}

// reportEventFallbackResult builds a HandleReportEvent success result by
// persisting the report straight to disk when the in-memory runtime is
// gone (mcp-orch restarted mid-turn). ok is false when the runtime error
// is not errAgentNotFound or the report could not be written.
func (s *service) reportEventFallbackResult(ctx context.Context, agentID, eventType, report string, runtimeErr error) (ReportEventResult, bool) {
	if !errors.Is(runtimeErr, errAgentNotFound) {
		return ReportEventResult{}, false
	}
	if !s.persistReportWithoutRuntime(ctx, agentID, report) {
		return ReportEventResult{}, false
	}
	return ReportEventResult{
		Success:   true,
		AgentID:   agentID,
		EventType: eventType,
		Report:    report,
	}, true
}

// persistReportWithoutRuntime writes a report straight to disk for an
// agent whose in-memory runtime is gone — e.g. mcp-orch restarted
// mid-turn and lost s.agents before the turn-completed event arrived.
// It resolves cwd/name from the persisted thread snapshot, the same
// fallback source GetReport reads, so a completed turn's report still
// reaches disk. Returns true only when the report was actually written.
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
	lines := strings.Split(text, "\n")
	trimmed := make([]string, 0, len(lines))
	nonEmpty := make([]string, 0, len(lines))
	prevBlank := false
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
			continue
		case strings.ContainsRune("._-+/=", r):
			continue
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
	return AgentReportResult{
		AgentID:  agent.id,
		Report:   normalizeDisplayReportText(agent.lastReport),
		State:    string(agent.state),
		Metadata: metadata,
	}
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

// turnCompletedReportText derives the report body for a completed turn.
// A successful turn carries its answer in result/summary/message; a
// failed turn carries none of those, only error/reason/stop_reason.
// Without the failure fallback a failed child agent's report stays
// empty and the parent's get_agent_report degrades to "not found",
// silently hiding the failure.
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

// noReportFallbackText is the body GetReport returns when a persisted
// agent snapshot exists but no report file was ever written — the turn
// never reached turn:complete, or mcp-orch restarted and lost the
// in-memory runtime before a report event could be persisted. Returning
// this in place of errAgentReportNotFound lets the parent agent see the
// agent's terminal state instead of a bare "not found".
func noReportFallbackText(state string) string {
	if state = strings.TrimSpace(state); state != "" {
		return "agent ended in state '" + state + "' without producing a turn report"
	}
	return "agent ended without producing a turn report"
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
		if !ok {
			continue
		}
		if report := reportTextFromPayload(nested); report != "" {
			return report
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
