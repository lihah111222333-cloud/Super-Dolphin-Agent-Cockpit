package gate

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/mod/modfile"
)

// CandidateObjectAuthority is an opaque, validated private candidate ODB
// proof. It is allowed only in the receipt's exact-object read boundary.
type CandidateObjectAuthority = gateprivate.CandidateObjectAuthority

// CaptureCandidateObjectAuthority consumes the candidate resolver's allowed
// object-routing environment once, before trusted Git clears the environment.
func CaptureCandidateObjectAuthority() (CandidateObjectAuthority, error) {
	return gateprivate.CaptureCandidateObjectAuthority()
}

// canonicalReceiptRepositoryRoot 规范化并验证 receipt 读取的仓库根目录。
func canonicalReceiptRepositoryRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("local executor receipt repository root must be a canonical absolute path")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve local executor receipt repository root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("local executor receipt repository root must be a directory")
	}
	return resolved, nil
}

func validLocalTreeObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func verifyGitTreeObject(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, expected string) (string, error) {
	command, err := localReceiptTrustedGitCommand(ctx, trustedGit, repositoryRoot, "rev-parse", "--verify", expected+"^{tree}")
	if err != nil {
		return "", err
	}
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("verify local executor receipt exact tree: %w", err)
	}
	actual := strings.TrimSpace(string(output))
	if actual != expected {
		return "", fmt.Errorf("local executor receipt exact tree %q resolved to %q", expected, actual)
	}
	return actual, nil
}

func gitTreeBlob(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, path string) ([]byte, error) {
	command, err := localReceiptTrustedGitCommand(ctx, trustedGit, repositoryRoot, "show", tree+":"+path)
	if err != nil {
		return nil, err
	}
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read exact tree file %q: %w", path, err)
	}
	return output, nil
}

type gitTreeRegularFile struct {
	path       string
	content    []byte
	executable bool
}

// gitTreeRegularFiles 从 exact tree 读取可安全物化的普通文件，拒绝链接、子模块和路径逃逸。
func gitTreeRegularFiles(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, relativeRoot string) ([]gitTreeRegularFile, error) {
	if _, err := localReceiptJoinRelativePath("/", relativeRoot); err != nil {
		return nil, err
	}
	command, err := localReceiptTrustedGitCommand(ctx, trustedGit, repositoryRoot, "ls-tree", "-r", "-z", "--full-tree", tree, "--", relativeRoot)
	if err != nil {
		return nil, err
	}
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list exact tree files: %w", err)
	}
	entries, err := parseGitTreeRegularFileEntries(output, relativeRoot)
	if err != nil {
		return nil, err
	}
	files := make([]gitTreeRegularFile, 0, len(entries))
	for _, entry := range entries {
		content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, entry.path)
		if err != nil {
			return nil, err
		}
		files = append(files, gitTreeRegularFile{path: entry.path, content: content, executable: entry.executable})
	}
	return files, nil
}

type gitTreeRegularFileEntry struct {
	path       string
	executable bool
}

func parseGitTreeRegularFileEntries(output []byte, relativeRoot string) ([]gitTreeRegularFileEntry, error) {
	entries := make([]gitTreeRegularFileEntry, 0)
	for raw := range strings.SplitSeq(string(output), "\x00") {
		if raw == "" {
			continue
		}
		entry, err := parseGitTreeRegularFileEntry(raw, relativeRoot)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("exact tree local replacement %q is empty or missing", relativeRoot)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	return entries, nil
}

// parseGitTreeRegularFileEntry 校验 Git ls-tree 记录仅指向指定 replace 子树的常规 blob。
func parseGitTreeRegularFileEntry(raw, relativeRoot string) (gitTreeRegularFileEntry, error) {
	metadata, filePath, found := strings.Cut(raw, "\t")
	if !found {
		return gitTreeRegularFileEntry{}, fmt.Errorf("exact tree file entry %q is malformed", raw)
	}
	fields := strings.Fields(metadata)
	if len(fields) != 3 || fields[1] != "blob" {
		return gitTreeRegularFileEntry{}, fmt.Errorf("exact tree file %q has unsupported mode/type", filePath)
	}
	executable, err := gitTreeFileExecutable(fields[0], filePath)
	if err != nil {
		return gitTreeRegularFileEntry{}, err
	}
	if _, err := localReceiptJoinRelativePath("/", filePath); err != nil {
		return gitTreeRegularFileEntry{}, err
	}
	if filePath != relativeRoot && !strings.HasPrefix(filePath, relativeRoot+"/") {
		return gitTreeRegularFileEntry{}, fmt.Errorf("exact tree file %q escaped local replacement %q", filePath, relativeRoot)
	}
	return gitTreeRegularFileEntry{path: filePath, executable: executable}, nil
}

func gitTreeFileExecutable(mode, filePath string) (bool, error) {
	switch mode {
	case "100644":
		return false, nil
	case "100755":
		return true, nil
	default:
		return false, fmt.Errorf("exact tree file %q has unsupported mode/type", filePath)
	}
}

func localReceiptJoinRelativePath(root, value string) (string, error) {
	clean, err := localReceiptRelativePath(value)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, joined)
	if err != nil {
		return "", fmt.Errorf("local receipt path %q escapes its root", value)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("local receipt path %q escapes its root", value)
	}
	return joined, nil
}

// localReceiptRelativePath 规范化相对路径并在写入或重验前阻断绝对路径和父目录逃逸。
func localReceiptRelativePath(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") {
		return "", fmt.Errorf("local receipt path %q is invalid", value)
	}
	if path.IsAbs(value) || filepath.IsAbs(value) {
		return "", fmt.Errorf("local receipt path %q is invalid", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("local receipt path %q escapes its root", value)
	}
	return clean, nil
}

// verifyLocalReceiptLockFiles 验证本次 workload 所需的精确树依赖锁文件存在。
func verifyLocalReceiptLockFiles(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, programs map[GateID]ExecutorProgram) error {
	needsGo, needsFrontend := false, false
	for _, program := range programs {
		needsGo = needsGo || program.NeedsGoSeed
		needsFrontend = needsFrontend || program.NeedsFrontendSeed
	}
	if needsGo {
		if err := verifyLocalReceiptLockSet(ctx, trustedGit, repositoryRoot, tree, []string{"go.mod", "go.sum"}, "local Go dependency lock"); err != nil {
			return err
		}
	}
	if needsFrontend {
		if err := verifyLocalReceiptLockSet(ctx, trustedGit, repositoryRoot, tree, []string{"frontend-app/package.json", "frontend-app/package-lock.json"}, "local frontend dependency lock"); err != nil {
			return err
		}
	}
	return nil
}

func verifyLocalReceiptLockSet(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, paths []string, label string) error {
	for _, path := range paths {
		if _, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, path); err != nil {
			return fmt.Errorf("%s %q is required: %w", label, path, err)
		}
	}
	return nil
}

func readLocalReceiptRunnerSources(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string) ([]localExecutorSourceProof, error) {
	paths, err := gitTreeLocalRunnerSourcePaths(ctx, trustedGit, repositoryRoot, tree)
	if err != nil {
		return nil, err
	}
	proofs := make([]localExecutorSourceProof, 0, len(paths))
	for _, path := range paths {
		content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, path)
		if err != nil {
			return nil, fmt.Errorf("read local runner source closure %q: %w", path, err)
		}
		proofs = append(proofs, localExecutorSourceProof{path: path, digest: digestBytes(content)})
	}
	return proofs, nil
}

const localRunnerSemanticEntryPackage = "cmd/super-dolphin-gate"

// gitTreeLocalRunnerSourcePaths 从 canonical local CLI 入口解析 exact Git tree
// 中可达的 Go package 和 go:embed asset 闭包。树、工作区路径和 commit 只用于读取
// 对象，绝不进入 receipt 或 environment identity payload。
func gitTreeLocalRunnerSourcePaths(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string) ([]string, error) {
	modulePath, err := gitTreeModulePath(ctx, trustedGit, repositoryRoot, tree)
	if err != nil {
		return nil, err
	}
	closure := newLocalRunnerSourceClosure(modulePath)
	for closure.hasPending() {
		if err := closure.visitNextPackage(ctx, trustedGit, repositoryRoot, tree); err != nil {
			return nil, err
		}
	}
	if len(closure.paths) == 0 {
		return nil, errors.New("local runner source closure is empty")
	}
	owners := make(map[string]localRunnerClosurePathOwner, len(closure.paths))
	paths := make([]string, 0, len(closure.paths))
	for _, sourcePath := range closure.paths {
		inputs, err := localRunnerClosureSourceInputs(ctx, trustedGit, repositoryRoot, tree, sourcePath)
		if err != nil {
			return nil, err
		}
		for _, input := range inputs {
			paths, err = appendLocalRunnerClosurePath(paths, owners, input.path, input.content, input.owner)
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

type localRunnerClosurePathInput struct {
	path    string
	content []byte
	owner   string
}

// localRunnerClosureSourceInputs returns one source and its exact-tree embed
// inputs without allowing worktree paths into the closure payload.
func localRunnerClosureSourceInputs(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, sourcePath string) ([]localRunnerClosurePathInput, error) {
	content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("read local runner embed source %q: %w", sourcePath, err)
	}
	inputs := []localRunnerClosurePathInput{{path: sourcePath, content: content, owner: "production source"}}
	assets, err := gitTreeLocalRunnerEmbedPaths(ctx, trustedGit, repositoryRoot, tree, sourcePath, content)
	if err != nil {
		return nil, err
	}
	for _, assetPath := range assets {
		assetContent, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, assetPath)
		if err != nil {
			return nil, fmt.Errorf("read local runner embed asset %q: %w", assetPath, err)
		}
		inputs = append(inputs, localRunnerClosurePathInput{path: assetPath, content: assetContent, owner: "go:embed asset"})
	}
	return inputs, nil
}

type localRunnerEmbedPattern struct {
	value         string
	includeHidden bool
}

// gitTreeLocalRunnerEmbedPaths 只根据 Git tree 对象解析每个嵌入指令，
// 绝不在调用方的工作树中展开模式。
func gitTreeLocalRunnerEmbedPaths(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, sourcePath string, content []byte) ([]string, error) {
	patterns, err := localRunnerEmbedPatterns(sourcePath, content)
	if err != nil || len(patterns) == 0 {
		return nil, err
	}
	packagePath := path.Dir(sourcePath)
	entries, err := gitTreeLocalRunnerPackageEntries(ctx, trustedGit, repositoryRoot, tree, packagePath)
	if err != nil {
		return nil, err
	}
	assets := make(map[string]struct{})
	for _, pattern := range patterns {
		matchedAssets, err := localRunnerEmbedPatternAssets(entries, pattern, packagePath, sourcePath)
		if err != nil {
			return nil, err
		}
		for _, assetPath := range matchedAssets {
			assets[assetPath] = struct{}{}
		}
	}
	paths := make([]string, 0, len(assets))
	for assetPath := range assets {
		paths = append(paths, assetPath)
	}
	sort.Strings(paths)
	return paths, nil
}

type localRunnerClosurePathOwner struct {
	digest string
	owner  string
}

// appendLocalRunnerClosurePath keeps one canonical path for equal exact-tree
// blobs, but rejects an impossible same-path content collision rather than
// silently accepting changed semantic material.
func appendLocalRunnerClosurePath(paths []string, owners map[string]localRunnerClosurePathOwner, candidatePath string, content []byte, owner string) ([]string, error) {
	digest := digestBytes(content)
	if existing, found := owners[candidatePath]; found {
		if existing.digest != digest {
			return nil, fmt.Errorf("local runner closure path %q content collision between %s and %s", candidatePath, existing.owner, owner)
		}
		return paths, nil
	}
	owners[candidatePath] = localRunnerClosurePathOwner{digest: digest, owner: owner}
	return append(paths, candidatePath), nil
}

// localRunnerEmbedPatternAssets 从单个模式收集其匹配的常规 Git blob。
func localRunnerEmbedPatternAssets(entries []string, pattern localRunnerEmbedPattern, packagePath, sourcePath string) ([]string, error) {
	assets := make([]string, 0)
	for _, raw := range entries {
		entry, err := parseLocalRunnerEmbedTreeEntry(raw, packagePath)
		if err != nil {
			return nil, fmt.Errorf("local runner embed source %q: %w", sourcePath, err)
		}
		selected, err := localRunnerEmbedPatternMatches(pattern, entry.path, packagePath)
		if err != nil {
			return nil, fmt.Errorf("local runner embed source %q: %w", sourcePath, err)
		}
		if !selected {
			continue
		}
		if entry.objectType != "blob" {
			return nil, fmt.Errorf("local runner embed asset %q is not a regular blob", entry.path)
		}
		if _, err := gitTreeFileExecutable(entry.mode, entry.path); err != nil {
			return nil, fmt.Errorf("local runner embed asset %q is not a regular blob: %w", entry.path, err)
		}
		assets = append(assets, entry.path)
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("local runner embed source %q pattern %q matched no regular blobs", sourcePath, pattern.value)
	}
	return assets, nil
}

type localRunnerEmbedTreeEntry struct {
	mode       string
	objectType string
	path       string
}

// parseLocalRunnerEmbedTreeEntry 解析 package 内单个 Git tree 条目，并拒绝逃逸路径。
func parseLocalRunnerEmbedTreeEntry(raw, packagePath string) (localRunnerEmbedTreeEntry, error) {
	metadata, sourcePath, found := strings.Cut(raw, "\t")
	if !found {
		return localRunnerEmbedTreeEntry{}, fmt.Errorf("tree entry %q is malformed", raw)
	}
	if _, err := localReceiptRelativePath(sourcePath); err != nil {
		return localRunnerEmbedTreeEntry{}, fmt.Errorf("asset path %q: %w", sourcePath, err)
	}
	relativePath, err := localRunnerEmbedRelativePath(sourcePath, packagePath)
	if err != nil {
		return localRunnerEmbedTreeEntry{}, err
	}
	if relativePath == "" {
		return localRunnerEmbedTreeEntry{}, fmt.Errorf("asset path %q is a package directory", sourcePath)
	}
	fields := strings.Fields(metadata)
	if len(fields) != 3 {
		return localRunnerEmbedTreeEntry{}, fmt.Errorf("asset %q has malformed tree metadata", sourcePath)
	}
	return localRunnerEmbedTreeEntry{mode: fields[0], objectType: fields[1], path: sourcePath}, nil
}

// localRunnerEmbedPatterns 从 Go 注释抽取并验证 go:embed 模式。
func localRunnerEmbedPatterns(sourcePath string, content []byte) ([]localRunnerEmbedPattern, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse local runner embed source %q: %w", sourcePath, err)
	}
	patterns := make([]localRunnerEmbedPattern, 0)
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			directivePatterns, matched, err := localRunnerEmbedCommentPatterns(sourcePath, comment.Text)
			if err != nil {
				return nil, err
			}
			if matched {
				patterns = append(patterns, directivePatterns...)
			}
		}
	}
	return patterns, nil
}

// localRunnerEmbedCommentPatterns 解析单条可能的 go:embed 注释。
func localRunnerEmbedCommentPatterns(sourcePath, comment string) ([]localRunnerEmbedPattern, bool, error) {
	if !strings.HasPrefix(comment, "//go:embed") {
		return nil, false, nil
	}
	rest := strings.TrimPrefix(comment, "//go:embed")
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return nil, false, nil
	}
	values, err := splitLocalRunnerEmbedPatterns(strings.TrimSpace(rest))
	if err != nil {
		return nil, true, fmt.Errorf("parse local runner embed directive in %q: %w", sourcePath, err)
	}
	patterns := make([]localRunnerEmbedPattern, 0, len(values))
	for _, value := range values {
		pattern, err := parseLocalRunnerEmbedPattern(value)
		if err != nil {
			return nil, true, fmt.Errorf("parse local runner embed directive in %q: %w", sourcePath, err)
		}
		patterns = append(patterns, pattern)
	}
	return patterns, true, nil
}

// splitLocalRunnerEmbedPatterns 按 go:embed 语法拆分普通和带引号的模式。
func splitLocalRunnerEmbedPatterns(value string) ([]string, error) {
	patterns := make([]string, 0)
	for value != "" {
		value = strings.TrimLeft(value, " \t")
		if value == "" {
			break
		}
		pattern, remaining, err := splitLocalRunnerEmbedPattern(value)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
		value = remaining
	}
	if len(patterns) == 0 {
		return nil, errors.New("go:embed directive has no patterns")
	}
	return patterns, nil
}

// splitLocalRunnerEmbedPattern 读取一个 go:embed 模式并返回余下输入。
func splitLocalRunnerEmbedPattern(value string) (string, string, error) {
	if value[0] != 0x60 && value[0] != '"' {
		pattern, remaining := splitLocalRunnerEmbedUnquotedPattern(value)
		return pattern, remaining, nil
	}
	return splitLocalRunnerEmbedQuotedPattern(value)
}

// splitLocalRunnerEmbedUnquotedPattern 读取一个以空白结束的普通模式。
func splitLocalRunnerEmbedUnquotedPattern(value string) (string, string) {
	end := strings.IndexAny(value, " \t")
	if end < 0 {
		return value, ""
	}
	return value[:end], value[end:]
}

// splitLocalRunnerEmbedQuotedPattern 解码一个带反引号或双引号的模式。
func splitLocalRunnerEmbedQuotedPattern(value string) (string, string, error) {
	end, err := localRunnerEmbedQuotedPatternEnd(value)
	if err != nil {
		return "", "", err
	}
	decoded, err := strconv.Unquote(value[:end+1])
	if err != nil {
		return "", "", fmt.Errorf("decode quoted pattern: %w", err)
	}
	return decoded, value[end+1:], nil
}

// localRunnerEmbedQuotedPatternEnd 查找带引号模式的闭合位置。
func localRunnerEmbedQuotedPatternEnd(value string) (int, error) {
	quote := value[0]
	escaped := false
	for end := 1; end < len(value); end++ {
		if quote == '"' && value[end] == '\\' && !escaped {
			escaped = true
			continue
		}
		if value[end] == quote && !escaped {
			return end, nil
		}
		escaped = false
	}
	return 0, errors.New("unterminated quoted pattern")
}

// parseLocalRunnerEmbedPattern 验证 all: 前缀和符合 Go 规则的嵌入路径。
func parseLocalRunnerEmbedPattern(value string) (localRunnerEmbedPattern, error) {
	pattern := localRunnerEmbedPattern{value: value}
	if strings.HasPrefix(pattern.value, "all:") {
		pattern.includeHidden = true
		pattern.value = strings.TrimPrefix(pattern.value, "all:")
	}
	if !validLocalRunnerEmbedPatternPath(pattern.value) {
		return localRunnerEmbedPattern{}, fmt.Errorf("invalid pattern %q", value)
	}
	if !validLocalRunnerEmbedPatternElements(pattern.value) {
		return localRunnerEmbedPattern{}, fmt.Errorf("invalid pattern %q", value)
	}
	if _, err := path.Match(pattern.value, ""); err != nil {
		return localRunnerEmbedPattern{}, fmt.Errorf("invalid pattern %q: %w", value, err)
	}
	return pattern, nil
}

// validLocalRunnerEmbedPatternPath 检查模式是否满足嵌入路径的基本边界。
func validLocalRunnerEmbedPatternPath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/")
}

// validLocalRunnerEmbedPatternElements 拒绝空、当前目录和父目录路径段。
func validLocalRunnerEmbedPatternElements(value string) bool {
	for element := range strings.SplitSeq(value, "/") {
		if element == "" || element == "." || element == ".." {
			return false
		}
	}
	return true
}

// localRunnerEmbedPatternMatches 判断资源是否被单个模式选中，并保持隐藏路径规则。
func localRunnerEmbedPatternMatches(pattern localRunnerEmbedPattern, assetPath, packagePath string) (bool, error) {
	relativePath, err := localRunnerEmbedRelativePath(assetPath, packagePath)
	if err != nil {
		return false, err
	}
	matched, err := path.Match(pattern.value, relativePath)
	if err != nil || matched {
		return matched, err
	}
	for directory := path.Dir(relativePath); directory != "."; directory = path.Dir(directory) {
		matched, err := path.Match(pattern.value, directory)
		if err != nil {
			return false, err
		}
		if matched {
			return pattern.includeHidden || !localRunnerEmbedHiddenDescendant(relativePath, directory), nil
		}
	}
	return false, nil
}

func localRunnerEmbedRelativePath(assetPath, packagePath string) (string, error) {
	if packagePath == "." {
		return assetPath, nil
	}
	prefix := packagePath + "/"
	if !strings.HasPrefix(assetPath, prefix) {
		return "", fmt.Errorf("asset path %q escaped source package %q", assetPath, packagePath)
	}
	relativePath := strings.TrimPrefix(assetPath, prefix)
	if _, err := localReceiptRelativePath(relativePath); err != nil {
		return "", fmt.Errorf("asset path %q: %w", assetPath, err)
	}
	return relativePath, nil
}

func localRunnerEmbedHiddenDescendant(relativePath, directory string) bool {
	for element := range strings.SplitSeq(strings.TrimPrefix(relativePath, directory+"/"), "/") {
		if strings.HasPrefix(element, ".") || strings.HasPrefix(element, "_") {
			return true
		}
	}
	return false
}

type localRunnerSourceClosure struct {
	modulePath string
	pending    []string
	visited    map[string]struct{}
	seenPaths  map[string]struct{}
	paths      []string
}

func newLocalRunnerSourceClosure(modulePath string) *localRunnerSourceClosure {
	return &localRunnerSourceClosure{
		modulePath: modulePath,
		pending:    []string{localRunnerSemanticEntryPackage},
		visited:    make(map[string]struct{}),
		seenPaths:  make(map[string]struct{}),
		paths:      make([]string, 0),
	}
}

func (closure *localRunnerSourceClosure) hasPending() bool {
	return len(closure.pending) != 0
}

func (closure *localRunnerSourceClosure) visitNextPackage(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string) error {
	packagePath := closure.pending[0]
	closure.pending = closure.pending[1:]
	if _, visited := closure.visited[packagePath]; visited {
		return nil
	}
	closure.visited[packagePath] = struct{}{}
	files, err := gitTreeLocalRunnerPackageFiles(ctx, trustedGit, repositoryRoot, tree, packagePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := closure.addFile(file); err != nil {
			return err
		}
	}
	return nil
}

// addFile 将源文件和本地 import 加入待解析的 exact-tree 闭包。
func (closure *localRunnerSourceClosure) addFile(file localRunnerSourceFile) error {
	if _, duplicate := closure.seenPaths[file.path]; duplicate {
		return fmt.Errorf("local runner source closure contains duplicate file %q", file.path)
	}
	closure.seenPaths[file.path] = struct{}{}
	closure.paths = append(closure.paths, file.path)
	imports, err := localRunnerSourceImports(file.path, file.content)
	if err != nil {
		return err
	}
	for _, imported := range imports {
		localPath, local, err := localRunnerLocalPackagePath(closure.modulePath, imported)
		if err != nil {
			return err
		}
		if local {
			closure.pending = append(closure.pending, localPath)
		}
	}
	return nil
}

func gitTreeModulePath(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string) (string, error) {
	content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, "go.mod")
	if err != nil {
		return "", fmt.Errorf("read local runner closure module: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return "", fmt.Errorf("parse local runner closure module: %w", err)
	}
	if parsed.Module == nil || strings.TrimSpace(parsed.Module.Mod.Path) == "" {
		return "", errors.New("local runner closure module path is required")
	}
	return parsed.Module.Mod.Path, nil
}

type localRunnerSourceFile struct {
	path    string
	content []byte
}

// gitTreeLocalRunnerPackageFiles 从 exact tree 读取 package 的生产 Go 源文件。
func gitTreeLocalRunnerPackageFiles(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, packagePath string) ([]localRunnerSourceFile, error) {
	if _, err := localReceiptRelativePath(packagePath); err != nil {
		return nil, fmt.Errorf("local runner source package: %w", err)
	}
	entries, err := gitTreeLocalRunnerPackageEntries(ctx, trustedGit, repositoryRoot, tree, packagePath)
	if err != nil {
		return nil, err
	}
	files := make([]localRunnerSourceFile, 0)
	for _, entry := range entries {
		file, source, err := gitTreeLocalRunnerSourceFile(ctx, trustedGit, repositoryRoot, tree, packagePath, entry)
		if err != nil {
			return nil, err
		}
		if source {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("local runner source package %q is missing or has no production Go files", packagePath)
	}
	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	return files, nil
}

func gitTreeLocalRunnerPackageEntries(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, packagePath string) ([]string, error) {
	command, err := localReceiptTrustedGitCommand(ctx, trustedGit, repositoryRoot, "ls-tree", "-r", "-z", "--full-tree", tree, "--", packagePath)
	if err != nil {
		return nil, err
	}
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list local runner source package %q: %w", packagePath, err)
	}
	return parseGitTreeLocalRunnerPackageEntries(output)
}

// localReceiptTrustedGitCommand preserves the receipt-bound binary proof while
// delegating exact-object invocation policy to gateprivate's sole owner.
func localReceiptTrustedGitCommand(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot string, args ...string) (*exec.Cmd, error) {
	gitPath, err := trustedGit.VerifiedPath()
	if err != nil {
		return nil, err
	}
	command, err := gateprivate.TrustedGitCommandWithCandidateObjectAuthority(ctx, repositoryRoot, trustedGit.candidateObjectAuthority, args...)
	if err != nil {
		return nil, err
	}
	if command.Path != gitPath {
		return nil, errors.New("local executor receipt trusted Git path drifted")
	}
	return command, nil
}

// parseGitTreeLocalRunnerPackageEntries 解析 git ls-tree -z 输出中的 package 条目。
func parseGitTreeLocalRunnerPackageEntries(output []byte) ([]string, error) {
	if len(output) == 0 {
		return nil, nil
	}
	entries := strings.Split(string(output), "\x00")
	if entries[len(entries)-1] != "" {
		return nil, fmt.Errorf("local runner package tree output is missing NUL terminator")
	}
	entries = entries[:len(entries)-1]
	for index, entry := range entries {
		if entry == "" {
			return nil, fmt.Errorf("local runner package tree entry %d is malformed", index+1)
		}
	}
	return entries, nil
}

// gitTreeLocalRunnerSourceFile 校验并读取单个常规 Go blob。
func gitTreeLocalRunnerSourceFile(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, packagePath, raw string) (localRunnerSourceFile, bool, error) {
	if raw == "" {
		return localRunnerSourceFile{}, false, nil
	}
	metadata, sourcePath, found := strings.Cut(raw, "\t")
	if !found {
		return localRunnerSourceFile{}, false, fmt.Errorf("local runner source package %q has malformed tree entry %q", packagePath, raw)
	}
	if path.Dir(sourcePath) != packagePath || !isLocalRunnerSourcePath(sourcePath) {
		return localRunnerSourceFile{}, false, nil
	}
	if _, err := localReceiptRelativePath(sourcePath); err != nil {
		return localRunnerSourceFile{}, false, fmt.Errorf("local runner source path %q: %w", sourcePath, err)
	}
	fields := strings.Fields(metadata)
	if len(fields) != 3 || fields[1] != "blob" {
		return localRunnerSourceFile{}, false, fmt.Errorf("local runner source %q is not a regular blob", sourcePath)
	}
	if _, err := gitTreeFileExecutable(fields[0], sourcePath); err != nil {
		return localRunnerSourceFile{}, false, err
	}
	content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, sourcePath)
	if err != nil {
		return localRunnerSourceFile{}, false, fmt.Errorf("read local runner source %q: %w", sourcePath, err)
	}
	return localRunnerSourceFile{path: sourcePath, content: content}, true, nil
}

func localRunnerSourceImports(sourcePath string, content []byte) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, content, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse local runner source %q imports: %w", sourcePath, err)
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, imported := range parsed.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("parse local runner source %q import %q: %w", sourcePath, imported.Path.Value, err)
		}
		imports = append(imports, value)
	}
	return imports, nil
}

func localRunnerLocalPackagePath(modulePath, imported string) (string, bool, error) {
	prefix := strings.TrimSpace(modulePath) + "/"
	if !strings.HasPrefix(imported, prefix) {
		return "", false, nil
	}
	localPath, err := localReceiptRelativePath(strings.TrimPrefix(imported, prefix))
	if err != nil {
		return "", true, fmt.Errorf("local runner import %q is invalid: %w", imported, err)
	}
	return localPath, true, nil
}

// isLocalRunnerSourcePath identifies production Go compilation units after the
// package itself has been derived from the canonical exact-tree import graph.
func isLocalRunnerSourcePath(sourcePath string) bool {
	return strings.HasSuffix(sourcePath, ".go") && !strings.HasSuffix(sourcePath, "_test.go")
}
