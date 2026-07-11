package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// prompt hint 读写两侧必须共用同一组 key/path。
// 写入侧在 internal/module/uistate/config_rpc.go；任一字符串变更时两边要同步更新。
const (
	promptHintOverridePreferenceKey = "config/lspPromptHint.override"
	promptHintDefaultSharedFilePath = "prompts/lsp-mandatory-prefix.md"
)

// resolvePromptHint 读取当前 cwd 可见的用户配置 LSP prompt hint。
// 用户偏好覆盖值优先于共享文件默认值；未配置、依赖未注入或读取失败时返回空串，不阻断 prompt 组装。
func (s *service) resolvePromptHint(ctx context.Context, cwd string) string {
	if s == nil {
		return ""
	}
	if override := s.readPromptHintOverride(ctx, cwd); strings.TrimSpace(override) != "" {
		return override
	}
	return s.readPromptHintDefault(ctx)
}

// readPromptHintOverride 读取当前 cwd 的用户偏好覆盖值；未配置或读取失败时返回空串并记录日志。
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
		// prompt 组装不能因提示前缀读取的瞬时 store 错误失败；记录日志供运维排查。
		if s.logger != nil {
			s.logger.Warn("prompt_hint.override_read_failed", "cwd", cwd, "error", err.Error())
		}
		return ""
	}
	return decodePromptHintRaw(raw)
}

// readPromptHintDefault 读取共享文件中的默认 LSP prompt hint，未配置时返回空串。
func (s *service) readPromptHintDefault(ctx context.Context) string {
	if s.sharedFiles == nil {
		return ""
	}
	content, err := s.sharedFiles.GetContent(ctx, promptHintDefaultSharedFilePath)
	switch {
	case err == nil:
		return content
	case contract.IsNotFound(err):
		return ""
	default:
		if s.logger != nil {
			s.logger.Warn("prompt_hint.default_read_failed", "path", promptHintDefaultSharedFilePath, "error", err.Error())
		}
		return ""
	}
}

// decodePromptHintRaw 支持字符串 JSON、null 和旧格式原始文本。
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
