// Package startconfig 提供线程启动配置的校验与解析工具，包括沙箱配置的合法性检查。
package startconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SanitizeSandbox 校验并规范化原始沙箱 JSON，返回 nil 表示空配置。
// 非空配置必须是已知 sandbox 字符串或带 type 字段的对象，未知 alias 直接 fail-fast。
func SanitizeSandbox(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid sandbox JSON: %s", string(raw))
	}
	if _, err := sandboxType(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// IsDangerFullAccessSandbox 检查给定的沙箱配置是否为 danger_full_access 模式，支持字符串值和对象两种格式。
func IsDangerFullAccessSandbox(raw json.RawMessage) (bool, error) {
	raw, err := SanitizeSandbox(raw)
	if err != nil || len(raw) == 0 {
		return false, err
	}
	value, err := sandboxType(raw)
	if err != nil {
		return false, err
	}
	return isDangerFullAccessValue(value), nil
}

// sandboxType 解析并校验 thread/start 传入的 sandbox 类型。
// 字符串和对象写法共用同一枚举校验，未知值必须 fail-fast。
func sandboxType(raw json.RawMessage) (string, error) {
	if raw[0] != '{' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errors.New("invalid sandbox JSON: expected string or object with string type")
		}
		return validateSandboxType(value)
	}
	var payload struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("invalid sandbox object: %w", err)
	}
	if len(payload.Type) == 0 {
		return "", errors.New("invalid sandbox object: type is required")
	}
	var value string
	if err := json.Unmarshal(payload.Type, &value); err != nil {
		return "", fmt.Errorf("invalid sandbox type: %w", err)
	}
	return validateSandboxType(value)
}

func validateSandboxType(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch normalizeSandboxType(trimmed) {
	case "readonly", "workspacewrite", "dangerfullaccess":
		return trimmed, nil
	default:
		return "", fmt.Errorf("invalid sandbox type %q", value)
	}
}

func isDangerFullAccessValue(value string) bool {
	return normalizeSandboxType(value) == "dangerfullaccess"
}

func normalizeSandboxType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	return normalized
}
