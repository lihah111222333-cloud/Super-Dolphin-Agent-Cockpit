package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"slices"
	"sort"
)

// ProjectRemoteTestCommand 将 catalog 命令投影为唯一适用 package+Test 选择器。
func ProjectRemoteTestCommand(command RemoteTestCommand, environment RemoteTestEnvironment, packages []RemoteTestPackageFiles) (RemoteTestProjection, error) {
	if err := environment.Validate(); err != nil {
		return RemoteTestProjection{}, err
	}
	provided, err := indexRemoteTestPackages(command, packages)
	if err != nil {
		return RemoteTestProjection{}, err
	}
	declarations, inputs, err := collectRemoteTestDeclarations(command, environment, provided)
	if err != nil {
		return RemoteTestProjection{}, err
	}
	selectors, err := projectRemoteTestSelectors(command, declarations)
	if err != nil {
		return RemoteTestProjection{}, err
	}
	return RemoteTestProjection{Selectors: selectors, InputFiles: inputs}, nil
}

func indexRemoteTestPackages(command RemoteTestCommand, packages []RemoteTestPackageFiles) (map[string][]RemoteTestFile, error) {
	provided := make(map[string][]RemoteTestFile, len(packages))
	for _, packageFiles := range packages {
		if !slices.Contains(command.Packages, packageFiles.Package) {
			return nil, fmt.Errorf("remote test package %q is not present in command", packageFiles.Package)
		}
		if _, exists := provided[packageFiles.Package]; exists {
			return nil, fmt.Errorf("remote test package %q is duplicated", packageFiles.Package)
		}
		provided[packageFiles.Package] = packageFiles.Files
	}
	return provided, nil
}

func collectRemoteTestDeclarations(command RemoteTestCommand, environment RemoteTestEnvironment, provided map[string][]RemoteTestFile) (map[string][]string, []string, error) {
	declarations := make(map[string][]string, len(command.Packages))
	inputs := make([]string, 0)
	for _, packagePath := range command.Packages {
		files, exists := provided[packagePath]
		if !exists {
			return nil, nil, fmt.Errorf("remote test package %q declarations are missing", packagePath)
		}
		names, packageInputs, err := applicableRemoteTestDeclarations(files, environment, command.BuildTags)
		if err != nil {
			return nil, nil, fmt.Errorf("remote test package %q: %w", packagePath, err)
		}
		declarations[packagePath] = names
		inputs = append(inputs, packageInputs...)
	}
	return declarations, inputs, nil
}

// projectRemoteTestSelectors 按声明唯一性投影稳定 package#Test 结果。
func projectRemoteTestSelectors(command RemoteTestCommand, declarations map[string][]string) ([]RemoteTestSelector, error) {
	selectors := make([]RemoteTestSelector, 0)
	if len(command.TestNames) == 0 {
		for _, packagePath := range command.Packages {
			for _, name := range declarations[packagePath] {
				selectors = append(selectors, RemoteTestSelector{Package: packagePath, TestName: name})
			}
		}
	} else {
		for _, name := range command.TestNames {
			matchedPackage, err := uniqueRemoteTestPackage(name, command.Packages, declarations)
			if err != nil {
				return nil, err
			}
			selectors = append(selectors, RemoteTestSelector{Package: matchedPackage, TestName: name})
		}
	}
	if len(selectors) == 0 {
		return nil, errors.New("remote test command has zero applicable test matches")
	}
	return selectors, nil
}

func uniqueRemoteTestPackage(name string, packages []string, declarations map[string][]string) (string, error) {
	matched := ""
	for _, packagePath := range packages {
		if !slices.Contains(declarations[packagePath], name) {
			continue
		}
		if matched != "" {
			return "", fmt.Errorf("test %q matches multiple packages %q and %q", name, matched, packagePath)
		}
		matched = packagePath
	}
	if matched == "" {
		return "", fmt.Errorf("test %q does not match an applicable package declaration", name)
	}
	return matched, nil
}

// applicableRemoteTestDeclarations 读取适用平台的 exact tree 测试声明与输入文件。
func applicableRemoteTestDeclarations(files []RemoteTestFile, environment RemoteTestEnvironment, buildTags []string) ([]string, []string, error) {
	names := make([]string, 0)
	inputs := make([]string, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		applicable, err := remoteTestFileApplies(file, environment, buildTags)
		if err != nil {
			return nil, nil, err
		}
		if !applicable {
			continue
		}
		inputs = append(inputs, file.Path)
		fileNames, err := remoteTestFileDeclarations(file, environment.GOOS)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %q: %w", file.Path, err)
		}
		for _, name := range fileNames {
			if seen[name] {
				return nil, nil, fmt.Errorf("applicable test %q is declared more than once", name)
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	sort.Strings(inputs)
	return names, inputs, nil
}

// remoteTestFileDeclarations 解析单个测试 blob 并验证真实顶层签名。
func remoteTestFileDeclarations(file RemoteTestFile, goos string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	included, err := GoTestDirectiveAllows(parsed.Doc, goos)
	if err != nil {
		return nil, err
	}
	if !included {
		return nil, nil
	}
	if remoteTestFileHasTestDeclaration(parsed) && !HasTestingImport(parsed) {
		return nil, errors.New("Go test declaration requires an explicit testing import")
	}
	names := make([]string, 0)
	for _, declaration := range parsed.Decls {
		name, err := remoteTestDeclarationName(declaration)
		if err != nil {
			return nil, err
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// remoteTestFileHasTestDeclaration 判断源码是否声明顶层 Test 函数。
func remoteTestFileHasTestDeclaration(file *ast.File) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name != nil && GoTestNameMatches(function.Name.Name, "Test") {
			return true
		}
	}
	return false
}

// remoteTestDeclarationName 返回一个合法顶层 Test 声明名并拒绝非法签名。
func remoteTestDeclarationName(declaration ast.Decl) (string, error) {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Recv != nil || function.Name == nil || function.Name.Name == "TestMain" || !GoTestNameMatches(function.Name.Name, "Test") {
		return "", nil
	}
	if err := ValidateGoTestFunction(function, "T"); err != nil {
		return "", err
	}
	return function.Name.Name, nil
}

func remoteTestFileApplies(file RemoteTestFile, environment RemoteTestEnvironment, buildTags []string) (bool, error) {
	context := build.Default
	context.GOOS = environment.GOOS
	context.GOARCH = environment.GOARCH
	context.CgoEnabled = environment.CGOEnabled
	context.BuildTags = slices.Clone(buildTags)
	context.OpenFile = func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(file.Source)), nil
	}
	matched, err := context.MatchFile(".", file.Name)
	if err != nil {
		return false, fmt.Errorf("match build constraints for %q: %w", file.Path, err)
	}
	return matched, nil
}

// RemoteTestSelectorsAt 从已解析候选 tree 的适用声明投影精确 package#Test 选择器。
func RemoteTestSelectorsAt(repoRoot, tree string, command []string) ([]string, error) {
	parsed, err := ParseRemoteTestCommand(command)
	if err != nil {
		return nil, err
	}
	packages, err := LoadRemoteTestPackageFilesAt(repoRoot, tree, parsed)
	if err != nil {
		return nil, err
	}
	projection, err := ProjectRemoteTestCommand(parsed, DefaultRemoteTestEnvironment(), packages)
	if err != nil {
		return nil, err
	}
	return projection.SelectorStrings(), nil
}

// SelectorStrings 返回 projection 的 canonical package#Test selectors。
func (projection RemoteTestProjection) SelectorStrings() []string {
	result := make([]string, 0, len(projection.Selectors))
	for _, selector := range projection.Selectors {
		result = append(result, selector.Package+"#"+selector.TestName)
	}
	return result
}
