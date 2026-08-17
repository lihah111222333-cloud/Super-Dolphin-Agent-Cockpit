package catalog

import (
	"errors"
	"fmt"
	"go/ast"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ParseRemoteTestCommand 严格解析唯一受支持的 go test flag 与精确测试名。
func ParseRemoteTestCommand(command []string) (RemoteTestCommand, error) {
	if len(command) < 3 || command[0] != "go" || command[1] != "test" {
		return RemoteTestCommand{}, errors.New("workload command is not a catalog-owned go test command")
	}
	parsed, runSelector, err := parseRemoteTestArguments(command[2:])
	if err != nil {
		return RemoteTestCommand{}, err
	}
	if len(parsed.Packages) == 0 {
		return RemoteTestCommand{}, errors.New("workload command contains no repository-relative Go package")
	}
	if runSelector == "" {
		return RemoteTestCommand{}, errors.New("workload command requires an explicit exact -run test selector")
	}
	names, err := remoteTestNames(runSelector)
	if err != nil {
		return RemoteTestCommand{}, err
	}
	parsed.TestNames = names
	return parsed, nil
}

// LocalHostTestCommand 将单一精确 catalog selector 投影到唯一受控宿主入口。
func LocalHostTestCommand(workload Workload) ([]string, error) {
	parsed, err := ParseRemoteTestCommand(workload.Command)
	if err != nil {
		return nil, err
	}
	if len(parsed.Packages) != 1 || len(parsed.TestNames) != 1 {
		return nil, errors.New("local host execution requires exactly one package and one test selector")
	}
	if workload.TimeoutSeconds <= 0 || workload.TimeoutSeconds > 600 {
		return nil, errors.New("local host execution requires timeout_seconds in 1..600")
	}
	class := "light"
	if workload.TimeoutSeconds > 120 {
		class = "medium"
	}
	return []string{
		"./scripts/test_with_guard.sh", "--host-test", class,
		parsed.Packages[0], "-run", "^" + parsed.TestNames[0] + "$",
		"-timeout", strconv.Itoa(workload.TimeoutSeconds) + "s", "-count", "1",
	}, nil
}

func parseRemoteTestArguments(arguments []string) (RemoteTestCommand, string, error) {
	parsed := RemoteTestCommand{}
	seenFlags := make(map[string]bool)
	runSelector := ""
	for index := 0; index < len(arguments); index++ {
		next, run, err := consumeRemoteTestArgument(arguments, index, &parsed, seenFlags)
		if err != nil {
			return RemoteTestCommand{}, "", err
		}
		if run != "" {
			runSelector = run
		}
		index = next
	}
	return parsed, runSelector, nil
}

// consumeRemoteTestArgument 消费一个 canonical 包路径或受限 flag。
func consumeRemoteTestArgument(arguments []string, index int, parsed *RemoteTestCommand, seenFlags map[string]bool) (int, string, error) {
	argument := arguments[index]
	if strings.HasPrefix(argument, "./") {
		packagePath, err := parseRemoteTestPackage(argument)
		if err != nil {
			return index, "", err
		}
		if slices.Contains(parsed.Packages, packagePath) {
			return index, "", fmt.Errorf("workload command repeats package %q", argument)
		}
		parsed.Packages = append(parsed.Packages, packagePath)
		return index, "", nil
	}
	name, value, next, err := parseRemoteTestFlag(arguments, index, seenFlags)
	if err != nil {
		return index, "", err
	}
	if name == "-run" {
		return next, value, nil
	}
	return next, "", nil
}

// parseRemoteTestPackage 校验 workload 命令中的规范相对包路径。
func parseRemoteTestPackage(argument string) (string, error) {
	if strings.ContainsAny(argument, "\\:\x00\r\n\t") {
		return "", fmt.Errorf("workload command package %q is not canonical", argument)
	}
	repositoryPath := strings.TrimPrefix(argument, "./")
	clean := path.Clean(repositoryPath)
	if repositoryPath != clean {
		return "", fmt.Errorf("workload command package %q is not canonical", argument)
	}
	if clean == "." || clean == ".." {
		return "", fmt.Errorf("workload command package %q is not canonical", argument)
	}
	if strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("workload command package %q is not canonical", argument)
	}
	return argument, nil
}

// parseRemoteTestFlag 解析唯一 flag，并校验 timeout/count/tags 边界。
func parseRemoteTestFlag(command []string, index int, seen map[string]bool) (string, string, int, error) {
	argument := command[index]
	if !strings.HasPrefix(argument, "-") {
		return "", "", index, fmt.Errorf("workload command contains an unsupported argument %q", argument)
	}
	name, value, hasValue := strings.Cut(argument, "=")
	if err := validateRemoteTestFlagName(name, seen); err != nil {
		return "", "", index, err
	}
	seen[name] = true
	value, next, err := remoteTestFlagValue(command, index, name, value, hasValue)
	if err != nil {
		return "", "", index, err
	}
	if strings.TrimSpace(value) == "" {
		if name == "-run" {
			return "", "", index, errors.New("workload command -run selector is empty")
		}
		return "", "", index, fmt.Errorf("workload command flag %s has an empty value", name)
	}
	if err := validateRemoteTestFlagValue(name, value); err != nil {
		return "", "", index, err
	}
	return name, value, next, nil
}

// validateRemoteTestFlagName 拒绝未贯通 runner 的 flag 与重复 flag。
func validateRemoteTestFlagName(name string, seen map[string]bool) error {
	if name == "-tags" || name == "-timeout" || name == "-count" {
		return fmt.Errorf("workload command flag %s is not carried by the catalog runner", name)
	}
	if !supportedRemoteTestFlag(name) {
		return fmt.Errorf("workload command contains unsupported flag %q", name)
	}
	if seen[name] {
		return fmt.Errorf("workload command repeats flag %s", name)
	}
	return nil
}

// remoteTestFlagValue 读取等号或相邻参数形式的唯一 flag 值。
func remoteTestFlagValue(command []string, index int, name, value string, hasValue bool) (string, int, error) {
	if hasValue {
		return value, index, nil
	}
	next := index + 1
	if next >= len(command) {
		return "", index, fmt.Errorf("workload command flag %s is missing a value", name)
	}
	return command[next], next, nil
}

// validateRemoteTestFlagValue 校验 catalog go test flag 的协议边界。
func validateRemoteTestFlagValue(name, value string) error {
	switch name {
	case "-count":
		if value != "1" {
			return fmt.Errorf("workload command flag -count must be exactly 1")
		}
	case "-timeout":
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("workload command flag -timeout must be a positive duration")
		}
	case "-tags":
		if err := validateRemoteTestTags(value); err != nil {
			return err
		}
	}
	return nil
}

// validateRemoteTestTags 校验 build tag token 不含路径或控制字符。
func validateRemoteTestTags(value string) error {
	for tag := range strings.SplitSeq(value, ",") {
		if !validRemoteTestTag(tag) {
			return fmt.Errorf("workload command flag -tags contains an invalid tag")
		}
	}
	return nil
}

// validRemoteTestTag 判断单个 build tag 是否符合 Go 标识符边界。
func validRemoteTestTag(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if index == 0 {
			if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
				return false
			}
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func supportedRemoteTestFlag(name string) bool {
	return name == "-run"
}

// remoteTestNames 将受限 -run 表达式展开为固定测试名。
func remoteTestNames(selector string) ([]string, error) {
	if selector == "" {
		return nil, nil
	}
	if !strings.HasPrefix(selector, "^Test") || !strings.HasSuffix(selector, "$") {
		return nil, fmt.Errorf("workload command -run selector %q is not an exact Test selector", selector)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(selector, "^Test"), "$")
	if inner == "" {
		return nil, fmt.Errorf("workload command -run selector %q is not an exact Test selector", selector)
	}
	parts, err := splitRemoteTestSelector(inner)
	if err != nil {
		return nil, fmt.Errorf("workload command -run selector %q is not an exact Test selector", selector)
	}
	result := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		name := "Test" + part
		if !canonicalRemoteTestName(name) {
			return nil, fmt.Errorf("workload command -run selector %q contains non-canonical Test name %q", selector, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("workload command -run selector %q repeats test %q", selector, name)
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

// splitRemoteTestSelector 只接受单一 Test 或括号包裹的 alternation。
func splitRemoteTestSelector(inner string) ([]string, error) {
	if inner == "" {
		return nil, errors.New("empty selector")
	}
	if strings.Contains(inner, "|") {
		if !strings.HasSuffix(inner, ")") || !strings.HasPrefix(inner, "(?:") {
			return nil, errors.New("ungrouped alternation")
		}
		inner = strings.TrimSuffix(inner, ")")
		inner = strings.TrimPrefix(inner, "(?:")
		if inner == "" || strings.ContainsAny(inner, "()") {
			return nil, errors.New("nested selector")
		}
		return strings.Split(inner, "|"), nil
	}
	if strings.ContainsAny(inner, "()|") {
		return nil, errors.New("unsupported selector syntax")
	}
	return []string{inner}, nil
}

func canonicalRemoteTestName(name string) bool {
	if name == "TestMain" {
		return false
	}
	return GoTestNameMatches(name, "Test")
}

// GoTestNameMatches 是 catalog 与 remote inventory 共享的唯一 Go 测试名称谓词。
func GoTestNameMatches(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return false
	}
	suffix := name[len(prefix):]
	first, size := utf8.DecodeRuneInString(suffix)
	return first != utf8.RuneError && size > 0 && !unicode.IsLower(first) && validGoIdentifier(suffix)
}

// validGoIdentifier 判断测试名后缀是否为严格 Go 标识符。
func validGoIdentifier(value string) bool {
	for index, character := range value {
		if index == 0 {
			if character != '_' && !unicode.IsLetter(character) {
				return false
			}
			continue
		}
		if character != '_' && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return value != ""
}

// ValidateGoTestFunction 校验 go test 接受的唯一顶层函数签名。
func ValidateGoTestFunction(function *ast.FuncDecl, argument string) error {
	if function == nil || function.Name == nil || !validGoTestFunctionShape(function) || hasGoTestTypeParameters(function) {
		if function == nil || function.Name == nil {
			return errors.New("Go test function has an invalid top-level signature")
		}
		return fmt.Errorf("Go test %q has an invalid signature", function.Name.Name)
	}
	if !goTestArgumentMatches(function.Type.Params.List[0].Type, argument) {
		return fmt.Errorf("Go test %q has an invalid signature", function.Name.Name)
	}
	return nil
}

// validGoTestFunctionShape 校验测试函数参数/返回值/receiver 形状。
func validGoTestFunctionShape(function *ast.FuncDecl) bool {
	return function != nil && function.Name != nil && function.Recv == nil && function.Type != nil &&
		(function.Type.Results == nil || len(function.Type.Results.List) == 0) && function.Type.Params != nil &&
		len(function.Type.Params.List) == 1 && len(function.Type.Params.List[0].Names) <= 1
}

func hasGoTestTypeParameters(function *ast.FuncDecl) bool {
	return function.Type != nil && function.Type.TypeParams != nil && function.Type.TypeParams.NumFields() > 0
}

// goTestArgumentMatches 校验 testing 包的精确指针参数类型。
func goTestArgumentMatches(parameter ast.Expr, argument string) bool {
	pointer, ok := parameter.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != argument {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "testing"
}

// HasTestingImport 确认测试文件显式导入标准 testing 包。
func HasTestingImport(file *ast.File) bool {
	if file == nil {
		return false
	}
	for _, importSpec := range file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err == nil && path == "testing" {
			return true
		}
	}
	return false
}

// GoTestDirectiveAllows 应用 inventory 与 catalog projection 共享的源码 directive 策略。
func GoTestDirectiveAllows(comments *ast.CommentGroup, goos string) (bool, error) {
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
	case directive == "helper", directive == "local-only":
		return false, nil
	case directive == "compile-group-exclusive":
		return true, nil
	case strings.HasPrefix(directive, "platform="):
		platform := strings.TrimPrefix(directive, "platform=")
		switch platform {
		case "darwin", "linux", "windows":
			return platform == goos, nil
		default:
			return false, fmt.Errorf("unsupported platform directive %q", directive)
		}
	default:
		return false, fmt.Errorf("unsupported directive %q", directive)
	}
}
