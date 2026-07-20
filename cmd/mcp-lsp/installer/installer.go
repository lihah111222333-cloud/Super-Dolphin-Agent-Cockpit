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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// InstallerConfig 描述单个语言服务的二进制、安装命令和伴随工具校验配置。
type InstallerConfig struct {
	BinaryName          string
	BinaryCheckArgs     []string
	InstallCmd          string
	InstallArgs         []string
	InstallTimeout      time.Duration
	Language            string
	RequiredBinaries    []RequiredBinary
	AllowInstallCommand bool
}

// RequiredBinary 描述安装后必须存在并可选执行健康检查的伴随命令。
type RequiredBinary struct {
	Name      string
	CheckArgs []string
}

type installCommandCapabilityContextKey struct{}
type installCheckOnlyContextKey struct{}

// MissingBinaryError 表示语义工具调用需要的 LSP 二进制不可用。
type MissingBinaryError struct {
	LanguageID string
	BinaryName string
	Reason     error
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
	InstallStatusPathFound         InstallStatus = "path_found"
	InstallStatusInstalledPath     InstallStatus = "installed_path"
	InstallStatusInstalledFallback InstallStatus = "installed_fallback"
	defaultInstallTimeout                        = 10 * time.Minute
	commandShimProbeLimit                        = 64 << 10
)

const commandShimTargetMarker = "# cmd-shim-target="

// InstallResult 返回语言服务二进制解析后的路径、语言和来源状态。
type InstallResult struct {
	Path   string
	Status InstallStatus
	Lang   string
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
	installLock := p.installLock(lang)
	installLock.Lock()
	defer installLock.Unlock()

	p.mu.Lock()
	cfg, ok := p.configs[lang]
	p.mu.Unlock()

	if !ok {
		return InstallResult{}, fmt.Errorf("no installer config found for language: %s", lang)
	}

	result := InstallResult{Lang: lang, Binary: cfg.BinaryName}

	// 先解析已安装候选；pnpm shell shim 前优先检查 npm 的规范全局安装目录。
	candidates, pathErr := installedBinaryCandidates(ctx, cfg)
	var readinessErr error
	for _, candidate := range candidates {
		if err := validateBinaryReadiness(ctx, candidate.path, cfg); err == nil {
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
		slog.String("cmd", cfg.InstallCmd),
	)

	installCtx, cancel, err := p.runInstallCommand(ctx, lang, cfg)
	if err != nil {
		return InstallResult{}, err
	}
	defer cancel()

	return p.resolveInstalledBinary(ctx, installCtx, cfg, result)
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
	return cfg.AllowInstallCommand &&
		strings.TrimSpace(cfg.InstallCmd) != "" &&
		installCommandCapabilityFromContext(ctx) &&
		!installCheckOnlyFromContext(ctx)
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
func (p *Provider) runInstallCommand(ctx context.Context, lang string, cfg InstallerConfig) (context.Context, context.CancelFunc, error) {
	installCtx, cancel, err := installCommandContext(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cmd := hiddenexec.CommandContext(installCtx, cfg.InstallCmd, cfg.InstallArgs...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		ctxErr := installCtx.Err()
		cancel()
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, nil, fmt.Errorf("auto-install %s exceeded timeout %s: %w\nOutput: %s",
				cfg.BinaryName, installTimeout(cfg).String(), ctxErr, string(out))
		}
		return nil, nil, fmt.Errorf("failed to auto-install %s (%s %v): %w\nOutput: %s",
			cfg.BinaryName, cfg.InstallCmd, cfg.InstallArgs, err, string(out))
	}

	p.logger.Info("LSP auto-install successful",
		slog.String("lang", lang),
		slog.String("duration", time.Since(start).String()),
	)
	return installCtx, cancel, nil
}

// resolveInstalledBinary 复验安装结果并返回最终二进制路径。
// 安装命令成功但 PATH 或 go install fallback 不可用时必须报错，不能伪装成功。
func (p *Provider) resolveInstalledBinary(ctx, installCtx context.Context, cfg InstallerConfig, result InstallResult) (InstallResult, error) {
	// 安装后重新解析所有候选并复验，避免 PATH 中的旧 pnpm shim 遮蔽刚安装的 npm 全局二进制。
	candidates, pathErr := installedBinaryCandidates(installCtx, cfg)
	var readinessErr error
	for _, candidate := range candidates {
		if err := validateBinaryReadiness(ctx, candidate.path, cfg); err != nil {
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
}

// installedBinaryCandidates 解析 PATH 与安装器规范目录；pnpm shim 被 npm 全局路径优先遮蔽。
func installedBinaryCandidates(ctx context.Context, cfg InstallerConfig) ([]installedBinaryCandidate, error) {
	path, pathErr := exec.LookPath(cfg.BinaryName)
	fallbackPath, fallbackOK := postInstallBinaryPath(ctx, cfg)
	_, pathIsShim := commandShimTarget(path)
	candidates := make([]installedBinaryCandidate, 0, 2)
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
	if err := validatePrimaryBinary(ctx, path, cfg); err != nil {
		return err
	}
	return validateRequiredBinaries(ctx, cfg)
}

func validatePrimaryBinary(ctx context.Context, path string, cfg InstallerConfig) error {
	if len(cfg.BinaryCheckArgs) == 0 {
		return nil
	}
	cmd := hiddenexec.CommandContext(ctx, path, cfg.BinaryCheckArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("LSP binary %s failed health check (%s %v): %w\nOutput: %s",
			cfg.BinaryName, path, cfg.BinaryCheckArgs, err, string(out))
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
		path, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("required LSP companion binary %s is not found in PATH", name)
		}
		if len(required.CheckArgs) == 0 {
			continue
		}
		cmd := hiddenexec.CommandContext(ctx, path, required.CheckArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("required LSP companion binary %s failed health check (%s %v): %w\nOutput: %s",
				name, path, required.CheckArgs, err, string(out))
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
	default:
		return "", false
	}
}

func npmInstallBinDir(ctx context.Context, npmCmd string) string {
	out, err := hiddenexec.CommandContext(ctx, npmCmd, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// commandShimTarget 读取 pnpm 生成的 shell shim 目标，不执行 shim 或目标文件。
func commandShimTarget(path string) (string, bool) {
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

// executableInDir 在指定目录中查找可执行二进制。
// Windows 额外尝试 .exe 后缀；非 Windows 需要执行位，避免把普通文件当作语言服务。
func executableInDir(dir, binaryName string) (string, bool) {
	binaryName = strings.TrimSpace(binaryName)
	if strings.TrimSpace(dir) == "" || binaryName == "" {
		return "", false
	}
	candidates := []string{filepath.Join(dir, binaryName)}
	if runtime.GOOS == "windows" && filepath.Ext(binaryName) == "" {
		candidates = append(candidates, filepath.Join(dir, binaryName+".exe"))
	}
	for _, candidate := range candidates {
		st, err := os.Stat(candidate)
		if err != nil || st.IsDir() {
			continue
		}
		if runtime.GOOS == "windows" || st.Mode()&0111 != 0 {
			return candidate, true
		}
	}
	return "", false
}
