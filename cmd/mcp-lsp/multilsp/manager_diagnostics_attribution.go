package multilsp

import (
	"strconv"
	"strings"
)

// logClangdDiagnosticsAttribution 记录 clangd 诊断的可归因字段，不写入原始路径或参数。
func (m *manager) logClangdDiagnosticsAttribution(scope ResolvedLSPToolScope) {
	if m == nil || m.logger == nil || !isClangdLanguageID(scope.LanguageID) {
		return
	}
	m.logger.Info("LSP diagnostics attribution", clangdDiagnosticsAttributionArgs(scope)...)
}

func clangdDiagnosticsAttributionArgs(scope ResolvedLSPToolScope) []any {
	rootKind := safeDiagnosticToken(scope.RootKind)
	if rootKind == "" {
		rootKind = "unknown"
	}
	args := []any{
		"language", safeDiagnosticToken(scope.LanguageID),
		"root_kind", rootKind,
	}
	if scope.LanguageSpecific[clangdMQLScopeKey] == "true" {
		return append(args, clangdMQLDiagnosticsAttributionArgs(scope.LanguageSpecific)...)
	}
	return append(args, "clangd_compile_db_attribution", "unavailable")
}

// clangdMQLDiagnosticsAttributionArgs 将 MQL 编译数据库证据转换为有限日志字段。
func clangdMQLDiagnosticsAttributionArgs(evidence map[string]string) []any {
	args := []any{"clangd_compile_db_attribution", "mql_evidence"}
	if strategy := safeDiagnosticToken(evidence[clangdMQLStrategyKey]); strategy != "" {
		args = append(args, "clangd_compile_db_strategy", strategy)
	}
	if count, err := strconv.Atoi(evidence[clangdMQLCandidateCountKey]); err == nil && count >= 0 {
		args = append(args, "clangd_compile_db_candidate_count", count)
	}
	if hashes := safeDiagnosticHashList(evidence[clangdMQLCandidateHashesKey]); hashes != "" {
		args = append(args, "clangd_compile_db_candidate_hashes", hashes)
	}
	if hash := safeDiagnosticHash(evidence[clangdMQLCompileTaskHashKey]); hash != "" {
		args = append(args, "clangd_compile_db_selected_hash", hash)
	}
	if hash := safeDiagnosticHash(evidence[clangdMQLTargetHashKey]); hash != "" {
		args = append(args, "clangd_compile_db_target_hash", hash)
	}
	if hash := safeDiagnosticHash(evidence[clangdMQLRootHashKey]); hash != "" {
		args = append(args, "clangd_compile_db_root_hash", hash)
	}
	return args
}

// isClangdLanguageID 判断语言是否属于 clangd 诊断归因范围。
func isClangdLanguageID(languageID string) bool {
	switch strings.ToLower(strings.TrimSpace(languageID)) {
	case "c", "cpp", "objective-c", "objective-cpp":
		return true
	default:
		return false
	}
}

// safeDiagnosticToken 仅保留短的诊断枚举 token，拒绝路径和控制字符。
func safeDiagnosticToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 {
		return ""
	}
	for _, char := range value {
		if !safeDiagnosticTokenChar(char) {
			return ""
		}
	}
	return value
}

// safeDiagnosticTokenChar 判断字符是否属于诊断 token 的固定安全字符集。
func safeDiagnosticTokenChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z':
		return true
	case char >= 'A' && char <= 'Z':
		return true
	case char >= '0' && char <= '9':
		return true
	case char == '_' || char == '-' || char == '.':
		return true
	default:
		return false
	}
}

// safeDiagnosticHash 只接受完整 SHA-256 十六进制值，避免日志暴露原始内容。
func safeDiagnosticHash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return ""
	}
	for _, char := range value {
		if !safeDiagnosticHashChar(char) {
			return ""
		}
	}
	return strings.ToLower(value)
}

// safeDiagnosticHashChar 判断字符是否属于十六进制 SHA-256 表示。
func safeDiagnosticHashChar(char rune) bool {
	return (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') || (char >= '0' && char <= '9')
}

// safeDiagnosticHashList 过滤候选 SHA-256 列表并保留稳定的逗号分隔顺序。
func safeDiagnosticHashList(value string) string {
	parts := strings.Split(value, ",")
	hashes := make([]string, 0, len(parts))
	for _, part := range parts {
		if hash := safeDiagnosticHash(part); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	return strings.Join(hashes, ",")
}
