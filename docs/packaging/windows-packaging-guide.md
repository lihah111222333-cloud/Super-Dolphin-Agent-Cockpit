# Windows Packaging Guide

This package must be built on Windows. The desktop host depends on the Windows
WebView2 runtime path, and the release package must contain Windows-native
`.exe` and `.cmd` launchers. Do not treat a macOS cross-compile as a release
artifact.

## Outputs

Default package command:

```powershell
.\scripts\package_windows_local.ps1 standard
```

Release package output on Windows amd64:

```text
dist\package\windows\super-dolphin-0.1.0-windows-amd64.zip
```

On Windows ARM64 the platform suffix is `windows-arm64`.

If Inno Setup `iscc.exe` is installed, the script also writes:

```text
dist\package\windows\SuperDolphinSetup-0.1.0-windows-amd64.exe
```

The expanded `super-dolphin-<version>-windows-<arch>\` directory is a temporary
staging directory. It is removed after a successful package build unless
`-KeepStage` or `SUPER_DOLPHIN_WINDOWS_KEEP_STAGE=1` is set.

For faster iteration, skip one of the two compression passes:

```powershell
.\scripts\package_windows_local.ps1 standard -Artifact installer
.\scripts\package_windows_local.ps1 standard -Artifact zip
```

`-Artifact all` is the default and writes both the installer and zip.

If `iscc.exe` is not on `PATH`, set `INNO_SETUP_ISCC` to the compiler path, or
use the default Inno Setup installation path:

```text
C:\Program Files (x86)\Inno Setup 6\ISCC.exe
```

## Required Inputs

Set these in a private PowerShell profile or temporary shell. Do not commit real
tokens.

```powershell
$env:SUPER_DOLPHIN_POSTGRES_DIST = 'C:\path\to\postgres-windows-amd64'
$env:SUPER_DOLPHIN_CODEX_ARTIFACT = 'C:\path\to\codex.exe'
$env:SUPER_DOLPHIN_CODEX_RELAY_BASE_URL = 'https://relay.example.com'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN = '<bootstrap-token>'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF = '<bootstrap-proof>'
```

`SUPER_DOLPHIN_POSTGRES_DIST` must contain:

```text
bin\postgres.exe
bin\initdb.exe
bin\pg_ctl.exe
bin\pg_config.exe
share\postgres.bki
```

The local helper computes `SUPER_DOLPHIN_CODEX_SHA256` and
`SUPER_DOLPHIN_CODEX_VERSION`, and uses `local-private-package` as the bootstrap
proof when `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF` is not set. Release
automation should call `scripts\package_windows.ps1` directly after setting the
checksum, version, and real bootstrap proof explicitly.

## Verification

The packaging script runs:

```powershell
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-amd64
```

This verification runs against the temporary staging directory before it is
compressed and cleaned. To inspect the expanded package manually, build with
`-KeepStage`. The verifier also accepts the zip artifact:

```powershell
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-amd64.zip
```

Acceptance for a clean machine or Parallels VM:

1. Install with `SuperDolphinSetup-<version>-windows-<arch>.exe`, or unzip the package to a fresh writable directory.
2. Launch from the installed Start menu/desktop shortcut, or run `.\bin\agent-terminal.exe` from the unzipped package root.
3. Confirm the desktop window opens.
4. Confirm first launch creates app data under `%APPDATA%\Super Dolphin`, not inside the package directory.
5. Confirm Provider settings accept Windows roots such as `C:\Users\alice\project` and UNC roots such as `\\server\share\repo`.

`run.ps1` and `run.cmd` are debug launchers. User-facing installer shortcuts point
directly at `bin\agent-terminal.exe`; packaged runtime environment is inferred by
the app from its install root.

## Package Runtime Contract

The Windows runtime manifest uses forward slashes and relative paths:

```json
{
  "bundled_codex_path": "bin/codex.exe",
  "bundled_gopls_path": "bin/gopls.exe",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml",
  "embedded_postgres_resource_path": "postgres/windows-amd64"
}
```

For Windows ARM64 this last value is `postgres/windows-arm64`.

The launcher sets `SUPER_DOLPHIN_PACKAGE_ROOT`, `PROJECT_ROOT`,
`GO_AGENT_PEER_BIN_DIR`, `SUPER_DOLPHIN_LSP_BUNDLE_DIR`,
`SUPER_DOLPHIN_LSP_MANIFEST`, `SUPER_DOLPHIN_RUNTIME_MODE=packaged`, and
`SUPER_DOLPHIN_PACKAGED_LAUNCHER=1` before starting `bin\agent-terminal.exe`.
