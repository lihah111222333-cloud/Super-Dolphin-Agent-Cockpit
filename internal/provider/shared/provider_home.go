package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	SuperDolphinHomeEnv = "SUPER_DOLPHIN_HOME"
	PackagedCodexEnv    = "SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY"
	RuntimeModeEnv      = "SUPER_DOLPHIN_RUNTIME_MODE"
	RuntimeModeDev      = "dev"
	RuntimeModePackaged = "packaged"

	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

const (
	ProviderHomePermissionFailedCode = "provider_home_permission_failed"
)

// ProviderStartupGateError 表示 provider 启动前置闸门失败。
// Code 给 UI/上层路由稳定分类，字段保留 provider、路径和操作，便于定位失败点。
type ProviderStartupGateError struct {
	Code      string
	Provider  string
	Path      string
	Operation string
	Err       error
}

// Error 返回包含稳定 code 和关键上下文的错误文本。
func (e *ProviderStartupGateError) Error() string {
	if e == nil {
		return ""
	}
	message := providerStartupGateBaseMessage(e.Code)
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

func providerStartupGateBaseMessage(code string) string {
	switch code {
	case ProviderHomePermissionFailedCode:
		return code + ": provider home permission failed"
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
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve app-managed provider home realpath: %w", err)
	}
	return filepath.Clean(real), nil
}

// EnsureProviderHome 解析 provider home。
// 显式传入 homeRoot 时准备隔离 home；缺省 CLI home 只做只读诊断，不替用户创建或 chmod。
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
// 这是启动硬闸门，权限收紧失败必须返回 typed error。
func ensureExplicitProviderHome(provider, home string) (string, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "create provider home", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return "", providerHomePermissionError(provider, home, "chmod provider home", err)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", providerHomePermissionError(provider, home, "resolve provider home realpath", err)
	}
	return filepath.Clean(real), nil
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

func appManagedSuperDolphinHome() (string, error) {
	override := strings.TrimSpace(os.Getenv(SuperDolphinHomeEnv))
	if override == "" {
		return "", fmt.Errorf("%s is required for app-managed provider home", SuperDolphinHomeEnv)
	}
	return absCleanPath(override)
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
