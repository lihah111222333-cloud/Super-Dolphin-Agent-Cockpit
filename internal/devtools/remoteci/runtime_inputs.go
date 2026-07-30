package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func runtimeDependencyPathsV5() []string {
	return []string{
		"build/gate/runtime-deps.Dockerfile",
		"build/gate/toolchain.lock",
		"go.mod",
		"go.sum",
		"internal/devtools/nilnessrunner/runner.go",
		"scripts/nilness_guard.go",
		"frontend-app/package-lock.json",
		"build/gate/runtime-lsp/package-lock.json",
		"build/gate/runtime-proxy/go.mod",
		"build/gate/runtime-proxy/go.sum",
		"build/gate/runtime-tools/go.mod",
		"build/gate/runtime-tools/go.sum",
		"internal/devtools/gate/executor_seed.go",
	}
}

func runtimeDependencyPathsV6() []string {
	return append(runtimeDependencyPathsV5(),
		"cmd/super-dolphin-gate/remote_refresh_seed.go",
	)
}

func runtimeDependencyPaths() []string {
	return append(runtimeDependencyPathsV6(),
		"cmd/super-dolphin-gate/remote_refresh_seed_script.go",
	)
}

type runtimeDependencyLock struct {
	SchemaVersion string            `json:"schema_version"`
	Inputs        map[string]string `json:"inputs"`
	Paths         map[string]string `json:"paths"`
	BuildMode     string            `json:"build_mode"`
	CacheScope    string            `json:"cache_scope"`
}

type runtimeToolchainLock struct {
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
	toolchain, err := loadRuntimeToolchainLock(byPath)
	if err != nil {
		return "", nil, err
	}
	buildArgs, err := runtimeDependencyBuildArgs(toolchain, platform)
	if err != nil {
		return "", nil, err
	}
	return runtimeLockDigest(lock), buildArgs, nil
}

// ResolveAcceptedRuntimeDependencyDigest 只为已验收基线重算依赖摘要，并显式支持 v5/v6 迁移。
func ResolveAcceptedRuntimeDependencyDigest(entries []sourceexport.TreeEntry, platform string) (string, error) {
	if !validRuntimePlatform(platform) {
		return "", errors.New("runtime dependency platform is invalid")
	}
	byPath := runtimeTreeByPath(entries)
	lock, err := decodeRuntimeDependencyLock(byPath)
	if err != nil {
		return "", err
	}
	paths := runtimeDependencyPaths()
	switch lock.SchemaVersion {
	case "7":
	case "6":
		paths = runtimeDependencyPathsV6()
	case "5":
		paths = runtimeDependencyPathsV5()
	default:
		return "", errors.New("accepted runtime dependency lock schema is unsupported")
	}
	if len(lock.Inputs) != len(paths) {
		return "", errors.New("accepted runtime dependency lock shape is invalid")
	}
	if err := verifyRuntimeDependencyInputs(byPath, lock, paths); err != nil {
		return "", err
	}
	toolchain, err := loadRuntimeToolchainLock(byPath)
	if err != nil {
		return "", err
	}
	if _, err := runtimeDependencyBuildArgs(toolchain, platform); err != nil {
		return "", err
	}
	return runtimeLockDigest(lock), nil
}

// validRuntimePlatform 判断 runtime seed 的目标平台是否受锁文件支持。
func validRuntimePlatform(platform string) bool {
	return platform == "linux/amd64" || platform == "linux/arm64"
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
	if lock.SchemaVersion != "7" || len(lock.Inputs) != len(runtimeDependencyPaths()) {
		return runtimeDependencyLock{}, errors.New("runtime dependency lock shape is invalid")
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
		if lock.Inputs[runtimeDependencyLockField(path)] != remoteBytesDigest(data) {
			return fmt.Errorf("runtime dependency input %s drifted from lock", path)
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

// runtimeLockDigest 返回排序后输入摘要的稳定身份。
func runtimeLockDigest(lock runtimeDependencyLock) string {
	ordered := make([]string, 0, len(lock.Inputs))
	for name, digest := range lock.Inputs {
		ordered = append(ordered, name+"="+digest)
	}
	sort.Strings(ordered)
	return remoteBytesDigest([]byte(strings.Join(ordered, "\n") + "\n"))
}

// runtimeDependencyBuildArgs 校验工具链镜像和 Sqruff 工件并生成 Docker 参数。
func runtimeDependencyBuildArgs(lock runtimeToolchainLock, platform string) ([]string, error) {
	if lock.SchemaVersion != "1" {
		return nil, errors.New("runtime toolchain schema is invalid")
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

// runtimeBaseImages 校验基础镜像并返回按名称索引的不可变引用。
func runtimeBaseImages(lock runtimeToolchainLock) (map[string]string, error) {
	images := make(map[string]string, len(lock.BaseImages))
	for _, image := range lock.BaseImages {
		if strings.TrimSpace(image.Name) == "" || !strings.Contains(image.Reference, "@sha256:") {
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

func runtimeDependencyLockField(path string) string {
	fields := map[string]string{
		"build/gate/runtime-deps.Dockerfile": "dockerfile_sha256",
		"build/gate/toolchain.lock":          "toolchain_lock_sha256",
		"go.mod":                             "go_mod_sha256",
		"go.sum":                             "go_sum_sha256",
		"internal/devtools/nilnessrunner/runner.go":            "nilness_runner_sha256",
		"scripts/nilness_guard.go":                             "nilness_guard_sha256",
		"frontend-app/package-lock.json":                       "frontend_package_lock_sha256",
		"build/gate/runtime-lsp/package-lock.json":             "lsp_package_lock_sha256",
		"build/gate/runtime-proxy/go.mod":                      "proxy_go_mod_sha256",
		"build/gate/runtime-proxy/go.sum":                      "proxy_go_sum_sha256",
		"build/gate/runtime-tools/go.mod":                      "tools_go_mod_sha256",
		"build/gate/runtime-tools/go.sum":                      "tools_go_sum_sha256",
		"internal/devtools/gate/executor_seed.go":              "runtime_seed_worker_sha256",
		"cmd/super-dolphin-gate/remote_refresh_seed.go":        "runtime_seed_recipe_sha256",
		"cmd/super-dolphin-gate/remote_refresh_seed_script.go": "runtime_seed_script_sha256",
	}
	return fields[path]
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
