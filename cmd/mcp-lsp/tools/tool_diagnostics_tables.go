package tools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// diagnosticDisplayPath 返回调用方可读的目标路径，必要时回退到 URI 路径。
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

// diagnosticMessageSummary 将 manager 消息限制为稳定的首行摘要。
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

// buildDiagnosticsTables 将 LSP 诊断整理为按文件分组的响应表。
// 默认保留全部严重度；只有显式差量过滤才隐藏未命中行的非 Error 诊断。
func buildDiagnosticsTables(items []protocol.PublishDiagnosticsParams, displayPaths map[string]string, filter *diagnosticsFilterSpec) ([]diagnosticsTable, *diagnosticsFilterMeta, error) {
	if len(items) == 0 {
		return emptyDiagnosticsTables(filter), diagnosticsFilterMetaForEmpty(filter), nil
	}
	filterMeta := newDiagnosticsFilterMeta(filter)
	sort.Slice(items, func(i, j int) bool { return items[i].URI < items[j].URI })
	tables := make([]diagnosticsTable, 0, len(items))
	for _, item := range items {
		table, err := buildDiagnosticsTable(item, displayPaths, filter, filterMeta)
		if err != nil {
			return nil, nil, err
		}
		if len(table.Rows) > 0 {
			tables = append(tables, table)
		}
	}
	sortDiagnosticsFilteredEntries(filterMeta)
	return tables, filterMeta, nil
}

func emptyDiagnosticsTables(filter *diagnosticsFilterSpec) []diagnosticsTable {
	if filter == nil {
		return nil
	}
	return []diagnosticsTable{}
}

func diagnosticsFilterMetaForEmpty(filter *diagnosticsFilterSpec) *diagnosticsFilterMeta {
	if filter == nil {
		return nil
	}
	return &diagnosticsFilterMeta{Mode: filter.Mode, FilteredDiagnostics: []diagnosticsFilteredEntry{}}
}

func newDiagnosticsFilterMeta(filter *diagnosticsFilterSpec) *diagnosticsFilterMeta {
	if filter == nil {
		return nil
	}
	return &diagnosticsFilterMeta{Mode: filter.Mode, FilteredDiagnostics: make([]diagnosticsFilteredEntry, 0)}
}

func buildDiagnosticsTable(item protocol.PublishDiagnosticsParams, displayPaths map[string]string, filter *diagnosticsFilterSpec, filterMeta *diagnosticsFilterMeta) (diagnosticsTable, error) {
	changedSet, hasChangedSet, err := diagnosticsChangedSet(item.URI, filter)
	if err != nil {
		return diagnosticsTable{}, err
	}
	seen := make(map[string]struct{}, len(item.Diagnostics))
	rows := make([][]any, 0, len(item.Diagnostics))
	for _, diag := range item.Diagnostics {
		key := diagnosticTableKey(diag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if entry, filtered := filteredDiagnostic(item.URI, diag, displayPaths, changedSet, hasChangedSet); filtered {
			filterMeta.Filtered++
			filterMeta.FilteredDiagnostics = append(filterMeta.FilteredDiagnostics, entry)
			continue
		}
		rows = append(rows, diagnosticTableRow(diag))
	}
	return diagnosticsTable{
		File: diagnosticDisplayPathForURI(item.URI, displayPaths),
		Cols: []string{"L", "C", "sev", "msg", "src", "code"},
		Rows: rows,
	}, nil
}

func diagnosticsChangedSet(uri string, filter *diagnosticsFilterSpec) (map[int]struct{}, bool, error) {
	if filter == nil {
		return nil, false, nil
	}
	changedSet, ok := filter.ChangedLinesByURI[uri]
	if !ok {
		return nil, false, fmt.Errorf("diff_filter has no changed_lines mapping for diagnostics URI %q", uri)
	}
	return changedSet, true, nil
}

func diagnosticTableKey(diag protocol.Diagnostic) string {
	return fmt.Sprintf("%d:%d:%d:%s:%s:%s", diag.Range.Start.Line, diag.Range.Start.Character, diag.Severity, diag.Message, diag.Source, diagnosticCode(diag.Code))
}

func filteredDiagnostic(uri string, diag protocol.Diagnostic, displayPaths map[string]string, changedSet map[int]struct{}, hasChangedSet bool) (diagnosticsFilteredEntry, bool) {
	if !hasChangedSet || !diffFilterableSeverity(diag) {
		return diagnosticsFilteredEntry{}, false
	}
	line := format.FromLSP(diag.Range.Start.Line)
	if _, changed := changedSet[line]; changed {
		return diagnosticsFilteredEntry{}, false
	}
	return diagnosticsFilteredEntry{
		File:     diagnosticDisplayPathForURI(uri, displayPaths),
		Line:     line,
		Column:   format.FromLSP(diag.Range.Start.Character),
		Severity: diagnosticSeverity(diag),
		Source:   diag.Source,
		Code:     diagnosticCode(diag.Code),
		Message:  diagnosticMessageSummary(diag.Message),
	}, true
}

func diagnosticTableRow(diag protocol.Diagnostic) []any {
	return []any{
		format.FromLSP(diag.Range.Start.Line),
		format.FromLSP(diag.Range.Start.Character),
		diagnosticSeverity(diag),
		diagnosticMessageSummary(diag.Message),
		diag.Source,
		diagnosticCode(diag.Code),
	}
}

// sortDiagnosticsFilteredEntries 对被过滤诊断按文件、位置和代码稳定排序。
func sortDiagnosticsFilteredEntries(filterMeta *diagnosticsFilterMeta) {
	if filterMeta == nil {
		return
	}
	sort.Slice(filterMeta.FilteredDiagnostics, func(i, j int) bool {
		left, right := filterMeta.FilteredDiagnostics[i], filterMeta.FilteredDiagnostics[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		if left.Severity != right.Severity {
			return left.Severity < right.Severity
		}
		return left.Code < right.Code
	})
}

func diagnosticDisplayPathForURI(uri string, displayPaths map[string]string) string {
	if displayPath := strings.TrimSpace(displayPaths[uri]); displayPath != "" {
		return displayPath
	}
	return format.URIToPath(uri)
}

func diffFilterableSeverity(diag protocol.Diagnostic) bool {
	switch diag.Severity {
	case protocol.SeverityWarning, protocol.SeverityInformation, protocol.SeverityHint:
		return true
	default:
		return false
	}
}

// diagnosticsMessageAfterFetch 在 partial 提示已被实际诊断覆盖时清空提示。
func diagnosticsMessageAfterFetch(message string, uris []string, items []protocol.PublishDiagnosticsParams) string {
	if !strings.Contains(message, "partial diagnostics") || len(uris) == 0 {
		return message
	}
	withRows := diagnosticsURIsWithRows(items)
	if len(withRows) == 0 || diagnosticsURIsMissing(withRows, uris) {
		return message
	}
	return ""
}

func diagnosticsURIsWithRows(items []protocol.PublishDiagnosticsParams) map[string]struct{} {
	withRows := make(map[string]struct{}, len(items))
	for _, item := range items {
		uri := strings.TrimSpace(item.URI)
		if uri != "" && len(item.Diagnostics) > 0 {
			withRows[uri] = struct{}{}
		}
	}
	return withRows
}

func diagnosticsURIsMissing(withRows map[string]struct{}, uris []string) bool {
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := withRows[uri]; !ok {
			return true
		}
	}
	return false
}

// countDiagnosticRows 统计按文件表格中的诊断行数。
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
	if diag.Severity == protocol.SeverityInformation {
		return "information"
	}
	return diag.Severity.String()
}
