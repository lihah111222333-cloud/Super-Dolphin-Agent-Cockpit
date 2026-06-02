package supportutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func DecodeAllowedModels(raw []byte) ([]string, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err == nil {
		if models := modelIDs(top["models"]); len(models) > 0 {
			return models, nil
		}
		if models := modelIDs(top["data"]); len(models) > 0 {
			return models, nil
		}
	}
	var list []any
	if err := json.Unmarshal(raw, &list); err == nil {
		if models := modelIDs(list); len(models) > 0 {
			return models, nil
		}
	}
	return nil, errors.New("codexapp: invalid model/list response")
}

func PreferredCodexModel(models []string) string {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), "gpt-5-codex") {
			return strings.TrimSpace(model)
		}
	}
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" && strings.Contains(strings.ToLower(trimmed), "codex") {
			return trimmed
		}
	}
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func CodexModelListContains(models []string, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), requested) {
			return true
		}
	}
	return false
}

func CodexModelNeedsListResolution(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "gpt-5.5" {
		return false
	}
	return model == "" || CodexModelIsGenericGPT(model)
}

func CodexModelIsGenericGPT(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-") && !CodexModelIsCodexFamily(model)
}

func CodexModelIsCodexFamily(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "codex")
}

func WrapCodexModelUnsupportedError(err error, model string) error {
	if err == nil {
		return nil
	}
	notice := CodexModelUnsupportedNotice(err, model)
	if notice == "" {
		return err
	}
	return fmt.Errorf("%s: %w", notice, err)
}

func CodexModelUnsupportedNotice(err error, model string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "model") ||
		!strings.Contains(lower, "not supported") ||
		!strings.Contains(lower, "codex") ||
		!strings.Contains(lower, "chatgpt") {
		return ""
	}
	selected := strings.TrimSpace(model)
	if selected == "" {
		selected = quotedModelFromUnsupportedText(text)
	}
	if selected != "" {
		return fmt.Sprintf("Codex model %q is not supported by the current ChatGPT account. Choose a supported Codex model in Settings or clear the model override, then retry", selected)
	}
	return "The selected Codex model is not supported by the current ChatGPT account. Choose a supported Codex model in Settings or clear the model override, then retry"
}

func ConfigString(cfg map[string]any, keys ...string) string {
	if cfg == nil {
		return ""
	}
	for _, key := range keys {
		value, _ := cfg[key].(string)
		if value = SanitizeConfigStringArtifact(value); value != "" {
			return value
		}
	}
	return ""
}

func FirstConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := ConfigString(cfg, key); value != "" {
			return value
		}
	}
	return ""
}

func SanitizeConfigStringArtifact(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
}

func ResolveApprovalPolicy(cfg map[string]any) string {
	for _, key := range []string{"approvalPolicy", "approval_policy"} {
		if value := ConfigString(cfg, key); value != "" {
			return value
		}
	}
	return "never"
}

func ConfigJSON(cfg map[string]any, key string) json.RawMessage {
	if cfg == nil || cfg[key] == nil {
		return nil
	}
	raw, err := json.Marshal(cfg[key])
	if err != nil || string(raw) == "null" {
		return nil
	}
	return raw
}

func SortedConfigKeys(cfg map[string]any) []string {
	if len(cfg) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func quotedModelFromUnsupportedText(text string) string {
	const prefix = "The '"
	start := strings.Index(text, prefix)
	if start < 0 {
		return ""
	}
	rest := text[start+len(prefix):]
	end := strings.Index(rest, "'")
	if end <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func modelIDs(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
