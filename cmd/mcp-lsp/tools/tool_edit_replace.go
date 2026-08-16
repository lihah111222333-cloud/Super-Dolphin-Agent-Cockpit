package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	editpkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/edit"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	// edit*Timeout 控制 replace_range 的磁盘确认、函数上下文查询和 LSP 同步窗口。
	editDiskConfirmTimeout    = 750 * time.Millisecond
	editFunctionLookupTimeout = 500 * time.Millisecond
	// sourcekit-lsp 等真实服务的首次 initialize 可稳定超过旧的 2 秒窗口；
	// 同步必须服从调用方 context，同时给冷启动留下明确且可测试的上限。
	editLSPSyncTimeout  = 60 * time.Second
	editRecoveryLogFile = "mcp-lsp-edit-recovery.jsonl"
)

// replacePlan 是 build 阶段产物，记录替换后的内容和用于回显/定位的上下文。
type replacePlan struct {
	updatedContent     string
	matchedBy          string
	resolvedStart      int
	resolvedEnd        int
	resolvedLSPLine    int
	affectedStartLine  int
	affectedEndLine    int
	editContext        string
	replaced           string
	replacement        string
	functionLookupLine int
}

// replaceRangeResult 是 replace_range 成功或 no_change 时返回给工具调用方的结构化结果。
type replaceRangeResult struct {
	editEnvelope
	MatchedBy         string `json:"matched_by,omitempty"`
	ResolvedStart     int    `json:"resolved_start_offset,omitempty"`
	ResolvedEnd       int    `json:"resolved_end_offset,omitempty"`
	ResolvedLSPLine   int    `json:"resolved_lsp_line,omitempty"`
	AffectedStartLine int    `json:"affected_start_line,omitempty"`
	AffectedEndLine   int    `json:"affected_end_line,omitempty"`
	EditContext       string `json:"edit_context,omitempty"`
	Replaced          string `json:"replaced,omitempty"`
	Replacement       string `json:"replacement,omitempty"`
	ReplacedLen       int    `json:"replaced_len,omitempty"`
	ReplacementLen    int    `json:"replacement_len,omitempty"`
	FuncStart         int    `json:"func_start,omitempty"`
	FuncEnd           int    `json:"func_end,omitempty"`
	FuncBody          string `json:"func_body,omitempty"`
}

// replaceRangeFailure 保留失败时的定位线索，提示模型下一步用 file.read_file 精读。
type replaceRangeFailure struct {
	Success              bool           `json:"success"`
	Error                string         `json:"error"`
	Code                 string         `json:"code,omitempty"`
	Retryable            bool           `json:"retryable,omitempty"`
	Hint                 string         `json:"hint,omitempty"`
	Meta                 map[string]any `json:"meta,omitempty"`
	FilePath             string         `json:"file_path,omitempty"`
	LineCount            int            `json:"line_count,omitempty"`
	FuncStart            int            `json:"func_start,omitempty"`
	FuncEnd              int            `json:"func_end,omitempty"`
	FuncBody             string         `json:"func_body,omitempty"`
	DiagnosticGeneration uint64         `json:"diagnostic_generation,omitempty"`
}

// functionContext 保存一次编辑影响到的函数范围和正文摘要。
type functionContext struct {
	Start int
	End   int
	Body  string
}

// replaceSyncResult 是 LSP DidChange/diagnostic 同步确认的结果。
type replaceSyncResult struct {
	lspSync bool
	warning string
	err     error
}

// editDiskConfirmResult 是磁盘内容和 git diff 确认的结果。
type editDiskConfirmResult struct {
	confirmed bool
	warning   string
	diffBytes int
	err       error
}

// replaceUpdateDecision 汇总 LSP 和磁盘两条确认路径的最终决策。
type replaceUpdateDecision struct {
	lspSync bool
	warning string
	err     error
}

// ToPlainText 在基础编辑结果后补充命中位置和编辑上下文。
// 原始替换前后正文只保留在 structuredContent 中，避免纯文本通道被大块 diff 挤占。
func (r replaceRangeResult) ToPlainText() string {
	base := r.editEnvelope.ToPlainText()
	if !strings.EqualFold(strings.TrimSpace(r.Status), "applied") {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	if r.AffectedStartLine > 0 && r.FilePath != "" {
		fmt.Fprintf(&sb, "\nApplied at: %s:%d-L%d\n", r.FilePath, r.AffectedStartLine, r.AffectedEndLine)
	} else {
		sb.WriteByte('\n')
	}
	appendMatchedByNotice(&sb, r.MatchedBy)
	context := strings.TrimSpace(r.EditContext)
	if context == "" {
		return strings.TrimSpace(sb.String())
	}
	return strings.TrimSpace(sb.String()) + "\n\nEdit context:\n" + context
}

// ToPlainText 将 replace_range 失败结果渲染成可继续操作的文本。
// 失败时优先给出 hint、候选位置和下一步 read_file 命令，不直接倾倒整文件。
func (r replaceRangeFailure) ToPlainText() string {
	var sb strings.Builder
	header := "Tool error in \"patch_edit\""
	if r.Code != "" {
		fmt.Fprintf(&sb, "%s [%s]: %s\n", header, r.Code, strings.TrimSpace(r.Error))
	} else {
		fmt.Fprintf(&sb, "%s: %s\n", header, strings.TrimSpace(r.Error))
	}
	if hint := strings.TrimSpace(r.Hint); hint != "" {
		fmt.Fprintf(&sb, "Hint: %s\n", hint)
	}
	if r.Retryable {
		sb.WriteString("Retryable: yes\n")
	}
	appendCandidateLocations(&sb, r.Meta)
	appendFailureNextStep(&sb, r)
	return strings.TrimSpace(sb.String())
}

func appendCandidateLocations(sb *strings.Builder, meta map[string]any) {
	cands, ok := meta["candidate_locations"].([]string)
	if !ok || len(cands) == 0 {
		return
	}
	sb.WriteString("Candidate locations:\n")
	for _, entry := range cands {
		fmt.Fprintf(sb, "  - %s\n", entry)
	}
}

// appendFailureNextStep 给模型返回可直接复用的 file.read_file 下一步。
// 这里不猜具体偏移，patch 本身已有期望上下文；只给文件和行数，让调用方自行缩小窗口。
func appendFailureNextStep(sb *strings.Builder, r replaceRangeFailure) {
	if r.FilePath == "" {
		return
	}
	if r.LineCount > 0 {
		fmt.Fprintf(sb, "next: file action=read_file pos=%s:1 limit=%d (file has %d lines; narrow the window with a smaller limit)\n",
			r.FilePath, minInt(r.LineCount, 200), r.LineCount)
		return
	}
	fmt.Fprintf(sb, "next: file action=read_file pos=%s\n", r.FilePath)
}

// handleReplaceRange 执行 patch 风格文本替换，并在写入后同步 LSP。
// 该流程必须持有文件锁、保留回滚路径，并把受影响函数上下文返回给调用方排查。
func (h EditHandler) handleReplaceRange(ctx context.Context, req EditRequest) (any, error) {
	log := newEditStageLogger("replace_range", req.FilePath)
	stage := log.Started("workspace_roots")
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		log.Failed("workspace_roots", stage, err)
		return nil, err
	}
	log.Completed("workspace_roots", stage, "root", root, "roots_count", len(roots))

	stage = log.Started("resolve_path")
	path, err := resolveWorkspacePathInRoots(root, roots, req.FilePath)
	if err != nil {
		log.Failed("resolve_path", stage, err)
		return nil, err
	}
	log.setFilePath(path)
	log.Completed("resolve_path", stage)

	stage = log.Started("file_lock")
	unlock := lockEditFile(h.lockRegistry, path)
	log.Completed("file_lock", stage)
	defer func() {
		unlock()
		log.Completed("file_unlock", time.Now())
	}()

	stage = log.Started("read_file")
	file, err := readFileWithMode(path)
	if err != nil {
		log.Failed("read_file", stage, err)
		return nil, err
	}
	log.Completed("read_file", stage, "content_bytes", len(file.content), "raw_bytes", len(file.raw))

	stage = log.Started("manager_lookup", "language_id", req.LanguageID)
	manager, managerWarning, err := h.replaceRangeManager(ctx, path, req.LanguageID)
	if err != nil {
		log.Failed("manager_lookup", stage, err)
		return nil, err
	}
	log.Completed("manager_lookup", stage, "manager_available", manager != nil, "warning", managerWarning != "")

	content := file.content
	stage = log.Started("build_plan", "content_bytes", len(content), "patch_bytes", len(req.Patch))
	plan, err := buildReplacePlan(content, req)
	if err != nil {
		log.Failed("build_plan", stage, err)
		return h.replaceFailure(ctx, manager, path, content, 0, err, log), err
	}
	log.Completed("build_plan", stage,
		"matched_by", plan.matchedBy,
		"resolved_lsp_line", plan.resolvedLSPLine,
		"affected_start_line", plan.affectedStartLine,
		"affected_end_line", plan.affectedEndLine,
		"replaced_bytes", len(plan.replaced),
		"replacement_bytes", len(plan.replacement),
	)
	if plan.updatedContent == content {
		log.Skipped("apply_update", "no_change")
		return h.buildNoChangeResult(manager, path, managerWarning), nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	warning := managerWarning
	version := normalizeEditVersion(req.Version)
	stage = log.Started("apply_update", "version", version, "updated_bytes", len(updatedContent))
	lspSync, syncWarning, err := h.applyReplaceRangeUpdate(ctx, manager, path, file, updatedContent, version, log)
	if err != nil {
		log.Failed("apply_update", stage, err)
		return h.replaceFailure(ctx, manager, path, content, plan.functionLookupLine, err, log), err
	}
	log.Completed("apply_update", stage, "lsp_sync", lspSync, "sync_warning", syncWarning != "")
	if syncWarning != "" {
		warning = syncWarning
	}
	functionCtx := h.lookupFunctionContextWithLog(ctx, manager, path, plan.functionLookupLine, plan.updatedContent, log)
	log.Completed("replace_range", log.started, "result_status", "applied")
	return h.buildAppliedResult(path, plan, lspSync, warning, functionCtx), nil
}

// buildNoChangeResult 返回语义化 no_change，保留 manager 诊断代际方便调用方判断新旧。
func (h EditHandler) buildNoChangeResult(manager lspmanager.Manager, path string, warning string) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Status:               "no_change",
			Message:              "replacement did not change file content",
			Persisted:            false,
			FilePath:             path,
			Warning:              warning,
			DiagnosticGeneration: managerDiagnosticGeneration(manager),
		},
	}
}

func (h EditHandler) buildAppliedResult(path string, plan replacePlan, lspSync bool, warning string, functionCtx functionContext) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Status:               "applied",
			Message:              "replacement applied",
			AppliedCount:         1,
			Persisted:            true,
			LSPSync:              lspSync,
			FilePath:             path,
			Warning:              warning,
			DiagnosticGeneration: h.registry.CurrentDiagnosticGeneration(),
		},
		MatchedBy:         plan.matchedBy,
		ResolvedStart:     plan.resolvedStart,
		ResolvedEnd:       plan.resolvedEnd,
		ResolvedLSPLine:   plan.resolvedLSPLine,
		AffectedStartLine: plan.affectedStartLine,
		AffectedEndLine:   plan.affectedEndLine,
		EditContext:       plan.editContext,
		Replaced:          plan.replaced,
		Replacement:       plan.replacement,
		ReplacedLen:       len(plan.replaced),
		ReplacementLen:    len(plan.replacement),
		FuncStart:         functionCtx.Start,
		FuncEnd:           functionCtx.End,
		FuncBody:          functionCtx.Body,
	}
}

func (h EditHandler) replaceRangeManager(ctx context.Context, path string, languageID string) (lspmanager.Manager, string, error) {
	manager, err := managerForFile(ctx, h.registry, path, languageID)
	if err == nil {
		return manager, "", nil
	}
	if normalizeLanguageIDOverride(languageID) == "" && errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		return nil, fmt.Sprintf("LSP sync skipped: %v", err), nil
	}
	return nil, "", err
}

func (h EditHandler) replaceFailure(ctx context.Context, manager lspmanager.Manager, path string, content string, line int, err error, log *editStageLogger) replaceRangeFailure {
	functionCtx := h.lookupFunctionContextWithLog(ctx, manager, path, line, content, log)
	envelope := newToolErrorEnvelope("patch_edit", "", err)
	meta := envelope.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	if ambig := asAmbiguousMatchError(err); ambig != nil {
		meta["candidate_locations"] = candidateLocationStrings(path, ambig.Candidates)
		meta["hunk_index"] = ambig.HunkIndex + 1
	}
	return replaceRangeFailure{
		Success:              false,
		Error:                err.Error(),
		Code:                 envelope.Code,
		Retryable:            envelope.Retryable,
		Hint:                 envelope.Hint,
		Meta:                 meta,
		FilePath:             path,
		LineCount:            countLines(content),
		FuncStart:            functionCtx.Start,
		FuncEnd:              functionCtx.End,
		FuncBody:             functionCtx.Body,
		DiagnosticGeneration: managerDiagnosticGeneration(manager),
	}
}

func asAmbiguousMatchError(err error) *editpkg.AmbiguousMatchError {
	if ambig, ok := errors.AsType[*editpkg.AmbiguousMatchError](err); ok {
		return ambig
	}
	return nil
}

// candidateLocationStrings turns the typed match candidates into
// path:line:col-friendly strings the LLM can lift into a follow-up
// read_file or pinpoint a context line near. The format mirrors the
// rest of the LSP suite's plain-text output ("path:line" + optional
// match-mode tag) so the model needs no extra formatting rules.
func candidateLocationStrings(path string, candidates []editpkg.CandidateLocation) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		entry := fmt.Sprintf("%s:%d-L%d", path, candidate.StartLine, candidate.EndLine)
		if candidate.MatchedBy != "" && candidate.MatchedBy != "exact" {
			entry += " [" + candidate.MatchedBy + "]"
		}
		out = append(out, entry)
	}
	return out
}

func (h EditHandler) syncRollbackDocument(ctx context.Context, manager lspmanager.Manager, path string, content string, version int) error {
	if manager == nil {
		return nil
	}
	rollbackCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), editLSPSyncTimeout)
	defer cancel()
	_, _, err := h.syncDocument(rollbackCtx, manager, path, content, nextEditVersion(version))
	return err
}

func (h EditHandler) syncDocument(ctx context.Context, manager lspmanager.Manager, path string, content string, version int) (bool, string, error) {
	if editpkg.ShouldForceBypass(len(content)) {
		if err := manager.BootstrapDocumentOpenOnly(ctx, path); err != nil {
			return false, "", err
		}
		return true, "used bootstrap-only LSP sync", nil
	}
	change := protocol.TextDocumentContentChangeEvent{Text: content}
	if err := manager.DidChange(ctx, path, version, []protocol.TextDocumentContentChangeEvent{change}); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func withRollbackError(err error, rollbackErr error) error {
	if err == nil {
		return rollbackErr
	}
	if rollbackErr == nil {
		return err
	}
	return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
}

func nextEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion + 1
	}
	return version + 1
}

func managerDiagnosticGeneration(manager lspmanager.Manager) uint64 {
	if manager == nil {
		return 0
	}
	return manager.CurrentDiagnosticGeneration()
}

func buildReplacePlan(content string, req EditRequest) (replacePlan, error) {
	if strings.TrimSpace(req.Patch) == "" {
		return replacePlan{}, errors.New("patch_edit requires patch")
	}
	return buildPatchReplacePlan(content, req.Patch)
}

func buildPatchReplacePlan(content string, patch string) (replacePlan, error) {
	hunks, err := parsePatchHunks(patch)
	if err != nil {
		return replacePlan{}, err
	}
	return buildHunksReplacePlan(content, hunks)
}

// buildHunksReplacePlan 把已解析 hunk 匹配到当前文件并生成一次完整替换计划。
// 多 hunk 按匹配结果顺序应用，返回的上下文用于失败排查和 relaxed match 验证。
func buildHunksReplacePlan(content string, hunks []editpkg.Hunk) (replacePlan, error) {
	matches, err := editpkg.MatchContext(content, hunks)
	if err != nil {
		return replacePlan{}, err
	}
	updated := content
	contexts := make([]string, 0, len(matches))
	modes := make([]string, 0, len(matches))
	firstChangedIndex := -1
	lastChangedIndex := -1
	for idx := range hunks {
		match := matches[idx]
		hunk := hunks[idx]
		if hunk.IsSectionAnchor() {
			if !match.SectionAnchor {
				return replacePlan{}, fmt.Errorf("%w: hunk %d section anchor match contract drift", editpkg.ErrInvalidPatch, idx+1)
			}
			continue
		}
		if match.SectionAnchor {
			return replacePlan{}, fmt.Errorf("%w: hunk %d change matched as section anchor", editpkg.ErrInvalidPatch, idx+1)
		}
		if firstChangedIndex < 0 {
			firstChangedIndex = idx
		}
		lastChangedIndex = idx
		modes = append(modes, match.MatchedBy)
		contexts = append(contexts, match.EditContext)
		updated = updated[:match.ResolvedStartOffset] + hunk.NewText + updated[match.ResolvedEndOffset:]
	}
	if firstChangedIndex < 0 {
		return replacePlan{}, fmt.Errorf("%w: patch must contain at least one changed hunk", editpkg.ErrInvalidPatch)
	}
	first := matches[firstChangedIndex]
	last := matches[lastChangedIndex]
	return replacePlan{
		updatedContent:     updated,
		matchedBy:          strings.Join(uniqueStrings(modes), ","),
		resolvedStart:      first.ResolvedStartOffset,
		resolvedEnd:        last.ResolvedEndOffset,
		resolvedLSPLine:    first.ResolvedLSPLine,
		affectedStartLine:  first.AffectedStartLine,
		affectedEndLine:    last.AffectedEndLine,
		editContext:        strings.Join(contexts, "\n\n"),
		replaced:           joinHunkOldText(hunks),
		replacement:        joinHunkNewText(hunks),
		functionLookupLine: first.ResolvedLSPLine,
	}, nil
}
