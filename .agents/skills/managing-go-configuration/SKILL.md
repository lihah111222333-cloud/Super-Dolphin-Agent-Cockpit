---
name: managing-go-configuration
description: Use when designing, adding, reviewing, loading, validating, documenting, testing, or changing Go configuration, environment variables, .env examples, config files, secret injection, secret redaction, config precedence, runtime config, credential rotation, or configuration guard rules.
---

# Managing Go Configuration

## Overview

Treat configuration as a typed runtime contract and secrets as externally supplied credentials. Configuration should be explicit, validated at startup, documented for operators, and safe to inspect; secrets must never be committed, logged, embedded in generated maps, or copied into examples as real values.

This skill owns configuration and secret-handling rules. Use `operating-go-services` for runtime lifecycle and observability, `securing-go-supply-chain` for repository and release security, and `guarding-go-projects` for executable gates.

## Industry Baseline

Use these practices as the default:

- Twelve-Factor style environment-specific configuration.
- Typed config structs with validation before serving traffic.
- Config precedence that is documented and deterministic.
- `.env.example` for local development placeholders only.
- Secret managers for real credentials in shared and production environments.
- Redaction for secret-like keys in logs, errors, traces, metrics, and debug output.
- Fail-fast startup for missing or invalid required configuration.

## Configuration Precedence

Use this order unless the project documents a stronger convention:

1. Safe code defaults for non-secret values.
2. Static config files under `configs/` for non-secret environment shape.
3. Environment variables for deployment-specific overrides.
4. Secret manager or CI/CD secret injection for credentials.
5. Command-line flags only for local tools or operational one-off commands.

Do not store real secrets in `configs/`, `.env.example`, tests, docs, generated files, or project maps.

## File Rules

| File | Policy |
| --- | --- |
| `.env` | Local only, gitignored, never committed |
| `.env.local` / `.env.*` | Local only, gitignored, never committed |
| `.env.example` | Committed placeholders only |
| `configs/*.yaml` / `*.yml` / `*.json` / `*.toml` | Non-secret defaults and structure only |
| CI secrets | Stored in CI secret store, not workflow files |
| Production secrets | Stored in secret manager, not repository files |

Allowed placeholder forms include `<secret-name>`, `${ENV_NAME}`, `changeme`, `example`, `dummy`, `placeholder`, and empty values.

## Required Rules

- Add or update `.env.example` when introducing a new environment variable.
- Keep variable names stable, uppercase, and scoped, such as `APPLE_HTTP_ADDR` or `APPLE_DATABASE_URL`.
- Parse configuration into typed structs. Do not pass raw environment lookups through business code.
- Validate required values, ranges, durations, URLs, enum values, and cross-field constraints at startup.
- Return configuration errors with context, but redact secret values.
- Never log raw values for keys containing `secret`, `token`, `password`, `passwd`, `credential`, `private_key`, `api_key`, `authorization`, or `cookie`.
- Tests must use `t.Setenv` or explicit config structs, not the developer's ambient environment.
- Domain and app packages must not read environment variables directly.
- Rotation-sensitive secrets should support versioning or a dual-read transition when the backing system requires it.
- Hot reload is disabled by default. If enabled, document which fields are reloadable and how validation/rollback works.

## Go Implementation Pattern

Keep config loading in `internal/platform/config` or an equivalent platform package:

```go
type Config struct {
    Environment string
    HTTPAddr    string
    DatabaseURL string
}

func Load() (Config, error) {
    cfg := Config{
        Environment: getenvDefault("APPLE_ENV", "development"),
        HTTPAddr:    getenvDefault("APPLE_HTTP_ADDR", ":8080"),
        DatabaseURL: os.Getenv("APPLE_DATABASE_URL"),
    }
    if err := cfg.Validate(); err != nil {
        return Config{}, fmt.Errorf("validate config: %w", err)
    }
    return cfg, nil
}
```

Do not expose `os.Getenv` calls outside platform/bootstrap/test code.

## Verification

After changing configuration, secrets, config docs, or guard rules:

```bash
make guard-change
make guard-commit
```

The project guard rejects staged local `.env` files and obvious non-placeholder secret values in committed config files.

## Common Mistakes

- Adding a new environment variable without updating `.env.example`.
- Treating `.env.example` as a convenient real local config.
- Logging an entire config struct that contains secrets.
- Reading environment variables from domain or application code.
- Making production behavior depend on an undocumented local default.
- Allowing hot reload for fields that cannot safely change at runtime.
