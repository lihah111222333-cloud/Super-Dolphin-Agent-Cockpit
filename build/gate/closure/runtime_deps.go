package gateclosure

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	runtimeDepsSchemaVersion = "3"
	runtimeDepsPullPolicy    = "anonymous"
	runtimeDepsBuildTimeout  = 30 * time.Minute
	runtimeDepsManifestLimit = 8 << 20
	ociIndexMediaType        = "application/vnd.oci.image.index.v1+json"
	ociManifestMediaType     = "application/vnd.oci.image.manifest.v1+json"
	dockerIndexMediaType     = "application/vnd.docker.distribution.manifest.list.v2+json"
	dockerManifestMediaType  = "application/vnd.docker.distribution.manifest.v2+json"
)

var runtimeDepsPlatforms = []string{"linux/amd64", "linux/arm64"}

var (
	errRuntimeDepsInputsDrift    = errors.New("runtime dependency lock inputs drifted")
	errRuntimeDepsLegacySchemaV2 = errors.New("runtime dependency lock uses legacy schema v2")
	runtimeDepsRunCommand        = runDependencyRefreshCommand
)

type runtimeDepsLock struct {
	SchemaVersion      string             `json:"schema_version"`
	RegistryPullPolicy string             `json:"registry_pull_policy"`
	Images             []runtimeDepsImage `json:"images"`
	Inputs             runtimeDepsInputs  `json:"inputs"`
	Paths              runtimeDepsPaths   `json:"paths"`
}

type runtimeDepsImage struct {
	Platform  string                     `json:"platform"`
	Image     gatecontract.ImageIdentity `json:"image"`
	ImageSize int64                      `json:"image_size_bytes"`
}

type sqruffArtifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type runtimeDepsInputs struct {
	Dockerfile          string `json:"dockerfile_sha256"`
	ToolchainLock       string `json:"toolchain_lock_sha256"`
	GoMod               string `json:"go_mod_sha256"`
	GoSum               string `json:"go_sum_sha256"`
	NilnessRunner       string `json:"nilness_runner_sha256"`
	NilnessGuard        string `json:"nilness_guard_sha256"`
	FrontendPackageLock string `json:"frontend_package_lock_sha256"`
	LSPPackageLock      string `json:"lsp_package_lock_sha256"`
	ProxyGoMod          string `json:"proxy_go_mod_sha256"`
	ProxyGoSum          string `json:"proxy_go_sum_sha256"`
	ToolsGoMod          string `json:"tools_go_mod_sha256"`
	ToolsGoSum          string `json:"tools_go_sum_sha256"`
	ManifestBuilder     string `json:"manifest_builder_sha256"`
	ManifestAPI         string `json:"manifest_api_sha256"`
}

type runtimeDepsPaths struct {
	Manifest            string `json:"manifest"`
	Vendor              string `json:"vendor"`
	GoModuleProxy       string `json:"go_module_proxy"`
	FrontendNodeModules string `json:"frontend_node_modules"`
	PlaywrightBrowsers  string `json:"playwright_browsers"`
	LSPNodeModules      string `json:"lsp_node_modules"`
	SQLC                string `json:"sqlc"`
	Ripgrep             string `json:"ripgrep"`
	Sqruff              string `json:"sqruff"`
	Gopls               string `json:"gopls"`
	Go                  string `json:"go"`
	Node                string `json:"node"`
	NPM                 string `json:"npm"`
	Git                 string `json:"git"`
	Make                string `json:"make"`
}

// canonicalRuntimeDepsPaths 返回真值镜像与执行器共享的不可变运行时路径集合。
func canonicalRuntimeDepsPaths() runtimeDepsPaths {
	return runtimeDepsPaths{
		Manifest:            "/opt/super-dolphin-gate/runtime/manifest.json",
		Vendor:              "/opt/super-dolphin-gate/runtime/vendor",
		GoModuleProxy:       "/opt/super-dolphin-gate/runtime/go-proxy",
		FrontendNodeModules: "/opt/super-dolphin-gate/runtime/frontend/node_modules",
		PlaywrightBrowsers:  "/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
		LSPNodeModules:      "/opt/super-dolphin-gate/runtime/lsp/node_modules",
		SQLC:                "/opt/super-dolphin-gate/runtime/bin/sqlc",
		Ripgrep:             "/opt/super-dolphin-gate/runtime/bin/rg",
		Sqruff:              "/opt/super-dolphin-gate/runtime/bin/sqruff",
		Gopls:               "/usr/local/bin/gopls", Go: "/usr/local/go/bin/go",
		Node: "/usr/local/bin/node", NPM: "/usr/local/bin/npm",
		Git: "/usr/bin/git", Make: "/usr/bin/make",
	}
}

// readRuntimeDepsLock 严格解码并验证运行时依赖锁文件，拒绝旧 schema、未知字段和尾随数据。
func readRuntimeDepsLock(lockPath string) (runtimeDepsLock, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return runtimeDepsLock{}, fmt.Errorf("read runtime dependency identity: %w", err)
	}
	var schema struct {
		Version string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return runtimeDepsLock{}, fmt.Errorf("decode runtime dependency identity schema: %w", err)
	}
	if schema.Version == "2" {
		return runtimeDepsLock{}, errRuntimeDepsLegacySchemaV2
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var lock runtimeDepsLock
	if err := decoder.Decode(&lock); err != nil {
		return runtimeDepsLock{}, fmt.Errorf("decode runtime dependency identity: %w", err)
	}
	if err := rejectTrailingDocument(decoder); err != nil {
		return runtimeDepsLock{}, err
	}
	if err := lock.validateShape(); err != nil {
		return runtimeDepsLock{}, err
	}
	return lock, nil
}

func (lock runtimeDepsLock) validateShape() error {
	if err := validateRuntimeDepsLockHeader(lock); err != nil {
		return err
	}
	if err := validateRuntimeDepsLockImages(lock.Images); err != nil {
		return err
	}
	if err := validateRuntimeDepsInputDigests(lock.Inputs); err != nil {
		return err
	}
	if lock.Paths != canonicalRuntimeDepsPaths() {
		return errors.New("runtime dependency paths drifted from the executor contract")
	}
	return nil
}

func validateRuntimeDepsLockHeader(lock runtimeDepsLock) error {
	if lock.SchemaVersion != runtimeDepsSchemaVersion {
		return fmt.Errorf("runtime dependency schema = %q, want %q", lock.SchemaVersion, runtimeDepsSchemaVersion)
	}
	if lock.RegistryPullPolicy != runtimeDepsPullPolicy {
		return fmt.Errorf("runtime dependency registry pull policy = %q, want %q", lock.RegistryPullPolicy, runtimeDepsPullPolicy)
	}
	return nil
}

// validateRuntimeDepsLockImages 校验锁文件必须精确覆盖支持的平台且没有重复镜像身份。
func validateRuntimeDepsLockImages(images []runtimeDepsImage) error {
	if len(images) != len(runtimeDepsPlatforms) {
		return fmt.Errorf("runtime dependency image count = %d, want %d", len(images), len(runtimeDepsPlatforms))
	}
	for index, platform := range runtimeDepsPlatforms {
		if err := validateRuntimeDepsLockImage(images[index], platform, index); err != nil {
			return err
		}
	}
	return validateRuntimeDepsImageSet(images)
}

// validateRuntimeDepsLockImage 校验单个平台镜像的 registry、摘要、平台与正大小契约。
func validateRuntimeDepsLockImage(image runtimeDepsImage, platform string, index int) error {
	if image.Platform != platform {
		return fmt.Errorf("runtime dependency image platform %q at index %d, want %q", image.Platform, index, platform)
	}
	if err := image.Image.Validate(); err != nil {
		return fmt.Errorf("runtime dependency image %s identity: %w", platform, err)
	}
	if err := validateRuntimeDepsRemoteRegistry(image.Image.Registry); err != nil {
		return fmt.Errorf("runtime dependency image %s registry: %w", platform, err)
	}
	if image.Image.OS+"/"+image.Image.Architecture != platform || image.Image.Variant != "" {
		return fmt.Errorf("runtime dependency image platform %q does not match identity", platform)
	}
	if image.ImageSize <= 0 {
		return fmt.Errorf("runtime dependency image %s size must be positive", platform)
	}
	return nil
}

// validateRuntimeDepsImageSet 验证多平台镜像共享同一索引并且平台清单与配置摘要互异。
func validateRuntimeDepsImageSet(images []runtimeDepsImage) error {
	if len(images) == 0 {
		return errors.New("runtime dependency image set is empty")
	}
	repository := images[0].Image.Registry
	indexDigest := images[0].Image.OCIIndexDigest
	manifests := make(map[string]struct{}, len(images))
	configs := make(map[string]struct{}, len(images))
	for _, image := range images {
		if image.Image.Registry != repository || image.Image.OCIIndexDigest != indexDigest {
			return errors.New("runtime dependency platforms must share one registry repository and OCI index digest")
		}
		if err := recordRuntimeDepsDigests(manifests, configs, image.Image); err != nil {
			return err
		}
	}
	return nil
}

func recordRuntimeDepsDigests(manifests, configs map[string]struct{}, image gatecontract.ImageIdentity) error {
	if _, exists := manifests[image.PlatformManifestDigest]; exists {
		return errors.New("runtime dependency platform manifest digest is duplicated")
	}
	manifests[image.PlatformManifestDigest] = struct{}{}
	if _, exists := configs[image.ConfigDigest]; exists {
		return errors.New("runtime dependency config digest is duplicated")
	}
	configs[image.ConfigDigest] = struct{}{}
	return nil
}

func validateRuntimeDepsInputDigests(inputs runtimeDepsInputs) error {
	for _, field := range runtimeDepsInputFields(inputs) {
		if !validSHA256(field.digest) {
			return fmt.Errorf("runtime dependency %s digest is invalid", field.name)
		}
	}
	return nil
}

type runtimeDepsInputField struct {
	name   string
	digest string
}

func runtimeDepsInputFields(inputs runtimeDepsInputs) []runtimeDepsInputField {
	return []runtimeDepsInputField{
		{"dockerfile", inputs.Dockerfile}, {"go.mod", inputs.GoMod}, {"go.sum", inputs.GoSum},
		{"nilness runner", inputs.NilnessRunner}, {"nilness guard", inputs.NilnessGuard},
		{"toolchain lock", inputs.ToolchainLock}, {"frontend package lock", inputs.FrontendPackageLock},
		{"LSP package lock", inputs.LSPPackageLock}, {"proxy go.mod", inputs.ProxyGoMod},
		{"proxy go.sum", inputs.ProxyGoSum}, {"tools go.mod", inputs.ToolsGoMod},
		{"tools go.sum", inputs.ToolsGoSum}, {"manifest builder", inputs.ManifestBuilder},
		{"manifest API", inputs.ManifestAPI},
	}
}

// validateAgainstSource 绑定锁文件输入、网络策略与平台集合到待构建的确切源树。
func (lock runtimeDepsLock) validateAgainstSource(sourceRoot string, toolchain toolchainLock) error {
	if err := lock.validateShape(); err != nil {
		return err
	}
	if toolchain.NetworkPolicy != "none" {
		return errors.New("normal truth image network policy must be none")
	}
	if !slices.Equal(toolchain.TargetPlatforms, runtimeDepsPlatforms) {
		return errors.New("runtime dependency and toolchain platforms must be exactly linux/amd64 and linux/arm64")
	}
	wanted, err := digestRuntimeDepsInputs(sourceRoot)
	if err != nil {
		return err
	}
	if wanted != lock.Inputs {
		return fmt.Errorf("%w; run the explicit refresh-dependencies command", errRuntimeDepsInputsDrift)
	}
	return nil
}

func digestRuntimeDepsInputs(root string) (runtimeDepsInputs, error) {
	var result runtimeDepsInputs
	for _, field := range runtimeDepsDigestTargets(&result) {
		value, err := digestRuntimeDepsFile(root, field.path)
		if err != nil {
			return runtimeDepsInputs{}, err
		}
		*field.out = value
	}
	return result, nil
}

type runtimeDepsDigestTarget struct {
	path string
	out  *string
}

func runtimeDepsDigestTargets(inputs *runtimeDepsInputs) []runtimeDepsDigestTarget {
	return []runtimeDepsDigestTarget{
		{gateRuntimeDepsDocker, &inputs.Dockerfile}, {gateToolchain, &inputs.ToolchainLock},
		{"go.mod", &inputs.GoMod}, {"go.sum", &inputs.GoSum},
		{"internal/devtools/nilnessrunner/runner.go", &inputs.NilnessRunner},
		{"scripts/nilness_guard.go", &inputs.NilnessGuard},
		{"frontend-app/package-lock.json", &inputs.FrontendPackageLock}, {gateRuntimeLSPLock, &inputs.LSPPackageLock},
		{gateRuntimeProxyModule, &inputs.ProxyGoMod}, {gateRuntimeProxySum, &inputs.ProxyGoSum},
		{gateRuntimeToolsModule, &inputs.ToolsGoMod}, {gateRuntimeToolsSum, &inputs.ToolsGoSum},
		{"build/gate/cmd/runtime-seed-manifest/main.go", &inputs.ManifestBuilder},
		{"internal/devtools/gate/executor_seed.go", &inputs.ManifestAPI},
	}
}

func digestRuntimeDepsFile(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", fmt.Errorf("read runtime dependency input %s: %w", name, err)
	}
	return digestBytes(data), nil
}

// RefreshDependencyClosure 显式构建并发布锁定的运行时依赖镜像，Hook 校验器不得调用。
func RefreshDependencyClosure(tree, repository string) error {
	if err := validateRuntimeDepsRefreshRequest(tree, repository); err != nil {
		return err
	}
	root, treeSHA, sourceRoot, cleanup, err := prepareRuntimeDepsRefresh(tree)
	if err != nil {
		return err
	}
	defer cleanup()
	lock, err := readToolchainLock(filepath.Join(sourceRoot, gateToolchain))
	if err != nil {
		return err
	}
	lockedReference := "locked-" + treeSHA[:16]
	if reused, err := reuseRuntimeDepsPublication(root, sourceRoot, repository, lockedReference, lock, treeSHA); err != nil || reused {
		return err
	}
	if err := buildRuntimeDepsImage(sourceRoot, repository, treeSHA, lock); err != nil {
		return err
	}
	if err := publishRuntimeDepsIndex(repository, lockedReference, "refresh-"+treeSHA[:16], lock.TargetPlatforms); err != nil {
		return fmt.Errorf("publish runtime dependency OCI index: %w", err)
	}
	return persistRefreshedRuntimeDepsLock(root, sourceRoot, repository, lockedReference, lock.TargetPlatforms, treeSHA)
}

func validateRuntimeDepsRefreshRequest(tree, repository string) error {
	if tree == "" || strings.ContainsAny(tree, " \t\r\n") {
		return errors.New("runtime dependency tree is required")
	}
	if err := validateRuntimeDepsRemoteRegistry(repository); err != nil {
		return err
	}
	return validateRuntimeDepsRegistry(repository)
}

// prepareRuntimeDepsRefresh 将指定 Git tree 导出到私有临时目录，避免把工作区状态混入发布输入。
func prepareRuntimeDepsRefresh(tree string) (string, string, string, func(), error) {
	rootOutput, err := commandOutput("", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", nil, err
	}
	root := strings.TrimSpace(rootOutput)
	treeOutput, err := commandOutput(root, nil, "git", "rev-parse", tree+"^{tree}")
	if err != nil {
		return "", "", "", nil, err
	}
	treeSHA := strings.TrimSpace(treeOutput)
	if len(treeSHA) < 16 {
		return "", "", "", nil, errors.New("runtime dependency Git tree SHA is invalid")
	}
	temporary, err := os.MkdirTemp("", "super-dolphin-runtime-deps-")
	if err != nil {
		return "", "", "", nil, fmt.Errorf("create runtime dependency build root: %w", err)
	}
	sourceRoot := filepath.Join(temporary, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		os.RemoveAll(temporary)
		return "", "", "", nil, err
	}
	if err := extractGitTree(root, treeSHA, sourceRoot); err != nil {
		os.RemoveAll(temporary)
		return "", "", "", nil, err
	}
	return root, treeSHA, sourceRoot, func() { _ = os.RemoveAll(temporary) }, nil
}

func buildRuntimeDepsImage(sourceRoot, repository, treeSHA string, lock toolchainLock) error {
	arguments, err := runtimeDepsBuildArguments(sourceRoot, repository, treeSHA, lock)
	if err != nil {
		return err
	}
	if err := runDependencyRefreshCommand(runtimeDepsBuildTimeout, "docker", arguments...); err != nil {
		return fmt.Errorf("build and publish runtime dependency image: %w", err)
	}
	return nil
}

// runtimeDepsBuildArguments 构造只引用锁定基础镜像和工件的 buildx 发布参数。
func runtimeDepsBuildArguments(sourceRoot, repository, treeSHA string, lock toolchainLock) ([]string, error) {
	goImage, err := immutableBaseImage(lock, "GO_IMAGE")
	if err != nil {
		return nil, err
	}
	nodeImage, err := immutableBaseImage(lock, "NODE_IMAGE")
	if err != nil {
		return nil, err
	}
	amd64, err := sqruffArtifactForPlatform(lock.RuntimeTools.SqruffArtifacts, "linux/amd64")
	if err != nil {
		return nil, err
	}
	arm64, err := sqruffArtifactForPlatform(lock.RuntimeTools.SqruffArtifacts, "linux/arm64")
	if err != nil {
		return nil, err
	}
	return []string{
		"buildx", "build", "--progress=plain", "--push", "--provenance=false", "--network=default",
		"--platform=" + strings.Join(lock.TargetPlatforms, ","), "--file=" + filepath.Join(sourceRoot, gateRuntimeDepsDocker),
		"--tag=" + repository + ":refresh-" + treeSHA[:16],
		"--build-arg=GO_IMAGE=" + goImage, "--build-arg=NODE_IMAGE=" + nodeImage,
		"--build-arg=SQRUFF_ARCHIVE_URL_AMD64=" + amd64.URL, "--build-arg=SQRUFF_ARCHIVE_SHA256_AMD64=" + amd64.SHA256,
		"--build-arg=SQRUFF_ARCHIVE_URL_ARM64=" + arm64.URL, "--build-arg=SQRUFF_ARCHIVE_SHA256_ARM64=" + arm64.SHA256,
		sourceRoot,
	}, nil
}

// persistRefreshedRuntimeDepsLock 在匿名验证 locked publication 后原子写入新的依赖身份锁。
func persistRefreshedRuntimeDepsLock(root, sourceRoot, repository, reference string, platforms []string, treeSHA string) error {
	images, err := inspectRuntimeDepsImages(repository, reference, platforms)
	if err != nil {
		return err
	}
	inputs, err := digestRuntimeDepsInputs(sourceRoot)
	if err != nil {
		return err
	}
	document := runtimeDepsLock{
		SchemaVersion: runtimeDepsSchemaVersion, RegistryPullPolicy: runtimeDepsPullPolicy,
		Images: images, Inputs: inputs, Paths: canonicalRuntimeDepsPaths(),
	}
	if err := persistRuntimeDepsLock(filepath.Join(root, gateRuntimeDepsLock), document, func() error { return nil }); err != nil {
		return err
	}
	fmt.Printf("published runtime dependency OCI index %s@%s for %s from Git tree %s\n", repository, images[0].Image.OCIIndexDigest, strings.Join(platforms, ","), treeSHA)
	return nil
}

// reuseRuntimeDepsPublication 仅将已验证且与源树一致的旧索引重新标记为当前 locked tag。
func reuseRuntimeDepsPublication(root, sourceRoot, repository, reference string, toolchain toolchainLock, treeSHA string) (bool, error) {
	lock, reusable, err := loadReusableRuntimeDepsLock(sourceRoot, repository, toolchain)
	if err != nil || !reusable {
		return false, err
	}
	images, err := verifyReusableRuntimeDepsImages(lock, repository, toolchain.TargetPlatforms)
	if err != nil {
		return false, err
	}
	if err := publishRuntimeDepsIndex(repository, reference, lock.Images[0].Image.OCIIndexDigest, toolchain.TargetPlatforms); err != nil {
		return false, err
	}
	if err := verifyPublishedRuntimeDepsImages(repository, reference, toolchain.TargetPlatforms, images); err != nil {
		return false, err
	}
	if err := persistRuntimeDepsLock(filepath.Join(root, gateRuntimeDepsLock), lock, func() error { return nil }); err != nil {
		return false, err
	}
	fmt.Printf("reused runtime dependency OCI index %s@%s for Git tree %s\n", repository, images[0].Image.OCIIndexDigest, treeSHA)
	return true, nil
}

// loadReusableRuntimeDepsLock 判断暂存源树中的锁文件是否可安全复用，迁移场景则要求重新发布。
func loadReusableRuntimeDepsLock(sourceRoot, repository string, toolchain toolchainLock) (runtimeDepsLock, bool, error) {
	lock, err := readRuntimeDepsLock(filepath.Join(sourceRoot, gateRuntimeDepsLock))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errRuntimeDepsLegacySchemaV2) {
			return runtimeDepsLock{}, false, nil
		}
		return runtimeDepsLock{}, false, fmt.Errorf("read staged runtime dependency lock for reuse: %w", err)
	}
	if len(lock.Images) == 0 || lock.Images[0].Image.Registry != repository {
		return runtimeDepsLock{}, false, nil
	}
	if err := lock.validateAgainstSource(sourceRoot, toolchain); err != nil {
		if errors.Is(err, errRuntimeDepsInputsDrift) {
			return runtimeDepsLock{}, false, nil
		}
		return runtimeDepsLock{}, false, fmt.Errorf("validate staged runtime dependency lock for reuse: %w", err)
	}
	return lock, true, nil
}

func verifyReusableRuntimeDepsImages(lock runtimeDepsLock, repository string, platforms []string) ([]runtimeDepsImage, error) {
	images, err := inspectRuntimeDepsImages(repository, lock.Images[0].Image.OCIIndexDigest, platforms)
	if err != nil {
		return nil, fmt.Errorf("verify staged runtime dependency publication: %w", err)
	}
	if !sameRuntimeDepsImages(images, lock.Images) {
		return nil, errors.New("staged runtime dependency publication differs from registry evidence")
	}
	return images, nil
}

func verifyPublishedRuntimeDepsImages(repository, reference string, platforms []string, images []runtimeDepsImage) error {
	published, err := inspectRuntimeDepsImages(repository, reference, platforms)
	if err != nil {
		return err
	}
	if !sameRuntimeDepsImages(published, images) {
		return errors.New("reused runtime dependency publication identity drifted")
	}
	return nil
}

func sameRuntimeDepsImages(left, right []runtimeDepsImage) bool {
	return slices.EqualFunc(left, right, sameRuntimeDepsImage)
}

// sameRuntimeDepsImage 比较单个平台的完整锁定 OCI 身份与镜像大小。
func sameRuntimeDepsImage(left, right runtimeDepsImage) bool {
	if left.Platform != right.Platform || left.ImageSize != right.ImageSize {
		return false
	}
	if left.Image.Registry != right.Image.Registry || left.Image.OCIIndexDigest != right.Image.OCIIndexDigest {
		return false
	}
	if left.Image.PlatformManifestDigest != right.Image.PlatformManifestDigest || left.Image.ConfigDigest != right.Image.ConfigDigest {
		return false
	}
	return left.Image.OS == right.Image.OS && left.Image.Architecture == right.Image.Architecture &&
		left.Image.Variant == right.Image.Variant && slices.Equal(left.Image.RootFSDiffIDs, right.Image.RootFSDiffIDs)
}

// runDependencyRefreshCommand 在有界时间内透传 Docker/buildx 到宿主凭据存储，不注入任何认证材料。
func runDependencyRefreshCommand(timeout time.Duration, name string, arguments ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s: deadline exceeded after %s: %w", name, strings.Join(arguments, " "), timeout, ctx.Err())
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}

// commandOutputWithTimeout 为 Git 等只读命令提供带时限的捕获输出，并保留失败 stderr。
func commandOutputWithTimeout(timeout time.Duration, directory string, environment []string, name string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s: deadline exceeded after %s: %w", name, strings.Join(arguments, " "), timeout, ctx.Err())
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// persistRuntimeDepsLock 在所有发布前置条件成功后以原子替换方式持久化锁文件。
func persistRuntimeDepsLock(lockPath string, document runtimeDepsLock, prerequisite func() error) error {
	if prerequisite == nil {
		return errors.New("runtime dependency lock prerequisite is required")
	}
	if err := prerequisite(); err != nil {
		return err
	}
	data, err := encodeRuntimeDepsLock(document)
	if err != nil {
		return err
	}
	return writeAtomic(lockPath, data)
}

func encodeRuntimeDepsLock(lock runtimeDepsLock) ([]byte, error) {
	if err := lock.validateShape(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(lock); err != nil {
		return nil, fmt.Errorf("encode runtime dependency identity: %w", err)
	}
	return output.Bytes(), nil
}

func rejectTrailingDocument(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("runtime dependency identity has trailing JSON")
		}
		return fmt.Errorf("decode runtime dependency identity trailer: %w", err)
	}
	return nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validateSqruffArtifacts(artifacts []sqruffArtifact) error {
	if len(artifacts) != len(runtimeDepsPlatforms) {
		return fmt.Errorf("sqruff artifact count = %d, want %d", len(artifacts), len(runtimeDepsPlatforms))
	}
	for index, platform := range runtimeDepsPlatforms {
		if err := validateSqruffArtifact(artifacts[index], platform, index); err != nil {
			return err
		}
	}
	return nil
}

// validateSqruffArtifact 校验每个平台工件只能使用规范 release URL 和小写 SHA-256。
func validateSqruffArtifact(artifact sqruffArtifact, platform string, index int) error {
	if artifact.Platform != platform {
		return fmt.Errorf("sqruff artifact platform %q at index %d, want %q", artifact.Platform, index, platform)
	}
	if artifact.URL != canonicalSqruffURL(platform) {
		return fmt.Errorf("sqruff archive URL for %s is not the canonical v0.38.0 release", platform)
	}
	if len(artifact.SHA256) != sha256.Size*2 {
		return fmt.Errorf("sqruff archive SHA-256 for %s must be 64 lowercase hexadecimal characters", platform)
	}
	decoded, err := hex.DecodeString(artifact.SHA256)
	if err != nil || hex.EncodeToString(decoded) != artifact.SHA256 {
		return fmt.Errorf("sqruff archive SHA-256 for %s must be 64 lowercase hexadecimal characters", platform)
	}
	return nil
}

func canonicalSqruffURL(platform string) string {
	const prefix = "https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-"
	if platform == "linux/amd64" {
		return prefix + "x86_64-musl.tar.gz"
	}
	return prefix + "aarch64-musl.tar.gz"
}

func sqruffArtifactForPlatform(artifacts []sqruffArtifact, platform string) (sqruffArtifact, error) {
	if err := validateSqruffArtifacts(artifacts); err != nil {
		return sqruffArtifact{}, err
	}
	for _, artifact := range artifacts {
		if artifact.Platform == platform {
			return artifact, nil
		}
	}
	return sqruffArtifact{}, fmt.Errorf("sqruff artifact for platform %q is not locked", platform)
}
