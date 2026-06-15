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
	processRoleEnv       = "SUPER_DOLPHIN_PROCESS_ROLE"
	runtimeModeEnv       = "SUPER_DOLPHIN_RUNTIME_MODE"
	packageRootEnv       = "SUPER_DOLPHIN_PACKAGE_ROOT"
	packagedLauncherEnv  = "SUPER_DOLPHIN_PACKAGED_LAUNCHER"
	trustedDevEntrypoint = "SUPER_DOLPHIN_TRUSTED_DEV_ENTRYPOINT"

	runtimeManifestName = "runtime-manifest.json"
)

type ProcessRole string

const (
	ProcessRoleOwner   ProcessRole = "owner"
	ProcessRoleSidecar ProcessRole = "sidecar"
)

type RuntimeMode string

const (
	RuntimeModeDev      RuntimeMode = "dev"
	RuntimeModePackaged RuntimeMode = "packaged"
)

type RuntimeCapabilities struct {
	BundledCodex    bool
	BundledLSP      bool
	BundledSidecars bool
}

type RuntimeResolveInput struct {
	GOOS           string
	GOARCH         string
	Env            map[string]string
	ExecutablePath string
	UserHome       string
}

type ResolvedRuntime struct {
	ProcessRole      ProcessRole
	RuntimeMode      RuntimeMode
	PackagedRuntime  *PackagedRuntime
	Capabilities     RuntimeCapabilities
	RuntimeManifest  string
	PackageResources string
}

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

func resolveOwnerRuntime(input RuntimeResolveInput, env map[string]string) (ResolvedRuntime, error) {
	goos := firstNonEmpty(input.GOOS, runtime.GOOS)
	goarch := firstNonEmpty(input.GOARCH, runtime.GOARCH)
	resources, hasBundleShape := ownerPackageResources(input, env, goos)
	explicitPackaged := ownerHasExplicitPackagedIntent(env)
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

func ownerPackageResources(input RuntimeResolveInput, env map[string]string, goos string) (string, bool) {
	if root := strings.TrimSpace(env[packageRootEnv]); root != "" {
		return root, true
	}
	resources := packagedResourcesDirForOS(goos, input.ExecutablePath)
	if goos == "darwin" || resources != "" {
		return resources, resources != ""
	}
	return "", false
}

func hasBundledSidecarSentinel(resources string) bool {
	return requireBundledSidecars(filepath.Join(resources, "bin")) == nil
}

func ownerHasExplicitPackagedIntent(env map[string]string) bool {
	if strings.TrimSpace(env[packageRootEnv]) != "" {
		return true
	}
	if envEnabled(env[packagedLauncherEnv]) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(env[runtimeModeEnv]), string(RuntimeModePackaged))
}

func trustedDevEntrypointEnabled(env map[string]string) bool {
	return envEnabled(env[trustedDevEntrypoint]) && strings.TrimSpace(env[packageRootEnv]) == ""
}

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

func devOwnerRuntime() ResolvedRuntime {
	return ResolvedRuntime{ProcessRole: ProcessRoleOwner, RuntimeMode: RuntimeModeDev}
}

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

func packagedCapabilities() RuntimeCapabilities {
	return RuntimeCapabilities{
		BundledCodex:    true,
		BundledLSP:      true,
		BundledSidecars: true,
	}
}

func envEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runtimeGOOS() string {
	return runtime.GOOS
}

func runtimeGOARCH() string {
	return runtime.GOARCH
}
