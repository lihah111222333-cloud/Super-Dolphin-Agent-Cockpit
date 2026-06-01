package codexmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func DecodeAllowed(raw []byte) ([]string, error) {
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

func ResolveSupported(raw []byte, requested string) (string, bool, error) {
	models, err := DecodeAllowed(raw)
	if err != nil {
		return "", false, err
	}
	model, replaced := resolveSupported(models, requested)
	return model, replaced, nil
}

func NeedsListResolution(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "" || isGenericGPT(model)
}

func WrapUnsupportedError(err error, model string) error {
	if err == nil {
		return nil
	}
	notice := UnsupportedNotice(err, model)
	if notice == "" {
		return err
	}
	return fmt.Errorf("%s: %w", notice, err)
}

func UnsupportedNotice(err error, model string) string {
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

func resolveSupported(models []string, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	preferred := preferred(models)
	if requested == "" {
		return preferred, preferred != ""
	}
	if isCodexFamily(requested) && contains(models, requested) {
		return requested, false
	}
	if !isGenericGPT(requested) && contains(models, requested) {
		return requested, false
	}
	if preferred != "" {
		return preferred, !strings.EqualFold(preferred, requested)
	}
	if contains(models, requested) {
		return requested, false
	}
	return requested, false
}

func preferred(models []string) string {
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

func contains(models []string, requested string) bool {
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

func isGenericGPT(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-") && !isCodexFamily(model)
}

func isCodexFamily(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "codex")
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
