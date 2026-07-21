// Package gateclosure verifies the gate-image closure from an exact Git tree.
package gateclosure

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
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
	SchemaVersion string   `json:"schema_version"`
	Dockerfile    string   `json:"dockerfile"`
	Inputs        []string `json:"inputs"`
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
	localFiles, ownerFiles, err := collectClosureFiles(sourceRoot)
	if err != nil {
		return nil, 0, err
	}
	lock, runtimeDeps, err := readClosureLocks(sourceRoot)
	if err != nil {
		return nil, 0, err
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, localFiles, ownerFiles)
	if err != nil {
		return nil, 0, err
	}
	manifestData, err := renderManifest(localFiles)
	if err != nil {
		return nil, 0, err
	}
	return map[string][]byte{gateDockerfile: dockerfile, gateInputs: manifestData}, len(localFiles), nil
}

// collectClosureFiles 汇集编译与运行时 owner 的去重有序闭包输入。
func collectClosureFiles(sourceRoot string) ([]string, []string, error) {
	localFiles, err := collectCompileFiles(sourceRoot)
	if err != nil {
		return nil, nil, err
	}
	ownerFiles, err := collectRuntimeOwnerFiles(sourceRoot)
	if err != nil {
		return nil, nil, err
	}
	return mergeSorted(localFiles, ownerFiles), ownerFiles, nil
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

// collectRuntimeOwnerFiles 收集运行时 owner 校验需要的常规文件。
func collectRuntimeOwnerFiles(sourceRoot string) ([]string, error) {
	return collectNamedRegularInputs(sourceRoot, "runtime owner", []string{
		".githooks",
		"Makefile",
		"frontend-app/.frontend_code_size_guard_baseline.json",
		"frontend-app/.frontend_code_size_guard_baseline_test.json",
		"frontend-app/eslint.config.js",
		"frontend-app/index.html",
		"frontend-app/jsconfig.json",
		"frontend-app/package-lock.json",
		"frontend-app/package.json",
		"frontend-app/scripts",
		"frontend-app/tsconfig.contracts.json",
		"frontend-app/vite.config.js",
		"frontend-app/vite.config.test.js",
		"internal/archtest",
		"scripts",
	})
}

// collectCompileFiles 收集构建 gate 二进制和校验器所需的常规输入。
func collectCompileFiles(sourceRoot string) ([]string, error) {
	return collectRegularInputs(sourceRoot, []string{
		"cmd/super-dolphin-gate", "cmd/super-dolphin-gate-executor",
		"internal", "pkg", "build/gate/closure", "build/gate/cmd/runtime-seed-manifest/main.go",
		gateRuntimeProxyModule, gateRuntimeProxySum,
	})
}

// collectRegularInputs 收集闭包编译输入中的常规文件。
func collectRegularInputs(sourceRoot string, roots []string) ([]string, error) {
	return collectNamedRegularInputs(sourceRoot, "closure", roots)
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

// mergeSorted 对两个路径集合去重并返回稳定排序的结果。
func mergeSorted(left, right []string) []string {
	values := make(map[string]struct{}, len(left)+len(right))
	for _, name := range left {
		values[name] = struct{}{}
	}
	for _, name := range right {
		values[name] = struct{}{}
	}
	return sortedKeys(values)
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

// renderDockerfile 生成使用预构建运行时依赖镜像的确定性 Dockerfile。
func renderDockerfile(lock toolchainLock, runtimeDeps runtimeDepsLock, localFiles, ownerFiles []string) ([]byte, error) {
	if err := validateDockerfileInputs(lock, runtimeDeps); err != nil {
		return nil, err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "ARG RUNTIME_DEPS_IMAGE\nARG SOURCE_DATE_EPOCH=%s\n", lock.SourceDateEpoch)
	output.WriteString("FROM ${RUNTIME_DEPS_IMAGE} AS build\nUSER root\nARG SOURCE_DATE_EPOCH\n\n")
	output.WriteString("WORKDIR /src\nENV GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off\n")
	output.WriteString("COPY [\"go.mod\", \"go.sum\", \"./\"]\n")
	output.WriteString("RUN --network=none cp -a /opt/super-dolphin-gate/runtime/vendor ./vendor\n")
	if err := writeDockerCopyInstructions(&output, localFiles, "./", "Docker COPY"); err != nil {
		return nil, err
	}
	output.WriteString("RUN --network=none /usr/local/bin/super-dolphin-runtime-seed verify /src /opt/super-dolphin-gate/runtime\n")
	output.WriteString("RUN --network=none CGO_ENABLED=0 go build -mod=vendor -trimpath -buildvcs=false -o /tmp/nilness-guard ./scripts/nilness_guard.go && rm /tmp/nilness-guard\n")
	output.WriteString("RUN --network=none CGO_ENABLED=0 go test -mod=vendor -run '^$' ./internal/devtools/gatehook\n")
	output.WriteString("RUN --network=none CGO_ENABLED=0 go build -mod=vendor -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate && \\\n")
	output.WriteString("    CGO_ENABLED=0 go build -mod=vendor -trimpath -buildvcs=false -o /out/super-dolphin-gate-executor ./cmd/super-dolphin-gate-executor && \\\n")
	output.WriteString("    touch -d \"@${SOURCE_DATE_EPOCH}\" /out/super-dolphin-gate /out/super-dolphin-gate-executor\n\n")
	output.WriteString("FROM ${RUNTIME_DEPS_IMAGE}\nUSER root\n")
	output.WriteString("COPY --from=build /out/super-dolphin-gate /super-dolphin-gate\n")
	output.WriteString("COPY --from=build /out/super-dolphin-gate-executor /usr/local/bin/super-dolphin-gate-executor\n")
	if err := writeDockerCopyInstructions(&output, ownerFiles, "/opt/super-dolphin-gate/owners/", "runtime owner COPY"); err != nil {
		return nil, err
	}
	output.WriteString("ENV GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor\\ -buildvcs=false\n")
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
func renderManifest(localFiles []string) ([]byte, error) {
	inputs := []string{
		gateDockerfile, gateInputs, gateToolchain, gateRuntimeDepsLock, gateRuntimeDepsDocker,
		gateRuntimeLSPPackage, gateRuntimeLSPLock,
		gateRuntimeToolsModule, gateRuntimeToolsSum, gateRuntimeToolsSource,
		"build/gate/closure_test.go",
		"build/gate/cmd/generate-closure/main.go",
		"go.mod", "go.sum",
	}
	inputs = append(inputs, localFiles...)
	sort.Strings(inputs)
	manifest := inputManifest{SchemaVersion: "1", Dockerfile: gateDockerfile, Inputs: inputs}
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
