package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	SuperDolphinHomeEnv = "SUPER_DOLPHIN_HOME"
	ProjectRootEnv      = "PROJECT_ROOT"
	PackagedCodexEnv    = "SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY"
	RuntimeModeEnv      = "SUPER_DOLPHIN_RUNTIME_MODE"
	RuntimeModeDev      = "dev"
	RuntimeModePackaged = "packaged"

	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

func AppManagedProviderHome(provider string) (string, error) {
	provider, err := normalizeAppManagedProvider(provider)
	if err != nil {
		return "", err
	}
	base, err := appManagedSuperDolphinHome()
	if err != nil {
		return "", err
	}
	home := filepath.Clean(filepath.Join(base, "providers", provider))
	real, err := filepath.EvalSymlinks(home)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if os.IsNotExist(err) {
		return home, nil
	}
	return "", fmt.Errorf("resolve app-managed provider home realpath: %w", err)
}

func AppManagedProviderSkillsRoot(provider string) (string, error) {
	home, err := AppManagedProviderHome(provider)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "skills"), nil
}

func EnsureAppManagedProviderHome(provider string) (string, error) {
	home, err := AppManagedProviderHome(provider)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create app-managed provider home: %w", err)
	}
	_ = os.Chmod(home, 0o700)
	skillsRoot := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return "", fmt.Errorf("create app-managed provider skills root: %w", err)
	}
	_ = os.Chmod(skillsRoot, 0o700)
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve app-managed provider home realpath: %w", err)
	}
	return filepath.Clean(real), nil
}

func EnsureProviderHome(provider, homeRoot string) (string, error) {
	normalizedProvider, err := normalizeAppManagedProvider(provider)
	if err != nil {
		return "", err
	}
	home, err := providerHomeRoot(normalizedProvider, homeRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create provider home: %w", err)
	}
	_ = os.Chmod(home, 0o700)
	if strings.TrimSpace(homeRoot) != "" {
		skillsRoot := filepath.Join(home, "skills")
		if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
			return "", fmt.Errorf("create explicit provider skills root: %w", err)
		}
		_ = os.Chmod(skillsRoot, 0o700)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve provider home realpath: %w", err)
	}
	return filepath.Clean(real), nil
}

func ProviderMirrorTargets(provider, cwd string, homeRoot ...string) ([]contract.SkillProviderMirrorTarget, error) {
	provider, err := normalizeAppManagedProvider(provider)
	if err != nil {
		return nil, err
	}
	rawHome := ""
	if len(homeRoot) > 0 {
		rawHome = homeRoot[0]
	}
	projectRoot, err := providerProjectRoot(cwd)
	if err != nil {
		return nil, err
	}
	projectSkillsRoot, err := providerProjectMirrorRoot(provider, projectRoot)
	if err != nil {
		return nil, err
	}
	allowExplicitHome := strings.TrimSpace(rawHome) != ""
	home, skillsRoot, err := providerPersonalMirrorRoot(provider, rawHome)
	if err != nil {
		return nil, err
	}
	return []contract.SkillProviderMirrorTarget{
		{
			Provider:          provider,
			HomeRoot:          home,
			SkillsRoot:        skillsRoot,
			AllowExplicitHome: allowExplicitHome,
		},
		{
			Provider:   provider,
			HomeRoot:   home,
			SkillsRoot: projectSkillsRoot,
		},
	}, nil
}

func normalizeAppManagedProvider(provider string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderClaude:
		return ProviderClaude, nil
	case ProviderCodex:
		return ProviderCodex, nil
	default:
		return "", fmt.Errorf("unsupported app-managed provider %q", provider)
	}
}

func providerHomeRoot(provider, homeRoot string) (string, error) {
	if strings.TrimSpace(homeRoot) != "" {
		return absCleanPathExpanded(homeRoot)
	}
	return defaultProviderCLIHome(provider)
}

func defaultProviderCLIHome(provider string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	switch provider {
	case ProviderClaude:
		return absCleanPath(filepath.Join(home, ".claude"))
	case ProviderCodex:
		return absCleanPath(filepath.Join(home, ".codex"))
	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func providerPersonalMirrorRoot(provider, homeRoot string) (string, string, error) {
	if strings.TrimSpace(homeRoot) != "" {
		home, err := providerHomeRoot(provider, homeRoot)
		if err != nil {
			return "", "", err
		}
		return home, filepath.Join(home, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve user home: %w", err)
	}
	switch provider {
	case ProviderClaude:
		root, err := absCleanPath(filepath.Join(home, ".claude"))
		if err != nil {
			return "", "", err
		}
		return root, filepath.Join(root, "skills"), nil
	case ProviderCodex:
		root, err := absCleanPath(filepath.Join(home, ".agents"))
		if err != nil {
			return "", "", err
		}
		return root, filepath.Join(root, "skills"), nil
	default:
		return "", "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func providerProjectSkillsRoot(provider, projectRoot string) string {
	switch provider {
	case ProviderClaude:
		return filepath.Join(projectRoot, ".claude", "skills")
	case ProviderCodex:
		return filepath.Join(projectRoot, ".agents", "skills")
	default:
		return filepath.Join(projectRoot, "."+provider, "skills")
	}
}

func providerProjectMirrorRoot(provider, projectRoot string) (string, error) {
	enabled, err := packagedProjectMirrorEnabled(projectRoot)
	if err != nil {
		return "", err
	}
	if enabled {
		return filepath.Join(os.Getenv(SuperDolphinHomeEnv), "provider-mirrors", "project", provider, "skills"), nil
	}
	return providerProjectSkillsRoot(provider, projectRoot), nil
}

func packagedProjectMirrorEnabled(projectRoot string) (bool, error) {
	packaged, err := PackagedRuntimeFromEnv()
	if err != nil {
		return false, err
	}
	if !packaged {
		return false, nil
	}
	home := strings.TrimSpace(os.Getenv(SuperDolphinHomeEnv))
	resources := strings.TrimSpace(os.Getenv(ProjectRootEnv))
	return home != "" && resources != "" && sameProviderPath(projectRoot, resources), nil
}

// PackagedRuntimeFromEnv consumes the runtime-mode contract produced by the
// runtime resolver. Empty means no packaged capability has been advertised.
func PackagedRuntimeFromEnv() (bool, error) {
	mode := strings.TrimSpace(os.Getenv(RuntimeModeEnv))
	switch mode {
	case "":
		return false, nil
	case RuntimeModeDev:
		return false, nil
	case RuntimeModePackaged:
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s %q", RuntimeModeEnv, mode)
	}
}

func sameProviderPath(left, right string) bool {
	leftAbs, errLeft := filepath.Abs(strings.TrimSpace(left))
	rightAbs, errRight := filepath.Abs(strings.TrimSpace(right))
	if errLeft != nil || errRight != nil {
		return false
	}
	leftClean := filepath.Clean(leftAbs)
	rightClean := filepath.Clean(rightAbs)
	leftReal, errLeft := filepath.EvalSymlinks(leftClean)
	rightReal, errRight := filepath.EvalSymlinks(rightClean)
	if errLeft == nil {
		leftClean = filepath.Clean(leftReal)
	}
	if errRight == nil {
		rightClean = filepath.Clean(rightReal)
	}
	return leftClean == rightClean
}

func appManagedSuperDolphinHome() (string, error) {
	override := strings.TrimSpace(os.Getenv(SuperDolphinHomeEnv))
	if override == "" {
		return "", fmt.Errorf("%s is required for app-managed provider home", SuperDolphinHomeEnv)
	}
	return absCleanPath(override)
}

func providerProjectRoot(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("provider project cwd is required")
	}
	cleaned := filepath.Clean(cwd)
	if cleaned == "." || !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("provider project cwd must be absolute: %s", cwd)
	}
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve provider project cwd realpath: %w", err)
	}
	root, err := nearestGitRoot(filepath.Clean(real))
	if err != nil {
		return "", err
	}
	return root, nil
}

func nearestGitRoot(dir string) (string, error) {
	original := dir
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return original, nil
		}
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat provider project git root marker: %w", err)
		}
		dir = parent
	}
}

func EnsureNoSkillMirrorConflicts(report contract.SkillMirrorReport) error {
	blocking := blockingSkillMirrorConflicts(report.Conflicts)
	if len(blocking) == 0 {
		return nil
	}
	first := blocking[0]
	detail := strings.TrimSpace(first.ConflictKind)
	if detail == "" {
		detail = strings.TrimSpace(first.TargetID)
	}
	if detail == "" {
		return fmt.Errorf("skill mirror conflicts: %d unresolved", len(blocking))
	}
	return fmt.Errorf("skill mirror conflicts: %d unresolved (%s)", len(blocking), detail)
}

func blockingSkillMirrorConflicts(conflicts []contract.SkillMirrorReportItem) []contract.SkillMirrorReportItem {
	if len(conflicts) == 0 {
		return nil
	}
	blocking := make([]contract.SkillMirrorReportItem, 0, len(conflicts))
	for _, item := range conflicts {
		if isBlockingSkillMirrorConflict(item) {
			blocking = append(blocking, item)
		}
	}
	return blocking
}

func isBlockingSkillMirrorConflict(item contract.SkillMirrorReportItem) bool {
	// Ordinary content conflicts are surfaced in the skill UI for user resolution.
	// Provider startup only fails closed when the mirror root itself is unsafe or unusable.
	switch strings.ToLower(strings.TrimSpace(item.ConflictKind)) {
	case "same_name",
		"same_name_scope_conflict",
		"drift",
		"mirror_drift",
		"multi_mirror_drift",
		"canonical_deleted_with_drift",
		"unmanaged",
		"unmanaged_same_name",
		"unmanaged_provider_skill":
		return false
	case "publish_error",
		"publish_targets_unconfigured",
		"mirror_root_symlink":
		return true
	default:
		return true
	}
}

func absCleanPath(path string) (string, error) {
	return absCleanPathExpanded(path)
}

func absCleanPathExpanded(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("provider home must be absolute after expansion: %s", path)
	}
	return filepath.Clean(path), nil
}
