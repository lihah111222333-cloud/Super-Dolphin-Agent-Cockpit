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
	"unicode"
	"unicode/utf8"
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
	buildContext.CgoEnabled = true
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
		included, err := remoteGoTestDirectiveAllows(file.Doc, goos)
		if err != nil {
			return nil, fmt.Errorf("Go test file directive: %w", err)
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
	case goTestNameMatches(name, "Test"):
		if err := validateGoTestFunction(function, "T"); err != nil {
			return err
		}
	case goTestNameMatches(name, "Fuzz"):
		if err := validateGoTestFunction(function, "F"); err != nil {
			return err
		}
	default:
		return nil
	}
	included, err := remoteGoTestDirectiveAllows(function.Doc, goos)
	if err != nil {
		return fmt.Errorf("Go test %q directive: %w", name, err)
	}
	if !included {
		return nil
	}
	return addRemoteGoTestName(seen, name)
}

// remoteGoTestDirectiveAllows 应用源码就地声明的平台或 helper 盘点边界。
func remoteGoTestDirectiveAllows(comments *ast.CommentGroup, goos string) (bool, error) {
	const prefix = "super-dolphin-ci:"
	directive := ""
	if comments != nil {
		for _, comment := range comments.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if !strings.HasPrefix(text, prefix) {
				continue
			}
			if directive != "" {
				return false, errors.New("multiple super-dolphin-ci directives are not allowed")
			}
			directive = strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	switch {
	case directive == "":
		return true, nil
	case directive == "helper":
		return false, nil
	case strings.HasPrefix(directive, "platform="):
		platform := strings.TrimPrefix(directive, "platform=")
		switch platform {
		case "darwin", "linux", "windows":
			return platform == goos, nil
		default:
			return false, fmt.Errorf("unsupported platform directive %q", platform)
		}
	default:
		return false, fmt.Errorf("unsupported directive %q", directive)
	}
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

// goTestNameMatches 判断名称是否符合 Go 测试前缀与大小写规则。
func goTestNameMatches(name string, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(first)
}

// validateGoTestFunction 校验测试函数的参数、返回值和泛型约束。
func validateGoTestFunction(function *ast.FuncDecl, argument string) error {
	if hasGoTestTypeParameters(function) {
		return fmt.Errorf("Go test %q cannot have type parameters", function.Name.Name)
	}
	if !hasValidGoTestSignatureShape(function) {
		return fmt.Errorf("Go test %q has an invalid signature", function.Name.Name)
	}
	if goTestArgumentMatches(function.Type.Params.List[0].Type, argument) {
		return nil
	}
	return fmt.Errorf("Go test %q has an invalid signature", function.Name.Name)
}

// hasGoTestTypeParameters 判断测试函数是否声明了泛型参数。
func hasGoTestTypeParameters(function *ast.FuncDecl) bool {
	return function.Type.TypeParams != nil && function.Type.TypeParams.NumFields() > 0
}

// hasValidGoTestSignatureShape 判断测试函数的返回值与参数个数是否合法。
func hasValidGoTestSignatureShape(function *ast.FuncDecl) bool {
	if function.Type.Results != nil && len(function.Type.Results.List) > 0 {
		return false
	}
	if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	return len(function.Type.Params.List[0].Names) <= 1
}

// goTestArgumentMatches 判断唯一参数是否为指定 testing 类型的指针。
func goTestArgumentMatches(parameter ast.Expr, argument string) bool {
	pointer, ok := parameter.(*ast.StarExpr)
	if !ok {
		return false
	}
	if identifier, ok := pointer.X.(*ast.Ident); ok {
		return identifier.Name == argument
	}
	if selector, ok := pointer.X.(*ast.SelectorExpr); ok {
		return selector.Sel.Name == argument
	}
	return false
}

// addRemoteGoTestName 将名称写入集合，并拒绝重复的可执行测试。
func addRemoteGoTestName(seen map[string]struct{}, name string) error {
	if _, duplicate := seen[name]; duplicate {
		return fmt.Errorf("Go test inventory repeats %q", name)
	}
	seen[name] = struct{}{}
	return nil
}
