package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
)

const (
	defaultEditVersion      = 2
	replaceRangeFuncBodyMax = 8 * 1024
)

var errEditManagerNil = errors.New("edit requires LSP manager; ensure language server is running for this file type")

// EditRequest 是 edit 工具的入参结构体，包含动作、路径、补丁和版本信息。
type EditRequest struct {
	Action     string   `json:"action"`
	FilePath   string   `json:"file_path,omitempty"`
	LanguageID string   `json:"language_id,omitempty"`
	Patch      string   `json:"patch,omitempty"`
	Version    int      `json:"version,omitempty"`
	Pos        string   `json:"pos,omitempty"`
	NewName    string   `json:"new_name,omitempty"`
	Only       []string `json:"only,omitempty"`
}

// EditHandler 持有 LSP 管理器和工作区根目录，处理文件编辑请求。
type EditHandler struct {
	registry lspmanager.Registry
	root     string
}

// editEnvelope 是 edit 工具的通用响应信封，包含状态、计数和持久化标记。
type editEnvelope struct {
	Status               string `json:"status"`
	Message              string `json:"message,omitempty"`
	AppliedCount         int    `json:"applied_count,omitempty"`
	Persisted            bool   `json:"persisted"`
	LSPSync              bool   `json:"lsp_sync,omitempty"`
	FilePath             string `json:"file_path,omitempty"`
	Warning              string `json:"warning,omitempty"`
	DiagnosticGeneration uint64 `json:"diagnostic_generation,omitempty"`
}

// NewEditHandler 创建编辑处理器。
func NewEditHandler(registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("edit", middleware.TierNormal, EditHandler{registry: registry}.Handle)
}

// NewEditHandlerWithRoot 创建带根目录的编辑处理器。
func NewEditHandlerWithRoot(root string, registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("edit", middleware.TierNormal, EditHandler{registry: registry, root: resolveRoot(root)}.Handle)
}

// HandleEdit 处理编辑。
func HandleEdit(ctx context.Context, registry lspmanager.Registry, params json.RawMessage) (any, error) {
	return EditHandler{registry: registry}.Handle(ctx, params)
}

// Handle 执行 LSP 工具请求。
func (h EditHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.registry == nil {
		return nil, errEditManagerNil
	}
	req, err := decodeToolParams[EditRequest](params, decodeLenient)
	if err != nil {
		return nil, fmt.Errorf("decode edit request: %w", err)
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "replace_range"
	}
	switch action {
	case "replace_range":
		if req.FilePath == "" {
			return nil, fmt.Errorf("replace_range requires file_path")
		}
		if req.Patch == "" {
			return nil, fmt.Errorf("replace_range requires patch")
		}
		return h.handleReplaceRange(ctx, req)
	case "rename":
		return h.handleRename(ctx, req)
	case "code_action":
		return h.handleCodeAction(ctx, req)
	case "format":
		return h.handleFormat(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported edit action %q (valid: replace_range, rename, code_action, format)", action)
	}
}

// normalizeEditVersion 确保版本号有效，默认使用 defaultEditVersion。
func normalizeEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion
	}
	return version
}

// ToPlainText 渲染为纯文本。
func (e editEnvelope) ToPlainText() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Edit Status: %s\n", editStatusText(e)))
	if e.Message != "" {
		sb.WriteString(fmt.Sprintf("Message: %s\n", e.Message))
	}
	appendEditApplyStatus(&sb, e)
	appendEditWarnings(&sb, e)
	return strings.TrimSpace(sb.String())
}

// editStatusText 把状态码映射为 SUCCESS/NO_CHANGE/FAILED 文本。
func editStatusText(e editEnvelope) string {
	switch strings.ToLower(strings.TrimSpace(e.Status)) {
	case "applied":
		return "SUCCESS"
	case "no_change":
		return "NO_CHANGE"
	case "failed":
		return "FAILED"
	default:
		return "FAILED"
	}
}

// appendEditApplyStatus 追加编辑应用状态。
func appendEditApplyStatus(sb *strings.Builder, e editEnvelope) {
	switch strings.ToLower(strings.TrimSpace(e.Status)) {
	case "applied":
		sb.WriteString(fmt.Sprintf("Applied: true (%d replacements", e.AppliedCount))
		if e.Persisted {
			sb.WriteString(", persisted to disk")
		}
		if e.LSPSync {
			sb.WriteString(", LSP synced")
		}
		sb.WriteString(")\n")
		return
	case "no_change":
		sb.WriteString("Applied: false (patch matched but normalised away - current file already equals the requested NewText; verify your intended diff)\n")
		return
	case "failed":
		sb.WriteString("Applied: false (edit failed)\n")
	}
}

// appendMatchedByNotice surfaces relaxed match modes (trim_right /
// trim_both / unicode_normalized / escape_normalized / substring_exact)
// as a verification nudge. Exact matches stay silent because the model
// already trusts the patch text.
func appendMatchedByNotice(sb *strings.Builder, matchedBy string) {
	matchedBy = strings.TrimSpace(matchedBy)
	if matchedBy == "" || matchedBy == "exact" {
		return
	}
	fmt.Fprintf(sb, "Matched by: %s — verify indentation/whitespace before continuing.\n", matchedBy)
}

// appendEditWarnings 把 warning 和后续操作提示追加到输出。
func appendEditWarnings(sb *strings.Builder, e editEnvelope) {
	if e.Warning != "" {
		sb.WriteString(fmt.Sprintf("Warning: %s\n", e.Warning))
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	if (status == "applied" || status == "no_change") && e.FilePath != "" {
		fmt.Fprintf(sb, "next: file action=diagnostics file_path=%s\n", e.FilePath)
	}
}
