package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/search"
)

const (
	defaultSearchResults = 30
	maxSearchResults     = 50
)

type grepToolInput struct {
	Action        string `json:"action"`
	Query         string `json:"query,omitempty"`
	Path          string `json:"path,omitempty"`
	Glob          string `json:"glob,omitempty"`
	Language      string `json:"language,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type grepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

type grepResponse struct {
	Files     map[string]grepFileRows `json:"files"`
	Total     int                     `json:"total"`
	Showing   int                     `json:"showing"`
	Truncated bool                    `json:"truncated,omitempty"`
	Hint      string                  `json:"hint,omitempty"`
}

type documentSymbolProvider struct {
	ctx     context.Context
	manager gopls.Manager
	cache   map[string][]protocol.DocumentSymbol
}

func NewGrepHandler(cfg Config) Handler {
	handler := handlerBase{
		root:    resolveRoot(cfg.WorkspaceRoot),
		manager: cfg.Manager,
	}
	base := func(ctx context.Context, params json.RawMessage) (any, error) {
		return handler.handleGrep(ctx, params)
	}
	return Handler(middleware.WithOutputBudget(middleware.Handler(base), middleware.Budget{
		Message: "lsp_grep response exceeded output budget",
	}))
}

func (h handlerBase) handleGrep(ctx context.Context, params json.RawMessage) (any, error) {
	var input grepToolInput
	if err := decodeInput(params, &input); err != nil {
		return nil, err
	}
	limit := normalizeSearchLimit(input.MaxResults)

	var (
		matches []search.SearchMatch
		err     error
	)
	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "text_search":
		matches, err = search.SearchText(ctx, search.TextSearchOptions{
			Root:          h.root,
			Path:          input.Path,
			Glob:          input.Glob,
			Query:         input.Query,
			Regex:         input.Regex,
			CaseSensitive: input.CaseSensitive,
			MaxResults:    limit,
			MaxFileBytes:  maxReadFileBytes,
		})
	case "ast_search":
		matches, err = search.SearchAST(ctx, search.ASTSearchOptions{
			Root:         h.root,
			Path:         input.Path,
			Glob:         input.Glob,
			Query:        input.Query,
			Language:     input.Language,
			MaxResults:   limit,
			MaxFileBytes: maxReadFileBytes,
		})
	default:
		return nil, fmt.Errorf("unsupported lsp_grep action %q", input.Action)
	}
	if err != nil {
		return nil, err
	}

	filtered, total, truncated := search.FilterAndCapSearchMatches(matches, limit)
	h.attachFuncRanges(ctx, filtered)
	if len(filtered) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: "no matches found"},
		}, nil
	}
	return buildGrepResponse(filtered, total, truncated), nil
}

func (h handlerBase) attachFuncRanges(ctx context.Context, matches []search.SearchMatch) {
	if h.manager == nil || len(matches) == 0 {
		return
	}
	provider := &documentSymbolProvider{
		ctx:     ctx,
		manager: h.manager,
		cache:   make(map[string][]protocol.DocumentSymbol),
	}
	lastRange := make(map[string][2]int)
	for index := range matches {
		start, end, _, ok := format.ResolveEnclosingFunctionRange(provider, fileURI(matches[index].AbsPath), matches[index].Line-1, lastRange)
		if !ok {
			continue
		}
		matches[index].FuncStart = start
		matches[index].FuncEnd = end
	}
}

func (p *documentSymbolProvider) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	if cached, ok := p.cache[absPath]; ok {
		return cached, nil
	}
	symbols, err := p.manager.DocumentSymbol(p.ctx, fileURI(absPath))
	if err != nil {
		return nil, err
	}
	p.cache[absPath] = symbols
	return symbols, nil
}

func buildGrepResponse(matches []search.SearchMatch, total int, truncated bool) grepResponse {
	files := make(map[string]grepFileRows, len(matches))
	hint := ""
	for _, match := range matches {
		row := []any{match.Line, match.Col, match.Text, match.FuncStart, match.FuncEnd}
		if match.FuncStart > 0 && match.FuncEnd >= match.FuncStart {
			hint = "step 2: use the returned func_start/func_end to read that function range, e.g. read_file(offset=func_start, limit=func_end-func_start+1)"
		}
		block := files[match.File]
		if len(block.Cols) == 0 {
			block.Cols = []string{"line", "col", "text", "func_start", "func_end"}
		}
		block.Rows = append(block.Rows, row)
		files[match.File] = block
	}
	return grepResponse{
		Files:     files,
		Total:     total,
		Showing:   len(matches),
		Truncated: truncated,
		Hint:      hint,
	}
}

func normalizeSearchLimit(requested int) int {
	switch {
	case requested <= 0:
		return defaultSearchResults
	case requested > maxSearchResults:
		return maxSearchResults
	default:
		return requested
	}
}
