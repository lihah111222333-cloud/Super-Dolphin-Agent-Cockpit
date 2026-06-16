package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

type codeActionResult struct {
	editEnvelope
	ActionTitle   string             `json:"action_title,omitempty"`
	AffectedFiles []renameFileChange `json:"affected_files,omitempty"`
	TotalEdits    int                `json:"total_edits,omitempty"`
	Actions       []string           `json:"actions,omitempty"`
}

// handleCodeAction 处理代码动作。
func (h EditHandler) handleCodeAction(ctx context.Context, req EditRequest) (any, error) {
	if strings.TrimSpace(req.Pos) == "" {
		return nil, fmt.Errorf("code_action requires pos (file_path:line:column)")
	}
	filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{Pos: req.Pos, LanguageID: req.LanguageID})
	if err != nil {
		return nil, fmt.Errorf("resolve code_action position: %w", err)
	}
	manager, err := managerForFile(ctx, h.registry, filePath, req.LanguageID)
	if err != nil {
		return nil, fmt.Errorf("code_action manager: %w", err)
	}
	rng := protocol.Range{Start: position, End: position}
	actions, err := manager.CodeAction(ctx, filePath, rng, req.Only)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("code action"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("LSP code_action: %w", err)
	}
	return h.applyCodeActions(ctx, actions, normalizeEditVersion(req.Version), manager)
}

// applyCodeActions 应用代码actions。
func (h EditHandler) applyCodeActions(ctx context.Context, actions []protocol.CodeActionResult, version int, manager lspmanager.Manager) (any, error) {
	if len(actions) == 0 {
		return emptyListEnvelope{Success: true, Data: []any{}, Meta: resultMeta{Count: 0, Message: "no code actions found"}}, nil
	}
	editable := codeActionsWithWorkspaceEdit(actions)
	if len(editable) == 0 {
		return h.codeActionRequiresApply(actions, "code actions returned no directly applicable edits", manager), nil
	}
	if len(editable) > 1 {
		return h.codeActionRequiresApply(actions, "multiple code actions returned; no edit applied", manager), nil
	}
	selected := editable[0]
	affected, totalEdits, warning, err := h.applyWorkspaceEdit(ctx, selected.edit, version)
	if err != nil {
		return nil, fmt.Errorf("apply code action edits: %w", err)
	}
	return codeActionResult{
		editEnvelope: editEnvelope{
			Status:               codeActionStatus(totalEdits),
			Message:              codeActionApplyMessage(selected.title, totalEdits),
			AppliedCount:         totalEdits,
			Persisted:            totalEdits > 0,
			LSPSync:              totalEdits > 0 && warning == "",
			Warning:              warning,
			DiagnosticGeneration: managerDiagnosticGeneration(manager),
		},
		ActionTitle:   selected.title,
		AffectedFiles: affected,
		TotalEdits:    totalEdits,
	}, nil
}

// handleFormat 处理格式化。
func (h EditHandler) handleFormat(ctx context.Context, req EditRequest) (any, error) {
	if strings.TrimSpace(req.FilePath) == "" {
		return nil, fmt.Errorf("format requires file_path")
	}
	path, err := h.resolveEditFilePath(ctx, req.FilePath)
	if err != nil {
		return nil, err
	}
	manager, err := managerForFile(ctx, h.registry, path, req.LanguageID)
	if err != nil {
		return nil, fmt.Errorf("format manager: %w", err)
	}
	edits, err := manager.Format(ctx, path, defaultFormattingOptions())
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("format"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("LSP format: %w", err)
	}
	return h.applyFormatEdits(ctx, manager, path, edits, normalizeEditVersion(req.Version))
}

func (h EditHandler) applyFormatEdits(ctx context.Context, manager lspmanager.Manager, path string, edits []protocol.TextEdit, version int) (any, error) {
	if len(edits) == 0 {
		return h.formatNoChange(path, manager), nil
	}
	result, err := h.applyTextEditsToPath(ctx, path, edits, version, manager)
	if err != nil {
		return nil, fmt.Errorf("apply format edits: %w", err)
	}
	if result == nil {
		return h.formatNoChange(path, manager), nil
	}
	return editEnvelope{
		Status:               "applied",
		Message:              fmt.Sprintf("format applied %d edit(s)", len(edits)),
		AppliedCount:         len(edits),
		Persisted:            true,
		LSPSync:              result.warning == "",
		FilePath:             path,
		Warning:              result.warning,
		DiagnosticGeneration: managerDiagnosticGeneration(manager),
	}, nil
}

func (h EditHandler) resolveEditFilePath(ctx context.Context, rawPath string) (string, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return "", err
	}
	return resolveWorkspacePathInRoots(root, roots, rawPath)
}

func (h EditHandler) formatNoChange(path string, manager lspmanager.Manager) editEnvelope {
	return editEnvelope{
		Status:               "no_change",
		Message:              "format returned no changes",
		Persisted:            false,
		FilePath:             path,
		DiagnosticGeneration: managerDiagnosticGeneration(manager),
	}
}

func (h EditHandler) codeActionRequiresApply(actions []protocol.CodeActionResult, message string, manager lspmanager.Manager) codeActionResult {
	return codeActionResult{
		editEnvelope: editEnvelope{
			Status:               "no_change",
			Message:              message,
			Persisted:            false,
			DiagnosticGeneration: managerDiagnosticGeneration(manager),
		},
		Actions: codeActionTitles(actions),
	}
}

func defaultFormattingOptions() protocol.FormattingOptions {
	return protocol.FormattingOptions{TabSize: 4, InsertSpaces: false}
}

type editableCodeAction struct {
	title string
	edit  *protocol.WorkspaceEdit
}

func codeActionsWithWorkspaceEdit(actions []protocol.CodeActionResult) []editableCodeAction {
	editable := make([]editableCodeAction, 0, len(actions))
	for _, action := range actions {
		if action.CodeAction == nil || action.CodeAction.Edit == nil {
			continue
		}
		editable = append(editable, editableCodeAction{
			title: action.CodeAction.Title,
			edit:  action.CodeAction.Edit,
		})
	}
	return editable
}

// codeActionTitles 处理代码动作titles。
func codeActionTitles(actions []protocol.CodeActionResult) []string {
	titles := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.CodeAction != nil && strings.TrimSpace(action.CodeAction.Title) != "" {
			titles = append(titles, action.CodeAction.Title)
			continue
		}
		if action.Command != nil && strings.TrimSpace(action.Command.Title) != "" {
			titles = append(titles, action.Command.Title)
		}
	}
	return titles
}

func codeActionStatus(totalEdits int) string {
	if totalEdits == 0 {
		return "no_change"
	}
	return "applied"
}

func codeActionApplyMessage(title string, totalEdits int) string {
	if totalEdits == 0 {
		return fmt.Sprintf("code action %q returned no changes", title)
	}
	return fmt.Sprintf("applied code action %q", title)
}

// applyTextEditsToPath 把文本编辑应用为路径。
func (h EditHandler) applyTextEditsToPath(ctx context.Context, absPath string, edits []protocol.TextEdit, version int, manager lspmanager.Manager) (*fileEditResult, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", absPath, err)
	}
	original, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", absPath, err)
	}
	updated, err := applyTextEdits(string(original), edits)
	if err != nil {
		return nil, fmt.Errorf("apply edits to %s: %w", absPath, err)
	}
	if updated == string(original) {
		return nil, nil
	}
	if err := os.WriteFile(absPath, []byte(updated), info.Mode()); err != nil {
		return nil, fmt.Errorf("write %s: %w", absPath, err)
	}
	result := &fileEditResult{path: absPath, original: original, mode: info.Mode()}
	syncManager := h.resolveSyncManager(ctx, absPath, manager)
	if syncManager != nil {
		_, warning, err := h.syncDocument(ctx, syncManager, absPath, updated, version)
		if err != nil {
			rollbackErr := os.WriteFile(absPath, original, info.Mode())
			if rollbackErr == nil {
				rollbackErr = h.syncRollbackDocument(ctx, syncManager, absPath, string(original), version)
			}
			return nil, withRollbackError(err, rollbackErr)
		}
		result.warning = warning
	}
	return result, nil
}

func (h EditHandler) resolveSyncManager(ctx context.Context, absPath string, manager lspmanager.Manager) lspmanager.Manager {
	if manager != nil {
		return manager
	}
	resolved, err := managerForFile(ctx, h.registry, absPath, "")
	if err != nil {
		return nil
	}
	return resolved
}
