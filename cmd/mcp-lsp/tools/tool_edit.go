package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

const (
	defaultEditVersion              = 2
	didChangeLargeFileLineThreshold = 200
	replaceRangeFuncBodyMax         = 8 * 1024
)

var errEditManagerNil = errors.New("edit manager is nil")

type EditRequest struct {
	Action        string        `json:"action"`
	FilePath      string        `json:"file_path"`
	Line          int           `json:"line,omitempty"`
	Column        int           `json:"column,omitempty"`
	EndLine       int           `json:"end_line,omitempty"`
	EndColumn     int           `json:"end_column,omitempty"`
	Patch         string        `json:"patch,omitempty"`
	Edits         []ReplaceEdit `json:"edits,omitempty"`
	NewName       string        `json:"new_name,omitempty"`
	NewText       string        `json:"new_text,omitempty"`
	PersistToDisk *bool         `json:"persist_to_disk,omitempty"`
	Version       int           `json:"version,omitempty"`
	Only          []string      `json:"only,omitempty"`
}

type ReplaceEdit struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

type EditHandler struct {
	registry lspmanager.Registry
	root     string
}

type editEnvelope struct {
	Success              bool                    `json:"success"`
	Action               string                  `json:"action,omitempty"`
	Status               string                  `json:"status,omitempty"`
	Message              string                  `json:"message,omitempty"`
	Applied              bool                    `json:"applied"`
	AppliedCount         int                     `json:"applied_count,omitempty"`
	Persisted            bool                    `json:"persisted"`
	RequiresApply        bool                    `json:"requires_apply,omitempty"`
	LSPSync              bool                    `json:"lsp_sync,omitempty"`
	Warning              string                  `json:"warning,omitempty"`
	WorkspaceEdit        *protocol.WorkspaceEdit `json:"workspace_edit,omitempty"`
	DiagnosticGeneration uint64                  `json:"diagnostic_generation,omitempty"`
}

func NewEditHandler(registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("lsp_edit", middleware.TierNormal, EditHandler{registry: registry}.Handle)
}

func NewEditHandlerWithRoot(root string, registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("lsp_edit", middleware.TierNormal, EditHandler{registry: registry, root: resolveRoot(root)}.Handle)
}

func HandleEdit(ctx context.Context, registry lspmanager.Registry, params json.RawMessage) (any, error) {
	return EditHandler{registry: registry}.Handle(ctx, params)
}

func (h EditHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.registry == nil {
		return nil, errEditManagerNil
	}
	req, err := decodeToolParams[EditRequest](params, decodeRaw)
	if err != nil {
		return nil, fmt.Errorf("decode lsp_edit request: %w", err)
	}
	return dispatchToolAction(ctx, "lsp_edit", req.Action, req, map[string]actionHandler[EditRequest]{
		"rename": func(ctx context.Context, req EditRequest) (any, error) {
			return h.handleRename(ctx, req)
		},
		"code_action": func(ctx context.Context, req EditRequest) (any, error) {
			return h.handleCodeAction(ctx, req)
		},
		"format": func(ctx context.Context, req EditRequest) (any, error) {
			return h.handleFormat(ctx, req)
		},
		"replace_range": func(ctx context.Context, req EditRequest) (any, error) {
			return h.handleReplaceRange(ctx, req)
		},
	})
}

func (h EditHandler) handleRename(ctx context.Context, req EditRequest) (any, error) {
	path, position, err := h.resolveRenameRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	manager, err := h.registry.GetManagerForFile(ctx, path)
	if err != nil {
		return nil, err
	}
	workspaceEdit, err := manager.Rename(ctx, path, position, req.NewName)
	if err != nil {
		return nil, err
	}
	if workspaceEdit == nil || workspaceEditSize(workspaceEdit) == 0 {
		return editEnvelope{
			Success:              true,
			Action:               "rename",
			Status:               "no_change",
			Message:              "no edits produced",
			Applied:              false,
			Persisted:            false,
			RequiresApply:        false,
			DiagnosticGeneration: manager.CurrentDiagnosticGeneration(),
		}, nil
	}
	if !persistToDisk(req.PersistToDisk) {
		if err := validateWorkspaceEditPaths(common.WorkspaceRootFromContext(ctx, h.root), workspaceEdit); err != nil {
			return nil, err
		}
		return editEnvelope{
			Success:              true,
			Action:               "rename",
			Status:               "prepared",
			Message:              "workspace edit prepared",
			Applied:              false,
			Persisted:            false,
			RequiresApply:        true,
			WorkspaceEdit:        format.WorkspaceEdit(workspaceEdit),
			DiagnosticGeneration: manager.CurrentDiagnosticGeneration(),
		}, nil
	}
	applied, err := h.applyWorkspaceEdit(ctx, manager, workspaceEdit, normalizeEditVersion(req.Version))
	if err != nil {
		return nil, err
	}
	status := "applied"
	if applied.AppliedCount == 0 {
		status = "no_change"
	}
	return editEnvelope{
		Success:              true,
		Action:               "rename",
		Status:               status,
		Message:              fmt.Sprintf("applied rename to %d file(s)", applied.AppliedCount),
		Applied:              applied.AppliedCount > 0,
		AppliedCount:         applied.AppliedCount,
		Persisted:            true,
		RequiresApply:        false,
		LSPSync:              applied.LSPSync,
		Warning:              applied.Warning,
		DiagnosticGeneration: manager.CurrentDiagnosticGeneration(),
	}, nil
}

func (h EditHandler) resolveRenameRequest(ctx context.Context, req EditRequest) (string, protocol.Position, error) {
	if strings.TrimSpace(req.NewName) == "" {
		return "", protocol.Position{}, errors.New("new_name is required for rename")
	}
	path, err := resolveWorkspacePath(common.WorkspaceRootFromContext(ctx, h.root), req.FilePath)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := requirePosition(req.Line, req.Column)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return path, position, nil
}

func (h EditHandler) resolveFilePositionRequest(ctx context.Context, req EditRequest) (string, protocol.Position, error) {
	path, err := resolveWorkspacePath(common.WorkspaceRootFromContext(ctx, h.root), req.FilePath)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := requirePosition(req.Line, req.Column)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return path, position, nil
}

func (h EditHandler) handleCodeAction(ctx context.Context, req EditRequest) (any, error) {
	path, position, err := h.resolveFilePositionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	manager, err := h.registry.GetManagerForFile(ctx, path)
	if err != nil {
		return nil, err
	}
	actions, err := manager.CodeAction(ctx, path, protocol.Range{Start: position, End: position}, req.Only)
	if err != nil {
		return nil, err
	}
	if err := validateCodeActionWorkspaceEditPaths(common.WorkspaceRootFromContext(ctx, h.root), actions); err != nil {
		return nil, err
	}
	return format.CodeActionResults(actions), nil
}

func (h EditHandler) handleFormat(ctx context.Context, req EditRequest) (any, error) {
	path, err := resolveWorkspacePath(common.WorkspaceRootFromContext(ctx, h.root), req.FilePath)
	if err != nil {
		return nil, err
	}
	manager, err := h.registry.GetManagerForFile(ctx, path)
	if err != nil {
		return nil, err
	}
	edits, err := manager.Format(ctx, path, protocol.FormattingOptions{
		TabSize:      4,
		InsertSpaces: false,
	})
	if err != nil {
		return nil, err
	}
	return format.TextEdits(edits), nil
}

func workspaceEditSize(edit *protocol.WorkspaceEdit) int {
	if edit == nil {
		return 0
	}
	count := 0
	for _, edits := range edit.Changes {
		count += len(edits)
	}
	count += len(edit.DocumentChanges)
	return count
}

func normalizeEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion
	}
	return version
}

func persistToDisk(flag *bool) bool {
	return flag == nil || *flag
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
