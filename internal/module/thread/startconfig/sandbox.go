package startconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
