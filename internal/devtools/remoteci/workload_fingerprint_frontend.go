package remoteci

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
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

// frontendPreflightInputDigest 覆盖一个 preflight 原子 workload 的最小可观察输入闭包；
// 不带 target 时返回所有 allowlist 子集的并集，仅供父身份兼容/诊断使用。
func (snapshot *remoteGitTreeSnapshot) frontendPreflightInputDigest(targets ...string) (string, error) {
	selected := make(map[string]remoteGitTreeEntry)
	if len(targets) == 0 {
		targets = gate.FrontendPreflightTargets()
	}
	for _, target := range targets {
		if !containsString(gate.FrontendPreflightTargets(), target) {
			return "", fmt.Errorf("frontend preflight target %q is not in the canonical allowlist", target)
		}
	}
	for _, entry := range snapshot.entries {
		for _, target := range targets {
			if frontendPreflightInputEntry(entry, target) {
				selected[entry.path] = entry
				break
			}
		}
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("frontend preflight input closure is empty")
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

// frontendPreflightInputEntry 按 preflight target 选择可观察输入。
func frontendPreflightInputEntry(entry remoteGitTreeEntry, target string) bool {
	const packageJSON = "frontend-app/package.json"
	const packageLock = "frontend-app/package-lock.json"
	switch target {
	case gate.FrontendPreflightTargetCriticalGuards:
		return frontendPreflightCriticalGuardEntry(entry.path, packageJSON)
	case gate.FrontendPreflightTargetTurnContractVerify:
		return frontendPreflightTurnVerifyEntry(entry.path, packageJSON)
	case gate.FrontendPreflightTargetTurnContractFieldGuard:
		return frontendPreflightTurnFieldEntry(entry.path, packageJSON)
	case gate.FrontendPreflightTargetCriticalTypecheck:
		return frontendPreflightCriticalTypecheckEntry(entry, packageLock)
	case gate.FrontendPreflightTargetContractsVitest:
		return frontendPreflightContractsEntry(entry, packageLock)
	case gate.FrontendPreflightTargetRPCAudit:
		return frontendPreflightRPCAuditEntry(entry.path, packageJSON)
	case gate.FrontendPreflightTargetDependencyContract:
		return frontendPreflightDependencyEntry(entry.path, packageJSON, packageLock)
	default:
		return false
	}
}

func frontendPreflightFrontendSourceEntry(entry remoteGitTreeEntry) bool {
	frontendPath := strings.TrimPrefix(entry.path, frontendAppInputPrefix)
	return strings.HasPrefix(entry.path, frontendAppInputPrefix) &&
		!strings.HasPrefix(entry.path, frontendE2EInputPrefix) && !frontendPlaywrightConfigSourcePath(frontendPath)
}

// frontendPreflightCriticalGuardEntry 判断 critical guard 的完整可观察输入。
func frontendPreflightCriticalGuardEntry(entryPath, packageJSON string) bool {
	// guard:critical-skip recursively observes the three roots below, including
	// Playwright specs under tests/e2e. Keep the owner package manifest in the
	// closure because its command chain determines which guards execute.
	return frontendPreflightToolchainEntry(entryPath) || entryPath == packageJSON || entryPath == "frontend-app/package-lock.json" || strings.HasPrefix(entryPath, "frontend-app/src/") ||
		strings.HasPrefix(entryPath, "frontend-app/scripts/") || strings.HasPrefix(entryPath, "frontend-app/tests/") ||
		entryPath == "frontend-app/config/action-producer-registry.json" || entryPath == "frontend-app/config/action-producer-test-matrix.json"
}

func frontendPreflightTurnVerifyEntry(entryPath, packageJSON string) bool {
	return frontendPreflightTurnContractEntry(entryPath) || entryPath == packageJSON || entryPath == "frontend-app/package-lock.json"
}

func frontendPreflightCriticalTypecheckEntry(entry remoteGitTreeEntry, packageLock string) bool {
	return frontendPreflightNonE2EEntry(entry) || entry.path == packageLock || frontendPreflightToolchainEntry(entry.path)
}

func frontendPreflightContractsEntry(entry remoteGitTreeEntry, packageLock string) bool {
	return frontendPreflightNonE2EEntry(entry) || entry.path == packageLock || frontendPreflightToolchainEntry(entry.path)
}

// frontendPreflightRPCAuditEntry 选择 RPC audit 的源码、工具链和契约输入。
func frontendPreflightRPCAuditEntry(entryPath, packageJSON string) bool {
	return frontendPreflightToolchainEntry(entryPath) || entryPath == "frontend-app/package-lock.json" || strings.HasPrefix(entryPath, "frontend-app/src/") || entryPath == packageJSON ||
		entryPath == "frontend-app/scripts/rpc-contract-audit.mjs" || strings.HasPrefix(entryPath, "internal/") ||
		strings.HasPrefix(entryPath, "cmd/")
}

func frontendPreflightNonE2EEntry(entry remoteGitTreeEntry) bool {
	return frontendPreflightFrontendSourceEntry(entry)
}

func frontendPreflightTurnContractEntry(entryPath string) bool {
	return frontendPreflightToolchainEntry(entryPath) || strings.HasPrefix(entryPath, "scripts/turncontract/") || strings.HasPrefix(entryPath, "internal/dto/turn/") ||
		entryPath == "frontend-app/src/shared/contracts/turnContracts.generated.js"
}

func frontendPreflightTurnFieldEntry(entryPath, packageJSON string) bool {
	// immutableRepositoryBaseline/discoverJSValidatorConsumers walks every
	// production JS/JSX file below frontend-app/src, not only the generated
	// schema. Include that entire observed root so a newly added consumer cannot
	// reuse an old PASS.
	return frontendPreflightTurnContractEntry(entryPath) || entryPath == packageJSON || entryPath == "frontend-app/package-lock.json" || strings.HasPrefix(entryPath, "frontend-app/src/") ||
		strings.HasPrefix(entryPath, "frontend-app/scripts/turn-contract-field-guard")
}

func frontendPreflightDependencyEntry(entryPath, packageJSON, packageLock string) bool {
	return entryPath == packageJSON || entryPath == packageLock || strings.HasPrefix(entryPath, "frontend-app/scripts/frontend-execution-closure") ||
		strings.HasPrefix(entryPath, "frontend-app/scripts/frontend-maintainability-dependency") || strings.HasPrefix(entryPath, "frontend-app/scripts/refresh-frontend-maintainability-dependencies")
}

// frontendPreflightToolchainEntry binds every root Go module descriptor consumed
// by preflight commands, including an optional workspace file. Missing files are
// naturally absent from the exact tree; if a workspace is introduced it cannot
// silently reuse a prior PASS.
func frontendPreflightToolchainEntry(entryPath string) bool {
	switch entryPath {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	default:
		return false
	}
}

// frontendPlaywrightConfigSourcePath 判断不属于 Vitest/preflight 执行闭包的 E2E 配置。
func frontendPlaywrightConfigSourcePath(relative string) bool {
	return strings.HasPrefix(relative, "playwright.") && strings.HasSuffix(relative, ".config.js")
}

// frontendBuildInputDigest 只绑定 Vite build 及其同步脚本的生产输入。
func (snapshot *remoteGitTreeSnapshot) frontendBuildInputDigest() (string, error) {
	selected := make(map[string]remoteGitTreeEntry)
	for _, entry := range snapshot.entries {
		if frontendBuildInputEntry(entry) {
			selected[entry.path] = entry
		}
	}
	seeds, observesDynamic, err := snapshot.frontendBuildEntrySeeds(context.Background())
	if err != nil {
		return "", err
	}
	if observesDynamic {
		return snapshot.digestEntries(snapshot.entries)
	}
	closure, observesDynamic, err := snapshot.frontendStaticImportClosure(context.Background(), seeds)
	if err != nil {
		return "", err
	}
	if observesDynamic {
		return snapshot.digestEntries(snapshot.entries)
	}
	for filePath := range closure {
		selected[filePath] = snapshot.byPath[filePath]
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

// frontendBuildInputEntry 判断单个 Git tree 条目是否位于前端构建树中。
func frontendBuildInputEntry(entry remoteGitTreeEntry) bool {
	if !strings.HasPrefix(entry.path, frontendAppInputPrefix) {
		return false
	}
	relative := strings.TrimPrefix(entry.path, frontendAppInputPrefix)
	return slices.Contains([]string{"package.json", "package-lock.json", "index.html", "vite.config.js", "recovery.html", "required-dist-entries.txt", "scripts/sync-frontend-dist.mjs"}, relative) ||
		strings.HasPrefix(relative, "config/") || strings.HasPrefix(relative, "public/") ||
		strings.HasPrefix(relative, "src/") && !frontendTestSourcePath(relative)
}

// frontendBuildEntrySeeds 解析 Vite 与 recovery HTML 的构建入口依赖。
func (snapshot *remoteGitTreeSnapshot) frontendBuildEntrySeeds(ctx context.Context) ([]string, bool, error) {
	seeds := []string{frontendAppInputPrefix + "vite.config.js"}
	htmlPaths := []string{frontendAppInputPrefix + "index.html", frontendAppInputPrefix + "recovery.html"}
	for _, filePath := range append(append([]string{}, seeds...), htmlPaths...) {
		if _, ok := snapshot.byPath[filePath]; !ok {
			return nil, true, nil
		}
	}
	sources, err := snapshot.frontendSourceBlobs(ctx, htmlPaths)
	if err != nil {
		return nil, false, err
	}
	for _, htmlPath := range htmlPaths {
		imports, dynamic := frontendHTMLScriptSources(sources[htmlPath])
		if dynamic {
			return seeds, true, nil
		}
		for _, spec := range imports {
			resolved, local, err := snapshot.resolveFrontendImport(htmlPath, spec)
			if err != nil {
				return nil, false, err
			}
			if local {
				seeds = append(seeds, resolved)
			}
		}
	}
	return seeds, false, nil
}

// frontendHTMLScriptSources 解析 HTML module script，并对未知标签形态 fail-close。
func frontendHTMLScriptSources(source []byte) ([]string, bool) {
	const marker = `<script type="module" src="`
	text := strings.ToLower(string(source))
	if strings.Contains(text, "<script") && !strings.Contains(text, marker) {
		return nil, true
	}
	imports := make([]string, 0, 1)
	offset := 0
	for {
		relative := strings.Index(text[offset:], marker)
		if relative < 0 {
			break
		}
		value := offset + relative + len(marker)
		close := strings.IndexByte(text[value:], '"')
		if close < 0 {
			return imports, true
		}
		imports = append(imports, string(source[value:value+close]))
		offset = value + close + 1
	}
	if strings.Contains(text[offset:], "<script") {
		return imports, true
	}
	return imports, len(imports) == 0
}

// frontendTestSourcePath 判断路径是否属于前端测试源码而非生产入口。
func frontendTestSourcePath(relative string) bool {
	base := path.Base(relative)
	stem := strings.TrimSuffix(base, path.Ext(base))
	dir := "/" + path.Dir(relative) + "/"
	return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") ||
		strings.Contains(dir, "/__tests__/") || strings.Contains(dir, "/__specs__/") ||
		strings.Contains(dir, "/test/") || strings.Contains(dir, "/tests/") ||
		strings.Contains(dir, "/spec/") || strings.Contains(dir, "/specs/")
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
	seeds := snapshot.frontendPlaywrightParentSeeds()
	if len(seeds) == 0 {
		return "", fmt.Errorf("Playwright parent input closure has no tracked specs or configs")
	}
	closure, observesDynamic, err := snapshot.frontendStaticImportClosure(ctx, seeds)
	if err != nil {
		return "", err
	}
	if observesDynamic {
		return snapshot.digestEntries(snapshot.entries)
	}
	for filePath := range closure {
		selected[filePath] = snapshot.byPath[filePath]
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

// frontendPlaywrightParentSeeds derives the parent closure from the tracked
// Playwright execution roots instead of duplicating a catalog target list here.
// The gate catalog may add a describe target within an existing spec; that
// target must observe the same spec/config closure. A newly tracked spec or
// config is conservatively included, yielding a false miss rather than a hit.
// frontendPlaywrightParentSeeds 收集所有已跟踪 Playwright spec/config 入口。
func (snapshot *remoteGitTreeSnapshot) frontendPlaywrightParentSeeds() []string {
	seeds := make([]string, 0)
	for _, entry := range snapshot.entries {
		if strings.HasPrefix(entry.path, "frontend-app/tests/e2e/") && strings.HasSuffix(entry.path, ".spec.js") {
			seeds = append(seeds, entry.path)
			continue
		}
		relative := strings.TrimPrefix(entry.path, frontendAppInputPrefix)
		if strings.HasPrefix(entry.path, frontendAppInputPrefix) && frontendPlaywrightConfigSourcePath(relative) {
			seeds = append(seeds, entry.path)
		}
	}
	return seeds
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
	if base == "." || base == ".." || strings.HasPrefix(base, "../") {
		return "", false, fmt.Errorf("frontend import %q from %q escapes repository root", importPath, currentPath)
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
	if relative, ok := strings.CutPrefix(importPath, "/"); ok {
		return path.Clean(path.Join("frontend-app", relative))
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
	case "fs":
		return frontendFSWord(source, start)
	case "addInitScript":
		return frontendAddInitScriptWord(source, start)
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
	resolve := bytes.HasPrefix(source[next:], []byte(".resolve"))
	if resolve {
		resolveEnd := next + len(".resolve")
		if resolveEnd < len(source) && frontendIdentifierContinue(source[resolveEnd]) {
			return nil, false, start
		}
		next = skipFrontendIgnorable(source, resolveEnd)
	}
	if next >= len(source) || source[next] != '(' {
		return nil, resolve, next
	}
	next = skipFrontendIgnorable(source, next+1)
	if spec, end, ok := frontendStringAt(source, next); ok {
		return []string{spec}, false, end
	}
	return nil, true, next
}

// frontendFSWord 解析 fs.readFileSync 的静态或动态路径观察。
func frontendFSWord(source []byte, start int) ([]string, bool, int) {
	next := skipFrontendIgnorable(source, start)
	if !bytes.HasPrefix(source[next:], []byte(".readFileSync")) {
		return nil, false, start
	}
	next = skipFrontendIgnorable(source, next+len(".readFileSync"))
	if next >= len(source) {
		return nil, true, next
	}
	if source[next] != '(' {
		return nil, true, next
	}
	next = skipFrontendIgnorable(source, next+1)
	if spec, end, ok := frontendStringAt(source, next); ok {
		return []string{spec}, false, end
	}
	if next >= len(source) || !frontendIdentifierStart(source[next]) {
		return nil, true, next
	}
	word, end := frontendIdentifierAt(source, next)
	if word == "require" {
		return frontendRequireWord(source, end)
	}
	return nil, true, next
}

// frontendAddInitScriptWord observes Playwright's optional external init script.
// A direct string path is resolved into the exact frontend tree; computed paths
// are intentionally dynamic and force the caller to hash the whole observed tree.
// frontendAddInitScriptWord 解析 Playwright addInitScript 的外部脚本观察。
func frontendAddInitScriptWord(source []byte, start int) ([]string, bool, int) {
	callOpen := skipFrontendIgnorable(source, start)
	if callOpen >= len(source) || source[callOpen] != '(' {
		return nil, false, start
	}
	callEnd, ok := frontendBalancedDelimiter(source, callOpen, '(', ')')
	if !ok {
		return nil, true, len(source)
	}
	next := skipFrontendIgnorable(source, callOpen+1)
	if next >= callEnd-1 {
		return nil, true, callEnd
	}
	if spec, _, ok := frontendStringAt(source, next); ok {
		return []string{spec}, false, callEnd
	}
	if frontendStartsInlineCallback(source, next, callEnd) {
		return nil, frontendCallbackHasExternalRead(source[next : callEnd-1]), callEnd
	}
	if source[next] == '{' {
		imports, observesDynamic, _ := frontendAddInitScriptObject(source, next)
		return imports, observesDynamic, callEnd
	}
	return nil, true, callEnd
}

// frontendBalancedDelimiter 匹配 JavaScript 分隔符并跳过字符串与注释。
func frontendBalancedDelimiter(source []byte, start int, open, close byte) (int, bool) {
	depth := 0
	for index := start; index < len(source); index++ {
		next, skipped := frontendSkipLexeme(source, index)
		if skipped {
			index = next - 1
			continue
		}
		switch source[index] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return len(source), false
}

func frontendStartsInlineCallback(source []byte, start, callEnd int) bool {
	cursor := skipFrontendIgnorable(source, start)
	if cursor >= callEnd {
		return false
	}
	if bytes.HasPrefix(source[cursor:], []byte("function")) {
		return true
	}
	if bytes.HasPrefix(source[cursor:], []byte("async")) {
		cursor = skipFrontendIgnorable(source, cursor+len("async"))
		if bytes.HasPrefix(source[cursor:], []byte("function")) {
			return true
		}
	}
	return bytes.Contains(source[cursor:callEnd], []byte("=>"))
}

func frontendCallbackHasExternalRead(source []byte) bool {
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
		if frontendCallbackIdentifierHasExternalRead(source, word, end) {
			return true
		}
		index = end
	}
	return false
}

// frontendCallbackIdentifierHasExternalRead 判断回调是否读取外部运行时输入。
func frontendCallbackIdentifierHasExternalRead(source []byte, word string, end int) bool {
	next := skipFrontendIgnorable(source, end)
	switch word {
	case "fetch", "WebSocket", "XMLHttpRequest", "EventSource":
		return next < len(source) && source[next] == '('
	case "document", "navigator", "location", "localStorage", "sessionStorage", "indexedDB", "performance", "crypto", "process", "runtime":
		return true
	case "window", "globalThis", "self":
		if next >= len(source) || source[next] != '.' {
			return false
		}
		next = skipFrontendIgnorable(source, next+1)
		if next >= len(source) || !frontendIdentifierStart(source[next]) {
			return false
		}
		property, _ := frontendIdentifierAt(source, next)
		property = strings.ToLower(property)
		return strings.Contains(property, "runtime") || containsString([]string{"fetch", "websocket", "xmlhttprequest", "eventsource", "navigator", "document", "location", "localstorage", "sessionstorage", "indexeddb", "performance", "crypto", "process"}, property)
	default:
		return false
	}
}

// frontendAddInitScriptObject 解析 addInitScript 对象的 path/content 字段。
func frontendAddInitScriptObject(source []byte, start int) ([]string, bool, int) {
	end, ok := frontendBalancedDelimiter(source, start, '{', '}')
	if !ok {
		return nil, true, len(source)
	}
	body := source[start:end]
	pathAt := bytes.Index(body, []byte("path"))
	if pathAt >= 0 {
		value := skipFrontendIgnorable(body, pathAt+len("path"))
		if value < len(body) && body[value] == ':' {
			value = skipFrontendIgnorable(body, value+1)
			if spec, _, ok := frontendStringAt(body, value); ok {
				return []string{spec}, false, end
			}
			return nil, true, end
		}
	}
	imports, dynamic := frontendJavaScriptImports(body)
	if dynamic || len(imports) > 0 {
		return imports, dynamic, end
	}
	if bytes.Contains(body, []byte("content")) {
		return nil, true, end
	}
	return nil, false, end
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
