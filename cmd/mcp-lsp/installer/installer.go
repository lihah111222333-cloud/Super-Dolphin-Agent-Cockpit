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

// InstallerConfig describes how to locate and, when needed, install one LSP
// server binary for a language.
type InstallerConfig struct {
	BinaryName       string
	InstallCmd       string
	InstallArgs      []string
	Language         string
	RequiredBinaries []RequiredBinary
}

// RequiredBinary describes a companion executable that must be available before
// the primary LSP binary is considered usable.
type RequiredBinary struct {
	Name      string
	CheckArgs []string
}

// InstallStatus records how an LSP binary path was resolved.
type InstallStatus string

const (
	// InstallStatusPathFound means the binary was already available on PATH.
	InstallStatusPathFound InstallStatus = "path_found"

	// InstallStatusInstalledPath means auto-install succeeded and PATH lookup
	// resolved the binary afterwards.
	InstallStatusInstalledPath InstallStatus = "installed_path"

	// InstallStatusInstalledFallback means auto-install succeeded and the
	// provider found the binary in a language-specific install directory.
	InstallStatusInstalledFallback InstallStatus = "installed_fallback"
)

// InstallResult is the observable outcome of resolving an LSP binary.
type InstallResult struct {
	Path   string
	Status InstallStatus
	Lang   string
	Binary string
}

// Provider stores language-specific installer configuration and emits boundary
// logs for binary resolution and auto-install attempts.
type Provider struct {
	mu      sync.Mutex
	configs map[string]InstallerConfig
	logger  *slog.Logger
}

// NewProvider 创建provider。
func NewProvider() *Provider {
	log := pkglogger.Get()
	return &Provider{
		configs: make(map[string]InstallerConfig),
		logger:  log,
	}
}

// Register registers an installer config for a specific language
// Register 注册LSP。
func (p *Provider) Register(lang string, cfg InstallerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[lang] = cfg
}

// EnsureInstalled checks if the binary exists, if not it attempts to auto-install it.
// Returns the resolved binary path.
// EnsureInstalled 确保安装状态。
func (p *Provider) EnsureInstalled(ctx context.Context, lang string) (string, error) {
	result, err := p.EnsureInstalledDetailed(ctx, lang)
	if err != nil {
		return "", err
	}
	return result.Path, nil
}

// EnsureInstalledDetailed 确保安装状态详情。
func (p *Provider) EnsureInstalledDetailed(ctx context.Context, lang string) (InstallResult, error) {
	p.mu.Lock()
	cfg, ok := p.configs[lang]
	p.mu.Unlock()

	if !ok {
		return InstallResult{}, fmt.Errorf("no installer config found for language: %s", lang)
	}

	result := InstallResult{Lang: lang, Binary: cfg.BinaryName}

	// 1. Check if binary is in PATH and its companion tools are usable.
	if path, err := exec.LookPath(cfg.BinaryName); err == nil {
		if err := validateRequiredBinaries(ctx, cfg); err == nil {
			result.Path = path
			result.Status = InstallStatusPathFound
			p.logResolvedBinary(result)
			return result, nil
		}
	}

	p.logger.Info("LSP binary or required companion not ready, attempting auto-install...",
		slog.String("component", "mcp-lsp.installer"),
		slog.String("operation", "ensure_lsp_binary"),
		slog.String("lang", lang),
		slog.String("binary", cfg.BinaryName),
		slog.String("cmd", cfg.InstallCmd),
	)

	// 2. Perform installation
	cmd := hiddenexec.CommandContext(ctx, cfg.InstallCmd, cfg.InstallArgs...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return InstallResult{}, fmt.Errorf("failed to auto-install %s (%s %v): %w\nOutput: %s",
			cfg.BinaryName, cfg.InstallCmd, cfg.InstallArgs, err, string(out))
	}

	p.logger.Info("LSP auto-install successful",
		slog.String("component", "mcp-lsp.installer"),
		slog.String("operation", "auto_install_lsp_binary"),
		slog.String("lang", lang),
		slog.String("duration", time.Since(start).String()),
	)

	// 3. Verify it is now in PATH and companion tools are usable.
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

// validateRequiredBinaries 校验必需二进制。
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
		slog.String("component", "mcp-lsp.installer"),
		slog.String("operation", "ensure_lsp_binary"),
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

// executableInDir 在目录处理可执行文件。
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
