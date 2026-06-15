package timeline

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// planDeltaHandler 处理plandelta处理器。
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

// planUpdatedHandler 处理planupdated处理器。
func planUpdatedHandler(svc Service, onUpdated func(string)) func(turndto.PlanUpdated) {
	return func(ev turndto.PlanUpdated) {
		content := planContent("", ev.Payload)
		threadID, text := strings.TrimSpace(ev.ThreadID), content.Text
		if threadID == "" || text == "" {
			return
		}
		agentID, turnID := strings.TrimSpace(ev.AgentID), strings.TrimSpace(ev.TurnID)
		id := timelineID("plan", turnID)

		if svc.UpdateByCallID(threadID, agentID, id, func(it *Item) {
			it.Text = text
			if content.DoneKnown {
				it.Done = content.Done
			}
		}) {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		svc.Append(threadID, agentID, Item{ID: id, Kind: "plan", Text: text, Done: content.DoneKnown && content.Done, Ts: ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"), AgentID: agentID, TurnID: turnID, lookupKey: id})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func agentErrorHandler(svc Service, onUpdated func(string)) func(agentdto.AgentError) {
	return func(ev agentdto.AgentError) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		appendErrorItem(svc, threadID, strings.TrimSpace(ev.AgentID), "", timelineID("error", "agent", ev.AgentID, ev.Code, ev.Message), util.FirstNonEmpty(strings.TrimSpace(ev.Message), strings.TrimSpace(ev.Code), strings.TrimSpace(string(ev.Payload))), ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
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

// itemCompletedHandler 处理itemcompleted处理器。
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

// applyItemCompleted 应用itemcompleted。
func applyItemCompleted(it *Item, ev turndto.ItemCompleted, success bool) {
	it.Kind = itemKind(
		util.FirstNonEmpty(strings.TrimSpace(ev.ItemType), it.ItemType),
		strings.TrimSpace(ev.RawType),
		util.FirstNonEmpty(strings.TrimSpace(ev.Command), it.Command),
		util.FirstNonEmpty(strings.TrimSpace(ev.File), it.File),
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
	if !shouldAppendCompletedItemFallback(ev) {
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

// shouldAppendCompletedItemFallback 判断appendcompleteditem兜底是否可用。
func shouldAppendCompletedItemFallback(ev turndto.ItemCompleted) bool {
	itemType := strings.TrimSpace(ev.ItemType)
	command := strings.TrimSpace(ev.Command)
	file := strings.TrimSpace(ev.File)
	toolName := strings.TrimSpace(ev.ToolName)
	if util.FirstNonEmpty(itemType, command, file, toolName) == "" {
		return false
	}
	if strings.TrimSpace(ev.CallID) != "" || command != "" || file != "" || strings.TrimSpace(ev.Error) != "" {
		return true
	}
	return !isMessageItemType(itemType)
}

func isMessageItemType(itemType string) bool {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "message", "usermessage", "user_message", "assistantmessage", "assistant_message":
		return true
	default:
		return false
	}
}

func applyToolCallCompleted(it *Item, ev tooldto.ToolCallEnd, success bool) {
	it.Kind = "tool"
	it.Status = toolCallStatus(success, ev.Error)
	it.Success = &success
	it.Done = true
	if strings.TrimSpace(it.Ts) == "" {
		it.Ts = ev.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}
	it.Tool = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.Tool)
	it.ToolName = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.ToolName)
	if ev.ElapsedMS > 0 {
		ms := int(ev.ElapsedMS)
		it.ElapsedMS = &ms
	}
	if preview := toolCallEndPreview(ev.Result, ev.Error, success); preview != "" {
		it.Preview = preview
	}
	if errText := strings.TrimSpace(ev.Error); errText != "" {
		it.Error = errText
	}
}

// appendCompletedToolFallback 追加completed工具兜底。
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
	if preview := toolCallEndPreview(ev.Result, ev.Error, success); preview != "" {
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

type parsedPlanContent struct {
	Text      string
	Done      bool
	DoneKnown bool
}

func planText(delta string, payload []byte) string {
	return planContent(delta, payload).Text
}

// planContent 处理plan内容。
func planContent(delta string, payload []byte) parsedPlanContent {
	if text := strings.TrimSpace(delta); text != "" {
		// If delta looks like serialized JSON, try structured extraction first.
		if len(text) > 1 && (text[0] == '{' || text[0] == '[') {
			if parsed := parseStructuredPlanContent([]byte(text)); parsed.Text != "" {
				return parsed
			}
		}
		return parsedPlanContent{Text: text}
	}
	if parsed := parseStructuredPlanContent(payload); parsed.Text != "" {
		return parsed
	}
	return parsedPlanContent{Text: strings.TrimSpace(string(payload))}
}

// parseStructuredPlan extracts human-readable text from a Codex structured
// plan payload that contains an "explanation" string and/or a "plan" array
// of {"status": ..., "step": ...} objects.
func parseStructuredPlan(data []byte) string {
	return parseStructuredPlanContent(data).Text
}

func parseStructuredPlanContent(data []byte) parsedPlanContent {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) < 2 {
		return parsedPlanContent{}
	}
	switch trimmed[0] {
	case '{':
		return parseStructuredPlanObject(data)
	case '[':
		text, done, doneKnown := parsePlanSteps(data)
		return parsedPlanContent{Text: text, Done: done, DoneKnown: doneKnown}
	default:
		return parsedPlanContent{}
	}
}

// parseStructuredPlanObject 解析structuredplanobject。
func parseStructuredPlanObject(data []byte) parsedPlanContent {
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return parsedPlanContent{}
	}
	var parts []string
	if raw, ok := obj["explanation"]; ok {
		var explanation string
		if json.Unmarshal(raw, &explanation) == nil && strings.TrimSpace(explanation) != "" {
			parts = append(parts, strings.TrimSpace(explanation))
		}
	}
	done, doneKnown := false, false
	if raw, ok := obj["plan"]; ok {
		if steps, stepsDone, stepsDoneKnown := parsePlanSteps(raw); steps != "" {
			parts = append(parts, steps)
			done, doneKnown = stepsDone, stepsDoneKnown
		}
	}
	if len(parts) > 0 {
		return parsedPlanContent{Text: strings.Join(parts, "\n"), Done: done, DoneKnown: doneKnown}
	}
	return parsedPlanContent{}
}

// parsePlanSteps 解析plansteps。
func parsePlanSteps(data []byte) (string, bool, bool) {
	var steps []map[string]any
	if json.Unmarshal(data, &steps) != nil || len(steps) == 0 {
		return "", false, false
	}
	var lines []string
	done, doneKnown := true, false
	for i, step := range steps {
		text, _ := step["step"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		status, _ := step["status"].(string)
		doneKnown = true
		if !planStepDone(status) {
			done = false
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", planStepIcon(status), i+1, text))
	}
	return strings.Join(lines, "\n"), done && doneKnown, doneKnown
}

func planStepIcon(status string) string {
	if planStepDone(status) {
		return "✅"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "inprogress", "in_progress", "running":
		return "🔄"
	case "failed", "error":
		return "❌"
	default:
		return "⏳"
	}
}

func planStepDone(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "completed", "complete", "success", "succeeded":
		return true
	default:
		return false
	}
}

func previewText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > maxPreviewRunes {
		return string(runes[:maxPreviewRunes])
	}
	return text
}

const maxPreviewRunes = 200

// compactToolResultPreview 处理紧凑列表工具结果preview。
func compactToolResultPreview(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxPreviewRunes || text == "" {
		return ""
	}
	if text[0] == '[' {
		return compactArrayPreview([]byte(text))
	}
	if text[0] != '{' {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &obj) != nil {
		return ""
	}
	compact := make(map[string]json.RawMessage)
	copyPreviewFields(compact, obj)
	if structured := decodeStructuredPreview(obj["structuredContent"]); structured != nil {
		copyPreviewFields(compact, structured)
	}
	if len(compact) == 0 {
		compact["total"] = json.RawMessage(strconv.Itoa(len(obj)))
	}
	return marshalLimitedPreview(compact)
}

// compactArrayPreview 处理紧凑列表arraypreview。
func compactArrayPreview(raw []byte) string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	summary := map[string]json.RawMessage{
		"total":   json.RawMessage(strconv.Itoa(len(items))),
		"showing": json.RawMessage(strconv.Itoa(len(items))),
	}
	for n := len(items); n > 0; n-- {
		summary["showing"] = json.RawMessage(strconv.Itoa(n))
		if rawPrefix, err := json.Marshal(items[:n]); err == nil {
			summary["items"] = rawPrefix
		}
		if out := marshalPreviewIfWithinLimit(summary); out != "" {
			return out
		}
	}
	delete(summary, "items")
	summary["showing"] = json.RawMessage("0")
	if out := marshalPreviewIfWithinLimit(summary); out != "" {
		return out
	}
	return `{"total":0,"showing":0}`
}

func copyPreviewFields(dst, src map[string]json.RawMessage) {
	for _, key := range []string{
		"success", "isError", "error", "message", "reason", "error_code", "errorCode",
		"action", "path", "file_path", "filePath", "total", "showing",
		"text_edit_count", "applied_count", "applied", "persisted",
	} {
		if raw := src[key]; len(raw) != 0 {
			dst[key] = raw
		}
	}
}

func decodeStructuredPreview(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil || json.Unmarshal([]byte(text), &obj) != nil {
			return nil
		}
	}
	return obj
}

// marshalLimitedPreview 编码limitedpreview。
func marshalLimitedPreview(fields map[string]json.RawMessage) string {
	compact := clonePreviewFields(fields)
	if out := marshalPreviewIfWithinLimit(compact); out != "" {
		return out
	}
	truncatePreviewStringFields(compact, 80)
	if out := marshalPreviewIfWithinLimit(compact); out != "" {
		return out
	}
	for _, key := range []string{"path", "file_path", "filePath", "message", "reason"} {
		delete(compact, key)
	}
	if out := marshalPreviewIfWithinLimit(compact); out != "" {
		return out
	}
	truncatePreviewStringFields(compact, 32)
	if out := marshalPreviewIfWithinLimit(compact); out != "" {
		return out
	}
	delete(compact, "error")
	if out := marshalPreviewIfWithinLimit(compact); out != "" {
		return out
	}
	return "{}"
}

func clonePreviewFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields))
	for key, raw := range fields {
		out[key] = append(json.RawMessage(nil), raw...)
	}
	return out
}

func truncatePreviewStringFields(fields map[string]json.RawMessage, limit int) {
	for key, raw := range fields {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		runes := []rune(value)
		if len(runes) <= limit {
			continue
		}
		next, err := json.Marshal(string(runes[:limit]))
		if err == nil {
			fields[key] = next
		}
	}
}

func marshalPreviewIfWithinLimit(fields map[string]json.RawMessage) string {
	raw, err := json.Marshal(fields)
	if err != nil || len([]rune(string(raw))) > maxPreviewRunes {
		return ""
	}
	return string(raw)
}

// toolCallEndPreview 处理工具callendpreview。
func toolCallEndPreview(result, errText string, success bool) string {
	result = strings.TrimSpace(result)
	errText = strings.TrimSpace(errText)
	if !success && isNullPreview(result) && errText != "" {
		return previewText(errText)
	}
	if isNullPreview(result) {
		result = ""
	}
	text := util.FirstNonEmpty(result, errText)
	if compact := compactToolResultPreview(text); compact != "" {
		return compact
	}
	return previewText(text)
}

func isNullPreview(text string) bool {
	return strings.TrimSpace(text) == "null"
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
