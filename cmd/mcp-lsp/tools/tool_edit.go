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
	MatchedBy            string `json:"matched_by_summary,omitempty"`
	FilePath             string `json:"file_path,omitempty"`
	AffectedStartLine    int    `json:"affected_start_line,omitempty"`
	AffectedEndLine      int    `json:"affected_end_line,omitempty"`
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

// editStatusText distinguishes the three terminal states a model cares
// about: applied (real change), no-op (patch parsed but resulted in
// identical content; usually a sign the model copied an already-applied
// patch), and failed. Collapsing no-op into SUCCESS used to silently
// trick callers into thinking they had moved the world.
func editStatusText(e editEnvelope) string {
	if !e.Success {
		return "FAILED"
	}
	if !e.Applied || strings.EqualFold(strings.TrimSpace(e.Status), "no_change") {
		return "NO_CHANGE"
	}
	return "SUCCESS"
}

func appendEditApplyStatus(sb *strings.Builder, e editEnvelope) {
	if e.Applied {
		sb.WriteString(fmt.Sprintf("Applied: true (%d replacements", e.AppliedCount))
		if e.Persisted {
			sb.WriteString(", persisted to disk")
		}
		if e.LSPSync {
			sb.WriteString(", LSP synced")
		}
		sb.WriteString(")\n")
		if e.AffectedStartLine > 0 && e.FilePath != "" {
			fmt.Fprintf(sb, "Applied at: %s:%d-L%d\n", e.FilePath, e.AffectedStartLine, e.AffectedEndLine)
		}
		appendMatchedByNotice(sb, e.MatchedBy)
		return
	}
	if e.Success {
		// no_change branch: patch matched but normalised away to the
		// existing content. Tell the model so they don't think the
		// edit landed.
		sb.WriteString("Applied: false (patch matched but normalised away — current file already equals the requested NewText; verify your intended diff)\n")
		return
	}
	if e.RequiresApply {
		sb.WriteString("Applied: false (requires manual apply)\n")
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

func appendEditWarnings(sb *strings.Builder, e editEnvelope) {
	if e.Warning != "" {
		sb.WriteString(fmt.Sprintf("Warning: %s\n", e.Warning))
	}
	if e.Success && e.FilePath != "" {
		fmt.Fprintf(sb, "Next step: file action=diagnostics file_path=%s\n", e.FilePath)
	}
}
