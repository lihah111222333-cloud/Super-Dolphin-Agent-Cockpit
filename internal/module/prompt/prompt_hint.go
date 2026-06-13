package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// These must stay in lockstep with the write side in
// internal/module/uistate/config_rpc.go (lspPromptHintOverrideKey,
// lspPromptHintDefaultPath). If either string changes there, change it here.
const (
	promptHintOverridePreferenceKey = "config/lspPromptHint.override"
	promptHintDefaultSharedFilePath = "prompts/lsp-mandatory-prefix.md"
)

// resolvePromptHint returns the user-configurable LSP prompt hint for the given
// cwd. Preference override wins over the shared-file default. Returns an empty
// string if neither is configured or wired.
func (s *service) resolvePromptHint(ctx context.Context, cwd string) string {
	if s == nil {
		return ""
	}
	if override := s.readPromptHintOverride(ctx, cwd); strings.TrimSpace(override) != "" {
		return override
	}
	return s.readPromptHintDefault(ctx)
}

func (s *service) readPromptHintOverride(ctx context.Context, cwd string) string {
	if s.prefs == nil {
		return ""
	}
	raw, err := s.prefs.GetValue(ctx, strings.TrimSpace(cwd), promptHintOverridePreferenceKey)
	switch {
	case err == nil:
	case contract.IsNotFound(err):
		return ""
	default:
		// Non-fatal: assembly must not fail because the override lookup hit a
		// transient store error. Log so operators can see the silent fallback.
		if s.logger != nil {
			s.logger.Warn("prompt_hint.override_read_failed", "cwd", cwd, "error", err.Error())
		}
		return ""
	}
	return decodePromptHintRaw(raw)
}

// readPromptHintDefault 读取prompthintdefault。
func (s *service) readPromptHintDefault(ctx context.Context) string {
	if s.sharedFiles == nil {
		return ""
	}
	file, err := s.sharedFiles.Get(ctx, promptHintDefaultSharedFilePath)
	switch {
	case err == nil:
		if file == nil {
			return ""
		}
		return file.Content
	case contract.IsNotFound(err):
		return ""
	default:
		if s.logger != nil {
			s.logger.Warn("prompt_hint.default_read_failed", "path", promptHintDefaultSharedFilePath, "error", err.Error())
		}
		return ""
	}
}

func decodePromptHintRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
