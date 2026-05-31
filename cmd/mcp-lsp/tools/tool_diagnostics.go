package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
)

const maxReactiveBootstrap = 30

type diagnosticsTable struct {
	File string   `json:"file"`
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

type diagnosticsResponse struct {
	Success bool               `json:"success"`
	Data    []diagnosticsTable `json:"data"`
	Meta    resultMeta         `json:"meta"`
}

func (h handlerBase) fetchDiagnosticsWithRetry(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, string, error) {
	existingURIs := existingDiagnosticURIs(uris)
	source := "manager"
	if len(existingURIs) > 0 {
		bootstrapped, err := h.reactiveBootstrap(ctx, existingURIs)
		if err != nil {
			return nil, "", err
		}
		if bootstrapped > 0 {
			source = "reactive_bootstrap"
		}
	}
	if _, err := h.waitDiagnosticsStable(ctx, uris); err != nil {
		return nil, "", err
	}
	items, err := h.registry.Diagnostics(ctx, uris)
	if err != nil {
		return nil, "", err
	}
	if len(items) > 0 || len(uris) == 0 {
		return items, source, nil
	}
	return items, source, nil
}

func (h handlerBase) handleDiagnostics(ctx context.Context, input fileToolInput) (any, error) {
	if h.registry == nil {
		return nil, errManagerUnavailable
	}
	uris, err := h.collectDiagnosticURIs(ctx, input)
	if err != nil {
		return nil, err
	}

	items, source, err := h.fetchDiagnosticsWithRetry(ctx, uris)
	if err != nil {
		return nil, err
	}

	tables := buildDiagnosticsTables(items)
	if len(tables) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Source: source, Message: "no diagnostics"},
		}, nil
	}
	return diagnosticsResponse{
		Success: true,
		Data:    tables,
		Meta:    resultMeta{Count: len(tables), Source: source},
	}, nil
}

func (h handlerBase) collectDiagnosticURIs(ctx context.Context, input fileToolInput) ([]string, error) {
	targets := collectDiagnosticTargets(input)
	if len(targets) == 0 {
		return nil, nil
	}

	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return nil, err
	}
	uris := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		pathInfo, err := search.ResolvePathInRoots(root, roots, target)
		if err != nil {
			return nil, err
		}
		if err := ensureDiagnosticFile(pathInfo.AbsPath, pathInfo.DisplayPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		uri := fileURI(pathInfo.AbsPath)
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		uris = append(uris, uri)
	}
	return uris, nil
}

func collectDiagnosticTargets(input fileToolInput) []string {
	targets := make([]string, 0, len(input.FilePaths)+1)
	if value := strings.TrimSpace(input.FilePath); value != "" {
		targets = append(targets, value)
	}
	for _, rawPath := range input.FilePaths {
		if value := strings.TrimSpace(rawPath); value != "" {
			targets = append(targets, value)
		}
	}
	return targets
}

func existingDiagnosticURIs(uris []string) []string {
	if len(uris) == 0 {
		return nil
	}
	existing := make([]string, 0, len(uris))
	for _, uri := range uris {
		path := format.URIToPath(uri)
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			existing = append(existing, uri)
		}
	}
	return existing
}

func ensureDiagnosticFile(absPath, displayPath string) error {
	info, err := os.Lstat(absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", displayPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("file_path %q cannot be a symlink", displayPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file_path %q must reference a regular file", displayPath)
	}
	return nil
}

func (h handlerBase) waitDiagnosticsStable(ctx context.Context, uris []string) (uint64, error) {
	startGeneration := h.registry.CurrentDiagnosticGeneration()
	if err := h.registry.WaitDiagnosticsStable(ctx, uris); err != nil {
		return 0, err
	}
	currentGeneration := h.registry.CurrentDiagnosticGeneration()
	if currentGeneration != startGeneration {
		if err := h.registry.WaitDiagnosticsStable(ctx, uris); err != nil {
			return 0, err
		}
		currentGeneration = h.registry.CurrentDiagnosticGeneration()
	}
	return currentGeneration, nil
}

func (h handlerBase) reactiveBootstrap(ctx context.Context, uris []string) (int, error) {
	count := 0
	seen := make(map[string]struct{}, len(uris))
	var errs []error
	for _, uri := range uris {
		if len(seen) >= maxReactiveBootstrap {
			break
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		if err := h.registry.BootstrapDocument(ctx, uri); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", uri, err))
			continue
		}
		count++
	}
	if len(errs) > 0 {
		return count, errors.Join(errs...)
	}
	return count, nil
}

func buildDiagnosticsTables(items []protocol.PublishDiagnosticsParams) []diagnosticsTable {
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].URI < items[j].URI
	})
	tables := make([]diagnosticsTable, 0, len(items))
	for _, item := range items {
		if len(item.Diagnostics) == 0 {
			continue
		}
		rows := make([][]any, 0, len(item.Diagnostics))
		for _, diag := range item.Diagnostics {
			rows = append(rows, []any{
				format.FromLSP(diag.Range.Start.Line),
				format.FromLSP(diag.Range.Start.Character),
				diag.Severity.String(),
				diag.Message,
				diag.Source,
				diagnosticCode(diag.Code),
			})
		}
		tables = append(tables, diagnosticsTable{
			File: format.URIToPath(item.URI),
			Cols: []string{"L", "C", "sev", "msg", "src", "code"},
			Rows: rows,
		})
	}
	return tables
}

func diagnosticCode(code any) string {
	switch value := code.(type) {
	case nil:
		return ""
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func (r diagnosticsResponse) ToPlainText() string {
	if !r.Success {
		return "Diagnostics retrieval failed."
	}
	if len(r.Data) == 0 {
		return "No diagnostics found."
	}

	var sb strings.Builder
	sb.WriteString("LSP Diagnostics:\n")
	for _, table := range r.Data {
		sb.WriteString(fmt.Sprintf("File: %s\n", table.File))
		for _, row := range table.Rows {
			r.formatDiagnosticRow(&sb, row)
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

func (r diagnosticsResponse) formatDiagnosticRow(sb *strings.Builder, row []any) {
	if len(row) < 4 {
		return
	}
	lineVal, _ := row[0].(int)
	colVal, _ := row[1].(int)
	severity, _ := row[2].(string)
	msg, _ := row[3].(string)

	source := ""
	if len(row) >= 5 {
		if src, ok := row[4].(string); ok && src != "" {
			source = fmt.Sprintf(" [%s]", src)
		}
	}
	codeVal := ""
	if len(row) >= 6 {
		if c, ok := row[5].(string); ok && c != "" {
			codeVal = fmt.Sprintf(" (%s)", c)
		}
	}
	fmt.Fprintf(sb, "  L%d:%d: [%s] %s%s%s\n", lineVal, colVal, severity, msg, source, codeVal)
}
