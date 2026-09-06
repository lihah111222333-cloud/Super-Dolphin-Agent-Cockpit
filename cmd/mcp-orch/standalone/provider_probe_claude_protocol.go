package standalone

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type claudeProbeRequest struct {
	Type      string                `json:"type"`
	RequestID string                `json:"request_id"`
	Request   claudeProbeInitialize `json:"request"`
}

type claudeProbeInitialize struct {
	Subtype string `json:"subtype"`
}

type claudeProbeResponse struct {
	Type     string            `json:"type"`
	Response claudeProbeResult `json:"response"`
}

type claudeProbeResult struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
}

// writeClaudeProbeInitialize 编码唯一的无模型控制请求，并拒绝短写。
func writeClaudeProbeInitialize(input io.Writer, requestID string) error {
	frame, err := json.Marshal(claudeProbeRequest{Type: "control_request", RequestID: requestID, Request: claudeProbeInitialize{Subtype: "initialize"}})
	if err != nil {
		return fmt.Errorf("encode Claude probe initialize: %w", err)
	}
	frame = append(frame, '\n')
	n, err := input.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("write Claude probe initialize: %w", err)
	}
	return nil
}

// readClaudeProbeInitialize 只接受一个相关控制回包；动态能力对象不代表授权或模型成功。
func readClaudeProbeInitialize(output io.Reader, requestID string) error {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	if !scanner.Scan() {
		if scanner.Err() != nil {
			return errors.New("Claude probe response unreadable or oversized")
		}
		return errors.New("Claude probe returned no control response")
	}
	frame := scanner.Bytes()
	if err := rejectClaudeProbeDuplicateFields(json.NewDecoder(bytes.NewReader(frame))); err != nil {
		return errors.New("Claude probe returned invalid JSON object")
	}
	var response claudeProbeResponse
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return errors.New("Claude probe returned invalid control envelope")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("Claude probe returned trailing JSON")
	}
	return validateClaudeProbeInitialize(response, requestID)
}

// validateClaudeProbeInitialize 确认关联身份和非空对象边界，不把扩展能力当作授权。
func validateClaudeProbeInitialize(response claudeProbeResponse, requestID string) error {
	if response.Type != "control_response" || response.Response.Subtype != "success" || response.Response.RequestID != requestID || requestID == "" {
		return errors.New("Claude probe returned unsuccessful or unrelated control response")
	}
	payload := bytes.TrimSpace(response.Response.Response)
	if len(payload) == 0 || payload[0] != '{' {
		return errors.New("Claude probe initialize payload must be an object")
	}
	return nil
}

// rejectClaudeProbeDuplicateFields 递归检查对象键唯一性，避免稳定字段和动态 payload 的歧义。
func rejectClaudeProbeDuplicateFields(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			if err := scanClaudeProbeObjectKey(decoder, seen); err != nil {
				return err
			}
		}
		if err := rejectClaudeProbeDuplicateFields(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// scanClaudeProbeObjectKey 对当前对象作用域检查唯一键，包括转义后的等价名称。
func scanClaudeProbeObjectKey(decoder *json.Decoder, seen map[string]bool) error {
	key, err := decoder.Token()
	if err != nil {
		return err
	}
	name, ok := key.(string)
	if !ok || seen[name] {
		return errors.New("duplicate or invalid object key")
	}
	seen[name] = true
	return nil
}
