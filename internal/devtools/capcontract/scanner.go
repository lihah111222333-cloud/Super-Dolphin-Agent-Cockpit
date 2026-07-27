package capcontract

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

var canonicalTargets = []string{
	"darwin/amd64",
	"darwin/arm64",
	"linux/amd64",
	"linux/arm64",
	"windows/amd64",
	"windows/arm64",
}

// ScanOptions 是能力契约扫描入口参数。
// RepoRoot 与 Roots 分离，确保报告中保留相对路径而不是本机绝对路径。
type ScanOptions struct {
	RepoRoot    string
	Roots       []string
	GeneratedAt string
}

// Scan 扫描指定 Go 根目录并生成能力契约清单。
// Roots 为空会直接报错，避免生成看似成功但没有覆盖面的空清单。
func Scan(opts ScanOptions) (*Manifest, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	if len(opts.Roots) == 0 {
		return nil, fmt.Errorf("capability contract scan roots are required")
	}
	manifest := &Manifest{
		Version:     "1.0",
		GeneratedAt: opts.GeneratedAt,
		Roots:       normalizeRoots(opts.Roots),
		Targets:     append([]string(nil), canonicalTargets...),
	}
	merged := map[string]PackageManifest{}
	for _, target := range manifest.Targets {
		targetPackages, provenance, err := scanTarget(repoRoot, manifest.Roots, target)
		if err != nil {
			return nil, err
		}
		manifest.Provenance = append(manifest.Provenance, provenance)
		for _, pkg := range targetPackages {
			if existing, ok := merged[pkg.Path]; ok {
				merged[pkg.Path] = mergePackageManifests(existing, pkg)
				continue
			}
			merged[pkg.Path] = pkg
		}
	}
	for _, pkg := range merged {
		manifest.Packages = append(manifest.Packages, pkg)
	}
	sort.Slice(manifest.Packages, func(i, j int) bool { return manifest.Packages[i].Path < manifest.Packages[j].Path })
	manifest.Summary = computeSummary(manifest.Packages)
	return manifest, nil
}

// mergePackageManifests 按符号签名合并不同平台包清单，并保持全部符号列表稳定排序。
func mergePackageManifests(dst, src PackageManifest) PackageManifest {
	for _, item := range src.Functions {
		if !containsFunction(dst.Functions, functionKey(item)) {
			dst.Functions = append(dst.Functions, item)
		}
	}
	for _, item := range src.Methods {
		if !containsMethod(dst.Methods, methodKey(item)) {
			dst.Methods = append(dst.Methods, item)
		}
	}
	for _, item := range src.Interfaces {
		if !containsInterface(dst.Interfaces, interfaceKey(item)) {
			dst.Interfaces = append(dst.Interfaces, item)
		}
	}
	for _, item := range src.Structs {
		if !containsStruct(dst.Structs, item.Name) {
			dst.Structs = append(dst.Structs, item)
		}
	}
	sortFunctions(dst.Functions)
	sortMethods(dst.Methods)
	sortInterfaces(dst.Interfaces)
	sortStructs(dst.Structs)
	return dst
}

func containsFunction(items []FunctionManifest, key string) bool {
	for _, item := range items {
		if functionKey(item) == key {
			return true
		}
	}
	return false
}
func containsMethod(items []MethodManifest, key string) bool {
	for _, item := range items {
		if methodKey(item) == key {
			return true
		}
	}
	return false
}
func containsInterface(items []InterfaceManifest, key string) bool {
	for _, item := range items {
		if interfaceKey(item) == key {
			return true
		}
	}
	return false
}
func containsStruct(items []StructManifest, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

// scanTarget 通过 go/packages 在单个固定 GOOS/GOARCH 下加载类型与语法树，失败即阻断生成。
func scanTarget(repoRoot string, roots []string, target string) ([]PackageManifest, TargetProvenance, error) {
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		return nil, TargetProvenance{}, fmt.Errorf("invalid capability target %q", target)
	}
	patterns := make([]string, 0, len(roots))
	for _, root := range roots {
		patterns = append(patterns, "./"+root+"/...")
	}
	cfg := &packages.Config{
		Dir:  repoRoot,
		Env:  append(os.Environ(), "GOOS="+parts[0], "GOARCH="+parts[1], "CGO_ENABLED=0"),
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes,
	}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, TargetProvenance{}, fmt.Errorf("load capability target %s: %w", target, err)
	}
	var result []PackageManifest
	provenance := TargetProvenance{Target: target}
	for _, loadedPkg := range loaded {
		if len(loadedPkg.Errors) > 0 {
			return nil, TargetProvenance{}, fmt.Errorf("load capability target %s package %s: %s", target, loadedPkg.PkgPath, loadedPkg.Errors[0])
		}
		pkg, err := scanLoadedPackage(repoRoot, loadedPkg)
		if err != nil {
			return nil, TargetProvenance{}, err
		}
		if pkg == nil {
			continue
		}
		result = append(result, *pkg)
		provenance.Packages = append(provenance.Packages, pkg.Path)
		for symbol := range manifestSymbols(&Manifest{Packages: []PackageManifest{*pkg}}) {
			provenance.Symbols = append(provenance.Symbols, symbol)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	sort.Strings(provenance.Packages)
	sort.Strings(provenance.Symbols)
	return result, provenance, nil
}

// scanLoadedPackage 从 go/packages 已按构建约束筛选的文件中提取单个平台包能力面。
func scanLoadedPackage(repoRoot string, loaded *packages.Package) (*PackageManifest, error) {
	if len(loaded.Syntax) == 0 || len(loaded.CompiledGoFiles) == 0 {
		return nil, nil
	}
	dir := filepath.Dir(loaded.CompiledGoFiles[0])
	relPath, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		return nil, err
	}
	manifest := &PackageManifest{Path: filepath.ToSlash(relPath), Name: loaded.Name}
	files := append([]*ast.File(nil), loaded.Syntax...)
	sort.Slice(files, func(i, j int) bool {
		return loaded.Fset.Position(files[i].Package).Filename < loaded.Fset.Position(files[j].Package).Filename
	})
	for _, file := range files {
		if manifest.Description == "" && file.Doc != nil {
			line, _, _ := strings.Cut(file.Doc.Text(), "\n")
			line = strings.TrimPrefix(line, "Package "+loaded.Name+" ")
			manifest.Description = strings.TrimSpace(strings.TrimPrefix(line, "Package "+loaded.Name))
		}
		extractFile(file, manifest)
	}
	sortFunctions(manifest.Functions)
	sortMethods(manifest.Methods)
	sortInterfaces(manifest.Interfaces)
	sortStructs(manifest.Structs)
	return manifest, nil
}

// normalizeRoots 规范化、去重并排序扫描根路径列表。
func normalizeRoots(roots []string) []string {
	normalized := make([]string, 0, len(roots))
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = filepath.ToSlash(strings.TrimSpace(root))
		root = strings.Trim(root, "/")
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		normalized = append(normalized, root)
	}
	sort.Strings(normalized)
	return normalized
}

// extractFile 把一个 AST 文件中的函数、方法、接口和结构体追加到包清单。
func extractFile(file *ast.File, manifest *PackageManifest) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				manifest.Functions = append(manifest.Functions, extractFunction(d))
			} else {
				manifest.Methods = append(manifest.Methods, extractMethod(d))
			}
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				extractTypes(d, manifest)
			}
		}
	}
}

// extractTypes 从 GenDecl 中提取接口和结构体定义并追加到 PackageManifest。
func extractTypes(decl *ast.GenDecl, manifest *PackageManifest) {
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		switch t := ts.Type.(type) {
		case *ast.InterfaceType:
			manifest.Interfaces = append(manifest.Interfaces, extractInterface(ts, t))
		case *ast.StructType:
			manifest.Structs = append(manifest.Structs, StructManifest{Name: ts.Name.Name, Exported: isExported(ts.Name.Name)})
		}
	}
}

// extractFunction 从 FuncDecl 中提取包级函数的清单信息。
func extractFunction(fn *ast.FuncDecl) FunctionManifest {
	params, returns := functionSignature(fn.Type)
	return FunctionManifest{Name: fn.Name.Name, Exported: isExported(fn.Name.Name), Params: params, Returns: returns}
}

// extractMethod 从 FuncDecl 中提取方法的清单信息，包含接收者类型。
func extractMethod(fn *ast.FuncDecl) MethodManifest {
	params, returns := functionSignature(fn.Type)
	return MethodManifest{Name: fn.Name.Name, Exported: isExported(fn.Name.Name), Receiver: typeToString(fn.Recv.List[0].Type), Params: params, Returns: returns}
}

// extractInterface 提取接口方法和嵌入类型。
// 方法和嵌入列表会稳定排序，避免 AST 原始顺序变化影响 diff。
func extractInterface(ts *ast.TypeSpec, iface *ast.InterfaceType) InterfaceManifest {
	out := InterfaceManifest{Name: ts.Name.Name, Exported: isExported(ts.Name.Name)}
	if iface.Methods == nil {
		return out
	}
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 {
			out.Embeds = append(out.Embeds, typeToString(field.Type))
			continue
		}
		for _, name := range field.Names {
			entry := InterfaceMethodEntry{Name: name.Name}
			if ft, ok := field.Type.(*ast.FuncType); ok {
				entry.Params, entry.Returns = functionSignature(ft)
			}
			out.Methods = append(out.Methods, entry)
		}
	}
	sort.Strings(out.Embeds)
	sort.Slice(out.Methods, func(i, j int) bool { return interfaceMethodKey(out.Methods[i]) < interfaceMethodKey(out.Methods[j]) })
	return out
}

// functionSignature 从 FuncType 中分别提取参数列表和返回类型列表。
func functionSignature(fnType *ast.FuncType) ([]ParamManifest, []string) {
	var params []ParamManifest
	if fnType.Params != nil {
		params = extractParams(fnType.Params)
	}
	var returns []string
	if fnType.Results != nil {
		returns = extractReturnTypes(fnType.Results)
	}
	return params, returns
}

// extractParams 从 FieldList 中提取参数名和类型的清单列表。
func extractParams(fields *ast.FieldList) []ParamManifest {
	var params []ParamManifest
	for _, field := range fields.List {
		typeStr := typeToString(field.Type)
		if len(field.Names) == 0 {
			params = append(params, ParamManifest{Type: typeStr})
			continue
		}
		for _, name := range field.Names {
			params = append(params, ParamManifest{Name: name.Name, Type: typeStr})
		}
	}
	return params
}

// extractReturnTypes 从 FieldList 中提取返回类型字符串列表。
func extractReturnTypes(fields *ast.FieldList) []string {
	var returns []string
	for _, field := range fields.List {
		typeStr := typeToString(field.Type)
		if len(field.Names) == 0 {
			returns = append(returns, typeStr)
			continue
		}
		for range field.Names {
			returns = append(returns, typeStr)
		}
	}
	return returns
}

// typeToString 将常见 AST 类型表达式转换为清单中的稳定字符串。
// 未覆盖的复合表达式交给 compositeTypeToString 处理，避免直接丢失签名信息。
func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeToString(t.X)
	case *ast.SelectorExpr:
		return typeToString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeToString(t.Elt)
	case *ast.MapType:
		return "map[" + typeToString(t.Key) + "]" + typeToString(t.Value)
	default:
		return compositeTypeToString(expr)
	}
}

// compositeTypeToString 转换结构体、泛型、接口、函数和省略号等复合类型表达式。
// 无法识别的表达式返回 unknown，使清单 diff 显式暴露解析盲区。
func compositeTypeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StructType:
		return "struct{" + fieldListToString(t.Fields, "; ") + "}"
	case *ast.ChanType:
		return channelTypeToString(t)
	case *ast.IndexExpr:
		return typeToString(t.X) + "[" + typeToString(t.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, 0, len(t.Indices))
		for _, index := range t.Indices {
			parts = append(parts, typeToString(index))
		}
		return typeToString(t.X) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.InterfaceType:
		return interfaceTypeToString(t)
	case *ast.FuncType:
		return "func" + funcSignatureSuffix(t)
	case *ast.Ellipsis:
		return "..." + typeToString(t.Elt)
	default:
		return "unknown"
	}
}

// channelTypeToString 把 channel 类型转换为字符串，区分收发方向。
func channelTypeToString(t *ast.ChanType) string {
	switch t.Dir {
	case ast.RECV:
		return "<-chan " + typeToString(t.Value)
	case ast.SEND:
		return "chan<- " + typeToString(t.Value)
	default:
		return "chan " + typeToString(t.Value)
	}
}

// interfaceTypeToString 将匿名接口类型格式化为紧凑签名字符串。
func interfaceTypeToString(iface *ast.InterfaceType) string {
	if iface.Methods == nil || len(iface.Methods.List) == 0 {
		return "interface{}"
	}
	parts := make([]string, 0, len(iface.Methods.List))
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 {
			parts = append(parts, typeToString(field.Type))
			continue
		}
		for _, name := range field.Names {
			if fn, ok := field.Type.(*ast.FuncType); ok {
				parts = append(parts, name.Name+funcSignatureSuffix(fn))
			} else {
				parts = append(parts, name.Name+" "+typeToString(field.Type))
			}
		}
	}
	return "interface{" + strings.Join(parts, "; ") + "}"
}

// funcSignatureSuffix 把函数类型格式化为参数列表 + 返回列表的后缀字符串。
func funcSignatureSuffix(fn *ast.FuncType) string {
	params := fieldListToString(fn.Params, ", ")
	returns := returnFieldListToString(fn.Results)
	if returns == "" {
		return "(" + params + ")"
	}
	if fn.Results != nil && (len(fn.Results.List) > 1 || len(fn.Results.List[0].Names) > 0) {
		return "(" + params + ") (" + returns + ")"
	}
	return "(" + params + ") " + returns
}

// fieldListToString 将字段列表格式化为签名片段。
// 有名字段会保留名称，未命名字段只保留类型。
func fieldListToString(fields *ast.FieldList, separator string) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		fieldType := typeToString(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, fieldType)
			continue
		}
		names := make([]string, 0, len(field.Names))
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
		parts = append(parts, strings.Join(names, ", ")+" "+fieldType)
	}
	return strings.Join(parts, separator)
}

// returnFieldListToString 把返回值 FieldList 转为逗号分隔的类型字符串。
func returnFieldListToString(fields *ast.FieldList) string {
	return fieldListToString(fields, ", ")
}

// isExported 判断标识符是否为导出名称（首字母大写）。
func isExported(name string) bool {
	return name != "" && unicode.IsUpper(rune(name[0]))
}

// computeSummary 汇总包、函数、方法、接口、结构体及导出性计数。
func computeSummary(packages []PackageManifest) ManifestSummary {
	summary := ManifestSummary{TotalPackages: len(packages)}
	for _, pkg := range packages {
		summary.TotalFunctions += len(pkg.Functions)
		summary.TotalMethods += len(pkg.Methods)
		summary.TotalInterfaces += len(pkg.Interfaces)
		summary.TotalStructs += len(pkg.Structs)
		for _, fn := range pkg.Functions {
			addExportCount(&summary, fn.Exported)
		}
		for _, method := range pkg.Methods {
			addExportCount(&summary, method.Exported)
		}
		for _, iface := range pkg.Interfaces {
			addExportCount(&summary, iface.Exported)
			summary.TotalInterfaceMethods += len(iface.Methods)
		}
		for _, st := range pkg.Structs {
			addExportCount(&summary, st.Exported)
		}
	}
	return summary
}

// addExportCount 根据导出性更新清单摘要的已导出/未导出计数。
func addExportCount(summary *ManifestSummary, exported bool) {
	if exported {
		summary.TotalExported++
	} else {
		summary.TotalUnexported++
	}
}

// sortFunctions/sortMethods/sortInterfaces/sortStructs 按稳定键对各类型列表排序，保证清单输出一致。
func sortFunctions(items []FunctionManifest) {
	sort.Slice(items, func(i, j int) bool { return functionKey(items[i]) < functionKey(items[j]) })
}

// sortMethods 按接收者+名称+签名排序方法列表，保证清单输出稳定。
func sortMethods(items []MethodManifest) {
	sort.Slice(items, func(i, j int) bool { return methodKey(items[i]) < methodKey(items[j]) })
}

// sortInterfaces 按接口名+嵌入+方法签名排序接口列表。
func sortInterfaces(items []InterfaceManifest) {
	sort.Slice(items, func(i, j int) bool { return interfaceKey(items[i]) < interfaceKey(items[j]) })
}

// sortStructs 按名称排序结构体列表。
func sortStructs(items []StructManifest) {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
}

// functionKey 生成函数清单的比较键，包含名称、参数和返回类型。
func functionKey(fn FunctionManifest) string {
	return strings.Join([]string{fn.Name, paramsKey(fn.Params), strings.Join(fn.Returns, ",")}, "|")
}

// methodKey 生成方法清单的比较键，包含接收者、名称和签名。
func methodKey(method MethodManifest) string {
	return strings.Join([]string{method.Receiver, method.Name, paramsKey(method.Params), strings.Join(method.Returns, ",")}, "|")
}

// interfaceKey 生成接口清单的比较键，包含名称、嵌入和方法签名。
func interfaceKey(iface InterfaceManifest) string {
	parts := make([]string, 0, len(iface.Methods))
	for _, method := range iface.Methods {
		parts = append(parts, interfaceMethodKey(method))
	}
	return strings.Join([]string{iface.Name, strings.Join(iface.Embeds, ","), strings.Join(parts, ",")}, "|")
}

// interfaceMethodKey 生成接口方法的比较键：名称 + 参数 + 返回。
func interfaceMethodKey(method InterfaceMethodEntry) string {
	return strings.Join([]string{method.Name, paramsKey(method.Params), strings.Join(method.Returns, ",")}, ":")
}
