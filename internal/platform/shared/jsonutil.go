package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

func DecodeInput(input json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	return json.Unmarshal(trimmed, dst)
}

func CloneSelector(selector mcp.Selector) mcp.Selector {
	cloned := selector
	if selector.Scope != nil {
		scope := *selector.Scope
		cloned.Scope = &scope
	}
	return cloned
}

func CloneHookPayload(payload mcp.HookPayload) mcp.HookPayload {
	cloned := payload
	cloned.Context = CloneRawMessage(payload.Context)
	return cloned
}

// CloneStrings delegates to clone.Strings.
func CloneStrings(input []string) []string { return clone.Strings(input) }

// CloneStringMap delegates to clone.StringMap.
func CloneStringMap(input map[string]string) map[string]string { return clone.StringMap(input) }

func FilterKeys(payload map[string]any, keys []string) map[string]any {
	if len(keys) == 0 {
		return payload
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := payload[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

// CloneRawMessage delegates to clone.RawMessage.
func CloneRawMessage(message json.RawMessage) json.RawMessage { return clone.RawMessage(message) }

// CloneJSONMap delegates to clone.JSONMap.
func CloneJSONMap(input map[string]any) map[string]any { return clone.JSONMap(input) }

// CloneRuntimeConfigMap delegates to clone.RuntimeConfigMap.
func CloneRuntimeConfigMap(cfg map[string]any) map[string]any { return clone.RuntimeConfigMap(cfg) }

func NormalizeAbsolutePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	cleaned := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned, nil
}
