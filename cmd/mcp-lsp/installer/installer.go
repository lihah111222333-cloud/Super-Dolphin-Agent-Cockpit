package installer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/internal/hiddenexec"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type InstallerConfig struct {
	BinaryName       string
	InstallCmd       string
	InstallArgs      []string
	Language         string
	RequiredBinaries []RequiredBinary
}

type RequiredBinary struct {
	Name      string
	CheckArgs []string
}

type InstallStatus string

const (
	InstallStatusPathFound         InstallStatus = "path_found"
	InstallStatusInstalledPath     InstallStatus = "installed_path"
	InstallStatusInstalledFallback InstallStatus = "installed_fallback"
)

type InstallResult struct {
	Path   string
	Status InstallStatus
	Lang   string
	Binary string
}

type Provider struct {
	mu      sync.Mutex
	configs map[string]InstallerConfig
	logger  *slog.Logger
}

// NewProvider 初始化语言服务安装器注册表。
// Provider 内部用锁保护配置表，允许工具初始化阶段并发注册或查询语言配置。
func NewProvider() *Provider {
	log := pkglogger.Get()
	return &Provider{
		configs: make(map[string]InstallerConfig),
		logger:  log,
	}
}

// Register 为语言登记安装命令和伴随二进制检查项。
// 后续 EnsureInstalled 会按语言读取该配置，未登记时直接返回错误。
func (p *Provider) Register(lang string, cfg InstallerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
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

	result := InstallResult{Lang: lang, Binary: cfg.BinaryName}

	// 先信任 PATH 中已有二进制，但必须同时验证伴随工具可用。
	if path, err := exec.LookPath(cfg.BinaryName); err == nil {
		if err := validateRequiredBinaries(ctx, cfg); err == nil {
			result.Path = path
			result.Status = InstallStatusPathFound
			return result, nil
		}
	}

	p.logger.Info("LSP binary or required companion not ready, attempting auto-install...",
		slog.String("lang", lang),
		slog.String("binary", cfg.BinaryName),
		slog.String("cmd", cfg.InstallCmd),
	)

	// PATH 不可用时执行声明式安装命令，输出会在失败时带回调用方。
	cmd := hiddenexec.CommandContext(ctx, cfg.InstallCmd, cfg.InstallArgs...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return InstallResult{}, fmt.Errorf("failed to auto-install %s (%s %v): %w\nOutput: %s",
			cfg.BinaryName, cfg.InstallCmd, cfg.InstallArgs, err, string(out))
	}

	p.logger.Info("LSP auto-install successful",
		slog.String("lang", lang),
		slog.String("duration", time.Since(start).String()),
	)

	// 安装后重新解析路径并复验伴随工具，避免报告“已安装”但运行时仍不可用。
	if path, err := exec.LookPath(cfg.BinaryName); err == nil {
		if err := validateRequiredBinaries(ctx, cfg); err != nil {
			return InstallResult{}, fmt.Errorf("auto-install succeeded but required LSP companion for %s is not usable: %w", cfg.BinaryName, err)
		}
		result.Path = path
		result.Status = InstallStatusInstalledPath
		p.logResolvedBinary(result)
		return result, nil
	}
	if path, ok := postInstallBinaryPath(ctx, cfg); ok {
		if err := validateRequiredBinaries(ctx, cfg); err != nil {
			return InstallResult{}, fmt.Errorf("auto-install succeeded but required LSP companion for %s is not usable: %w", cfg.BinaryName, err)
		}
		result.Path = path
		result.Status = InstallStatusInstalledFallback
		p.logResolvedBinary(result)
		return result, nil
	}

	return InstallResult{}, fmt.Errorf("auto-install succeeded but binary %s is still not found in PATH", cfg.BinaryName)
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

func postInstallBinaryPath(ctx context.Context, cfg InstallerConfig) (string, bool) {
	if filepath.Base(strings.TrimSpace(cfg.InstallCmd)) != "go" {
		return "", false
	}
	dir := goInstallBinDir(ctx, cfg.InstallCmd)
	if dir == "" {
		return "", false
	}
	return executableInDir(dir, cfg.BinaryName)
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
