package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
)

const maxReactiveBootstrap = 30
const maxDiagnosticSummaryRunes = 300
const typeScriptDeprecatedDiagnosticCode = "6385"
const appManagedDiagnosticsNoBootstrapSource = "app_managed_no_bootstrap"
const appManagedDiagnosticsNoBootstrapMessage = "diagnostics target is app-managed data outside workspace roots; skipped workspace LSP bootstrap and returned an empty diagnostics cache"
const diagnosticsStartupRetryCount = 5
const diagnosticsStartupRetryBaseDelay = 300 * time.Millisecond

type diagnosticsTable struct {
	File string   `json:"file"`
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

type diagnosticsMeta struct {
	Message string `json:"message,omitempty"`
}

type diagnosticsResponse struct {
	Success   bool               `json:"success"`
	Data      []diagnosticsTable `json:"data"`
	Total     int                `json:"total"`
	Showing   int                `json:"showing"`
	Truncated bool               `json:"truncated,omitempty"`
	Hint      string             `json:"hint,omitempty"`
	Meta      diagnosticsMeta    `json:"meta"`
}

type diagnosticsWaitResult struct {
	recovered                  bool
	partial                    bool
	appManagedOutsideWorkspace bool
	message                    string
}

// fetchDiagnosticsWithRetry 获取诊断并在启动未就绪时执行一次恢复流程。
// app-managed 且位于 workspace roots 外的目标不会触发 LSP bootstrap，只返回带说明的空诊断。
func (h handlerBase) fetchDiagnosticsWithRetry(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	existingURIs := existingDiagnosticURIs(uris)
	source, err := h.bootstrapDiagnostics(ctx, existingURIs)
	if err != nil {
		return nil, "", "", err
	}
	if source == appManagedDiagnosticsNoBootstrapSource {
		return emptyDiagnosticsForURIs(uris), source, appManagedDiagnosticsNoBootstrapMessage, nil
	}
	waitResult, err := h.waitDiagnosticsWithStartupRecovery(ctx, uris, existingURIs)
	if err != nil {
		return nil, "", "", err
	}
	if waitResult.appManagedOutsideWorkspace {
		return emptyDiagnosticsForURIs(uris), source, waitResult.message, nil
	}
	if waitResult.partial {
		source = "startup_recovery_partial"
	} else if waitResult.recovered {
		source = "startup_recovery"
	}
	items, err := h.registry.Diagnostics(ctx, uris)
	if err != nil {
		return nil, "", "", err
	}
	message := waitResult.message
	message = diagnosticsMessageAfterFetch(message, uris, items)
	return items, source, message, nil
}

// bootstrapDiagnostics 为已有文件触发诊断前置 bootstrap。
// 文件列表为空时只读 manager 缓存；所有目标都在 app-managed 外部区时跳过 LSP 启动。
func (h handlerBase) bootstrapDiagnostics(ctx context.Context, existingURIs []string) (string, error) {
	if len(existingURIs) == 0 {
		return "manager", nil
	}
	appManaged, _, err := appManagedDiagnosticsOutsideWorkspace(ctx, existingURIs)
	if err != nil {
		return "", err
	}
	if appManaged {
		return appManagedDiagnosticsNoBootstrapSource, nil
	}
	bootstrapped, err := h.reactiveBootstrap(ctx, existingURIs)
	if err != nil {
		return "", err
	}
	if bootstrapped > 0 {
		return "reactive_bootstrap", nil
	}
	return "manager", nil
}

func (h handlerBase) waitDiagnosticsWithStartupRecovery(ctx context.Context, uris, existingURIs []string) (diagnosticsWaitResult, error) {
	if diagnosticsStartupWaitUnsupported(existingURIs) {
		return diagnosticsWaitResult{}, nil
	}
	if _, err := h.waitDiagnosticsStable(ctx, uris); err != nil {
		return h.recoverDiagnosticsStartupWait(ctx, uris, existingURIs, err)
	}
	return diagnosticsWaitResult{}, nil
}

// recoverDiagnosticsStartupWait 在诊断等待未就绪时尝试打开目标文档后重试。
// 只有 ErrDiagnosticsNotReady 且存在真实文件时进入恢复路径，其他错误原样返回。
func (h handlerBase) recoverDiagnosticsStartupWait(ctx context.Context, uris, existingURIs []string, waitErr error) (diagnosticsWaitResult, error) {
	if !errors.Is(waitErr, lspmanager.ErrDiagnosticsNotReady) || len(existingURIs) == 0 {
		return diagnosticsWaitResult{}, waitErr
	}
	appManaged, message, err := appManagedDiagnosticsOutsideWorkspace(ctx, existingURIs)
	if err != nil {
		return diagnosticsWaitResult{}, err
	}
	if appManaged {
		return diagnosticsWaitResult{appManagedOutsideWorkspace: true, message: message}, nil
	}
	opened, openErr := h.openDiagnosticDocuments(ctx, existingURIs)
	if openErr != nil {
		return diagnosticsWaitResult{}, errors.Join(waitErr, openErr)
	}
	if opened == 0 {
		return diagnosticsWaitResult{}, waitErr
	}
	if retryErr := h.waitDiagnosticsStableWithStartupRetries(ctx, uris); retryErr != nil {
		return h.recoverPartialDiagnosticsWait(ctx, uris, retryErr)
	}
	return diagnosticsWaitResult{recovered: true}, nil
}

// waitDiagnosticsStableWithStartupRetries 在启动恢复后继续等待 diagnostics ready。
// 仅 ErrDiagnosticsNotReady 进入有限指数退避重试，其他错误必须立即返回，避免吞掉真实 LSP 失败。
func (h handlerBase) waitDiagnosticsStableWithStartupRetries(ctx context.Context, uris []string) error {
	var lastErr error
	for retry := 1; retry <= diagnosticsStartupRetryCount; retry++ {
		if err := sleepDiagnosticsRetryBackoff(ctx, retry); err != nil {
			return err
		}
		if _, err := h.waitDiagnosticsStable(ctx, uris); err != nil {
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

func sleepDiagnosticsRetryBackoff(ctx context.Context, retry int) error {
	delay := diagnosticsRetryBackoff(retry)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func diagnosticsRetryBackoff(retry int) time.Duration {
	if retry <= 0 {
		return 0
	}
	delay := diagnosticsStartupRetryBaseDelay
	for i := 1; i < retry; i++ {
		delay *= 2
	}
	return delay
}

// recoverPartialDiagnosticsWait 将批量等待失败降级为逐文件等待。
// 至少一个目标 ready 时返回 partial 状态和缺失列表，调用方可把可用诊断先展示出来。
func (h handlerBase) recoverPartialDiagnosticsWait(ctx context.Context, uris []string, batchErr error) (diagnosticsWaitResult, error) {
	if !errors.Is(batchErr, lspmanager.ErrDiagnosticsNotReady) || len(uris) <= 1 {
		return diagnosticsWaitResult{}, batchErr
	}
	ready, missing, err := h.waitDiagnosticsTargetsIndividually(ctx, uris)
	if err != nil {
		return diagnosticsWaitResult{}, err
	}
	if ready == 0 {
		return diagnosticsWaitResult{}, batchErr
	}
	if len(missing) == 0 {
		return diagnosticsWaitResult{recovered: true}, nil
	}
	return diagnosticsWaitResult{
		recovered: true,
		partial:   true,
		message:   fmt.Sprintf("partial diagnostics: %d of %d targets became ready; missing: %s", ready, ready+len(missing), strings.Join(missing, ", ")),
	}, nil
}

// waitDiagnosticsTargetsIndividually 分别等待每个目标的诊断结果稳定。
func (h handlerBase) waitDiagnosticsTargetsIndividually(ctx context.Context, uris []string) (int, []string, error) {
	ready := 0
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		if _, err := h.waitDiagnosticsStable(ctx, []string{uri}); err != nil {
			if !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
				return 0, nil, err
			}
			missing = append(missing, uri)
			continue
		}
		ready++
	}
	return ready, missing, nil
}

// handleDiagnostics 是 file diagnostics 工具入口。
// 它先把输入路径限制在可信 workspace roots 内，再返回按文件分组的诊断表。
func (h handlerBase) handleDiagnostics(ctx context.Context, input fileToolInput) (any, error) {
	if h.registry == nil {
		return nil, errManagerUnavailable
	}
	uris, displayPaths, err := h.collectDiagnosticURIs(ctx, input)
	if err != nil {
		return nil, err
	}

	items, _, message, err := h.fetchDiagnosticsWithRetry(ctx, uris)
	if err != nil {
		return nil, rustDetachedWorkspaceError(uris, "diagnostics", err)
	}

	tables := buildDiagnosticsTables(items, displayPaths)
	total := countDiagnosticRows(tables)
	if len(tables) == 0 {
		baseMessage := "no diagnostics"
		if strings.TrimSpace(input.FilePath) == "" && len(input.FilePaths) == 0 {
			baseMessage = "no diagnostics for currently open documents (pass file_path or file_paths to scope to specific files)"
		}
		return diagnosticsResponse{
			Success: true,
			Data:    []diagnosticsTable{},
			Total:   0,
			Showing: 0,
			Meta:    diagnosticsMeta{Message: rustDetachedWorkspaceMessageForURIs(uris, "diagnostics", appendMessage(baseMessage, message))},
		}, nil
	}
	return diagnosticsResponse{
		Success: true,
		Data:    tables,
		Total:   total,
		Showing: total,
		Hint:    "next: edit action=replace_range file_path=<file> patch=\"...\" or file action=read_file pos=<file>:<line>",
		Meta:    diagnosticsMeta{Message: message},
	}, nil
}

// collectDiagnosticURIs 将 file_path/file_paths 解析为可诊断的 file URI。
// 每个目标必须通过 workspace containment 和普通文件校验，显示路径保留调用方传入的可读形式。
func (h handlerBase) collectDiagnosticURIs(ctx context.Context, input fileToolInput) ([]string, map[string]string, error) {
	targets := collectDiagnosticTargets(input)
	if len(targets) == 0 {
		return nil, nil, nil
	}

	root, roots, err := toolReadableRoots(ctx)
	if err != nil {
		return nil, nil, err
	}
	uris := make([]string, 0, len(targets))
	displayPaths := make(map[string]string, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		normalizedTarget, err := normalizeFilePathTarget(target)
		if err != nil {
			return nil, nil, err
		}
		pathInfo, err := search.ResolvePathInRoots(root, roots, normalizedTarget)
		if err != nil {
			return nil, nil, err
		}
		if err := ensureDiagnosticFile(pathInfo.AbsPath, pathInfo.DisplayPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		uri := fileURI(pathInfo.AbsPath)
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		displayPaths[uri] = diagnosticDisplayPath(normalizedTarget, pathInfo.DisplayPath)
		uris = append(uris, uri)
	}
	return uris, displayPaths, nil
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

// existingDiagnosticURIs 过滤出当前磁盘上仍存在的普通文件 URI。
// symlink 和缺失文件不会进入 LSP bootstrap，避免诊断请求越过 workspace 根。
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

func diagnosticsStartupWaitUnsupported(uris []string) bool {
	if len(uris) == 0 {
		return false
	}
	for _, uri := range uris {
		if !isDetachedRustFile(format.URIToPath(uri)) {
			return false
		}
	}
	return true
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

// openDiagnosticDocuments 去重后打开一组诊断目标。
// 单个文件打开失败会汇总为 joined error，但仍继续尝试其他目标，便于部分恢复。
func (h handlerBase) openDiagnosticDocuments(ctx context.Context, uris []string) (int, error) {
	count := 0
	seen := make(map[string]struct{}, len(uris))
	var errs []error
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		if err := h.openDiagnosticDocument(ctx, uri); err != nil {
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

// openDiagnosticDocument 读取普通文件并向对应 LSP manager 发送 DidOpen。
// 目标必须不是 symlink，manager 缺失会显式报错而不是返回空诊断。
func (h handlerBase) openDiagnosticDocument(ctx context.Context, uri string) error {
	path := format.URIToPath(uri)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("diagnostic target %q cannot be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("diagnostic target %q must reference a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	manager, err := managerForFile(ctx, h.registry, path, "")
	if err != nil {
		return err
	}
	if manager == nil {
		return errManagerUnavailable
	}
	return manager.DidOpen(ctx, uri, lspmanager.DetectLanguageID(path), 1, string(content))
}

// appManagedDiagnosticsOutsideWorkspace 判断诊断目标是否全部位于 app-managed 外部数据区。
// 这种路径可被读取但不应启动 workspace LSP，以免把应用托管缓存误当用户项目。
func appManagedDiagnosticsOutsideWorkspace(ctx context.Context, uris []string) (bool, string, error) {
	root, workspaceRootsOnly, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return false, "", err
	}
	workspaceRoots, err := search.NormalizeRootSet(root, workspaceRootsOnly)
	if err != nil {
		return false, "", err
	}
	readRoot, readableRoots, err := toolReadableRoots(ctx)
	if err != nil {
		return false, "", err
	}
	allAppManaged := len(uris) > 0
	for _, uri := range uris {
		path := format.URIToPath(uri)
		if strings.TrimSpace(path) == "" {
			return false, "", nil
		}
		pathInfo, err := search.ResolvePathInRoots(readRoot, readableRoots, path)
		if err != nil {
			return false, "", err
		}
		if pathWithinAnyRoot(workspaceRoots, pathInfo.AbsPath) {
			allAppManaged = false
			break
		}
	}
	if !allAppManaged {
		return false, "", nil
	}
	return true, appManagedDiagnosticsNoBootstrapMessage, nil
}

func emptyDiagnosticsForURIs(uris []string) []protocol.PublishDiagnosticsParams {
	items := make([]protocol.PublishDiagnosticsParams, 0, len(uris))
	for _, uri := range uris {
		items = append(items, protocol.PublishDiagnosticsParams{URI: uri})
	}
	return items
}

// reactiveBootstrap 对最多 maxReactiveBootstrap 个 URI 触发按需文档 bootstrap。
// 去重后逐个执行，部分失败会合并错误返回，调用方据此决定是否进入恢复分支。
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

func diagnosticDisplayPath(raw string, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if filepath.IsAbs(trimmed) {
		absolute, err := filepath.Abs(filepath.Clean(trimmed))
		if err == nil {
			return absolute
		}
		return filepath.Clean(trimmed)
	}
	return fallback
}

func diagnosticMessageSummary(message string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(message), "\r\n", "\n")
	firstLine, _, _ := strings.Cut(normalized, "\n")
	firstLine = strings.TrimSpace(firstLine)
	runes := []rune(firstLine)
	if len(runes) <= maxDiagnosticSummaryRunes {
		return firstLine
	}
	return string(runes[:maxDiagnosticSummaryRunes]) + "…"
}

// buildDiagnosticsTables 将 LSP 诊断整理为工具响应表。
// 同一文件内相同位置/严重级别/消息的重复诊断会去重，消息会裁剪到摘要长度。
func buildDiagnosticsTables(items []protocol.PublishDiagnosticsParams, displayPaths map[string]string) []diagnosticsTable {
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
		seen := make(map[string]struct{}, len(item.Diagnostics))
		rows := make([][]any, 0, len(item.Diagnostics))
		for _, diag := range item.Diagnostics {
			key := fmt.Sprintf("%d:%d:%d:%s:%s", diag.Range.Start.Line, diag.Range.Start.Character, diag.Severity, diag.Message, diag.Source)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			rows = append(rows, []any{
				format.FromLSP(diag.Range.Start.Line),
				format.FromLSP(diag.Range.Start.Character),
				diagnosticSeverity(diag),
				diagnosticMessageSummary(diag.Message),
				diag.Source,
				diagnosticCode(diag.Code),
			})
		}
		if len(rows) == 0 {
			continue
		}
		file := format.URIToPath(item.URI)
		if displayPath := strings.TrimSpace(displayPaths[item.URI]); displayPath != "" {
			file = displayPath
		}
		tables = append(tables, diagnosticsTable{
			File: file,
			Cols: []string{"L", "C", "sev", "msg", "src", "code"},
			Rows: rows,
		})
	}
	return tables
}

// diagnosticsMessageAfterFetch 在 partial 提示已被实际诊断覆盖时清空提示。
// 如果仍有请求目标没有诊断行，则保留原提示帮助调用方判断缺口。
func diagnosticsMessageAfterFetch(message string, uris []string, items []protocol.PublishDiagnosticsParams) string {
	if !strings.Contains(message, "partial diagnostics") || len(uris) == 0 {
		return message
	}
	withRows := make(map[string]struct{}, len(items))
	for _, item := range items {
		uri := strings.TrimSpace(item.URI)
		if uri == "" || len(item.Diagnostics) == 0 {
			continue
		}
		withRows[uri] = struct{}{}
	}
	if len(withRows) == 0 {
		return message
	}
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := withRows[uri]; !ok {
			return message
		}
	}
	return ""
}

func countDiagnosticRows(tables []diagnosticsTable) int {
	total := 0
	for _, table := range tables {
		total += len(table.Rows)
	}
	return total
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

func diagnosticSeverity(diag protocol.Diagnostic) string {
	if diagnosticCode(diag.Code) == typeScriptDeprecatedDiagnosticCode {
		return protocol.SeverityHint.String()
	}
	return diag.Severity.String()
}

// ToPlainText 渲染为纯文本。
func (r diagnosticsResponse) ToPlainText() string {
	if !r.Success {
		return "Diagnostics retrieval failed."
	}
	if len(r.Data) == 0 {
		return "No diagnostics found."
	}

	var sb strings.Builder
	sb.WriteString("LSP Diagnostics:\n")
	if r.Meta.Message != "" {
		fmt.Fprintf(&sb, "Message: %s\n", r.Meta.Message)
	}
	for _, table := range r.Data {
		for _, row := range table.Rows {
			r.formatDiagnosticRow(&sb, table.File, row)
		}
	}

	return strings.TrimSpace(sb.String())
}

// formatDiagnosticRow 格式化诊断row。
func (r diagnosticsResponse) formatDiagnosticRow(sb *strings.Builder, file string, row []any) {
	if len(row) < 4 {
		return
	}
	lineVal := numericRowValue(row[0])
	colVal := numericRowValue(row[1])
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
	fmt.Fprintf(sb, "%s:%d:%d: [%s] %s%s%s\n", file, lineVal, colVal, severity, msg, source, codeVal)
}
