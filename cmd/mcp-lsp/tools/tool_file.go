package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	defaultReadFileLimit       = 250
	maxReadFileLimit           = 2000
	maxReadFileBytes           = 2 << 20
	lspReadFileBatchMax        = 10
	lspReadFileBatchPayloadMax = 50 * 1024
	batchReadTruncatedHint     = "next: reduce file_paths or split into smaller read_file batches"
)

var errManagerUnavailable = errors.New("lsp manager is not configured; use read_file for content access or text_search for symbol lookup")

type Config struct {
	WorkspaceRoot string
	Registry      lspmanager.Registry
}
type handlerBase struct {
	root     string
	registry lspmanager.Registry
}
type resultMeta struct {
	Count   int    `json:"count"`
	Message string `json:"message,omitempty"`
	Source  string `json:"source,omitempty"`
}
type emptyListEnvelope struct {
	Success bool       `json:"success"`
	Data    []any      `json:"data"`
	Meta    resultMeta `json:"meta"`
}
type fileToolInput struct {
	Action     string   `json:"action"`
	Pos        string   `json:"pos,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	FilePath   string   `json:"file_path,omitempty"`
	FilePaths  []string `json:"file_paths,omitempty"`
	LanguageID string   `json:"language_id,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	// ExpandComments is retained as a no-op accept so legacy callers
	// passing expand_comments=true/false do not get a strict-decode
	// error. Comment expansion is now always on.
	ExpandComments *bool `json:"expand_comments,omitempty"`
}
type openFileResult struct {
	Success  bool   `json:"success"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	FilePath string `json:"file_path"`
	Bytes    int    `json:"bytes"`
}
type batchReadItem struct {
	FilePath string `json:"file_path"`
	Success  bool   `json:"success"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}
type batchReadMeta struct {
	ErrorCount     int    `json:"error_count"`
	RequestedCount int    `json:"requested_count,omitempty"`
	MaxBatch       int    `json:"max_batch,omitempty"`
	Dropped        int    `json:"dropped,omitempty"`
	Message        string `json:"message,omitempty"`
}

type batchReadResponse struct {
	Success   bool            `json:"success"`
	Data      []batchReadItem `json:"data"`
	Total     int             `json:"total"`
	Showing   int             `json:"showing"`
	Truncated bool            `json:"truncated,omitempty"`
	Hint      string          `json:"hint,omitempty"`
	Meta      batchReadMeta   `json:"meta"`
}
type indexedBatchItem struct {
	Index int
	Item  batchReadItem
}

func (h handlerBase) warnFileCWDTrace(ctx context.Context, input fileToolInput) {
	metaCWD, _ := ctx.Value(common.CwdContextKey).(string)
	pkglogger.Get().Warn("mcp-lsp: file cwd trace",
		"action", strings.TrimSpace(input.Action),
		"fallback_root", strings.TrimSpace(h.root),
		"meta_cwd", strings.TrimSpace(metaCWD),
		"effective_root", func() string { r, _ := toolWorkspaceRoot(ctx); return r }(),
		"file_path", strings.TrimSpace(input.FilePath),
		"file_paths", input.FilePaths,
	)
}

func warnFileReadFailure(action, root, rawPath string, err error) {
	pkglogger.Get().Warn("mcp-lsp: file cwd failure",
		"action", strings.TrimSpace(action),
		"effective_root", strings.TrimSpace(root),
		"file_path", strings.TrimSpace(rawPath),
		"error", err,
	)
}

// NewFileHandler 创建文件处理器。
func NewFileHandler(cfg Config) Handler {
	handler := handlerBase{
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(wrapToolHandler("file", middleware.TierNormal, handler.handleFile))
}

// handleFile 处理文件。
func (h handlerBase) handleFile(ctx context.Context, params json.RawMessage) (any, error) {
	input, err := decodeToolParams[fileToolInput](params, decodeLenient)
	if err != nil {
		return nil, err
	}
	line, err := normalizeFileInputFromPos(&input)
	if err != nil {
		return nil, err
	}
	h.warnFileCWDTrace(ctx, input)

	return dispatchToolAction(ctx, "file", input.Action, input, map[string]actionHandler[fileToolInput]{
		"open_file": func(ctx context.Context, input fileToolInput) (any, error) {
			return h.openFile(ctx, input.FilePath, input.LanguageID)
		},
		"read_file": func(ctx context.Context, input fileToolInput) (any, error) {
			req := readFileRequest{
				rawPath:    input.FilePath,
				rawPaths:   input.FilePaths,
				line:       line,
				limit:      input.Limit,
				scope:      strings.ToLower(strings.TrimSpace(input.Scope)),
				languageID: input.LanguageID,
			}
			if len(req.rawPaths) > 0 {
				return h.readBatch(ctx, req)
			}
			return h.readSingle(ctx, req)
		},
		"diagnostics": func(ctx context.Context, input fileToolInput) (any, error) {
			return h.handleDiagnostics(ctx, input)
		},
	})
}

// readFileRequest packages the read_file inputs after pos parsing so the
// readSingle / readBatch / function-mode helpers share one shape and we
// can keep the public input struct small.
type readFileRequest struct {
	rawPath    string
	rawPaths   []string
	line       int
	limit      int
	scope      string
	languageID string
}

func (r readFileRequest) wantsLineWindow() bool {
	return r.scope == "lines" || r.line <= 0
}

// normalizeFileInputFromPos parses pos="file_path:line" (line optional)
// and populates FilePath and returns the parsed line number.
func normalizeFileInputFromPos(input *fileToolInput) (int, error) {
	pos := strings.TrimSpace(input.Pos)
	if pos == "" {
		return 0, nil
	}
	filePath, line, _, _, err := parseFilePos(pos, false)
	if err != nil {
		if strings.Contains(pos, ":") {
			return 0, err
		}
		filePath = pos
	}
	if strings.TrimSpace(input.FilePath) == "" {
		input.FilePath = filePath
	}
	return line, nil
}

// openFile 打开文件。
func (h handlerBase) openFile(ctx context.Context, rawPath string, languageID string) (openFileResult, error) {
	if h.registry == nil {
		return openFileResult{}, errManagerUnavailable
	}
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return openFileResult{}, err
	}
	file, err := search.ReadToolFileContentInRoots(root, roots, rawPath, maxReadFileBytes)
	if err != nil {
		warnFileReadFailure("open_file", root, rawPath, err)
		return openFileResult{}, err
	}
	uri := fileURI(file.Path.AbsPath)
	manager, err := managerForFile(ctx, h.registry, file.Path.AbsPath, languageID)
	if err != nil {
		return openFileResult{}, err
	}
	openLanguageID := normalizeLanguageIDOverride(languageID)
	if openLanguageID == "" {
		openLanguageID = lspmanager.DetectLanguageID(file.Path.AbsPath)
	}
	if err := manager.DidOpen(ctx, uri, openLanguageID, 1, file.Content); err != nil {
		return openFileResult{}, fmt.Errorf("open_file DidOpen %s: %w", file.Path.DisplayPath, err)
	}
	return openFileResult{
		Success:  true,
		Status:   "opened",
		Message:  "opened",
		FilePath: file.Path.DisplayPath,
		Bytes:    len(file.Content),
	}, nil
}

func (h handlerBase) readSingle(ctx context.Context, req readFileRequest) (string, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return "", err
	}
	file, err := search.ReadToolFileContentInRoots(root, roots, req.rawPath, maxReadFileBytes)
	if err != nil {
		warnFileReadFailure("read_file", root, req.rawPath, err)
		return "", err
	}
	budget := middleware.ToolBudget("file")
	if req.wantsLineWindow() {
		return renderLineWindowWithinBudget(file.Path.DisplayPath, file.Content, req, lineWindowReasonExplicit, budget), nil
	}
	rendered := h.renderFunctionOrFallback(ctx, file.Path, file.Content, req, budget)
	if fitsReadTextBudget(rendered, budget) {
		return rendered, nil
	}
	return truncateRenderedReadText(rendered, file.Path.DisplayPath, req.line+1, budget), nil
}

// renderFunctionOrFallback tries to extract the enclosing function for
// req.line via DocumentSymbol. On any failure (no LSP, no symbol
// provider, line outside any function) we fall back to a default
// line window and explain the reason in the footer so the model knows
// whether retrying with a different line could help.
func (h handlerBase) renderFunctionOrFallback(ctx context.Context, path search.PathInfo, content string, req readFileRequest, budget int) string {
	reason, ok := h.tryFunctionWindow(ctx, path, content, req)
	if ok {
		return reason
	}
	return renderLineWindowWithinBudget(path.DisplayPath, content, req, reason, budget)
}

// tryFunctionWindow returns (rendered, true) on a successful function
// extraction, or (fallbackReason, false) when the caller should render a
// line window instead. The fallbackReason is a tag from
// lineWindowReason* so renderLineWindow can produce a footer that
// distinguishes "no symbol provider" from "outside any function".
// tryFunctionWindow 处理try函数window。
func (h handlerBase) tryFunctionWindow(ctx context.Context, path search.PathInfo, content string, req readFileRequest) (string, bool) {
	if h.registry == nil {
		return lineWindowReasonNoLSP, false
	}
	manager, err := managerForFile(ctx, h.registry, path.AbsPath, req.languageID)
	if err != nil {
		return lineWindowReasonNoLSP, false
	}
	symbols, err := readFileDocumentSymbols(ctx, manager, path.AbsPath)
	if err != nil || len(symbols) == 0 {
		return lineWindowReasonNoSymbols, false
	}
	start, end, ok := format.FindEnclosingFunction(symbols, req.line-1)
	if !ok {
		return lineWindowReasonOutsideFunction, false
	}
	name := enclosingFunctionName(symbols, req.line-1)
	return renderFunctionWindow(content, name, start, end, req.limit), true
}

func readFileDocumentSymbols(ctx context.Context, manager lspmanager.Manager, uri string) ([]protocol.DocumentSymbol, error) {
	if bestEffort, ok := manager.(lspmanager.BestEffortDocumentSymbolManager); ok {
		return bestEffort.DocumentSymbolBestEffort(ctx, uri)
	}
	return manager.DocumentSymbol(ctx, uri)
}

// readBatch 读取batch。
func (h handlerBase) readBatch(ctx context.Context, req readFileRequest) (batchReadResponse, error) {
	paths, meta := trimBatchPaths(req.rawPaths)
	results := make(chan indexedBatchItem, len(paths))
	var wg sync.WaitGroup
	for index, rawPath := range paths {
		wg.Add(1)
		go func(idx int, target string) {
			defer wg.Done()
			item := batchReadItem{FilePath: strings.TrimSpace(target)}
			root, roots, err := toolWorkspaceRoots(ctx)
			if err != nil {
				item.Error = err.Error()
				results <- indexedBatchItem{Index: idx, Item: item}
				return
			}
			file, err := search.ReadToolFileContentInRoots(root, roots, target, maxReadFileBytes)
			if err != nil {
				warnFileReadFailure("read_file", root, target, err)
				item.Error = err.Error()
				results <- indexedBatchItem{Index: idx, Item: item}
				return
			}
			item.FilePath = file.Path.DisplayPath
			item.Success = true
			// Batch reads always render full files (line=0 → wantsLineWindow,
			// no function lookup) so the model gets predictable content
			// for many files at once. Per-file line targeting is reserved
			// for the single-file pos="file:line" path.
			batchReq := readFileRequest{rawPath: target, limit: req.limit}
			item.Content = renderLineWindow(file.Path.DisplayPath, file.Content, batchReq, lineWindowReasonBatch)
			results <- indexedBatchItem{Index: idx, Item: item}
		}(index, rawPath)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	items := make([]indexedBatchItem, 0, len(paths))
	for {
		select {
		case <-ctx.Done():
			return batchReadResponse{}, ctx.Err()
		case item, ok := <-results:
			if !ok {
				sort.Slice(items, func(i, j int) bool {
					return items[i].Index < items[j].Index
				})
				return buildBatchReadPayload(items, meta)
			}
			items = append(items, item)
		}
	}
}

func buildBatchReadPayload(items []indexedBatchItem, meta batchReadMeta) (batchReadResponse, error) {
	resp := batchReadResponse{
		Success:   true,
		Data:      make([]batchReadItem, 0, len(items)),
		Total:     meta.RequestedCount,
		Truncated: meta.Dropped > 0,
		Meta:      meta,
	}
	var errs []error
	for _, indexed := range items {
		resp.Data = append(resp.Data, indexed.Item)
		if indexed.Item.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", indexed.Item.FilePath, indexed.Item.Error))
		}
	}
	resp.Success = len(errs) == 0
	payload := encodeBatchReadPayload(resp)
	if len(errs) > 0 {
		return payload, errors.Join(errs...)
	}
	return payload, nil
}

func trimBatchPaths(rawPaths []string) ([]string, batchReadMeta) {
	trimmed := make([]string, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		if value := strings.TrimSpace(rawPath); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	meta := batchReadMeta{RequestedCount: len(trimmed)}
	if len(trimmed) <= lspReadFileBatchMax {
		return trimmed, meta
	}
	meta.MaxBatch = lspReadFileBatchMax
	meta.Dropped = len(trimmed) - lspReadFileBatchMax
	meta.Message = fmt.Sprintf("read_file batch capped at %d files", lspReadFileBatchMax)
	return trimmed[:lspReadFileBatchMax], meta
}

// encodeBatchReadPayload 编码batchread载荷。
func encodeBatchReadPayload(resp batchReadResponse) batchReadResponse {
	finalizeBatchMeta(&resp)
	if fitsBatchPayload(resp) {
		return resp
	}

	resp.Truncated = true
	for _, budget := range []int{2048, 1024, 512, 256} {
		candidate := cloneBatchResponse(resp)
		applyBatchContentLimit(&candidate, budget)
		finalizeBatchMeta(&candidate)
		if fitsBatchPayload(candidate) {
			candidate.Meta.Message = appendMessage(candidate.Meta.Message, fmt.Sprintf("batch payload truncated to %d bytes", lspReadFileBatchPayloadMax))
			return candidate
		}
		resp = candidate
	}
	for len(resp.Data) > 1 && !fitsBatchPayload(resp) {
		resp.Data = resp.Data[:len(resp.Data)-1]
		resp.Meta.Dropped++
		finalizeBatchMeta(&resp)
	}
	if !fitsBatchPayload(resp) && len(resp.Data) == 1 {
		resp.Data[0].Content = truncateText(resp.Data[0].Content, 128)
	}
	finalizeBatchMeta(&resp)
	resp.Meta.Message = appendMessage(resp.Meta.Message, fmt.Sprintf("batch payload truncated to %d bytes", lspReadFileBatchPayloadMax))
	return resp
}

// finalizeBatchMeta 处理finalizebatchmeta。
func finalizeBatchMeta(resp *batchReadResponse) {
	resp.Showing = len(resp.Data)
	if resp.Total < len(resp.Data) {
		resp.Total = len(resp.Data)
	}
	resp.Meta.ErrorCount = 0
	for _, item := range resp.Data {
		if item.Success {
			continue
		}
		resp.Meta.ErrorCount++
	}
	if resp.Truncated && resp.Hint == "" {
		resp.Hint = batchReadTruncatedHint
	}
}

func cloneBatchResponse(resp batchReadResponse) batchReadResponse {
	clone := resp
	clone.Data = append([]batchReadItem(nil), resp.Data...)
	return clone
}

func applyBatchContentLimit(resp *batchReadResponse, maxChars int) {
	for index := range resp.Data {
		if !resp.Data[index].Success {
			continue
		}
		resp.Data[index].Content = truncateText(resp.Data[index].Content, maxChars)
	}
}

func fitsBatchPayload(resp batchReadResponse) bool {
	raw, err := json.Marshal(resp)
	return err == nil && len(raw) <= lspReadFileBatchPayloadMax
}

func resolveRoot(raw string) string {
	root, err := search.NormalizeRoot(raw)
	if err == nil {
		return root
	}
	root, _ = search.NormalizeRoot("")
	return root
}

func splitNormalizedLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

func clampOffset(offset, total int) int {
	if offset <= 0 {
		return 1
	}
	if total <= 0 {
		return 1
	}
	if offset > total {
		return total
	}
	return offset
}

func truncateText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return text[:maxChars-3] + "..."
}

func appendMessage(current, extra string) string {
	switch {
	case strings.TrimSpace(current) == "":
		return extra
	case strings.TrimSpace(extra) == "":
		return current
	default:
		return current + "; " + extra
	}
}

func fileURI(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// ToPlainText 渲染为纯文本。
func (r openFileResult) ToPlainText() string {
	if r.Success {
		return fmt.Sprintf("Successfully opened file: %s (%d bytes).", r.FilePath, r.Bytes)
	}
	return fmt.Sprintf("Failed to open file: %s. Message: %s", r.FilePath, r.Message)
}

// ToPlainText 渲染为纯文本。
func (r batchReadResponse) ToPlainText() string {
	var sb strings.Builder
	successCount := r.Showing - r.Meta.ErrorCount
	if successCount < 0 {
		successCount = 0
	}
	sb.WriteString(fmt.Sprintf("Batch Read Results: success=%t (showing %d of %d requested; %d succeeded)\n", r.Success, r.Showing, r.Total, successCount))
	if r.Meta.Message != "" {
		sb.WriteString(fmt.Sprintf("Message: %s\n", r.Meta.Message))
	}
	sb.WriteString("\n")

	for _, item := range r.Data {
		fmt.Fprintf(&sb, "===== %s =====\n", item.FilePath)
		if item.Success {
			sb.WriteString(item.Content)
		} else {
			fmt.Fprintf(&sb, "Error: %s\n", item.Error)
		}
		fmt.Fprintf(&sb, "\n===== END %s =====\n\n", item.FilePath)
	}

	if r.Truncated || r.Meta.Dropped > 0 {
		sb.WriteString("Warning: batch payload was truncated due to batch limits.\n")
	}

	return strings.TrimSpace(sb.String())
}
