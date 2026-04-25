package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// Terminal event-type and thread-status tables plus the
// report-text payload scan order now live in report_protocol.go.
// See P22 P4 §64 / §122 / §283.

func (s *service) GetState(ctx context.Context, agentID string) (AgentStateResult, error) {
	var result AgentStateResult
	err := s.withAgentReadLockedByAgentID(agentID, func(agent *agentRuntime) error {
		result = AgentStateResult{AgentID: agent.id, State: agent.state}
		return nil
	})
	if err != nil && errors.Is(err, errAgentNotFound) {
		snapshot, lookupErr := s.persistedAgentSnapshot(ctx, agentID)
		if lookupErr == nil {
			return AgentStateResult{AgentID: snapshot.AgentID, State: snapshot.State}, nil
		}
		if !errors.Is(lookupErr, errAgentNotFound) {
			return AgentStateResult{}, lookupErr
		}
	}
	return result, err
}

func (s *service) GetReport(ctx context.Context, agentID string) (AgentReportResult, error) {
	var result AgentReportResult
	err := s.withAgentReadLockedByAgentID(agentID, func(agent *agentRuntime) error {
		result = agentReportLocked(agent)
		return nil
	})
	if err != nil && errors.Is(err, errAgentNotFound) {
		snapshot, lookupErr := s.persistedAgentSnapshot(ctx, agentID)
		if lookupErr == nil {
			return AgentReportResult{AgentID: snapshot.AgentID, State: snapshot.State}, nil
		}
		if !errors.Is(lookupErr, errAgentNotFound) {
			return AgentReportResult{}, lookupErr
		}
	}
	return result, err
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
	report := resolveReportText(event)

	result := ReportEventResult{}
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if report != "" {
			setReportLocked(ctx, agent, report)
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
	return result, err
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
		State:    agent.state,
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
