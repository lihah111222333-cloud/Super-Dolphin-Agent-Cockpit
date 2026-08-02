// Package gateclosure verifies the gate-image closure from an exact Git tree.
package gateclosure

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	gateDockerfile         = "build/gate/Dockerfile"
	gateInputs             = "build/gate/inputs.json"
	gateToolchain          = "build/gate/toolchain.lock"
	gateRuntimeDepsLock    = "build/gate/runtime-deps.lock"
	gateRuntimeDepsDocker  = "build/gate/runtime-deps.Dockerfile"
	gateRuntimeLSPPackage  = "build/gate/runtime-lsp/package.json"
	gateRuntimeLSPLock     = "build/gate/runtime-lsp/package-lock.json"
	gateRuntimeProxyModule = "build/gate/runtime-proxy/go.mod"
	gateRuntimeProxySum    = "build/gate/runtime-proxy/go.sum"
	gateRuntimeToolsModule = "build/gate/runtime-tools/go.mod"
	gateRuntimeToolsSum    = "build/gate/runtime-tools/go.sum"
	gateRuntimeToolsSource = "build/gate/runtime-tools/tools.go"
)

type inputManifest struct {
	SchemaVersion     string   `json:"schema_version"`
	Dockerfile        string   `json:"dockerfile"`
	Inputs            []string `json:"inputs"`
	GateCompileInputs []string `json:"gate_compile_inputs"`
}

type toolchainLock struct {
	SchemaVersion      string   `json:"schema_version"`
	BuildKitVersion    string   `json:"buildkit_version"`
	BuildKitImage      string   `json:"buildkit_image"`
	DockerfileFrontend string   `json:"dockerfile_frontend"`
	SourceDateEpoch    string   `json:"source_date_epoch"`
	TargetPlatforms    []string `json:"target_platforms"`
	BaseImages         []struct {
		Name      string `json:"name"`
		Reference string `json:"reference"`
	} `json:"base_images"`
	DependencySources []string `json:"dependency_sources"`
	RuntimeDepsLock   string   `json:"runtime_deps_lock"`
	RuntimeTools      struct {
		NodeVersion     string           `json:"node_version"`
		NPMVersion      string           `json:"npm_version"`
		PythonVersion   string           `json:"python_version"`
		Ripgrep         string           `json:"ripgrep"`
		Sqruff          string           `json:"sqruff"`
		SqruffArtifacts []sqruffArtifact `json:"sqruff_artifacts"`
		Gopls           string           `json:"gopls"`
		SQLC            string           `json:"sqlc"`
		NPMPackages     []string         `json:"npm_lsp_packages"`
	} `json:"runtime_tools"`
	NetworkPolicy string `json:"network_policy"`
}

// Generate 从当前仓库生成闭包文件，调用方只能用于明确的维护操作。
func Generate(tree string, check bool) error {
	root, err := commandOutput("", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	return generateFromRepository(strings.TrimSpace(root), tree, check)
}

// CheckTree 校验精确 Git 树，不读取或执行调用方工作树中的文件。
func CheckTree(repository string, tree string) error {
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(tree) == "" {
		return errors.New("closure repository and tree are required")
	}
	return generateFromRepository(repository, tree, true)
}

// generateFromRepository 从精确 Git 树生成或校验门禁镜像闭包。
func generateFromRepository(root string, tree string, check bool) error {
	treeSHA, err := resolveTreeSHA(root, tree)
	if err != nil {
		return err
	}
	sourceRoot, cleanup, err := createTemporarySourceRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	if err := extractGitTree(root, treeSHA, sourceRoot); err != nil {
		return err
	}
	outputs, sourceCount, err := generateClosureOutputs(sourceRoot)
	if err != nil {
		return err
	}
	if check {
		return verifyClosureOutputs(sourceRoot, treeSHA, outputs, sourceCount)
	}
	if err := writeClosureOutputs(root, outputs); err != nil {
		return err
	}
	fmt.Printf("generated gate image closure from Git tree %s (%d source files)\n", treeSHA, sourceCount)
	return nil
}

// resolveTreeSHA 将调用方给出的树标识解析为不可变的 Git tree SHA。
func resolveTreeSHA(root string, tree string) (string, error) {
	treeSHA, err := commandOutput(root, nil, "git", "rev-parse", tree+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(treeSHA), nil
}

// createTemporarySourceRoot 创建并解析用于安全解包 Git 树的临时目录。
func createTemporarySourceRoot() (string, func(), error) {
	tempRoot, err := os.MkdirTemp("", "super-dolphin-gate-closure-")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempRoot) }
	sourceRoot := filepath.Join(tempRoot, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create source root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve source root: %w", err)
	}
	return resolvedRoot, cleanup, nil
}

// generateClosureOutputs 从解包后的 Git 树构造 Dockerfile 与输入清单。
func generateClosureOutputs(sourceRoot string) (map[string][]byte, int, error) {
	localFiles, gateCompileFiles, err := collectClosureFiles(sourceRoot)
	if err != nil {
		return nil, 0, err
	}
	lock, runtimeDeps, err := readClosureLocks(sourceRoot)
	if err != nil {
		return nil, 0, err
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, localFiles)
	if err != nil {
		return nil, 0, err
	}
	manifestData, err := renderManifest(localFiles, gateCompileFiles)
	if err != nil {
		return nil, 0, err
	}
	return map[string][]byte{gateDockerfile: dockerfile, gateInputs: manifestData}, len(localFiles), nil
}

// collectClosureFiles 汇集环境镜像和运行时命令的最小有序闭包输入。
func collectClosureFiles(sourceRoot string) ([]string, []string, error) {
	gateCompileFiles, err := collectGateCompileFiles(sourceRoot)
	if err != nil {
		return nil, nil, err
	}
	localFiles, err := collectEnvironmentImageFiles(sourceRoot, gateCompileFiles)
	if err != nil {
		return nil, nil, err
	}
	return localFiles, gateCompileFiles, nil
}

// collectEnvironmentImageFiles 收集受信 CI runtime 和预编译门禁缓存所需的闭包输入。
func collectEnvironmentImageFiles(sourceRoot string, gateCompileFiles []string) ([]string, error) {
	files, err := collectNamedRegularInputs(sourceRoot, "environment image", []string{
		gateDockerfile,
		gateInputs,
		gateToolchain,
		gateRuntimeDepsLock,
		gateRuntimeDepsDocker,
		gateRuntimeLSPPackage,
		gateRuntimeLSPLock,
		gateRuntimeProxyModule,
		gateRuntimeProxySum,
		gateRuntimeToolsModule,
		gateRuntimeToolsSum,
		gateRuntimeToolsSource,
		"frontend-app/package.json",
		"frontend-app/package-lock.json",
		"internal/devtools/nilnessrunner/runner.go",
		"scripts/nilness_guard.go",
	})
	if err != nil {
		return nil, err
	}
	testCompileFiles, err := collectGoTestCompileFiles(sourceRoot)
	if err != nil {
		return nil, err
	}
	embeddedFiles, err := collectGoEmbedCompileFiles(sourceRoot, testCompileFiles)
	if err != nil {
		return nil, err
	}
	files = append(files, gateCompileFiles...)
	files = append(files, testCompileFiles...)
	files = append(files, embeddedFiles...)
	frontendFiles, err := collectFrontendViteCacheFiles(sourceRoot)
	if err != nil {
		return nil, err
	}
	files = append(files, frontendFiles...)
	return sortedUniqueStrings(files), nil
}

// collectFrontendViteCacheFiles collects the finite frontend build surface.
// It deliberately excludes dependencies and generated output, never copying
// the repository wholesale into the image context.
func collectFrontendViteCacheFiles(sourceRoot string) ([]string, error) {
	root := filepath.Join(sourceRoot, "frontend-app")
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "coverage") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("frontend Vite cache input %s is not a regular file", name)
		}
		relative, err := filepath.Rel(sourceRoot, name)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect frontend Vite cache inputs: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("frontend Vite cache closure is empty")
	}
	return sortedUniqueStrings(files), nil
}

// collectGoTestCompileFiles 收集根模块全部 Go 测试编译单元。镜像内 normal、e2e
// 与 race 的预热必须与实际 gate 使用相同的测试源码，不能只缓存 gate CLI 依赖图。
func collectGoTestCompileFiles(sourceRoot string) ([]string, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(sourceRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Go test compile input contains symlink %s", name)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Go test compile input %s is not regular", name)
		}
		relative, err := filepath.Rel(sourceRoot, name)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect Go test compile inputs: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("Go test compile closure is empty")
	}
	return sortedUniqueStrings(files), nil
}

// collectGateCompileFiles 只收集构建云端 gate CLI 所需的仓内源码与模块身份。
func collectGateCompileFiles(sourceRoot string) ([]string, error) {
	goFiles, err := collectLocalGoDependencyFiles(sourceRoot, []string{
		"cmd/super-dolphin-gate",
	})
	if err != nil {
		return nil, err
	}
	replacementFiles, err := collectLocalReplacementCompileFiles(sourceRoot)
	if err != nil {
		return nil, err
	}
	embeddedFiles, err := collectGoEmbedCompileFiles(sourceRoot, append(slices.Clone(goFiles), replacementFiles...))
	if err != nil {
		return nil, err
	}
	files := append([]string{"go.mod", "go.sum"}, goFiles...)
	files = append(files, replacementFiles...)
	files = append(files, embeddedFiles...)
	return sortedUniqueStrings(files), nil
}

// collectLocalReplacementCompileFiles 收集所有本地 replace 模块的非测试 Go 源码和模块锁。
func collectLocalReplacementCompileFiles(sourceRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(sourceRoot, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("read root go.mod: %w", err)
	}
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse root go.mod: %w", err)
	}
	files := make([]string, 0)
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		root, err := canonicalLocalReplacementRoot(sourceRoot, replacement.New.Path)
		if err != nil {
			return nil, fmt.Errorf("local replacement %s: %w", replacement.Old.Path, err)
		}
		moduleFiles, err := collectReplacementModuleFiles(sourceRoot, root)
		if err != nil {
			return nil, fmt.Errorf("local replacement %s: %w", replacement.Old.Path, err)
		}
		files = append(files, moduleFiles...)
	}
	return sortedUniqueStrings(files), nil
}

func canonicalLocalReplacementRoot(sourceRoot, replacement string) (string, error) {
	if replacement == "" || filepath.IsAbs(replacement) {
		return "", errors.New("path must be a repository-relative directory")
	}
	cleaned := filepath.Clean(filepath.FromSlash(replacement))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the repository")
	}
	absolute := filepath.Join(sourceRoot, cleaned)
	relative, err := filepath.Rel(sourceRoot, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the repository")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return filepath.ToSlash(relative), nil
}

func collectReplacementModuleFiles(sourceRoot, moduleRoot string) ([]string, error) {
	files := make([]string, 0)
	absoluteRoot := filepath.Join(sourceRoot, filepath.FromSlash(moduleRoot))
	err := filepath.WalkDir(absoluteRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("replacement module contains symlink %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		base := entry.Name()
		include := strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go")
		if name == filepath.Join(absoluteRoot, "go.mod") || name == filepath.Join(absoluteRoot, "go.sum") {
			include = true
		}
		if !include {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("replacement module input %s is not regular", name)
		}
		relative, err := filepath.Rel(sourceRoot, name)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	required := filepath.ToSlash(filepath.Join(moduleRoot, "go.mod"))
	if !slices.Contains(files, required) {
		return nil, fmt.Errorf("required module input %s is missing", required)
	}
	return sortedUniqueStrings(files), nil
}

// collectLocalGoDependencyFiles 从入口包递归收集环境运行时所需的仓内 Go 源码。
func collectLocalGoDependencyFiles(sourceRoot string, roots []string) ([]string, error) {
	modulePath, err := readModulePath(filepath.Join(sourceRoot, "go.mod"))
	if err != nil {
		return nil, err
	}
	queue := slices.Clone(roots)
	seen := make(map[string]struct{}, len(queue))
	files := make(map[string]struct{})
	for len(queue) > 0 {
		directory := queue[0]
		queue = queue[1:]
		if _, exists := seen[directory]; exists {
			continue
		}
		seen[directory] = struct{}{}
		imports, err := collectGoPackageFiles(sourceRoot, directory, files)
		if err != nil {
			return nil, err
		}
		for _, imported := range imports {
			prefix := modulePath + "/"
			if localPackage, ok := strings.CutPrefix(imported, prefix); ok {
				queue = append(queue, localPackage)
			}
		}
	}
	return sortedKeys(files), nil
}

// collectGoPackageFiles 收集单个仓内包的非测试源码并返回其导入路径。
func collectGoPackageFiles(sourceRoot, directory string, files map[string]struct{}) ([]string, error) {
	absolute := filepath.Join(sourceRoot, filepath.FromSlash(directory))
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, fmt.Errorf("read local Go package %s: %w", directory, err)
	}
	imports := make(map[string]struct{})
	fileCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		relative := filepath.ToSlash(filepath.Join(directory, name))
		files[relative] = struct{}{}
		fileCount++
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(absolute, name), nil, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse local Go package file %s: %w", relative, err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("decode import in %s: %w", relative, err)
			}
			imports[path] = struct{}{}
		}
	}
	if fileCount == 0 {
		return nil, fmt.Errorf("local Go package %s has no build files", directory)
	}
	return sortedKeys(imports), nil
}

func readModulePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read module path: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", errors.New("module path is missing")
}

func sortedUniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedKeys(set)
}

// readClosureLocks 读取并交叉校验闭包生成所需的两份锁文件。
func readClosureLocks(sourceRoot string) (toolchainLock, runtimeDepsLock, error) {
	lock, err := readToolchainLock(filepath.Join(sourceRoot, gateToolchain))
	if err != nil {
		return toolchainLock{}, runtimeDepsLock{}, err
	}
	runtimeDeps, err := readRuntimeDepsLock(filepath.Join(sourceRoot, gateRuntimeDepsLock))
	if err != nil {
		return toolchainLock{}, runtimeDepsLock{}, err
	}
	if err := runtimeDeps.validateAgainstSource(sourceRoot, lock); err != nil {
		return toolchainLock{}, runtimeDepsLock{}, err
	}
	return lock, runtimeDeps, nil
}

// verifyClosureOutputs 比对生成结果与精确 Git 树中已跟踪的文件。
func verifyClosureOutputs(sourceRoot, treeSHA string, outputs map[string][]byte, sourceCount int) error {
	for name, wanted := range outputs {
		tracked, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("read generated file %s from Git tree %s: %w", name, treeSHA, err)
		}
		if !bytes.Equal(tracked, wanted) {
			return fmt.Errorf("generated file %s drifted from Git tree %s; run go run ./build/gate/cmd/generate-closure -tree <tree>", name, treeSHA)
		}
	}
	fmt.Printf("verified gate image closure in Git tree %s (%d source files)\n", treeSHA, sourceCount)
	return nil
}

// writeClosureOutputs 原子地将生成结果写入仓库根目录。
func writeClosureOutputs(root string, outputs map[string][]byte) error {
	for name, data := range outputs {
		if err := writeAtomic(filepath.Join(root, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	return nil
}

// extractGitTree 将精确 Git tree 以 tar 流形式解包到受控临时目录。
func extractGitTree(repo string, tree string, destination string) error {
	command := exec.Command("git", "-C", repo, "archive", "--format=tar", tree)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open git archive stream: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}
	if err := extractArchive(tar.NewReader(stdout), destination); err != nil {
		return err
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %w: %s", tree, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// extractArchive 将 Git archive 中经过路径校验的常规条目写入临时根目录。
func extractArchive(reader *tar.Reader, destination string) error {
	for {
		header, err := nextArchiveHeader(reader)
		if err != nil {
			return err
		}
		if header == nil {
			return nil
		}
		if err := extractArchiveEntry(reader, destination, header); err != nil {
			return err
		}
	}
}

// nextArchiveHeader 读取下一个归档条目，并将归档结束标识为 nil。
func nextArchiveHeader(reader *tar.Reader) (*tar.Header, error) {
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read git archive: %w", err)
	}
	return header, nil
}

// extractArchiveEntry 根据条目类型安全地创建目录或写入常规文件。
func extractArchiveEntry(reader *tar.Reader, destination string, header *tar.Header) error {
	name, err := safeArchiveName(header.Name)
	if err != nil {
		return err
	}
	target := filepath.Join(destination, filepath.FromSlash(name))
	switch header.Typeflag {
	case tar.TypeDir:
		return createArchiveDirectory(target, name)
	case tar.TypeReg:
		return writeArchiveFile(reader, target, name, header.FileInfo().Mode().Perm())
	default:
		return fmt.Errorf("Git tree contains forbidden non-regular entry %q (type %d)", name, header.Typeflag)
	}
}

// createArchiveDirectory 创建归档中的目录条目。
func createArchiveDirectory(target string, name string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create archive directory %s: %w", name, err)
	}
	return nil
}

// writeArchiveFile 以排他创建方式写入归档中的常规文件条目。
func writeArchiveFile(reader io.Reader, target string, name string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create archive parent for %s: %w", name, err)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create archived file %s: %w", name, err)
	}
	copyErr := copyAndCloseArchiveFile(file, reader)
	if copyErr != nil {
		return fmt.Errorf("write archived file %s: %w", name, copyErr)
	}
	return nil
}

// copyAndCloseArchiveFile 将条目内容写入文件，并始终返回复制或关闭错误。
func copyAndCloseArchiveFile(file *os.File, reader io.Reader) error {
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// safeArchiveName 拒绝非规范路径，防止归档条目逃逸临时根目录。
func safeArchiveName(name string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if name == "" || cleaned == "." || cleaned != strings.TrimSuffix(name, "/") || strings.HasPrefix(cleaned, "../") || filepath.IsAbs(name) {
		return "", fmt.Errorf("archive path %q is not canonical", name)
	}
	return cleaned, nil
}

// collectNamedRegularInputs 收集指定用途根路径下的常规文件并按路径排序。
func collectNamedRegularInputs(sourceRoot, inputKind string, roots []string) ([]string, error) {
	set := make(map[string]struct{})
	for _, name := range roots {
		if err := collectNamedRegularInput(sourceRoot, inputKind, name, set); err != nil {
			return nil, err
		}
	}
	return sortedKeys(set), nil
}

// collectNamedRegularInput 将单个文件或目录根加入用途对应的常规文件集合。
func collectNamedRegularInput(sourceRoot, inputKind, name string, set map[string]struct{}) error {
	absolute := filepath.Join(sourceRoot, filepath.FromSlash(name))
	info, err := os.Lstat(absolute)
	if err != nil {
		return fmt.Errorf("inspect %s input %s: %w", inputKind, name, err)
	}
	if info.Mode().IsRegular() {
		set[name] = struct{}{}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("%s input %s is not a regular file or directory", inputKind, name)
	}
	if err := collectNamedRegularTree(sourceRoot, inputKind, absolute, set); err != nil {
		return fmt.Errorf("walk %s input %s: %w", inputKind, name, err)
	}
	return nil
}

// collectNamedRegularTree 遍历目录并拒绝其中的任何非普通条目。
func collectNamedRegularTree(sourceRoot, inputKind, absolute string, set map[string]struct{}) error {
	return filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("%s tree contains non-regular entry %s", inputKind, path)
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		set[filepath.ToSlash(relative)] = struct{}{}
		return nil
	})
}

// immutableBaseImage 查找并校验指定的不可变远程基础镜像引用。
func immutableBaseImage(lock toolchainLock, name string) (string, error) {
	for _, image := range lock.BaseImages {
		if image.Name != name {
			continue
		}
		if !strings.Contains(image.Reference, "@sha256:") {
			return "", fmt.Errorf("toolchain image %s is not immutable", name)
		}
		if err := validateRemoteImageReference(image.Reference); err != nil {
			return "", fmt.Errorf("toolchain image %s: %w", name, err)
		}
		return image.Reference, nil
	}
	return "", fmt.Errorf("toolchain image %s is missing", name)
}

// validateRemoteImageReference 拒绝可解析为本机回环地址的镜像仓库。
func validateRemoteImageReference(reference string) error {
	registry, _, found := strings.Cut(reference, "/")
	if !found || registry == "" {
		return nil // Docker Hub's canonical repository reference is remotely resolved.
	}
	if strings.HasPrefix(registry, "127.") || registry == "localhost" || strings.HasPrefix(registry, "localhost:") || registry == "::1" || strings.HasPrefix(registry, "[::1]") {
		return errors.New("loopback image registries are forbidden for cross-platform truth images")
	}
	return nil
}

// readToolchainLock 严格读取并校验工具链锁文件。
func readToolchainLock(path string) (toolchainLock, error) {
	lock, err := decodeToolchainLock(path)
	if err != nil {
		return toolchainLock{}, err
	}
	if err := validateToolchainLock(lock); err != nil {
		return toolchainLock{}, err
	}
	return lock, nil
}

// decodeToolchainLock 拒绝未知字段和尾随 JSON 文档后解析工具链锁。
func decodeToolchainLock(path string) (toolchainLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolchainLock{}, fmt.Errorf("read toolchain lock: %w", err)
	}
	var lock toolchainLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return toolchainLock{}, fmt.Errorf("decode toolchain lock: %w", err)
	}
	if err := rejectTrailingDocument(decoder); err != nil {
		return toolchainLock{}, err
	}
	return lock, nil
}

// validateToolchainLock 校验工具链锁的固定 schema、平台和依赖约束。
func validateToolchainLock(lock toolchainLock) error {
	if lock.SchemaVersion != "1" || lock.RuntimeDepsLock != gateRuntimeDepsLock || lock.NetworkPolicy != "none" {
		return errors.New("toolchain lock schema, runtime_deps_lock, or network policy is invalid")
	}
	if !slices.Equal(lock.TargetPlatforms, runtimeDepsPlatforms) {
		return errors.New("toolchain target platforms must be exactly linux/amd64 and linux/arm64")
	}
	if err := validateToolchainBaseImages(lock); err != nil {
		return err
	}
	return validateSqruffArtifacts(lock.RuntimeTools.SqruffArtifacts)
}

// validateToolchainBaseImages 确认锁文件只包含必需且不可变的两份基础镜像。
func validateToolchainBaseImages(lock toolchainLock) error {
	if len(lock.BaseImages) != 2 {
		return errors.New("toolchain lock must contain exactly GO_IMAGE and NODE_IMAGE")
	}
	if _, err := immutableBaseImage(lock, "GO_IMAGE"); err != nil {
		return err
	}
	if _, err := immutableBaseImage(lock, "NODE_IMAGE"); err != nil {
		return err
	}
	return nil
}

// renderDockerfile 生成带有只读门禁 Go 编译缓存的确定性环境镜像。
func renderDockerfile(lock toolchainLock, runtimeDeps runtimeDepsLock, buildFiles []string) ([]byte, error) {
	if err := validateDockerfileInputs(lock, runtimeDeps); err != nil {
		return nil, err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "ARG RUNTIME_DEPS_IMAGE\nARG BASELINE_CACHE_IMAGE\nARG SOURCE_DATE_EPOCH=%s\n", lock.SourceDateEpoch)
	output.WriteString("ARG BUILD_SOURCE_TREE\nARG IMAGE_INPUT_DIGEST\nARG POLICY_DIGEST\nARG TOOLCHAIN_DIGEST\nARG TARGET_PLATFORM\n")
	output.WriteString("FROM ${BASELINE_CACHE_IMAGE} AS baseline-cache\nFROM ${RUNTIME_DEPS_IMAGE} AS build\nUSER root\nARG BASELINE_CACHE_IMAGE\nARG SOURCE_DATE_EPOCH\n\n")
	output.WriteString("WORKDIR /src\nENV GOCACHE=/root/.cache/go-build GOTOOLCHAIN=local GOPROXY=file:///opt/super-dolphin-gate/runtime/go-proxy GOSUMDB=off\n")
	if err := writeDockerCopyInstructions(&output, buildFiles, "/src/", "environment build COPY"); err != nil {
		return nil, err
	}
	output.WriteString("RUN --network=none --mount=type=cache,target=/root/.cache/go-build,sharing=locked --mount=type=bind,from=baseline-cache,source=/,target=/baseline-cache,ro sh -ec '\\\n")
	output.WriteString("    mkdir -p cmd/agent-terminal/web-dist; printf \"<!doctype html><title>gate compile seed</title>\\n\" > cmd/agent-terminal/web-dist/index.html; \\\n")
	output.WriteString("    go_cache_proxy=; if test \"$BASELINE_CACHE_IMAGE\" != runtime-deps; then test -x /baseline-cache/super-dolphin-gate && test -d /baseline-cache/opt/super-dolphin/cache/go-build; go_cache_proxy=\"/baseline-cache/super-dolphin-gate worker go-cache-proxy --seed /baseline-cache/opt/super-dolphin/cache/go-build --private /root/.cache/go-build\"; fi; \\\n")
	output.WriteString("    compile_go() { if test -n \"$go_cache_proxy\"; then env GOCACHEPROG=\"$go_cache_proxy\" \"$@\"; else \"$@\"; fi; }; \\\n")
	output.WriteString("    compile_phase() { phase=$1; shift; started=$(date +%s); compile_go \"$@\"; finished=$(date +%s); entries=$(find /root/.cache/go-build -type f | wc -l); printf \"[gate-image] compile phase=%s seconds=%s cache_entries=%s\\n\" \"$phase\" \"$((finished-started))\" \"$entries\"; }; \\\n")
	output.WriteString("    compile_phase gate-cli env CGO_ENABLED=0 go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate; \\\n")
	output.WriteString("    compile_phase normal env CGO_ENABLED=1 go test -mod=mod -run \"^$\" ./...; \\\n")
	output.WriteString("    compile_phase e2e env CGO_ENABLED=1 go test -mod=mod -tags=e2e -run \"^$\" ./cmd/mcp-lsp; \\\n")
	output.WriteString("    set -- $(/out/super-dolphin-gate worker race-package-patterns); test $# -gt 0; \\\n")
	output.WriteString("    compile_phase race env CGO_ENABLED=1 go test -mod=mod -race -run \"^$\" \"$@\"; \\\n")
	output.WriteString("    vite_started=$(date +%s); ln -s /opt/super-dolphin-gate/runtime/frontend/node_modules frontend-app/node_modules; env npm_config_cache=/opt/super-dolphin-gate/runtime/frontend/npm-cache npm_config_offline=true ./frontend-app/node_modules/.bin/vite optimize --root frontend-app --force; test -s frontend-app/node_modules/.vite/deps/_metadata.json; mkdir -p /out/vite-cache; cp -a frontend-app/node_modules/.vite/. /out/vite-cache/; vite_finished=$(date +%s); printf \"[gate-image] vite-cache-export seconds=%s cache_entries=%s\\n\" \"$((vite_finished-vite_started))\" \"$(find /out/vite-cache -type f | wc -l)\"; \\\n")
	output.WriteString("    mkdir -p /out/runtime-seed-snapshot/frontend-app; cp go.sum /out/runtime-seed-snapshot/go.sum; cp frontend-app/package-lock.json /out/runtime-seed-snapshot/frontend-app/package-lock.json; \\\n")
	output.WriteString("    cache_started=$(date +%s); rm -rf /out/go-build-cache; mkdir -p /out/go-build-cache; cp -a /root/.cache/go-build/. /out/go-build-cache/; cache_finished=$(date +%s); printf \"[gate-image] cache-export seconds=%s cache_entries=%s\\n\" \"$((cache_finished-cache_started))\" \"$(find /out/go-build-cache -type f | wc -l)\"; \\\n")
	output.WriteString("    touch -d \"@${SOURCE_DATE_EPOCH}\" /out/super-dolphin-gate\n'\n\n")
	output.WriteString("FROM ${RUNTIME_DEPS_IMAGE}\nUSER root\n")
	output.WriteString("ARG BUILD_SOURCE_TREE\nARG IMAGE_INPUT_DIGEST\nARG POLICY_DIGEST\nARG TOOLCHAIN_DIGEST\nARG TARGET_PLATFORM\n")
	output.WriteString("LABEL org.super-dolphin.source-tree-sha=\"${BUILD_SOURCE_TREE}\" \\\n")
	output.WriteString("      org.super-dolphin.image-input-digest=\"${IMAGE_INPUT_DIGEST}\" \\\n")
	output.WriteString("      org.super-dolphin.policy-sha=\"${POLICY_DIGEST}\" \\\n")
	output.WriteString("      org.super-dolphin.toolchain-digest=\"${TOOLCHAIN_DIGEST}\" \\\n")
	output.WriteString("      org.super-dolphin.platform=\"${TARGET_PLATFORM}\" \\\n")
	output.WriteString("      org.super-dolphin.schema-version=\"1\"\n")
	output.WriteString("COPY --from=build /out/super-dolphin-gate /super-dolphin-gate\n")
	output.WriteString("COPY --from=build --chown=65532:65532 /out/go-build-cache /opt/super-dolphin/cache/go-build\n")
	output.WriteString("COPY --from=build --chown=65532:65532 /out/vite-cache /opt/super-dolphin-gate/runtime/frontend/vite-cache\n")
	output.WriteString("COPY --from=build /out/runtime-seed-snapshot /tmp/runtime-seed-snapshot\n")
	output.WriteString("RUN --network=none mkdir -p /opt/super-dolphin-gate/frontend-embed && \\\n")
	output.WriteString("    printf '<!doctype html><title>gate compile seed</title>\\n' > /opt/super-dolphin-gate/frontend-embed/index.html && \\\n")
	output.WriteString("    /super-dolphin-gate worker runtime-seed write /tmp/runtime-seed-snapshot /opt/super-dolphin-gate/runtime && rm -rf /tmp/runtime-seed-snapshot && \\\n")
	output.WriteString("    chmod -R a-w /opt/super-dolphin-gate/frontend-embed /opt/super-dolphin-gate/runtime/frontend/vite-cache && \\\n")
	output.WriteString("    chmod -R a-w /opt/super-dolphin/cache/go-build\n")
	output.WriteString("ENV GOTOOLCHAIN=local GOPROXY=file:///opt/super-dolphin-gate/runtime/go-proxy GOSUMDB=off GOFLAGS=-mod=mod\\ -buildvcs=false\n")
	output.WriteString("USER 65532:65532\nENTRYPOINT [\"/super-dolphin-gate\"]\n")
	return []byte(output.String()), nil
}

// validateDockerfileInputs 校验 Dockerfile 渲染所依赖的锁文件不变量。
func validateDockerfileInputs(lock toolchainLock, runtimeDeps runtimeDepsLock) error {
	if !isCanonicalSourceDateEpoch(lock.SourceDateEpoch) {
		return errors.New("toolchain lock source_date_epoch must be a canonical non-negative integer")
	}
	if lock.NetworkPolicy != "none" {
		return errors.New("toolchain lock network_policy must be none")
	}
	return runtimeDeps.validateShape()
}

// writeDockerCopyInstructions 按目录稳定分组并写入 Docker COPY 指令。
func writeDockerCopyInstructions(output *strings.Builder, files []string, destinationPrefix, copyLabel string) error {
	groups := groupFilesByDirectory(files)
	for _, directory := range sortedKeys(groups) {
		values := slices.Clone(groups[directory])
		sort.Strings(values)
		values = append(values, destinationPrefix+directory+"/")
		encoded, err := json.Marshal(values)
		if err != nil {
			return fmt.Errorf("encode %s for %s: %w", copyLabel, directory, err)
		}
		fmt.Fprintf(output, "COPY %s\n", encoded)
	}
	return nil
}

// groupFilesByDirectory 将输入文件按其规范化父目录分组。
func groupFilesByDirectory(files []string) map[string][]string {
	groups := make(map[string][]string)
	for _, name := range files {
		directory := filepath.ToSlash(filepath.Dir(name))
		groups[directory] = append(groups[directory], name)
	}
	return groups
}

// isCanonicalSourceDateEpoch 判断时间戳是否为非负十进制规范整数。
func isCanonicalSourceDateEpoch(value string) bool {
	seconds, err := strconv.ParseInt(value, 10, 64)
	return err == nil && seconds >= 0 && strconv.FormatInt(seconds, 10) == value
}

// renderManifest 生成包含全部闭包输入的稳定 JSON 清单。
func renderManifest(localFiles, gateCompileFiles []string) ([]byte, error) {
	inputs := slices.Clone(localFiles)
	compileInputs := slices.Clone(gateCompileFiles)
	manifest := inputManifest{SchemaVersion: "2", Dockerfile: gateDockerfile, Inputs: inputs, GateCompileInputs: compileInputs}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode input manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// commandOutput 在指定目录和环境中运行命令并返回其标准输出。
func commandOutput(directory string, environment []string, name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// sortedKeys 返回字符串键集合的稳定排序结果。
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// writeAtomic 通过同目录临时文件和重命名原子替换生成物。
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory for %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gate-closure-")
	if err != nil {
		return fmt.Errorf("create temporary output for %s: %w", path, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary output for %s: %w", path, err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod temporary output for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output for %s: %w", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace output %s: %w", path, err)
	}
	return nil
}

func collectGoEmbedCompileFiles(sourceRoot string, compileFiles []string) ([]string, error) {
	files := make(map[string]struct{})
	for _, compileFile := range compileFiles {
		if !strings.HasSuffix(compileFile, ".go") {
			continue
		}
		absolute, packageDirectory, err := secureGoCompileSource(sourceRoot, compileFile)
		if err != nil {
			return nil, err
		}
		source, err := os.ReadFile(absolute)
		if err != nil {
			return nil, fmt.Errorf("read Go compile source %s: %w", compileFile, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), compileFile, source, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse Go compile source %s: %w", compileFile, err)
		}
		embedded, err := collectGoEmbedFiles(sourceRoot, packageDirectory, parsed)
		if err != nil {
			return nil, fmt.Errorf("collect embedded files from %s: %w", compileFile, err)
		}
		for _, embeddedFile := range embedded {
			files[embeddedFile] = struct{}{}
		}
	}
	return sortedKeys(files), nil
}

func secureGoCompileSource(sourceRoot, compileFile string) (string, string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(compileFile))
	if cleaned == "." || filepath.IsAbs(cleaned) || relativeEscapesRoot(cleaned) || filepath.ToSlash(cleaned) != compileFile {
		return "", "", fmt.Errorf("Go compile source path %q is invalid or escapes source root", compileFile)
	}
	packageDirectory := filepath.ToSlash(filepath.Dir(cleaned))
	packageRoot, err := secureEmbedPackageRoot(sourceRoot, packageDirectory)
	if err != nil {
		return "", "", fmt.Errorf("Go compile source %q: %w", compileFile, err)
	}
	absolute := filepath.Join(packageRoot, filepath.Base(cleaned))
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", "", fmt.Errorf("stat Go compile source %q: %w", compileFile, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("Go compile source %q is not a regular file", compileFile)
	}
	return absolute, packageDirectory, nil
}

func collectGoEmbedFiles(sourceRoot, packageDirectory string, parsed *ast.File) ([]string, error) {
	patterns, err := parseGoEmbedPatterns(parsed)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	packageRoot, err := secureEmbedPackageRoot(sourceRoot, packageDirectory)
	if err != nil {
		return nil, err
	}
	files := make(map[string]struct{})
	for _, pattern := range patterns {
		if packageDirectory == "cmd/agent-terminal" && pattern == "all:web-dist" {
			// web-dist 是候选构建前由前端 gate 生成的产物；预热闭包只放入
			// 最小占位文件以满足 Go embed 的编译期约束，绝不复制 node_modules。
			continue
		}
		matches, err := resolveGoEmbedPattern(packageRoot, pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", pattern, err)
		}
		for _, match := range matches {
			target := filepath.Join(packageRoot, filepath.FromSlash(match))
			relative, err := filepath.Rel(sourceRoot, target)
			if err != nil || relativeEscapesRoot(relative) {
				return nil, fmt.Errorf("embedded file %q escapes source root", match)
			}
			files[filepath.ToSlash(relative)] = struct{}{}
		}
	}
	return sortedKeys(files), nil
}

func parseGoEmbedPatterns(parsed *ast.File) ([]string, error) {
	importsEmbed := false
	for _, imported := range parsed.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("decode import %s: %w", imported.Path.Value, err)
		}
		if path == "embed" {
			importsEmbed = true
		}
	}

	var patterns []string
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			const prefix = "//go:embed"
			if !strings.HasPrefix(comment.Text, prefix) {
				continue
			}
			arguments := strings.TrimPrefix(comment.Text, prefix)
			if arguments != "" && arguments[0] != ' ' && arguments[0] != '\t' {
				continue
			}
			args, err := parseGoEmbedArguments(arguments)
			if err != nil {
				return nil, fmt.Errorf("parse //go:embed directive: %w", err)
			}
			if len(args) == 0 {
				return nil, errors.New("//go:embed directive has no patterns")
			}
			patterns = append(patterns, args...)
		}
	}
	if len(patterns) > 0 && !importsEmbed {
		return nil, errors.New("//go:embed directive requires importing embed")
	}
	return patterns, nil
}

func parseGoEmbedArguments(arguments string) ([]string, error) {
	var patterns []string
	for arguments = strings.TrimLeftFunc(arguments, unicode.IsSpace); arguments != ""; arguments = strings.TrimLeftFunc(arguments, unicode.IsSpace) {
		var pattern string
	ParseArgument:
		switch arguments[0] {
		case '`':
			var found bool
			pattern, arguments, found = strings.Cut(arguments[1:], "`")
			if !found {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", arguments)
			}
		case '"':
			end := 1
			for ; end < len(arguments); end++ {
				if arguments[end] == '\\' {
					end++
					continue
				}
				if arguments[end] != '"' {
					continue
				}
				quoted := arguments[:end+1]
				unquoted, err := strconv.Unquote(quoted)
				if err != nil {
					return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", quoted)
				}
				pattern = unquoted
				arguments = arguments[end+1:]
				break ParseArgument
			}
			return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", arguments)
		default:
			end := len(arguments)
			for index, character := range arguments {
				if unicode.IsSpace(character) {
					end = index
					break
				}
			}
			pattern = arguments[:end]
			arguments = arguments[end:]
		}
		if arguments != "" {
			character, _ := utf8.DecodeRuneInString(arguments)
			if !unicode.IsSpace(character) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", arguments)
			}
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func secureEmbedPackageRoot(sourceRoot, packageDirectory string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(packageDirectory))
	if filepath.IsAbs(cleaned) || relativeEscapesRoot(cleaned) {
		return "", fmt.Errorf("Go package path %q escapes source root", packageDirectory)
	}
	packageRoot := filepath.Join(sourceRoot, cleaned)
	relative, err := filepath.Rel(sourceRoot, packageRoot)
	if err != nil || relativeEscapesRoot(relative) {
		return "", fmt.Errorf("Go package path %q escapes source root", packageDirectory)
	}
	info, err := os.Lstat(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("stat source root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("source root is not a real directory")
	}
	if relative == "." {
		return packageRoot, nil
	}
	current := sourceRoot
	for element := range strings.SplitSeq(filepath.ToSlash(relative), "/") {
		current = filepath.Join(current, filepath.FromSlash(element))
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("stat Go package path %q: %w", packageDirectory, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("Go package path %q contains a non-directory or symbolic link", packageDirectory)
		}
	}
	return packageRoot, nil
}

func resolveGoEmbedPattern(packageRoot, pattern string) ([]string, error) {
	glob, all := strings.CutPrefix(pattern, "all:")
	if glob == "." || !fs.ValidPath(glob) {
		return nil, errors.New("invalid pattern syntax")
	}
	if _, err := pathpkg.Match(glob, ""); err != nil {
		return nil, errors.New("invalid pattern syntax")
	}
	matches, err := globGoEmbedMatches(packageRoot, glob)
	if err != nil {
		return nil, err
	}
	files := make(map[string]struct{})
	for _, match := range matches {
		info, err := inspectGoEmbedMatch(packageRoot, match)
		if err != nil {
			return nil, err
		}
		switch {
		case info.Mode().IsRegular():
			files[match] = struct{}{}
		case info.IsDir():
			children, err := collectGoEmbedDirectory(packageRoot, match, all)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				files[child] = struct{}{}
			}
		default:
			return nil, fmt.Errorf("cannot embed irregular file %s", match)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("no matching files found")
	}
	return sortedKeys(files), nil
}

func globGoEmbedMatches(packageRoot, pattern string) ([]string, error) {
	segments := strings.Split(pattern, "/")
	var matches []string
	var visit func(absolute, relative string, index int) error
	visit = func(absolute, relative string, index int) error {
		entries, err := os.ReadDir(absolute)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			matched, err := pathpkg.Match(segments[index], entry.Name())
			if err != nil {
				return err
			}
			if !matched {
				continue
			}
			childRelative := entry.Name()
			if relative != "" {
				childRelative = relative + "/" + entry.Name()
			}
			if index == len(segments)-1 {
				matches = append(matches, childRelative)
				continue
			}
			info, err := inspectGoEmbedMatch(packageRoot, childRelative)
			if err != nil {
				return err
			}
			if info.IsDir() {
				if err := visit(filepath.Join(absolute, entry.Name()), childRelative, index+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(packageRoot, "", 0); err != nil {
		return nil, fmt.Errorf("expand embed glob %q: %w", pattern, err)
	}
	sort.Strings(matches)
	return matches, nil
}

func inspectGoEmbedMatch(packageRoot, relative string) (fs.FileInfo, error) {
	if relative == "." || !fs.ValidPath(relative) {
		return nil, fmt.Errorf("embedded path %q is invalid", relative)
	}
	target := filepath.Join(packageRoot, filepath.FromSlash(relative))
	rootRelative, err := filepath.Rel(packageRoot, target)
	if err != nil || relativeEscapesRoot(rootRelative) {
		return nil, fmt.Errorf("embedded path %q escapes package directory", relative)
	}
	var info fs.FileInfo
	current := packageRoot
	elements := strings.Split(relative, "/")
	for index, element := range elements {
		if badGoEmbedName(element) {
			return nil, fmt.Errorf("cannot embed %s: invalid name %s", relative, element)
		}
		current = filepath.Join(current, filepath.FromSlash(element))
		info, err = os.Lstat(current)
		if err != nil {
			return nil, fmt.Errorf("stat embedded path %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("cannot embed symbolic link %s", relative)
		}
		if index < len(elements)-1 && !info.IsDir() {
			return nil, fmt.Errorf("embedded path %q traverses non-directory %s", relative, strings.Join(elements[:index+1], "/"))
		}
		if info.IsDir() {
			nestedModule, err := directoryContainsGoMod(current)
			if err != nil {
				return nil, err
			}
			if nestedModule {
				return nil, fmt.Errorf("cannot embed %s: in different module", relative)
			}
		}
	}
	return info, nil
}

func collectGoEmbedDirectory(packageRoot, relative string, all bool) ([]string, error) {
	root := filepath.Join(packageRoot, filepath.FromSlash(relative))
	var files []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		entryRelative, err := filepath.Rel(packageRoot, name)
		if err != nil || relativeEscapesRoot(entryRelative) {
			return fmt.Errorf("embedded directory entry %q escapes package directory", name)
		}
		entryRelative = filepath.ToSlash(entryRelative)
		base := entry.Name()
		hidden := strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")
		if badGoEmbedName(base) || hidden && !all {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			if hidden {
				return nil
			}
			return fmt.Errorf("cannot embed file %s: invalid name %s", entryRelative, base)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot embed symbolic link %s", entryRelative)
		}
		if entry.IsDir() {
			nestedModule, err := directoryContainsGoMod(name)
			if err != nil {
				return err
			}
			if nestedModule {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("cannot embed irregular file %s", entryRelative)
		}
		files = append(files, entryRelative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func directoryContainsGoMod(directory string) (bool, error) {
	info, err := os.Lstat(filepath.Join(directory, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect nested module boundary %q: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("nested module boundary %q is a symbolic link", filepath.Join(directory, "go.mod"))
	}
	return true, nil
}

func badGoEmbedName(name string) bool {
	if module.CheckFilePath(name) != nil {
		return true
	}
	switch name {
	case "", ".bzr", ".git", ".hg", ".svn":
		return true
	default:
		return false
	}
}

func relativeEscapesRoot(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
