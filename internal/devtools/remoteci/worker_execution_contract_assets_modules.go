package remoteci

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// addLocalGoModuleMetadata 把本地替换模块元数据加入 Worker 执行资产，使执行契约绑定精确 tree 对象。
func (assets *workerExecutionAssets) addLocalGoModuleMetadata() error {
	metadataEntries, err := assets.snapshot.localGoModuleMetadataEntries()
	if err != nil {
		return err
	}
	for _, entry := range metadataEntries {
		assets.entries[entry.path] = entry
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addExternalModuleInputs(
	ctx context.Context,
	closure *workerExecutionGoClosure,
) error {
	parsed, err := modfile.Parse("go.mod", assets.snapshot.goSources["go.mod"], nil)
	if err != nil {
		return fmt.Errorf("parse worker execution go.mod: %w", err)
	}
	selected, err := assets.selectedWorkerModules(parsed.Require, closure)
	if err != nil || len(selected) == 0 {
		return err
	}
	sources, err := assets.snapshot.readGitBlobs(ctx, []string{"go.sum"})
	if err != nil {
		return err
	}
	modulePaths := make([]string, 0, len(selected))
	for modulePath := range selected {
		modulePaths = append(modulePaths, modulePath)
	}
	sort.Strings(modulePaths)
	for _, modulePath := range modulePaths {
		if err := assets.addWorkerModuleFragment(modulePath, selected[modulePath], parsed.Replace, sources["go.sum"]); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) selectedWorkerModules(requirements []*modfile.Require, closure *workerExecutionGoClosure) (map[string]*modfile.Require, error) {
	selected := make(map[string]*modfile.Require)
	for _, imports := range closure.usedImports {
		for importPath := range imports {
			if _, local := assets.snapshot.resolveLocalGoImport(importPath); local {
				continue
			}
			requirement := workerExecutionModuleRequirement(requirements, importPath)
			if requirement != nil {
				selected[requirement.Mod.Path] = requirement
				continue
			}
			if !workerExecutionStandardImport(importPath) {
				return nil, fmt.Errorf("worker execution external import %q has no selected module requirement", importPath)
			}
		}
	}
	if len(selected) > 0 {
		entry, ok := assets.snapshot.byPath["go.sum"]
		if !ok || entry.kind != "blob" {
			return nil, errors.New("worker execution external modules require a tracked go.sum")
		}
	}
	return selected, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addWorkerModuleFragment(modulePath string, requirement *modfile.Require, replacements []*modfile.Replace, goSum []byte) error {
	content, err := workerExecutionModuleContent(requirement, workerExecutionModuleReplacement(replacements, requirement), goSum)
	if err != nil {
		return err
	}
	assets.fragments["module:"+modulePath] = workerExecutionFragment{kind: "module", path: "go.mod", name: modulePath, content: content}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionModuleContent(requirement *modfile.Require, replacement *modfile.Replace, goSum []byte) ([]byte, error) {
	checksumPath, checksumVersion := requirement.Mod.Path, requirement.Mod.Version
	var content strings.Builder
	fmt.Fprintf(&content, "require %s %s\n", requirement.Mod.Path, requirement.Mod.Version)
	if replacement != nil {
		if replacement.New.Version == "" {
			return nil, fmt.Errorf("worker execution module %q uses an unresolved local replacement %q", requirement.Mod.Path, replacement.New.Path)
		}
		fmt.Fprintf(&content, "replace %s %s => %s %s\n", replacement.Old.Path, replacement.Old.Version, replacement.New.Path, replacement.New.Version)
		checksumPath, checksumVersion = replacement.New.Path, replacement.New.Version
	}
	sums := workerExecutionModuleSums(goSum, checksumPath, checksumVersion)
	if len(sums) == 0 {
		return nil, fmt.Errorf("worker execution module %s@%s has no selected go.sum checksum", checksumPath, checksumVersion)
	}
	for _, sum := range sums {
		fmt.Fprintf(&content, "sum %s\n", sum)
	}
	return []byte(content.String()), nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionModuleRequirement(
	requirements []*modfile.Require,
	importPath string,
) *modfile.Require {
	var selected *modfile.Require
	for _, requirement := range requirements {
		modulePath := requirement.Mod.Path
		if importPath != modulePath && !strings.HasPrefix(importPath, modulePath+"/") {
			continue
		}
		if selected == nil || len(modulePath) > len(selected.Mod.Path) {
			selected = requirement
		}
	}
	return selected
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionModuleReplacement(
	replacements []*modfile.Replace,
	requirement *modfile.Require,
) *modfile.Replace {
	var wildcard *modfile.Replace
	for _, replacement := range replacements {
		if replacement.Old.Path != requirement.Mod.Path {
			continue
		}
		if replacement.Old.Version == requirement.Mod.Version {
			return replacement
		}
		if replacement.Old.Version == "" {
			wildcard = replacement
		}
	}
	return wildcard
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionModuleSums(source []byte, modulePath string, version string) []string {
	selected := make(map[string]struct{})
	for line := range strings.SplitSeq(string(source), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != modulePath ||
			(fields[1] != version && fields[1] != version+"/go.mod") {
			continue
		}
		selected[strings.Join(fields, " ")] = struct{}{}
	}
	return sortedRemoteStringSet(selected)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionStandardImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return first != "" && !strings.Contains(first, ".")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoEmbedAssets(directory string, source []byte) error {
	entries, err := assets.snapshot.resolveGoEmbedAssets(directory, source)
	if err != nil {
		return fmt.Errorf("worker execution %w", err)
	}
	maps.Copy(assets.entries, entries)
	return nil
}

const (
	// remoteGoEmbedRuntimeSeedDirectory 是唯一由 accepted runtime seed 提供生成嵌入树的包目录。
	remoteGoEmbedRuntimeSeedDirectory = "cmd/agent-terminal"
	remoteGoEmbedRuntimeSeedPattern   = "all:web-dist"
)

// resolveGoEmbedAssets 解析源码中的全部 go:embed pattern，并返回实际匹配的 tracked blob。
// 该解析器同时服务 Worker execution contract 与 workload fingerprint，避免两套匹配语义漂移。
func (snapshot *remoteGitTreeSnapshot) resolveGoEmbedAssets(directory string, source []byte) (map[string]remoteGitTreeEntry, error) {
	if snapshot == nil {
		return nil, errors.New("go:embed resolution snapshot is required")
	}
	key := remoteGoEmbedResolutionKey{directory: directory, sourceIdentity: sha256.Sum256(source)}
	snapshot.cacheMu.Lock()
	defer snapshot.cacheMu.Unlock()
	if snapshot.goEmbedResolutionCache == nil {
		snapshot.goEmbedResolutionCache = make(map[remoteGoEmbedResolutionKey]remoteGoEmbedResolutionCache)
	}
	if cached, ok := snapshot.goEmbedResolutionCache[key]; ok {
		snapshot.goEmbedResolutionCacheHits++
		return remoteGoEmbedResolutionResult(cached)
	}
	snapshot.goEmbedResolutionComputations++
	entries, err := snapshot.computeGoEmbedAssets(directory, source)
	cached := remoteGoEmbedResolutionCache{entries: entries, err: err}
	snapshot.goEmbedResolutionCache[key] = cached
	return remoteGoEmbedResolutionResult(cached)
}

// computeGoEmbedAssets 执行一次 go:embed AST 解析与 tracked asset 匹配。
func (snapshot *remoteGitTreeSnapshot) computeGoEmbedAssets(directory string, source []byte) ([]remoteGitTreeEntry, error) {
	patterns, err := remoteGoEmbedPatterns(source)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]remoteGitTreeEntry)
	for _, raw := range patterns {
		entries, err := snapshot.resolveGoEmbedPattern(directory, raw)
		if err != nil {
			return nil, err
		}
		maps.Copy(selected, entries)
	}
	return sortedRemoteGitTreeEntries(selected), nil
}

// remoteGoEmbedEntriesMap 从 immutable entries 克隆调用方可修改的返回 map。
func remoteGoEmbedEntriesMap(entries []remoteGitTreeEntry) map[string]remoteGitTreeEntry {
	if len(entries) == 0 {
		return make(map[string]remoteGitTreeEntry)
	}
	selected := make(map[string]remoteGitTreeEntry, len(entries))
	for _, entry := range entries {
		selected[entry.path] = entry
	}
	return selected
}

// remoteGoEmbedResolutionResult 保持缓存错误 fail-fast，并为成功结果返回 map clone。
func remoteGoEmbedResolutionResult(cached remoteGoEmbedResolutionCache) (map[string]remoteGitTreeEntry, error) {
	if cached.err != nil {
		return nil, cached.err
	}
	return remoteGoEmbedEntriesMap(cached.entries), nil
}

// resolveGoEmbedPattern 解析一个相对包目录的 go:embed pattern。
func (snapshot *remoteGitTreeSnapshot) resolveGoEmbedPattern(directory, raw string) (map[string]remoteGitTreeEntry, error) {
	if remoteGoEmbedRuntimeSeedPatternAllowed(directory, raw) {
		// ECI runtime seed 在 Go 编译前安装该生成目录；它不是源码指纹输入，
		// seed identity 由 RunInput 的 runtime-seed contract 绑定。
		return make(map[string]remoteGitTreeEntry), nil
	}
	pattern, err := remoteGoEmbedPattern(raw)
	if err != nil {
		return nil, err
	}
	return snapshot.resolveGoEmbedPatternPath(path.Join(directory, pattern))
}

// remoteGoEmbedRuntimeSeedPatternAllowed 只识别生成 web-dist 的精确 contract；
// 其他缺失 embed 仍由下方 resolver fail-fast。
func remoteGoEmbedRuntimeSeedPatternAllowed(directory, raw string) bool {
	if directory != remoteGoEmbedRuntimeSeedDirectory {
		return false
	}
	pattern := raw
	if unquoted, err := strconv.Unquote(raw); err == nil {
		pattern = unquoted
	}
	return pattern == remoteGoEmbedRuntimeSeedPattern
}

// resolveGoEmbedPatternPath 返回一个已规范化 pattern 匹配到的 tracked blob 集合。
func (snapshot *remoteGitTreeSnapshot) resolveGoEmbedPatternPath(pattern string) (map[string]remoteGitTreeEntry, error) {
	selected := make(map[string]remoteGitTreeEntry)
	for _, entry := range snapshot.entries {
		if entry.kind != "blob" {
			continue
		}
		matched, err := remoteGoEmbedPatternMatches(pattern, entry.path)
		if err != nil {
			return nil, fmt.Errorf("go:embed pattern %q: %w", pattern, err)
		}
		if matched {
			selected[entry.path] = entry
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("go:embed pattern %q matched no tracked asset", pattern)
	}
	return selected, nil
}

// remoteGoEmbedPatterns 使用 Go parser 的 comment nodes 提取 go:embed raw pattern tokens。
func remoteGoEmbedPatterns(source []byte) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "embed.go", source, parser.ParseComments)
	if err != nil {
		// Worker execution unit 可能是声明片段；只有 synthetic package 仍能通过
		// AST parser 且确实包含声明时才接受，禁止退回文本扫描。
		fragmentSource := append([]byte("package remoteci\n"), source...)
		fragment, fragmentErr := parser.ParseFile(token.NewFileSet(), "embed.go", fragmentSource, parser.ParseComments)
		if fragmentErr != nil || len(fragment.Decls) == 0 {
			return nil, fmt.Errorf("parse Go source for go:embed directives: %w", err)
		}
		parsed = fragment
	}
	patterns := make([]string, 0)
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			linePatterns, directive, err := remoteGoEmbedLinePatterns(comment.Text)
			if err != nil {
				return nil, err
			}
			if directive {
				patterns = append(patterns, linePatterns...)
			}
		}
	}
	return patterns, nil
}

// remoteGoEmbedLinePatterns 提取单行 directive 的 raw tokens，并拒绝空 directive。
func remoteGoEmbedLinePatterns(line string) ([]string, bool, error) {
	line = strings.TrimSpace(line)
	if line == "//go:embed" {
		return nil, true, errors.New("go:embed directive has no patterns")
	}
	if !strings.HasPrefix(line, "//go:embed") {
		return nil, false, nil
	}
	rest := strings.TrimPrefix(line, "//go:embed")
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return nil, false, nil
	}
	patterns, err := remoteGoEmbedPatternTokens(strings.TrimSpace(rest))
	return patterns, true, err
}

// remoteGoEmbedPatternTokens 分词时保留 Go quoted pattern 中的空格。
func remoteGoEmbedPatternTokens(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("go:embed directive has no patterns")
	}
	patterns := make([]string, 0)
	for input != "" {
		input = strings.TrimLeft(input, " \t\r\n")
		if input == "" {
			break
		}
		if input[0] != '"' && input[0] != '`' {
			end := strings.IndexAny(input, " \t\r\n")
			if end < 0 {
				end = len(input)
			}
			patterns = append(patterns, input[:end])
			input = input[end:]
			continue
		}
		quote := input[0]
		end := remoteGoEmbedQuotedEnd(input, quote)
		if end < 0 {
			return nil, fmt.Errorf("go:embed pattern %q is not a valid quoted string", input)
		}
		raw := input[:end+1]
		if _, err := strconv.Unquote(raw); err != nil {
			return nil, fmt.Errorf("go:embed pattern %q is not a valid quoted string: %w", raw, err)
		}
		patterns = append(patterns, raw)
		input = input[end+1:]
	}
	return patterns, nil
}

// remoteGoEmbedQuotedEnd 定位 quoted embed pattern 的结束引号，并正确跳过转义字符。
func remoteGoEmbedQuotedEnd(input string, quote byte) int {
	if quote == '`' {
		index := strings.IndexByte(input[1:], '`')
		if index < 0 {
			return -1
		}
		return index + 1
	}
	escaped := false
	for index := 1; index < len(input); index++ {
		switch {
		case escaped:
			escaped = false
		case input[index] == '\\':
			escaped = true
		case input[index] == quote:
			return index
		}
	}
	return -1
}

// remoteGoEmbedPattern 规范化 raw pattern，并拒绝越出包目录的路径。
func remoteGoEmbedPattern(raw string) (string, error) {
	pattern := raw
	if unquoted, err := strconv.Unquote(raw); err == nil {
		pattern = unquoted
	} else if strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "`") {
		return "", fmt.Errorf("go:embed pattern %q is invalid: %w", raw, err)
	}
	pattern = strings.TrimPrefix(pattern, "all:")
	clean := path.Clean(pattern)
	if pattern == "" || path.IsAbs(pattern) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("go:embed pattern %q is invalid", raw)
	}
	return pattern, nil
}

// remoteGoEmbedPatternMatches 是 go:embed 唯一的 path.Match 祖先匹配实现。
func remoteGoEmbedPatternMatches(pattern string, filePath string) (bool, error) {
	for candidate := filePath; candidate != "." && candidate != "/"; candidate = path.Dir(candidate) {
		matched, err := path.Match(pattern, candidate)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) resolveScripts(ctx context.Context) error {
	for len(assets.scriptQueue) > 0 {
		filePath := assets.scriptQueue[0]
		assets.scriptQueue = assets.scriptQueue[1:]
		if _, ok := assets.scannedScripts[filePath]; ok {
			continue
		}
		assets.scannedScripts[filePath] = struct{}{}
		sources, err := assets.snapshot.readGitBlobs(ctx, []string{filePath})
		if err != nil {
			return err
		}
		for _, command := range workerExecutionShellCommands(sources[filePath]) {
			if len(command) > 1 && path.Base(command[0]) == "go" && command[1] == "test" {
				continue
			}
			if err := assets.addCommand(ctx, command); err != nil {
				return fmt.Errorf("resolve worker execution script %q: %w", filePath, err)
			}
		}
	}
	return nil
}
