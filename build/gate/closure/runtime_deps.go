package gateclosure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeDepsSchemaVersion = "9"
	runtimeDepsBuildMode     = "node-local"
	runtimeDepsCacheScope    = "node"
)

var runtimeDepsPlatforms = []string{"linux/amd64", "linux/arm64"}

var errRuntimeDepsInputsDrift = errors.New("runtime dependency lock inputs drifted")

type runtimeDepsLock struct {
	SchemaVersion string            `json:"schema_version"`
	BuildMode     string            `json:"build_mode"`
	CacheScope    string            `json:"cache_scope"`
	Inputs        runtimeDepsInputs `json:"inputs"`
	Paths         runtimeDepsPaths  `json:"paths"`
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
	RuntimeSeedWorker   string `json:"runtime_seed_worker_sha256"`
	RuntimeSeedRecipe   string `json:"runtime_seed_recipe_sha256"`
	RuntimeSeedScript   string `json:"runtime_seed_script_sha256"`
	RuntimeSeedBrowser  string `json:"runtime_seed_script_browser_sha256"`
	RuntimeSeedRuntime  string `json:"runtime_seed_script_runtime_sha256"`
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

func canonicalRuntimeDepsPaths() runtimeDepsPaths {
	return runtimeDepsPaths{
		Manifest: "/opt/super-dolphin-gate/runtime/manifest.json", Vendor: "/opt/super-dolphin-gate/runtime/vendor",
		GoModuleProxy: "/opt/super-dolphin-gate/runtime/go-proxy", FrontendNodeModules: "/opt/super-dolphin-gate/runtime/frontend/node_modules",
		PlaywrightBrowsers: "/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright", LSPNodeModules: "/opt/super-dolphin-gate/runtime/lsp/node_modules",
		SQLC: "/opt/super-dolphin-gate/runtime/bin/sqlc", Ripgrep: "/opt/super-dolphin-gate/runtime/bin/rg", Sqruff: "/opt/super-dolphin-gate/runtime/bin/sqruff",
		Gopls: "/usr/local/bin/gopls", Go: "/usr/local/go/bin/go", Node: "/usr/local/bin/node", NPM: "/usr/local/bin/npm",
		Git: "/usr/bin/git", Make: "/usr/bin/make",
	}
}

// readRuntimeDepsLock 严格解码 node-local 依赖锁，拒绝旧 schema、未知字段和尾随数据。
func readRuntimeDepsLock(lockPath string) (runtimeDepsLock, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return runtimeDepsLock{}, fmt.Errorf("read runtime dependency lock: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var lock runtimeDepsLock
	if err := decoder.Decode(&lock); err != nil {
		return runtimeDepsLock{}, fmt.Errorf("decode runtime dependency lock: %w", err)
	}
	if err := rejectTrailingDocument(decoder); err != nil {
		return runtimeDepsLock{}, err
	}
	if err := lock.validateShape(); err != nil {
		return runtimeDepsLock{}, err
	}
	return lock, nil
}

// validateShape 校验运行时依赖锁的模式、输入摘要和容器内路径契约。
func (lock runtimeDepsLock) validateShape() error {
	if lock.SchemaVersion != runtimeDepsSchemaVersion || lock.BuildMode != runtimeDepsBuildMode || lock.CacheScope != runtimeDepsCacheScope {
		return errors.New("runtime dependency lock schema, build mode, or cache scope is invalid")
	}
	if err := validateRuntimeDepsInputDigests(lock.Inputs); err != nil {
		return err
	}
	if lock.Paths != canonicalRuntimeDepsPaths() {
		return errors.New("runtime dependency paths drifted from the executor contract")
	}
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

type runtimeDepsInputField struct{ name, digest string }

func runtimeDepsInputFields(inputs runtimeDepsInputs) []runtimeDepsInputField {
	return []runtimeDepsInputField{
		{"dockerfile", inputs.Dockerfile}, {"toolchain lock", inputs.ToolchainLock}, {"go.mod", inputs.GoMod}, {"go.sum", inputs.GoSum},
		{"nilness runner", inputs.NilnessRunner}, {"nilness guard", inputs.NilnessGuard}, {"frontend package lock", inputs.FrontendPackageLock},
		{"LSP package lock", inputs.LSPPackageLock}, {"proxy go.mod", inputs.ProxyGoMod}, {"proxy go.sum", inputs.ProxyGoSum},
		{"tools go.mod", inputs.ToolsGoMod}, {"tools go.sum", inputs.ToolsGoSum}, {"runtime seed worker", inputs.RuntimeSeedWorker},
		{"runtime seed recipe", inputs.RuntimeSeedRecipe}, {"runtime seed script", inputs.RuntimeSeedScript},
		{"runtime seed script browser", inputs.RuntimeSeedBrowser},
		{"runtime seed script runtime", inputs.RuntimeSeedRuntime},
	}
}

// validateAgainstSource binds the node-local cache inputs to the exact source tree.
func (lock runtimeDepsLock) validateAgainstSource(sourceRoot string, toolchain toolchainLock) error {
	if err := lock.validateShape(); err != nil {
		return err
	}
	if toolchain.NetworkPolicy != "none" {
		return errors.New("normal truth image network policy must be none")
	}
	wanted, err := digestRuntimeDepsInputs(sourceRoot)
	if err != nil {
		return err
	}
	if wanted != lock.Inputs {
		return errRuntimeDepsInputsDrift
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
		{gateRuntimeDepsDocker, &inputs.Dockerfile}, {gateToolchain, &inputs.ToolchainLock}, {"go.mod", &inputs.GoMod}, {"go.sum", &inputs.GoSum},
		{"internal/devtools/nilnessrunner/runner.go", &inputs.NilnessRunner}, {"scripts/nilness_guard.go", &inputs.NilnessGuard},
		{"frontend-app/package-lock.json", &inputs.FrontendPackageLock}, {gateRuntimeLSPLock, &inputs.LSPPackageLock},
		{gateRuntimeProxyModule, &inputs.ProxyGoMod}, {gateRuntimeProxySum, &inputs.ProxyGoSum}, {gateRuntimeToolsModule, &inputs.ToolsGoMod}, {gateRuntimeToolsSum, &inputs.ToolsGoSum},
		{"internal/devtools/gate/executor_seed.go", &inputs.RuntimeSeedWorker},
		{"cmd/super-dolphin-gate/remote_refresh_seed.go", &inputs.RuntimeSeedRecipe},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script.go", &inputs.RuntimeSeedScript},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go", &inputs.RuntimeSeedBrowser},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go", &inputs.RuntimeSeedRuntime},
	}
}

func digestRuntimeDepsFile(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", fmt.Errorf("read runtime dependency input %s: %w", name, err)
	}
	return digestBytes(data), nil
}

// RefreshDependencyClosure 仅从指定 Git 树刷新节点本地运行时依赖锁。
func RefreshDependencyClosure(tree string) error {
	if tree == "" || strings.ContainsAny(tree, " \t\r\n") {
		return errors.New("runtime dependency tree is required")
	}
	rootOutput, err := commandOutput("", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root := strings.TrimSpace(rootOutput)
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
	inputs, err := digestRuntimeDepsInputs(sourceRoot)
	if err != nil {
		return err
	}
	lock := runtimeDepsLock{SchemaVersion: runtimeDepsSchemaVersion, BuildMode: runtimeDepsBuildMode, CacheScope: runtimeDepsCacheScope, Inputs: inputs, Paths: canonicalRuntimeDepsPaths()}
	if err := persistRuntimeDepsLock(filepath.Join(root, gateRuntimeDepsLock), lock); err != nil {
		return err
	}
	fmt.Printf("refreshed node-local runtime dependency lock from Git tree %s\n", treeSHA)
	return nil
}

func persistRuntimeDepsLock(lockPath string, document runtimeDepsLock) error {
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
		return nil, fmt.Errorf("encode runtime dependency lock: %w", err)
	}
	return output.Bytes(), nil
}

func rejectTrailingDocument(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("runtime dependency lock has trailing JSON")
		}
		return fmt.Errorf("decode runtime dependency lock trailer: %w", err)
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

// validateSqruffArtifact 校验单个平台 sqruff 产物的固定来源和摘要格式。
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
