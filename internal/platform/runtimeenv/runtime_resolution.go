package runtimeenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// 运行时解析相关环境变量和打包清单文件名。
	processRoleEnv       = "SUPER_DOLPHIN_PROCESS_ROLE"
	runtimeModeEnv       = "SUPER_DOLPHIN_RUNTIME_MODE"
	packageRootEnv       = "SUPER_DOLPHIN_PACKAGE_ROOT"
	packagedLauncherEnv  = "SUPER_DOLPHIN_PACKAGED_LAUNCHER"
	trustedDevEntrypoint = "SUPER_DOLPHIN_TRUSTED_DEV_ENTRYPOINT"

	runtimeManifestName = "runtime-manifest.json"
)

// ProcessRole 区分桌面 owner 进程和由 owner 拉起的 sidecar 进程。
type ProcessRole string

const (
	// 支持的进程角色；空环境变量按 owner 处理。
	ProcessRoleOwner   ProcessRole = "owner"
	ProcessRoleSidecar ProcessRole = "sidecar"
)

// RuntimeMode 是 owner 与 sidecar 通过环境变量传递的运行时模式 contract。
type RuntimeMode string

const (
	// 支持的运行模式；sidecar 必须由父进程显式传入其中之一。
	RuntimeModeDev      RuntimeMode = "dev"
	RuntimeModePackaged RuntimeMode = "packaged"
)

// RuntimeCapabilities 暴露 packaged 模式已验证的随包能力，调用方据此决定是否要求外部工具。
type RuntimeCapabilities struct {
	BundledCodex    bool // 包内是否提供 Codex CLI。
	BundledLSP      bool // 包内是否提供 LSP bundle。
	BundledSidecars bool // 包内是否提供 mcp-orch/mcp-lsp/mcp-ida。
}

// RuntimeResolveInput 汇总运行时解析所需的系统、环境和路径信息。
type RuntimeResolveInput struct {
	GOOS           string            // 目标系统；为空时使用 runtime.GOOS。
	GOARCH         string            // 目标架构；为空时使用 runtime.GOARCH。
	Env            map[string]string // 待解析环境；nil 时读取当前进程环境。
	ExecutablePath string            // owner 可执行文件路径，用于推断打包资源根。
	UserHome       string            // 用户 home，用于派生打包数据目录。
}

// ResolvedRuntime 是 owner/sidecar 运行时解析后的结果。
type ResolvedRuntime struct {
	ProcessRole      ProcessRole         // 当前进程角色。
	RuntimeMode      RuntimeMode         // dev 或 packaged。
	PackagedRuntime  *PackagedRuntime    // packaged 模式下的资源路径；dev 模式为 nil。
	Capabilities     RuntimeCapabilities // packaged 模式的包内能力声明。
	RuntimeManifest  string              // 已校验的 runtime-manifest.json 路径。
	PackageResources string              // 打包资源根目录。
}

// runtimeManifest 映射 runtime-manifest.json，字段必须指向包根内的固定资源。
type runtimeManifest struct {
	BundledCodexPath  string `json:"bundled_codex_path"`
	BundledGoplsPath  string `json:"bundled_gopls_path"`
	LSPBundlePath     string `json:"lsp_bundle_path"`
	LSPManifestPath   string `json:"lsp_manifest_path"`
	ModelRegistryPath string `json:"model_registry_path"`
}

// ResolveRuntime 根据进程角色、运行模式和包资源解析当前运行时。
func ResolveRuntime(input RuntimeResolveInput) (ResolvedRuntime, error) {
	env := input.Env
	if env == nil {
		env = environmentMap(os.Environ())
	}
	role, err := resolveProcessRole(env[processRoleEnv])
	if err != nil {
		return ResolvedRuntime{}, err
	}
	if role == ProcessRoleSidecar {
		return resolveSidecarRuntime(input, env)
	}
	return resolveOwnerRuntime(input, env)
}

// resolveProcessRole 解析父进程传入的角色，空值保持桌面 owner 兼容。
func resolveProcessRole(value string) (ProcessRole, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "owner", "desktop":
		return ProcessRoleOwner, nil
	case "sidecar":
		return ProcessRoleSidecar, nil
	default:
		return "", fmt.Errorf("invalid process role %q", value)
	}
}

// resolveSidecarRuntime 校验父进程启动 sidecar 时必须提供的运行时 contract。
func resolveSidecarRuntime(input RuntimeResolveInput, env map[string]string) (ResolvedRuntime, error) {
	mode, err := parseRuntimeMode(env[runtimeModeEnv])
	if err != nil {
		return ResolvedRuntime{}, fmt.Errorf("parent launch contract: %w", err)
	}
	if err := requireSidecarResourceContract(mode, env); err != nil {
		return ResolvedRuntime{}, err
	}
	resolved := ResolvedRuntime{ProcessRole: ProcessRoleSidecar, RuntimeMode: mode}
	if mode == RuntimeModePackaged {
		resources := firstNonEmpty(env[packageRootEnv], env[projectRootEnv])
		runtime := packagedRuntimeFromResources(resources, input.UserHome)
		resolved.PackagedRuntime = &runtime
		resolved.PackageResources = resources
		resolved.Capabilities = packagedCapabilities()
	}
	return resolved, nil
}

// requireSidecarResourceContract 对 sidecar 的资源根做 fail-fast 校验。
// dev 模式依赖 PROJECT_ROOT，packaged 模式依赖包资源根；缺任一项都不静默猜测。
func requireSidecarResourceContract(mode RuntimeMode, env map[string]string) error {
	switch mode {
	case RuntimeModeDev:
		if strings.TrimSpace(env[projectRootEnv]) == "" {
			return fmt.Errorf("parent launch contract: missing %s for dev sidecar", projectRootEnv)
		}
	case RuntimeModePackaged:
		if strings.TrimSpace(firstNonEmpty(env[packageRootEnv], env[projectRootEnv])) == "" {
			return fmt.Errorf("parent launch contract: missing packaged resource root")
		}
	}
	return nil
}

// resolveOwnerRuntime 解析桌面 owner 进程的运行时。
// 显式 packaged 意图必须走完整 manifest 校验；只有可信开发入口或无包形态时才返回 dev。
func resolveOwnerRuntime(input RuntimeResolveInput, env map[string]string) (ResolvedRuntime, error) {
	goos := firstNonEmpty(input.GOOS, runtime.GOOS)
	goarch := firstNonEmpty(input.GOARCH, runtime.GOARCH)
	resources, hasBundleShape, pathErr := ownerPackageResources(input, env, goos)
	explicitPackaged := ownerHasExplicitPackagedIntent(env)
	if explicitPackaged && pathErr != nil {
		return ResolvedRuntime{}, pathErr
	}
	if explicitPackaged {
		return resolvePackagedOwner(resources, input.UserHome, goos, goarch)
	}
	if trustedDevEntrypointEnabled(env) {
		return devOwnerRuntime(), nil
	}
	if hasBundleShape {
		manifestPath := filepath.Join(resources, runtimeManifestName)
		if _, err := os.Stat(manifestPath); err == nil {
			return resolvePackagedOwner(resources, input.UserHome, goos, goarch)
		} else if !os.IsNotExist(err) {
			return ResolvedRuntime{}, fmt.Errorf("stat runtime manifest %s: %w", manifestPath, err)
		}
		if hasBundledSidecarSentinel(resources) {
			return resolvePackagedOwner(resources, input.UserHome, goos, goarch)
		}
	}
	return devOwnerRuntime(), nil
}

// ownerPackageResources 找出 owner 可用的包资源根，并标记是否呈现打包布局。
// 若平台不支持 packaged 路径自动推断（如 Linux），pathErr 会向上返回给调用方在显式 packaged 意图时 fail-fast。
func ownerPackageResources(input RuntimeResolveInput, env map[string]string, goos string) (resources string, hasBundleShape bool, pathErr error) {
	if root := strings.TrimSpace(env[packageRootEnv]); root != "" {
		return root, true, nil
	}
	resources, pathErr = packagedResourcesDirForOS(goos, input.ExecutablePath)
	if pathErr != nil {
		return "", false, pathErr
	}
	if goos == "darwin" || resources != "" {
		return resources, resources != "", nil
	}
	return "", false, nil
}

// hasBundledSidecarSentinel 用包内 sidecar 可执行文件作为旧包布局的探针。
func hasBundledSidecarSentinel(resources string) bool {
	return requireBundledSidecars(filepath.Join(resources, "bin")) == nil
}

// ownerHasExplicitPackagedIntent 判断环境是否强制要求 packaged 解析。
func ownerHasExplicitPackagedIntent(env map[string]string) bool {
	if strings.TrimSpace(env[packageRootEnv]) != "" {
		return true
	}
	if envEnabled(env[packagedLauncherEnv]) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(env[runtimeModeEnv]), string(RuntimeModePackaged))
}

// trustedDevEntrypointEnabled 只允许显式开发入口绕过包资源自动探测。
func trustedDevEntrypointEnabled(env map[string]string) bool {
	return envEnabled(env[trustedDevEntrypoint]) && strings.TrimSpace(env[packageRootEnv]) == ""
}

// resolvePackagedOwner 校验 manifest 后返回完整 packaged owner 运行时。
func resolvePackagedOwner(resources, userHome, goos, goarch string) (ResolvedRuntime, error) {
	if strings.TrimSpace(resources) == "" {
		return ResolvedRuntime{}, fmt.Errorf("runtime manifest requires packaged resource root")
	}
	manifestPath, err := verifyRuntimeManifest(resources, goos, goarch)
	if err != nil {
		return ResolvedRuntime{}, err
	}
	runtime := packagedRuntimeFromResourcesForOS(goos, resources, userHome)
	return ResolvedRuntime{
		ProcessRole:      ProcessRoleOwner,
		RuntimeMode:      RuntimeModePackaged,
		PackagedRuntime:  &runtime,
		Capabilities:     packagedCapabilities(),
		RuntimeManifest:  manifestPath,
		PackageResources: resources,
	}, nil
}

// devOwnerRuntime 返回不携带包资源的开发 owner 运行时。
func devOwnerRuntime() ResolvedRuntime {
	return ResolvedRuntime{ProcessRole: ProcessRoleOwner, RuntimeMode: RuntimeModeDev}
}

// parseRuntimeMode 解析父进程 contract 中的运行模式，缺失或未知值直接报错。
func parseRuntimeMode(value string) (RuntimeMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dev":
		return RuntimeModeDev, nil
	case "packaged":
		return RuntimeModePackaged, nil
	default:
		return "", fmt.Errorf("missing or invalid %s", runtimeModeEnv)
	}
}

// verifyRuntimeManifest 校验打包清单和每个资源路径，防止包根外资源被信任。
func verifyRuntimeManifest(resources, goos, goarch string) (string, error) {
	manifestPath := filepath.Join(resources, runtimeManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("runtime manifest %s: %w", manifestPath, err)
	}
	var manifest runtimeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parse runtime manifest %s: %w", manifestPath, err)
	}
	checks := []struct {
		label string
		value string
		want  string
		kind  string
	}{
		{"bundled_codex_path", manifest.BundledCodexPath, filepath.Join("bin", executableNameForOS(goos, "codex")), "exec"},
		{"bundled_gopls_path", manifest.BundledGoplsPath, filepath.Join("bin", executableNameForOS(goos, "gopls")), "exec"},
		{"lsp_bundle_path", manifest.LSPBundlePath, lspBundleName, "dir"},
		{"lsp_manifest_path", manifest.LSPManifestPath, filepath.Join(lspBundleName, lspManifestName), "file"},
		{"model_registry_path", manifest.ModelRegistryPath, modelRegistryBundle, "file"},
	}
	for _, check := range checks {
		if err := verifyManifestResource(resources, check.label, check.value, check.want, check.kind, goos); err != nil {
			return "", err
		}
	}
	return manifestPath, nil
}

// verifyManifestResource 校验 manifest 单项资源的相对路径、位置和文件类型。
func verifyManifestResource(resources, label, value, want, kind, goos string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("runtime manifest missing %s", label)
	}
	rel, err := cleanManifestRelativePath(label, value)
	if err != nil {
		return err
	}
	if rel != filepath.Clean(want) {
		return fmt.Errorf("runtime manifest %s mismatch: expected %s, got %s", label, want, value)
	}
	fullPath := filepath.Join(resources, rel)
	if err := requirePathInsideRoot(resources, fullPath); err != nil {
		return fmt.Errorf("runtime manifest %s %w", label, err)
	}
	return requireManifestPathKind(fullPath, kind, goos)
}

// cleanManifestRelativePath 校验并清理 runtime manifest 中的相对路径。
func cleanManifestRelativePath(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return "", fmt.Errorf("runtime manifest %s must be a relative path under package root: %s", label, value)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runtime manifest %s escapes package root: %s", label, value)
	}
	return clean, nil
}

// requirePathInsideRoot 解析符号链接后的真实路径，确保资源仍在包根内。
func requirePathInsideRoot(root, path string) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("root cannot be resolved: %w", err)
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootReal, pathReal)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("escapes package root: %s", path)
	}
	return nil
}

// requireManifestPathKind 校验 manifest 资源的文件类型和可执行权限。
func requireManifestPathKind(path, kind, goos string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	switch kind {
	case "exec":
		if err := requireExecutableFileForOS(goos, path); err != nil {
			return fmt.Errorf("points to non-executable path: %s", path)
		}
	case "file":
		if info.IsDir() {
			return fmt.Errorf("points to non-file path: %s", path)
		}
	case "dir":
		if !info.IsDir() {
			return fmt.Errorf("points to non-directory path: %s", path)
		}
	default:
		return fmt.Errorf("unknown runtime manifest resource kind: %s", kind)
	}
	return nil
}

// packagedCapabilities 返回当前发行包必须同时具备的运行能力。
func packagedCapabilities() RuntimeCapabilities {
	return RuntimeCapabilities{
		BundledCodex:    true,
		BundledLSP:      true,
		BundledSidecars: true,
	}
}

// envEnabled 解析常见布尔环境变量表示。
func envEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// environmentMap 将 os.Environ 形态转换为 map，保留最后一次出现的键。
func environmentMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

// firstNonEmpty 返回第一个非空白字符串，用于环境变量优先级选择。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// runtimeGOOS 包装 runtime.GOOS，方便测试替换相关解析输入。
func runtimeGOOS() string {
	return runtime.GOOS
}

// runtimeGOARCH 包装 runtime.GOARCH，方便测试替换相关解析输入。
func runtimeGOARCH() string {
	return runtime.GOARCH
}
