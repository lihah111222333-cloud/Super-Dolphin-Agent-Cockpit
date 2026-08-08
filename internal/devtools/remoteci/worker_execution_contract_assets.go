package remoteci

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionStaticCommands(node ast.Node) [][]string {
	commands := make([][]string, 0)
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		command, ok := workerExecutionStringSlice(literal)
		if ok && workerExecutionLooksLikeCommand(command) {
			commands = append(commands, command)
		}
		return true
	})
	return commands
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionStringSlice(literal *ast.CompositeLit) ([]string, bool) {
	array, ok := literal.Type.(*ast.ArrayType)
	if !ok {
		return nil, false
	}
	identifier, ok := array.Elt.(*ast.Ident)
	if !ok || identifier.Name != "string" || len(literal.Elts) == 0 {
		return nil, false
	}
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		basic, ok := element.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			break
		}
		value, err := strconv.Unquote(basic.Value)
		if err != nil {
			break
		}
		values = append(values, value)
	}
	return values, len(values) > 0
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionLooksLikeCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	first := strings.TrimSpace(command[0])
	if workerExecutionRepositoryPath(first) {
		return workerExecutionRepositoryCommand(first)
	}
	if strings.Contains(first, "/") && validRemoteGitTreePath(first) {
		return workerExecutionRepositoryCommand(first)
	}
	switch path.Base(first) {
	case "bash", "go", "make", "node", "sh", "super-dolphin-gate":
		return true
	default:
		return first == "$(MAKE)"
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionRepositoryCommand(value string) bool {
	candidate := strings.TrimPrefix(value, "./")
	extension := path.Ext(candidate)
	if workerExecutionCommandExtension(extension) {
		return true
	}
	return strings.HasPrefix(candidate, "scripts/") && extension == "" && !strings.HasSuffix(candidate, "/...")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionCommandExtension(extension string) bool {
	return slices.Contains([]string{".bash", ".go", ".js", ".mjs", ".sh"}, extension)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addCommand(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return nil
	}
	executable := strings.TrimSpace(command[0])
	if workerExecutionRepositoryPath(executable) {
		return assets.addRepositoryCommandPath(ctx, executable, false)
	}
	switch path.Base(executable) {
	case "go":
		return assets.addGoRunCommand(ctx, command)
	case "bash", "sh", "node":
		return assets.addScriptCommand(ctx, command)
	default:
		return nil
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoRunCommand(ctx context.Context, command []string) error {
	if len(command) < 3 || command[1] != "run" {
		return nil
	}
	foundTarget := false
	for _, argument := range command[2:] {
		if workerExecutionRepositoryPath(argument) {
			if err := assets.addGoCommandTarget(ctx, argument, false); err != nil {
				return err
			}
			foundTarget = true
			if path.Ext(strings.Trim(argument, "\"'` \t\r\n")) != ".go" {
				break
			}
			continue
		}
		if foundTarget {
			break
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addScriptCommand(ctx context.Context, command []string) error {
	for _, argument := range command[1:] {
		if workerExecutionRepositoryPath(argument) {
			return assets.addRepositoryCommandPath(ctx, argument, false)
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionRepositoryPath(value string) bool {
	return strings.HasPrefix(value, "./") || strings.HasPrefix(value, "scripts/") ||
		strings.HasPrefix(value, "$ROOT_DIR/") || strings.HasPrefix(value, "${ROOT_DIR}/") ||
		strings.HasPrefix(value, "$SCRIPT_DIR/") || strings.HasPrefix(value, "${SCRIPT_DIR}/")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionNormalizeRepositoryPath(value string) (string, error) {
	value = strings.Trim(value, "\"'` \t\r\n")
	for _, prefix := range []string{"$ROOT_DIR/", "${ROOT_DIR}/"} {
		value = strings.TrimPrefix(value, prefix)
	}
	for _, prefix := range []string{"$SCRIPT_DIR/", "${SCRIPT_DIR}/"} {
		if suffix, ok := strings.CutPrefix(value, prefix); ok {
			value = "scripts/" + suffix
		}
	}
	value = strings.TrimPrefix(value, "./")
	if strings.Contains(value, "$") {
		return "", fmt.Errorf("worker execution command path %q is dynamic", value)
	}
	value = strings.TrimSuffix(value, "/...")
	if !validRemoteGitTreePath(value) {
		return "", fmt.Errorf("worker execution command path %q is invalid", value)
	}
	return value, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addRepositoryCommandPath(
	ctx context.Context,
	value string,
	includeTests bool,
) error {
	filePath, err := workerExecutionNormalizeRepositoryPath(value)
	if err != nil {
		return err
	}
	if entry, ok := assets.snapshot.byPath[filePath]; ok {
		if entry.kind != "blob" {
			return fmt.Errorf("worker execution command path %q is not a Git blob", filePath)
		}
		assets.entries[filePath] = entry
		if workerExecutionIsShellScript(filePath) {
			assets.scriptQueue = append(assets.scriptQueue, filePath)
		}
		return nil
	}
	if assets.snapshot.hasDirectory(filePath) {
		return assets.addGoCommandTarget(ctx, filePath, includeTests)
	}
	return fmt.Errorf("worker execution command path %q is not tracked", filePath)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (snapshot *remoteGitTreeSnapshot) hasDirectory(directory string) bool {
	for _, entry := range snapshot.entries {
		if strings.HasPrefix(entry.path, directory+"/") {
			return true
		}
	}
	return false
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionIsShellScript(filePath string) bool {
	switch path.Ext(filePath) {
	case ".bash", ".sh":
		return true
	default:
		return false
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoCommandTarget(
	ctx context.Context,
	value string,
	includeTests bool,
) error {
	target, err := workerExecutionNormalizeRepositoryPath(value)
	if err != nil {
		return err
	}
	if path.Ext(target) == ".go" {
		entry, ok := assets.snapshot.byPath[target]
		if !ok || entry.kind != "blob" {
			return fmt.Errorf("worker execution Go command source %q is not tracked", target)
		}
		// An explicit go run file is a named source input; //go:build ignore keeps it
		// out of package builds but does not remove it from this command contract.
		assets.entries[target] = entry
		if err := assets.addGoEmbedAssets(path.Dir(target), assets.snapshot.goSources[target]); err != nil {
			return err
		}
		return nil
	}
	pattern := strings.HasSuffix(strings.TrimPrefix(value, "./"), "/...")
	directories := assets.goCommandDirectories(target, pattern)
	if len(directories) == 0 {
		return fmt.Errorf("worker execution Go command target %q has no linux/amd64 package", target)
	}
	for _, directory := range directories {
		if err := assets.addGoCommandPackage(ctx, directory, includeTests); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) goCommandDirectories(target string, recursive bool) []string {
	directories := make(map[string]struct{})
	for filePath, source := range assets.snapshot.goSources {
		if path.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") ||
			!remoteGoSourceAppliesLinuxAMD64(filePath, source) {
			continue
		}
		directory := path.Dir(filePath)
		if directory == target || (recursive && strings.HasPrefix(directory, target+"/")) {
			directories[directory] = struct{}{}
		}
	}
	return sortedRemoteStringSet(directories)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoCommandPackage(
	ctx context.Context,
	directory string,
	includeTests bool,
) error {
	foundProduction, foundTest, err := assets.addGoCommandSources(directory, includeTests)
	if err != nil {
		return err
	}
	if err := workerExecutionValidateGoCommandSources(directory, includeTests, foundProduction, foundTest); err != nil {
		return err
	}
	return assets.addPackageBuildInputs(ctx, map[string]struct{}{directory: {}})
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoCommandSources(directory string, includeTests bool) (bool, bool, error) {
	foundProduction, foundTest := false, false
	for filePath, source := range assets.snapshot.goSources {
		if !workerExecutionGoCommandSource(filePath, source, directory) {
			continue
		}
		isTest := strings.HasSuffix(filePath, "_test.go")
		if isTest && !includeTests {
			continue
		}
		if err := assets.addGoCommandSource(directory, filePath, source); err != nil {
			return false, false, err
		}
		foundProduction = foundProduction || !isTest
		foundTest = foundTest || isTest
	}
	return foundProduction, foundTest, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionGoCommandSource(filePath string, source []byte, directory string) bool {
	return path.Dir(filePath) == directory && path.Ext(filePath) == ".go" && remoteGoSourceAppliesLinuxAMD64(filePath, source)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addGoCommandSource(directory, filePath string, source []byte) error {
	entry, ok := assets.snapshot.byPath[filePath]
	if !ok || entry.kind != "blob" {
		return fmt.Errorf("worker execution Go command source %q is not a Git blob", filePath)
	}
	assets.entries[filePath] = entry
	return assets.addGoEmbedAssets(directory, source)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionValidateGoCommandSources(directory string, includeTests, foundProduction, foundTest bool) error {
	if !foundProduction {
		return fmt.Errorf("worker execution Go command target %q has no linux/amd64 production source", directory)
	}
	if includeTests && !foundTest {
		return fmt.Errorf("worker execution Go test target %q has no linux/amd64 tests", directory)
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addPackageBuildInputs(
	ctx context.Context,
	directories map[string]struct{},
) error {
	cgoDirectories, err := assets.snapshot.workerExecutionCgoDirectories(directories)
	if err != nil {
		return err
	}
	entries := assets.workerExecutionBuildInputEntries(directories, cgoDirectories)
	if len(entries) == 0 {
		return nil
	}
	paths := workerExecutionEntryPaths(entries)
	sources, err := assets.snapshot.readGitBlobs(ctx, paths)
	if err != nil {
		return err
	}
	assets.addApplicableBuildInputs(entries, sources)
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) workerExecutionBuildInputEntries(directories, cgoDirectories map[string]struct{}) []remoteGitTreeEntry {
	candidates := make(map[string]remoteGitTreeEntry)
	for _, entry := range assets.snapshot.entries {
		if workerExecutionBuildInputCandidate(entry, directories, cgoDirectories) {
			candidates[entry.path] = entry
		}
	}
	return sortedRemoteGitTreeEntries(candidates)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionBuildInputCandidate(entry remoteGitTreeEntry, directories, cgoDirectories map[string]struct{}) bool {
	if entry.kind != "blob" {
		return false
	}
	if _, ok := directories[path.Dir(entry.path)]; ok && workerExecutionPackageAuxiliaryInput(entry.path) {
		return true
	}
	for directory := range cgoDirectories {
		if workerExecutionPathWithinDirectory(entry.path, directory) && workerExecutionCgoLinkedInput(entry.path) {
			return true
		}
	}
	return false
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionEntryPaths(entries []remoteGitTreeEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.path)
	}
	return paths
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) addApplicableBuildInputs(entries []remoteGitTreeEntry, sources map[string][]byte) {
	for _, entry := range entries {
		if remoteBuildSourceAppliesLinuxAMD64(entry.path, sources[entry.path]) {
			assets.entries[entry.path] = entry
		}
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (snapshot *remoteGitTreeSnapshot) workerExecutionCgoDirectories(
	directories map[string]struct{},
) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for filePath, source := range snapshot.goSources {
		directory := path.Dir(filePath)
		if _, ok := directories[directory]; !ok || strings.HasSuffix(filePath, "_test.go") ||
			!remoteGoSourceAppliesLinuxAMD64(filePath, source) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse worker execution package source %q: %w", filePath, err)
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("parse worker execution package import in %q: %w", filePath, err)
			}
			if importPath == "C" {
				result[directory] = struct{}{}
				break
			}
		}
	}
	return result, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionPackageAuxiliaryInput(filePath string) bool {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".c", ".cc", ".cpp", ".cxx", ".f", ".for", ".f90", ".h", ".hh", ".hpp", ".hxx",
		".m", ".mm", ".s", ".swig", ".swigcxx", ".sx", ".syso":
		return true
	default:
		return false
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionCgoLinkedInput(filePath string) bool {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".a", ".h", ".hh", ".hpp", ".hxx", ".inc", ".ld", ".lds", ".o", ".so":
		return true
	default:
		return false
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionPathWithinDirectory(filePath string, directory string) bool {
	if directory == "." {
		return !strings.Contains(filePath, "/") || filePath != ""
	}
	return strings.HasPrefix(filePath, directory+"/")
}

// resolveWorkerExecutionAssets 收集 Worker 进程的构建、模块、资源和脚本输入。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (assets *workerExecutionAssets) resolveWorkerExecutionAssets(ctx context.Context, closure *workerExecutionGoClosure) error {
	if err := assets.addPackageBuildInputs(ctx, closure.reached); err != nil {
		return err
	}
	if err := assets.addExternalModuleInputs(ctx, closure); err != nil {
		return err
	}
	for _, unit := range closure.selected {
		content, err := workerExecutionUnitContent(unit)
		if err != nil {
			return err
		}
		if err := assets.addGoEmbedAssets(unit.directory, content); err != nil {
			return err
		}
	}
	for _, command := range closure.commands {
		if err := assets.addCommand(ctx, command); err != nil {
			return err
		}
	}
	return assets.resolveScripts(ctx)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
