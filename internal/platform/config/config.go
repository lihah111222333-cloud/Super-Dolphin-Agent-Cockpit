package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// 这些别名保留 platform/config 的旧导入面；字段定义以 internal/contract 为准。
type (
	Config           = contract.Config
	SkillConfig      = contract.SkillConfig
	AgentConfig      = contract.AgentConfig
	NotifyConfig     = contract.NotifyConfig
	LSPConfig        = contract.LSPConfig
	DependencyConfig = contract.DependencyConfig
)

const (
	DependencyProfileDesktopHost = contract.DependencyProfileDesktopHost
	DependencyProfileProduction  = contract.DependencyProfileProduction
	DependencyProfileTest        = contract.DependencyProfileTest

	DependencyBootstrapDesktopHost = contract.DependencyBootstrapDesktopHost
	DependencyBootstrapProduction  = contract.DependencyBootstrapProduction
	DependencyBootstrapTest        = contract.DependencyBootstrapTest
)

type parsedEnvConfig struct {
	skillProgressiveDisclosure bool
	skillTokenBudget           int
	persistentSubagentDefault  bool
	notifyAllowPrivateCIDR     bool
	notifyTimeoutSeconds       int
	notifyQueueCapacity        int
	notifyDrainSeconds         int
	lsp                        contract.LSPConfig
}

// New 创建平台配置并解析 SQLite 运行时路径。
func New() (*Config, error) {
	projectRoot, err := PrimeProcessEnvironment()
	if err != nil {
		return nil, err
	}
	sqlitePath, err := resolveSQLitePath(projectRoot)
	if err != nil {
		return nil, err
	}
	envCfg, err := parseConfigEnv()
	if err != nil {
		return nil, err
	}
	profile, err := dependencyProfileFromEnv()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		SQLitePath:  sqlitePath,
		RPCAddr:     envOrCompat("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR", "127.0.0.1:8090"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		ProjectRoot: projectRoot,
		Skill: SkillConfig{
			ProgressiveDisclosure: envCfg.skillProgressiveDisclosure,
			TokenBudget:           envCfg.skillTokenBudget,
		},
		Agent: AgentConfig{
			PersistentSubagentDefault: envCfg.persistentSubagentDefault,
		},
		Notify: NotifyConfig{
			ChannelsJSON:     os.Getenv("NOTIFY_CHANNELS_JSON"),
			AllowPrivateCIDR: envCfg.notifyAllowPrivateCIDR,
			TimeoutSeconds:   envCfg.notifyTimeoutSeconds,
			QueueCapacity:    envCfg.notifyQueueCapacity,
			DrainSeconds:     envCfg.notifyDrainSeconds,
		},
		LSP:        envCfg.lsp,
		Dependency: DependencyConfig{Profile: profile},
	}
	if err := exportRPCAddrIfMissing(os.Setenv, cfg.RPCAddr); err != nil {
		return nil, err
	}
	return cfg, nil
}

func dependencyProfileFromEnv() (contract.DependencyProfile, error) {
	bootstrap, err := dependencyBootstrapModeFromEnv()
	if err != nil {
		return "", err
	}
	return resolveDependencyProfile(os.Getenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE"), bootstrap)
}

// resolveDependencyProfile 根据 bootstrap 模式解析 profile；生产默认必须显式声明，避免缺依赖时静默走桌面或测试路径。
func resolveDependencyProfile(raw string, bootstrap contract.DependencyBootstrapMode) (contract.DependencyProfile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		switch bootstrap {
		case contract.DependencyBootstrapDesktopHost:
			return contract.DependencyProfileDesktopHost, nil
		case contract.DependencyBootstrapTest:
			return contract.DependencyProfileTest, nil
		default:
			return "", fmt.Errorf("SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for %s bootstrap", bootstrap)
		}
	}

	profile := contract.DependencyProfile(raw)
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileProduction, contract.DependencyProfileTest:
		if profile == contract.DependencyProfileTest && bootstrap != contract.DependencyBootstrapTest {
			return "", fmt.Errorf("test dependency profile is allowed only with test bootstrap")
		}
		if bootstrap == contract.DependencyBootstrapProduction && profile != contract.DependencyProfileProduction {
			return "", fmt.Errorf("%s dependency profile is not allowed for production bootstrap", profile)
		}
		return profile, nil
	default:
		return "", fmt.Errorf("invalid SUPER_DOLPHIN_DEPENDENCY_PROFILE %q", raw)
	}
}

// dependencyBootstrapModeFromEnv 解析当前进程的 dependency bootstrap，测试模式只能由 Go test 显式打开。
func dependencyBootstrapModeFromEnv() (contract.DependencyBootstrapMode, error) {
	return dependencyBootstrapMode(
		os.Getenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP"),
		os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE"),
		os.Getenv("SUPER_DOLPHIN_PROCESS_ROLE"),
		runningUnderGoTest,
	)
}

func dependencyBootstrapMode(raw string, runtimeMode string, role string, goTestBinary func() bool) (contract.DependencyBootstrapMode, error) {
	raw = strings.TrimSpace(raw)
	packaged := strings.EqualFold(strings.TrimSpace(runtimeMode), "packaged")
	role = strings.TrimSpace(role)
	if raw != "" {
		return explicitDependencyBootstrapMode(raw, packaged, role, goTestBinary)
	}
	if packaged {
		return contract.DependencyBootstrapProduction, nil
	}
	return dependencyBootstrapModeForProcessRole(role), nil
}

func explicitDependencyBootstrapMode(raw string, packaged bool, role string, goTestBinary func() bool) (contract.DependencyBootstrapMode, error) {
	switch raw {
	case "test":
		if !testDependencyBootstrapAllowed(packaged, role, goTestBinary) {
			return "", errors.New("test dependency bootstrap is allowed only in Go test binaries")
		}
		return contract.DependencyBootstrapTest, nil
	case "desktop_host":
		return contract.DependencyBootstrapDesktopHost, nil
	case "production":
		return contract.DependencyBootstrapProduction, nil
	default:
		return "", fmt.Errorf("invalid SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP %q", raw)
	}
}

func testDependencyBootstrapAllowed(packaged bool, role string, goTestBinary func() bool) bool {
	return !packaged && role != "sidecar" && goTestBinary()
}

func dependencyBootstrapModeForProcessRole(role string) contract.DependencyBootstrapMode {
	switch role {
	case "desktop":
		return contract.DependencyBootstrapDesktopHost
	case "sidecar":
		return contract.DependencyBootstrapProduction
	default:
		return contract.DependencyBootstrapProduction
	}
}

func runningUnderGoTest() bool {
	return strings.HasSuffix(filepath.Base(os.Args[0]), ".test")
}

// parseConfigEnv 解析会影响运行行为的环境变量。
// 缺失或空白值使用默认值；显式非法值直接返回错误，防止启动时静默降级。
func parseConfigEnv() (parsedEnvConfig, error) {
	var cfg parsedEnvConfig
	var err error
	if cfg.skillProgressiveDisclosure, err = envBoolOr("SKILL_PROGRESSIVE_DISCLOSURE", false); err != nil {
		return cfg, err
	}
	if cfg.skillTokenBudget, err = envPositiveIntOr("SKILL_TOKEN_BUDGET", 3000); err != nil {
		return cfg, err
	}
	if cfg.persistentSubagentDefault, err = envBoolOr("PERSISTENT_SUBAGENT_DEFAULT", true); err != nil {
		return cfg, err
	}
	if cfg.notifyAllowPrivateCIDR, err = envBoolOr("NOTIFY_ALLOW_PRIVATE_CIDR", false); err != nil {
		return cfg, err
	}
	if cfg.notifyTimeoutSeconds, err = envPositiveIntOr("NOTIFY_TIMEOUT_SECONDS", 10); err != nil {
		return cfg, err
	}
	if cfg.notifyQueueCapacity, err = envPositiveIntOr("NOTIFY_QUEUE_CAPACITY", 512); err != nil {
		return cfg, err
	}
	if cfg.notifyDrainSeconds, err = envPositiveIntOr("NOTIFY_DRAIN_SECONDS", 5); err != nil {
		return cfg, err
	}
	cfg.lsp, err = lspConfigFromEnv()
	return cfg, err
}

// PrimeProcessEnvironment 加载进程环境并返回项目根目录。
func PrimeProcessEnvironment() (string, error) {
	projectRoot := resolveProjectRoot()
	if err := validateTrustedDevRuntimeMode(projectRoot); err != nil {
		return "", err
	}
	if err := loadDotEnv(projectRoot); err != nil {
		return "", err
	}
	return projectRoot, nil
}

func validateTrustedDevRuntimeMode(projectRoot string) error {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "dev") {
		return nil
	}
	packaged, err := hasPackagedRuntimeManifest(projectRoot)
	if err != nil {
		return err
	}
	if !packaged || trustedDevEntrypoint(os.Getenv("SUPER_DOLPHIN_DEV_ENTRYPOINT")) {
		return nil
	}
	return fmt.Errorf("SUPER_DOLPHIN_RUNTIME_MODE=dev requires a trusted dev entrypoint and cannot downgrade packaged root %q", projectRoot)
}

func trustedDevEntrypoint(value string) bool {
	switch strings.TrimSpace(value) {
	case "run-new-ui-desktop.sh", "run-new-ui-desktop.ps1", "goland", "make run-agent-terminal-debug", "make run-agent-terminal-debug-plain":
		return true
	default:
		return false
	}
}

func loadDotEnv(projectRoot string) error {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return nil
	}
	packaged, err := hasPackagedRuntimeManifest(projectRoot)
	if err != nil {
		return err
	}
	path := filepath.Join(projectRoot, ".env")
	content, readFailure := os.ReadFile(path)
	if readFailure != nil {
		if packaged {
			return fmt.Errorf("load packaged .env %s: %s", redactPath(path), securefs.SafeErrorForPath(readFailure, path))
		}
		return nil
	}
	return applyDotEnv(os.Setenv, path, string(content), packaged)
}

// applyDotEnv 将 .env 内容写入当前进程环境，已有环境变量保持调用方显式配置优先。
// 只要 .env 文件存在，解析错误就会阻断启动，避免关键配置行被静默跳过。
func applyDotEnv(setenv func(string, string) error, path, content string, strict bool) error {
	for i, line := range strings.Split(content, "\n") {
		key, value, ok, err := parseDotEnvLineStrict(line, i+1)
		if err != nil {
			prefix := "parse .env"
			if strict {
				prefix = "parse packaged .env"
			}
			return fmt.Errorf("%s %s: %w", prefix, redactPath(path), err)
		}
		if !ok || strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		if err := setenv(key, value); err != nil {
			return fmt.Errorf("set environment from .env %s for %s: %w", redactPath(path), key, err)
		}
	}
	return nil
}

// parseDotEnvLineStrict 解析单行 key=value，并返回空行或注释行的跳过标记。
// lineNumber 只用于错误定位，调用方决定解析失败是阻断启动还是兼容跳过。
func parseDotEnvLineStrict(line string, lineNumber int) (string, string, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false, dotEnvLineError(lineNumber, "missing key=value separator")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false, dotEnvLineError(lineNumber, "invalid key")
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return key, value, true, nil
}

func dotEnvLineError(lineNumber int, reason string) error {
	if lineNumber > 0 {
		return fmt.Errorf("line %d: %s", lineNumber, reason)
	}
	return fmt.Errorf("%s", reason)
}

func hasPackagedRuntimeManifest(projectRoot string) (bool, error) {
	path := filepath.Join(strings.TrimSpace(projectRoot), "runtime-manifest.json")
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect packaged runtime manifest %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
}

// exportRPCAddrIfMissing 在进程环境中补齐 GO_AGENT_CTL_RPC_ADDR。
// 只有规范环境变量和兼容旧变量都为空时才写入，确保 MCP 子进程继承已解析的 RPC 地址。
func exportRPCAddrIfMissing(setenv func(string, string) error, addr string) error {
	if strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR")) != "" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("RPC_ADDR")) != "" {
		return nil
	}
	if err := setenv("GO_AGENT_CTL_RPC_ADDR", addr); err != nil {
		return fmt.Errorf("set GO_AGENT_CTL_RPC_ADDR: %w", err)
	}
	return nil
}

// resolveProjectRoot 解析运行时项目根目录。
// PROJECT_ROOT 优先；打包二进制只接受带 SQLite migrations 的安装根，否则回退到当前工作目录。
func resolveProjectRoot() string {
	if root := strings.TrimSpace(os.Getenv("PROJECT_ROOT")); root != "" {
		return root
	}
	if exe, err := os.Executable(); err == nil {
		if root := resolvePackagedProjectRoot(exe); root != "" {
			if hasPackagedProjectRootMigrationsDir(root) {
				return root
			}
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

func hasPackagedProjectRootMigrationsDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"))
	return err == nil && info.IsDir()
}

// resolveSQLitePath 解析当前运行时使用的 SQLite 数据库路径。
// 显式环境变量必须通过校验；未显式配置时按打包模式和项目根推导默认路径。
func resolveSQLitePath(projectRoot string) (string, error) {
	publicRaw, hasPublic := os.LookupEnv(contract.SQLitePathEnvKey)
	internalRaw, hasInternal := os.LookupEnv(contract.InternalSQLitePathEnvKey)
	if hasPublic || hasInternal {
		return resolveExplicitSQLitePath(publicRaw, hasPublic, internalRaw, hasInternal)
	}

	home := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	if home == "" && strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "packaged") {
		resolved, err := resolvePackagedSQLiteHome(runtime.GOOS, os.Getenv, os.UserHomeDir)
		if err != nil {
			return "", err
		}
		home = resolved
	}
	if home == "" {
		if strings.TrimSpace(projectRoot) == "" {
			return "", fmt.Errorf("SQLite path requires PROJECT_ROOT or SUPER_DOLPHIN_HOME")
		}
		home = filepath.Join(projectRoot, ".super-dolphin")
	}
	return validateSQLitePath(filepath.Join(home, "super-dolphin.db"), "")
}

// resolveExplicitSQLitePath 校验公开和内部 SQLite 路径环境变量，并拒绝两者指向不同数据库。
// 返回值已经过清理和父目录检查，可直接交给数据库初始化流程。
func resolveExplicitSQLitePath(publicRaw string, hasPublic bool, internalRaw string, hasInternal bool) (string, error) {
	publicPath := ""
	if hasPublic {
		var err error
		publicPath, err = validateSQLitePath(publicRaw, contract.SQLitePathEnvKey)
		if err != nil {
			return "", err
		}
	}
	internalPath := ""
	if hasInternal {
		var err error
		internalPath, err = validateSQLitePath(internalRaw, contract.InternalSQLitePathEnvKey)
		if err != nil {
			return "", err
		}
	}
	if hasPublic && hasInternal && publicPath != internalPath {
		return "", fmt.Errorf("conflicting SQLite path env %s=%s and %s=%s",
			contract.SQLitePathEnvKey, redactPath(publicPath),
			contract.InternalSQLitePathEnvKey, redactPath(internalPath))
	}
	if hasPublic {
		return publicPath, nil
	}
	return internalPath, nil
}

// validateSQLitePath 规范化 SQLite 数据库路径，并在路径为空、指向目录或父目录不可用时立即报错。
func validateSQLitePath(path string, explicitKey string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if explicitKey != "" {
			return "", fmt.Errorf("%s resolves to an empty SQLite path", explicitKey)
		}
		return "", fmt.Errorf("resolved SQLite path is empty")
	}
	clean, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(clean)
	if err := validateSQLiteParent(clean, parent); err != nil {
		return "", err
	}
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return "", fmt.Errorf("SQLite database path points to a directory: %s", redactPath(clean))
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect SQLite database path %s: %s", redactPath(clean), securefs.SafeErrorForPath(err, clean))
	}
	return clean, nil
}

// absoluteCleanPath 在配置边界把相对路径固定为绝对路径，避免数据库位置随后续 CWD 改变。
func absoluteCleanPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("resolve absolute SQLite path %s: %w", redactPath(clean), err)
	}
	return filepath.Clean(abs), nil
}

// validateSQLiteParent 检查 SQLite 父目录的类型、权限和可写性。
// 父目录尚不存在时允许后续数据库打开流程创建，但已存在目录必须满足 owner-only 约束。
func validateSQLiteParent(dbPath, parent string) error {
	if parent == "." || strings.TrimSpace(parent) == "" {
		return fmt.Errorf("SQLite database path must include a parent directory: %s", redactPath(dbPath))
	}
	info, err := os.Stat(parent)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite database parent %s: %s", redactPath(parent), securefs.SafeErrorForPath(err, parent))
	}
	if !info.IsDir() {
		return fmt.Errorf("SQLite database parent is not a directory: %s", redactPath(parent))
	}
	if err := securefs.CheckExistingOwnerOnly(parent, info); err != nil {
		return fmt.Errorf("SQLite database parent permissions are not owner-only: %s", redactPath(parent))
	}
	if err := securefs.ProbeWritableDir(parent); err != nil {
		return fmt.Errorf("SQLite database parent is not writable: %s", redactPath(parent))
	}
	return nil
}

// resolvePackagedSQLiteHome 根据平台解析打包应用的 SQLite 数据目录。
// 必要的用户目录环境缺失时直接返回错误，避免把数据库落到不可预期的位置。
func resolvePackagedSQLiteHome(goos string, getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	switch goos {
	case "windows":
		if dir := strings.TrimSpace(getenv("APPDATA")); dir != "" {
			return filepath.Join(dir, "Super Dolphin"), nil
		}
		return "", fmt.Errorf("packaged SQLite path requires APPDATA on Windows")
	case "darwin":
		home, err := userHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", fmt.Errorf("packaged SQLite path requires user home on macOS")
		}
		return filepath.Join(home, "Library", "Application Support", "Super Dolphin"), nil
	default:
		if dir := strings.TrimSpace(getenv("XDG_DATA_HOME")); dir != "" {
			return filepath.Join(dir, "Super Dolphin"), nil
		}
		home, err := userHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", fmt.Errorf("packaged SQLite path requires XDG_DATA_HOME or user home on Linux")
		}
		return filepath.Join(home, ".local", "share", "Super Dolphin"), nil
	}
}

func redactPath(path string) string {
	return securefs.RedactPath(path)
}

// SharedFileRoot 返回 shared file 存储根目录，并确保目录在首次使用前存在。
// 配置为空时回落到 SQLite 数据库旁的 shared-files 目录，保持本地数据同根。
func SharedFileRoot(cfg *Config) (string, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "packaged") {
		root := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
		if root == "" {
			return "", fmt.Errorf("packaged sharedfile root requires SUPER_DOLPHIN_HOME")
		}
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("packaged sharedfile root must be absolute: %s", root)
		}
		return filepath.Clean(root), nil
	}
	if cfg == nil {
		return "", nil
	}
	return strings.TrimSpace(cfg.ProjectRoot), nil
}

func resolvePackagedProjectRoot(executablePath string) string {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return ""
	}
	exeDir := filepath.Dir(executablePath)
	if filepath.Base(exeDir) != "MacOS" {
		return ""
	}
	contentsDir := filepath.Dir(exeDir)
	if filepath.Base(contentsDir) != "Contents" {
		return ""
	}
	return filepath.Join(contentsDir, "Resources")
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envOrCompat(canonical, legacy, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(canonical)); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv(legacy)); value != "" {
		pkglogger.Get().Warn("config env deprecated", "legacy", legacy, "canonical", canonical)
		return value
	}
	return fallback
}

// envBoolOr 区分未配置和非法配置；只有缺失或空白时才使用默认值。
// 已显式设置的坏值会阻断启动，避免生产配置悄悄退回默认行为。
func envBoolOr(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	value := strings.TrimSpace(raw)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

// envPositiveIntOr 解析正整数环境变量，非法或非正显式值必须 fail-fast。
func envPositiveIntOr(key string, fallback int) (int, error) {
	raw, ok := os.LookupEnv(key)
	value := strings.TrimSpace(raw)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer: got %d", key, parsed)
	}
	return parsed, nil
}
