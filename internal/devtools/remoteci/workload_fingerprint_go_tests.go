package remoteci

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

type remoteGoTestSource struct {
	path string
	text []byte
}

type remoteGoTestFile struct {
	path   string
	source []byte
	file   *ast.File
}

type remoteGoTestDeclaration struct {
	filePath    string
	source      []byte
	file        *ast.File
	declaration ast.Decl
}

func (snapshot *remoteGitTreeSnapshot) remoteGoTestDeclarations(directory string) ([]remoteGoTestFile, map[string][]remoteGoTestDeclaration, bool) {
	snapshot.cacheMu.Lock()
	if snapshot.goTestDeclarationCache == nil {
		snapshot.goTestDeclarationCache = make(map[string]remoteGoTestDeclarationCache)
	}
	cached, ok := snapshot.goTestDeclarationCache[directory]
	snapshot.cacheMu.Unlock()
	if ok {
		return cached.files, cached.declarations, cached.fallback
	}
	files, declarations, fallback := snapshot.parseRemoteGoTestDeclarations(directory)
	cached = remoteGoTestDeclarationCache{
		files: files, declarations: declarations, fallback: fallback,
	}
	snapshot.cacheMu.Lock()
	if existing, exists := snapshot.goTestDeclarationCache[directory]; exists {
		cached = existing
	} else {
		snapshot.goTestDeclarationCache[directory] = cached
	}
	snapshot.cacheMu.Unlock()
	return cached.files, cached.declarations, cached.fallback
}

// parseRemoteGoTestDeclarations 解析 worker 平台适用测试文件的顶层声明索引。
func (snapshot *remoteGitTreeSnapshot) parseRemoteGoTestDeclarations(directory string) ([]remoteGoTestFile, map[string][]remoteGoTestDeclaration, bool) {
	paths := make([]string, 0)
	for filePath := range snapshot.goSources {
		if path.Dir(filePath) == directory && strings.HasSuffix(filePath, "_test.go") {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	files := make([]remoteGoTestFile, 0, len(paths))
	declarations := make(map[string][]remoteGoTestDeclaration)
	for _, filePath := range paths {
		source := snapshot.goSources[filePath]
		if !remoteGoSourceAppliesLinuxAMD64(filePath, source) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, 0)
		if err != nil {
			return files, declarations, true
		}
		entry := remoteGoTestFile{path: filePath, source: source, file: file}
		files = append(files, entry)
		for _, declaration := range file.Decls {
			for _, name := range remoteGoTestDeclarationNames(declaration) {
				declarations[name] = append(declarations[name], remoteGoTestDeclaration{
					filePath: filePath, source: source, file: file, declaration: declaration,
				})
			}
		}
	}
	return files, declarations, false
}

func remoteGoSourceAppliesLinuxAMD64(filePath string, source []byte) bool {
	return remoteBuildSourceAppliesLinuxAMD64(filePath, source)
}

// remoteBuildSourceAppliesLinuxAMD64 依据文件名与 build 约束判断 worker 是否会编译源码。
func remoteBuildSourceAppliesLinuxAMD64(filePath string, source []byte) bool {
	return remoteBuildFilenameAppliesLinuxAMD64(filePath) && remoteBuildConstraintAppliesLinuxAMD64(source)
}

func remoteBuildFilenameAppliesLinuxAMD64(filePath string) bool {
	base := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if path.Ext(filePath) == ".go" {
		base = strings.TrimSuffix(base, "_test")
	}
	parts := strings.Split(base, "_")
	if len(parts) <= 1 {
		return true
	}
	last := parts[len(parts)-1]
	if remoteGoKnownOS(last) {
		return last == "linux"
	}
	if !remoteGoKnownArch(last) {
		return true
	}
	return remoteBuildArchAppliesLinuxAMD64(parts)
}

func remoteBuildArchAppliesLinuxAMD64(parts []string) bool {
	if parts[len(parts)-1] != "amd64" {
		return false
	}
	return len(parts) <= 2 || !remoteGoKnownOS(parts[len(parts)-2]) || parts[len(parts)-2] == "linux"
}

// remoteBuildConstraintAppliesLinuxAMD64 求值源码中第一个 Go build 约束。
func remoteBuildConstraintAppliesLinuxAMD64(source []byte) bool {
	for line := range strings.SplitSeq(string(source), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:build ") {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return true // The compiler will reject it; retain it rather than false-hit.
		}
		return expr.Eval(func(tag string) bool {
			return tag == "linux" || tag == "amd64" || tag == "unix" || tag == "cgo"
		})
	}
	return true
}

func remoteGoKnownOS(tag string) bool {
	switch tag {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows":
		return true
	default:
		return false
	}
}

func remoteGoKnownArch(tag string) bool {
	switch tag {
	case "386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
		return true
	default:
		return false
	}
}

// remoteGoTestDeclarationNames 返回顶层声明提供的可解析名称。
func remoteGoTestDeclarationNames(declaration ast.Decl) []string {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		if value.Name == nil {
			return nil
		}
		if value.Recv != nil {
			return remoteGoTestReceiverNames(value.Recv)
		}
		return []string{value.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, spec := range value.Specs {
			switch value := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, value.Name.Name)
			case *ast.ValueSpec:
				for _, name := range value.Names {
					names = append(names, name.Name)
				}
			}
		}
		return names
	default:
		return nil
	}
}

// remoteGoTestReceiverNames 返回单一方法接收者的类型名。
func remoteGoTestReceiverNames(receivers *ast.FieldList) []string {
	if receivers == nil || len(receivers.List) != 1 {
		return nil
	}
	switch receiver := receivers.List[0].Type.(type) {
	case *ast.Ident:
		return []string{receiver.Name}
	case *ast.StarExpr:
		if name, ok := receiver.X.(*ast.Ident); ok {
			return []string{name.Name}
		}
	}
	return nil
}

func remoteGoTestDeclarationDependencies(declaration ast.Decl) map[string]struct{} {
	dependencies := make(map[string]struct{})
	ast.Inspect(declaration, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name != "_" {
			dependencies[identifier.Name] = struct{}{}
		}
		return true
	})
	return dependencies
}

func remoteGoTestDeclarationText(declaration remoteGoTestDeclaration) []byte {
	var text bytes.Buffer
	headerEnd := declaration.file.Name.End() - 1
	text.Write(declaration.source[:headerEnd])
	text.WriteByte('\n')
	for _, imported := range declaration.file.Imports {
		start := imported.Pos() - 1
		end := imported.End() - 1
		text.Write(declaration.source[start:end])
		text.WriteByte('\n')
	}
	start := declaration.declaration.Pos() - 1
	end := declaration.declaration.End() - 1
	text.Write(declaration.source[start:end])
	return text.Bytes()
}

// goTestSources returns the selected test declarations and their direct repository observations.
// Any source we cannot close conservatively binds every test source in the package.
// goTestSources 计算单个测试声明的源码闭包和直接仓库观察。
func (snapshot *remoteGitTreeSnapshot) goTestSources(target, directory string, selected map[string]remoteGitTreeEntry) ([]remoteGoTestSource, bool, error) {
	files, declarations, fallback := snapshot.remoteGoTestDeclarations(directory)
	if fallback {
		return snapshot.allGoTestSources(files, directory, selected)
	}
	targetDeclaration, ok := declarations[target]
	if !ok || len(targetDeclaration) != 1 {
		return snapshot.allGoTestSources(files, directory, selected)
	}
	selectedDeclarations, reflectUsed := remoteGoTestSelectedDeclarations(targetDeclaration[0], files, declarations)
	if reflectUsed {
		return snapshot.allGoTestSources(files, directory, selected)
	}
	return snapshot.goTestDeclarationSources(directory, selectedDeclarations, selected)
}

// remoteGoTestSelectedDeclarations 解析入口测试、初始化声明和引用闭包。
func remoteGoTestSelectedDeclarations(target remoteGoTestDeclaration, files []remoteGoTestFile, declarations map[string][]remoteGoTestDeclaration) (map[ast.Decl]remoteGoTestDeclaration, bool) {
	queue := []remoteGoTestDeclaration{target}
	queue = append(queue, declarations["TestMain"]...)
	queue = append(queue, declarations["init"]...)
	queue = append(queue, remoteGoTestPackageVariableDeclarations(files)...)
	selectedDeclarations := make(map[ast.Decl]remoteGoTestDeclaration)
	for len(queue) > 0 {
		declaration := queue[0]
		queue = queue[1:]
		if _, ok := selectedDeclarations[declaration.declaration]; ok {
			continue
		}
		selectedDeclarations[declaration.declaration] = declaration
		dependencies := remoteGoTestDeclarationDependencies(declaration.declaration)
		if remoteGoTestUsesReflect(declaration.file) {
			return nil, true
		}
		for dependency := range dependencies {
			for _, helper := range declarations[dependency] {
				queue = append(queue, helper)
			}
		}
	}
	return selectedDeclarations, false
}

// goTestDeclarationSources 将声明闭包转换为源码摘要并收集其观察输入。
func (snapshot *remoteGitTreeSnapshot) goTestDeclarationSources(directory string, selectedDeclarations map[ast.Decl]remoteGoTestDeclaration, selected map[string]remoteGitTreeEntry) ([]remoteGoTestSource, bool, error) {
	result := make([]remoteGoTestSource, 0, len(selectedDeclarations))
	for _, declaration := range selectedDeclarations {
		for _, importPath := range remoteGoTestImports(declaration.file) {
			localDirectory, local := snapshot.resolveLocalGoImport(importPath)
			if !local {
				continue
			}
			if err := snapshot.addProductionGoPackageEntriesWithAssets(
				localDirectory,
				selected,
				true,
			); err != nil {
				return nil, false, err
			}
		}
		observesWholeTree, err := snapshot.addGoTestFileObservedEntries(
			directory,
			declaration.file,
			declaration.declaration,
			selected,
		)
		if err != nil || observesWholeTree {
			return nil, observesWholeTree, err
		}
		result = append(result, remoteGoTestSource{path: declaration.filePath, text: remoteGoTestDeclarationText(declaration)})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].path == result[right].path {
			return bytes.Compare(result[left].text, result[right].text) < 0
		}
		return result[left].path < result[right].path
	})
	return result, false, nil
}

// remoteGoTestPackageVariableDeclarations 返回测试包初始化可能读取的变量声明。
func remoteGoTestPackageVariableDeclarations(
	files []remoteGoTestFile,
) []remoteGoTestDeclaration {
	var declarations []remoteGoTestDeclaration
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			declarations = append(declarations, remoteGoTestDeclaration{
				filePath: file.path, source: file.source,
				file: file.file, declaration: declaration,
			})
		}
	}
	return declarations
}

func remoteGoTestUsesReflect(file *ast.File) bool {
	for _, importPath := range remoteGoTestImports(file) {
		if importPath == "reflect" {
			return true
		}
	}
	return false
}

func (snapshot *remoteGitTreeSnapshot) allGoTestSources(files []remoteGoTestFile, directory string, selected map[string]remoteGitTreeEntry) ([]remoteGoTestSource, bool, error) {
	result := make([]remoteGoTestSource, 0, len(files))
	for _, file := range files {
		observesWholeTree, err := snapshot.addGoTestFileObservedEntries(
			directory,
			file.file,
			file.file,
			selected,
		)
		if err != nil || observesWholeTree {
			return nil, observesWholeTree, err
		}
		result = append(result, remoteGoTestSource{path: file.path, text: file.source})
	}
	return result, false, nil
}

// addGoTestFileObservedEntries 将测试源码内可静态证明的仓库读取加入输入集合。
func (snapshot *remoteGitTreeSnapshot) addGoTestFileObservedEntries(
	directory string,
	file *ast.File,
	observed ast.Node,
	selected map[string]remoteGitTreeEntry,
) (bool, error) {
	imports := remoteGoTestImports(file)
	wholeTree := false
	var visitErr error
	ast.Inspect(observed, func(node ast.Node) bool {
		if wholeTree || visitErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		kind, staticPath, dynamic := remoteGoTestObservation(call, file, imports)
		if kind == "" {
			return true
		}
		if dynamic {
			wholeTree = true
			return false
		}
		observedPath, ok := remoteGoTestRelativePath(directory, staticPath)
		if !ok {
			return true
		}
		switch kind {
		case "glob":
			visitErr = snapshot.addRemoteGitGlobEntries(observedPath, selected)
		case "tree":
			snapshot.addRemoteGitDirectoryEntries(observedPath, selected)
		default:
			snapshot.addRemoteGitPathEntry(observedPath, selected)
		}
		return visitErr == nil
	})
	return wholeTree, visitErr
}

func remoteGoTestImports(file *ast.File) map[string]string {
	imports := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = importPath
	}
	return imports
}

// remoteGoTestObservation classifies repository-observation calls. An empty kind is not an observation.
// remoteGoTestObservation 分类测试中的候选树文件系统读取。
func remoteGoTestObservation(call *ast.CallExpr, file *ast.File, imports map[string]string) (kind string, staticPath string, dynamic bool) {
	importPath, method, ok := remoteGoTestSelector(call, imports)
	if !ok {
		return "", "", false
	}
	if importPath == "golang.org/x/tools/go/packages" && method == "Load" {
		return "tree", "", true
	}
	index, observesPath := remoteGoTestObservationPathIndex(importPath, method)
	if !observesPath {
		return "", "", false
	}
	value, ok := remoteGoTestStringArgument(call, index)
	if !ok {
		return "", "", false
	}
	if importPath == "io/fs" {
		value, ok = remoteGoTestFSObservationPath(call, file, imports, value)
		if !ok {
			return "", "", false
		}
	}
	return remoteGoTestObservationKind(method), value, false
}

func remoteGoTestSelector(call *ast.CallExpr, imports map[string]string) (string, string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return imports[packageName.Name], selector.Sel.Name, true
}

// remoteGoTestObservationPathIndex 返回仓库读取路径参数的位置。
func remoteGoTestObservationPathIndex(importPath, method string) (int, bool) {
	switch importPath {
	case "io/fs":
		return 1, method == "WalkDir" || method == "ReadFile" || method == "ReadDir"
	case "path/filepath":
		return 0, method == "Walk" || method == "WalkDir" || method == "Glob"
	case "os":
		return 0, remoteGoTestOSPathMethod(method)
	default:
		return 0, false
	}
}

func remoteGoTestOSPathMethod(method string) bool {
	switch method {
	case "Open", "OpenFile", "ReadFile", "ReadDir", "Stat", "Lstat", "Readlink":
		return true
	default:
		return false
	}
}

func remoteGoTestFSObservationPath(call *ast.CallExpr, file *ast.File, imports map[string]string, value string) (string, bool) {
	root, ok := remoteGoTestFSRoot(call.Args[0], file, imports, make(map[string]struct{}))
	return path.Join(root, value), ok
}

func remoteGoTestObservationKind(method string) string {
	if method == "Walk" || method == "WalkDir" {
		return "tree"
	}
	if method == "Glob" {
		return "glob"
	}
	return "path"
}

// remoteGoTestFSRoot 解析可静态追踪的 fs.FS 根目录。
func remoteGoTestFSRoot(expression ast.Expr, file *ast.File, imports map[string]string, seen map[string]struct{}) (string, bool) {
	if call, ok := expression.(*ast.CallExpr); ok {
		return remoteGoTestFSCallRoot(call, file, imports, seen)
	}
	return remoteGoTestFSIdentifierRoot(expression, file, imports, seen)
}

// remoteGoTestFSCallRoot 解析 DirFS 和 Sub 两类可追踪 fs.FS 构造调用。
func remoteGoTestFSCallRoot(call *ast.CallExpr, file *ast.File, imports map[string]string, seen map[string]struct{}) (string, bool) {
	importPath, method, ok := remoteGoTestSelector(call, imports)
	if !ok {
		return "", false
	}
	if importPath == "os" && method == "DirFS" {
		return remoteGoTestStringArgument(call, 0)
	}
	if importPath != "io/fs" || method != "Sub" {
		return "", false
	}
	root, ok := remoteGoTestFSRoot(call.Args[0], file, imports, seen)
	if !ok {
		return "", false
	}
	subdirectory, ok := remoteGoTestStringArgument(call, 1)
	return path.Join(root, subdirectory), ok
}

// remoteGoTestFSIdentifierRoot 回溯包级变量持有的静态 fs.FS 根目录。
func remoteGoTestFSIdentifierRoot(expression ast.Expr, file *ast.File, imports map[string]string, seen map[string]struct{}) (string, bool) {
	identifier, ok := expression.(*ast.Ident)
	_, visited := seen[identifier.Name]
	if !ok || visited {
		return "", false
	}
	seen[identifier.Name] = struct{}{}
	value, ok := remoteGoTestFSVariableValue(identifier.Name, file)
	if !ok {
		return "", false
	}
	return remoteGoTestFSRoot(value, file, imports, seen)
}

// remoteGoTestFSVariableValue 查找包级变量中唯一的 fs.FS 构造表达式。
func remoteGoTestFSVariableValue(name string, file *ast.File) (ast.Expr, bool) {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name || len(value.Values) != 1 {
				continue
			}
			return value.Values[0], true
		}
	}
	return nil, false
}

func remoteGoTestStringArgument(call *ast.CallExpr, index int) (string, bool) {
	if index >= len(call.Args) {
		return "", false
	}
	literal, ok := call.Args[index].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func remoteGoTestRelativePath(directory string, value string) (string, bool) {
	if value == "" || path.IsAbs(value) {
		return "", false
	}
	resolved := path.Clean(path.Join(directory, value))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	return resolved, true
}

func (snapshot *remoteGitTreeSnapshot) addRemoteGitPathEntry(filePath string, selected map[string]remoteGitTreeEntry) {
	if entry, ok := snapshot.byPath[filePath]; ok {
		selected[filePath] = entry
	}
}

func (snapshot *remoteGitTreeSnapshot) addRemoteGitDirectoryEntries(directory string, selected map[string]remoteGitTreeEntry) {
	for _, entry := range snapshot.entries {
		if entry.path == directory || strings.HasPrefix(entry.path, directory+"/") {
			selected[entry.path] = entry
		}
	}
}

func (snapshot *remoteGitTreeSnapshot) addRemoteGitGlobEntries(pattern string, selected map[string]remoteGitTreeEntry) error {
	for _, entry := range snapshot.entries {
		matched, err := path.Match(pattern, entry.path)
		if err != nil {
			return fmt.Errorf("match static Go test glob %q: %w", pattern, err)
		}
		if matched {
			selected[entry.path] = entry
		}
	}
	return nil
}
