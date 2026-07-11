package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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

const (
	ProviderHomePermissionFailedCode = "provider_home_permission_failed"
	SkillMirrorConflictCode          = "skill_mirror_conflict"
)

// ProviderStartupGateError 表示 provider 启动前置闸门失败。
// Code 给 UI/上层路由稳定分类，字段保留 provider、路径和冲突目标，便于定位失败点。
type ProviderStartupGateError struct {
	Code         string
	Provider     string
	Path         string
	Operation    string
	TargetID     string
	Scope        string
	ConflictKind string
	Count        int
	Err          error
}

// Error 返回包含稳定 code 和关键上下文的错误文本。
func (e *ProviderStartupGateError) Error() string {
	if e == nil {
		return ""
	}
	message := providerStartupGateBaseMessage(e.Code, e.Count)
	message += providerStartupGateDetails(e)
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Unwrap 保留底层 chmod/mkdir 错误，调用方可继续用 errors.Is/As 判断根因。
func (e *ProviderStartupGateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// GateCode 返回 provider 启动前置闸门的稳定错误码。
func (e *ProviderStartupGateError) GateCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func providerStartupGateBaseMessage(code string, count int) string {
	switch code {
	case ProviderHomePermissionFailedCode:
		return code + ": provider home permission failed"
	case SkillMirrorConflictCode:
		if count > 0 {
			return fmt.Sprintf("%s: skill mirror conflicts: %d unresolved", code, count)
		}
		return code + ": skill mirror conflicts"
	default:
		return code
	}
}

func providerStartupGateDetails(e *ProviderStartupGateError) string {
	fields := []struct {
		key   string
		value string
	}{
		{key: "provider", value: e.Provider},
		{key: "path", value: e.Path},
		{key: "operation", value: e.Operation},
		{key: "kind", value: e.ConflictKind},
		{key: "scope", value: e.Scope},
		{key: "target", value: e.TargetID},
	}
	details := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.value != "" {
			details = append(details, field.key+"="+field.value)
		}
	}
	if len(details) == 0 {
		return ""
	}
	return " (" + strings.Join(details, " ") + ")"
}

// AppManagedProviderHome 返回 Super Dolphin 自己管理的 provider home。
// 没有 SUPER_DOLPHIN_HOME 就报错，不要回退到用户全局目录。
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
	return "", providerHomePermissionError(provider, home, "resolve app-managed provider home realpath", err)
}

// AppManagedProviderSkillsRoot 是应用管理的 provider skills mirror。
// 写到这里的是生成物，不是真实 skill 来源。
func AppManagedProviderSkillsRoot(provider string) (string, error) {
	home, err := AppManagedProviderHome(provider)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "skills"), nil
}

// EnsureAppManagedProviderHome 确保应用托管的 provider home 已准备好。
func EnsureAppManagedProviderHome(provider string) (string, error) {
	home, err := AppManagedProviderHome(provider)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "create app-managed provider home", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "chmod app-managed provider home", err)
	}
	skillsRoot := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return "", providerHomePermissionError(provider, skillsRoot, "create app-managed provider skills root", err)
	}
	if err := os.Chmod(skillsRoot, 0o700); err != nil {
		return "", providerHomePermissionError(provider, skillsRoot, "chmod app-managed provider skills root", err)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve app-managed provider home realpath: %w", err)
	}
	return filepath.Clean(real), nil
}

// EnsureProviderHome 解析 provider home。
// 显式传入 homeRoot 时会创建 skills mirror；缺省 CLI home 只做只读诊断，不替用户创建或 chmod。
func EnsureProviderHome(provider, homeRoot string) (string, error) {
	normalizedProvider, err := normalizeAppManagedProvider(provider)
	if err != nil {
		return "", err
	}
	home, err := providerHomeRoot(normalizedProvider, homeRoot)
	if err != nil {
		return "", err
	}
	explicitHome := strings.TrimSpace(homeRoot) != ""
	if explicitHome {
		return ensureExplicitProviderHome(normalizedProvider, home)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve provider home realpath: %w", err)
	}
	return filepath.Clean(real), nil
}

// ensureExplicitProviderHome 准备用户显式指定的 provider home。
// 这是启动硬闸门，权限收紧失败必须返回 typed error，不能继续读旧 mirror。
func ensureExplicitProviderHome(provider, home string) (string, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "create provider home", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "chmod provider home", err)
	}
	skillsRoot := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return "", providerHomePermissionError(provider, skillsRoot, "create explicit provider skills root", err)
	}
	if err := os.Chmod(skillsRoot, 0o700); err != nil {
		return "", providerHomePermissionError(provider, skillsRoot, "chmod explicit provider skills root", err)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", providerHomePermissionError(provider, home, "resolve provider home realpath", err)
	}
	return filepath.Clean(real), nil
}

// ProviderMirrorTargets 返回 provider 会读取的 personal 和 project mirror。
// Codex 的 personal mirror 是 ~/.agents/skills，不是 ~/.codex。
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

// providerPersonalMirrorRoot 找到 personal mirror 的位置。
// 显式 homeRoot 用 homeRoot/skills；默认 Codex 用 ~/.agents/skills。
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

// providerProjectMirrorRoot 找到项目 mirror 的位置。
// 普通项目写 .claude/.agents；packaged 场景写到应用管理目录。
func providerProjectMirrorRoot(provider, projectRoot string) (string, error) {
	enabled, err := packagedProjectMirrorEnabled(projectRoot)
	if err != nil {
		return "", err
	}
	if enabled {
		home, err := appManagedSuperDolphinHome()
		if err != nil {
			return "", fmt.Errorf("packaged provider project mirror: %w", err)
		}
		return filepath.Join(home, "provider-mirrors", "project", provider, "skills"), nil
	}
	return providerProjectSkillsRoot(provider, projectRoot), nil
}

// packagedProjectMirrorEnabled 判断打包运行时是否启用项目 mirror。
func packagedProjectMirrorEnabled(projectRoot string) (bool, error) {
	packaged, err := PackagedRuntimeFromEnv()
	if err != nil {
		return false, err
	}
	if !packaged {
		return false, nil
	}
	// 打包运行时只保证 app 管理的 Super Dolphin home 可写。
	// Wails/macOS 启动时可能把 app bundle 或 /Library 暴露为 cwd，不能拿来创建 provider 镜像。
	resources := strings.TrimSpace(os.Getenv(ProjectRootEnv))
	if resources != "" && sameProviderPath(projectRoot, resources) {
		return true, nil
	}
	return isPackagedAppBundlePath(projectRoot) || isRootLibraryPath(projectRoot), nil
}

// PackagedRuntimeFromEnv consumes the runtime-mode contract produced by the
// runtime resolver. Empty means no packaged capability has been advertised.
// PackagedRuntimeFromEnv 从环境变量识别当前是否为打包运行时。
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

// isPackagedAppBundlePath 判断路径是否位于打包应用 bundle 内。
func isPackagedAppBundlePath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return false
	}
	for {
		if strings.HasSuffix(strings.ToLower(filepath.Base(cleaned)), ".app") {
			return true
		}
		parent := filepath.Dir(cleaned)
		if parent == cleaned {
			return false
		}
		cleaned = parent
	}
}

func isRootLibraryPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	rootLibrary := string(filepath.Separator) + "Library"
	return cleaned == rootLibrary || strings.HasPrefix(cleaned, rootLibrary+string(filepath.Separator))
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

// providerProjectRoot 把 cwd 解析成真实项目根。
// cwd 不明确就报错，不能猜一个目录去写 mirror。
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
		gitPath := filepath.Join(dir, ".git")
		ok, err := isValidGitRootMarker(gitPath)
		if err != nil {
			return "", err
		}
		if ok {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return original, nil
		}
		dir = parent
	}
}

// isValidGitRootMarker 判断目录是否是可信的 git 根标记。
func isValidGitRootMarker(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat provider project git root marker: %w", err)
	}
	if info.IsDir() {
		headInfo, err := os.Stat(filepath.Join(path, "HEAD"))
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("stat provider project git HEAD marker: %w", err)
		}
		return !headInfo.IsDir(), nil
	}
	if info.Size() > 4096 {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read provider project git root marker: %w", err)
	}
	marker := strings.TrimSpace(string(data))
	return strings.HasPrefix(strings.ToLower(marker), "gitdir:"), nil
}

// EnsureNoSkillMirrorConflicts 在 provider 启动前检查 mirror 是否能用。
// personal/user-global 内容漂移交给 UI；project/app-managed 活跃 mirror 漂移、
// 根目录不安全或发布失败会阻断启动，避免 provider 读取过期技能镜像。
func EnsureNoSkillMirrorConflicts(report contract.SkillMirrorReport) error {
	blocking := blockingSkillMirrorConflicts(report.Conflicts)
	for _, item := range report.Skipped {
		if isProviderReadableMirrorDrift(item.TargetID, item) {
			blocking = append(blocking, item)
		}
	}
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
	scope := strings.TrimSpace(first.Scope)
	target := strings.TrimSpace(first.TargetID)
	return &ProviderStartupGateError{
		Code:         SkillMirrorConflictCode,
		TargetID:     target,
		Scope:        scope,
		ConflictKind: detail,
		Count:        len(blocking),
	}
}

func blockingSkillMirrorConflicts(conflicts []contract.SkillMirrorReportItem) []contract.SkillMirrorReportItem {
	if len(conflicts) == 0 {
		return nil
	}
	blocking := make([]contract.SkillMirrorReportItem, 0, len(conflicts))
	for _, item := range conflicts {
		if contract.IsBlockingSkillMirrorConflict(item) {
			blocking = append(blocking, item)
		}
	}
	return blocking
}

func isProviderReadableMirrorDrift(target string, item contract.SkillMirrorReportItem) bool {
	if !isMirrorDriftConflictKind(item.ConflictKind) {
		return false
	}
	if strings.TrimSpace(target) != "" && strings.TrimSpace(item.TargetID) == "" {
		item.TargetID = target
	}
	return isActiveProviderMirrorTarget(item)
}

func isMirrorDriftConflictKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "drift",
		"mirror_drift",
		"multi_mirror_drift",
		"canonical_deleted_with_drift":
		return true
	default:
		return false
	}
}

func isActiveProviderMirrorTarget(item contract.SkillMirrorReportItem) bool {
	scope := strings.ToLower(strings.TrimSpace(item.Scope))
	targetID := strings.ToLower(strings.TrimSpace(item.TargetID))
	return scope == "project" ||
		strings.Contains(targetID, ":project:") ||
		strings.Contains(targetID, ":app-managed:")
}

func providerHomePermissionError(provider, path, operation string, err error) error {
	return &ProviderStartupGateError{
		Code:      ProviderHomePermissionFailedCode,
		Provider:  strings.TrimSpace(provider),
		Path:      filepath.Clean(strings.TrimSpace(path)),
		Operation: strings.TrimSpace(operation),
		Err:       err,
	}
}

func absCleanPath(path string) (string, error) {
	return absCleanPathExpanded(path)
}

// absCleanPathExpanded 展开并清理路径为绝对路径。
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
