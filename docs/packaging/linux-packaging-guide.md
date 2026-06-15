# Linux Tarball Packaging Guide

This is the current Linux packaging runbook for the SQLite runtime. Super
Dolphin no longer packages or starts embedded PostgreSQL.

## Outputs

```text
dist/package/linux/super-dolphin-0.1.0-linux-amd64/
dist/package/linux/super-dolphin-0.1.0-linux-amd64.tar.gz
```

`APP_NAME`, `VERSION`, `GOOS`, and `GOARCH` determine the exact stage and
tarball names.

## Current Script Inventory

```text
scripts/package_linux.sh              # release packaging entrypoint
scripts/package_linux_local.sh        # local convenience wrapper
scripts/prepare_lsp_bundle_linux.sh   # builds the Linux LSP bundle
scripts/verify_packaged_app_linux.sh  # validates a stage directory or tarball
```

## Runtime Contract

The package uses SQLite migrations from
`internal/platform/db/sqlite/migrations`. Current packages must not contain
PostgreSQL binaries, `pg_ctl`, `initdb`, `postgres.bki`, or an
`embedded_postgres_resource_path` manifest entry.

The generated package root contains at least:

```text
run.sh
.env
runtime-manifest.json
codex-manifest.json
models.yaml
internal/platform/db/sqlite/migrations/
bin/agent-terminal
bin/mcp-orch
bin/mcp-lsp
bin/mcp-ida
bin/codex
lsp/
```

`run.sh` exports `PROJECT_ROOT`, `SUPER_DOLPHIN_MODEL_REGISTRY`,
`GO_AGENT_PEER_BIN_DIR`, `SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1`,
`SUPER_DOLPHIN_LSP_BUNDLE_DIR`, and `SUPER_DOLPHIN_LSP_MANIFEST` before
launching `bin/agent-terminal`.

## Release Environment

Set these before a release build:

```bash
export SUPER_DOLPHIN_LSP_PROFILE=standard
export SUPER_DOLPHIN_LSP_BUNDLE_DIR=/absolute/path/to/prepared-lsp-bundle
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-64-char-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
```

Do not set `SUPER_DOLPHIN_CODEX_RELAY_API_KEY` for packaging.

## Local Package

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
./scripts/package_linux_local.sh standard
```

Use `full` or `all` when a full LSP bundle is required.

## Release Package

```bash
./scripts/prepare_lsp_bundle_linux.sh
./scripts/package_linux.sh
```

## Verification

```bash
linux_stage="dist/package/linux/${APP_NAME:-super-dolphin}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"
scripts/verify_packaged_app_linux.sh "$linux_stage"
scripts/verify_packaged_app_linux.sh "$linux_stage.tar.gz"
```

Clean VM acceptance must prove the app starts without Go, Node.js, PostgreSQL,
Codex, or LSP tools installed on the target machine.
