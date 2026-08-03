package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

// RuntimeDependencySchemaVersion 是当前 runtime seed 构建合同版本。
const RuntimeDependencySchemaVersion = "14"

// runtimeDependencyRecipePaths 返回须审计但不标识可复用运行时依赖内容的 seed 控制面输入。
func runtimeDependencyRecipePaths() []string {
	return []string{
		"internal/devtools/gate/executor_seed.go",
	}
}

// runtimeDependencyPaths 返回当前 schema 14 OCI runtime 的实际内容依赖；seed worker 仅作配方审计。
func runtimeDependencyPaths() []string {
	return []string{
		"build/gate/runtime-deps.Dockerfile",
		"build/gate/toolchain.lock",
		"go.mod",
		"go.sum",
		"internal/devtools/godistribution/go-distribution.lock",
		"internal/devtools/nilnessrunner/runner.go",
		"scripts/nilness_guard.go",
		"frontend-app/package-lock.json",
		"build/gate/runtime-lsp/package-lock.json",
		"build/gate/runtime-proxy/go.mod",
		"build/gate/runtime-proxy/go.sum",
		"build/gate/runtime-tools/go.mod",
		"build/gate/runtime-tools/go.sum",
	}
}

type runtimeDependencyLock struct {
	SchemaVersion string            `json:"schema_version"`
	Inputs        map[string]string `json:"inputs"`
	RecipeInputs  map[string]string `json:"recipe_inputs"`
	Paths         map[string]string `json:"paths"`
	BuildMode     string            `json:"build_mode"`
	CacheScope    string            `json:"cache_scope"`
}

type runtimeGoModuleManifest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type runtimeDependencyIdentity struct {
	SchemaVersion     uint32                    `json:"schema_version"`
	LockDigest        string                    `json:"lock_digest"`
	GoModuleManifests []runtimeGoModuleManifest `json:"go_module_manifests"`
}

type runtimeToolchainLock struct {
	SchemaVersion      string   `json:"schema_version"`
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
		GoVersion       string `json:"go_version"`
		NodeVersion     string `json:"node_version"`
		NPMVersion      string `json:"npm_version"`
		PythonVersion   string `json:"python_version"`
		Ripgrep         string `json:"ripgrep"`
		Sqruff          string `json:"sqruff"`
		Gopls           string `json:"gopls"`
		SQLC            string `json:"sqlc"`
		SqruffArtifacts []struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
			SHA256   string `json:"sha256"`
		} `json:"sqruff_artifacts"`
		NPMLSPPackages []string `json:"npm_lsp_packages"`
	} `json:"runtime_tools"`
	NetworkPolicy string `json:"network_policy"`
}

// ResolveRuntimeDependencyBuild 校验精确 Git 树中的依赖锁并返回 runtime seed 构建参数。
func ResolveRuntimeDependencyBuild(entries []sourceexport.TreeEntry, platform string) (string, []string, error) {
	if !validRuntimePlatform(platform) {
		return "", nil, errors.New("runtime dependency platform is invalid")
	}
	byPath := runtimeTreeByPath(entries)
	lock, err := loadRuntimeDependencyLock(byPath)
	if err != nil {
		return "", nil, err
	}
	if err := verifyRuntimeDependencyInputs(byPath, lock); err != nil {
		return "", nil, err
	}
	return resolveRuntimeDependencyBuildFromLock(entries, byPath, lock, platform)
}

// resolveRuntimeDependencyBuildFromLock 由已按对应 schema 校验的锁生成统一构建身份。
func resolveRuntimeDependencyBuildFromLock(
	entries []sourceexport.TreeEntry,
	byPath map[string][]byte,
	lock runtimeDependencyLock,
	platform string,
) (string, []string, error) {
	toolchain, err := loadRuntimeToolchainLock(byPath)
	if err != nil {
		return "", nil, err
	}
	buildArgs, err := runtimeDependencyBuildArgs(toolchain, platform)
	if err != nil {
		return "", nil, err
	}
	digest, err := resolvedRuntimeDependencyDigest(lock, entries)
	if err != nil {
		return "", nil, err
	}
	return digest, buildArgs, nil
}

// validRuntimePlatform 判断 runtime seed 是否保留官方 artifact 支持的平台。
func validRuntimePlatform(platform string) bool {
	return platform == cicontract.TargetPlatform || platform == "linux/arm64"
}

// runtimeTreeByPath 将精确 Git tree 条目索引为路径到字节内容。
func runtimeTreeByPath(entries []sourceexport.TreeEntry) map[string][]byte {
	byPath := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry.Data
	}
	return byPath
}

// loadRuntimeDependencyLock 严格读取 runtime 依赖摘要锁。
func loadRuntimeDependencyLock(byPath map[string][]byte) (runtimeDependencyLock, error) {
	lock, err := decodeRuntimeDependencyLock(byPath)
	if err != nil {
		return runtimeDependencyLock{}, err
	}
	if lock.SchemaVersion != RuntimeDependencySchemaVersion || len(lock.Inputs) != len(runtimeDependencyPaths()) ||
		len(lock.RecipeInputs) != len(runtimeDependencyRecipePaths()) {
		return runtimeDependencyLock{}, errors.New("runtime dependency lock shape is invalid")
	}
	if err := verifyRuntimeDependencyRecipeInputs(byPath, lock); err != nil {
		return runtimeDependencyLock{}, err
	}
	return lock, nil
}

func decodeRuntimeDependencyLock(byPath map[string][]byte) (runtimeDependencyLock, error) {
	data, ok := byPath["build/gate/runtime-deps.lock"]
	if !ok {
		return runtimeDependencyLock{}, errors.New("runtime dependency lock is missing from Git tree")
	}
	var lock runtimeDependencyLock
	if err := decodeRemoteStrictJSON(data, &lock); err != nil {
		return runtimeDependencyLock{}, fmt.Errorf("decode runtime dependency lock: %w", err)
	}
	return lock, nil
}

// verifyRuntimeDependencyInputs 校验锁文件中每个声明输入都来自当前 Git tree。
func verifyRuntimeDependencyInputs(byPath map[string][]byte, lock runtimeDependencyLock, paths ...[]string) error {
	wanted := runtimeDependencyPaths()
	if len(paths) == 1 {
		wanted = paths[0]
	}
	for _, path := range wanted {
		data, exists := byPath[path]
		if !exists {
			return fmt.Errorf("runtime dependency input %s is missing from Git tree", path)
		}
		field := runtimeDependencyLockField(lock.SchemaVersion, path)
		if field == "" {
			return fmt.Errorf("runtime dependency input %s has no schema %s lock field", path, lock.SchemaVersion)
		}
		if lock.Inputs[field] != remoteBytesDigest(data) {
			return fmt.Errorf("runtime dependency input %s drifted from lock", path)
		}
	}
	return nil
}

// verifyRuntimeDependencyRecipeInputs 保持 seed 配方可审计，同时不让协调器控制面改动使不可变依赖缓存失效。
func verifyRuntimeDependencyRecipeInputs(byPath map[string][]byte, lock runtimeDependencyLock) error {
	for _, path := range runtimeDependencyRecipePaths() {
		data, exists := byPath[path]
		if !exists {
			return fmt.Errorf("runtime dependency recipe input %s is missing from Git tree", path)
		}
		field := runtimeDependencyRecipeLockField(path)
		if field == "" || lock.RecipeInputs[field] != remoteBytesDigest(data) {
			return fmt.Errorf("runtime dependency recipe input %s drifted from lock", path)
		}
	}
	return nil
}

// loadRuntimeToolchainLock 严格读取工具链锁文件。
func loadRuntimeToolchainLock(byPath map[string][]byte) (runtimeToolchainLock, error) {
	data, ok := byPath["build/gate/toolchain.lock"]
	if !ok {
		return runtimeToolchainLock{}, errors.New("runtime toolchain lock is missing from Git tree")
	}
	var lock runtimeToolchainLock
	if err := decodeRemoteStrictJSON(data, &lock); err != nil {
		return runtimeToolchainLock{}, fmt.Errorf("decode runtime toolchain lock: %w", err)
	}
	return lock, nil
}

// runtimeLockDigest 返回可复用运行时内容的稳定身份；seed 控制面实现由候选 CLI 身份单独绑定，不得使依赖缓存失效。
func runtimeLockDigest(lock runtimeDependencyLock) string {
	ordered := make([]string, 0, len(lock.Inputs))
	for name, digest := range lock.Inputs {
		if lock.SchemaVersion == "11" && name == "runtime_seed_worker_sha256" {
			continue
		}
		switch name {
		case "runtime_seed_recipe_sha256",
			"go_mod_sha256", "go_sum_sha256", "proxy_go_mod_sha256", "proxy_go_sum_sha256",
			"tools_go_mod_sha256", "tools_go_sum_sha256":
			continue
		}
		ordered = append(ordered, name+"="+digest)
	}
	sort.Strings(ordered)
	return remoteBytesDigest([]byte(strings.Join(ordered, "\n") + "\n"))
}

// resolvedRuntimeDependencyDigest 把固定配方锁与候选树全部 Go 模块清单绑定为运行时身份。
func resolvedRuntimeDependencyDigest(lock runtimeDependencyLock, entries []sourceexport.TreeEntry) (string, error) {
	manifests, err := runtimeGoModuleManifests(entries)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(runtimeDependencyIdentity{
		SchemaVersion: 1, LockDigest: runtimeLockDigest(lock), GoModuleManifests: manifests,
	})
	if err != nil {
		return "", fmt.Errorf("encode runtime dependency identity: %w", err)
	}
	return remoteBytesDigest(append(payload, '\n')), nil
}

// runtimeGoModuleManifests 自动索引精确候选树中的全部 go.mod 和 go.sum。
func runtimeGoModuleManifests(entries []sourceexport.TreeEntry) ([]runtimeGoModuleManifest, error) {
	manifests := make([]runtimeGoModuleManifest, 0)
	seen := make(map[string]struct{})
	hasRoot := false
	for _, entry := range entries {
		if !isRuntimeGoModuleManifest(entry.Path) {
			continue
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return nil, fmt.Errorf("runtime Go module manifest %q is duplicated", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		hasRoot = hasRoot || entry.Path == "go.mod"
		data := entry.Data
		if strings.HasSuffix(entry.Path, "go.mod") {
			lines := strings.Split(string(data), "\n")
			for index, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "go ") {
					lines[index] = ""
				}
			}
			data = []byte(strings.Join(lines, "\n"))
		}
		manifests = append(manifests, runtimeGoModuleManifest{Path: entry.Path, Digest: remoteBytesDigest(data)})
	}
	if !hasRoot {
		return nil, errors.New("runtime dependency tree is missing root go.mod")
	}
	sort.Slice(manifests, func(left, right int) bool { return manifests[left].Path < manifests[right].Path })
	return manifests, nil
}

func isRuntimeGoModuleManifest(path string) bool {
	return path == "go.mod" || path == "go.sum" || strings.HasSuffix(path, "/go.mod") || strings.HasSuffix(path, "/go.sum")
}

// runtimeDependencyBuildArgs 校验工具链镜像和 Sqruff 工件并生成 Docker 参数。
func runtimeDependencyBuildArgs(lock runtimeToolchainLock, platform string) ([]string, error) {
	if lock.SchemaVersion != "1" {
		return nil, errors.New("runtime toolchain schema is invalid")
	}
	if err := validateRuntimeToolVersions(lock); err != nil {
		return nil, err
	}
	images, err := runtimeBaseImages(lock)
	if err != nil {
		return nil, err
	}
	arguments, matches := []string{"GO_IMAGE=" + images["GO_IMAGE"], "NODE_IMAGE=" + images["NODE_IMAGE"]}, 0
	for _, artifact := range lock.RuntimeTools.SqruffArtifacts {
		args, match, err := sqruffBuildArgs(artifact, platform)
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, args...)
		matches += match
	}
	if matches != 1 {
		return nil, errors.New("runtime toolchain target Sqruff artifact is missing")
	}
	return arguments, nil
}

func validateRuntimeToolVersions(lock runtimeToolchainLock) error {
	actual := []string{lock.RuntimeTools.GoVersion, lock.RuntimeTools.NodeVersion, lock.RuntimeTools.NPMVersion, lock.RuntimeTools.PythonVersion, lock.RuntimeTools.Ripgrep, lock.RuntimeTools.Sqruff, lock.RuntimeTools.Gopls, lock.RuntimeTools.SQLC}
	expected := []string{"go1.26.5", "v24.18.0", "11.16.0", "3.11.2", "/opt/super-dolphin-gate/runtime/bin/rg@13.0.0-4+b2", "/opt/super-dolphin-gate/runtime/bin/sqruff@0.38.0", "golang.org/x/tools/gopls@v0.22.0", "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0"}
	if !slices.Equal(actual, expected) {
		return errors.New("runtime toolchain versions are not the accepted exact set")
	}
	return nil
}

// runtimeBaseImages 校验基础镜像并返回按名称索引的不可变引用。
func runtimeBaseImages(lock runtimeToolchainLock) (map[string]string, error) {
	images := make(map[string]string, len(lock.BaseImages))
	for _, image := range lock.BaseImages {
		if strings.TrimSpace(image.Name) == "" || !validRemoteImageReference(image.Reference) {
			return nil, errors.New("runtime toolchain base image is invalid")
		}
		images[image.Name] = image.Reference
	}
	if images["GO_IMAGE"] == "" || images["NODE_IMAGE"] == "" {
		return nil, errors.New("runtime toolchain base images are incomplete")
	}
	return images, nil
}

// sqruffBuildArgs 校验一个 Sqruff 工件并返回其 Docker 参数及目标平台匹配数。
func sqruffBuildArgs(artifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}, platform string) ([]string, int, error) {
	architecture := strings.TrimPrefix(artifact.Platform, "linux/")
	if (architecture != "amd64" && architecture != "arm64") || strings.TrimSpace(artifact.URL) == "" || len(artifact.SHA256) != sha256.Size*2 || !remoteOIDPattern.MatchString(artifact.SHA256) {
		return nil, 0, errors.New("runtime toolchain Sqruff artifact is invalid")
	}
	return []string{"SQRUFF_ARCHIVE_URL_" + strings.ToUpper(architecture) + "=" + artifact.URL, "SQRUFF_ARCHIVE_SHA256_" + strings.ToUpper(architecture) + "=" + artifact.SHA256}, boolCount(artifact.Platform == platform), nil
}

// boolCount 将平台匹配布尔值转换为计数。
func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func runtimeDependencyLockField(schemaVersion, path string) string {
	if schemaVersion == "4" {
		switch path {
		case "build/gate/cmd/runtime-seed-manifest/main.go":
			return "manifest_builder_sha256"
		case "internal/devtools/gate/executor_seed.go":
			return "manifest_api_sha256"
		}
	}
	fields := map[string]string{
		"build/gate/runtime-deps.Dockerfile": "dockerfile_sha256",
		"build/gate/toolchain.lock":          "toolchain_lock_sha256",
		"go.mod":                             "go_mod_sha256",
		"go.sum":                             "go_sum_sha256",
		"internal/devtools/godistribution/go-distribution.lock": "go_distribution_lock_sha256",
		"internal/devtools/nilnessrunner/runner.go":             "nilness_runner_sha256",
		"scripts/nilness_guard.go":                              "nilness_guard_sha256",
		"frontend-app/package-lock.json":                        "frontend_package_lock_sha256",
		"build/gate/runtime-lsp/package-lock.json":              "lsp_package_lock_sha256",
		"build/gate/runtime-proxy/go.mod":                       "proxy_go_mod_sha256",
		"build/gate/runtime-proxy/go.sum":                       "proxy_go_sum_sha256",
		"build/gate/runtime-tools/go.mod":                       "tools_go_mod_sha256",
		"build/gate/runtime-tools/go.sum":                       "tools_go_sum_sha256",
		"internal/devtools/gate/executor_seed.go":               "runtime_seed_worker_sha256",
	}
	if field := fields[path]; field != "" {
		return field
	}
	if schemaVersion != RuntimeDependencySchemaVersion {
		return runtimeDependencyRecipeLockField(path)
	}
	return ""
}

func runtimeDependencyRecipeLockField(path string) string {
	return map[string]string{
		"internal/devtools/gate/executor_seed.go": "runtime_seed_worker_sha256",
	}[path]
}

func decodeRemoteStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON document has trailing data")
		}
		return err
	}
	return nil
}

func remoteBytesDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
