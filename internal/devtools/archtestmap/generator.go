package archtestmap

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

const (
	ruleMapPath      = "docs/doc/codemap/13-archtest-boundaries.md"
	readmePath       = "README.md"
	statsBeginMarker = "<!-- BEGIN GENERATED ARCHTEST STATS -->"
	statsEndMarker   = "<!-- END GENERATED ARCHTEST STATS -->"
	codeQualityTitle = "## Code Quality"
	metricsHeader    = "| Metric | Value |"
	metricsDivider   = "|--------|-------|"
	archtestRowStart = "| Architecture Tests | "
)

type archtestStats struct {
	Tests int
	Files int
}

type readmeHeading struct {
	text         string
	start        int
	contentStart int
}

type generatedArtifact struct {
	path    string
	content string
	update  func(string) (string, error)
}

type generatedFileState struct {
	content []byte
	mode    fs.FileMode
	exists  bool
}

type stagedGeneratedArtifact struct {
	generatedArtifact
	original generatedFileState
	tempPath string
}

type generatedFileOps struct {
	rename        func(*os.Root, string, string) error
	afterValidate func() error
}

// Generate 从统一 registry 和源码 AST 生成或只读校验规则地图及 README 统计。
func Generate(root string, check bool) error {
	return runWithRegistry(root, archtest.DefaultBackendBoundaryRegistry(), check)
}

func runWithRegistry(root string, registry archtest.BackendBoundaryRegistry, check bool) error {
	if violations := archtest.ValidateBackendBoundaryGovernance(root, registry); len(violations) > 0 {
		return fmt.Errorf("invalid backend boundary governance:\n%s", strings.Join(violations, "\n"))
	}
	stats, err := collectArchtestStats(root)
	if err != nil {
		return err
	}
	return syncGeneratedFiles(root, []generatedArtifact{
		{path: filepath.Join(root, ruleMapPath), content: renderRuleMap(registry)},
		{path: filepath.Join(root, readmePath), update: func(input string) (string, error) {
			return replaceREADMEStats(input, stats)
		}},
	}, check)
}

// renderRuleMap 按稳定键排序输出 owner、rule、guard 与 surface 的完整治理地图。
func renderRuleMap(registry archtest.BackendBoundaryRegistry) string {
	var out strings.Builder
	out.WriteString("# 13 Archtest 后端边界规则地图\n\n")
	out.WriteString("> 由 `go run ./scripts/archtestmap` 从 `DefaultBackendBoundaryRegistry()` 自动生成。请勿手工维护本页事实。\n\n")
	fmt.Fprintf(&out, "- Owners: %d\n- Canonical rules: %d\n- Specialized guards: %d\n- Governed backend surfaces: %d\n\n", len(registry.Owners), len(registry.Rules), len(registry.Guards), len(registry.Surfaces))
	renderOwners(&out, registry.Owners)
	renderRules(&out, registry.Rules)
	renderGuards(&out, registry.Guards)
	renderSurfaces(&out, registry.Surfaces)
	return out.String()
}

func renderOwners(out *strings.Builder, owners []archtest.BackendBoundaryOwner) {
	items := append([]archtest.BackendBoundaryOwner(nil), owners...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	out.WriteString("## Rule owners\n\n| Owner | File patterns | Reason |\n|---|---|---|\n")
	for _, owner := range items {
		fmt.Fprintf(out, "| `%s` | %s | %s |\n", owner.ID, renderCodeList(owner.FilePatterns), escapeMarkdown(owner.Reason))
	}
	out.WriteString("\n")
}

// renderRules 输出 typed rule 的求值种类、匹配范围与政策摘要。
func renderRules(out *strings.Builder, rules []archtest.BackendBoundaryRule) {
	items := append([]archtest.BackendBoundaryRule(nil), rules...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	out.WriteString("## Canonical rules\n\n| Rule | Owner | Kind | Files | Allow | Deny | Scope allow | Exceptions | Reason |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, rule := range items {
		fmt.Fprintf(out, "| `%s` | `%s` | `%s` | %s | %s | %s | %s | %s | %s |\n",
			rule.ID, rule.Owner, rule.Kind, renderCodeList(rule.FilePatterns), renderImportPolicies(rule.Allow),
			renderImportPolicies(rule.Deny), renderFilePolicies(rule.ScopeAllow), renderExceptions(rule.Exceptions), escapeMarkdown(rule.Reason))
	}
	out.WriteString("\n")
}

func renderGuards(out *strings.Builder, guards []archtest.BackendBoundaryGuard) {
	items := append([]archtest.BackendBoundaryGuard(nil), guards...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	out.WriteString("## Specialized guards\n\n")
	out.WriteString("| Guard | Test file | Build tags | Runnable tests | Applies to | Reason |\n")
	out.WriteString("|---|---|---|---|---|---|\n")
	for _, guard := range items {
		fmt.Fprintf(
			out,
			"| `%s` | `%s` | %s | %s | %s | %s |\n",
			guard.ID,
			guard.File,
			renderCodeList(guard.BuildTags),
			renderCodeList(guard.TestNames),
			renderSurfaceIDs(guard.AppliesTo),
			escapeMarkdown(guard.Reason),
		)
	}
	out.WriteString("\n")
}

func renderSurfaces(out *strings.Builder, surfaces []archtest.BackendBoundarySurface) {
	items := append([]archtest.BackendBoundarySurface(nil), surfaces...)
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	out.WriteString("## Governed backend surfaces\n\n| Surface | Canonical rules | Specialized guards | Reason |\n|---|---|---|---|\n")
	for _, surface := range items {
		fmt.Fprintf(out, "| `%s` | %s | %s | %s |\n", surface.Path, renderRuleIDs(surface.RuleIDs), renderGuardIDs(surface.GuardIDs), escapeMarkdown(surface.Reason))
	}
}

func renderImportPolicies(policies []archtest.BoundaryImportPolicy) string {
	if len(policies) > 8 {
		patterns := make(map[string]bool)
		for _, policy := range policies {
			patterns[policy.FilePattern] = true
		}
		return fmt.Sprintf("%d policies across %d file patterns", len(policies), len(patterns))
	}
	items := make([]string, 0, len(policies))
	for _, policy := range policies {
		items = append(items, fmt.Sprintf("`%s` → `%s`", policy.FilePattern, policy.ImportPrefix))
	}
	return renderSortedItems(items)
}

func renderFilePolicies(policies []archtest.BoundaryFilePolicy) string {
	items := make([]string, 0, len(policies))
	for _, policy := range policies {
		items = append(items, fmt.Sprintf("`%s` (`%s`)", policy.FilePattern, policy.Scope))
	}
	return renderSortedItems(items)
}

func renderExceptions(exceptions []archtest.BoundaryException) string {
	items := make([]string, 0, len(exceptions))
	for _, exception := range exceptions {
		items = append(items, fmt.Sprintf("`%s`: `%s` → `%s` (`%s`)", exception.ID, exception.FilePattern, exception.ImportPrefix, exception.Class))
	}
	return renderSortedItems(items)
}

func renderRuleIDs(ids []archtest.BoundaryRuleID) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, "`"+string(id)+"`")
	}
	return renderSortedItems(items)
}

func renderGuardIDs(ids []archtest.BoundaryGuardID) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, "`"+string(id)+"`")
	}
	return renderSortedItems(items)
}

func renderSurfaceIDs(ids []archtest.BoundarySurfaceID) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, "`"+string(id)+"`")
	}
	return renderSortedItems(items)
}

func renderCodeList(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, "`"+value+"`")
	}
	return renderSortedItems(items)
}

func renderSortedItems(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	sort.Strings(items)
	return strings.Join(items, "<br>")
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", " ")
}

// collectArchtestStats 以 Go testing 顶层 Test 签名规则统计源码函数和非空测试文件。
func collectArchtestStats(root string) (archtestStats, error) {
	base := filepath.Join(root, "internal", "archtest")
	skip := archtest.DefaultSkipDirs()
	var stats archtestStats
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		return collectArchtestStatEntry(base, path, entry, walkErr, skip, &stats)
	})
	if err != nil {
		return archtestStats{}, fmt.Errorf("collect archtest stats: %w", err)
	}
	if stats.Tests == 0 || stats.Files == 0 {
		return archtestStats{}, fmt.Errorf("archtest source AST contains zero runnable tests")
	}
	return stats, nil
}

// collectArchtestStatEntry 处理单个目录项并把可运行测试累计到统计值。
func collectArchtestStatEntry(base, path string, entry fs.DirEntry, walkErr error, skip map[string]bool, stats *archtestStats) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() && path != base && skip[entry.Name()] {
		return filepath.SkipDir
	}
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
		return nil
	}
	names, err := archtest.DiscoverRunnableGoTestsFromSource(path)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(names) > 0 {
		stats.Files++
		stats.Tests += len(names)
	}
	return nil
}

// replaceREADMEStats 只替换唯一 Architecture Tests 表格行内有序 marker 的内容。
func replaceREADMEStats(input string, stats archtestStats) (string, error) {
	begin, end, err := readmeArchtestStatsBounds(input)
	if err != nil {
		return "", err
	}
	value := fmt.Sprintf("Source AST: %d runnable `Test*` functions across %d `_test.go` files in `internal/archtest`", stats.Tests, stats.Files)
	return input[:begin] + value + input[end:], nil
}

// readmeArchtestStatsBounds 校验唯一表格行和 marker，并返回可替换内容边界。
func readmeArchtestStatsBounds(input string) (int, int, error) {
	markerStart, begin, end, err := readmeArchtestMarkerBounds(input)
	if err != nil {
		return 0, 0, err
	}
	sectionStart, sectionEnd, err := readmeCodeQualitySectionBounds(input)
	if err != nil {
		return 0, 0, err
	}
	rowStart, rowEnd := readmeLineBounds(input, markerStart)
	if err := validateReadmeArchtestRow(input, markerStart, end, rowStart, rowEnd, sectionStart, sectionEnd); err != nil {
		return 0, 0, err
	}
	return begin, end, nil
}

func readmeArchtestMarkerBounds(input string) (int, int, int, error) {
	if strings.Count(input, statsBeginMarker) != 1 || strings.Count(input, statsEndMarker) != 1 {
		return 0, 0, 0, fmt.Errorf("README archtest stats markers must each appear exactly once")
	}
	markerStart := strings.Index(input, statsBeginMarker)
	begin, end := markerStart+len(statsBeginMarker), strings.Index(input, statsEndMarker)
	if begin > end {
		return 0, 0, 0, fmt.Errorf("README archtest stats markers are reversed")
	}
	return markerStart, begin, end, nil
}

func readmeLineBounds(input string, position int) (int, int) {
	start, end := strings.LastIndex(input[:position], "\n")+1, len(input)
	if offset := strings.Index(input[position:], "\n"); offset >= 0 {
		end = position + offset
	}
	return start, end
}

// validateReadmeArchtestRow 拒绝伪表格、围栏内容、缩进副本和额外单元格。
func validateReadmeArchtestRow(input string, markerStart, end, rowStart, rowEnd, sectionStart, sectionEnd int) error {
	if countREADMEArchitectureTestRows(input) != 1 {
		return fmt.Errorf("README must contain exactly one Architecture Tests row")
	}
	if rowStart < sectionStart || rowEnd > sectionEnd || readmePositionIsFenced(input[sectionStart:rowStart]) {
		return fmt.Errorf("README Architecture Tests row must be an unfenced row in the Code Quality section")
	}
	markerEnd := end + len(statsEndMarker)
	row := input[rowStart:rowEnd]
	if markerStart != rowStart+len(archtestRowStart) || markerEnd > rowEnd || row != archtestRowStart+input[markerStart:markerEnd]+" |" {
		return fmt.Errorf("README archtest stats markers must occupy the Value cell of a two-column Architecture Tests row")
	}
	if !readmeMetricsTablePrecedesRow(input[sectionStart:rowStart]) {
		return fmt.Errorf("README Architecture Tests row must belong to the Code Quality metrics table")
	}
	return nil
}

func countREADMEArchitectureTestRows(input string) int {
	count := 0
	for line := range strings.SplitSeq(input, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), archtestRowStart) {
			count++
		}
	}
	return count
}

// readmeCodeQualitySectionBounds 返回唯一未围栏 Code Quality 二级章节的内容区间。
func readmeCodeQualitySectionBounds(input string) (int, int, error) {
	headings := unfencedReadmeSectionHeadings(input)
	target := -1
	for index, heading := range headings {
		if heading.text == codeQualityTitle {
			if target >= 0 {
				return 0, 0, fmt.Errorf("README must contain exactly one unfenced Code Quality section")
			}
			target = index
		}
	}
	if target < 0 {
		return 0, 0, fmt.Errorf("README must contain exactly one unfenced Code Quality section")
	}
	end := len(input)
	if target+1 < len(headings) {
		end = headings[target+1].start
	}
	return headings[target].contentStart, end, nil
}

// unfencedReadmeSectionHeadings 返回可终止二级章节的未围栏一级或二级标题。
func unfencedReadmeSectionHeadings(input string) []readmeHeading {
	var headings []readmeHeading
	offset, fenced := 0, false
	for line := range strings.SplitSeq(input, "\n") {
		if !fenced && (strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ")) {
			contentStart := min(offset+len(line)+1, len(input))
			headings = append(headings, readmeHeading{text: line, start: offset, contentStart: contentStart})
		}
		if isMarkdownFenceLine(line) {
			fenced = !fenced
		}
		offset += len(line) + 1
	}
	return headings
}

func readmePositionIsFenced(input string) bool {
	fenced := false
	for line := range strings.SplitSeq(input, "\n") {
		if isMarkdownFenceLine(line) {
			fenced = !fenced
		}
	}
	return fenced
}

// readmeMetricsTablePrecedesRow 要求目标行紧随以指标表头开头的连续 Markdown 表格块。
func readmeMetricsTablePrecedesRow(input string) bool {
	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	blockStart := len(lines)
	for blockStart > 0 && isReadmeTableRow(lines[blockStart-1]) {
		blockStart--
	}
	return len(lines)-blockStart >= 2 && lines[blockStart] == metricsHeader && lines[blockStart+1] == metricsDivider
}

func isReadmeTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|")
}

func isMarkdownFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// syncGeneratedFile 保留单文件调用兼容性，并使用同一暂存和失败回滚路径。
func syncGeneratedFile(path, content string, check bool) error {
	return syncGeneratedFiles(filepath.Dir(path), []generatedArtifact{{path: path, content: content}}, check)
}

// syncGeneratedFiles 逐文件原子替换，并在批次失败时尽力回滚已替换目标。
func syncGeneratedFiles(root string, artifacts []generatedArtifact, check bool) error {
	return syncGeneratedFilesWithOps(root, artifacts, check, generatedFileOps{rename: renameGeneratedArtifact})
}

// syncGeneratedFilesWithOps 注入 rename 仅用于验证第二次提交失败时的回滚路径。
func syncGeneratedFilesWithOps(root string, artifacts []generatedArtifact, check bool, ops generatedFileOps) (resultErr error) {
	rootPath, relativeArtifacts, err := prepareGeneratedArtifacts(root, artifacts)
	if err != nil {
		return err
	}
	rootHandle, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open generated root %s: %w", root, err)
	}
	defer func() {
		if closeErr := rootHandle.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close generated root %s: %w", root, closeErr))
		}
	}()
	changed, err := changedGeneratedArtifacts(rootHandle, relativeArtifacts, ops.afterValidate)
	if err != nil || len(changed) == 0 {
		return err
	}
	if check {
		return fmt.Errorf("generated file is stale: %s", changed[0].path)
	}
	if ops.rename == nil {
		return fmt.Errorf("generated file rename operation is nil")
	}
	staged, err := stageGeneratedArtifacts(rootHandle, changed)
	if err != nil {
		return err
	}
	defer cleanupGeneratedTemps(rootHandle, staged)
	return commitGeneratedArtifacts(rootHandle, staged, ops)
}

// commitGeneratedArtifacts 顺序提交暂存文件，失败时只回滚已提交前缀并保留双重失败上下文。
func commitGeneratedArtifacts(root *os.Root, staged []stagedGeneratedArtifact, ops generatedFileOps) error {
	for index := range staged {
		if err := ops.rename(root, staged[index].tempPath, staged[index].path); err != nil {
			commitErr := fmt.Errorf("commit generated file %s: %w", staged[index].path, err)
			rollbackErr := rollbackGeneratedArtifacts(root, staged[:index], ops)
			if rollbackErr != nil {
				return errors.Join(commitErr, fmt.Errorf("generated rollback incomplete; outputs may be partially refreshed: %w", rollbackErr))
			}
			return commitErr
		}
	}
	return nil
}

func renameGeneratedArtifact(root *os.Root, oldPath, newPath string) error {
	return root.Rename(oldPath, newPath)
}

// prepareGeneratedArtifacts 在打开 os.Root 前保留既有词法与父 symlink 预检，并把目标转换为 root-relative 路径。
func prepareGeneratedArtifacts(root string, artifacts []generatedArtifact) (string, []generatedArtifact, error) {
	if err := validateGeneratedArtifactPaths(root, artifacts); err != nil {
		return "", nil, err
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve generated root %s: %w", root, err)
	}
	relative := make([]generatedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		pathAbsolute, err := filepath.Abs(artifact.path)
		if err != nil {
			return "", nil, fmt.Errorf("resolve generated artifact path %s: %w", artifact.path, err)
		}
		pathRelative, err := filepath.Rel(rootAbsolute, pathAbsolute)
		if err != nil {
			return "", nil, fmt.Errorf("make generated artifact root-relative %s: %w", artifact.path, err)
		}
		relative = append(relative, generatedArtifact{path: filepath.Clean(pathRelative), content: artifact.content, update: artifact.update})
	}
	return rootAbsolute, relative, nil
}

// validateGeneratedArtifactPaths 在读取或写入前确认所有目标的现存父路径解析后仍位于仓库内。
func validateGeneratedArtifactPaths(root string, artifacts []generatedArtifact) error {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve generated root %s: %w", root, err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return fmt.Errorf("resolve generated root %s: %w", root, err)
	}
	rootInfo, err := os.Stat(rootResolved)
	if err != nil {
		return fmt.Errorf("inspect generated root %s: %w", root, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("generated root is not a directory: %s", root)
	}
	for _, artifact := range artifacts {
		if err := validateGeneratedArtifactPath(rootAbsolute, rootResolved, artifact.path); err != nil {
			return err
		}
	}
	return nil
}

// validateGeneratedArtifactPath 同时校验词法路径与最近现存父目录的真实解析路径。
func validateGeneratedArtifactPath(rootAbsolute, rootResolved, path string) error {
	pathAbsolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve generated artifact path %s: %w", path, err)
	}
	if pathAbsolute == rootAbsolute || !generatedPathWithin(rootAbsolute, pathAbsolute) {
		return fmt.Errorf("generated artifact path escapes repository root: %s", path)
	}
	existingParent, err := nearestExistingGeneratedParent(filepath.Dir(pathAbsolute))
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(existingParent)
	if err != nil {
		return fmt.Errorf("resolve generated artifact parent %s: %w", existingParent, err)
	}
	parentInfo, err := os.Stat(resolvedParent)
	if err != nil {
		return fmt.Errorf("inspect generated artifact parent %s: %w", existingParent, err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("generated artifact parent is not a directory: %s", existingParent)
	}
	if !generatedPathWithin(rootResolved, resolvedParent) {
		return fmt.Errorf("generated artifact parent escapes repository root: %s", path)
	}
	return nil
}

func nearestExistingGeneratedParent(path string) (string, error) {
	for {
		_, err := os.Lstat(path)
		if err == nil {
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect generated artifact parent %s: %w", path, err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("generated artifact has no existing parent: %s", path)
		}
		path = parent
	}
}

func generatedPathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// changedGeneratedArtifacts 预读整批目标，保证任何暂存或提交前已取得完整旧状态。
func changedGeneratedArtifacts(root *os.Root, artifacts []generatedArtifact, afterValidate func() error) ([]stagedGeneratedArtifact, error) {
	seen := make(map[string]bool, len(artifacts))
	changed := make([]stagedGeneratedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if seen[artifact.path] {
			return nil, fmt.Errorf("duplicate generated artifact path: %s", artifact.path)
		}
		seen[artifact.path] = true
		state, err := readGeneratedFileState(root, artifact.path)
		if err != nil {
			return nil, err
		}
		if artifact.update != nil {
			artifact.content, err = artifact.update(string(state.content))
			if err != nil {
				return nil, fmt.Errorf("render generated file %s: %w", artifact.path, err)
			}
		}
		if state.exists && string(state.content) == artifact.content {
			continue
		}
		changed = append(changed, stagedGeneratedArtifact{generatedArtifact: artifact, original: state})
	}
	if afterValidate != nil {
		if err := afterValidate(); err != nil {
			return nil, fmt.Errorf("after generated artifact validation: %w", err)
		}
	}
	return changed, nil
}

// readGeneratedFileState 读取普通生成文件的内容、权限和存在状态。
func readGeneratedFileState(root *os.Root, path string) (generatedFileState, error) {
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return generatedFileState{mode: 0o644}, nil
	}
	if err != nil {
		return generatedFileState{}, fmt.Errorf("inspect generated file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return generatedFileState{}, fmt.Errorf("generated target is not a regular file: %s", path)
	}
	content, err := root.ReadFile(path)
	if err != nil {
		return generatedFileState{}, fmt.Errorf("read generated file %s: %w", path, err)
	}
	return generatedFileState{content: content, mode: info.Mode().Perm(), exists: true}, nil
}

func stageGeneratedArtifacts(root *os.Root, artifacts []stagedGeneratedArtifact) ([]stagedGeneratedArtifact, error) {
	staged := append([]stagedGeneratedArtifact(nil), artifacts...)
	for index := range staged {
		tempPath, err := writeGeneratedTemp(root, staged[index].path, []byte(staged[index].content), staged[index].original.mode)
		if err != nil {
			cleanupGeneratedTemps(root, staged)
			return nil, err
		}
		staged[index].tempPath = tempPath
	}
	return staged, nil
}

// writeGeneratedTemp 在目标目录完整写入并同步临时文件，目标文件此时仍保持不变。
func writeGeneratedTemp(root *os.Root, path string, content []byte, mode fs.FileMode) (string, error) {
	if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create generated directory for %s: %w", path, err)
	}
	temp, tempPath, err := createGeneratedTemp(root, path)
	if err != nil {
		return "", fmt.Errorf("create generated temp for %s: %w", path, err)
	}
	if err := writeAndCloseGeneratedTemp(temp, content, mode); err != nil {
		_ = root.Remove(tempPath)
		return "", fmt.Errorf("stage generated file %s: %w", path, err)
	}
	return tempPath, nil
}

// createGeneratedTemp 在目标目录以随机 basename 和 O_EXCL 建立暂存文件，碰撞时重新取名。
func createGeneratedTemp(root *os.Root, path string) (*os.File, string, error) {
	for range 16 {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", err
		}
		tempPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.tmp-%x", filepath.Base(path), suffix))
		temp, err := root.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return temp, tempPath, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("exhausted generated temp name attempts for %s", path)
}

func writeAndCloseGeneratedTemp(temp *os.File, content []byte, mode fs.FileMode) error {
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	return temp.Close()
}

func rollbackGeneratedArtifacts(root *os.Root, committed []stagedGeneratedArtifact, ops generatedFileOps) error {
	var rollbackErrors []error
	for index := len(committed) - 1; index >= 0; index-- {
		if err := restoreGeneratedArtifact(root, committed[index], ops); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

// restoreGeneratedArtifact 把已经提交的生成物恢复到事务开始前状态。
func restoreGeneratedArtifact(root *os.Root, artifact stagedGeneratedArtifact, ops generatedFileOps) error {
	if !artifact.original.exists {
		if err := root.Remove(artifact.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove generated file during rollback %s: %w", artifact.path, err)
		}
		return nil
	}
	tempPath, err := writeGeneratedTemp(root, artifact.path, artifact.original.content, artifact.original.mode)
	if err != nil {
		return fmt.Errorf("stage generated rollback %s: %w", artifact.path, err)
	}
	defer root.Remove(tempPath)
	if err := ops.rename(root, tempPath, artifact.path); err != nil {
		return fmt.Errorf("restore generated file %s: %w", artifact.path, err)
	}
	return nil
}

func cleanupGeneratedTemps(root *os.Root, artifacts []stagedGeneratedArtifact) {
	for _, artifact := range artifacts {
		if artifact.tempPath != "" {
			_ = root.Remove(artifact.tempPath)
		}
	}
}

// repositoryRoot 沿当前目录向上查找唯一仓库 go.mod。
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from working directory")
		}
		dir = parent
	}
}
