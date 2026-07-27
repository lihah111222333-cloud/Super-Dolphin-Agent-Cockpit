package nodeexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const (
	automationRedactionMarker          = "[REDACTED]"
	automationTextMarshalFailureResult = `{"redacted":true,"reason":"marshal_scrubbed_text"}`
)

// finalizeAutomationOutcome 将命令执行结果物化为配置声明的输出。
// sharedfile 写入失败必须让节点失败，避免 node.result 指向不存在或未落盘的产物。
func finalizeAutomationOutcome(
	ctx context.Context,
	cfg *AutomationNodeConfig,
	node Node,
	runCtx RunContext,
	result AutomationCommandResult,
) (NodeOutcome, error) {
	payload, err := automationCommandResultPayload(result)
	if err != nil {
		return failedAutomationOutcome(FailureClassValidation, "marshal automation result: "+err.Error()), nil
	}
	outcome := NodeOutcome{Status: NodeStatusDone}
	if shouldEmitFullNodeResult(cfg.Outputs) {
		if failure := enforceNodeResultSizeCap(payload); failure != nil {
			return *failure, nil
		}
		outcome.Result = payload
	} else if shouldEmitSharedfileEnvelope(cfg.Outputs) {
		envelope, failure := buildAutomationSharedfileEnvelope(cfg.Outputs, node, runCtx)
		if failure != nil {
			return *failure, nil
		}
		outcome.Result = envelope
	}
	if failure := writeAutomationSharedfile(ctx, cfg, runCtx, result); failure != nil {
		return *failure, nil
	}
	return outcome, nil
}

// automationCommandResultPayload 生成可持久化的 command 结果。
// Args 只用于 runner 入参，不能落入 node.result；其他字段再统一递归脱敏后才允许公开。
func automationCommandResultPayload(result AutomationCommandResult) (json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		CardKey  string `json:"card_key"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
		Command  string `json:"command,omitempty"`
	}{
		CardKey:  result.CardKey,
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Command:  result.Command,
	})
	if err != nil {
		return nil, err
	}
	return ScrubAutomationResultPayload(payload), nil
}

// ScrubAutomationResultPayload 清理对外或落库的 automation result JSON。
// 它删除 args/__inputs，并递归遮蔽 token、authorization、cookie、secret、password、api_key 等敏感键。
func ScrubAutomationResultPayload(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"invalid_result_json"}`)
	}
	cleaned, err := json.Marshal(scrubAutomationResultValue(value))
	if err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"marshal_scrubbed_result"}`)
	}
	return cleaned
}

// redactSensitiveText 对命令、stdout、stderr 中的结构化或普通文本做统一脱敏。
// 完整 JSON 和 JSONL 递归清理；其余文本按赋值或 Header 边界扫描，避免供应商前缀绕过。
func redactSensitiveText(text string) string {
	if text == "" {
		return text
	}
	if cleaned, ok := scrubAutomationJSONText(text); ok {
		return cleaned
	}
	if cleaned, ok := scrubAutomationJSONLines(text); ok {
		return cleaned
	}
	return redactSensitiveAssignments(text)
}

func scrubAutomationJSONText(text string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return "", false
	}
	cleaned, err := json.Marshal(scrubAutomationResultValue(value))
	if err != nil {
		return automationTextMarshalFailureResult, true
	}
	return string(cleaned), true
}

// scrubAutomationJSONLines 仅在所有非空行都是完整 JSON 值时逐行递归脱敏。
func scrubAutomationJSONLines(text string) (string, bool) {
	if !strings.Contains(text, "\n") {
		return "", false
	}
	lines := strings.Split(text, "\n")
	parsedLines := 0
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cleaned, ok := scrubAutomationJSONText(line)
		if !ok {
			return "", false
		}
		lines[index] = cleaned
		parsedLines++
	}
	if parsedLines == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// redactSensitiveAssignments 扫描普通文本中的赋值和 Header，并只替换敏感键对应的值。
func redactSensitiveAssignments(text string) string {
	var redacted strings.Builder
	redacted.Grow(len(text))
	copyFrom := 0
	searchFrom := 0
	for searchFrom < len(text) {
		relativeDelimiter := strings.IndexAny(text[searchFrom:], "=:")
		if relativeDelimiter < 0 {
			break
		}
		delimiter := searchFrom + relativeDelimiter
		quotedKey, sensitive := sensitiveTextKeyBeforeDelimiter(text, delimiter)
		if !sensitive {
			searchFrom = delimiter + 1
			continue
		}
		valueStart := skipAutomationInlineSpace(text, delimiter+1)
		headerValue := text[delimiter] == ':' && !quotedKey
		valueEnd := sensitiveTextValueEnd(text, valueStart, headerValue)
		redacted.WriteString(text[copyFrom:valueStart])
		redacted.WriteString(automationRedactionMarker)
		copyFrom = valueEnd
		searchFrom = valueEnd
	}
	if copyFrom == 0 {
		return text
	}
	redacted.WriteString(text[copyFrom:])
	return redacted.String()
}

// sensitiveTextKeyBeforeDelimiter 提取分隔符前的 quoted 或普通键并执行统一敏感判定。
func sensitiveTextKeyBeforeDelimiter(text string, delimiter int) (bool, bool) {
	keyEnd := trimAutomationInlineSpaceBackward(text, delimiter)
	if keyEnd == 0 {
		return false, false
	}
	if isAutomationQuote(text[keyEnd-1]) {
		keyStart := openingAutomationQuote(text, keyEnd-1)
		if keyStart < 0 {
			return false, false
		}
		return true, isSensitiveAutomationResultKey(text[keyStart+1 : keyEnd-1])
	}
	keyStart := keyEnd
	for keyStart > 0 && isAutomationKeyByte(text[keyStart-1]) {
		keyStart--
	}
	if keyStart == keyEnd {
		return false, false
	}
	return false, isSensitiveAutomationResultKey(text[keyStart:keyEnd])
}

func trimAutomationInlineSpaceBackward(text string, end int) int {
	for end > 0 && isAutomationInlineSpace(text[end-1]) {
		end--
	}
	return end
}

func skipAutomationInlineSpace(text string, start int) int {
	for start < len(text) && isAutomationInlineSpace(text[start]) {
		start++
	}
	return start
}

func isAutomationInlineSpace(value byte) bool {
	return value == ' ' || value == '\t'
}

// isAutomationKeyByte 约束普通文本扫描可接受的环境变量或 Header 键字符。
func isAutomationKeyByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' || value == '-' || value == '.'
}

func isAutomationQuote(value byte) bool {
	return value == '"' || value == '\''
}

// openingAutomationQuote 在当前行内寻找未转义的 quoted key 起始引号。
func openingAutomationQuote(text string, closing int) int {
	quote := text[closing]
	for index := closing - 1; index >= 0 && text[index] != '\n' && text[index] != '\r'; index-- {
		if text[index] == quote && !automationByteIsEscaped(text, index) {
			return index
		}
	}
	return -1
}

func automationByteIsEscaped(text string, index int) bool {
	backslashes := 0
	for index--; index >= 0 && text[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 != 0
}

// sensitiveTextValueEnd 根据 quoted、Header 或普通赋值语义确定需要整体遮蔽的值边界。
func sensitiveTextValueEnd(text string, start int, headerValue bool) int {
	if start >= len(text) {
		return start
	}
	if isAutomationQuote(text[start]) {
		return quotedAutomationValueEnd(text, start)
	}
	if headerValue {
		return automationLineEnd(text, start)
	}
	end := start
	for end < len(text) && !unicode.IsSpace(rune(text[end])) {
		end++
	}
	return end
}

// quotedAutomationValueEnd 扫描 quoted value，缺少闭合引号时安全截断到行尾或文本末尾。
func quotedAutomationValueEnd(text string, start int) int {
	quote := text[start]
	escaped := false
	for index := start + 1; index < len(text); index++ {
		switch {
		case text[index] == '\n' || text[index] == '\r':
			return index
		case text[index] == quote && !escaped:
			return index + 1
		case text[index] == '\\':
			escaped = !escaped
		default:
			escaped = false
		}
	}
	return len(text)
}

func automationLineEnd(text string, start int) int {
	end := start
	for end < len(text) && text[end] != '\n' && text[end] != '\r' {
		end++
	}
	return end
}

func scrubAutomationResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return scrubAutomationResultObject(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, scrubAutomationResultValue(item))
		}
		return out
	case string:
		return redactSensitiveText(typed)
	default:
		return value
	}
}

func scrubAutomationResultObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		if shouldDropAutomationResultKey(key) {
			continue
		}
		if isSensitiveAutomationResultKey(key) {
			out[key] = automationRedactionMarker
			continue
		}
		out[key] = scrubAutomationResultValue(value)
	}
	return out
}

func shouldDropAutomationResultKey(key string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(key))
	return trimmed == "args" || trimmed == "__inputs"
}

// isSensitiveAutomationResultKey 使用分词后的完整语义词判断敏感键，避免子串误杀普通单词。
func isSensitiveAutomationResultKey(key string) bool {
	words := automationSensitiveKeyWords(key)
	for index, word := range words {
		switch word {
		case "token", "authorization", "cookie", "secret", "password", "apikey":
			return true
		case "api":
			if index+1 < len(words) && words[index+1] == "key" {
				return true
			}
		}
	}
	return false
}

// automationSensitiveKeyWords 将 snake、kebab、空白和 camel case 键统一拆成小写语义词。
func automationSensitiveKeyWords(key string) []string {
	runes := []rune(strings.TrimSpace(key))
	words := make([]string, 0, 4)
	wordStart := -1
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			words = appendAutomationSensitiveKeyWord(words, runes, wordStart, index)
			wordStart = -1
			continue
		}
		if wordStart < 0 {
			wordStart = index
			continue
		}
		if startsAutomationSensitiveKeyWord(runes, index) {
			words = appendAutomationSensitiveKeyWord(words, runes, wordStart, index)
			wordStart = index
		}
	}
	return appendAutomationSensitiveKeyWord(words, runes, wordStart, len(runes))
}

// startsAutomationSensitiveKeyWord 识别 camel case 与大写缩写转普通词的边界。
func startsAutomationSensitiveKeyWord(runes []rune, index int) bool {
	current := runes[index]
	if !unicode.IsUpper(current) {
		return false
	}
	previous := runes[index-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && index+1 < len(runes) && unicode.IsLower(runes[index+1])
}

func appendAutomationSensitiveKeyWord(words []string, runes []rune, start, end int) []string {
	if start < 0 || start == end {
		return words
	}
	return append(words, strings.ToLower(string(runes[start:end])))
}

// NodeResultSizeCapBytes 是 outputs.to_node_result 写入 task_dag_nodes.result 前的硬上限。
// 超过 4KB 的命令结果必须改走 outputs.to_sharedfile，避免持久化列和节点列表承载大块原始输出。
const NodeResultSizeCapBytes = 4096

// enforceNodeResultSizeCap 在 node.result 落库前执行大小检查。
// len(payload) <= 4096 放行；超过上限返回 validation outcome，并提示调用方改用 outputs.to_sharedfile。
func enforceNodeResultSizeCap(payload []byte) *NodeOutcome {
	if len(payload) <= NodeResultSizeCapBytes {
		return nil
	}
	outcome := failedAutomationOutcome(
		FailureClassValidation,
		fmt.Sprintf(
			"result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)",
			len(payload), NodeResultSizeCapBytes,
		),
	)
	return &outcome
}

// shouldEmitFullNodeResult 判定是否把完整命令结果写入 NodeOutcome.Result。
// 未声明 sharedfile 时保留完整结果，避免旧配置丢失输出；声明 sharedfile 且未显式要求 node_result 时只写轻量 envelope。
func shouldEmitFullNodeResult(out OutputsConfig) bool {
	if out.ToNodeResult {
		return true
	}
	return automationSharedfilePath(out) == ""
}

func shouldEmitSharedfileEnvelope(out OutputsConfig) bool {
	if automationSharedfilePath(out) == "" {
		return false
	}
	return out.NodeResultEnvelope == nil || *out.NodeResultEnvelope
}

func automationSharedfilePath(out OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

// buildAutomationSharedfileEnvelope 构建写入 node.result 的轻量 sharedfile envelope。
// envelope 必须带 dag/run/node 三元组，缺任一上下文都会失败，避免 UI 指向不可审计的输出路径。
func buildAutomationSharedfileEnvelope(out OutputsConfig, node Node, runCtx RunContext) (json.RawMessage, *NodeOutcome) {
	path := automationSharedfilePath(out)
	if path == "" {
		return nil, nil
	}
	dagKey, nodeKey := resolveAutomationEnvelopeKeys(node, runCtx)
	if dagKey == "" || nodeKey == "" || runCtx.RunID <= 0 {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile requires dag_key/run_id/node_key for node result envelope")
		return nil, &outcome
	}
	payload, err := json.Marshal(struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Dag        string `json:"dag"`
		Run        int64  `json:"run"`
		Node       string `json:"node"`
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}{
		Kind: "sharedfile",
		Path: path,
		Dag:  dagKey,
		Run:  runCtx.RunID,
		Node: nodeKey,
		Sharedfile: struct {
			Path string `json:"path"`
		}{Path: path},
	})
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal automation sharedfile envelope: "+err.Error())
		return nil, &outcome
	}
	return payload, nil
}

func resolveAutomationEnvelopeKeys(node Node, runCtx RunContext) (string, string) {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	if dagKey == "" {
		dagKey = strings.TrimSpace(node.DagKey)
	}
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		nodeKey = strings.TrimSpace(node.NodeKey)
	}
	return dagKey, nodeKey
}

// writeAutomationSharedfile 在配置了 outputs.to_sharedfile 时，把 result.Stdout 写入 sharedfile。
// 设计选型：仅写 stdout——这是 command_card 输出的「有意义载荷」；如需完整 result（包括 stderr / exit）
// 可后续在 SharedfileTarget 上加 mode 字段扩展。写入失败 → validation（任务约定）。
func writeAutomationSharedfile(
	ctx context.Context,
	cfg *AutomationNodeConfig,
	runCtx RunContext,
	result AutomationCommandResult,
) *NodeOutcome {
	target := cfg.Outputs.ToSharedfile
	if target == nil {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return nil
	}
	if runCtx.SharedFileWriter == nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile configured but SharedFileWriter not wired in RunContext")
		return &outcome
	}
	content := stripAutomationControlFieldsBeforePromptReuse(result.Stdout)
	if writer, ok := runCtx.SharedFileWriter.(SharedFileMetadataWriter); ok {
		err := writer.WriteSharedFileWithMetadata(ctx, SharedFileWriteRequest{
			Path:          path,
			Content:       content,
			ContentType:   "text/plain",
			OwnerNode:     automationSharedFileOwnerNode(runCtx),
			ProducerActor: automationSharedFileProducerActor(runCtx),
			RunID:         runCtx.RunID,
		})
		if err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
			return &outcome
		}
		return nil
	}
	if err := runCtx.SharedFileWriter.WriteSharedFile(ctx, path, content); err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
		return &outcome
	}
	return nil
}

func automationSharedFileOwnerNode(runCtx RunContext) string {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return ""
	}
	return dagKey + "/" + nodeKey
}

func automationSharedFileProducerActor(runCtx RunContext) string {
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		return ""
	}
	return "automation:" + nodeKey
}
