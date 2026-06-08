# Windows Packaging Guide

This package must be built on Windows. The desktop host depends on the Windows
WebView2 runtime path, and the release package must contain Windows-native
`.exe` and `.cmd` launchers. Do not treat a macOS cross-compile as a release
artifact.

## Outputs

Default ARM64 package command for a Windows 11 on ARM packaging host:

```powershell
$env:SUPER_DOLPHIN_WINDOWS_ARCH = 'arm64'
.\scripts\package_windows_local.ps1 standard
```

Release package output on Windows ARM64:

```text
dist\package\windows\super-dolphin-0.1.0-windows-arm64.zip
```

For AMD64 packages, set `SUPER_DOLPHIN_WINDOWS_ARCH=amd64`; the platform suffix
is `windows-amd64`.

If Inno Setup `iscc.exe` is installed, the script also writes:

```text
dist\package\windows\SuperDolphinSetup-0.1.0-windows-arm64.exe
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

These inputs are required on the packaging host only. The end user's Windows 11
on ARM cloud desktop must not need Go, Node.js, PostgreSQL, Codex, gopls,
rust-analyzer, shellcheck, or ast-grep installed locally.

Set these in a private PowerShell profile or temporary shell. Do not commit real
tokens.

```powershell
$env:SUPER_DOLPHIN_WINDOWS_ARCH = 'arm64'
$env:SUPER_DOLPHIN_POSTGRES_DIST = 'C:\path\to\postgres-windows-arm64'
$env:SUPER_DOLPHIN_CODEX_ARTIFACT = 'C:\path\to\codex.exe'
$env:SUPER_DOLPHIN_SHELLCHECK_BIN = 'C:\path\to\shellcheck.exe' # required for arm64
$env:SUPER_DOLPHIN_CODEX_RELAY_BASE_URL = 'https://relay.example.com'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN = '<bootstrap-token>'
$env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF = '<bootstrap-proof>'
```

The packaging host must provide native ARM64 build inputs:

```powershell
go env GOOS      # windows
go env GOARCH    # arm64, unless SUPER_DOLPHIN_WINDOWS_ARCH overrides it
node -p "process.platform + ' ' + process.arch"  # win32 arm64
npm -v
git --version
```

The scripts fail fast if a `windows-arm64` package would include AMD64 PE
binaries. Native `.exe` and `.dll` files are checked from their PE machine type;
ARM64 packages accept `0xAA64` only.

`SUPER_DOLPHIN_POSTGRES_DIST` must contain:

```text
bin\postgres.exe
bin\initdb.exe
bin\pg_ctl.exe
bin\pg_config.exe
share\postgres.bki
```

For `windows-arm64`, the PostgreSQL runtime and all `.exe` / `.dll` files under
that directory must be ARM64. The same applies to the Codex CLI artifact, Node
runtime, Go toolchain, gopls, rust-analyzer, shellcheck, ast-grep, and the MSVC
runtime DLL copied into the LSP bundle.

The Windows LSP bundle pins npm package versions for reproducible packaging:

```text
typescript-language-server@5.3.0
typescript@6.0.3
vscode-langservers-extracted@4.10.0
pyright@1.1.410
bash-language-server@5.6.0
shellcheck@4.1.0      # amd64 only, unless SUPER_DOLPHIN_SHELLCHECK_BIN is set
@ast-grep/cli@0.43.0
```

`@ast-grep/cli` publishes a Windows ARM64 prebuild. The `shellcheck` npm package
does not provide the Windows ARM64 executable used by this package, so ARM64
packaging hosts must set `SUPER_DOLPHIN_SHELLCHECK_BIN` to a native ARM64
`shellcheck.exe`. The script copies that executable into the LSP bundle and
validates it with a PE architecture check plus `shellcheck.exe --version`.

The local helper computes `SUPER_DOLPHIN_CODEX_SHA256` and
`SUPER_DOLPHIN_CODEX_VERSION`, and uses `local-private-package` as the bootstrap
proof when `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF` is not set. Release
automation should call `scripts\package_windows.ps1` directly after setting the
checksum, version, and real bootstrap proof explicitly.

## Verification

The packaging script runs:

```powershell
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-arm64
```

This verification runs against the temporary staging directory before it is
compressed and cleaned. To inspect the expanded package manually, build with
`-KeepStage`. The verifier also accepts the zip artifact:

```powershell
.\scripts\verify_packaged_app_windows.ps1 dist\package\windows\super-dolphin-0.1.0-windows-arm64.zip
```

Acceptance for the end user's clean Windows 11 on ARM cloud desktop:

1. Install with `SuperDolphinSetup-<version>-windows-<arch>.exe`, or unzip the package to a fresh writable directory.
2. Do not install Go, Node.js, PostgreSQL, Codex, or LSP tools on the user machine.
3. Launch from the installed Start menu/desktop shortcut, or run `.\bin\agent-terminal.exe` from the unzipped package root.
4. Confirm the executable starts on Windows 11 ARM without x64 dependency prompts.
5. Confirm the desktop window opens.
6. Confirm first launch creates app data under `%APPDATA%\Super Dolphin`, not inside the package directory.
7. Confirm Provider settings accept Windows roots such as `C:\Users\alice\project` and UNC roots such as `\\server\share\repo`.
8. Confirm a Codex-backed conversation reaches the packaged `codex.exe app-server` path.

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
  "embedded_postgres_resource_path": "postgres/windows-arm64"
}
```

For Windows AMD64 this last value is `postgres/windows-amd64`.

The launcher sets `SUPER_DOLPHIN_PACKAGE_ROOT`, `PROJECT_ROOT`,
`GO_AGENT_PEER_BIN_DIR`, `SUPER_DOLPHIN_LSP_BUNDLE_DIR`,
`SUPER_DOLPHIN_LSP_MANIFEST`, `SUPER_DOLPHIN_RUNTIME_MODE=packaged`, and
`SUPER_DOLPHIN_PACKAGED_LAUNCHER=1` before starting `bin\agent-terminal.exe`.
