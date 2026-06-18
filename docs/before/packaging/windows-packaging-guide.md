# Windows Packaging Guide

This is the current Windows packaging runbook for the SQLite runtime. Super
Dolphin no longer packages or starts embedded PostgreSQL.

## Outputs

Default ARM64 package command:

```powershell
$env:SUPER_DOLPHIN_WINDOWS_ARCH = 'arm64'
.\scripts\package_windows_local.ps1 standard
```

Release outputs:

```text
dist\package\windows\super-dolphin-0.1.0-windows-arm64.zip
dist\package\windows\SuperDolphinSetup-0.1.0-windows-arm64.exe
```

The installer is written when Inno Setup is available.

## Runtime Contract

The package uses SQLite migrations from
`internal/platform/db/sqlite/migrations`. Current packages must not contain
PostgreSQL binaries, `pg_ctl`, `initdb`, `postgres.bki`, or an
`embedded_postgres_resource_path` manifest entry.

The Windows runtime manifest uses relative paths for bundled Codex, LSP,
models, and SQLite migrations. User-facing shortcuts launch
`bin\agent-terminal.exe`; packaged runtime state is inferred from the install
root and created under `%APPDATA%\Super Dolphin`.

## Release Environment

Set these on the packaging host:

```powershell
$env:SUPER_DOLPHIN_WINDOWS_ARCH = 'arm64' # or amd64
$env:SUPER_DOLPHIN_LSP_BUNDLE_DIR = 'C:\path\to\lsp-bundle'
$env:SUPER_DOLPHIN_CODEX_ARTIFACT = 'C:\path\to\codex.exe'
$env:SUPER_DOLPHIN_CODEX_SHA256 = '<trusted-64-char-sha256>'
$env:SUPER_DOLPHIN_CODEX_VERSION = '<codex-version>'
$env:SUPER_DOLPHIN_CODEX_RELAY_BASE_URL = 'https://relay.example.com'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN = '<public-bootstrap-token>'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF = '<relay-owner-attestation-or-config-id>'
```

Do not set `SUPER_DOLPHIN_CODEX_RELAY_API_KEY` for packaging.

For ARM64 packages, every bundled `.exe` and `.dll` must be ARM64. The verifier
checks native binary architecture and fails fast on mixed-architecture payloads.

## Build

```powershell
.\scripts\package_windows.ps1 -Artifact all
```

For faster iteration:

```powershell
.\scripts\package_windows_local.ps1 standard -Artifact zip
.\scripts\package_windows_local.ps1 standard -Artifact installer
```

## Verification

```powershell
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-arm64
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-arm64.zip
```

Clean VM acceptance must prove the app starts without Go, Node.js, PostgreSQL,
Codex, or LSP tools installed on the target machine.
