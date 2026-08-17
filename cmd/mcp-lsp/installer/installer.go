package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// InstallerConfig 描述单个语言服务的二进制、安装命令和伴随工具校验配置。
type InstallerConfig struct {
	// BinaryName 是历史 PATH 探测或安装结果中的语言服务二进制名；Windows 显式
	// resolver 仍必须返回 cache 内的绝对路径，不得由该名字触发 PATH 回退。
	BinaryName string
	// BinaryCheckArgs 是安装后对主二进制执行的健康检查参数；为空表示只做文件
	// 存在性校验，非空且失败时直接阻断生命周期。
	BinaryCheckArgs []string
	// InstallCmd 是 InstallAction 为 nil 时保留的历史安装命令；仅在能力已授予、
	// 非 check-only 且互斥 resolver 为空时执行。
	InstallCmd string
	// InstallAction 是一次安装生命周期钩子；它在 Provider 已取得安装锁、能力
	// 标记允许且未启用 check-only 后，以 InstallTimeout 派生的 context 调用。
	// 钩子负责安装器特有的联网与 cache 写盘，并必须返回安装后的绝对二进制
	// 路径；返回错误会原样阻断本次安装。非 nil 时不得再配置历史 command
	// 字段，nil 则逐字保留 InstallCmd/InstallArgs 的历史执行语义。
	InstallAction InstallAction
	// InstallCommandResolver 在真正安装时解析显式安装器路径；Windows 锁定 runtime
	// 必须从 cache 返回绝对路径，不能通过 PATH 查找或静默回退。
	InstallCommandResolver func(context.Context) (string, error)
	// InstalledBinaryPathResolver 在安装前后解析显式二进制路径；返回错误或非绝对
	// 路径会立即阻断，避免把包管理器输出或 PATH shim 当作锁定 runtime。
	InstalledBinaryPathResolver func(context.Context) (string, error)
	// InstallArgs 是 InstallAction 为 nil 时传给 InstallCmd 的原样参数；不得在
	// InstallAction 非 nil 时同时配置。
	InstallArgs []string
	// InstallArgsResolver 根据即将物化的 runtime prefix 构造参数；nil 时保留静态
	// InstallArgs 的历史 command 语义，解析失败必须直接返回。
	InstallArgsResolver func(context.Context) ([]string, error)
	// InstalledReadinessValidator 在主二进制和伴随工具检查后执行只读校验；Windows
	// cohort 用它核对精确包元数据，失败时阻断而不改写 cache。
	InstalledReadinessValidator func(context.Context) error
	// InstallTimeout 同时限制历史 command 和 InstallAction 的单次安装 context；零值
	// 使用默认超时，负值 fail-fast。
	InstallTimeout time.Duration
	// Language 是该配置绑定的语言 ID；Register 为空时以注册键填充。
	Language string
	// RequiredBinaries 是安装后必须存在并可选执行健康检查的伴随二进制集合；每个
	// 显式 PathResolver 都必须返回绝对路径。
	RequiredBinaries []RequiredBinary
	// InstallLockKey 串行化会修改同一 prefix 的安装动作；为空时保留按语言加锁的历史
	// 生命周期，非空值必须由共享 cohort 的注册方明确提供。
	InstallLockKey string
	// UnsupportedPlatform 在任何 PATH 或 asset 探测前阻断安装，并把 typed unsupported
	// 或 evidence-gap 错误原样交给调用方。
	UnsupportedPlatform error
	// OptionalUnsupportedPlatform 记录不阻断主安装的能力缺口（例如 Windows ARM64/x86
	// 上 bash-language-server 旁的 shellcheck），供能力层审计而不作静默回退。
	OptionalUnsupportedPlatform error
	// AllowInstallCommand 声明该配置可在调用方明确授予能力且非 check-only 时安装。
	AllowInstallCommand bool
	ManagedInstall      ManagedInstallFunc
	ManagedBinaryPath   string
	// ManagedOnly 禁止探测 PATH，仅允许使用 ManagedBinaryPath；用于不依赖系统 runtime 的服务。
	ManagedOnly bool
}

// ManagedInstallFunc 在受控安装根内安装语言服务器并返回绝对 launcher 路径。
// 它与 InstallCmd 互斥，避免同一语言同时存在全局命令和 managed artifact 两条真值路径。
type ManagedInstallFunc func(context.Context) (string, error)

// RequiredBinary 描述安装后必须存在并可选执行健康检查的伴随命令。
type RequiredBinary struct {
	// Name 是伴随二进制的历史 PATH 名称或显式 resolver 的审计名称。
	Name string
	// CheckArgs 是伴随二进制健康检查参数；为空表示仅校验文件。
	CheckArgs []string
	// PathResolver 返回 bundled runtime 的显式伴随路径；nil 时保留历史 PATH 查找，
	// 显式 resolver 的空值、相对路径或失败都会阻断安装生命周期。
	PathResolver func(context.Context) (string, error)
}

// InstallAction 是通用安装动作签名；Provider 只负责生命周期、锁、超时、能力
// 和 check-only 门禁，动作本身才允许联网或写入可变 cache，失败必须直接返回。
type InstallAction func(context.Context) (InstallResult, error)

// UnsupportedPlatformError 表示 runtime feature 在检测到的原生 Windows 宿主上没有
// 可用 asset；调用方可与缺失二进制区分，禁止通过 PATH、仿真或跨架构资产回退。
type UnsupportedPlatformError struct {
	// Feature 是被拒绝的语言服务或 runtime 产品名。
	Feature string
	// OS 是原生宿主系统标识，Windows bridge 期望为 windows。
	OS string
	// NativeArch 是由宿主 API 确认且用于精确选资产的 arm64、x64 或 x86。
	NativeArch string
}

// Error 返回包含 feature、宿主系统和原生架构的稳定失败信息；nil receiver 也直接
// 返回通用 unsupported 文本，不触发任何探测或回退。
func (e *UnsupportedPlatformError) Error() string {
	if e == nil {
		return "unsupported host platform"
	}
	return fmt.Sprintf("%s is unsupported on native host platform %s/%s", e.Feature, e.OS, e.NativeArch)
}

type installCommandCapabilityContextKey struct{}
type installCheckOnlyContextKey struct{}

// MissingBinaryError 表示语义工具调用需要的 LSP 二进制不可用。
type MissingBinaryError struct {
	// LanguageID 是触发安装请求的语言标识。
	LanguageID string
	// BinaryName 是配置声明的主二进制名；显式 Windows 路径失败时仍保留该审计名。
	BinaryName string
	// Reason 是 PATH、显式 cache resolver 或 readiness 校验的底层失败原因。
	Reason error
}

// Error 返回缺失 LSP 二进制的可读错误信息。
func (e *MissingBinaryError) Error() string {
	if e == nil {
		return "missing LSP binary"
	}
	message := fmt.Sprintf("missing LSP binary %s for language %s", e.BinaryName, e.LanguageID)
	if e.Reason != nil {
		message += ": " + e.Reason.Error()
	}
	return message
}

// Unwrap 返回底层探测失败原因，供 errors.Is 和 errors.As 使用。
func (e *MissingBinaryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}

// MissingLSPBinary 返回缺失的语言 ID 和二进制名，供工具层做 typed error 断言。
func (e *MissingBinaryError) MissingLSPBinary() (languageID string, binaryName string) {
	if e == nil {
		return "", ""
	}
	return e.LanguageID, e.BinaryName
}

// WithInstallCommandCapability 标记调用方明确允许执行安装命令。
func WithInstallCommandCapability(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, installCommandCapabilityContextKey{}, true)
}

// WithToolCallInstallCheckOnly 标记语义工具调用只能检查二进制，不能执行安装命令。
func WithToolCallInstallCheckOnly(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, installCheckOnlyContextKey{}, true)
}

// InstallStatus 标记语言服务二进制路径的来源。
type InstallStatus string

const (
	// InstallStatusPathFound 表示从现有显式路径或历史 PATH 找到并通过校验。
	InstallStatusPathFound InstallStatus = "path_found"
	// InstallStatusInstalledPath 表示本次安装后解析到明确的二进制路径。
	InstallStatusInstalledPath InstallStatus = "installed_path"
	// InstallStatusInstalledFallback 表示历史 command 成功后使用兼容性安装目录回退。
	InstallStatusInstalledFallback InstallStatus = "installed_fallback"
	defaultInstallTimeout                        = 10 * time.Minute
	commandShimProbeLimit                        = 64 << 10
)

const commandShimTargetMarker = "# cmd-shim-target="

// InstallResult 返回语言服务二进制解析后的路径、语言和来源状态。
type InstallResult struct {
	// Path 是最终可直接启动的绝对二进制路径；InstallAction 返回相对路径会被拒绝。
	Path string
	// Status 是路径来源及生命周期阶段。
	Status InstallStatus
	// Lang 是结果语言 ID；InstallAction 非空返回值为空时由 Provider 填充，否则必须
	// 与配置绑定语言一致。
	Lang string
	// Binary 是结果主二进制名；InstallAction 非空返回值为空时由 Provider 填充，否则
	// 必须与配置声明一致。
	Binary string
}

// Provider 管理语言服务安装配置，并按需执行自动安装和复验。
type Provider struct {
	mu           sync.Mutex
	configs      map[string]InstallerConfig
	installLocks map[string]*sync.Mutex
	logger       *slog.Logger
}

// NewProvider 初始化语言服务安装器注册表。
// Provider 内部用锁保护配置表，允许工具初始化阶段并发注册或查询语言配置。
func NewProvider() *Provider {
	log := pkglogger.Get()
	return &Provider{
		configs:      make(map[string]InstallerConfig),
		installLocks: make(map[string]*sync.Mutex),
		logger:       log,
	}
}

// ConfigForLanguage 返回指定语言的安装配置副本。
// 返回值中的切片均为深拷贝，调用方不能通过快照修改 Provider 内部配置。
func (p *Provider) ConfigForLanguage(lang string) (InstallerConfig, bool) {
	if p == nil {
		return InstallerConfig{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg, ok := p.configs[lang]
	if !ok {
		return InstallerConfig{}, false
	}
	return cloneInstallerConfig(cfg), true
}

func cloneInstallerConfig(cfg InstallerConfig) InstallerConfig {
	cfg.BinaryCheckArgs = slices.Clone(cfg.BinaryCheckArgs)
	cfg.InstallArgs = slices.Clone(cfg.InstallArgs)
	cfg.RequiredBinaries = slices.Clone(cfg.RequiredBinaries)
	for index := range cfg.RequiredBinaries {
		cfg.RequiredBinaries[index].CheckArgs = slices.Clone(cfg.RequiredBinaries[index].CheckArgs)
	}
	return cfg
}

// Register 为语言登记安装命令和伴随二进制检查项。
// 后续 EnsureInstalled 会按语言读取该配置，未登记时直接返回错误。
func (p *Provider) Register(lang string, cfg InstallerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = lang
	}
	p.configs[lang] = cfg
}

// EnsureInstalled 返回可执行语言服务二进制路径，缺失时按配置尝试安装。
// 安装或伴随工具校验失败会直接返回错误，避免静默降级到不可用 LSP。
func (p *Provider) EnsureInstalled(ctx context.Context, lang string) (string, error) {
	result, err := p.EnsureInstalledDetailed(ctx, lang)
	if err != nil {
		return "", err
	}
	return result.Path, nil
}

// EnsureInstalledDetailed 解析语言服务安装路径和来源状态。
// 它先校验 PATH，再执行自动安装并复验伴随工具，任何一步失败都会带原因返回。
func (p *Provider) EnsureInstalledDetailed(ctx context.Context, lang string) (InstallResult, error) {
	p.mu.Lock()
	cfg, ok := p.configs[lang]
	p.mu.Unlock()

	if !ok {
		return InstallResult{}, fmt.Errorf("no installer config found for language: %s", lang)
	}
	if err := validateInstallerConfig(cfg); err != nil {
		return InstallResult{}, fmt.Errorf("invalid installer config for language %s: %w", lang, err)
	}
	p.logOptionalPlatformGap(lang, cfg)
	if cfg.UnsupportedPlatform != nil {
		return InstallResult{}, cfg.UnsupportedPlatform
	}
	lockKey := strings.TrimSpace(cfg.InstallLockKey)
	if lockKey == "" {
		lockKey = lang
	}
	installLock := p.installLock(lockKey)
	installLock.Lock()
	defer installLock.Unlock()

	result := InstallResult{Lang: lang, Binary: cfg.BinaryName}

	// 先解析已安装候选；pnpm shell shim 前优先检查 npm 的规范全局安装目录。
	candidates, pathErr := installedBinaryCandidates(ctx, cfg)
	var readinessErr error
	for _, candidate := range candidates {
		if err := validateBinaryReadinessWithExplicitPath(ctx, candidate.path, cfg, candidate.explicit); err == nil {
			result.Path = candidate.path
			result.Status = InstallStatusPathFound
			return result, nil
		} else {
			readinessErr = errors.Join(readinessErr, err)
		}
	}

	if !canRunInstallCommand(ctx, cfg) {
		return InstallResult{}, missingBinaryError(lang, cfg, firstNonNilError(readinessErr, pathErr))
	}

	p.logger.Info("LSP binary or required companion not ready, attempting auto-install...",
		slog.String("lang", lang),
		slog.String("binary", cfg.BinaryName),
		slog.String("cmd", installSourceName(cfg)),
	)

	if cfg.InstallAction != nil {
		return p.runInstallAction(ctx, lang, cfg, result)
	}

	installCtx, cancel, managedPath, err := p.runInstallCommand(ctx, lang, cfg)
	if err != nil {
		return InstallResult{}, err
	}
	defer cancel()

	return p.resolveInstalledBinary(ctx, installCtx, cfg, result, managedPath)
}

// installLock 返回单个语言的首次安装互斥锁，不阻塞其他语言并行安装。
func (p *Provider) installLock(lang string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	lock, ok := p.installLocks[lang]
	if !ok {
		lock = &sync.Mutex{}
		p.installLocks[lang] = lock
	}
	return lock
}

func missingBinaryError(lang string, cfg InstallerConfig, reason error) *MissingBinaryError {
	languageID := strings.TrimSpace(cfg.Language)
	if languageID == "" {
		languageID = lang
	}
	return &MissingBinaryError{
		LanguageID: languageID,
		BinaryName: cfg.BinaryName,
		Reason:     reason,
	}
}

func canRunInstallCommand(ctx context.Context, cfg InstallerConfig) bool {
	if !cfg.AllowInstallCommand || !installCommandCapabilityFromContext(ctx) || installCheckOnlyFromContext(ctx) {
		return false
	}
	return cfg.InstallAction != nil ||
		cfg.ManagedInstall != nil ||
		strings.TrimSpace(cfg.InstallCmd) != "" ||
		cfg.InstallCommandResolver != nil
}

// validateInstallerConfig enforces one installation implementation and rejects
// combinations that could bypass the explicit runtime or capability gates.
func validateInstallerConfig(cfg InstallerConfig) error {
	commandConfigured := strings.TrimSpace(cfg.InstallCmd) != "" || cfg.InstallCommandResolver != nil
	actionConfigured := cfg.InstallAction != nil
	managedConfigured := cfg.ManagedInstall != nil
	managedPath := strings.TrimSpace(cfg.ManagedBinaryPath)
	if actionConfigured && (strings.TrimSpace(cfg.InstallCmd) != "" ||
		cfg.InstallCommandResolver != nil ||
		len(cfg.InstallArgs) > 0 ||
		cfg.InstallArgsResolver != nil) {
		return fmt.Errorf("installer config for %s cannot combine InstallAction with command-based install fields", cfg.BinaryName)
	}
	if actionConfigured && (managedConfigured || managedPath != "") {
		return fmt.Errorf("installer config for %s cannot combine InstallAction with managed install fields", cfg.BinaryName)
	}
	if managedConfigured && (len(cfg.InstallArgs) > 0 || cfg.InstallArgsResolver != nil) {
		return errors.New("ManagedInstall cannot be combined with command argument fields")
	}
	if err := validateManagedInstallerConfig(commandConfigured, managedConfigured, managedPath); err != nil {
		return err
	}
	if cfg.ManagedOnly && managedPath == "" {
		return errors.New("ManagedOnly requires an absolute ManagedBinaryPath")
	}
	implementationConfigured := actionConfigured || commandConfigured || managedConfigured
	if cfg.AllowInstallCommand && !implementationConfigured {
		return errors.New("install capability is enabled without an install implementation")
	}
	if !cfg.AllowInstallCommand && implementationConfigured {
		return errors.New("install implementation is configured without AllowInstallCommand")
	}
	return nil
}

// validateManagedInstallerConfig validates managed artifact ownership and path
// scope before any filesystem or process probing takes place.
func validateManagedInstallerConfig(commandConfigured, managedConfigured bool, managedPath string) error {
	if commandConfigured && managedConfigured {
		return errors.New("InstallCmd and ManagedInstall are mutually exclusive")
	}
	if managedConfigured && (managedPath == "" || !filepath.IsAbs(managedPath)) {
		return errors.New("ManagedInstall requires an absolute ManagedBinaryPath")
	}
	if !managedConfigured && managedPath != "" {
		return errors.New("ManagedBinaryPath is configured without ManagedInstall")
	}
	return nil
}

func installSourceName(cfg InstallerConfig) string {
	if cfg.ManagedInstall != nil {
		return "managed_artifact"
	}
	if cfg.InstallAction != nil {
		return "install_action"
	}
	return cfg.InstallCmd
}

// runInstallAction invokes the provider-specific installer only after the
// capability, check-only, lock, and timeout gates have all succeeded.
func (p *Provider) runInstallAction(ctx context.Context, lang string, cfg InstallerConfig, result InstallResult) (InstallResult, error) {
	installCtx, cancel, err := installCommandContext(ctx, cfg)
	if err != nil {
		return InstallResult{}, err
	}
	defer cancel()

	installed, err := cfg.InstallAction(installCtx)
	if err != nil {
		if errors.Is(installCtx.Err(), context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
			return InstallResult{}, fmt.Errorf("auto-install action %s exceeded timeout %s: %w", cfg.BinaryName, installTimeout(cfg), installCtx.Err())
		}
		return InstallResult{}, err
	}
	installed.Path = strings.TrimSpace(installed.Path)
	if installed.Path == "" {
		return InstallResult{}, fmt.Errorf("install action for %s returned an empty binary path", cfg.BinaryName)
	}
	if !filepath.IsAbs(installed.Path) {
		return InstallResult{}, fmt.Errorf("install action for %s returned a non-absolute binary path %q", cfg.BinaryName, installed.Path)
	}
	if installed.Lang != "" && installed.Lang != result.Lang {
		return InstallResult{}, fmt.Errorf("install action for %s returned language %q, expected %q", cfg.BinaryName, installed.Lang, result.Lang)
	}
	if installed.Binary != "" && installed.Binary != result.Binary {
		return InstallResult{}, fmt.Errorf("install action for %s returned binary %q, expected %q", cfg.BinaryName, installed.Binary, result.Binary)
	}
	if err := validateBinaryReadinessWithExplicitPath(installCtx, installed.Path, cfg, true); err != nil {
		return InstallResult{}, fmt.Errorf("install action returned an unusable binary %s: %w", cfg.BinaryName, err)
	}
	if installed.Lang == "" {
		installed.Lang = result.Lang
	}
	if installed.Binary == "" {
		installed.Binary = result.Binary
	}
	if installed.Status == "" {
		installed.Status = InstallStatusInstalledPath
	}
	p.logResolvedBinary(installed)
	return installed, nil
}

func installCommandCapabilityFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(installCommandCapabilityContextKey{}).(bool)
	return value
}

func installCheckOnlyFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(installCheckOnlyContextKey{}).(bool)
	return value
}

func firstNonNilError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// runInstallCommand 执行声明式安装命令，并为这一层强制设置 deadline。
// 成功时返回仍可用于安装后探测的 installCtx，调用方负责 cancel。
func (p *Provider) runInstallCommand(ctx context.Context, lang string, cfg InstallerConfig) (context.Context, context.CancelFunc, string, error) {
	installCtx, cancel, err := installCommandContext(ctx, cfg)
	if err != nil {
		return nil, nil, "", err
	}
	if cfg.ManagedInstall != nil {
		start := time.Now()
		managedPath, installErr := cfg.ManagedInstall(installCtx)
		if installErr != nil {
			ctxErr := installCtx.Err()
			cancel()
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return nil, nil, "", fmt.Errorf("managed auto-install %s exceeded timeout %s: %w", cfg.BinaryName, installTimeout(cfg), ctxErr)
			}
			return nil, nil, "", fmt.Errorf("managed auto-install %s failed: %w", cfg.BinaryName, installErr)
		}
		managedPath = strings.TrimSpace(managedPath)
		if managedPath == "" || !filepath.IsAbs(managedPath) {
			cancel()
			return nil, nil, "", fmt.Errorf("managed auto-install %s returned non-absolute launcher path %q", cfg.BinaryName, managedPath)
		}
		if filepath.Clean(managedPath) != filepath.Clean(cfg.ManagedBinaryPath) {
			cancel()
			return nil, nil, "", fmt.Errorf("managed auto-install %s returned launcher %q, want declared path %q", cfg.BinaryName, managedPath, cfg.ManagedBinaryPath)
		}
		p.logger.Info("LSP managed auto-install successful", slog.String("lang", lang), slog.String("duration", time.Since(start).String()))
		return installCtx, cancel, managedPath, nil
	}
	installCmd, err := resolveInstallCommand(installCtx, cfg)
	if err != nil {
		cancel()
		return nil, nil, "", err
	}
	installArgs, err := resolveInstallArgs(installCtx, cfg)
	if err != nil {
		cancel()
		return nil, nil, "", err
	}
	cmd := hiddenexec.CommandContext(installCtx, installCmd, installArgs...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		ctxErr := installCtx.Err()
		cancel()
		return nil, nil, "", newProcessFailureError(
			"auto-install",
			cfg.BinaryName,
			joinProcessFailureCause(ctxErr, err),
			out,
			len(installArgs),
			0,
		)
	}

	p.logger.Info("LSP auto-install successful",
		slog.String("lang", lang),
		slog.String("duration", time.Since(start).String()),
	)
	return installCtx, cancel, "", nil
}

// resolveInstallArgs resolves the complete command argument vector before
// spawning the package manager, preserving exact cohort prefixes.
func resolveInstallArgs(ctx context.Context, cfg InstallerConfig) ([]string, error) {
	if cfg.InstallArgsResolver != nil {
		args, err := cfg.InstallArgsResolver(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve installer arguments for %s: %w", cfg.BinaryName, err)
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("installer arguments for %s are empty", cfg.BinaryName)
		}
		return append([]string(nil), args...), nil
	}
	if len(cfg.InstallArgs) == 0 {
		return nil, fmt.Errorf("installer arguments for %s are empty", cfg.BinaryName)
	}
	return append([]string(nil), cfg.InstallArgs...), nil
}

// resolveInstallCommand resolves the executable only from the installer
// configuration. A resolver is mandatory for locked runtimes so a missing
// Node asset cannot silently fall back to a PATH npm shim.
func resolveInstallCommand(ctx context.Context, cfg InstallerConfig) (string, error) {
	if cfg.InstallCommandResolver != nil {
		command, err := cfg.InstallCommandResolver(ctx)
		if err != nil {
			return "", fmt.Errorf("resolve explicit installer command for %s: %w", cfg.BinaryName, err)
		}
		command = strings.TrimSpace(command)
		if command == "" {
			return "", fmt.Errorf("explicit installer command for %s is empty", cfg.BinaryName)
		}
		return command, nil
	}
	command := strings.TrimSpace(cfg.InstallCmd)
	if command == "" {
		return "", fmt.Errorf("installer command for %s is empty", cfg.BinaryName)
	}
	return command, nil
}

// resolveInstalledBinary 复验安装结果并返回最终二进制路径。
// 安装命令成功但 PATH 或 go install fallback 不可用时必须报错，不能伪装成功。
func (p *Provider) resolveInstalledBinary(ctx, installCtx context.Context, cfg InstallerConfig, result InstallResult, managedPath string) (InstallResult, error) {
	// 安装后重新解析所有候选并复验，避免 PATH 中的旧 pnpm shim 遮蔽刚安装的 npm 全局二进制。
	candidates, pathErr := installedBinaryCandidates(installCtx, cfg)
	if managedPath != "" {
		candidates = append([]installedBinaryCandidate{{path: managedPath, explicit: true}}, candidates...)
	}
	var readinessErr error
	for _, candidate := range candidates {
		if err := validateBinaryReadinessWithExplicitPath(ctx, candidate.path, cfg, candidate.explicit); err != nil {
			readinessErr = errors.Join(readinessErr, err)
			continue
		}
		result.Path = candidate.path
		result.Status = InstallStatusInstalledPath
		if candidate.fallback {
			result.Status = InstallStatusInstalledFallback
		}
		p.logResolvedBinary(result)
		return result, nil
	}
	if readinessErr != nil {
		return InstallResult{}, fmt.Errorf("auto-install succeeded but LSP binary %s is not usable: %w", cfg.BinaryName, readinessErr)
	}
	if pathErr != nil {
		return InstallResult{}, fmt.Errorf("auto-install succeeded but binary %s is still not found in PATH: %w", cfg.BinaryName, pathErr)
	}

	return InstallResult{}, fmt.Errorf("auto-install succeeded but binary %s is still not found in PATH", cfg.BinaryName)
}

type installedBinaryCandidate struct {
	path     string
	fallback bool
	explicit bool
}

// installedBinaryCandidates 解析 PATH 与安装器规范目录；pnpm shim 被 npm 全局路径优先遮蔽。
func installedBinaryCandidates(ctx context.Context, cfg InstallerConfig) ([]installedBinaryCandidate, error) {
	// A resolver denotes a fully explicit runtime (for example the locked
	// Windows Node cohort). Do not inspect PATH in this branch: an unrelated
	// shim must not short-circuit the exact cohort or become a fallback.
	if cfg.InstalledBinaryPathResolver != nil {
		explicitPath, err := cfg.InstalledBinaryPathResolver(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve explicit installed binary for %s: %w", cfg.BinaryName, err)
		}
		explicitPath = strings.TrimSpace(explicitPath)
		if explicitPath == "" {
			return nil, fmt.Errorf("explicit installed binary for %s is empty", cfg.BinaryName)
		}
		return []installedBinaryCandidate{{path: explicitPath, explicit: true}}, nil
	}
	candidates := make([]installedBinaryCandidate, 0, 3)
	appendCandidate := func(candidate installedBinaryCandidate) {
		if candidate.path == "" {
			return
		}
		for _, current := range candidates {
			if filepath.Clean(current.path) == filepath.Clean(candidate.path) {
				return
			}
		}
		candidates = append(candidates, candidate)
	}
	if managedPath := strings.TrimSpace(cfg.ManagedBinaryPath); managedPath != "" {
		appendCandidate(installedBinaryCandidate{path: managedPath, explicit: true})
	}
	if cfg.ManagedOnly {
		return candidates, fmt.Errorf("managed-only LSP binary %s is not available in PATH by contract", cfg.BinaryName)
	}

	path, pathErr := exec.LookPath(cfg.BinaryName)
	_, pathIsShim := CommandShimTarget(path)
	fallbackPath, fallbackOK := postInstallBinaryPath(ctx, cfg)
	if pathIsShim && fallbackOK {
		appendCandidate(installedBinaryCandidate{path: fallbackPath, fallback: true})
	}
	appendCandidate(installedBinaryCandidate{path: path})
	if fallbackOK {
		appendCandidate(installedBinaryCandidate{path: fallbackPath, fallback: true})
	}
	return candidates, pathErr
}

// installCommandContext 为单次自动安装套上本层 deadline。
// 调用方 ctx 没有超时时也必须有安装预算，避免外部包管理器卡住整条 LSP 请求链。
func installCommandContext(ctx context.Context, cfg InstallerConfig) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := installTimeout(cfg)
	if timeout < 0 {
		return nil, nil, fmt.Errorf("install timeout for %s cannot be negative", cfg.BinaryName)
	}
	installCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	return installCtx, cancel, nil
}

func installTimeout(cfg InstallerConfig) time.Duration {
	if cfg.InstallTimeout == 0 {
		return defaultInstallTimeout
	}
	return cfg.InstallTimeout
}

func validateBinaryReadiness(ctx context.Context, path string, cfg InstallerConfig) error {
	return validateBinaryReadinessWithExplicitPath(ctx, path, cfg, false)
}

func validateBinaryReadinessWithExplicitPath(ctx context.Context, path string, cfg InstallerConfig, explicit bool) error {
	if explicit {
		if err := validateExplicitBinaryPath(path, cfg.BinaryName); err != nil {
			return err
		}
	}
	if err := validatePrimaryBinary(ctx, path, cfg); err != nil {
		return err
	}
	if err := validateRequiredBinaries(ctx, cfg); err != nil {
		return err
	}
	if cfg.InstalledReadinessValidator != nil {
		if err := cfg.InstalledReadinessValidator(ctx); err != nil {
			return fmt.Errorf("installed readiness validation failed: %w", err)
		}
	}
	return nil
}

func validateExplicitBinaryPath(path, binaryName string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("explicit LSP binary %s path must be absolute: %q", safeProcessLabel(binaryName), path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("explicit LSP binary %s is not ready: %w", safeProcessLabel(binaryName), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("explicit LSP binary %s is not a regular non-empty file", safeProcessLabel(binaryName))
	}
	return nil
}

func validatePrimaryBinary(ctx context.Context, path string, cfg InstallerConfig) error {
	resolved := strings.TrimSpace(path)
	if !filepath.IsAbs(resolved) {
		var err error
		resolved, err = exec.LookPath(resolved)
		if err != nil {
			return fmt.Errorf("LSP binary %s is not executable at %s: %w", cfg.BinaryName, path, err)
		}
	}
	if len(cfg.BinaryCheckArgs) == 0 {
		return nil
	}
	cmd := hiddenexec.CommandContext(ctx, resolved, cfg.BinaryCheckArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return newProcessFailureError(
			"primary-health-check",
			cfg.BinaryName,
			joinProcessFailureCause(ctx.Err(), err),
			out,
			len(cfg.BinaryCheckArgs),
			0,
		)
	}
	return nil
}

// validateRequiredBinaries 确认语言服务依赖的伴随命令都可执行。
// 配置中出现空名称、PATH 缺失或健康检查失败都会阻断安装结果。
func validateRequiredBinaries(ctx context.Context, cfg InstallerConfig) error {
	for _, required := range cfg.RequiredBinaries {
		name := strings.TrimSpace(required.Name)
		if name == "" {
			return errors.New("required LSP companion binary name is empty")
		}
		var path string
		var err error
		if required.PathResolver != nil {
			path, err = required.PathResolver(ctx)
			if err == nil {
				path = strings.TrimSpace(path)
				if path == "" {
					err = fmt.Errorf("explicit required binary %s path is empty", name)
				} else {
					err = validateExplicitBinaryPath(path, name)
				}
			}
		} else {
			path, err = exec.LookPath(name)
		}
		if err != nil {
			if required.PathResolver != nil {
				return fmt.Errorf("required LSP companion binary %s is not ready at its explicit path: %w", name, err)
			}
			return fmt.Errorf("required LSP companion binary %s is not found in PATH", name)
		}
		if len(required.CheckArgs) == 0 {
			continue
		}
		cmd := hiddenexec.CommandContext(ctx, path, required.CheckArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return newProcessFailureError(
				"required-health-check",
				name,
				joinProcessFailureCause(ctx.Err(), err),
				out,
				len(required.CheckArgs),
				0,
			)
		}
	}
	return nil
}

func (p *Provider) logResolvedBinary(result InstallResult) {
	if p == nil || p.logger == nil || strings.TrimSpace(result.Path) == "" {
		return
	}
	p.logger.Info("LSP binary resolved",
		slog.String("lang", result.Lang),
		slog.String("binary", result.Binary),
		slog.String("path", result.Path),
		slog.String("status", string(result.Status)),
	)
}

// logOptionalPlatformGap records an intentionally non-blocking capability gap
// for auditability without selecting an alternate runtime or install path.
func (p *Provider) logOptionalPlatformGap(lang string, cfg InstallerConfig) {
	if p == nil || p.logger == nil || cfg.OptionalUnsupportedPlatform == nil {
		return
	}
	p.logger.Warn("optional LSP installer capability unavailable",
		slog.String("lang", lang),
		slog.String("binary", cfg.BinaryName),
		slog.String("error_type", fmt.Sprintf("%T", cfg.OptionalUnsupportedPlatform)),
	)
}

// postInstallBinaryPath 返回安装命令对应规范目录中的目标二进制路径。
func postInstallBinaryPath(ctx context.Context, cfg InstallerConfig) (string, bool) {
	switch filepath.Base(strings.TrimSpace(cfg.InstallCmd)) {
	case "npm", "npm.cmd":
		dir := npmInstallBinDir(ctx, cfg.InstallCmd)
		if dir == "" {
			return "", false
		}
		return executableInDir(dir, cfg.BinaryName)
	case "go", "go.exe":
		dir := goInstallBinDir(ctx, cfg.InstallCmd)
		if dir == "" {
			return "", false
		}
		return executableInDir(dir, cfg.BinaryName)
	case "cargo", "cargo.exe":
		dir := cargoInstallBinDir()
		if dir == "" {
			return "", false
		}
		return executableInDir(dir, cfg.BinaryName)
	case "dotnet", "dotnet.exe":
		dir := dotnetGlobalToolBinDir()
		if dir == "" {
			return "", false
		}
		return executableInDir(dir, cfg.BinaryName)
	default:
		return "", false
	}
}

// npmInstallBinDir 只负责读取 npm 的全局 prefix；prefix 如何映射到可执行目录由
// 带显式 build tag 的平台实现决定，公共安装流程不包含 runtime.GOOS 分支。
func npmInstallBinDir(ctx context.Context, npmCmd string) string {
	out, err := hiddenexec.CommandContext(ctx, npmCmd, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return ""
	}
	return npmInstallBinDirFromPrefix(prefix)
}

// CommandShimTarget 读取 pnpm 生成的 shell shim 目标，不执行 shim 或目标文件。
func CommandShimTarget(path string) (string, bool) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, commandShimProbeLimit))
	if err != nil {
		return "", false
	}
	index := strings.LastIndex(string(data), commandShimTargetMarker)
	if index < 0 {
		return "", false
	}
	target := string(data[index+len(commandShimTargetMarker):])
	if lineEnd := strings.IndexAny(target, "\r\n"); lineEnd >= 0 {
		target = target[:lineEnd]
	}
	target = strings.TrimSpace(target)
	return target, target != "" && filepath.IsAbs(target)
}

func cargoInstallBinDir() string {
	if cargoHome := strings.TrimSpace(os.Getenv("CARGO_HOME")); cargoHome != "" {
		return filepath.Join(cargoHome, "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".cargo", "bin")
}

// dotnetGlobalToolBinDir 返回 dotnet tool install --global 的规范 launcher 目录。
func dotnetGlobalToolBinDir() string {
	if cliHome := strings.TrimSpace(os.Getenv("DOTNET_CLI_HOME")); cliHome != "" {
		return filepath.Join(cliHome, ".dotnet", "tools")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".dotnet", "tools")
}

func goInstallBinDir(ctx context.Context, goCmd string) string {
	out, err := hiddenexec.CommandContext(ctx, goCmd, "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	if gobin := strings.TrimSpace(lines[0]); gobin != "" {
		return gobin
	}
	gopath := strings.TrimSpace(lines[1])
	if gopath == "" {
		return ""
	}
	return filepath.Join(gopath, "bin")
}
