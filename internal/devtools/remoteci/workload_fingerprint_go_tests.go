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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/godistribution"
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

type remoteGoBuildProfile struct {
	race bool
}

type remoteGoTestScope uint8

const (
	remoteGoTestScopeSelector remoteGoTestScope = iota
	remoteGoTestScopePackage
	remoteGoTestScopeCompileClosure
	remoteGoTestScopeTree
)

func (scope remoteGoTestScope) widen(other remoteGoTestScope) remoteGoTestScope {
	if other > scope {
		return other
	}
	return scope
}

func (profile remoteGoBuildProfile) cacheKey() string {
	return fmt.Sprintf("go_flags=%s", gate.CanonicalGoFlags(profile.race))
}

func (snapshot *remoteGitTreeSnapshot) remoteGoTestDeclarations(directory string, profile remoteGoBuildProfile) ([]remoteGoTestFile, map[string][]remoteGoTestDeclaration, bool) {
	snapshot.cacheMu.Lock()
	if snapshot.goTestDeclarationCache == nil {
		snapshot.goTestDeclarationCache = make(map[string]remoteGoTestDeclarationCache)
	}
	cacheKey := profile.cacheKey() + ":" + directory
	cached, ok := snapshot.goTestDeclarationCache[cacheKey]
	snapshot.cacheMu.Unlock()
	if ok {
		return cached.files, cached.declarations, cached.fallback
	}
	files, declarations, fallback := snapshot.parseRemoteGoTestDeclarations(directory, profile)
	cached = remoteGoTestDeclarationCache{
		files: files, declarations: declarations, fallback: fallback,
	}
	snapshot.cacheMu.Lock()
	if existing, exists := snapshot.goTestDeclarationCache[cacheKey]; exists {
		cached = existing
	} else {
		snapshot.goTestDeclarationCache[cacheKey] = cached
	}
	snapshot.cacheMu.Unlock()
	return cached.files, cached.declarations, cached.fallback
}

// parseRemoteGoTestDeclarations 解析 worker 平台适用测试文件的顶层声明索引。
func (snapshot *remoteGitTreeSnapshot) parseRemoteGoTestDeclarations(directory string, profile remoteGoBuildProfile) ([]remoteGoTestFile, map[string][]remoteGoTestDeclaration, bool) {
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
		if !remoteGoSourceAppliesLinuxAMD64WithProfile(filePath, source, profile) {
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
	return remoteGoSourceAppliesLinuxAMD64WithProfile(filePath, source, remoteGoBuildProfile{})
}

func remoteGoSourceAppliesLinuxAMD64WithProfile(filePath string, source []byte, profile remoteGoBuildProfile) bool {
	return remoteBuildSourceAppliesLinuxAMD64WithProfile(filePath, source, profile)
}

// remoteBuildSourceAppliesLinuxAMD64 依据文件名与 build 约束判断 worker 是否会编译源码。
func remoteBuildSourceAppliesLinuxAMD64(filePath string, source []byte) bool {
	return remoteBuildSourceAppliesLinuxAMD64WithProfile(filePath, source, remoteGoBuildProfile{})
}

func remoteBuildSourceAppliesLinuxAMD64WithProfile(filePath string, source []byte, profile remoteGoBuildProfile) bool {
	return remoteBuildFilenameAppliesLinuxAMD64(filePath) && remoteBuildConstraintAppliesLinuxAMD64(source, profile)
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
func remoteBuildConstraintAppliesLinuxAMD64(source []byte, profile remoteGoBuildProfile) bool {
	for line := range strings.SplitSeq(string(source), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:build ") {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return true // The compiler will reject it; retain it rather than false-hit.
		}
		if !remoteBuildConstraintUsesOnlyKnownTags(expr) {
			return true
		}
		asset, err := godistribution.RemoteCIAsset()
		if err != nil {
			return true
		}
		goReleaseMinor, ok := remoteGoReleaseMinor(asset.Version)
		if !ok {
			return true
		}
		return expr.Eval(func(tag string) bool {
			return remoteBuildConstraintTagAppliesLinuxAMD64(tag, goReleaseMinor, profile)
		})
	}
	return true
}

// remoteBuildConstraintUsesOnlyKnownTags 拒绝用无法由远程 worker 完整重建的约束缩窄输入。
func remoteBuildConstraintUsesOnlyKnownTags(expr constraint.Expr) bool {
	switch value := expr.(type) {
	case *constraint.TagExpr:
		return remoteBuildConstraintTagKnown(value.Tag)
	case *constraint.NotExpr:
		return remoteBuildConstraintUsesOnlyKnownTags(value.X)
	case *constraint.AndExpr:
		return remoteBuildConstraintUsesOnlyKnownTags(value.X) &&
			remoteBuildConstraintUsesOnlyKnownTags(value.Y)
	case *constraint.OrExpr:
		return remoteBuildConstraintUsesOnlyKnownTags(value.X) &&
			remoteBuildConstraintUsesOnlyKnownTags(value.Y)
	default:
		return false
	}
}

func remoteBuildConstraintTagKnown(tag string) bool {
	if _, known := remoteBuildConstraintSpecialTag(tag); known {
		return true
	}
	if remoteGoKnownOS(tag) || remoteGoKnownArch(tag) {
		return true
	}
	_, ok := remoteGoReleaseTagMinor(tag)
	return ok
}

// remoteBuildConstraintTagAppliesLinuxAMD64 判断 build tag 是否适用于 linux/amd64 worker。
func remoteBuildConstraintTagAppliesLinuxAMD64(tag string, goReleaseMinor int, profile remoteGoBuildProfile) bool {
	if special, known := remoteBuildConstraintSpecialTag(tag); known {
		return special.applies(profile)
	}
	if remoteGoKnownOS(tag) {
		return tag == "linux"
	}
	if remoteGoKnownArch(tag) {
		return tag == "amd64"
	}
	minor, ok := remoteGoReleaseTagMinor(tag)
	return ok && minor <= goReleaseMinor
}

type remoteBuildConstraintSpecial uint8

const (
	remoteBuildConstraintAlwaysApplies remoteBuildConstraintSpecial = iota
	remoteBuildConstraintNeverApplies
	remoteBuildConstraintRaceApplies
)

// remoteBuildConstraintSpecialTag 归类不能从 OS、架构或 Go 版本推导的固定 build tag。
func remoteBuildConstraintSpecialTag(tag string) (remoteBuildConstraintSpecial, bool) {
	switch tag {
	case "unix", "cgo", "gc", "amd64.v1":
		return remoteBuildConstraintAlwaysApplies, true
	case "gccgo", "amd64.v2", "amd64.v3", "amd64.v4":
		return remoteBuildConstraintNeverApplies, true
	case "race":
		return remoteBuildConstraintRaceApplies, true
	default:
		return 0, false
	}
}

// applies 返回固定 build tag 是否适用于给定的远程 Go 编译 profile。
func (special remoteBuildConstraintSpecial) applies(profile remoteGoBuildProfile) bool {
	switch special {
	case remoteBuildConstraintAlwaysApplies:
		return true
	case remoteBuildConstraintRaceApplies:
		return profile.race
	default:
		return false
	}
}

func remoteGoReleaseMinor(version string) (int, bool) {
	const prefix = "go1."
	if !strings.HasPrefix(version, prefix) {
		return 0, false
	}
	minor, _, _ := strings.Cut(strings.TrimPrefix(version, prefix), ".")
	parsed, err := strconv.Atoi(minor)
	return parsed, err == nil && parsed >= 1
}

func remoteGoReleaseTagMinor(tag string) (int, bool) {
	const prefix = "go1."
	if !strings.HasPrefix(tag, prefix) {
		return 0, false
	}
	minor, err := strconv.Atoi(strings.TrimPrefix(tag, prefix))
	return minor, err == nil && minor >= 1
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
	start := remoteGoTestDeclarationStart(declaration.declaration) - 1
	end := declaration.declaration.End() - 1
	text.Write(declaration.source[start:end])
	return text.Bytes()
}

// remoteGoTestDeclarationStart 将声明拥有的注释纳入 selector 语义；go:embed
// 等编译指令不能因移除无关 sibling test blob 而从 PASS 输入中消失。
func remoteGoTestDeclarationStart(declaration ast.Decl) token.Pos {
	switch current := declaration.(type) {
	case *ast.FuncDecl:
		if current.Doc != nil {
			return current.Doc.Pos()
		}
	case *ast.GenDecl:
		if current.Doc != nil {
			return current.Doc.Pos()
		}
	}
	return declaration.Pos()
}

// goTestSources returns the selected test declarations and their direct repository observations.
// Any source we cannot close conservatively binds every test source in the package.
// goTestSources 计算单个测试声明的源码闭包和直接仓库观察。
func (snapshot *remoteGitTreeSnapshot) goTestSources(target, directory string, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile, includeCompileInputs bool) ([]remoteGoTestSource, bool, error) {
	files, declarations, fallback := snapshot.remoteGoTestDeclarations(directory, profile)
	if fallback {
		return snapshot.allGoTestSources(files, directory, selected)
	}
	targetDeclaration, ok := declarations[target]
	if !ok || len(targetDeclaration) != 1 {
		return snapshot.allGoTestSources(files, directory, selected)
	}
	selectedDeclarations, reflectUsed := remoteGoTestSelectedDeclarations(targetDeclaration[0], files, declarations)
	if reflectUsed {
		// 无法从 AST 闭合反射调用的目标可能观察候选 tree 任意位置，必须直接全树绑定。
		return nil, true, nil
	}
	return snapshot.goTestDeclarationSources(directory, selectedDeclarations, selected, profile, includeCompileInputs)
}

// remoteGoTestSelectedDeclarations 解析入口测试、初始化声明和引用闭包。
func remoteGoTestSelectedDeclarations(target remoteGoTestDeclaration, files []remoteGoTestFile, declarations map[string][]remoteGoTestDeclaration) (map[ast.Decl]remoteGoTestDeclaration, bool) {
	queue := []remoteGoTestDeclaration{target}
	queue = append(queue, declarations["TestMain"]...)
	queue = append(queue, declarations["init"]...)
	queue = append(queue, remoteGoTestPackageVariableDeclarations(files)...)
	reflectValues := remoteGoTestPackageReflectValueNames(files)
	selectedDeclarations := make(map[ast.Decl]remoteGoTestDeclaration)
	for len(queue) > 0 {
		declaration := queue[0]
		queue = queue[1:]
		if _, ok := selectedDeclarations[declaration.declaration]; ok {
			continue
		}
		selectedDeclarations[declaration.declaration] = declaration
		dependencies := remoteGoTestDeclarationDependencies(declaration.declaration)
		if remoteGoTestUsesUnresolvedReflection(declaration.declaration, declaration.file, reflectValues) {
			return nil, true
		}
		for dependency := range dependencies {
			queue = append(queue, declarations[dependency]...)
		}
	}
	return selectedDeclarations, false
}

// goTestDeclarationSources 将声明闭包转换为源码摘要并收集其观察输入。
func (snapshot *remoteGitTreeSnapshot) goTestDeclarationSources(directory string, selectedDeclarations map[ast.Decl]remoteGoTestDeclaration, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile, includeCompileInputs bool) ([]remoteGoTestSource, bool, error) {
	result := make([]remoteGoTestSource, 0, len(selectedDeclarations))
	for _, declaration := range selectedDeclarations {
		declarationText, err := snapshot.goTestDeclarationInputText(declaration, selected, profile, includeCompileInputs)
		if err != nil {
			return nil, false, err
		}
		scope, err := snapshot.addGoTestFileObservedEntriesScoped(
			directory,
			declaration.file,
			declaration.declaration,
			selected,
			profile,
		)
		observesWholeTree := scope == remoteGoTestScopeTree
		if err != nil || observesWholeTree {
			return nil, observesWholeTree, err
		}
		productionScope, productionSources, err := snapshot.goProductionRuntimeInputs(
			directory,
			declaration,
			selected,
			profile,
		)
		if err != nil {
			return nil, false, err
		}
		if productionScope == remoteGoTestScopeTree {
			snapshot.addGoProductionRuntimeTreeEntries(directory, selected)
		}
		result = append(result, remoteGoTestSource{path: declaration.filePath, text: declarationText})
		result = append(result, productionSources...)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].path == result[right].path {
			return bytes.Compare(result[left].text, result[right].text) < 0
		}
		return result[left].path < result[right].path
	})
	return result, false, nil
}

// goTestDeclarationInputText 在 broad 路径保留编译导入，在独立运行时投票中只保留 embed 资产。
func (snapshot *remoteGitTreeSnapshot) goTestDeclarationInputText(declaration remoteGoTestDeclaration, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile, includeCompileInputs bool) ([]byte, error) {
	if includeCompileInputs {
		return snapshot.addGoTestDeclarationCompileInputs(declaration, selected, profile)
	}
	if err := snapshot.addGoEmbedEntries(path.Dir(declaration.filePath), declaration.source, selected); err != nil {
		return nil, err
	}
	return remoteGoTestDeclarationText(declaration), nil
}

// addGoTestDeclarationCompileInputs 收集 selector 声明自身的 embed 与本地导入；
// 未引用 sibling test 仍由独立 compile-group 构建，但不进入 PASS 语义。
func (snapshot *remoteGitTreeSnapshot) addGoTestDeclarationCompileInputs(declaration remoteGoTestDeclaration, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) ([]byte, error) {
	declarationText := remoteGoTestDeclarationText(declaration)
	if err := snapshot.addGoEmbedEntries(path.Dir(declaration.filePath), declaration.source, selected); err != nil {
		return nil, err
	}
	for _, importPath := range remoteGoTestImports(declaration.file) {
		localDirectory, local := snapshot.resolveLocalGoImport(importPath)
		if !local {
			continue
		}
		if err := snapshot.addProductionGoPackageEntriesWithAssets(localDirectory, selected, false, profile); err != nil {
			return nil, err
		}
	}
	return declarationText, nil
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
	aliasScope := snapshot.remoteGoObservedAliasScope(directory, remoteGoBuildProfile{}, true, observed, imports)
	wholeTree := false
	var visitErr error
	ast.Inspect(observed, func(node ast.Node) bool {
		if visitErr != nil {
			return false
		}
		continueVisit, observedWholeTree, err := snapshot.addGoTestObservedNode(directory, file, imports, node, selected, aliasScope, wholeTree)
		wholeTree = wholeTree || observedWholeTree
		visitErr = err
		return continueVisit && err == nil
	})
	return wholeTree, visitErr
}

// addGoTestObservedNode 处理单个测试 AST 节点并收敛观察作用域。
func (snapshot *remoteGitTreeSnapshot) addGoTestObservedNode(
	directory string,
	file *ast.File,
	imports map[string]string,
	node ast.Node,
	selected map[string]remoteGitTreeEntry,
	aliasScope remoteGoObservedAliasCallScope,
	wholeTree bool,
) (continueVisit bool, observedWholeTree bool, err error) {
	if remoteGoDotImportWholeTree(imports) {
		return true, true, nil
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return true, false, nil
	}
	if aliasScope.matches(call) {
		return true, true, nil
	}
	kind, staticPath, dynamic := remoteGoTestObservation(call, file, imports)
	if kind == "" {
		return true, false, nil
	}
	if dynamic {
		// exact-test 仍需将动态观察收敛到整树；整包指纹则必须继续扫描同一源码文件中的后续静态观察。
		return true, true, nil
	}
	observedPath, ok := remoteGoTestRelativePath(directory, staticPath)
	if !ok {
		// 非 canonical source 根或越出候选树的路径无法安全闭合，必须整树绑定。
		return true, true, nil
	}
	if err := snapshot.addGoTestObservedNodePath(kind, observedPath, selected, wholeTree); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// addGoTestObservedNodePath 校验并加入测试节点解析出的仓库路径。
func (snapshot *remoteGitTreeSnapshot) addGoTestObservedNodePath(
	kind, observedPath string,
	selected map[string]remoteGitTreeEntry,
	wholeTree bool,
) error {
	if kind != "tree" && kind != "glob" {
		if _, exists := snapshot.byPath[observedPath]; !exists {
			if wholeTree {
				// exact-test 动态观察已经绑定整树；未知 CWD 下解析出的后续路径
				// 不能再派生更严格的缺失文件错误。
				return nil
			}
			return fmt.Errorf("static Go test file observation %q is absent from Git tree", observedPath)
		}
	}
	return snapshot.addRemoteGitObservedPath(kind, observedPath, selected)
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
	if method == "ReadDir" || method == "Walk" || method == "WalkDir" {
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

// remoteGoTestRelativePath 将测试观察路径安全收敛到规范工作树相对路径。
func remoteGoTestRelativePath(directory string, value string) (string, bool) {
	if value == "" {
		return "", false
	}
	if path.IsAbs(value) {
		if value == gate.ExecutorSourcePath {
			return ".", true
		}
		prefix := gate.ExecutorSourcePath + "/"
		if !strings.HasPrefix(value, prefix) {
			return "", false
		}
		directory = "."
		value = strings.TrimPrefix(value, prefix)
	}
	resolved := path.Clean(path.Join(directory, value))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	return resolved, true
}

func (snapshot *remoteGitTreeSnapshot) addRemoteGitDirectoryEntries(directory string, selected map[string]remoteGitTreeEntry) {
	for _, entry := range snapshot.entries {
		if entry.path == directory || strings.HasPrefix(entry.path, directory+"/") {
			selected[entry.path] = entry
		}
	}
}

// addRemoteGitGlobEntries 将静态 glob 匹配的 Git 条目加入观察集合。
func (snapshot *remoteGitTreeSnapshot) addRemoteGitGlobEntries(pattern string, selected map[string]remoteGitTreeEntry) error {
	for _, entry := range snapshot.entries {
		matched, err := path.Match(pattern, entry.path)
		if err != nil {
			return fmt.Errorf("match static Go test glob %q: %w", pattern, err)
		}
		if matched {
			if err := snapshot.addRemoteGitPathEntry(entry.path, selected); err != nil {
				return err
			}
		}
	}
	return nil
}
