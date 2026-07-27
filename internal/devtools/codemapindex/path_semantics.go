package codemapindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// validateInlineRepoRefs 校验反引号中的完整仓库路径及行号范围。
func validateInlineRepoRefs(root string, doc SemanticMarkdown, policy codemapPolicy) []string {
	var problems []string
	declaredAbsent := declaredCodemapAbsences(doc)
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		for _, code := range inlineCodeRe.FindAllStringSubmatch(doc.Lines[lineIndex], -1) {
			problems = append(problems, validateInlineCodeSpan(root, doc.File, lineIndex+1, code[1], policy, declaredAbsent)...)
		}
	}
	return problems
}

// validateInlineCodeSpan 校验一个反引号 span；glob/占位模式不是导航路径。
func validateInlineCodeSpan(
	root, codemapFile string,
	lineNumber int,
	code string,
	policy codemapPolicy,
	declaredAbsent map[string]struct{},
) []string {
	if isArchtestPolicyPattern(codemapFile, code) {
		return nil
	}
	if isRepositoryPattern(code) && hasRepositoryPrefix(code) {
		return validateRepositoryPattern(root, codemapFile, lineNumber, code, policy)
	}
	if repoBarePathRe.MatchString(code) {
		return appendRepoRefProblem(nil, root, codemapFile, lineNumber, code, "", policy, declaredAbsent)
	}
	var problems []string
	fileMatches := repoFileRefRe.FindAllStringSubmatch(code, -1)
	for _, match := range fileMatches {
		problems = appendRepoRefProblem(problems, root, codemapFile, lineNumber, match[1], match[2], policy, declaredAbsent)
	}
	dirMatches := repoDirRefRe.FindAllStringSubmatch(code, -1)
	for _, match := range dirMatches {
		problems = appendRepoRefProblem(problems, root, codemapFile, lineNumber, match[1], "", policy, declaredAbsent)
	}
	return problems
}

// isRepositoryPattern 识别概念命名空间、glob 与省略占位符。
func isRepositoryPattern(value string) bool {
	return strings.ContainsAny(value, "*?[]{}<>") || strings.Contains(value, "...")
}

// isArchtestPolicyPattern 只豁免由 archtest-map owner 生成的 deny-prefix token。
func isArchtestPolicyPattern(codemapFile, value string) bool {
	return codemapFile == "13-archtest-boundaries.md" && archtestPolicyPatternRe.MatchString(value)
}

// hasRepositoryPrefix 判断 pattern 是否以受管仓库目录开头。
func hasRepositoryPrefix(value string) bool {
	for _, prefix := range []string{"cmd/", "internal/", "pkg/", "scripts/", "frontend-app/", "sql/", "migrations/", "docs/", "test/", "tests/"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// appendRepoRefProblem 归一化路径并追加生命周期或存在性问题。
func appendRepoRefProblem(
	problems []string,
	root, codemapFile string,
	lineNumber int,
	raw, anchor string,
	policy codemapPolicy,
	declaredAbsent map[string]struct{},
) []string {
	relative := normalizeRepoRelative(raw)
	if isHistoricalDocumentPath(relative, policy) {
		return append(problems, fmt.Sprintf("%s:%d historical document cannot be a codemap authority: %s", codemapFile, lineNumber, relative))
	}
	if _, ok := declaredAbsent[relative]; ok {
		return problems
	}
	if problem := validateInlineRepoRef(root, codemapFile, lineNumber, relative, anchor); problem != "" {
		return append(problems, problem)
	}
	return problems
}

// declaredCodemapAbsences 返回当前卷显式声明且由独立 absence validator 校验的缺失路径。
func declaredCodemapAbsences(doc SemanticMarkdown) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		for _, match := range codemapAbsentRe.FindAllStringSubmatch(doc.Lines[lineIndex], -1) {
			declared[normalizeRepoRelative(match[1])] = struct{}{}
		}
	}
	return declared
}

// validateInlineRepoRef 校验仓库内目录、文件及多锚点引用。
func validateInlineRepoRef(root, codemapFile string, lineNumber int, relative, anchor string) string {
	absolute, err := resolveRepoPath(root, relative)
	if err != nil {
		return fmt.Sprintf("%s:%d invalid repository path %s: %v", codemapFile, lineNumber, relative, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		symbolPath, symbolInfo, symbols, symbolErr := resolveSymbolQualifiedPath(root, relative)
		if symbolErr != nil {
			return fmt.Sprintf("%s:%d invalid repository path %s: %v", codemapFile, lineNumber, relative, symbolErr)
		}
		if symbolInfo == nil {
			return fmt.Sprintf("%s:%d missing repository path: %s", codemapFile, lineNumber, relative)
		}
		if !repositorySymbolExists(symbolPath, symbolInfo, symbols) {
			return fmt.Sprintf("%s:%d missing repository symbol: %s", codemapFile, lineNumber, relative)
		}
		absolute, info = symbolPath, symbolInfo
	}
	if anchor == "" {
		return ""
	}
	if info.IsDir() {
		return fmt.Sprintf("%s:%d line anchor cannot target directory: %s:%s", codemapFile, lineNumber, relative, anchor)
	}
	sourceLines, err := readLines(absolute)
	if err != nil {
		return fmt.Sprintf("%s:%d cannot read repository path %s: %v", codemapFile, lineNumber, relative, err)
	}
	if !lineAnchorInRange(anchor, len(sourceLines)) {
		return fmt.Sprintf("%s:%d line anchor %s:%s exceeds %d lines", codemapFile, lineNumber, relative, anchor, len(sourceLines))
	}
	return ""
}

// resolveSymbolQualifiedPath 接受 package.Symbol 或 file.go.Symbol 导航写法。
func resolveSymbolQualifiedPath(root, relative string) (string, os.FileInfo, []string, error) {
	candidate := relative
	var symbols []string
	for {
		dot := strings.LastIndexByte(candidate, '.')
		if dot < 0 || !isGoSymbolSuffix(candidate[dot+1:]) {
			return "", nil, nil, nil
		}
		symbols = append([]string{candidate[dot+1:]}, symbols...)
		candidate = candidate[:dot]
		absolute, err := resolveRepoPath(root, candidate)
		if err != nil {
			return "", nil, nil, err
		}
		info, err := os.Stat(absolute)
		if err == nil {
			return absolute, info, symbols, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, nil, err
		}
	}
}

// isGoSymbolSuffix 拒绝把常见文件扩展名误当作 package-qualified symbol。
func isGoSymbolSuffix(value string) bool {
	if value == "" || !isGoIdentifier(value) {
		return false
	}
	switch value {
	case "go", "js", "ts", "jsx", "tsx", "mjs", "cjs", "json", "yaml", "yml",
		"toml", "html", "proto", "txt", "css", "md", "sql", "sh", "ps1":
		return false
	default:
		return true
	}
}

// isGoIdentifier 判断字符串是否满足 ASCII Go identifier 形状。
func isGoIdentifier(value string) bool {
	for index, char := range value {
		if char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

// lineAnchorInRange 判断逗号或斜线分隔的单行/闭区间是否落在文件内。
func lineAnchorInRange(spec string, totalLines int) bool {
	parts := strings.FieldsFunc(strings.TrimSuffix(spec, "*"), func(value rune) bool {
		return value == ',' || value == '/'
	})
	for _, part := range parts {
		if !anchorPartInRange(part, totalLines) {
			return false
		}
	}
	return len(parts) > 0
}

// anchorPartInRange 校验一个单行或闭区间锚点。
func anchorPartInRange(part string, totalLines int) bool {
	for _, bound := range strings.SplitN(strings.TrimSuffix(part, "*"), "-", 2) {
		line, err := strconv.Atoi(bound)
		if err != nil || line < 1 || line > totalLines {
			return false
		}
	}
	return true
}

// resolveRepoPath 拒绝词法或符号链接逃逸仓库根目录的路径。
func resolveRepoPath(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || escapesRoot(clean) {
		return "", fmt.Errorf("path escapes repository")
	}
	absolute := filepath.Join(root, clean)
	lexical, err := filepath.Rel(root, absolute)
	if err != nil || escapesRoot(lexical) {
		return "", fmt.Errorf("path escapes repository")
	}
	existing, err := nearestExistingPath(absolute)
	if err != nil {
		return "", err
	}
	if err := ensureRealPathWithinRoot(root, existing); err != nil {
		return "", err
	}
	return absolute, nil
}

// escapesRoot 判断相对路径是否越出根目录。
func escapesRoot(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// nearestExistingPath 找到目标路径最近的现存祖先，供符号链接校验。
func nearestExistingPath(path string) (string, error) {
	for {
		if _, err := os.Lstat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("cannot resolve existing parent")
		}
		path = parent
	}
}

// ensureRealPathWithinRoot 校验现存祖先解析符号链接后仍位于仓库。
func ensureRealPathWithinRoot(root, existing string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realRoot, realExisting)
	if err != nil || escapesRoot(relative) {
		return fmt.Errorf("path escapes repository through symlink")
	}
	return nil
}
