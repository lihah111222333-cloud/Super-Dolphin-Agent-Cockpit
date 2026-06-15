package dreamexec

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TokenUsage 是 dream provider 从 CLI 输出解析出的 token 计数。
// 3 字段与 dreammetrics.AddTokens 签名 (input, output, cacheRead) 一对一，
// wrapper 可直接 dreammetrics.AddTokens(usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens)。
//
// 语义对齐主线 internal/provider/claudecli/session_log_watcher.go parseLogLineUsage：
//   - InputTokens 已含 cacheCreation 与 cacheRead （为了两 provider 跨家可加，
//     不代表 LLM API 语义上的 input_tokens）
//   - OutputTokens 仅包含生成输出
//   - CacheReadTokens 单列为命中缓存信号（成本/时延优化）
type TokenUsage struct {
	InputTokens     uint64
	OutputTokens    uint64
	CacheReadTokens uint64
}

// StripJSONFences 移除 ```json ... ``` / ``` ... ``` 包裹。
// 容忍 fence 前后空白、CRLF。fence 不存在时原样返回（仅 trim 边缘空白）。
// 不处理嵌套 fence — LLM 输出 JSON 内嵌 ``` 的概率极低，遇到再扩展。
func StripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// 去掉首行 ```（可能带 json/JSON/jsonc/任意语言标签）
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[idx+1:]
	} else {
		// 单行 ``` 无 newline，无 body
		return ""
	}
	// 去掉尾部 ``` （可能带尾部空白或 newline）
	s = strings.TrimRight(s, " \t\r\n")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// ExtractFirstJSONObject 从字符串里提取第一个 balanced { ... } 对象。
// 用于 LLM 输出夹 prose preamble / 多个对象的情况。
// 字符串内的 { } 和转义按 JSON 词法处理（识别字符串、转义符）。
// 找不到则返回错误。
func ExtractFirstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", errors.New("no JSON object found (no '{' in input)")
	}
	end := findBalancedJSONEnd(s, start)
	if end < 0 {
		return "", errors.New("unbalanced JSON object (missing '}')")
	}
	return s[start:end], nil
}

// findBalancedJSONEnd 从 start ('{') 开始扫描，返回配对 '}' 后一位下标（exclusive）。
// 识别 JSON 字符串词法（转义符 + 引号），字符串内的 { } 不计入 depth。
// 未找到配对返回 -1。
func findBalancedJSONEnd(s string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			inString, escaped = stepStringState(ch, escaped)
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// stepStringState 推进字符串内部状态机：由 (escaped) 状态决定下一个 (inString, escaped)。
// 只在 inString=true 调用；返回后 ch 被消费。
func stepStringState(ch byte, escaped bool) (inString bool, nextEscaped bool) {
	if escaped {
		return true, false
	}
	if ch == '\\' {
		return true, true
	}
	if ch == '"' {
		return false, false
	}
	return true, false
}

// claudeEnvelope 是 claude -p --output-format json 输出的包络结构。
// 实例（仅列 dream 使用到的字段）：
// {"type":"result","is_error":false,"result":"...text...",
//
//	"usage":{"input_tokens":6,"cache_creation_input_tokens":7409,
//	         "cache_read_input_tokens":16153,"output_tokens":8,...}, ...}
type claudeEnvelope struct {
	Type           string `json:"type"`
	IsError        bool   `json:"is_error"`
	APIErrorStatus int    `json:"api_error_status"`
	Result         string `json:"result"`
	Usage          struct {
		InputTokens              uint64 `json:"input_tokens"`
		OutputTokens             uint64 `json:"output_tokens"`
		CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// ExtractClaudeEnvelope 解析 claude -p --output-format json 的包络 JSON，
// 返回模型生成的 result 文本 + token usage。
//
// is_error=true 或 type 非 "result" 返错误。result 为空返错误。
// 上层拿到 result 后再走 ExtractFirstJSONObject 提取实际的 {"memories":...}。
func ExtractClaudeEnvelope(raw []byte) (string, TokenUsage, error) {
	var env claudeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", TokenUsage{}, fmt.Errorf("dreamexec: parse claude envelope: %w", err)
	}
	if env.IsError {
		if isModelUnavailableMessage(env.APIErrorStatus, env.Result) {
			return "", TokenUsage{}, fmt.Errorf("%w: claude envelope reports model error (status=%d)", ErrModelUnavailable, env.APIErrorStatus)
		}
		return "", TokenUsage{}, fmt.Errorf("dreamexec: claude envelope reports error (type=%s)", env.Type)
	}
	if env.Type != "" && env.Type != "result" {
		return "", TokenUsage{}, fmt.Errorf("dreamexec: unexpected claude envelope type: %s", env.Type)
	}
	if strings.TrimSpace(env.Result) == "" {
		return "", TokenUsage{}, errors.New("dreamexec: claude envelope has empty result")
	}
	usage := TokenUsage{
		InputTokens:     env.Usage.InputTokens + env.Usage.CacheCreationInputTokens + env.Usage.CacheReadInputTokens,
		OutputTokens:    env.Usage.OutputTokens,
		CacheReadTokens: env.Usage.CacheReadInputTokens,
	}
	return env.Result, usage, nil
}

// modelUnavailableErrorFromOutput 从输出中识别模型不可用错误。
func modelUnavailableErrorFromOutput(parts ...[]byte) error {
	for _, raw := range parts {
		if !looksLikeClaudeEnvelope(raw) {
			if isModelUnavailableMessage(0, string(raw)) {
				return ErrModelUnavailable
			}
			continue
		}
		_, _, err := ExtractClaudeEnvelope(raw)
		if errors.Is(err, ErrModelUnavailable) {
			return err
		}
	}
	return nil
}

// isModelUnavailableMessage 判断模型unavailable消息是否可用。
func isModelUnavailableMessage(status int, message string) bool {
	lower := strings.ToLower(message)
	hasModelContext := strings.Contains(lower, "model")
	if status == 404 && hasModelContext {
		return true
	}
	for _, term := range []string{
		"selected model",
		"model may not exist",
		"model does not exist",
		"model not found",
		"unknown model",
		"invalid model",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	if hasModelContext && (strings.Contains(lower, "not have access") || strings.Contains(lower, "no access to")) {
		return true
	}
	return false
}

// codexJSONLEvent 是 codex exec --json 输出的单行 JSON event 结构。
// 只列 dream 使用到的事件类型与字段。
type codexJSONLEvent struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item,omitempty"`
	Usage *struct {
		InputTokens       uint64 `json:"input_tokens"`
		CachedInputTokens uint64 `json:"cached_input_tokens"`
		OutputTokens      uint64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// ExtractCodexJSONL 解析 codex exec --json 的 JSONL stream，返回模型 agent_message 文本 + token usage。
//
// 事件流示例：
//
//	{"type":"thread.started",...}
//	{"type":"turn.started"}
//	{"type":"item.completed","item":{"type":"reasoning","text":"..."}}     <- 跳过
//	{"type":"item.completed","item":{"type":"agent_message","text":"..."}} <- 提取
//	{"type":"turn.completed","usage":{...}}                                <- 提取
//
// 取最后一个 agent_message 事件的 text（dream 是 single-turn，通常只一个）。
// usage 字段名与 claude 不同：input_tokens 已含 cached（OpenAI 惯例），无 cache_creation。
// 未找到 agent_message 返错误；usage 缺失仅返回零值 usage（不报错，上报 0 不污染 counter）。
func ExtractCodexJSONL(raw []byte) (string, TokenUsage, error) {
	var lastAgentText string
	var usage TokenUsage
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if ev, ok := decodeCodexJSONLEvent(scanner.Bytes()); ok {
			applyCodexJSONLEvent(ev, &lastAgentText, &usage)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", TokenUsage{}, fmt.Errorf("dreamexec: scan codex jsonl: %w", err)
	}
	if strings.TrimSpace(lastAgentText) == "" {
		return "", TokenUsage{}, errors.New("dreamexec: no agent_message in codex jsonl")
	}
	return lastAgentText, usage, nil
}

func decodeCodexJSONLEvent(rawLine []byte) (codexJSONLEvent, bool) {
	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || line[0] != '{' {
		return codexJSONLEvent{}, false
	}
	var ev codexJSONLEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return codexJSONLEvent{}, false
	}
	return ev, true
}

func applyCodexJSONLEvent(ev codexJSONLEvent, lastAgentText *string, usage *TokenUsage) {
	switch ev.Type {
	case "item.completed":
		applyCodexCompletedItem(ev, lastAgentText)
	case "turn.completed":
		applyCodexUsage(ev, usage)
	}
}

func applyCodexCompletedItem(ev codexJSONLEvent, lastAgentText *string) {
	if ev.Item == nil || ev.Item.Type != "agent_message" {
		return
	}
	*lastAgentText = ev.Item.Text
}

func applyCodexUsage(ev codexJSONLEvent, usage *TokenUsage) {
	if ev.Usage == nil {
		return
	}
	*usage = TokenUsage{
		InputTokens:     ev.Usage.InputTokens,
		OutputTokens:    ev.Usage.OutputTokens,
		CacheReadTokens: ev.Usage.CachedInputTokens,
	}
}

// extractStructuredCLIResult 探测 raw stdout 是否为已知 CLI 结构化输出（claude envelope / codex JSONL），
// 是则调用对应 Extract* 返回 result text + usage；否则返回 structured=false 让调用方走 fallback。
//
// 返回语义：
//   - structured=true + err=nil: 成功提取，调用方以 result 为 text 走 ExtractFirstJSONObject
//   - structured=true + err!=nil: 识别出了格式但解析失败（如 envelope is_error=true），错误透传走 retry
//   - structured=false: 未识别为任一 CLI 格式，调用方 fallback 到原始 raw text 路径
func extractStructuredCLIResult(raw []byte) (result string, usage TokenUsage, structured bool, err error) {
	if looksLikeClaudeEnvelope(raw) {
		result, usage, err := ExtractClaudeEnvelope(raw)
		return result, usage, true, err
	}
	if looksLikeCodexJSONL(raw) {
		result, usage, err := ExtractCodexJSONL(raw)
		return result, usage, true, err
	}
	return "", TokenUsage{}, false, nil
}

// looksLikeClaudeEnvelope 按 envelope 探针字段判别：root 位是单个 JSON 对象，含 type/result/is_error 之一。
func looksLikeClaudeEnvelope(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var probe struct {
		Type    string           `json:"type"`
		IsError *bool            `json:"is_error"`
		Result  *json.RawMessage `json:"result"`
		Usage   *json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return false
	}
	return probe.Result != nil || probe.IsError != nil || probe.Type == "result" || probe.Type == "error"
}

// looksLikeCodexJSONL 按 JSONL stream 探针：有至少一行 JSON 含 codex 特定 type 枚举。
func looksLikeCodexJSONL(raw []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		switch probe.Type {
		case "thread.started", "turn.started", "turn.completed", "item.completed":
			return true
		}
	}
	return false
}
