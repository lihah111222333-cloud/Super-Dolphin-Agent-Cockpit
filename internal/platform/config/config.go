package config

import (
	"log"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string
	RPCAddr     string
	LogLevel    string
	ProjectRoot string
}

func New() *Config {
	return &Config{
		DatabaseURL: envOr("DATABASE_URL", "postgres://mima0000@127.0.0.1:54320/super_agent_v3?sslmode=disable"),
		RPCAddr:     envOrCompat("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR", "127.0.0.1:8090"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		ProjectRoot: resolveProjectRoot(),
	}
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
