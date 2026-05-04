package timeline

import (
	"encoding/json"
	"fmt"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func planDeltaHandler(svc Service, onUpdated func(string)) func(turndto.PlanDelta) {
	return func(ev turndto.PlanDelta) {
		threadID, text := strings.TrimSpace(ev.ThreadID), planText(ev.Delta, ev.Payload)
		if threadID == "" || text == "" {
			return
		}
		agentID, turnID := strings.TrimSpace(ev.AgentID), strings.TrimSpace(ev.TurnID)
		id := timelineID("plan", turnID)

		if svc.UpdateByCallID(threadID, agentID, id, func(it *Item) {
			if !strings.Contains(it.Text, text) {
				if it.Text == "" {
					it.Text = text
				} else {
					it.Text += "\n" + text
				}
			}
		}) {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		svc.Append(threadID, agentID, Item{ID: id, Kind: "plan", Text: text, Ts: ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"), AgentID: agentID, TurnID: turnID, lookupKey: id})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func planUpdatedHandler(svc Service, onUpdated func(string)) func(turndto.PlanUpdated) {
	return func(ev turndto.PlanUpdated) {
		threadID, text := strings.TrimSpace(ev.ThreadID), planText("", ev.Payload)
		if threadID == "" || text == "" {
			return
		}
		agentID, turnID := strings.TrimSpace(ev.AgentID), strings.TrimSpace(ev.TurnID)
		id := timelineID("plan", turnID)

		if svc.UpdateByCallID(threadID, agentID, id, func(it *Item) { it.Text = text }) {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		svc.Append(threadID, agentID, Item{ID: id, Kind: "plan", Text: text, Ts: ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"), AgentID: agentID, TurnID: turnID, lookupKey: id})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func agentErrorHandler(svc Service, onUpdated func(string)) func(agentdto.AgentError) {
	return func(ev agentdto.AgentError) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		appendErrorItem(svc, threadID, strings.TrimSpace(ev.AgentID), "", timelineID("error", "agent", ev.AgentID, ev.Code, ev.Message), shared.FirstNonEmpty(strings.TrimSpace(ev.Message), strings.TrimSpace(ev.Code), strings.TrimSpace(string(ev.Payload))), ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func agentFailedHandler(svc Service, onUpdated func(string)) func(agentdto.AgentFailed) {
	return func(ev agentdto.AgentFailed) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		appendErrorItem(svc, threadID, strings.TrimSpace(ev.AgentID), "", timelineID("error", "failed", ev.AgentID, ev.Error), strings.TrimSpace(ev.Error), ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func itemKind(itemType, rawType, command, file string) string {
	if strings.TrimSpace(file) != "" {
		return "file"
	}
	if strings.TrimSpace(command) != "" {
		return "command"
	}
	joined := strings.ToLower(strings.TrimSpace(itemType + " " + rawType))
	switch {
	case strings.Contains(joined, "file"):
		return "file"
	case strings.Contains(joined, "command"):
		return "command"
	default:
		return "command"
	}
}

func itemCompletedStatus(kind string, success bool, exitCode int, errText string) string {
	if !success || exitCode != 0 || strings.TrimSpace(errText) != "" {
		return "failed"
	}
	if strings.EqualFold(strings.TrimSpace(kind), "file") {
		return "saved"
	}
	return "completed"
}

func itemCompletedHandler(svc Service, onUpdated func(string)) func(turndto.ItemCompleted) {
	return func(ev turndto.ItemCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		updateKey := itemUpdateKey(ev.CallID)
		if threadID == "" || updateKey == "" {
			return
		}
		success := ev.Success
		updated := svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
			applyItemCompleted(it, ev, success)
		})
		if updated {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		if appendCompletedItemFallback(svc, threadID, ev, updateKey, success) {
			emitTimelineUpdated(onUpdated, threadID)
		}
	}
}

func applyItemCompleted(it *Item, ev turndto.ItemCompleted, success bool) {
	it.Kind = itemKind(
		shared.FirstNonEmpty(strings.TrimSpace(ev.ItemType), it.ItemType),
		strings.TrimSpace(ev.RawType),
		shared.FirstNonEmpty(strings.TrimSpace(ev.Command), it.Command),
		shared.FirstNonEmpty(strings.TrimSpace(ev.File), it.File),
	)
	it.Status = itemCompletedStatus(it.Kind, success, ev.ExitCode, ev.Error)
	it.Success = &success
	it.Done = true
	if strings.TrimSpace(it.Ts) == "" {
		it.Ts = ev.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}
	if tool := strings.TrimSpace(ev.ToolName); tool != "" {
		it.Tool = tool
		it.ToolName = tool
	}
	if itemType := strings.TrimSpace(ev.ItemType); itemType != "" {
		it.ItemType = itemType
	}
	if command := strings.TrimSpace(ev.Command); command != "" {
		it.Command = command
	}
	if file := strings.TrimSpace(ev.File); file != "" {
		it.File = file
	}
	if strings.EqualFold(it.Kind, "command") || ev.ExitCode != 0 {
		code := ev.ExitCode
		it.ExitCode = &code
	}
	if errText := strings.TrimSpace(ev.Error); errText != "" {
		it.Error = errText
	}
}

func appendCompletedItemFallback(svc Service, threadID string, ev turndto.ItemCompleted, updateKey string, success bool) bool {
	if shared.FirstNonEmpty(
		strings.TrimSpace(ev.ItemType),
		strings.TrimSpace(ev.Command),
		strings.TrimSpace(ev.File),
		strings.TrimSpace(ev.ToolName),
	) == "" {
		return false
	}
	item := Item{
		lookupKey: updateKey,
		ID:        timelineID("item", ev.CallID, ev.ItemType, ev.Command, ev.File, ev.ToolName),
		Kind:      itemKind(ev.ItemType, ev.RawType, ev.Command, ev.File),
		CallID:    strings.TrimSpace(ev.CallID),
		Tool:      strings.TrimSpace(ev.ToolName),
		ToolName:  strings.TrimSpace(ev.ToolName),
		ItemType:  strings.TrimSpace(ev.ItemType),
		Command:   strings.TrimSpace(ev.Command),
		File:      strings.TrimSpace(ev.File),
		Error:     strings.TrimSpace(ev.Error),
		Success:   &success,
		Done:      true,
		AgentID:   strings.TrimSpace(ev.AgentID),
		TurnID:    strings.TrimSpace(ev.TurnID),
		Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	}
	item.Status = itemCompletedStatus(item.Kind, success, ev.ExitCode, ev.Error)
	if item.Kind == "command" || ev.ExitCode != 0 {
		exitCode := ev.ExitCode
		item.ExitCode = &exitCode
	}
	svc.Append(threadID, strings.TrimSpace(ev.AgentID), item)
	return true
}

func applyToolCallCompleted(it *Item, ev tooldto.ToolCallEnd, success bool) {
	it.Kind = "tool"
	it.Status = toolCallStatus(success, ev.Error)
	it.Success = &success
	it.Done = true
	if strings.TrimSpace(it.Ts) == "" {
		it.Ts = ev.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}
	it.Tool = shared.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.Tool)
	it.ToolName = shared.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.ToolName)
	if ev.ElapsedMS > 0 {
		ms := int(ev.ElapsedMS)
		it.ElapsedMS = &ms
	}
	if preview := previewText(shared.FirstNonEmpty(strings.TrimSpace(ev.Result), strings.TrimSpace(ev.Error))); preview != "" {
		it.Preview = preview
	}
	if errText := strings.TrimSpace(ev.Error); errText != "" {
		it.Error = errText
	}
}

func appendCompletedToolFallback(svc Service, threadID string, ev tooldto.ToolCallEnd, updateKey string, success bool) bool {
	tool := strings.TrimSpace(ev.ToolName)
	// Without a tool name the fallback row would render as “未知工具”, which
	// is worse than dropping the orphan event — the matching Begin-side row
	// (now keyed by CallID alone) carries the canonical name and stays in
	// the timeline as the source of truth. We only append a fallback when
	// ToolName is present so the new row is self-identifying.
	if tool == "" {
		return false
	}
	item := Item{
		lookupKey: updateKey,
		ID:        timelineID("tool", ev.CallID, ev.ToolName),
		Kind:      "tool",
		Status:    toolCallStatus(success, ev.Error),
		CallID:    strings.TrimSpace(ev.CallID),
		Tool:      tool,
		ToolName:  tool,
		Error:     strings.TrimSpace(ev.Error),
		Success:   &success,
		Done:      true,
		AgentID:   strings.TrimSpace(ev.AgentID),
		TurnID:    strings.TrimSpace(ev.TurnID),
		Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	}
	if ev.ElapsedMS > 0 {
		ms := int(ev.ElapsedMS)
		item.ElapsedMS = &ms
	}
	if preview := previewText(shared.FirstNonEmpty(strings.TrimSpace(ev.Result), strings.TrimSpace(ev.Error))); preview != "" {
		item.Preview = preview
	}
	svc.Append(threadID, strings.TrimSpace(ev.AgentID), item)
	return true
}

func toolCallStatus(success bool, errText string) string {
	if !success || strings.TrimSpace(errText) != "" {
		return "failed"
	}
	return "completed"
}

func planText(delta string, payload []byte) string {
	if text := strings.TrimSpace(delta); text != "" {
		// If delta looks like serialized JSON, try structured extraction first.
		if len(text) > 1 && (text[0] == '{' || text[0] == '[') {
			if parsed := parseStructuredPlan([]byte(text)); parsed != "" {
				return parsed
			}
		}
		return text
	}
	if parsed := parseStructuredPlan(payload); parsed != "" {
		return parsed
	}
	return strings.TrimSpace(string(payload))
}

// parseStructuredPlan extracts human-readable text from a Codex structured
// plan payload that contains an "explanation" string and/or a "plan" array
// of {"status": ..., "step": ...} objects.
func parseStructuredPlan(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) < 2 {
		return ""
	}
	switch trimmed[0] {
	case '{':
		return parseStructuredPlanObject(data)
	case '[':
		return parsePlanSteps(data)
	default:
		return ""
	}
}

func parseStructuredPlanObject(data []byte) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return ""
	}
	var parts []string
	if raw, ok := obj["explanation"]; ok {
		var explanation string
		if json.Unmarshal(raw, &explanation) == nil && strings.TrimSpace(explanation) != "" {
			parts = append(parts, strings.TrimSpace(explanation))
		}
	}
	if raw, ok := obj["plan"]; ok {
		if steps := parsePlanSteps(raw); steps != "" {
			parts = append(parts, steps)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return ""
}

func parsePlanSteps(data []byte) string {
	var steps []map[string]any
	if json.Unmarshal(data, &steps) != nil || len(steps) == 0 {
		return ""
	}
	var lines []string
	for i, step := range steps {
		text, _ := step["step"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		status, _ := step["status"].(string)
		lines = append(lines, fmt.Sprintf("%s %d. %s", planStepIcon(status), i+1, text))
	}
	return strings.Join(lines, "\n")
}

func planStepIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "completed", "complete":
		return "✅"
	case "inprogress", "in_progress", "running":
		return "🔄"
	case "failed", "error":
		return "❌"
	default:
		return "⏳"
	}
}

func previewText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return text
}

func emitTimelineUpdated(onUpdated func(string), threadID string) {
	if onUpdated == nil {
		return
	}
	onUpdated(strings.TrimSpace(threadID))
}

func appendErrorItem(svc Service, threadID, agentID, turnID, id, text, ts string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	svc.Append(threadID, agentID, Item{ID: id, Kind: "error", Status: "failed", Error: text, Text: text, AgentID: agentID, TurnID: turnID, Ts: ts, lookupKey: id})
}
