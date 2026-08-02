package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
)

const (
	defaultEditVersion      = 2
	replaceRangeFuncBodyMax = 8 * 1024
)

var errEditManagerNil = errors.New("patch_edit requires LSP manager; ensure language server is running for this file type")

// EditRequest 是 patch_edit 工具的入参结构体，包含动作、路径、补丁和版本信息。
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
	registry     lspmanager.Registry
	root         string
	lockRegistry *editLockRegistry
}

// editEnvelope 是 patch_edit 工具的通用响应信封，包含状态、计数和持久化标记。
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

// NewEditHandler 注册 patch_edit 工具处理器。
// 默认不绑定额外根目录，路径解析依赖调用上下文中的可信 workspace。
// 返回值应由调用方复用；factory wrapper 统一提供 scope、timeout、recovery、logging 和 budget。
func NewEditHandler(registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandlerWithTimeoutResolver("patch_edit", middleware.TierNormal, patchEditTimeoutTier, newEditHandler("", registry).Handle)
}

// NewEditHandlerWithRoot 注册绑定固定根目录的 patch_edit 工具处理器。
// 该入口用于 sidecar 初始化已知根目录的场景，所有写入仍需通过后续路径校验。
// 返回值应由调用方复用；factory wrapper 统一提供 scope、timeout、recovery、logging 和 budget。
func NewEditHandlerWithRoot(root string, registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandlerWithTimeoutResolver("patch_edit", middleware.TierNormal, patchEditTimeoutTier, newEditHandler(resolveRoot(root), registry).Handle)
}

func newEditHandler(root string, registry lspmanager.Registry) EditHandler {
	return EditHandler{registry: registry, root: root, lockRegistry: &editLockRegistry{}}
}

func patchEditTimeoutTier(params json.RawMessage) time.Duration {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(params, &input); err == nil && strings.TrimSpace(input.Action) == "replace_range" {
		return toolTimeoutDisabled
	}
	return middleware.TierNormal
}

// Handle 执行 LSP 工具请求。
func (h EditHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.registry == nil {
		return nil, errEditManagerNil
	}
	req, err := decodeToolParams[EditRequest](params, decodeLenient)
	if err != nil {
		return nil, fmt.Errorf("decode patch_edit request: %w", err)
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		return nil, fmt.Errorf("patch_edit requires action (valid: replace_range, rename, code_action, format)")
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
		return nil, fmt.Errorf("unsupported patch_edit action %q (valid: replace_range, rename, code_action, format)", action)
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
	fmt.Fprintf(&sb, "Edit Status: %s\n", editStatusText(e))
	if e.Message != "" {
		fmt.Fprintf(&sb, "Message: %s\n", e.Message)
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
		fmt.Fprintf(sb, "Applied: true (%d replacements", e.AppliedCount)
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
		sb.WriteString("Applied: false (patch_edit failed)\n")
	}
}

// appendMatchedByNotice 在补丁通过宽松匹配命中时追加人工复核提示。
// exact 命中保持静默；trim/unicode/escape/substring 命中都提示检查缩进和空白。
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
		fmt.Fprintf(sb, "Warning: %s\n", e.Warning)
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	if (status == "applied" || status == "no_change") && e.FilePath != "" {
		fmt.Fprintf(sb, "next: file action=diagnostics file_path=%s\n", e.FilePath)
	}
}
