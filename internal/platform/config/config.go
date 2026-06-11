package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/embeddedpg"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Type aliases – canonical definitions live in contract.
type (
	Config       = contract.Config
	SkillConfig  = contract.SkillConfig
	AgentConfig  = contract.AgentConfig
	NotifyConfig = contract.NotifyConfig
	LSPConfig    = contract.LSPConfig
)

func New() (*Config, error) {
	projectRoot, err := PrimeProcessEnvironment()
	if err != nil {
		return nil, err
	}
	embeddedPostgres, databaseURL := embeddedpg.ResolveFromEnvironment(projectRoot)
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" && strings.TrimSpace(os.Getenv("POSTGRES_CONNECTION_STRING")) != "" {
		pkglogger.Get().Warn("config env POSTGRES_CONNECTION_STRING is deprecated; use DATABASE_URL instead")
	}

	cfg := &Config{
		DatabaseURL:      databaseURL,
		RPCAddr:          envOrCompat("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR", "127.0.0.1:8090"),
		LogLevel:         envOr("LOG_LEVEL", "info"),
		ProjectRoot:      projectRoot,
		EmbeddedPostgres: embeddedPostgres,
		Skill: SkillConfig{
			ProgressiveDisclosure: envBoolOr("SKILL_PROGRESSIVE_DISCLOSURE", false),
			TokenBudget:           envPositiveIntOr("SKILL_TOKEN_BUDGET", 3000),
		},
		Agent: AgentConfig{
			PersistentSubagentDefault: envBoolOr("PERSISTENT_SUBAGENT_DEFAULT", false),
		},
		Notify: NotifyConfig{
			ChannelsJSON:     os.Getenv("NOTIFY_CHANNELS_JSON"),
			AllowPrivateCIDR: envBoolOr("NOTIFY_ALLOW_PRIVATE_CIDR", false),
			TimeoutSeconds:   envPositiveIntOr("NOTIFY_TIMEOUT_SECONDS", 10),
			QueueCapacity:    envPositiveIntOr("NOTIFY_QUEUE_CAPACITY", 512),
			DrainSeconds:     envPositiveIntOr("NOTIFY_DRAIN_SECONDS", 5),
		},
		LSP: lspConfigFromEnv(),
	}
	if err := exportRPCAddrIfMissing(os.Setenv, cfg.RPCAddr); err != nil {
		return nil, err
	}
	if err := exportDatabaseURLIfMissing(os.Setenv, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	return cfg, nil
}

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
	case "run-debug.sh", "run-debug.ps1", "run-new-ui-desktop.sh", "make run-agent-terminal-debug", "make run-agent-terminal-debug-plain":
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
			return fmt.Errorf("load packaged .env %s: %w", path, readFailure)
		}
		return nil
	}
	return applyDotEnv(os.Setenv, path, string(content), packaged)
}

func applyDotEnv(setenv func(string, string) error, path, content string, strict bool) error {
	for i, line := range strings.Split(content, "\n") {
		key, value, ok, err := parseDotEnvLineStrict(line, i+1)
		if err != nil {
			if strict {
				return fmt.Errorf("parse packaged .env %s: %w", path, err)
			}
			continue
		}
		if !ok || strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		if err := setenv(key, value); err != nil {
			return fmt.Errorf("set environment from .env %s for %s: %w", path, key, err)
		}
	}
	return nil
}

func parseDotEnvLine(line string) (string, string, bool) {
	key, value, ok, err := parseDotEnvLineStrict(line, 0)
	if err != nil {
		return "", "", false
	}
	return key, value, ok
}

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
	return false, fmt.Errorf("inspect packaged runtime manifest %s: %w", path, err)
}

// exportRPCAddrIfMissing sets GO_AGENT_CTL_RPC_ADDR in the process environment
// when neither the canonical nor legacy env var is present. This ensures that
// normalizeManifestEnv (dto/provider/manifest.go) can propagate the resolved
// RPC address to MCP child processes (mcp-orch, mcp-lsp) automatically.
// Without this, mcp-orch falls back to localLauncher and spawns the desktop
// binary as a subprocess, which crashes immediately.
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

func exportDatabaseURLIfMissing(setenv func(string, string) error, databaseURL string) error {
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
		return nil
	}
	if strings.TrimSpace(databaseURL) == "" {
		return nil
	}
	if err := setenv("DATABASE_URL", databaseURL); err != nil {
		return fmt.Errorf("set DATABASE_URL: %w", err)
	}
	return nil
}

func resolveProjectRoot() string {
	if root := strings.TrimSpace(os.Getenv("PROJECT_ROOT")); root != "" {
		return root
	}
	if exe, err := os.Executable(); err == nil {
		if root := resolvePackagedProjectRoot(exe); root != "" {
			if info, err := os.Stat(filepath.Join(root, "migrations")); err == nil && info.IsDir() {
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

func envBoolOr(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envPositiveIntOr(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
