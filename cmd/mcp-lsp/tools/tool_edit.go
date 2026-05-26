package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
)

const (
	defaultEditVersion              = 2
	didChangeLargeFileLineThreshold = 200
	replaceRangeFuncBodyMax         = 8 * 1024
)

var errEditManagerNil = errors.New("edit manager is nil")

type EditRequest struct {
	FilePath   string `json:"file_path"`
	LanguageID string `json:"language_id,omitempty"`
	Patch      string `json:"patch,omitempty"`
	Version    int    `json:"version,omitempty"`
}

type EditHandler struct {
	registry lspmanager.Registry
	root     string
}

type editEnvelope struct {
	Success              bool   `json:"success"`
	Status               string `json:"status,omitempty"`
	Message              string `json:"message,omitempty"`
	Applied              bool   `json:"applied"`
	AppliedCount         int    `json:"applied_count,omitempty"`
	Persisted            bool   `json:"persisted"`
	RequiresApply        bool   `json:"requires_apply,omitempty"`
	LSPSync              bool   `json:"lsp_sync,omitempty"`
	Warning              string `json:"warning,omitempty"`
	DiagnosticGeneration uint64 `json:"diagnostic_generation,omitempty"`
}

func NewEditHandler(registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("edit", middleware.TierNormal, EditHandler{registry: registry}.Handle)
}

func NewEditHandlerWithRoot(root string, registry lspmanager.Registry) middleware.Handler {
	return wrapToolHandler("edit", middleware.TierNormal, EditHandler{registry: registry, root: resolveRoot(root)}.Handle)
}

func HandleEdit(ctx context.Context, registry lspmanager.Registry, params json.RawMessage) (any, error) {
	return EditHandler{registry: registry}.Handle(ctx, params)
}

func (h EditHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.registry == nil {
		return nil, errEditManagerNil
	}
	req, err := decodeToolParams[EditRequest](params, decodeStrict)
	if err != nil {
		return nil, fmt.Errorf("decode edit request: %w", err)
	}
	return h.handleReplaceRange(ctx, req)
}

func normalizeEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion
	}
	return version
}
