# macOS DMG Packaging Guide

This is the current macOS packaging runbook for the SQLite runtime. Super
Dolphin no longer packages or starts embedded PostgreSQL.

## Outputs

```text
dist/package/macos/Super Dolphin.app
dist/package/macos/Super Dolphin.dmg
```

Full LSP profile builds write the corresponding `Super Dolphin Full LSP`
artifacts.

## Current Script Inventory

```text
scripts/package_macos.sh              # release packaging entrypoint
scripts/package_macos_local.sh        # local convenience wrapper
scripts/prepare_lsp_bundle_macos.sh   # builds the macOS LSP bundle
scripts/verify_packaged_app_macos.sh  # validates the staged app bundle
docs/scripts/macos_release_smoke.sh   # release smoke harness
```

## Runtime Contract

The packaged app uses SQLite data under the packaged runtime home. Current
packages must not contain PostgreSQL binaries, `pg_ctl`, `initdb`,
`postgres.bki`, or an `embedded_postgres_resource_path` manifest entry.

Required resources include:

```text
Contents/MacOS/agent-terminal
Contents/Resources/.env
Contents/Resources/bin/codex
Contents/Resources/bin/git
Contents/Resources/bin/mcp-orch
Contents/Resources/bin/mcp-lsp
Contents/Resources/bin/mcp-ida
Contents/Resources/lsp/
Contents/Resources/internal/platform/db/sqlite/migrations/
Contents/Resources/runtime-manifest.json
Contents/Resources/codex-manifest.json
Contents/Resources/lsp/lsp-manifest.json
```

## Release Environment

Set these before a release build:

```bash
export SUPER_DOLPHIN_LSP_BUNDLE_DIR=/absolute/path/to/lsp-bundle
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-64-char-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
```

For signed gray releases also set `SUPER_DOLPHIN_RELEASE_PROFILE=gray`,
`CODESIGN_IDENTITY`, `NOTARY_PROFILE`, and update metadata as documented in
`docs/packaging/macos-gray-release.md`.

Do not set `SUPER_DOLPHIN_CODEX_RELAY_API_KEY` for packaging. The packaged
`.env` may contain public bootstrap credentials only.

## Local Package

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
./scripts/package_macos_local.sh standard
```

Use `full` or `all` when a full LSP bundle is required.

## Release Package

```bash
./scripts/prepare_lsp_bundle_macos.sh
./scripts/package_macos.sh
```

The release script fails fast when required bundle, Codex, relay, signing, or
update inputs are missing or malformed.

## Verification

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
docs/scripts/macos_release_smoke.sh local
```

For signed gray releases, also run the relevant smoke modes such as
`manifest`, `notarized-dmg`, and `relay-turn`.
