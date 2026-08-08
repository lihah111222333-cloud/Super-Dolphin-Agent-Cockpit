package remoteci

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	frontendAppInputPrefix = "frontend-app/"
	frontendE2EInputPrefix = "frontend-app/tests/e2e/"
)

// frontendNonE2EInputDigest 覆盖 Vitest、前端 preflight/full test 的执行输入，
// 但排除由独立 Playwright executor 运行且不会被 Vitest 读取的 E2E specs。
func (snapshot *remoteGitTreeSnapshot) frontendNonE2EInputDigest() (string, error) {
	return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
		if !strings.HasPrefix(entry.path, frontendAppInputPrefix) {
			return false
		}
		relative := strings.TrimPrefix(entry.path, frontendAppInputPrefix)
		return !strings.HasPrefix(entry.path, frontendE2EInputPrefix) && !frontendPlaywrightConfigSourcePath(relative)
	})
}

// frontendPlaywrightConfigSourcePath 判断不属于 Vitest/preflight 执行闭包的 E2E 配置。
func frontendPlaywrightConfigSourcePath(relative string) bool {
	return strings.HasPrefix(relative, "playwright.") && strings.HasSuffix(relative, ".config.js")
}

// frontendBuildInputDigest 只绑定 Vite build 及其同步脚本的生产输入。
func (snapshot *remoteGitTreeSnapshot) frontendBuildInputDigest() (string, error) {
	return snapshot.digestMatching(frontendBuildInputEntry)
}

// frontendBuildInputEntry 判断单个 Git tree 条目是否属于生产构建输入。
func frontendBuildInputEntry(entry remoteGitTreeEntry) bool {
	if !strings.HasPrefix(entry.path, frontendAppInputPrefix) {
		return false
	}
	relative := strings.TrimPrefix(entry.path, frontendAppInputPrefix)
	switch {
	case relative == "package.json", relative == "package-lock.json", relative == "index.html", relative == "vite.config.js":
		return true
	case relative == "scripts/sync-frontend-dist.mjs":
		return true
	case strings.HasPrefix(relative, "config/"), strings.HasPrefix(relative, "public/"):
		return true
	case strings.HasPrefix(relative, "src/"):
		return !frontendTestSourcePath(relative)
	default:
		return false
	}
}

func frontendTestSourcePath(relative string) bool {
	return strings.Contains(relative, ".test.") || strings.Contains(relative, ".spec.") ||
		strings.HasSuffix(relative, ".test.js") || strings.HasSuffix(relative, ".test.jsx")
}

func (snapshot *remoteGitTreeSnapshot) projectMapInputDigest() (string, error) {
	// projectmaptrusted runs the candidate generator with --filesystem-scan. The
	// generator observes every tracked path while producing the managed map, so a
	// narrow map/policy-only digest could reuse a stale pass after source changes.
	// Bind the exact tree fail-closed; this includes the generated map, policy,
	// generator, trusted asset, and project-map CLI implementation as observed inputs.
	return snapshot.digestEntries(snapshot.entries)
}

func (snapshot *remoteGitTreeSnapshot) frontendPlaywrightInputDigest(ctx context.Context, target string) (string, error) {
	selected, observesDynamic, err := snapshot.frontendPlaywrightTargetEntries(ctx, target)
	if err != nil {
		return "", err
	}
	if observesDynamic {
		return snapshot.digestEntries(snapshot.entries)
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

func frontendPlaywrightConfigPath(specPath string) (string, error) {
	switch specPath {
	case "tests/e2e/business-flows.spec.js":
		return "playwright.business-flows.config.js", nil
	case "tests/e2e/desktop-wide.spec.js":
		return "playwright.desktop-wide.config.js", nil
	default:
		return "", fmt.Errorf("unsupported Playwright spec %q", specPath)
	}
}

// frontendPlaywrightTargetEntries 建立一个 target 的生产输入集合并报告动态观察。
func (snapshot *remoteGitTreeSnapshot) frontendPlaywrightTargetEntries(ctx context.Context, target string) (map[string]remoteGitTreeEntry, bool, error) {
	seeds, err := frontendPlaywrightTargetSeeds(target)
	if err != nil {
		return nil, false, err
	}
	selected := make(map[string]remoteGitTreeEntry)
	for _, entry := range snapshot.entries {
		if frontendBuildInputEntry(entry) {
			selected[entry.path] = entry
		}
	}
	for _, filePath := range seeds {
		entry, ok := snapshot.byPath[filePath]
		if !ok {
			return nil, false, fmt.Errorf("Playwright target %q requires absent tracked file %q", target, filePath)
		}
		selected[filePath] = entry
	}
	closure, observesDynamic, err := snapshot.frontendStaticImportClosure(ctx, seeds)
	if err != nil || observesDynamic {
		return selected, observesDynamic, err
	}
	for filePath := range closure {
		selected[filePath] = snapshot.byPath[filePath]
	}
	return selected, false, nil
}

// frontendPlaywrightTargetSeeds 将稳定 target 解析为确切的 spec 与 Playwright 配置路径。
func frontendPlaywrightTargetSeeds(target string) ([]string, error) {
	specPath, _, err := gate.ParsePlaywrightE2ETarget(target)
	if err != nil {
		return nil, err
	}
	configPath, err := frontendPlaywrightConfigPath(specPath)
	if err != nil {
		return nil, err
	}
	return []string{frontendAppInputPrefix + specPath, frontendAppInputPrefix + configPath}, nil
}

// frontendPlaywrightParentInputDigest 聚合所有已登记 E2E target 的真实闭包。
func (snapshot *remoteGitTreeSnapshot) frontendPlaywrightParentInputDigest(ctx context.Context) (string, error) {
	selected := make(map[string]remoteGitTreeEntry)
	for _, entry := range snapshot.entries {
		if frontendBuildInputEntry(entry) {
			selected[entry.path] = entry
		}
	}
	for _, target := range []string{
		"tests/e2e/business-flows.spec.js#business-read-surfaces",
		"tests/e2e/business-flows.spec.js#business-chat-bridge",
		"tests/e2e/desktop-wide.spec.js#desktop-shell",
		"tests/e2e/desktop-wide.spec.js#desktop-business-pages",
		"tests/e2e/desktop-wide.spec.js#desktop-read-settings",
	} {
		targetEntries, observesDynamic, err := snapshot.frontendPlaywrightTargetEntries(ctx, target)
		if err != nil {
			return "", err
		}
		if observesDynamic {
			return snapshot.digestEntries(snapshot.entries)
		}
		for filePath, entry := range targetEntries {
			selected[filePath] = entry
		}
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

func (snapshot *remoteGitTreeSnapshot) frontendStaticImportClosure(ctx context.Context, seeds []string) (map[string]struct{}, bool, error) {
	closure := make(map[string]struct{}, len(seeds))
	queue := append([]string(nil), seeds...)
	for len(queue) > 0 {
		batch, remaining := frontendImportBatch(queue, closure)
		queue = remaining
		if len(batch) == 0 {
			continue
		}
		next, observesDynamic, err := snapshot.frontendStaticImportBatch(ctx, batch)
		if err != nil {
			return nil, false, err
		}
		if observesDynamic {
			return closure, true, nil
		}
		queue = append(queue, next...)
	}
	return closure, false, nil
}

// frontendImportBatch 将尚未处理的路径分成 JavaScript 解析批次和剩余队列。
func frontendImportBatch(queue []string, closure map[string]struct{}) ([]string, []string) {
	batch := make([]string, 0, len(queue))
	remaining := make([]string, 0, len(queue))
	for _, filePath := range queue {
		if _, done := closure[filePath]; done {
			continue
		}
		closure[filePath] = struct{}{}
		if frontendJavaScriptPath(filePath) {
			batch = append(batch, filePath)
		}
	}
	return batch, remaining
}

// frontendStaticImportBatch 读取一批源码、解析静态依赖并返回下一批本地路径。
func (snapshot *remoteGitTreeSnapshot) frontendStaticImportBatch(ctx context.Context, batch []string) ([]string, bool, error) {
	blobs, err := snapshot.frontendSourceBlobs(ctx, batch)
	if err != nil {
		return nil, false, err
	}
	next := make([]string, 0)
	for _, filePath := range batch {
		imports, observesDynamic := frontendJavaScriptImports(blobs[filePath])
		if observesDynamic {
			return nil, true, nil
		}
		for _, importPath := range imports {
			resolved, local, err := snapshot.resolveFrontendImport(filePath, importPath)
			if err != nil {
				return nil, false, err
			}
			if local {
				next = append(next, resolved)
			}
		}
	}
	return next, false, nil
}

func frontendJavaScriptPath(filePath string) bool {
	switch path.Ext(filePath) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

// frontendSourceBlobs 读取精确树中的前端源码，测试夹具可注入同树 blob。
func (snapshot *remoteGitTreeSnapshot) frontendSourceBlobs(ctx context.Context, paths []string) (map[string][]byte, error) {
	if snapshot.frontendSources != nil {
		contents := make(map[string][]byte, len(paths))
		for _, filePath := range paths {
			source, ok := snapshot.frontendSources[filePath]
			if !ok {
				return nil, fmt.Errorf("tracked frontend source %q is unavailable", filePath)
			}
			contents[filePath] = source
		}
		return contents, nil
	}
	return snapshot.readGitBlobs(ctx, paths)
}

// resolveFrontendImport 将相对或根绝对的前端 import 解析为已跟踪文件。
func (snapshot *remoteGitTreeSnapshot) resolveFrontendImport(currentPath, importPath string) (string, bool, error) {
	if !frontendLocalImport(importPath) {
		return "", false, nil
	}
	base := frontendImportBase(currentPath, importPath)
	if base != "frontend-app" && !strings.HasPrefix(base, "frontend-app/") {
		return "", false, fmt.Errorf("frontend import %q from %q escapes frontend-app", importPath, currentPath)
	}
	for _, candidate := range frontendImportCandidates(base) {
		if _, ok := snapshot.byPath[candidate]; ok {
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("frontend import %q from %q is not tracked", importPath, currentPath)
}

// frontendLocalImport 识别需要进入本地前端闭包的 specifier。
func frontendLocalImport(importPath string) bool {
	return strings.HasPrefix(importPath, ".") || strings.HasPrefix(importPath, "/")
}

// frontendImportBase 计算 import 在 frontend-app 内的规范化路径。
func frontendImportBase(currentPath, importPath string) string {
	if strings.HasPrefix(importPath, "/") {
		return path.Clean(path.Join("frontend-app", strings.TrimPrefix(importPath, "/")))
	}
	return path.Clean(path.Join(path.Dir(currentPath), importPath))
}

// frontendImportCandidates 返回无扩展名 import 的 JS/资源和 index 候选。
func frontendImportCandidates(base string) []string {
	candidates := []string{base}
	if path.Ext(base) != "" {
		return candidates
	}
	for _, extension := range frontendImportExtensions() {
		candidates = append(candidates, base+extension)
	}
	for _, extension := range frontendImportExtensions() {
		candidates = append(candidates, path.Join(base, "index"+extension))
	}
	return candidates
}

func frontendImportExtensions() []string {
	return []string{".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".json", ".css"}
}

func frontendJavaScriptImports(source []byte) ([]string, bool) {
	imports := make([]string, 0, 4)
	for index := 0; index < len(source); {
		next, skipped := frontendSkipLexeme(source, index)
		if skipped {
			index = next
			continue
		}
		if !frontendIdentifierStart(source[index]) {
			index++
			continue
		}
		word, end := frontendIdentifierAt(source, index)
		found, observesDynamic, next := frontendJavaScriptWord(source, word, end)
		imports = append(imports, found...)
		if observesDynamic {
			return imports, true
		}
		index = next
	}
	return imports, false
}

// frontendSkipLexeme 跳过注释或字符串，避免把字面量内容当作代码分析。
func frontendSkipLexeme(source []byte, index int) (int, bool) {
	if source[index] == '/' && index+1 < len(source) && source[index+1] == '/' {
		return skipFrontendLineComment(source, index), true
	}
	if source[index] == '/' && index+1 < len(source) && source[index+1] == '*' {
		return skipFrontendBlockComment(source, index), true
	}
	if source[index] == '\'' || source[index] == '"' || source[index] == '`' {
		return skipFrontendString(source, index), true
	}
	return index, false
}

// frontendIdentifierAt 读取 JavaScript 标识符并返回其结束位置。
func frontendIdentifierAt(source []byte, start int) (string, int) {
	index := start + 1
	for index < len(source) && frontendIdentifierContinue(source[index]) {
		index++
	}
	return string(source[start:index]), index
}

// frontendJavaScriptWord 解析一个关键字后面的静态依赖或动态观察。
func frontendJavaScriptWord(source []byte, word string, start int) ([]string, bool, int) {
	switch word {
	case "import":
		return frontendImportWord(source, start)
	case "export":
		return frontendExportWord(source, start)
	case "require":
		return frontendRequireWord(source, start)
	default:
		return nil, false, start
	}
}

// frontendImportWord 解析 ESM import，并对动态 import 或 import.meta 观察 fail-closed。
func frontendImportWord(source []byte, start int) ([]string, bool, int) {
	next := skipFrontendIgnorable(source, start)
	if next < len(source) && source[next] == '(' {
		return nil, true, next
	}
	if next < len(source) {
		property := string(source[next:])
		if strings.HasPrefix(property, ".meta.glob") || strings.HasPrefix(property, ".meta.resolve") {
			return nil, true, next
		}
	}
	if spec, end, ok := frontendStringAt(source, next); ok {
		return []string{spec}, false, end
	}
	if spec, end, ok := frontendFromSpecifier(source, next); ok {
		return []string{spec}, false, end
	}
	return nil, false, start
}

// frontendExportWord 解析 export ... from 的本地静态依赖。
func frontendExportWord(source []byte, start int) ([]string, bool, int) {
	if spec, end, ok := frontendFromSpecifier(source, skipFrontendIgnorable(source, start)); ok {
		return []string{spec}, false, end
	}
	return nil, false, start
}

// frontendRequireWord 解析字符串形式 require，并将变量形式视为动态观察。
func frontendRequireWord(source []byte, start int) ([]string, bool, int) {
	next := skipFrontendIgnorable(source, start)
	if next >= len(source) || source[next] != '(' {
		return nil, false, start
	}
	next = skipFrontendIgnorable(source, next+1)
	if spec, end, ok := frontendStringAt(source, next); ok {
		return []string{spec}, false, end
	}
	return nil, true, next
}

func frontendFromSpecifier(source []byte, start int) (string, int, bool) {
	for index := start; index < len(source); {
		next, skipped := frontendSkipLexeme(source, index)
		if skipped {
			index = next
			continue
		}
		if !frontendIdentifierStart(source[index]) {
			index++
			continue
		}
		word, end := frontendIdentifierAt(source, index)
		if word == "from" {
			return frontendStringAt(source, skipFrontendIgnorable(source, end))
		}
		index = end
	}
	return "", start, false
}

// frontendStringAt 读取单引号或双引号字符串，并返回字符串结束位置。
func frontendStringAt(source []byte, start int) (string, int, bool) {
	if start >= len(source) || (source[start] != '\'' && source[start] != '"') {
		return "", start, false
	}
	quote := source[start]
	for index := start + 1; index < len(source); index++ {
		if source[index] == '\\' {
			index++
			continue
		}
		if source[index] == quote {
			return string(source[start+1 : index]), index + 1, true
		}
	}
	return "", start, false
}

// skipFrontendIgnorable 跳过空白和注释，供关键字后的 specifier 解析使用。
func skipFrontendIgnorable(source []byte, start int) int {
	for start < len(source) {
		next := skipFrontendWhitespace(source, start)
		if next != start {
			start = next
			continue
		}
		next, skipped := skipFrontendComment(source, start)
		if skipped {
			start = next
			continue
		}
		return start
	}
	return start
}

// skipFrontendWhitespace 跳过 JavaScript 空白字符。
func skipFrontendWhitespace(source []byte, start int) int {
	for start < len(source) && (source[start] == ' ' || source[start] == '\t' || source[start] == '\r' || source[start] == '\n') {
		start++
	}
	return start
}

func skipFrontendComment(source []byte, start int) (int, bool) {
	if start+1 >= len(source) || source[start] != '/' {
		return start, false
	}
	if source[start+1] == '/' {
		return skipFrontendLineComment(source, start), true
	}
	if source[start+1] == '*' {
		return skipFrontendBlockComment(source, start), true
	}
	return start, false
}

func skipFrontendLineComment(source []byte, start int) int {
	for start < len(source) && source[start] != '\n' {
		start++
	}
	return start
}

func skipFrontendBlockComment(source []byte, start int) int {
	for start += 2; start+1 < len(source); start++ {
		if source[start] == '*' && source[start+1] == '/' {
			return start + 2
		}
	}
	return len(source)
}

func skipFrontendString(source []byte, start int) int {
	quote := source[start]
	for start++; start < len(source); start++ {
		if source[start] == '\\' {
			start++
			continue
		}
		if source[start] == quote {
			return start + 1
		}
	}
	return len(source)
}

// frontendIdentifierStart 判断 JavaScript 标识符首字符。
func frontendIdentifierStart(value byte) bool {
	return value == '_' || value == '$' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func frontendIdentifierContinue(value byte) bool {
	return frontendIdentifierStart(value) || value >= '0' && value <= '9'
}
