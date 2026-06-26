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

// planDeltaHandler 将增量计划事件合并到同一个 plan timeline item。
// 同一 turn 的 plan 文本按 CallID 聚合，避免前端看到多个碎片化计划行。
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

// planUpdatedHandler 用完整计划快照覆盖 timeline 中的 plan item。
// 结构化 payload 会同时携带完成状态，供前端 checkbox/完成态展示使用。
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

// agentErrorHandler 将 agent 运行错误追加为 timeline error item。
// 缺少 threadID 的事件无法定位 UI 线程，直接丢弃以避免跨线程污染。
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

// agentFailedHandler 将 agent 失败事件追加为 timeline error item。
// 失败事件没有 call 维度，ID 由 agent 和错误文本组合生成以保证同线程稳定展示。
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

// itemKind 根据完成事件里的文件、命令和原始类型推导 timeline item 类型。
// 缺少明确类型时保守归为 command，避免把可执行动作展示成普通消息。
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

// itemCompletedStatus 将完成事件映射为前端状态。
// 文件写入成功显示 saved，命令或工具失败统一标记 failed。
func itemCompletedStatus(kind string, success bool, exitCode int, errText string) string {
	if !success || exitCode != 0 || strings.TrimSpace(errText) != "" {
		return "failed"
	}
	if strings.EqualFold(strings.TrimSpace(kind), "file") {
		return "saved"
	}
	return "completed"
}

// itemCompletedHandler 将 item 完成事件写回已有 timeline 行，必要时追加兜底行。
// 兜底只处理足够自描述的事件，避免把纯消息完成事件误渲染成工具/命令。
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

// applyItemCompleted 用完成事件补齐已有 item 的状态、类型和错误字段。
// 已有时间戳优先保留，避免完成事件覆盖 begin 事件的排序锚点。
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

// appendCompletedItemFallback 在缺少 begin-side item 时追加完成态兜底行。
// 该路径只用于可自描述的命令/文件/工具事件，防止普通消息被重复展示。
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

// shouldAppendCompletedItemFallback 判断完成事件是否足以生成独立 timeline 行。
// 没有 CallID 的普通 message 不追加，避免与消息投影链重复。
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

// applyToolCallCompleted 用工具结束事件补齐已有 tool item。
// 大结果会先压缩 preview，防止 timeline 快照携带过大的工具输出。
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

// appendCompletedToolFallback 在缺少 begin-side tool item 时追加完成态兜底行。
func appendCompletedToolFallback(svc Service, threadID string, ev tooldto.ToolCallEnd, updateKey string, success bool) bool {
	tool := strings.TrimSpace(ev.ToolName)
	// 没有工具名的兜底行只能渲染为“未知工具”，不如保留 begin-side 行作为权威展示。
	// 因此只有 ToolName 存在时才追加新的完成态行。
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

// toolCallStatus 将工具结束结果映射为前端状态。
func toolCallStatus(success bool, errText string) string {
	if !success || strings.TrimSpace(errText) != "" {
		return "failed"
	}
	return "completed"
}

// parsedPlanContent 承载结构化 plan payload 中的文本和完成态。
type parsedPlanContent struct {
	Text      string
	Done      bool
	DoneKnown bool
}

// planText 从增量文本或结构化 payload 中提取可展示计划文本。
func planText(delta string, payload []byte) string {
	return planContent(delta, payload).Text
}

// planContent 解析增量或完整计划 payload，优先保留结构化完成态。
func planContent(delta string, payload []byte) parsedPlanContent {
	if text := strings.TrimSpace(delta); text != "" {
		// delta 可能已经是序列化 JSON，先走结构化解析以保留步骤完成态。
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

// parseStructuredPlan 从结构化 plan payload 中提取前端可展示文本。
// payload 可包含 explanation 字符串，也可包含 status/step 数组。
func parseStructuredPlan(data []byte) string {
	return parseStructuredPlanContent(data).Text
}

// parseStructuredPlanContent 按 JSON 顶层类型分派 plan payload 解析。
// 非 JSON 或空 payload 返回零值，让调用方回退到原始文本。
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

// parseStructuredPlanObject 解析对象形式的 plan payload，并合并说明和步骤文本。
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

// parsePlanSteps 解析数组形式的 plan 步骤，并推导整体完成态。
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

// planStepIcon 将步骤状态映射为紧凑列表前缀。
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

// planStepDone 判断步骤状态是否代表完成。
func planStepDone(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "completed", "complete", "success", "succeeded":
		return true
	default:
		return false
	}
}

// previewText 截断 timeline preview 文本，避免大输出撑开快照。
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

// compactToolResultPreview 从大型 JSON 工具结果中抽取少量关键字段作为 preview。
// 非 JSON 或已经足够短的文本返回空串，让调用方使用原始截断逻辑。
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

// compactArrayPreview 压缩数组型工具结果，尽量保留前缀元素和 total/showing 摘要。
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

// copyPreviewFields 复制对排查最有用且体积可控的 preview 字段。
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

// decodeStructuredPreview 解出 structuredContent 中可能嵌套的对象或 JSON 字符串。
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

// marshalLimitedPreview 逐级删减 preview 字段，直到 JSON 文本落入 timeline 长度限制。
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

// clonePreviewFields 深拷贝 preview 字段，避免压缩过程改写调用方 map。
func clonePreviewFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields))
	for key, raw := range fields {
		out[key] = append(json.RawMessage(nil), raw...)
	}
	return out
}

// truncatePreviewStringFields 截断指定 map 内的字符串字段。
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

// marshalPreviewIfWithinLimit 在 preview 未超过长度限制时返回 JSON 文本。
func marshalPreviewIfWithinLimit(fields map[string]json.RawMessage) string {
	raw, err := json.Marshal(fields)
	if err != nil || len([]rune(string(raw))) > maxPreviewRunes {
		return ""
	}
	return string(raw)
}

// toolCallEndPreview 生成工具结束事件的前端 preview。
// 失败且结果为空时优先展示错误文本；成功大 JSON 会走紧凑摘要。
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

// isNullPreview 判断 provider 返回的文本是否只是 JSON null。
func isNullPreview(text string) bool {
	return strings.TrimSpace(text) == "null"
}

// emitTimelineUpdated 在回调存在时通知指定线程刷新。
func emitTimelineUpdated(onUpdated func(string), threadID string) {
	if onUpdated == nil {
		return
	}
	onUpdated(strings.TrimSpace(threadID))
}

// appendErrorItem 追加错误 item，空错误文本不产生 timeline 行。
func appendErrorItem(svc Service, threadID, agentID, turnID, id, text, ts string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	svc.Append(threadID, agentID, Item{ID: id, Kind: "error", Status: "failed", Error: text, Text: text, AgentID: agentID, TurnID: turnID, Ts: ts, lookupKey: id})
}
