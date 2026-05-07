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
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultReadFileLimit       = 300
	maxReadFileLimit           = 2000
	maxReadFileBytes           = 2 << 20
	lspReadFileBatchMax        = 10
	lspReadFileBatchPayloadMax = 16 * 1024
)

var errManagerUnavailable = errors.New("lsp manager is not configured")

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
	Action    string   `json:"action"`
	FilePath  string   `json:"file_path,omitempty"`
	FilePaths []string `json:"file_paths,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Limit     int      `json:"limit,omitempty"`
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
	Count          int    `json:"count"`
	SuccessCount   int    `json:"success_count"`
	ErrorCount     int    `json:"error_count"`
	Truncated      bool   `json:"truncated,omitempty"`
	RequestedCount int    `json:"requested_count,omitempty"`
	MaxBatch       int    `json:"max_batch,omitempty"`
	Dropped        int    `json:"dropped,omitempty"`
	Message        string `json:"message,omitempty"`
}

type batchReadResponse struct {
	Success bool            `json:"success"`
	Data    []batchReadItem `json:"data"`
	Meta    batchReadMeta   `json:"meta"`
}
type indexedBatchItem struct {
	Index int
	Item  batchReadItem
}

func NewFileHandler(cfg Config) Handler {
	handler := handlerBase{
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(middleware.WithOutputBudget(
		wrapToolHandler("lsp_file", middleware.TierNormal, handler.handleFile),
		middleware.Budget{Message: "lsp_file response exceeded output budget"},
	))
}

func (h handlerBase) handleFile(ctx context.Context, params json.RawMessage) (any, error) {
	input, err := decodeToolParams[fileToolInput](params, decodeLenient)
	if err != nil {
		return nil, err
	}
	return dispatchToolAction(ctx, "lsp_file", input.Action, input, map[string]actionHandler[fileToolInput]{
		"open_file": func(ctx context.Context, input fileToolInput) (any, error) {
			return h.openFile(ctx, input.FilePath)
		},
		"read_file": func(ctx context.Context, input fileToolInput) (any, error) {
			if len(input.FilePaths) > 0 {
				return h.readBatch(ctx, input.FilePaths, input.Offset, input.Limit)
			}
			return h.readSingle(ctx, input.FilePath, input.Offset, input.Limit)
		},
		"diagnostics": func(ctx context.Context, input fileToolInput) (any, error) {
			return h.handleDiagnostics(ctx, input)
		},
	})
}

func (h handlerBase) openFile(ctx context.Context, rawPath string) (openFileResult, error) {
	if h.registry == nil {
		return openFileResult{}, errManagerUnavailable
	}
	file, err := search.ReadToolFileContent(common.WorkspaceRootFromContext(ctx, h.root), rawPath, maxReadFileBytes)
	if err != nil {
		return openFileResult{}, err
	}
	uri := fileURI(file.Path.AbsPath)
	manager, err := h.registry.GetManagerForFile(ctx, file.Path.AbsPath)
	if err == nil {
		// Only open file in the language server if a manager exists for it
		_ = manager.DidOpen(ctx, uri, lspmanager.DetectLanguageID(file.Path.AbsPath), 1, file.Content)
	}
	return openFileResult{
		Success:  true,
		Status:   "opened",
		Message:  "opened",
		FilePath: file.Path.DisplayPath,
		Bytes:    len(file.Content),
	}, nil
}

func (h handlerBase) readSingle(ctx context.Context, rawPath string, offset, limit int) (string, error) {
	file, err := search.ReadToolFileContent(common.WorkspaceRootFromContext(ctx, h.root), rawPath, maxReadFileBytes)
	if err != nil {
		return "", err
	}
	return renderReadContent(file.Content, offset, limit), nil
}

func (h handlerBase) readBatch(ctx context.Context, rawPaths []string, offset, limit int) (batchReadResponse, error) {
	paths, meta := trimBatchPaths(rawPaths)
	results := make(chan indexedBatchItem, len(paths))
	var wg sync.WaitGroup
	for index, rawPath := range paths {
		wg.Add(1)
		go func(idx int, target string) {
			defer wg.Done()
			item := batchReadItem{FilePath: strings.TrimSpace(target)}
			file, err := search.ReadToolFileContent(common.WorkspaceRootFromContext(ctx, h.root), target, maxReadFileBytes)
			if err != nil {
				item.Error = err.Error()
				results <- indexedBatchItem{Index: idx, Item: item}
				return
			}
			item.FilePath = file.Path.DisplayPath
			item.Success = true
			item.Content = renderReadContent(file.Content, offset, limit)
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
				resp := batchReadResponse{Success: true, Data: make([]batchReadItem, 0, len(items)), Meta: meta}
				for _, indexed := range items {
					resp.Data = append(resp.Data, indexed.Item)
				}
				return encodeBatchReadPayload(resp), nil
			}
			items = append(items, item)
		}
	}
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
	meta.Truncated = true
	meta.Message = fmt.Sprintf("read_file batch capped at %d files", lspReadFileBatchMax)
	return trimmed[:lspReadFileBatchMax], meta
}

func encodeBatchReadPayload(resp batchReadResponse) batchReadResponse {
	finalizeBatchMeta(&resp)
	if fitsBatchPayload(resp) {
		return resp
	}

	resp.Meta.Truncated = true
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

func finalizeBatchMeta(resp *batchReadResponse) {
	resp.Meta.Count = len(resp.Data)
	resp.Meta.SuccessCount = 0
	resp.Meta.ErrorCount = 0
	for _, item := range resp.Data {
		if item.Success {
			resp.Meta.SuccessCount++
			continue
		}
		resp.Meta.ErrorCount++
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

func renderReadContent(content string, offset, limit int) string {
	lines := splitNormalizedLines(content)
	start := clampOffset(offset, len(lines))
	limit = shared.ClampLimit(limit, 1, maxReadFileLimit, defaultReadFileLimit)
	end := minInt(start+limit-1, len(lines))
	segment := strings.Join(lines[start-1:end], "\n")
	rendered := format.RenderLineNumberedText(segment, start)
	if start == 1 && end == len(lines) {
		return rendered
	}
	return fmt.Sprintf("%s\n\n...[showing lines %d-%d of %d total, use offset=%d to continue]", rendered, start, end, len(lines), end+1)
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
