package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const (
	ruleMapPath      = "docs/doc/codemap/13-archtest-boundaries.md"
	readmePath       = "README.md"
	statsBeginMarker = "<!-- BEGIN GENERATED ARCHTEST STATS -->"
	statsEndMarker   = "<!-- END GENERATED ARCHTEST STATS -->"
)

type archtestStats struct {
	Tests int
	Files int
}

type generatedArtifact struct {
	path    string
	content string
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
	rename func(string, string) error
}

func main() {
	check := flag.Bool("check", false, "fail when generated archtest documentation is stale")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "archtestmap does not accept positional arguments")
		os.Exit(2)
	}
	root, err := repositoryRoot()
	if err == nil {
		err = run(root, *check)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 从统一 registry 和源码 AST 生成或只读校验规则地图及 README 统计。
func run(root string, check bool) error {
	registry := archtest.DefaultBackendBoundaryRegistry()
	if violations := archtest.ValidateBackendBoundaryGovernance(root, registry); len(violations) > 0 {
		return fmt.Errorf("invalid backend boundary governance:\n%s", strings.Join(violations, "\n"))
	}
	stats, err := collectArchtestStats(root)
	if err != nil {
		return err
	}
	readme, err := os.ReadFile(filepath.Join(root, readmePath))
	if err != nil {
		return fmt.Errorf("read %s: %w", readmePath, err)
	}
	updatedREADME, err := replaceREADMEStats(string(readme), stats)
	if err != nil {
		return fmt.Errorf("update %s: %w", readmePath, err)
	}
	return syncGeneratedFiles([]generatedArtifact{
		{path: filepath.Join(root, ruleMapPath), content: renderRuleMap(registry)},
		{path: filepath.Join(root, readmePath), content: updatedREADME},
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
	out.WriteString("## Specialized guards\n\n| Guard | Test file | Runnable tests | Reason |\n|---|---|---|---|\n")
	for _, guard := range items {
		fmt.Fprintf(out, "| `%s` | `%s` | %s | %s |\n", guard.ID, guard.File, renderCodeList(guard.TestNames), escapeMarkdown(guard.Reason))
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
	names, err := archtest.DiscoverRunnableGoTests(path)
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
	if strings.Count(input, statsBeginMarker) != 1 || strings.Count(input, statsEndMarker) != 1 {
		return 0, 0, fmt.Errorf("README archtest stats markers must each appear exactly once")
	}
	markerStart := strings.Index(input, statsBeginMarker)
	begin := markerStart + len(statsBeginMarker)
	end := strings.Index(input, statsEndMarker)
	if begin > end {
		return 0, 0, fmt.Errorf("README archtest stats markers are reversed")
	}
	rowStart := strings.LastIndex(input[:markerStart], "\n") + 1
	rowEnd := len(input)
	if offset := strings.Index(input[markerStart:], "\n"); offset >= 0 {
		rowEnd = markerStart + offset
	}
	row := input[rowStart:rowEnd]
	if end+len(statsEndMarker) > rowEnd || !strings.HasPrefix(row, "| Architecture Tests | ") {
		return 0, 0, fmt.Errorf("README archtest stats markers must be inline in the Architecture Tests row")
	}
	if countREADMEArchitectureTestRows(input) != 1 {
		return 0, 0, fmt.Errorf("README must contain exactly one Architecture Tests row")
	}
	return begin, end, nil
}

func countREADMEArchitectureTestRows(input string) int {
	count := 0
	for line := range strings.SplitSeq(input, "\n") {
		if strings.HasPrefix(line, "| Architecture Tests | ") {
			count++
		}
	}
	return count
}

// syncGeneratedFile 保留单文件调用兼容性，并使用同一原子刷新路径。
func syncGeneratedFile(path, content string, check bool) error {
	return syncGeneratedFiles([]generatedArtifact{{path: path, content: content}}, check)
}

// syncGeneratedFiles 以暂存、原子替换和失败回滚同步同一批生成物。
func syncGeneratedFiles(artifacts []generatedArtifact, check bool) error {
	return syncGeneratedFilesWithOps(artifacts, check, generatedFileOps{rename: os.Rename})
}

// syncGeneratedFilesWithOps 注入 rename 仅用于验证第二次提交失败时的回滚路径。
func syncGeneratedFilesWithOps(artifacts []generatedArtifact, check bool, ops generatedFileOps) error {
	changed, err := changedGeneratedArtifacts(artifacts)
	if err != nil || len(changed) == 0 {
		return err
	}
	if check {
		return fmt.Errorf("generated file is stale: %s", changed[0].path)
	}
	if ops.rename == nil {
		return fmt.Errorf("generated file rename operation is nil")
	}
	staged, err := stageGeneratedArtifacts(changed)
	if err != nil {
		return err
	}
	defer cleanupGeneratedTemps(staged)
	for index := range staged {
		if err := ops.rename(staged[index].tempPath, staged[index].path); err != nil {
			rollbackErr := rollbackGeneratedArtifacts(staged[:index], ops)
			return errors.Join(fmt.Errorf("commit generated file %s: %w", staged[index].path, err), rollbackErr)
		}
	}
	return nil
}

// changedGeneratedArtifacts 预读整批目标，保证任何暂存或提交前已取得完整旧状态。
func changedGeneratedArtifacts(artifacts []generatedArtifact) ([]stagedGeneratedArtifact, error) {
	seen := make(map[string]bool, len(artifacts))
	changed := make([]stagedGeneratedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if seen[artifact.path] {
			return nil, fmt.Errorf("duplicate generated artifact path: %s", artifact.path)
		}
		seen[artifact.path] = true
		state, err := readGeneratedFileState(artifact.path)
		if err != nil {
			return nil, err
		}
		if state.exists && string(state.content) == artifact.content {
			continue
		}
		changed = append(changed, stagedGeneratedArtifact{generatedArtifact: artifact, original: state})
	}
	return changed, nil
}

// readGeneratedFileState 读取普通生成文件的内容、权限和存在状态。
func readGeneratedFileState(path string) (generatedFileState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return generatedFileState{mode: 0o644}, nil
	}
	if err != nil {
		return generatedFileState{}, fmt.Errorf("inspect generated file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return generatedFileState{}, fmt.Errorf("generated target is not a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return generatedFileState{}, fmt.Errorf("read generated file %s: %w", path, err)
	}
	return generatedFileState{content: content, mode: info.Mode().Perm(), exists: true}, nil
}

func stageGeneratedArtifacts(artifacts []stagedGeneratedArtifact) ([]stagedGeneratedArtifact, error) {
	staged := append([]stagedGeneratedArtifact(nil), artifacts...)
	for index := range staged {
		tempPath, err := writeGeneratedTemp(staged[index].path, []byte(staged[index].content), staged[index].original.mode)
		if err != nil {
			cleanupGeneratedTemps(staged)
			return nil, err
		}
		staged[index].tempPath = tempPath
	}
	return staged, nil
}

// writeGeneratedTemp 在目标目录完整写入并同步临时文件，目标文件此时仍保持不变。
func writeGeneratedTemp(path string, content []byte, mode fs.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create generated directory for %s: %w", path, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create generated temp for %s: %w", path, err)
	}
	tempPath := temp.Name()
	if err := writeAndCloseGeneratedTemp(temp, content, mode); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("stage generated file %s: %w", path, err)
	}
	return tempPath, nil
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

func rollbackGeneratedArtifacts(committed []stagedGeneratedArtifact, ops generatedFileOps) error {
	var rollbackErrors []error
	for index := len(committed) - 1; index >= 0; index-- {
		if err := restoreGeneratedArtifact(committed[index], ops); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

// restoreGeneratedArtifact 把已经提交的生成物恢复到事务开始前状态。
func restoreGeneratedArtifact(artifact stagedGeneratedArtifact, ops generatedFileOps) error {
	if !artifact.original.exists {
		if err := os.Remove(artifact.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove generated file during rollback %s: %w", artifact.path, err)
		}
		return nil
	}
	tempPath, err := writeGeneratedTemp(artifact.path, artifact.original.content, artifact.original.mode)
	if err != nil {
		return fmt.Errorf("stage generated rollback %s: %w", artifact.path, err)
	}
	defer os.Remove(tempPath)
	if err := ops.rename(tempPath, artifact.path); err != nil {
		return fmt.Errorf("restore generated file %s: %w", artifact.path, err)
	}
	return nil
}

func cleanupGeneratedTemps(artifacts []stagedGeneratedArtifact) {
	for _, artifact := range artifacts {
		if artifact.tempPath != "" {
			_ = os.Remove(artifact.tempPath)
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
