package remoteci

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

// remoteGoTestPlatform 校验并拆分远程执行平台。
func remoteGoTestPlatform(platform string) (string, string, error) {
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
		return "", "", errors.New("remote Go test inventory platform is invalid")
	}
	return goos, goarch, nil
}

// remoteGoPackageTestInventory 返回精确包中可执行的顶层测试名称。
func (snapshot *remoteGitTreeSnapshot) remoteGoPackageTestInventory(
	directory string,
	goos string,
	goarch string,
	race bool,
) ([]string, error) {
	files, err := snapshot.remoteGoPackageTestFiles(directory, goos, goarch, race)
	if err != nil {
		return nil, err
	}
	return goTestNamesFromFiles(files, goos)
}

// remoteGoPackageMatchesPlatform 判断目录是否至少包含一个目标平台可构建的 Go 文件。
func (snapshot *remoteGitTreeSnapshot) remoteGoPackageMatchesPlatform(
	directory string,
	goos string,
	goarch string,
	race bool,
) (bool, error) {
	buildContext := snapshot.remoteGoBuildContext(goos, goarch, race)
	for filePath := range snapshot.goSources {
		if path.Dir(filePath) != directory || path.Ext(filePath) != ".go" {
			continue
		}
		matched, err := buildContext.MatchFile(directory, path.Base(filePath))
		if err != nil {
			return false, fmt.Errorf("match Go source %q: %w", filePath, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func (snapshot *remoteGitTreeSnapshot) remoteGoBuildContext(goos string, goarch string, race bool) build.Context {
	buildContext := build.Default
	buildContext.GOOS = goos
	buildContext.GOARCH = goarch
	buildContext.CgoEnabled = false
	buildContext.UseAllFiles = false
	if race {
		buildContext.BuildTags = append(buildContext.BuildTags, "race")
	}
	buildContext.JoinPath = path.Join
	buildContext.OpenFile = func(name string) (io.ReadCloser, error) {
		source, ok := snapshot.goSources[filepath.ToSlash(filepath.Clean(name))]
		if !ok {
			return nil, fmt.Errorf("Go test inventory source %q is absent", name)
		}
		return io.NopCloser(bytes.NewReader(source)), nil
	}
	return buildContext
}

// remoteGoPackageTestFiles 解析目标平台与标签下参与测试的文件。
func (snapshot *remoteGitTreeSnapshot) remoteGoPackageTestFiles(
	directory string,
	goos string,
	goarch string,
	race bool,
) ([]*ast.File, error) {
	buildContext := snapshot.remoteGoBuildContext(goos, goarch, race)

	var files []*ast.File
	for filePath, source := range snapshot.goSources {
		if path.Dir(filePath) != directory || !strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		matched, err := buildContext.MatchFile(directory, path.Base(filePath))
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// goTestNamesFromFiles 收集目标平台可执行的测试、模糊测试和带输出示例。
func goTestNamesFromFiles(files []*ast.File, goos string) ([]string, error) {
	if strings.TrimSpace(goos) == "" {
		return nil, errors.New("remote Go test inventory GOOS is required")
	}
	seen := make(map[string]struct{})
	for _, file := range files {
		included, err := remoteGoTestFileIncluded(file, goos)
		if err != nil {
			return nil, err
		}
		if !included {
			continue
		}
		if err := addGoTestDeclarations(seen, file.Decls, goos); err != nil {
			return nil, err
		}
		for _, example := range doc.Examples(file) {
			if example.Output == "" && !example.EmptyOutput {
				continue
			}
			if err := addRemoteGoTestName(seen, "Example"+example.Name); err != nil {
				return nil, err
			}
		}
	}
	return sortedRemoteGoTestNames(seen), nil
}

func remoteGoTestFileIncluded(file *ast.File, goos string) (bool, error) {
	included, err := catalog.GoTestDirectiveAllows(file.Doc, goos)
	if err != nil {
		return false, fmt.Errorf("Go test file directive: %w", err)
	}
	if included && remoteGoTestFileHasDeclaration(file) && !catalog.HasTestingImport(file) {
		return false, errors.New("Go test declaration requires an explicit testing import")
	}
	return included, nil
}

// remoteGoTestFileHasDeclaration 判断文件是否包含顶层 Test 声明。
func remoteGoTestFileHasDeclaration(file *ast.File) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name != nil && catalog.GoTestNameMatches(function.Name.Name, "Test") {
			return true
		}
	}
	return false
}

// addGoTestDeclarations 校验并记录文件中的顶层测试声明。
func addGoTestDeclarations(seen map[string]struct{}, declarations []ast.Decl, goos string) error {
	for _, declaration := range declarations {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		if err := addGoTestFunction(seen, function, goos); err != nil {
			return err
		}
	}
	return nil
}

// addGoTestFunction 校验测试函数，并仅记录可由 go test 枚举的名称。
func addGoTestFunction(seen map[string]struct{}, function *ast.FuncDecl, goos string) error {
	name := function.Name.Name
	switch {
	case name == "TestMain":
		return validateGoTestFunction(function, "M")
	case catalog.GoTestNameMatches(name, "Test"):
		if err := catalog.ValidateGoTestFunction(function, "T"); err != nil {
			return err
		}
	case catalog.GoTestNameMatches(name, "Fuzz"):
		if err := catalog.ValidateGoTestFunction(function, "F"); err != nil {
			return err
		}
	default:
		return nil
	}
	included, err := catalog.GoTestDirectiveAllows(function.Doc, goos)
	if err != nil {
		return fmt.Errorf("Go test %q directive: %w", name, err)
	}
	if !included {
		return nil
	}
	return addRemoteGoTestName(seen, name)
}

// remoteGoTestDirectiveAllows 保留同包兼容符号，但策略唯一归 catalog 所有。
func remoteGoTestDirectiveAllows(comments *ast.CommentGroup, goos string) (bool, error) {
	return catalog.GoTestDirectiveAllows(comments, goos)
}

// sortedRemoteGoTestNames 将去重后的测试名称按字典序返回。
func sortedRemoteGoTestNames(seen map[string]struct{}) []string {
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// goTestNameMatches 保留同包兼容符号，但策略唯一归 catalog 所有。
func goTestNameMatches(name string, prefix string) bool {
	return catalog.GoTestNameMatches(name, prefix)
}

// validateGoTestFunction 保留同包兼容符号，但策略唯一归 catalog 所有。
func validateGoTestFunction(function *ast.FuncDecl, argument string) error {
	return catalog.ValidateGoTestFunction(function, argument)
}

// addRemoteGoTestName 将名称写入集合，并拒绝重复的可执行测试。
func addRemoteGoTestName(seen map[string]struct{}, name string) error {
	if _, duplicate := seen[name]; duplicate {
		return fmt.Errorf("Go test inventory repeats %q", name)
	}
	seen[name] = struct{}{}
	return nil
}
