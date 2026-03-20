package config

import "os"

type Config struct {
	DatabaseURL string
	RPCAddr     string
	LogLevel    string
}

func New() *Config {
	return &Config{
		DatabaseURL: envOr("DATABASE_URL", "postgres://postgres:postgres@127.0.0.1:5432/super_agent_v3?sslmode=disable"),
		RPCAddr:     envOr("RPC_ADDR", "127.0.0.1:8080"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
