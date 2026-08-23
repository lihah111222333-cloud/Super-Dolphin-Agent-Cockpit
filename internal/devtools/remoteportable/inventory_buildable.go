package remoteportable

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

type portableGoPackageCandidate struct {
	hasBuildableFile bool
	invalid          bool
}

// portableGoSource 保留 exact tree 中 Go blob 的内容，供标准构建约束和 embed 解析共同使用。
type portableGoSource struct {
	entry      GitTreeEntry
	data       []byte
	moduleRoot string
}

// portableGoBuildContext 使用 exact-tree 内存 blob 实现 go/build 的文件选择，不接触宿主文件系统。
func portableGoBuildContext(platform string, sources map[string]portableGoSource) (build.Context, error) {
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok || goos == "" || goarch == "" {
		return build.Context{}, errors.New("portable Go inventory platform is invalid")
	}
	context := build.Default
	context.GOOS = goos
	context.GOARCH = goarch
	context.Compiler = "gc"
	context.CgoEnabled = false
	context.BuildTags = nil
	context.JoinPath = path.Join
	context.OpenFile = func(filePath string) (io.ReadCloser, error) {
		clean := path.Clean(filePath)
		source, ok := sources[clean]
		if !ok {
			return nil, fmt.Errorf("portable exact tree Go blob %q is missing", clean)
		}
		return io.NopCloser(bytes.NewReader(source.data)), nil
	}
	return context, nil
}

// portableGoSourceCandidate 将一个标准构建约束匹配的 source 投影为包候选状态。
func portableGoSourceCandidate(buildContext build.Context, source portableGoSource, entries []GitTreeEntry, moduleDirs, excludedNestedModules []string) (string, portableGoPackageCandidate, bool, error) {
	directory := path.Dir(source.entry.Path)
	matched, err := buildContext.MatchFile(directory, path.Base(source.entry.Path))
	if err != nil {
		return "", portableGoPackageCandidate{}, false, fmt.Errorf("match portable Go source %q: %w", source.entry.Path, err)
	}
	if !matched {
		return "", portableGoPackageCandidate{}, false, nil
	}
	buildable, invalid, err := portableGoSourceBuildability(source, entries, moduleDirs, excludedNestedModules)
	if err != nil {
		return "", portableGoPackageCandidate{}, false, err
	}
	return portableGoPackageKey(directory, source.moduleRoot), portableGoPackageCandidate{
		hasBuildableFile: buildable,
		invalid:          invalid,
	}, true, nil
}

// portableGoPackageKey 生成 module root 与 module-relative package target 的稳定组合键。
func portableGoPackageKey(directory, moduleRoot string) string {
	relative := strings.TrimPrefix(directory, moduleRoot)
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		return moduleRoot + "\x00."
	}
	return moduleRoot + "\x00./" + relative
}

// portableGoSourceBuildability 解析 Go blob 的 cgo/embed 可构建性；unresolved embed 会使整包无效。
func portableGoSourceBuildability(source portableGoSource, entries []GitTreeEntry, moduleDirs, excludedNestedModules []string) (bool, bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), source.entry.Path, source.data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return false, false, fmt.Errorf("parse portable Go source %q: %w", source.entry.Path, err)
	}
	if portableGoImportsPath(file, "C") {
		return false, false, nil
	}
	patterns, hasEmbed, importsEmbed, err := portableGoEmbedPatterns(file)
	if err != nil {
		return false, false, fmt.Errorf("parse portable Go embed directives in %q: %w", source.entry.Path, err)
	}
	if !hasEmbed {
		return true, false, nil
	}
	if !importsEmbed || len(patterns) == 0 {
		return false, true, nil
	}
	packageDir := path.Dir(source.entry.Path)
	for _, pattern := range patterns {
		matched, err := portableGoEmbedPatternMatches(packageDir, pattern, entries, moduleDirs, source.moduleRoot, excludedNestedModules)
		if err != nil {
			return false, false, fmt.Errorf("validate portable Go embed pattern %q in %q: %w", pattern, source.entry.Path, err)
		}
		if !matched {
			return false, true, nil
		}
	}
	return true, false, nil
}

// portableGoEmbedPatterns 提取 Go AST 中的 embed directives 和 embed import。
func portableGoEmbedPatterns(file *ast.File) ([]string, bool, bool, error) {
	patterns := make([]string, 0)
	hasEmbed := false
	for _, commentGroup := range file.Comments {
		for _, comment := range commentGroup.List {
			directive, ok := ast.ParseDirective(comment.Slash, comment.Text)
			if !ok || directive.Tool != "go" || directive.Name != "embed" {
				continue
			}
			hasEmbed = true
			args, err := directive.ParseArgs()
			if err != nil {
				return nil, true, false, err
			}
			for _, arg := range args {
				patterns = append(patterns, arg.Arg)
			}
		}
	}
	importsEmbed := portableGoImportsPath(file, "embed")
	return patterns, hasEmbed, importsEmbed, nil
}

func portableGoImportsPath(file *ast.File, target string) bool {
	for _, importSpec := range file.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err == nil && importPath == target {
			return true
		}
	}
	return false
}

// portableGoEmbedPatternMatches 在 exact tree 中解析一个 embed pattern；目录匹配会递归收集文件。
func portableGoEmbedPatternMatches(packageDir, pattern string, entries []GitTreeEntry, moduleDirs []string, moduleRoot string, excludedNestedModules []string) (bool, error) {
	glob, all, err := portableGoEmbedGlob(pattern)
	if err != nil {
		return false, err
	}
	prefix := portableGoEmbedPackagePrefix(packageDir)
	for _, entry := range entries {
		relative, eligible := portableGoEmbedRelativePath(entry, prefix, moduleDirs, moduleRoot, excludedNestedModules)
		if !eligible {
			continue
		}
		matched, err := portableGoEmbedRelativeMatches(relative, glob, all)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// portableGoEmbedGlob 校验 embed pattern 并分离 all 前缀语义。
func portableGoEmbedGlob(pattern string) (string, bool, error) {
	glob, all := strings.CutPrefix(pattern, "all:")
	if !all {
		glob = pattern
	}
	if glob == "" || glob == "." || !fs.ValidPath(glob) {
		return "", false, errors.New("portable Go embed pattern is invalid")
	}
	if _, err := path.Match(glob, ""); err != nil {
		return "", false, err
	}
	return glob, all, nil
}

func portableGoEmbedPackagePrefix(packageDir string) string {
	if packageDir == "." {
		return ""
	}
	return packageDir + "/"
}

// portableGoEmbedRelativePath 拒绝包外、excluded subtree 和其他 declared module 中的候选 entry。
func portableGoEmbedRelativePath(entry GitTreeEntry, prefix string, moduleDirs []string, moduleRoot string, excludedNestedModules []string) (string, bool) {
	if entry.Kind != "blob" {
		return "", false
	}
	if !strings.HasPrefix(entry.Path, prefix) {
		return "", false
	}
	if portablePathUnderAnyExcludedModule(path.Dir(entry.Path), excludedNestedModules) {
		return "", false
	}
	if portablePathUnderOtherDeclaredModule(entry.Path, moduleDirs, moduleRoot) {
		return "", false
	}
	relative := strings.TrimPrefix(entry.Path, prefix)
	if relative == "" || strings.Contains(relative, "\x00") {
		return "", false
	}
	return relative, true
}

// portableGoEmbedRelativeMatches 匹配文件本身或其祖先目录，并保留 all 对隐藏后代的语义。
func portableGoEmbedRelativeMatches(relative, glob string, all bool) (bool, error) {
	matched, err := path.Match(glob, relative)
	if err != nil || matched {
		return matched, err
	}
	for directory := path.Dir(relative); directory != "."; directory = path.Dir(directory) {
		matched, err := path.Match(glob, directory)
		if err != nil {
			return false, err
		}
		if matched && (all || portableGoEmbedDescendantIsVisible(relative, directory)) {
			return true, nil
		}
	}
	return false, nil
}

// portablePathUnderOtherDeclaredModule 判断文件是否落入当前 module root 之外的 declared module。
func portablePathUnderOtherDeclaredModule(filePath string, moduleDirs []string, moduleRoot string) bool {
	for _, module := range moduleDirs {
		if module == "." || module == moduleRoot {
			continue
		}
		if filePath == module || strings.HasPrefix(filePath, module+"/") {
			return true
		}
	}
	return false
}

func portableGoEmbedDescendantIsVisible(relative, directory string) bool {
	suffix := strings.TrimPrefix(relative, directory+"/")
	for _, component := range strings.Split(suffix, "/") {
		if component != "" && (strings.HasPrefix(component, ".") || strings.HasPrefix(component, "_")) {
			return false
		}
	}
	return true
}
