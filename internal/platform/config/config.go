package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type SkillConfig struct {
	ProgressiveDisclosure bool
	TokenBudget           int
}

type AgentConfig struct {
	PersistentSubagentDefault bool
}

type Config struct {
	DatabaseURL string
	RPCAddr     string
	LogLevel    string
	ProjectRoot string
	Skill       SkillConfig
	Agent       AgentConfig
}

func New() *Config {
	cfg := &Config{
		DatabaseURL: envOr("DATABASE_URL", "postgres://mima0000@127.0.0.1:54320/super_agent_v3?sslmode=disable"),
		RPCAddr:     envOrCompat("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR", "127.0.0.1:8090"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		ProjectRoot: resolveProjectRoot(),
		Skill: SkillConfig{
			ProgressiveDisclosure: envBoolOr("SKILL_PROGRESSIVE_DISCLOSURE", false),
			TokenBudget:           envPositiveIntOr("SKILL_TOKEN_BUDGET", 3000),
		},
		Agent: AgentConfig{
			PersistentSubagentDefault: envBoolOr("PERSISTENT_SUBAGENT_DEFAULT", false),
		},
	}
	exportRPCAddrIfMissing(cfg.RPCAddr)
	exportDatabaseURLIfMissing(cfg.DatabaseURL)
	return cfg
}

// exportRPCAddrIfMissing sets GO_AGENT_CTL_RPC_ADDR in the process environment
// when neither the canonical nor legacy env var is present. This ensures that
// normalizeManifestEnv (dto/provider/manifest.go) can propagate the resolved
// RPC address to MCP child processes (mcp-orch, mcp-lsp) automatically.
// Without this, mcp-orch falls back to localLauncher and spawns the desktop
// binary as a subprocess, which crashes immediately.
func exportRPCAddrIfMissing(addr string) {
	if strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR")) != "" {
		return
	}
	if strings.TrimSpace(os.Getenv("RPC_ADDR")) != "" {
		return
	}
	os.Setenv("GO_AGENT_CTL_RPC_ADDR", addr)
}

func exportDatabaseURLIfMissing(databaseURL string) {
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
		return
	}
	if strings.TrimSpace(databaseURL) == "" {
		return
	}
	os.Setenv("DATABASE_URL", databaseURL)
}

func resolveProjectRoot() string {
	if root := strings.TrimSpace(os.Getenv("PROJECT_ROOT")); root != "" {
		return root
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
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
		log.Printf("config env %s is deprecated; use %s instead before 2026-06-30", legacy, canonical)
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
