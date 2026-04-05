package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/search"
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
	if _, err := h.waitDiagnosticsStable(ctx, uris); err != nil {
		return nil, "", err
	}
	items, err := h.registry.Diagnostics(ctx, uris)
	if err != nil {
		return nil, "", err
	}
	if len(items) > 0 || len(uris) == 0 {
		return items, "manager", nil
	}
	bootstrapped, err := h.reactiveBootstrap(ctx, uris)
	if err != nil {
		return nil, "", err
	}
	if bootstrapped == 0 {
		return items, "manager", nil
	}
	if _, err := h.waitDiagnosticsStable(ctx, uris); err != nil {
		return nil, "", err
	}
	items, err = h.registry.Diagnostics(ctx, uris)
	if err != nil {
		return nil, "", err
	}
	return items, "reactive_bootstrap", nil
}

func (h handlerBase) handleDiagnostics(ctx context.Context, input fileToolInput) (any, error) {
	if h.registry == nil {
		return nil, errManagerUnavailable
	}
	uris, err := h.collectDiagnosticURIs(input)
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

func (h handlerBase) collectDiagnosticURIs(input fileToolInput) ([]string, error) {
	targets := make([]string, 0, len(input.FilePaths)+1)
	if value := strings.TrimSpace(input.FilePath); value != "" {
		targets = append(targets, value)
	}
	for _, rawPath := range input.FilePaths {
		if value := strings.TrimSpace(rawPath); value != "" {
			targets = append(targets, value)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}

	uris := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		pathInfo, err := search.ResolvePath(h.root, target)
		if err != nil {
			return nil, err
		}
		if err := ensureDiagnosticFile(pathInfo.AbsPath, pathInfo.DisplayPath); err != nil {
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
	var firstErr error
	for _, uri := range uris {
		if len(seen) >= maxReactiveBootstrap {
			break
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		if err := h.registry.BootstrapDocument(ctx, uri); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		count++
	}
	if count == 0 && firstErr != nil {
		return 0, firstErr
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
