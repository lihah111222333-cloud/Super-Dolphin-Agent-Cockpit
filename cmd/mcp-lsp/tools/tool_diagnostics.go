package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
)

const maxReactiveBootstrap = 30
const maxDiagnosticSummaryRunes = 300
const typeScriptDeprecatedDiagnosticCode = "6385"
const diagnosticsStartupRetryCount = 5
const diagnosticsStartupRetryBaseDelay = 300 * time.Millisecond

// NewDiagnosticsHandler 创建一级 diagnostics 工具处理器。
// 诊断启动、恢复和批量收敛各有自己的 deadline，因此不再共享工具层总 deadline。
func NewDiagnosticsHandler(cfg Config) Handler {
	handler := handlerBase{
		root:             resolveRoot(cfg.WorkspaceRoot),
		registry:         cfg.Registry,
		diagnosticsCache: newDiagnosticsResultCache(),
	}
	toolHandler := newManagerToolWithoutOuterTimeout(
		"diagnostics",
		middleware.TierNormal,
		cfg.Registry,
		decodeStrict,
		func(ctx context.Context, _ lspmanager.Registry, input diagnosticsInput) (any, error) {
			filterRequest, err := validateDiagnosticsInput(input)
			if err != nil {
				return nil, err
			}
			return handler.handleDiagnostics(ctx, fileToolInput{
				FilePath:   input.FilePath,
				FilePaths:  input.FilePaths,
				LanguageID: input.LanguageID,
			}, filterRequest)
		},
	)
	return deduplicateSemanticToolHandler("diagnostics", cfg.Registry, toolHandler)
}

// fetchDiagnosticsWithRetry 获取诊断并在启动未就绪时执行一次恢复流程。
func (h handlerBase) fetchDiagnosticsWithRetry(ctx context.Context, input fileToolInput, targets []diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	uris := diagnosticTargetURIs(targets)
	if err := rejectExplicitAppManagedDiagnosticTargets(ctx, input, uris); err != nil {
		return nil, "", "", err
	}
	if shouldUseLanguageOverrideDiagnostics(input, targets) {
		return h.fetchLanguageOverrideDiagnostics(ctx, input, targets)
	}
	existingURIs := existingDiagnosticURIs(uris)
	source, err := h.bootstrapDiagnostics(ctx, existingURIs)
	if err != nil {
		return nil, "", "", err
	}
	if err := h.reopenExplicitDiagnosticTargets(ctx, input, uris); err != nil {
		return nil, "", "", err
	}
	waitResult, err := h.waitDiagnosticsWithStartupRecovery(ctx, uris, existingURIs)
	if err != nil {
		return nil, "", "", err
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

// rejectExplicitAppManagedDiagnosticTargets 在调用任何 LSP 能力前拒绝 app-managed 外部显式目标。
// 无显式目标时保留只读 manager 诊断缓存的原有语义。
func rejectExplicitAppManagedDiagnosticTargets(ctx context.Context, input fileToolInput, uris []string) error {
	if len(collectDiagnosticTargetPaths(input)) == 0 {
		return nil
	}
	appManaged, message, err := appManagedDiagnosticsOutsideWorkspace(ctx, uris)
	if err != nil {
		return err
	}
	if appManaged {
		return errors.New(message)
	}
	return nil
}

func (h handlerBase) reopenExplicitDiagnosticTargets(ctx context.Context, input fileToolInput, uris []string) error {
	if len(collectDiagnosticTargetPaths(input)) == 0 {
		return nil
	}
	return lspmanager.ReopenDocumentsForDiagnostics(ctx, h.registry, uris)
}

// bootstrapDiagnostics 为已有文件触发诊断前置 bootstrap。
// 文件列表为空时只读 manager 缓存。
func (h handlerBase) bootstrapDiagnostics(ctx context.Context, existingURIs []string) (string, error) {
	if len(existingURIs) == 0 {
		return "manager", nil
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
	bootstrapped, bootstrapErr := h.bootstrapDiagnosticDocuments(ctx, existingURIs)
	if bootstrapErr != nil {
		return diagnosticsWaitResult{}, errors.Join(waitErr, bootstrapErr)
	}
	if bootstrapped == 0 {
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

// handleDiagnostics 执行一级 diagnostics 工具的内部诊断流程。
// 它先把输入路径限制在可信 workspace roots 内，再返回按文件分组的诊断表。
func (h handlerBase) handleDiagnostics(ctx context.Context, input fileToolInput, filterRequest *diagnosticsFilterRequest) (any, error) {
	if h.registry == nil {
		return nil, errManagerUnavailable
	}
	targets, displayPaths, err := h.collectDiagnosticTargets(ctx, input)
	if err != nil {
		return nil, err
	}
	uris := diagnosticTargetURIs(targets)
	filterSpec, err := resolveDiagnosticsFilter(ctx, targets, filterRequest)
	if err != nil {
		return nil, err
	}

	items, message, err := h.loadDiagnosticsResult(ctx, input, targets, filterSpec, uris)
	if err != nil {
		return nil, err
	}
	tables, filterMeta, err := buildDiagnosticsTables(items, displayPaths, filterSpec)
	if err != nil {
		return nil, err
	}
	total := countDiagnosticRows(tables)
	hint := diagnosticsResponseHintForResult(total, filterMeta)

	if len(tables) == 0 {
		baseMessage := diagnosticsCheckedScopeMessage(input, targets)
		return diagnosticsResponse{
			Success: true,
			Data:    []diagnosticsTable{},
			Total:   0,
			Showing: 0,
			Hint:    hint,
			Meta: diagnosticsMeta{
				Message:    rustDetachedWorkspaceMessageForURIs(uris, "diagnostics", appendMessage(baseMessage, message)),
				DiffFilter: filterMeta,
			},
		}, nil
	}
	return diagnosticsResponse{
		Success: true,
		Data:    tables,
		Total:   total,
		Showing: total,
		Hint:    hint,
		Meta: diagnosticsMeta{
			Message:    message,
			DiffFilter: filterMeta,
		},
	}, nil
}

// loadDiagnosticsResult 统一处理 diagnostics 的缓存命中、失效和真实抓取。
func (h handlerBase) loadDiagnosticsResult(ctx context.Context, input fileToolInput, targets []diagnosticTarget, filter *diagnosticsFilterSpec, uris []string) ([]protocol.PublishDiagnosticsParams, string, error) {
	if h.diagnosticsCache == nil || len(targets) == 0 {
		return h.fetchDiagnosticsUncached(ctx, input, targets, uris)
	}
	return h.fetchDiagnosticsCached(ctx, input, targets, filter, uris)
}

func (h handlerBase) fetchDiagnosticsUncached(ctx context.Context, input fileToolInput, targets []diagnosticTarget, uris []string) ([]protocol.PublishDiagnosticsParams, string, error) {
	items, message, err := h.fetchDiagnosticsBySQLDialect(ctx, input, targets)
	if err != nil {
		return nil, "", rustDetachedWorkspaceError(uris, "diagnostics", err)
	}
	return items, message, nil
}

func (h handlerBase) fetchDiagnosticsCached(ctx context.Context, input fileToolInput, targets []diagnosticTarget, filter *diagnosticsFilterSpec, uris []string) ([]protocol.PublishDiagnosticsParams, string, error) {
	cacheKey, err := h.buildDiagnosticsCacheKey(ctx, input, targets, filter)
	if err != nil {
		return nil, "", err
	}
	if cached, ok := h.diagnosticsCache.get(cacheKey); ok {
		return cached.Items, cached.Message, nil
	}
	items, message, err := h.fetchDiagnosticsUncached(ctx, input, targets, uris)
	if err != nil {
		return nil, "", err
	}
	finalKey, err := h.buildDiagnosticsCacheKey(ctx, input, targets, filter)
	if err != nil {
		return nil, "", err
	}
	h.diagnosticsCache.put(finalKey, diagnosticsCacheValue{Items: items, Message: message})
	return items, message, nil
}

func (h handlerBase) buildDiagnosticsCacheKey(
	ctx context.Context,
	input fileToolInput,
	targets []diagnosticTarget,
	filter *diagnosticsFilterSpec,
) (DiagnosticsCacheKey, error) {
	digest, err := diagnosticsTargetFileDigest(targets)
	if err != nil {
		return DiagnosticsCacheKey{}, err
	}
	return BuildDiagnosticsCacheKey(
		diagnosticsCacheArguments(ctx, input, targets, filter),
		digest,
		diagnosticsServerGeneration(h.registry),
	)
}

// diagnosticsResponseHintForResult 按返回诊断数量和过滤状态生成可执行且不误报 clean 的提示。
// 空结果只表示本次调用没有返回诊断，不能继续给出针对诊断行的修复动作。
func diagnosticsResponseHintForResult(total int, filterMeta *diagnosticsFilterMeta) string {
	if total == 0 {
		if filterMeta != nil && filterMeta.Filtered > 0 {
			return fmt.Sprintf(
				"no diagnostics reported for returned changed lines; FILTERED records audit %d diagnostics outside those lines; filtered is not clean; continue focused tests",
				filterMeta.Filtered,
			)
		}
		return "no diagnostics reported; continue focused tests"
	}

	const nextStep = "next: focus on diagnostics intersecting active diff/patch hunks; inspect reported locations with native file tools, apply a native apply_patch edit, then rerun diagnostics"
	const triageStep = "process changed-line findings; classify findings as pre-existing or environment errors; rerun focused tests; diagnostics are not clean"
	if filterMeta == nil {
		return nextStep + "; " + triageStep
	}
	if filterMeta.Filtered == 0 {
		return fmt.Sprintf(
			"%s; returned rows are changed-line findings; %s; FILTERED records audit mode=%s filtered=0 (all diagnostics retained)",
			nextStep,
			triageStep,
			filterMeta.Mode,
		)
	}
	return fmt.Sprintf(
		"%s; returned rows are changed-line findings; %s; FILTERED records audit %d non-Error diagnostics outside changed lines (mode=%s filtered=%d); filtered is not clean",
		nextStep,
		triageStep,
		filterMeta.Filtered,
		filterMeta.Mode,
		filterMeta.Filtered,
	)
}

// diagnosticsCheckedScopeMessage 描述空诊断实际检查过的文件范围，避免被误解为全仓检查。
func diagnosticsCheckedScopeMessage(input fileToolInput, targets []diagnosticTarget) string {
	if strings.TrimSpace(input.FilePath) == "" && len(input.FilePaths) == 0 {
		return "Checked currently open documents (pass file_path or file_paths to scope to specific files)"
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		if path := strings.TrimSpace(target.DisplayPath); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 1 {
		return "Checked file: " + paths[0]
	}
	const previewLimit = 3
	preview := paths
	if len(preview) > previewLimit {
		preview = preview[:previewLimit]
	}
	message := fmt.Sprintf("Checked %d files: %s", len(paths), strings.Join(preview, ", "))
	if len(preview) < len(paths) {
		message += fmt.Sprintf(" (+%d more)", len(paths)-len(preview))
	}
	return message
}

// fetchDiagnosticsBySQLDialect 在批量请求含 SQLite 查询时逐文件路由，避免不同 SQL 后端共享一次诊断生命周期。
func (h handlerBase) fetchDiagnosticsBySQLDialect(ctx context.Context, input fileToolInput, targets []diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, error) {
	inputs, split, err := diagnosticsInputsBySQLDialect(ctx, input, targets)
	if err != nil {
		return nil, "", err
	}
	if !split {
		items, _, message, err := h.fetchDiagnosticsWithRetry(ctx, inputs[0], targets)
		return items, message, err
	}
	var items []protocol.PublishDiagnosticsParams
	var message string
	for index, routedInput := range inputs {
		fetched, _, fetchedMessage, err := h.fetchDiagnosticsWithRetry(ctx, routedInput, targets[index:index+1])
		if err != nil {
			return nil, "", err
		}
		items = append(items, fetched...)
		message = appendMessage(message, fetchedMessage)
	}
	return items, message, nil
}

// collectDiagnosticTargets 将 file_path/file_paths 解析为可诊断的 file URI。
// 每个目标必须通过 workspace containment 和普通文件校验，显示路径保留调用方传入的可读形式。
func (h handlerBase) collectDiagnosticTargets(ctx context.Context, input fileToolInput) ([]diagnosticTarget, map[string]string, error) {
	rawTargets := collectDiagnosticTargetPaths(input)
	if len(rawTargets) == 0 {
		return nil, nil, nil
	}

	root, roots, err := toolReadableRoots(ctx)
	if err != nil {
		return nil, nil, err
	}
	targets := make([]diagnosticTarget, 0, len(rawTargets))
	displayPaths := make(map[string]string, len(rawTargets))
	seen := make(map[string]struct{}, len(rawTargets))
	for _, target := range rawTargets {
		normalizedTarget, err := normalizeFilePathTarget(target)
		if err != nil {
			return nil, nil, err
		}
		pathInfo, err := search.ResolvePathInRoots(root, roots, normalizedTarget)
		if err != nil {
			return nil, nil, err
		}
		if err := ensureDiagnosticFile(pathInfo.AbsPath, pathInfo.DisplayPath); err != nil {
			return nil, nil, err
		}
		uri := fileURI(pathInfo.AbsPath)
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		displayPaths[uri] = diagnosticDisplayPath(normalizedTarget, pathInfo.DisplayPath)
		targets = append(targets, diagnosticTarget{
			URI:         uri,
			AbsPath:     pathInfo.AbsPath,
			DisplayPath: displayPaths[uri],
		})
	}
	return targets, displayPaths, nil
}

func collectDiagnosticTargetPaths(input fileToolInput) []string {
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

func diagnosticTargetURIs(targets []diagnosticTarget) []string {
	uris := make([]string, 0, len(targets))
	for _, target := range targets {
		uris = append(uris, target.URI)
	}
	return uris
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

// bootstrapDiagnosticDocuments 去重后重新触发诊断目标 bootstrap。
// 单个文件 bootstrap 失败会汇总为 joined error，但仍继续尝试其他目标，便于部分恢复。
func (h handlerBase) bootstrapDiagnosticDocuments(ctx context.Context, uris []string) (int, error) {
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
		if err := h.bootstrapDiagnosticDocument(ctx, uri); err != nil {
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

// bootstrapDiagnosticDocument 校验普通文件后委托 manager 状态机同步目标文档。
// 目标必须不是 symlink，registry 缺失会显式报错而不是返回空诊断。
func (h handlerBase) bootstrapDiagnosticDocument(ctx context.Context, uri string) error {
	if h.registry == nil {
		return errManagerUnavailable
	}
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
	return h.registry.BootstrapDocument(ctx, uri)
}

// appManagedDiagnosticsOutsideWorkspace 判断诊断目标是否包含 app-managed 外部数据区文件。
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
	for _, uri := range uris {
		path := format.URIToPath(uri)
		if strings.TrimSpace(path) == "" {
			return false, "", nil
		}
		pathInfo, err := search.ResolvePathInRoots(readRoot, readableRoots, path)
		if err != nil {
			return false, "", err
		}
		if !pathWithinAnyRoot(workspaceRoots, pathInfo.AbsPath) {
			return true, fmt.Sprintf(
				"diagnostics target %q is app-managed data outside workspace roots",
				pathInfo.AbsPath,
			), nil
		}
	}
	return false, "", nil
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
