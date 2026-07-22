package main

import "strings"

// changedDiagnosticFiles 从 Git 变更真值中筛出需要机器诊断的源码路径。
func changedDiagnosticFiles(files []string) []string {
	var out []string
	for _, file := range files {
		if lspDiagnosticsRelevant(file) {
			out = append(out, file)
		}
	}
	return out
}

// lspDiagnosticsRelevant 约束 changed-file 门禁的源码边界，具体语言支持仍由 adapter registry 裁决。
func lspDiagnosticsRelevant(file string) bool {
	return strings.HasPrefix(file, "frontend-app/") ||
		strings.HasPrefix(file, "cmd/") ||
		strings.HasPrefix(file, "internal/") ||
		strings.HasPrefix(file, "pkg/") ||
		(strings.HasPrefix(file, "scripts/") && strings.HasSuffix(file, ".go"))
}

// sourceLike 判断路径是否属于现有源码门禁覆盖的语言后缀。
func sourceLike(file string) bool {
	for _, suffix := range []string{".go", ".js", ".jsx", ".ts", ".tsx", ".css", ".sql"} {
		if strings.HasSuffix(file, suffix) {
			return true
		}
	}
	return false
}
