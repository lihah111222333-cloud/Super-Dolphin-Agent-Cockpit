// Package startconfig 提供线程启动配置的校验与解析工具，包括沙箱配置的合法性检查。
package startconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SanitizeSandbox 校验并规范化原始沙箱 JSON，返回 nil 表示空配置，非法 JSON 返回错误。
func SanitizeSandbox(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid sandbox JSON: %s", string(raw))
	}
	return raw, nil
}

// IsDangerFullAccessSandbox 检查给定的沙箱配置是否为 danger_full_access 模式，支持字符串值和对象两种格式。
func IsDangerFullAccessSandbox(raw json.RawMessage) (bool, error) {
	raw, err := SanitizeSandbox(raw)
	if err != nil || len(raw) == 0 {
		return false, err
	}
	if raw[0] != '{' {
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return isDangerFullAccessValue(value), nil
		}
		return false, errors.New("invalid sandbox JSON: expected string or object with string type")
	}
	var payload struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false, fmt.Errorf("invalid sandbox object: %w", err)
	}
	if len(payload.Type) == 0 {
		return false, errors.New("invalid sandbox object: type is required")
	}
	var value string
	if err := json.Unmarshal(payload.Type, &value); err != nil {
		return false, fmt.Errorf("invalid sandbox type: %w", err)
	}
	return isDangerFullAccessValue(value), nil
}

func isDangerFullAccessValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized == "dangerfullaccess"
}
