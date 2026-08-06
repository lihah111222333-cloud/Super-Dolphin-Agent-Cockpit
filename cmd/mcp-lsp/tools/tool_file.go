package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// Config 是 LSP 工具 handler 的公共配置，绑定工作区根目录和语言服务注册表。
type Config struct {
	WorkspaceRoot string
	Registry      lspmanager.Registry
}

// handlerBase 保存文件类工具共享的根目录和 LSP registry。
type handlerBase struct {
	root     string
	registry lspmanager.Registry
}

// resultMeta 是空列表响应的统一元信息。
type resultMeta struct {
	Count   int    `json:"count"`
	Message string `json:"message,omitempty"`
	Source  string `json:"source,omitempty"`
}

// emptyListEnvelope 是 LSP 能力不支持或无结果时的标准成功响应。
type emptyListEnvelope struct {
	Success bool       `json:"success"`
	Data    []any      `json:"data"`
	Meta    resultMeta `json:"meta"`
}

// fileToolInput 是 file 工具的外部入参，兼容 pos 与 file_path 两种定位方式。
type fileToolInput struct {
	Action     string   `json:"action"`
	Pos        string   `json:"pos,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	FilePath   string   `json:"file_path,omitempty"`
	FilePaths  []string `json:"file_paths,omitempty"`
	LanguageID string   `json:"language_id,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	// ExpandComments 仅保留为兼容字段：旧调用方传 expand_comments 不会触发严格解码错误。
	// 当前 read_file 始终自动包含相邻注释，字段值不再改变行为。
	ExpandComments *bool `json:"expand_comments,omitempty"`
}

// openFileResult 描述 open_file 对 LSP manager 的打开结果。
type openFileResult struct {
	Success  bool   `json:"success"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	FilePath string `json:"file_path"`
	Bytes    int    `json:"bytes"`
}

// batchReadItem 是 read_file 批量响应里的单文件 wire 项。
// 成功时填 Content，失败时填 Error；调用方可按 Success 对每个文件独立处理。
type batchReadItem struct {
	FilePath string `json:"file_path"`
	Success  bool   `json:"success"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}

// batchReadMeta 汇总批量读取的预算裁剪信息。
// 这些字段进入 structuredContent，方便调用方判断是否需要拆分 file_paths 重试。
type batchReadMeta struct {
	ErrorCount     int    `json:"error_count"`
	RequestedCount int    `json:"requested_count,omitempty"`
	MaxBatch       int    `json:"max_batch,omitempty"`
	Dropped        int    `json:"dropped,omitempty"`
	Message        string `json:"message,omitempty"`
}

// batchReadResponse 是批量 read_file 的结构化响应。
// Data 保留请求顺序，Meta/Hints 描述预算截断与下一步建议，兼容纯文本和 JSON 客户端。
type batchReadResponse struct {
	Success   bool            `json:"success"`
	Data      []batchReadItem `json:"data"`
	Total     int             `json:"total"`
	Showing   int             `json:"showing"`
	Truncated bool            `json:"truncated,omitempty"`
	Hint      string          `json:"hint,omitempty"`
	Meta      batchReadMeta   `json:"meta"`
}

// indexedBatchItem 保留批量读取原始顺序，避免 goroutine 返回顺序影响输出。
type indexedBatchItem struct {
	Index int
	Item  batchReadItem
}

func (h handlerBase) warnFileCWDTrace(ctx context.Context, input fileToolInput) {
	metaCWD, _ := ctx.Value(common.CwdContextKey).(string)
	effectiveRoot, _ := toolWorkspaceRoot(ctx)
	fields := []any{
		"action", strings.TrimSpace(input.Action),
	}
	fields = append(fields, platformshared.SafePathLogFields("fallback_root", strings.TrimSpace(h.root))...)
	fields = append(fields, platformshared.SafePathLogFields("meta_cwd", strings.TrimSpace(metaCWD))...)
	fields = append(fields, platformshared.SafePathLogFields("effective_root", strings.TrimSpace(effectiveRoot))...)
	fields = append(fields, platformshared.SafePathLogFields("file_path", strings.TrimSpace(input.FilePath))...)
	fields = append(fields, platformshared.SafePathLogFields("file_paths", input.FilePaths)...)
	pkglogger.Get().Warn("mcp-lsp: file cwd trace", fields...)
}

func warnFileReadFailure(action, root, rawPath string, err error) {
	fields := []any{
		"action", strings.TrimSpace(action),
		"error", err,
	}
	fields = append(fields, platformshared.SafePathLogFields("effective_root", strings.TrimSpace(root))...)
	fields = append(fields, platformshared.SafePathLogFields("file_path", strings.TrimSpace(rawPath))...)
	pkglogger.Get().Warn("mcp-lsp: file cwd failure", fields...)
}

// NewFileHandler 创建 file 工具处理器，支持 open_file、read_file 和 diagnostics。
func NewFileHandler(cfg Config) Handler {
	handler := handlerBase{
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(wrapToolHandlerWithTimeoutResolver("file", middleware.TierNormal, fileToolTimeoutTier, handler.handleFile))
}

func fileToolTimeoutTier(params json.RawMessage) time.Duration {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return middleware.TierNormal
	}
	action := normalizeAction(input.Action)
	if action == "diagnostics" {
		return toolTimeoutDisabled
	}
	return middleware.TierNormal
}

// handleFile 解码 file 工具请求，并按 action 分发到打开、读取或诊断路径。
// read_file 的 pos 会在分发前归一到 FilePath + line，避免各 action 重复解析。
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

// readFileRequest 保存 pos 解析后的 read_file 入参。
// readSingle、readBatch 和函数窗口共用这份内部结构，避免把兼容字段扩散到执行路径。
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

// normalizeFileInputFromPos 解析 pos="file_path:line" 形式，并回填 FilePath。
// line 可省略；无法按带行号位置解析但看起来像纯路径时，按文件路径兼容处理。
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

// openFile 将文件内容送入对应 LSP manager，供后续 hover/definition 等操作复用。
func (h handlerBase) openFile(ctx context.Context, rawPath string, languageID string) (openFileResult, error) {
	if h.registry == nil {
		return openFileResult{}, errManagerUnavailable
	}
	root, roots, err := toolReadableRoots(ctx)
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
	if openLanguageID == sqliteSQLLanguageID {
		openLanguageID = "sql"
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

func shouldUseLanguageOverrideDiagnostics(input fileToolInput, targets []diagnosticTarget) bool {
	return normalizeLanguageIDOverride(input.LanguageID) != "" && len(targets) > 0
}

// fetchLanguageOverrideDiagnostics 对 file_path 与 file_paths 使用同一逐目标语言覆盖生命周期。
func (h handlerBase) fetchLanguageOverrideDiagnostics(ctx context.Context, input fileToolInput, targets []diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	items := make([]protocol.PublishDiagnosticsParams, 0, len(targets))
	var message string
	for _, target := range targets {
		fetched, _, fetchedMessage, err := h.fetchSingleFileLanguageOverrideDiagnostics(ctx, input, target)
		if err != nil {
			return nil, "", "", err
		}
		items = append(items, fetched...)
		message = appendMessage(message, fetchedMessage)
	}
	return items, "language_override", message, nil
}

// fetchSingleFileLanguageOverrideDiagnostics 为 .txt 模板等扩展名不可信的单文件诊断走显式语言。
// 现存文档会按 override 打开；已删除文档先解析同一 manager，再由其 diagnostics 清理缓存和墓碑。
func (h handlerBase) fetchSingleFileLanguageOverrideDiagnostics(ctx context.Context, input fileToolInput, target diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	manager, err := managerForFile(ctx, h.registry, target.AbsPath, input.LanguageID)
	if err != nil {
		return nil, "", "", err
	}
	if _, err := os.Lstat(target.AbsPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, "", "", err
		}
		items, err := manager.Diagnostics(ctx, []string{target.URI})
		if err != nil {
			return nil, "", "", err
		}
		filtered := diagnosticsForTargetURI(target.URI, items)
		message := diagnosticsMessageAfterFetch("", []string{target.URI}, filtered)
		return filtered, "language_override", message, nil
	}
	if _, err := h.openFile(ctx, target.AbsPath, input.LanguageID); err != nil {
		return nil, "", "", err
	}
	if err := reopenManagerDocumentForDiagnostics(ctx, manager, target.URI); err != nil {
		return nil, "", "", err
	}
	if err := manager.WaitDiagnosticsStable(ctx, nil); err != nil {
		if !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
			return nil, "", "", err
		}
		if retryErr := h.waitSingleFileOverrideDiagnosticsStableWithStartupRetries(ctx, manager); retryErr != nil {
			return nil, "", "", retryErr
		}
	}
	items, err := manager.Diagnostics(ctx, nil)
	if err != nil {
		return nil, "", "", err
	}
	filtered := diagnosticsForTargetURI(target.URI, items)
	message := diagnosticsMessageAfterFetch("", []string{target.URI}, filtered)
	return filtered, "language_override", message, nil
}

// reopenManagerDocumentForDiagnostics 在显式语言覆盖路径中强制重开目标文档。
// 该路径绕过 registry 的常规诊断流程，因此直接要求已解析 scope 的 manager 执行重开。
func reopenManagerDocumentForDiagnostics(ctx context.Context, manager lspmanager.Manager, uri string) error {
	reopener, ok := manager.(lspmanager.DiagnosticDocumentReopener)
	if !ok {
		return fmt.Errorf("%w: diagnostics document reopen", lspmanager.ErrUnsupportedCapability)
	}
	if err := reopener.ReopenDocumentForDiagnostics(ctx, uri); err != nil {
		return fmt.Errorf("reopen diagnostics document %s: %w", uri, err)
	}
	return nil
}

func diagnosticsForTargetURI(uri string, items []protocol.PublishDiagnosticsParams) []protocol.PublishDiagnosticsParams {
	for _, item := range items {
		if item.URI == uri {
			return []protocol.PublishDiagnosticsParams{item}
		}
	}
	return []protocol.PublishDiagnosticsParams{{URI: uri}}
}

// waitSingleFileOverrideDiagnosticsStableWithStartupRetries 只服务显式语言单文件诊断的启动等待。
// 它复用有限退避策略，但固定等待已经解析出的 manager，避免重新按 .txt 扩展名分组。
func (h handlerBase) waitSingleFileOverrideDiagnosticsStableWithStartupRetries(ctx context.Context, manager lspmanager.Manager) error {
	var lastErr error
	for retry := 1; retry <= diagnosticsStartupRetryCount; retry++ {
		if err := sleepDiagnosticsRetryBackoff(ctx, retry); err != nil {
			return err
		}
		if err := manager.WaitDiagnosticsStable(ctx, nil); err != nil {
			if !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
				return err
			}
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// readSingle 读取单文件内容；带 line 时优先尝试函数窗口，失败再降级为行窗口。
func (h handlerBase) readSingle(ctx context.Context, req readFileRequest) (string, error) {
	root, roots, err := toolReadableRoots(ctx)
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

// tryFunctionWindow 尝试用 DocumentSymbol 抽取包含目标行的完整函数。
// 失败时返回可展示在 footer 的原因，让调用方知道是 LSP 不可用、无符号还是行号不在函数内。
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

// readFileDocumentSymbols 优先使用 best-effort symbol 查询，避免慢诊断阻塞 read_file。
func readFileDocumentSymbols(ctx context.Context, manager lspmanager.Manager, uri string) ([]protocol.DocumentSymbol, error) {
	if bestEffort, ok := manager.(lspmanager.BestEffortDocumentSymbolManager); ok {
		return bestEffort.DocumentSymbolBestEffort(ctx, uri)
	}
	return manager.DocumentSymbol(ctx, uri)
}

// readBatch 并发读取多个文件，并按原始请求顺序组装响应。
// 批量路径只返回行窗口，避免每个文件都触发 LSP 函数解析导致响应不可预测。
func (h handlerBase) readBatch(ctx context.Context, req readFileRequest) (batchReadResponse, error) {
	paths, meta := trimBatchPaths(req.rawPaths)
	results := make(chan indexedBatchItem, len(paths))
	var wg sync.WaitGroup
	for index, rawPath := range paths {
		wg.Add(1)
		currentIndex := index
		targetPath := rawPath
		safego.Go(ctx, nil, "mcp-lsp.file.read-batch.item", func(context.Context) {
			defer wg.Done()
			item := batchReadItem{FilePath: strings.TrimSpace(targetPath)}
			root, roots, err := toolReadableRoots(ctx)
			if err != nil {
				item.Error = err.Error()
				results <- indexedBatchItem{Index: currentIndex, Item: item}
				return
			}
			file, err := search.ReadToolFileContentInRoots(root, roots, targetPath, maxReadFileBytes)
			if err != nil {
				warnFileReadFailure("read_file", root, targetPath, err)
				item.Error = err.Error()
				results <- indexedBatchItem{Index: currentIndex, Item: item}
				return
			}
			item.FilePath = file.Path.DisplayPath
			item.Success = true
			// 批量读取固定走全文行窗口，不触发逐文件函数符号查询，避免多文件响应因 LSP 状态而抖动。
			// 需要精确定位时由单文件 pos="file:line" 路径承担。
			batchReq := readFileRequest{rawPath: targetPath, limit: req.limit}
			item.Content = renderLineWindow(file.Path.DisplayPath, file.Content, batchReq, lineWindowReasonBatch)
			results <- indexedBatchItem{Index: currentIndex, Item: item}
		})
	}
	safego.Go(ctx, nil, "mcp-lsp.file.read-batch.close", func(context.Context) {
		wg.Wait()
		close(results)
	})

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

// buildBatchReadPayload 合并批量读取结果；任一文件失败时仍返回可读 payload 并附带 error。
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

// trimBatchPaths 清理批量路径，并按上限丢弃超出的请求项。
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

// encodeBatchReadPayload 在输出预算内压缩批量读取内容，保留元信息和错误计数。
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

// finalizeBatchMeta 重新计算 showing/error_count，并补齐截断提示。
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

// cloneBatchResponse 浅拷贝响应并复制 Data 切片，便于尝试不同截断预算。
func cloneBatchResponse(resp batchReadResponse) batchReadResponse {
	clone := resp
	clone.Data = append([]batchReadItem(nil), resp.Data...)
	return clone
}

// applyBatchContentLimit 对成功读取项逐个裁剪正文。
func applyBatchContentLimit(resp *batchReadResponse, maxChars int) {
	for index := range resp.Data {
		if !resp.Data[index].Success {
			continue
		}
		resp.Data[index].Content = truncateText(resp.Data[index].Content, maxChars)
	}
}

// fitsBatchPayload 判断结构化 batch 响应是否落在 payload 字节预算内。
// 预算以 JSON 后字节数为准，和实际 structuredContent 输出路径一致。
func fitsBatchPayload(resp batchReadResponse) bool {
	raw, err := json.Marshal(resp)
	return err == nil && len(raw) <= lspReadFileBatchPayloadMax
}

// resolveRoot 规范化工作区根目录；无效输入回退到当前目录规范化结果。
func resolveRoot(raw string) string {
	root, err := search.NormalizeRoot(raw)
	if err == nil {
		return root
	}
	root, _ = search.NormalizeRoot("")
	return root
}

// splitNormalizedLines 统一 CRLF 后切行，空文件返回一个空行占位。
func splitNormalizedLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

// clampOffset 把 1-based 行号限制在文件范围内。
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

// truncateText 以字符数量裁剪文本，并保留省略号。
func truncateText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return text[:maxChars-3] + "..."
}

// appendMessage 合并批量响应中的附加说明。
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

// fileURI 把本地绝对路径转换成 LSP file URI。
func fileURI(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}

// minInt 返回两个整数中的较小值。
func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// ToPlainText 将 open_file 结果渲染成简短文本，供纯文本客户端展示。
func (r openFileResult) ToPlainText() string {
	if r.Success {
		return fmt.Sprintf("Successfully opened file: %s (%d bytes).", r.FilePath, r.Bytes)
	}
	return fmt.Sprintf("Failed to open file: %s. Message: %s", r.FilePath, r.Message)
}

// ToPlainText 将批量读取结果渲染成带文件分隔符的文本。
func (r batchReadResponse) ToPlainText() string {
	var sb strings.Builder
	successCount := r.Showing - r.Meta.ErrorCount
	successCount = max(successCount, 0)
	fmt.Fprintf(&sb, "Batch Read Results: success=%t (showing %d of %d requested; %d succeeded)\n", r.Success, r.Showing, r.Total, successCount)
	if r.Meta.Message != "" {
		fmt.Fprintf(&sb, "Message: %s\n", r.Meta.Message)
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
