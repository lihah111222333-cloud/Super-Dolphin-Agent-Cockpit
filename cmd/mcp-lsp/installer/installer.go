package installer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type InstallerConfig struct {
	BinaryName  string
	InstallCmd  string
	InstallArgs []string
	Language    string
}

type Provider struct {
	mu      sync.Mutex
	configs map[string]InstallerConfig
	logger  *slog.Logger
}

func NewProvider() *Provider {
	log := pkglogger.Get()
	return &Provider{
		configs: make(map[string]InstallerConfig),
		logger:  log,
	}
}

// Register registers an installer config for a specific language
func (p *Provider) Register(lang string, cfg InstallerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[lang] = cfg
}

// EnsureInstalled checks if the binary exists, if not it attempts to auto-install it.
// Returns the binary name/path.
func (p *Provider) EnsureInstalled(ctx context.Context, lang string) (string, error) {
	p.mu.Lock()
	cfg, ok := p.configs[lang]
	p.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("no installer config found for language: %s", lang)
	}

	// 1. Check if binary is in PATH
	if path, err := exec.LookPath(cfg.BinaryName); err == nil {
		return path, nil
	}

	p.logger.Info("LSP binary not found, attempting auto-install...",
		slog.String("lang", lang),
		slog.String("binary", cfg.BinaryName),
		slog.String("cmd", cfg.InstallCmd),
	)

	// 2. Perform installation
	cmd := exec.CommandContext(ctx, cfg.InstallCmd, cfg.InstallArgs...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to auto-install %s (%s %v): %w\nOutput: %s",
			cfg.BinaryName, cfg.InstallCmd, cfg.InstallArgs, err, string(out))
	}

	p.logger.Info("LSP auto-install successful",
		slog.String("lang", lang),
		slog.String("duration", time.Since(start).String()),
	)

	// 3. Verify it is now in PATH
	if path, err := exec.LookPath(cfg.BinaryName); err == nil {
		return path, nil
	}
	if path, ok := postInstallBinaryPath(ctx, cfg); ok {
		return path, nil
	}

	return "", fmt.Errorf("auto-install succeeded but binary %s is still not found in PATH", cfg.BinaryName)
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
	out, err := exec.CommandContext(ctx, goCmd, "env", "GOBIN", "GOPATH").Output()
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
