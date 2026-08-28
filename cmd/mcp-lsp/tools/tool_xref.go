package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const externalConsumptionSampleLimit = 5

type fileReferenceGroup struct {
	normPath string
	items    []protocol.LocationResult
}

// referenceFileStats 描述一个文件在截断前的总数和本次实际展示数。
// sampled 标记展示数是否因外部文件采样上限而减少，而不是因 max_results 减少。
type referenceFileStats struct {
	normPath string
	total    int
	showing  int
	sampled  bool
}

// sampledReferences 保存引用结果及其两类截断证据。
type sampledReferences struct {
	results          []protocol.LocationResult
	files            []referenceFileStats
	externalSampling bool
	maxResults       bool
}

// sampleReferencesByFile 按文件分组并对外部消费端采样保留最多 5 行。
//
// 外部文件的固定采样是输出预算策略，不应被报告成 max_results 截断；否则
// 调用方增大 max_results 仍然拿不到被采样掉的行，也无法知道该如何恢复。
func sampleReferencesByFile(results []protocol.LocationResult, currentFilePath string, limit int) sampledReferences {
	currentNorm := normalizePathForComparison(currentFilePath)
	groups := groupReferencesByFile(results)
	sampled, stats, externalSampling := sampleReferenceGroups(groups, currentNorm)
	beforeMaxResults := len(sampled)
	finalResults := limitSlice(sampled, limit)
	setReferenceShowing(stats, finalResults)
	return sampledReferences{
		results:          finalResults,
		files:            stats,
		externalSampling: externalSampling,
		maxResults:       beforeMaxResults > len(finalResults),
	}
}

func groupReferencesByFile(results []protocol.LocationResult) []*fileReferenceGroup {
	var groups []*fileReferenceGroup
	groupMap := make(map[string]*fileReferenceGroup)
	for _, res := range results {
		loc := res.PrimaryLocation()
		if loc == nil {
			continue
		}
		norm := normalizePathForComparison(loc.URI)
		group, seen := groupMap[norm]
		if !seen {
			group = &fileReferenceGroup{normPath: norm}
			groupMap[norm] = group
			groups = append(groups, group)
		}
		group.items = append(group.items, res)
	}
	return groups
}

// sampleReferenceGroups 对外部文件应用固定预览上限并记录采样证据。
func sampleReferenceGroups(groups []*fileReferenceGroup, currentNorm string) ([]protocol.LocationResult, []referenceFileStats, bool) {
	var sampled []protocol.LocationResult
	stats := make([]referenceFileStats, 0, len(groups))
	externalSampling := false
	for _, group := range groups {
		items := group.items
		stat := referenceFileStats{normPath: group.normPath, total: len(group.items)}
		if currentNorm == "" || group.normPath != currentNorm {
			if len(items) > externalConsumptionSampleLimit {
				items = limitSlice(items, externalConsumptionSampleLimit)
				stat.sampled = true
				externalSampling = true
			}
		}
		stats = append(stats, stat)
		sampled = append(sampled, items...)
	}
	return sampled, stats, externalSampling
}

func setReferenceShowing(stats []referenceFileStats, results []protocol.LocationResult) {
	showingByFile := make(map[string]int, len(stats))
	for _, item := range results {
		if loc := item.PrimaryLocation(); loc != nil {
			showingByFile[normalizePathForComparison(loc.URI)]++
		}
	}
	for index := range stats {
		stats[index].showing = showingByFile[stats[index].normPath]
	}
}

// normalizePathForComparison 将文件路径和 file URI 归一化为可比较的斜杠路径。
func normalizePathForComparison(pathOrURI string) string {
	if strings.TrimSpace(pathOrURI) == "" {
		return ""
	}
	p := format.URIToPath(pathOrURI)
	if strings.TrimSpace(p) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// referenceHintFilePath 将每文件统计中的绝对路径压缩到工具作用域，避免 HINT
// 既泄露无关路径，又让调用方能把该文件直接用于下一次收窄查询。
func referenceHintFilePath(ctx context.Context, normPath string) string {
	if normPath == "" {
		return "<unknown>"
	}
	if compact := compactToolFilePath(ctx, normPath); compact != "" {
		return compact
	}
	return filepath.ToSlash(filepath.Clean(normPath))
}

// referenceFilesMetadata 生成现有 Hint 字段内的机器可读每文件统计。
// 每项都保留原始 total/showing，并显式标记 external_sampling，避免把采样
// 误读成 max_results 截断或误称为全集。
func referenceFilesMetadata(ctx context.Context, files []referenceFileStats) (string, []string) {
	entries := make([]string, 0, len(files))
	sampledFiles := make([]string, 0, len(files))
	for _, file := range files {
		path := referenceHintFilePath(ctx, file.normPath)
		entry := fmt.Sprintf("file=%s,total=%d,showing=%d", path, file.total, file.showing)
		if file.sampled {
			entry += ",sampled=external_sampling"
			sampledFiles = append(sampledFiles, path)
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, ";"), sampledFiles
}

// referenceTruncationMetadata 返回截断原因和每文件展示证据。
// Hint 是 xref 当前唯一可扩展的输出字段，因此保持短 key=value 片段，既可
// 供人阅读，也可由调用方按分号和逗号解析；Data/Total/Showing 的旧结构不变。
func referenceTruncationMetadata(ctx context.Context, sampled sampledReferences, limit int) string {
	reasons := make([]string, 0, 2)
	if sampled.maxResults {
		reasons = append(reasons, "max_results")
	}
	if sampled.externalSampling {
		reasons = append(reasons, "external_sampling")
	}
	if len(reasons) == 0 {
		return ""
	}
	metadata, sampledFiles := referenceFilesMetadata(ctx, sampled.files)
	parts := []string{
		fmt.Sprintf("truncation: reason=%s", strings.Join(reasons, ",")),
		fmt.Sprintf("max_results=%d", limit),
		fmt.Sprintf("per_file=[%s]", metadata),
	}
	if len(sampledFiles) > 0 {
		parts = append(parts, fmt.Sprintf("sampled_files=[%s]", strings.Join(sampledFiles, ",")))
	}
	return strings.Join(parts, "; ")
}

// referenceSummaryHint 保留旧的总量摘要，同时让摘要与 per_file 的展示统计
// 一致。它只描述 total/showing，不宣称结果覆盖全集。
func referenceSummaryHint(filePath string, total int, sampled sampledReferences) string {
	if total <= 0 {
		return ""
	}
	currentNorm := normalizePathForComparison(filePath)
	moreInCurrentFile := 0
	for _, file := range sampled.files {
		if file.normPath == currentNorm {
			moreInCurrentFile = max(0, file.total-file.showing)
			break
		}
	}

	var parts []string
	if moreInCurrentFile > 0 {
		parts = append(parts, fmt.Sprintf("+ %d more references in this file", moreInCurrentFile))
		parts = append(parts, fmt.Sprintf("total %d across %d files", total, len(sampled.files)))
	} else if len(sampled.files) > 1 {
		parts = append(parts, fmt.Sprintf("total %d across %d files", total, len(sampled.files)))
	} else if total > len(sampled.results) {
		parts = append(parts, fmt.Sprintf("+ %d more references, total %d across %d files", total-len(sampled.results), total, len(sampled.files)))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("(%s)", strings.Join(parts, ", "))
}

// referenceNextHint 给出能区分两类截断的恢复动作。外部采样不会因增大
// max_results 消失，必须按 HINT 中列出的具体文件收窄重查。
func referenceNextHint(ctx context.Context, sampled sampledReferences, importedSource string) string {
	_, sampledFiles := referenceFilesMetadata(ctx, sampled.files)
	if sampled.externalSampling {
		if len(sampledFiles) == 0 {
			sampledFiles = []string{"<sampled-file>"}
		}
		var hint string
		if sampled.maxResults {
			hint = fmt.Sprintf("next: increase max_results for max_results; narrow the target with pos=<file>:<line>:<col> for external_sampling; external_sampling cannot be recovered by increasing max_results (sampled_files=%s)", strings.Join(sampledFiles, ","))
		} else {
			hint = fmt.Sprintf("next: narrow the target with pos=<file>:<line>:<col> for external_sampling; external_sampling cannot be recovered by increasing max_results (sampled_files=%s)", strings.Join(sampledFiles, ","))
		}
		if importedSource != "" {
			hint += fmt.Sprintf("; symbol imported from %s", importedSource)
		}
		return hint
	}
	if sampled.maxResults {
		if importedSource != "" {
			return fmt.Sprintf("next: increase max_results; symbol imported from %s", importedSource)
		}
		return "next: increase max_results or narrow the target with pos=<file>:<line>:<col>"
	}
	return ""
}

// appendReferenceHint 在不破坏既有自然语言提示的前提下追加结构化细节。
func appendReferenceHint(base, detail string) string {
	base = strings.TrimSpace(base)
	detail = strings.TrimSpace(detail)
	if base == "" {
		return detail
	}
	if detail == "" {
		return base
	}
	return base + "; " + detail
}

// xrefParams 是 xref 工具的入参，覆盖引用、调用层级和类型层级查询。
type xrefParams struct {
	Action             string `json:"action"`
	Pos                string `json:"pos"`
	LanguageID         string `json:"language_id,omitempty"`
	Direction          string `json:"direction"`
	IncludeDeclaration *bool  `json:"include_declaration"`
	MaxResults         int    `json:"max_results"`
}

// NewXRefHandler 创建 xref 工具处理器，并为引用结果补充函数范围。
func NewXRefHandler(registry lspmanager.Registry) ToolHandler {
	handler := newManagerToolWithoutOuterTimeout("xref", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req xrefParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{
			Pos:        req.Pos,
			LanguageID: req.LanguageID,
		})
		if err != nil {
			return nil, err
		}
		manager, err := managerForFile(ctx, registry, filePath, req.LanguageID)
		if err != nil {
			return nil, err
		}
		funcEnricher := newFuncRangeEnricher(ctx, registry)
		return dispatchToolAction(ctx, "xref", req.Action, req, map[string]actionHandler[xrefParams]{
			"references": func(ctx context.Context, req xrefParams) (any, error) {
				return runReferences(ctx, manager, filePath, position, req, funcEnricher)
			},
			"call_hierarchy": func(ctx context.Context, req xrefParams) (any, error) {
				return runCallHierarchy(ctx, manager, filePath, position, req)
			},
			"type_hierarchy": func(ctx context.Context, req xrefParams) (any, error) {
				return runTypeHierarchy(ctx, manager, filePath, position, req)
			},
		})
	})
	return deduplicateSemanticToolHandler("xref", registry, handler)
}

// runReferences 查询符号引用，并按结果上限裁剪。
func runReferences(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
	enricher format.SymbolProvider,
) (any, error) {
	includeDeclaration := true
	if req.IncludeDeclaration != nil {
		includeDeclaration = *req.IncludeDeclaration
	}
	limit := format.ReferencesLimit(req.MaxResults)
	results, err := manager.References(ctx, filePath, position, includeDeclaration)
	if err != nil {
		return nil, enrichIdentifierNotFoundError(filePath, position, err)
	}
	format.EnrichLocationResultsWithFuncRange(results, enricher)
	importedSource := importedReferenceSource(ctx, manager, filePath, position)
	total := len(results)
	sampled := sampleReferencesByFile(results, filePath, limit)
	return renderReferencesResult(ctx, req, filePath, includeDeclaration, total, sampled, importedSource, limit)
}

// importedReferenceSource 从 definition 结果提取跨文件来源提示。
func importedReferenceSource(ctx context.Context, manager lspmanager.Manager, filePath string, position protocol.Position) string {
	if manager == nil {
		return ""
	}
	definitions, err := manager.Definition(ctx, filePath, position)
	if err != nil || len(definitions) == 0 {
		return ""
	}
	primary := definitions[0].PrimaryLocation()
	if primary == nil || strings.TrimSpace(primary.URI) == "" {
		return ""
	}
	currentPath, definitionPath := format.URIToPath(filePath), format.URIToPath(primary.URI)
	if currentPath == "" || definitionPath == "" || currentPath == definitionPath {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", compactToolFilePath(ctx, primary.URI), format.FromLSP(primary.Range.Start.Line), format.FromLSP(primary.Range.Start.Character))
}

func renderReferencesResult(ctx context.Context, req xrefParams, filePath string, includeDeclaration bool, total int, sampled sampledReferences, importedSource string, limit int) (protocol.GroupedLocationResult, error) {
	grouped := groupLocationsByFile(ctx, sampled.results, total)
	grouped.SampledFiles = sampledReferenceFiles(ctx, sampled.files)
	grouped.Hint = referencesHint(ctx, grouped.Hint, filePath, total, sampled, importedSource, limit)
	if err := addReferencesContinuation(&grouped, req, includeDeclaration, sampled, total, limit); err != nil {
		return protocol.GroupedLocationResult{}, err
	}
	return grouped, nil
}

// sampledReferenceFiles 返回因 external sampling 被截断的具体文件，供结构化 API 直接消费。
func sampledReferenceFiles(ctx context.Context, files []referenceFileStats) []string {
	_, sampled := referenceFilesMetadata(ctx, files)
	sort.Strings(sampled)
	return sampled
}

// referencesHint 在原有自然语言提示后追加原因、每文件统计和恢复动作。
// 该函数只拼装展示层字符串，不改变 GroupedLocationResult 的既有字段协议。
func referencesHint(
	ctx context.Context,
	existing string,
	filePath string,
	total int,
	sampled sampledReferences,
	importedSource string,
	limit int,
) string {
	summaryHint := referenceSummaryHint(filePath, total, sampled)
	truncationHint := referenceTruncationMetadata(ctx, sampled, limit)
	hint := baseReferencesHint(ctx, existing, importedSource, total, sampled, summaryHint)
	if truncationHint != "" {
		hint = appendReferenceHint(hint, truncationHint)
	}
	if hint != "" && summaryHint != "" && !strings.Contains(hint, summaryHint) {
		hint = appendReferenceHint(hint, summaryHint)
	}
	return hint
}

// baseReferencesHint 根据结果状态选择基础可执行提示。
func baseReferencesHint(ctx context.Context, existing, importedSource string, total int, sampled sampledReferences, summaryHint string) string {
	if total == 0 {
		if importedSource != "" {
			return fmt.Sprintf("no references found; symbol is imported from %s, check usages in source module or use document_symbol", importedSource)
		}
		return "no references found; check symbol declaration or position, or use document_symbol to locate the exact identifier"
	}
	if sampled.maxResults || sampled.externalSampling {
		if hint := referenceNextHint(ctx, sampled, importedSource); hint != "" {
			return hint
		}
		return "next: increase max_results or narrow the target with pos=<file>:<line>:<col>"
	}
	if importedSource != "" {
		return appendReferenceHint("symbol imported from "+importedSource+"; references include cross-file usages", summaryHint)
	}
	if summaryHint != "" && len(sampled.files) > 1 {
		return "references include cross-file usages " + summaryHint
	}
	return existing
}

// runCallHierarchy 查询调用层级；对不支持该能力的语言返回标准空结果。
func runCallHierarchy(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
) (any, error) {
	direction, err := normalizeCallHierarchyDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	languageID := firstNonEmpty(req.LanguageID, lspmanager.DetectLanguageID(filePath))
	if limitedDocumentFallbackLanguage(languageID) != "" {
		return unsupportedCapabilityEmptyResult("call hierarchy", languageID), nil
	}
	results, err := manager.CallHierarchy(ctx, filePath, position, direction)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("call hierarchy", languageID), nil
	}
	if err != nil {
		return nil, err
	}
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	incoming, outgoing := compactCallHierarchyEdges(ctx, results)
	rows, total := selectCallHierarchyEdges(incoming, outgoing, direction, limit)
	hint := "next: increase max_results or narrow the target position"
	if total == 0 {
		hint = emptyCallHierarchyHint(filePath, req.LanguageID)
	}
	response := hierarchyEdgeListResponse{Rows: rows, Total: total, Unit: "edge", Hint: hint}
	if err := addHierarchyContinuation(&response, "call_hierarchy", req.Pos, direction, req.LanguageID, total, limit); err != nil {
		return nil, err
	}
	return response, nil
}

// runTypeHierarchy 查询类型层级，并把无效目标包装成可读错误。
func runTypeHierarchy(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
) (any, error) {
	direction, err := normalizeTypeHierarchyDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	results, err := manager.TypeHierarchy(ctx, filePath, position, direction)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("type hierarchy"), nil
	}
	if err != nil {
		return nil, typeHierarchyTargetError(err)
	}
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	supertypes, subtypes := compactTypeHierarchyEdges(ctx, results)
	rows, total := selectTypeHierarchyEdges(supertypes, subtypes, direction, limit)
	hint := "next: increase max_results or narrow the target position"
	if total == 0 {
		hint = "no type hierarchy found; verify cursor is on a class, struct, or interface type, or use document_symbol to locate type declaration"
	}
	response := hierarchyEdgeListResponse{Rows: rows, Total: total, Unit: "type_edge", Hint: hint}
	if err := addHierarchyContinuation(&response, "type_hierarchy", req.Pos, direction, req.LanguageID, total, limit); err != nil {
		return nil, err
	}
	return response, nil
}

func selectCallHierarchyEdges(incoming, outgoing []hierarchyEdgeRow, direction string, limit int) ([]hierarchyEdgeRow, int) {
	switch direction {
	case "incoming":
		return limitSlice(incoming, limit), len(incoming)
	case "outgoing":
		return limitSlice(outgoing, limit), len(outgoing)
	default:
		return alternateHierarchyEdges(incoming, outgoing, limit), len(incoming) + len(outgoing)
	}
}

func selectTypeHierarchyEdges(supertypes, subtypes []hierarchyEdgeRow, direction string, limit int) ([]hierarchyEdgeRow, int) {
	switch direction {
	case "supertypes":
		return limitSlice(supertypes, limit), len(supertypes)
	case "subtypes":
		return limitSlice(subtypes, limit), len(subtypes)
	default:
		return alternateHierarchyEdges(supertypes, subtypes, limit), len(supertypes) + len(subtypes)
	}
}

// alternateHierarchyEdges 在两个方向都有结果时稳定交替，避免一侧因上限饥饿。
func alternateHierarchyEdges(first, second []hierarchyEdgeRow, limit int) []hierarchyEdgeRow {
	selected := make([]hierarchyEdgeRow, 0, min(limit, len(first)+len(second)))
	for firstIndex, secondIndex := 0, 0; len(selected) < limit && (firstIndex < len(first) || secondIndex < len(second)); {
		if firstIndex < len(first) {
			selected = append(selected, first[firstIndex])
			firstIndex++
		}
		if len(selected) < limit && secondIndex < len(second) {
			selected = append(selected, second[secondIndex])
			secondIndex++
		}
	}
	return selected
}

// normalizeCallHierarchyDirection 校验 call_hierarchy direction 参数。
func normalizeCallHierarchyDirection(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "incoming", "outgoing", "both":
		return value, nil
	default:
		return "", fmt.Errorf("invalid call_hierarchy direction %q", raw)
	}
}

// normalizeTypeHierarchyDirection 校验 type_hierarchy direction 参数。
func normalizeTypeHierarchyDirection(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "both":
		return "", nil
	case "supertypes", "subtypes":
		return value, nil
	default:
		return "", fmt.Errorf("invalid type_hierarchy direction %q", raw)
	}
}

// emptyCallHierarchyHint 为 JS/TS bootstrap 不完整场景提供更具体的重试提示。
func emptyCallHierarchyHint(filePath, languageID string) string {
	if isJSTSCallHierarchyLanguage(languageIDForCallHierarchyHint(filePath, languageID)) {
		return "no call hierarchy found; partial reason: JS/TS language server returned no prepare item after document bootstrap. next: retry with the cursor on a function or method identifier; if this persists, run diagnostics and verify package.json/tsconfig plus installed dependencies"
	}
	return "no call hierarchy found; verify cursor is on a function or method declaration/call, or use document_symbol to locate symbol position"
}

// languageIDForCallHierarchyHint 选择提示用语言 ID，优先使用调用方显式覆盖。
func languageIDForCallHierarchyHint(filePath, languageID string) string {
	if normalized := normalizeLanguageIDOverride(languageID); normalized != "" {
		return normalized
	}
	return lspmanager.DetectLanguageID(filePath)
}

// isJSTSCallHierarchyLanguage 判断语言是否属于 JS/TS 家族。
func isJSTSCallHierarchyLanguage(languageID string) bool {
	switch strings.ToLower(strings.TrimSpace(languageID)) {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return true
	default:
		return false
	}
}
