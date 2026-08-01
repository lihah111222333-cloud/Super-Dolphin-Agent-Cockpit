package remoteci

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
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
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionStandardImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return first != "" && !strings.Contains(first, ".")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoEmbedAssets(directory string, source []byte) error {
	for line := range strings.SplitSeq(string(source), "\n") {
		if err := assets.addWorkerEmbedLine(directory, line); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addWorkerEmbedLine(directory, line string) error {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "//go:embed ") {
		return nil
	}
	for raw := range strings.FieldsSeq(strings.TrimSpace(strings.TrimPrefix(line, "//go:embed "))) {
		if err := assets.addWorkerEmbedPattern(directory, raw); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addWorkerEmbedPattern(directory, raw string) error {
	pattern := raw
	if unquoted, err := strconv.Unquote(raw); err == nil {
		pattern = unquoted
	}
	pattern = strings.TrimPrefix(pattern, "all:")
	clean := path.Clean(pattern)
	if pattern == "" || path.IsAbs(pattern) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("worker execution go:embed pattern %q is invalid", raw)
	}
	matched, err := assets.addWorkerEmbedMatches(path.Join(directory, pattern))
	if err != nil {
		return fmt.Errorf("worker execution go:embed pattern %q: %w", raw, err)
	}
	if !matched {
		return fmt.Errorf("worker execution go:embed pattern %q matched no tracked asset", raw)
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addWorkerEmbedMatches(pattern string) (bool, error) {
	matched := false
	for _, entry := range assets.snapshot.entries {
		if entry.kind != "blob" {
			continue
		}
		match, err := workerExecutionEmbedMatches(pattern, entry.path)
		if err != nil {
			return false, err
		}
		if match {
			assets.entries[entry.path] = entry
			matched = true
		}
	}
	return matched, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionEmbedMatches(pattern string, filePath string) (bool, error) {
	for candidate := filePath; candidate != "." && candidate != "/"; candidate = path.Dir(candidate) {
		matched, err := path.Match(pattern, candidate)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
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
