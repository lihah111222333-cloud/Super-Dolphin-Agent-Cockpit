// Package projectmaptrusted 从可信编译资产为精确 Git tree 生成项目地图。
package projectmaptrusted

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	canonicalGeneratorPath = "scripts/generate_ai_project_map.mjs"
	candidatePolicyPath    = "scripts/codemap_policy.txt"
	managedOutputPath      = "docs/doc/codemap/project-map"
)

var exactTreePattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// ErrManagedOutputsModified 表示受管输出在 refresh 生成期间发生了并发变化。
var ErrManagedOutputsModified = errors.New("project-map managed outputs changed during refresh")

// TreeError 表示调用方提供的 tree 不是当前仓库中的精确 Git tree 对象。
type TreeError struct {
	Tree string
	Err  error
}

// Error 返回 Git tree 准备失败的可读描述。
func (err *TreeError) Error() string {
	return fmt.Sprintf("resolve exact project-map tree %q: %v", err.Tree, err.Err)
}

// Unwrap 返回 Git tree 准备失败的根因。
func (err *TreeError) Unwrap() error {
	return err.Err
}

// CandidateError 表示候选 tree 数据无法由可信生成器安全处理。
type CandidateError struct {
	Err error
}

// Error 返回候选 tree 违反可信输入约束的描述。
func (err *CandidateError) Error() string {
	return fmt.Sprintf("project-map candidate tree: %v", err.Err)
}

// Unwrap 返回候选输入失败的根因。
func (err *CandidateError) Unwrap() error {
	return err.Err
}

// GeneratorError 表示可信生成器拒绝了候选 tree。
type GeneratorError struct {
	Output string
	Err    error
}

// Error 返回可信生成器的执行失败描述。
func (err *GeneratorError) Error() string {
	if err.Output == "" {
		return fmt.Sprintf("trusted project-map generator: %v", err.Err)
	}
	return fmt.Sprintf("trusted project-map generator: %v: %s", err.Err, err.Output)
}

// Unwrap 返回可信生成器执行失败的根因。
func (err *GeneratorError) Unwrap() error {
	return err.Err
}

type preparedTree struct {
	repositoryRoot string
	sourceRoot     string
	generatorPath  string
	cleanup        func() error
}

// ExactTree 是从精确 Git tree 安全解包出的临时只读候选视图。
type ExactTree struct {
	RepositoryRoot string
	SourceRoot     string
	Cleanup        func() error
}

// MaterializeExactTree 校验 tree 对象并安全解包，不读取工作区覆盖内容。
func MaterializeExactTree(repository, tree, temporaryPrefix string) (ExactTree, error) {
	root, err := canonicalRepository(repository)
	if err != nil {
		return ExactTree{}, err
	}
	treeSHA, err := requireExactTree(root, tree)
	if err != nil {
		return ExactTree{}, err
	}
	tempRoot, err := makeExactTreeTempRoot(temporaryPrefix)
	if err != nil {
		return ExactTree{}, fmt.Errorf("create exact-tree temporary root: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(tempRoot); err != nil {
			return fmt.Errorf("remove exact-tree temporary root: %w", err)
		}
		return nil
	}
	sourceRoot := filepath.Join(tempRoot, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return ExactTree{}, errors.Join(fmt.Errorf("create exact-tree source root: %w", err), cleanup())
	}
	if err := extractGitTree(root, treeSHA, sourceRoot); err != nil {
		return ExactTree{}, errors.Join(err, cleanup())
	}
	return ExactTree{RepositoryRoot: root, SourceRoot: sourceRoot, Cleanup: cleanup}, nil
}

// makeExactTreeTempRoot 只接受绝对临时根，避免相对 TMPDIR 随进程 cwd 泄漏到仓库。
func makeExactTreeTempRoot(prefix string) (string, error) {
	temporaryRoot := os.TempDir()
	if !filepath.IsAbs(temporaryRoot) {
		return "", fmt.Errorf("exact-tree temporary directory must be absolute: %q", temporaryRoot)
	}
	tempRoot, err := os.MkdirTemp(temporaryRoot, prefix)
	if err != nil {
		return "", err
	}
	return tempRoot, nil
}

// CheckTree 在临时目录中校验精确 Git tree，不读取或执行候选工作树入口。
func CheckTree(repository, tree string, stdout io.Writer) (resultErr error) {
	if stdout == nil {
		return errors.New("project-map stdout is required")
	}
	prepared, err := prepareTree(repository, tree)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := prepared.cleanup(); cleanupErr != nil {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	return runTrustedGenerator(prepared, true, stdout)
}

// RefreshTree 从精确 Git tree 生成项目地图，并逐文件原子写回当前仓库。
func RefreshTree(repository, tree string, stdout io.Writer) (resultErr error) {
	if stdout == nil {
		return errors.New("project-map stdout is required")
	}
	prepared, err := prepareTree(repository, tree)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := prepared.cleanup(); cleanupErr != nil {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()

	repositoryOutput := filepath.Join(prepared.repositoryRoot, filepath.FromSlash(managedOutputPath))
	observedWorktree, err := snapshotDirectory(repositoryOutput)
	if err != nil {
		return fmt.Errorf("snapshot current project-map outputs: %w", err)
	}
	if err := runTrustedGenerator(prepared, false, stdout); err != nil {
		return err
	}
	if err := requireSnapshot(repositoryOutput, observedWorktree); err != nil {
		return err
	}
	treeOutput := filepath.Join(prepared.sourceRoot, filepath.FromSlash(managedOutputPath))
	return replaceManagedOutputs(treeOutput, prepared.repositoryRoot)
}

// prepareTree 将精确 Git tree 解包到隔离目录并安装可信生成器。
func prepareTree(repository, tree string) (preparedTree, error) {
	exact, err := MaterializeExactTree(repository, tree, "super-dolphin-project-map-")
	if err != nil {
		return preparedTree{}, err
	}
	tempRoot := filepath.Dir(exact.SourceRoot)
	generatorPath, err := installTrustedGenerator(tempRoot, exact.SourceRoot)
	if err != nil {
		return preparedTree{}, errors.Join(err, exact.Cleanup())
	}
	return preparedTree{
		repositoryRoot: exact.RepositoryRoot,
		sourceRoot:     exact.SourceRoot,
		generatorPath:  generatorPath,
		cleanup:        exact.Cleanup,
	}, nil
}

// canonicalRepository 解析并验证仓库根目录的真实路径。
func canonicalRepository(repository string) (string, error) {
	if strings.TrimSpace(repository) == "" {
		return "", errors.New("project-map repository is required")
	}
	root, err := filepath.Abs(repository)
	if err != nil {
		return "", fmt.Errorf("resolve project-map repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve project-map repository symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("stat project-map repository: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("project-map repository must be a directory")
	}
	return root, nil
}

func requireExactTree(repository, tree string) (string, error) {
	treeSHA := strings.TrimSpace(tree)
	if treeSHA != tree || !exactTreePattern.MatchString(treeSHA) {
		return "", &TreeError{Tree: tree, Err: errors.New("tree must be one lowercase 40- or 64-hex object ID")}
	}
	command := exec.Command("git", "-C", repository, "cat-file", "-t", treeSHA)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", &TreeError{Tree: treeSHA, Err: commandFailure(command, output, err)}
	}
	if strings.TrimSpace(string(output)) != "tree" {
		return "", &TreeError{Tree: treeSHA, Err: fmt.Errorf("object type is %q, want tree", strings.TrimSpace(string(output)))}
	}
	return treeSHA, nil
}

// extractGitTree 从指定 tree 导出候选内容，不读取工作区文件。
func extractGitTree(repository, tree, destination string) error {
	command := exec.Command("git", "-C", repository, "archive", "--format=tar", tree)
	archive, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Git tree archive: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Git tree archive: %w", err)
	}
	if err := extractArchive(archive, destination); err != nil {
		err = errors.Join(err, stopGitArchive(command, "rejected"))
		return &CandidateError{Err: err}
	}
	if _, err := io.Copy(io.Discard, archive); err != nil {
		err = errors.Join(err, stopGitArchive(command, "undrainable"))
		return &CandidateError{Err: fmt.Errorf("drain Git tree archive: %w", err)}
	}
	if err := command.Wait(); err != nil {
		return &TreeError{Tree: tree, Err: commandFailure(command, stderr.Bytes(), err)}
	}
	return nil
}

// stopGitArchive 终止无法继续消费的 archive 进程，并始终回收子进程。
func stopGitArchive(command *exec.Cmd, reason string) error {
	var stopErr error
	if killErr := command.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		stopErr = fmt.Errorf("stop %s Git archive: %w", reason, killErr)
	}
	if waitErr := command.Wait(); waitErr != nil {
		stopErr = errors.Join(stopErr, fmt.Errorf("wait for %s Git archive: %w", reason, waitErr))
	}
	return stopErr
}

// extractArchive 安全解包 Git archive 流，拒绝越界和链接条目。
func extractArchive(reader io.Reader, destination string) error {
	archive := tar.NewReader(reader)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Git tree archive: %w", err)
		}
		target, err := archiveTarget(destination, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create archived directory %q: %w", header.Name, err)
			}
		case tar.TypeReg, 0:
			if err := writeArchiveFile(target, header, archive); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has forbidden type %d", header.Name, header.Typeflag)
		}
	}
}

// archiveTarget 返回 archive 条目的受限目标路径。
func archiveTarget(root, name string) (string, error) {
	trimmed := strings.TrimSuffix(name, "/")
	clean := pathpkg.Clean(trimmed)
	if trimmed == "" || clean != trimmed || pathpkg.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive entry has unsafe path %q", name)
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve archive entry %q: %w", name, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry escapes temporary root: %q", name)
	}
	return target, nil
}

// writeArchiveFile 以 archive 指定权限写入一个常规文件。
func writeArchiveFile(target string, header *tar.Header, reader io.Reader) error {
	if header.Size < 0 {
		return fmt.Errorf("archive entry %q has negative size", header.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create archive parent for %q: %w", header.Name, err)
	}
	mode := os.FileMode(0o600)
	if header.Mode&0o111 != 0 {
		mode = 0o700
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create archived file %q: %w", header.Name, err)
	}
	_, copyErr := io.CopyN(file, reader, header.Size)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write archived file %q: %w", header.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close archived file %q: %w", header.Name, closeErr)
	}
	return nil
}

// installTrustedGenerator 将编译资产和候选策略写入隔离的可信目录。
func installTrustedGenerator(tempRoot, sourceRoot string) (string, error) {
	policy, err := candidatePolicy(sourceRoot)
	if err != nil {
		return "", err
	}
	generator, err := trustedGeneratorSource()
	if err != nil {
		return "", fmt.Errorf("decode compiled project-map generator: %w", err)
	}
	return writeTrustedGenerator(tempRoot, generator, policy)
}

// candidatePolicy 验证候选仓库标记并读取其项目地图策略。
func candidatePolicy(sourceRoot string) ([]byte, error) {
	for _, required := range []string{"go.mod", "CLAUDE.md"} {
		info, err := os.Stat(filepath.Join(sourceRoot, required))
		if err != nil || !info.Mode().IsRegular() {
			return nil, &CandidateError{Err: fmt.Errorf("required repository marker %q is missing or not regular", required)}
		}
	}
	policyPath := filepath.Join(sourceRoot, filepath.FromSlash(candidatePolicyPath))
	info, err := os.Stat(policyPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &CandidateError{Err: fmt.Errorf("candidate policy %q is missing or not regular", candidatePolicyPath)}
	}
	policy, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, &CandidateError{Err: fmt.Errorf("read candidate policy: %w", err)}
	}
	return policy, nil
}

func writeTrustedGenerator(tempRoot string, generator, policy []byte) (string, error) {
	trustedRoot := filepath.Join(tempRoot, "trusted")
	if err := os.Mkdir(trustedRoot, 0o700); err != nil {
		return "", fmt.Errorf("create trusted generator root: %w", err)
	}
	generatorPath := filepath.Join(trustedRoot, filepath.Base(canonicalGeneratorPath))
	if err := os.WriteFile(generatorPath, generator, 0o600); err != nil {
		return "", fmt.Errorf("write trusted generator asset: %w", err)
	}
	if err := os.WriteFile(filepath.Join(trustedRoot, filepath.Base(candidatePolicyPath)), policy, 0o600); err != nil {
		return "", fmt.Errorf("write candidate policy data: %w", err)
	}
	return generatorPath, nil
}

// runTrustedGenerator 使用候选 tree 外部的 Node 运行可信生成器。
func runTrustedGenerator(prepared preparedTree, check bool, stdout io.Writer) error {
	node, err := trustedNode(prepared)
	if err != nil {
		return err
	}
	args := []string{prepared.generatorPath, "--filesystem-scan"}
	if check {
		args = append(args, "--check", "--strict-drift")
	}
	command := exec.Command(node, args...)
	command.Dir = prepared.sourceRoot
	command.Env = trustedGeneratorEnvironment()
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return &GeneratorError{Output: strings.TrimSpace(stderr.String()), Err: err}
	}
	return nil
}

// trustedNode 解析候选 tree 外部的可执行 Node 二进制。
func trustedNode(prepared preparedTree) (string, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("resolve trusted Node runtime: %w", err)
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return "", fmt.Errorf("resolve trusted Node runtime path: %w", err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		return "", fmt.Errorf("resolve trusted Node runtime symlinks: %w", err)
	}
	if pathWithin(prepared.repositoryRoot, node) || pathWithin(prepared.sourceRoot, node) {
		return "", fmt.Errorf("trusted Node runtime must be external to candidate source: %q", node)
	}
	info, err := os.Stat(node)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("trusted Node runtime %q is not an executable regular file", node)
	}
	return node, nil
}

func trustedGeneratorEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, assignment := range os.Environ() {
		name, _, found := strings.Cut(assignment, "=")
		if !found || strings.HasPrefix(name, "NODE_") {
			continue
		}
		environment = append(environment, assignment)
	}
	return environment
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." ||
		relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// snapshotDirectory 读取无链接、仅常规文件的受管目录快照。
func snapshotDirectory(root string) (map[string][]byte, error) {
	snapshot := make(map[string][]byte)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat managed output root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("managed output root must be a directory")
	}
	err = filepath.WalkDir(root, snapshotDirectoryEntry(root, snapshot))
	if err != nil {
		return nil, fmt.Errorf("snapshot managed output: %w", err)
	}
	return snapshot, nil
}

// snapshotDirectoryEntry 返回填充受管输出快照的受限目录遍历回调。
func snapshotDirectoryEntry(root string, snapshot map[string][]byte) fs.WalkDirFunc {
	return func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed output contains symbolic link %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed output contains non-regular file %q", name)
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = data
		return nil
	}
}

// requireSnapshot 确认生成器运行期间受管输出未被外部修改。
func requireSnapshot(root string, expected map[string][]byte) error {
	actual, err := snapshotDirectory(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrManagedOutputsModified, err)
	}
	if len(actual) != len(expected) {
		return ErrManagedOutputsModified
	}
	for name, want := range expected {
		got, ok := actual[name]
		if !ok || !bytes.Equal(got, want) {
			return ErrManagedOutputsModified
		}
	}
	return nil
}

// replaceManagedOutputs 以生成快照替换受管项目地图输出。
func replaceManagedOutputs(source, repositoryRoot string) error {
	generated, err := snapshotDirectory(source)
	if err != nil {
		return fmt.Errorf("snapshot generated project-map outputs: %w", err)
	}
	outputRoot, err := ensureRealDirectoryPath(repositoryRoot, managedOutputPath)
	if err != nil {
		return err
	}
	current, err := snapshotDirectory(outputRoot)
	if err != nil {
		return fmt.Errorf("snapshot current project-map outputs: %w", err)
	}
	if err := removeStaleOutputs(outputRoot, current, generated); err != nil {
		return err
	}
	if err := writeGeneratedOutputs(outputRoot, generated); err != nil {
		return err
	}
	return removeEmptyIndex(outputRoot)
}

func removeStaleOutputs(root string, current, generated map[string][]byte) error {
	for name := range current {
		if _, ok := generated[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("remove stale project-map output %q: %w", name, err)
		}
	}
	return nil
}

func writeGeneratedOutputs(root string, generated map[string][]byte) error {
	for _, name := range orderedGeneratedOutputs(generated) {
		if err := atomicWriteFile(filepath.Join(root, filepath.FromSlash(name)), generated[name]); err != nil {
			return fmt.Errorf("write project-map output %q: %w", name, err)
		}
	}
	return nil
}

func orderedGeneratedOutputs(generated map[string][]byte) []string {
	names := make([]string, 0, len(generated))
	for name := range generated {
		if name != "AI_PROJECT_MANIFEST.json" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if _, ok := generated["AI_PROJECT_MANIFEST.json"]; ok {
		names = append(names, "AI_PROJECT_MANIFEST.json")
	}
	return names
}

func removeEmptyIndex(root string) error {
	indexPath := filepath.Join(root, "index")
	entries, err := os.ReadDir(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect project-map index: %w", err)
	}
	if len(entries) == 0 {
		if err := os.Remove(indexPath); err != nil {
			return fmt.Errorf("remove empty project-map index: %w", err)
		}
	}
	return nil
}

// ensureRealDirectoryPath 创建并验证不经过符号链接的受管目录路径。
func ensureRealDirectoryPath(root, relative string) (string, error) {
	current := root
	for element := range strings.SplitSeq(filepath.FromSlash(relative), string(filepath.Separator)) {
		if element == "" || element == "." || element == ".." {
			return "", fmt.Errorf("project-map output path is invalid: %q", relative)
		}
		current = filepath.Join(current, element)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", fmt.Errorf("create project-map output directory %q: %w", current, err)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect project-map output directory %q: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("project-map output path component is not a real directory: %q", current)
		}
	}
	return current, nil
}

// atomicWriteFile 将内容经同目录临时文件原子落盘。
func atomicWriteFile(target string, data []byte) (resultErr error) {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create atomic output parent: %w", err)
	}
	temporary, err := os.CreateTemp(parent, ".project-map-output-")
	if err != nil {
		return fmt.Errorf("create atomic output: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOwned := true
	defer cleanupAtomicOutput(temporaryPath, &temporaryOwned, &resultErr)
	if err := temporary.Chmod(0o644); err != nil {
		closeErr := temporary.Close()
		return errors.Join(fmt.Errorf("set atomic output permissions: %w", err), closeErr)
	}
	if _, err := temporary.Write(data); err != nil {
		closeErr := temporary.Close()
		return errors.Join(fmt.Errorf("write atomic output: %w", err), closeErr)
	}
	if err := temporary.Sync(); err != nil {
		closeErr := temporary.Close()
		return errors.Join(fmt.Errorf("sync atomic output: %w", err), closeErr)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close atomic output: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install atomic output: %w", err)
	}
	temporaryOwned = false
	return nil
}

func cleanupAtomicOutput(path string, owned *bool, resultErr *error) {
	if !*owned {
		return
	}
	if cleanupErr := os.Remove(path); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
		*resultErr = errors.Join(*resultErr, fmt.Errorf("remove atomic output: %w", cleanupErr))
	}
}

func commandFailure(command *exec.Cmd, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", strings.Join(command.Args, " "), err)
	}
	return fmt.Errorf("%s: %w: %s", strings.Join(command.Args, " "), err, detail)
}
