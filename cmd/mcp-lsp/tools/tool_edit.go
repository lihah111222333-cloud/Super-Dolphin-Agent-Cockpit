package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

const defaultEditVersion = 2

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
	Hint                 string `json:"hint,omitempty"`
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

// patchEditTimeoutTierForOS 是故意保留在公共文件中的纯策略函数：调用方显式传入
// 目标 OS，测试可覆盖 Windows 与非 Windows，而不在这里执行任何平台专用系统调用。
// Windows 冷安装动作关闭工具层外部 deadline，安装器自身仍负责有界超时；其他平台
// 保留原有 tier deadline。replace_range 在所有平台都由内部事务边界管理。
func patchEditTimeoutTierForOS(params json.RawMessage, goos string) time.Duration {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(params, &input); err == nil {
		action := strings.TrimSpace(input.Action)
		if action == "replace_range" || (windowsColdInstallOuterTimeoutDisabled(goos) && (action == "rename" || action == "code_action" || action == "format")) {
			return toolTimeoutDisabled
		}
	}
	return middleware.TierNormal
}

// Handle 执行 LSP 工具请求。
func (h EditHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.registry == nil {
		return nil, errEditManagerNil
	}
	req, err := decodeEditRequest(params)
	if err != nil {
		return nil, err
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
		return h.handleValidatedRename(ctx, req)
	case "code_action":
		return h.handleCodeAction(ctx, req)
	case "format":
		return h.handleFormat(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported patch_edit action %q (valid: replace_range, rename, code_action, format)", action)
	}
}

// handleValidatedRename 校验 rename 参数后进入真实 LSP 写入流程。
func (h EditHandler) handleValidatedRename(ctx context.Context, req EditRequest) (any, error) {
	if err := validateRenameEditRequest(req); err != nil {
		return nil, err
	}
	return h.handleRename(ctx, req)
}

func decodeEditRequest(params json.RawMessage) (EditRequest, error) {
	req, err := decodeToolParams[EditRequest](params, decodeLenient)
	if err != nil {
		return EditRequest{}, fmt.Errorf("decode patch_edit request: %w", err)
	}
	return req, nil
}

func validateRenameEditRequest(req EditRequest) error {
	if strings.TrimSpace(req.NewName) == "" {
		return common.NewCodedToolError("invalid_params", errors.New("rename requires new_name"), false,
			"next: pass a language-valid identifier in new_name")
	}
	if strings.TrimSpace(req.Pos) == "" {
		return common.NewCodedToolError("invalid_params", errors.New("rename requires pos"), false,
			"next: pass pos=file_path:line:column on the identifier to rename")
	}
	return nil
}

// normalizeEditVersion 确保版本号有效，默认使用 defaultEditVersion。
func normalizeEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion
	}
	return version
}

// ToPlainText 将单文件编辑结果渲染为稳定行协议。
func (e editEnvelope) ToPlainText() string {
	files := []renameFileChange(nil)
	if strings.TrimSpace(e.FilePath) != "" {
		files = []renameFileChange{{FilePath: e.FilePath, EditCount: max(e.AppliedCount, 0)}}
	}
	return renderEditReceipt(e.AppliedCount, e.Status, e.Message, e.Hint, e.Warning, e.Persisted, e.LSPSync, files)
}

// ToPlainText 将 rename 结果渲染为同一编辑行协议。
func (r renameResult) ToPlainText() string {
	status := "no_change"
	if r.TotalEdits > 0 {
		status = "applied"
	}
	return renderEditReceipt(r.TotalEdits, status, r.Message, "", r.Warning, r.TotalEdits > 0, r.Warning == "", r.AffectedFiles)
}

// ToPlainText 将 code_action 结果渲染为同一编辑行协议。
func (r codeActionResult) ToPlainText() string {
	return renderEditReceipt(r.TotalEdits, r.Status, r.Message, r.Hint, r.Warning, r.Persisted, r.LSPSync, r.AffectedFiles)
}

func renderEditReceipt(total int, status, message, hint, warning string, persisted, lspSync bool, files []renameFileChange) string {
	total = max(total, 0)
	lines := []string{lineprotocol.HeaderLine(total, total, false, "edit")}
	if message = strings.TrimSpace(message); message != "" {
		lines = append(lines, lineprotocol.TextRecord("MESSAGE", message))
	}
	for _, file := range files {
		lines = append(lines, lineprotocol.FieldsRecord("FILE",
			lineprotocol.Field{Key: "path", Value: file.FilePath},
			lineprotocol.Field{Key: "edits", Value: strconv.Itoa(max(file.EditCount, 0))},
			lineprotocol.Field{Key: "status", Value: strings.TrimSpace(status)},
			lineprotocol.Field{Key: "persisted", Value: strconv.Itoa(boolToInt(persisted))},
			lineprotocol.Field{Key: "lsp_sync", Value: strconv.Itoa(boolToInt(lspSync))},
		))
	}
	if warning = strings.TrimSpace(warning); warning != "" {
		lines = append(lines, lineprotocol.TextRecord("WARNING", warning))
	}
	if hint = strings.TrimSpace(hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}
