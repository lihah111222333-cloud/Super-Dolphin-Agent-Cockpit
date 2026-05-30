# Linux Tarball Packaging Guide

This guide is the operator runbook for building and verifying the Linux
`super-dolphin` tarball. It complements the macOS DMG guide and documents the
current Linux scripts exactly as they exist in this repository.

> Scope note: Linux packaging currently produces a relocatable `.tar.gz` stage,
> not a `.deb`, `.rpm`, AppImage, or DMG-equivalent installer.

## Outputs

Default release output:

```text
dist/package/linux/super-dolphin-0.1.0-linux-amd64/
dist/package/linux/super-dolphin-0.1.0-linux-amd64.tar.gz
```

The exact path is controlled by:

```text
APP_NAME=${APP_NAME:-super-dolphin}
VERSION=${VERSION:-0.1.0}
platform=$(go env GOOS)-$(go env GOARCH)
```

Examples:

```text
dist/package/linux/super-dolphin-0.1.0-linux-amd64.tar.gz
dist/package/linux/super-dolphin-0.1.0-linux-arm64.tar.gz
dist/package/linux/super-dolphin-full-lsp-0.1.0-linux-amd64.tar.gz
```

The package script overwrites only the exact stage directory and tarball for the
selected `APP_NAME/VERSION/platform`:

```bash
rm -rf "$stage" "$stage.tar.gz"
```

It does **not** delete the whole `dist/package` tree.

## Current Script Inventory

```text
scripts/package_linux.sh              # release packaging entrypoint
scripts/package_linux_local.sh        # local convenience wrapper
scripts/prepare_lsp_bundle_linux.sh   # builds the Linux LSP bundle
scripts/verify_packaged_app_linux.sh  # validates a stage directory or .tar.gz
```

Makefile wrapper:

```bash
make package-linux
```

`make package-linux` just calls `./scripts/package_linux.sh`, so all required
environment variables below still apply.

## What the Linux Tarball Contains

The generated package root contains at least:

```text
run.sh
.env
runtime-manifest.json
codex-manifest.json
models.yaml
migrations/
bin/agent-terminal
bin/mcp-orch
bin/mcp-lsp
bin/mcp-ida
bin/codex
bin/gopls                         -> ../lsp/bin/gopls
bin/go                            -> ../lsp/bin/go
bin/typescript-language-server    -> ../lsp/bin/typescript-language-server
bin/vscode-css-language-server    -> ../lsp/bin/vscode-css-language-server
bin/pyright-langserver            -> ../lsp/bin/pyright-langserver
bin/rust-analyzer                 -> ../lsp/bin/rust-analyzer
bin/sg                            -> ../lsp/bin/sg
lsp/
postgres/linux-<arch>/
```

For the `full` LSP profile, it also contains:

```text
bin/jdtls                         -> ../lsp/bin/jdtls
lsp/bin/java
lsp/jdk/
lsp/jdtls/
```

Important current gap:

- `scripts/package_linux.sh` does **not** bundle Git today.
- `run.sh` prepends `bin/` to `PATH`, then leaves the host `PATH` available.
- Therefore Git, if needed at runtime, is currently resolved from the target
  Linux system unless a future script change copies Git into `bin/`.
- If the release requirement is “clean VM with no system Git”, Linux packaging
  is not release-complete until Git bundling/parity is added and verified.

## Runtime Entrypoint Behavior

Users run the package via:

```bash
./run.sh
```

`run.sh` resolves the package root and exports controlled runtime paths before
launching `bin/agent-terminal`:

```bash
export PROJECT_ROOT="$here"
export SUPER_DOLPHIN_MODEL_REGISTRY="$here/models.yaml"
export SUPER_DOLPHIN_POSTGRES_BIN_DIR="$here/postgres/linux-$arch/bin"
export PATH="$here/bin:${PATH:-}"
export GO_AGENT_PEER_BIN_DIR="$here/bin"
export SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$here/lsp"
export SUPER_DOLPHIN_LSP_MANIFEST="$here/lsp/lsp-manifest.json"
exec "$here/bin/agent-terminal" "$@"
```

The script maps Linux machine architecture as follows:

```text
x86_64        -> amd64
aarch64/arm64 -> arm64
```

Unsupported architectures fail fast.

## Builder Requirements

Run Linux packaging on Linux. The release script fails fast elsewhere:

```text
package_linux.sh must run with GOOS=linux
```

Minimum toolchain and host tools:

- Go matching `go.mod`.
- Node.js and npm.
- Bash.
- `git` for repository operations.
- `rsync`.
- `tar` and gzip support.
- SHA-256 tool: `sha256sum` preferred, `shasum` fallback.
- C/C++ build dependencies needed by this Go/Wails stack on the chosen distro.
- PostgreSQL runtime input prepared for the target architecture.
- Codex CLI release artifact for Linux and its trusted SHA-256.
- LSP dependencies:
  - `gopls`
  - `rust-analyzer`
  - Go toolchain directory (`go env GOROOT` by default)
  - Node/npm for TypeScript/CSS/Python/ast-grep LSP packages
  - for `full`: JDK and jdtls home

Example Ubuntu/Debian host package names vary by distro release, but a typical
builder starts with packages like:

```bash
sudo apt-get update
sudo apt-get install -y \
  bash git rsync tar gzip ca-certificates curl \
  build-essential pkg-config \
  libgtk-3-dev libwebkit2gtk-4.1-dev \
  postgresql postgresql-server-dev-all
```

Install Go, Node/npm, gopls, rust-analyzer, Codex, and optionally jdtls/JDK using
your release-approved channels. Do not let the packaging script download or pick
untrusted release artifacts implicitly.

## Required Release Environment Variables

`scripts/package_linux.sh` is strict. These are required for a normal release
build:

```bash
export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
export SUPER_DOLPHIN_LSP_BUNDLE_DIR=/absolute/path/to/prepared-lsp-bundle
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-64-char-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>
```

Optional release variables:

```bash
export APP_NAME=super-dolphin
export VERSION=0.1.0
export SUPER_DOLPHIN_LSP_PROFILE=standard   # standard or full
export SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1
```

Forbidden for release packaging:

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
```

If `SUPER_DOLPHIN_CODEX_RELAY_API_KEY` is set, the packaging script aborts. A
package may contain a public bootstrap token, but it must not contain a
privileged relay API key.

Relay note: `package_linux.sh` validates
`SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF` as a release attestation input, but
the staged `.env` currently writes only:

```text
SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=...
SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=...
```

## PostgreSQL Runtime Input

The PostgreSQL prefix must contain executable binaries:

```text
bin/postgres
bin/initdb
bin/pg_ctl
bin/pg_config
```

The verifier also requires PostgreSQL share data, including `postgres.bki`:

```text
share/.../postgres.bki
```

Default source path if `SUPER_DOLPHIN_POSTGRES_DIST` is omitted:

```text
third_party/postgres/linux-$(go env GOARCH)
```

Recommended release behavior: always set `SUPER_DOLPHIN_POSTGRES_DIST`
explicitly so the package is reproducible and does not depend on a repository
placeholder directory.

### Creating a PostgreSQL Prefix From a Distro Install

There is currently no `scripts/build_relocatable_postgres_linux.sh`. If you use a
distro PostgreSQL install, assemble a prefix explicitly:

```bash
PG_RUNTIME="$PWD/.build-cache/postgres/linux-$(go env GOARCH)"
rm -rf "$PG_RUNTIME"
mkdir -p "$PG_RUNTIME/bin" "$PG_RUNTIME/share" "$PG_RUNTIME/lib"

rsync -a "$(pg_config --bindir)/" "$PG_RUNTIME/bin/"
rsync -a "$(pg_config --sharedir)/" "$PG_RUNTIME/share/"
rsync -a "$(pg_config --pkglibdir)/" "$PG_RUNTIME/lib/"

chmod +x "$PG_RUNTIME/bin/postgres" \
  "$PG_RUNTIME/bin/initdb" \
  "$PG_RUNTIME/bin/pg_ctl" \
  "$PG_RUNTIME/bin/pg_config"

export SUPER_DOLPHIN_POSTGRES_DIST="$PG_RUNTIME"
```

Then verify the minimum contract before packaging:

```bash
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/postgres"
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/initdb"
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/pg_ctl"
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/pg_config"
find "$SUPER_DOLPHIN_POSTGRES_DIST/share" -name postgres.bki -type f -print -quit
```

Dynamic library warning:

- `package_linux.sh` copies the provided PostgreSQL prefix with `rsync -aL`.
- It does not rewrite ELF RPATH/RUNPATH.
- If your PostgreSQL binaries depend on shared libraries outside the prefix, a
  minimal clean VM may still fail at runtime unless those libraries are present
  on the target OS or you provide a properly relocatable/self-contained prefix.
- Treat clean VM runtime smoke as required evidence, not optional polish.

## LSP Bundle Preparation

The release package script expects a prepared LSP bundle. It does not prepare the
bundle itself.

Standard profile:

```bash
export SUPER_DOLPHIN_LSP_PROFILE=standard
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/standard/$(go env GOOS)-$(go env GOARCH)"
./scripts/prepare_lsp_bundle_linux.sh
```

Full profile:

```bash
export SUPER_DOLPHIN_LSP_PROFILE=full
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/full/$(go env GOOS)-$(go env GOARCH)"
export SUPER_DOLPHIN_JDTLS_HOME=/absolute/path/to/jdtls
export SUPER_DOLPHIN_JDK_HOME=/absolute/path/to/jdk
./scripts/prepare_lsp_bundle_linux.sh
```

Useful overrides:

```bash
export SUPER_DOLPHIN_NODE_DIST=/absolute/path/to/node-prefix
export SUPER_DOLPHIN_NPM_BIN=/absolute/path/to/npm
export SUPER_DOLPHIN_GOPLS_BIN=/absolute/path/to/gopls
export SUPER_DOLPHIN_GO_TOOLCHAIN_DIR="$(go env GOROOT)"
export SUPER_DOLPHIN_RUST_ANALYZER_BIN=/absolute/path/to/rust-analyzer
```

The prepared bundle writes:

```text
lsp-manifest.json
lsp-checksums.sha256
bin/gopls
bin/typescript-language-server
bin/vscode-css-language-server
bin/pyright-langserver
bin/rust-analyzer
bin/sg
bin/go
bin/python      # stub that exits 127; prevents silent system Python fallback
bin/python3     # stub that exits 127; prevents silent system Python fallback
```

The `full` profile additionally writes Java/JDTLS entries.

Sanity check:

```bash
cd "$SUPER_DOLPHIN_LSP_BUNDLE_DIR"
sha256sum -c lsp-checksums.sha256
```

## Codex Artifact Preparation

Release builds must pass a specific Codex binary plus trusted metadata:

```bash
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-64-char-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>
```

The script verifies:

- the artifact exists;
- the artifact is executable;
- `SUPER_DOLPHIN_CODEX_SHA256` is a 64-character hex digest;
- the artifact digest matches the expected digest;
- the version value is non-empty.

The trusted SHA-256 must come from a release manifest/signature verification
path. Do not compute a “trusted” checksum from the same untrusted file you just
downloaded and call that a release verification.

Local helper exception: `scripts/package_linux_local.sh` computes the checksum
from `command -v codex` for developer smoke only. Do not distribute packages
created this way.

## First-Time Local Packaging Flow

Use this only for local smoke and development packaging. It prepares the LSP
bundle and then calls `scripts/package_linux.sh` with local values.

```bash
cd /path/to/Super-Dolphin

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-relay.example/v1"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<public-bootstrap-token>"

# Required unless you have third_party/postgres/linux-$(go env GOARCH).
export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/linux-$(go env GOARCH)"

# Optional; defaults to command -v codex.
export SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"

./scripts/package_linux_local.sh standard
```

Full LSP local package:

```bash
export SUPER_DOLPHIN_JDTLS_HOME=/absolute/path/to/jdtls
export SUPER_DOLPHIN_JDK_HOME=/absolute/path/to/jdk
./scripts/package_linux_local.sh full
```

Both profiles:

```bash
./scripts/package_linux_local.sh all
```

Local helper output warning:

```text
WARNING: local package contains the provided relay bootstrap token in .env; do not distribute it.
```

## Release Packaging Flow

Run on the Linux builder for the target architecture.

```bash
cd /path/to/Super-Dolphin

git status --short
# Ensure the worktree is the intended release commit and not a dirty build.

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY

export VERSION=0.1.0
export APP_NAME=super-dolphin
export SUPER_DOLPHIN_LSP_PROFILE=standard

export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/standard/$(go env GOOS)-$(go env GOARCH)"
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-64-char-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>

./scripts/prepare_lsp_bundle_linux.sh
./scripts/package_linux.sh
```

Expected result:

```text
dist/package/linux/super-dolphin-0.1.0-linux-$(go env GOARCH).tar.gz
```

Verify both the staged directory and the tarball:

```bash
linux_stage="dist/package/linux/${APP_NAME:-super-dolphin}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"

scripts/verify_packaged_app_linux.sh "$linux_stage"
scripts/verify_packaged_app_linux.sh "$linux_stage.tar.gz"
```

## Full LSP Release Flow

```bash
export APP_NAME=super-dolphin-full-lsp
export SUPER_DOLPHIN_LSP_PROFILE=full
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/full/$(go env GOOS)-$(go env GOARCH)"
export SUPER_DOLPHIN_JDTLS_HOME=/absolute/path/to/jdtls
export SUPER_DOLPHIN_JDK_HOME=/absolute/path/to/jdk

./scripts/prepare_lsp_bundle_linux.sh
./scripts/package_linux.sh

linux_stage="dist/package/linux/${APP_NAME}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"
scripts/verify_packaged_app_linux.sh "$linux_stage.tar.gz"
```

## New Packaging Machine Checklist

On each new Linux packaging machine:

1. Clone or sync the repository.
2. Checkout the release branch/commit.
3. Install Go matching `go.mod`.
4. Install Node/npm.
5. Install host build dependencies for the Wails/Linux stack.
6. Install or provide PostgreSQL runtime input.
7. Install or provide `gopls` and `rust-analyzer`.
8. Install or provide JDK/jdtls only if building `full`.
9. Place the release Codex artifact on disk.
10. Verify the Codex artifact against the release manifest/signature and export
    `SUPER_DOLPHIN_CODEX_SHA256` from that trusted source.
11. Set relay bootstrap environment variables. Do not set a privileged API key.
12. Prepare the LSP bundle.
13. Run `scripts/package_linux.sh`.
14. Run `scripts/verify_packaged_app_linux.sh` on both the stage and tarball.
15. Copy only the `.tar.gz` and release logs to the release packet.

Do not rely on hidden shell profile state. Store the packaging environment in a
redacted build log, with tokens omitted or redacted.

## Clean VM Smoke

Copy the generated tarball to a clean Linux VM with the same architecture and a
compatible libc/system library baseline.

```bash
tar -xzf super-dolphin-0.1.0-linux-amd64.tar.gz
cd super-dolphin-0.1.0-linux-amd64
```

Inspect package-local resources:

```bash
test -x bin/agent-terminal
test -x bin/codex
test -x bin/mcp-orch
test -x bin/mcp-lsp
test -x bin/mcp-ida
test -x postgres/linux-amd64/bin/postgres
test -f runtime-manifest.json
test -f codex-manifest.json
test -f lsp/lsp-manifest.json
```

Run the verifier if the repository is available on the VM:

```bash
/path/to/repo/scripts/verify_packaged_app_linux.sh "$PWD"
```

Run with a clean home and sanitized environment. Use a real desktop session for
GUI validation; headless VMs may need Xvfb or a Wayland/X11 session configured
by the tester.

```bash
CLEAN_HOME="$(mktemp -d)"
mkdir -p "$CLEAN_HOME/.local/share"

env -i \
  HOME="$CLEAN_HOME" \
  XDG_DATA_HOME="$CLEAN_HOME/.local/share" \
  PATH="/usr/bin:/bin" \
  DISPLAY="${DISPLAY:-}" \
  WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-}" \
  XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-}" \
  ./run.sh
```

Acceptance points:

- App starts without requiring `DATABASE_URL`.
- Embedded PostgreSQL initializes under the clean home data directory.
- Provider default is usable on first launch.
- Provider toggle can switch Codex/Claude.
- Codex path resolves to the bundled `bin/codex`, not a host binary.
- LSP manifest points inside the package root.
- No packaged resource symlink escapes the package root.
- If the VM has no Git and the tested workflow needs Git, record Linux Git
  bundling as a blocker rather than silently relying on host state.

## Verifier Coverage

`scripts/verify_packaged_app_linux.sh` accepts either:

```bash
scripts/verify_packaged_app_linux.sh dist/package/linux/<stage-dir>
scripts/verify_packaged_app_linux.sh dist/package/linux/<stage-dir>.tar.gz
```

It checks:

- tarball contains exactly one package root;
- required executables exist and are executable;
- `runtime-manifest.json` exists and uses expected relative paths;
- `codex-manifest.json` exists and the packaged Codex digest matches;
- `bin/codex app-server --help` succeeds;
- `lsp/lsp-manifest.json` exists and server paths/digests match;
- LSP server version/help smoke commands succeed;
- PostgreSQL binaries exist;
- `postgres.bki` exists under PostgreSQL `share`;
- `migrations/` exists and is non-empty;
- symlinks are not broken;
- symlinks do not escape the package root.

## Common Errors

### `package_linux.sh must run with GOOS=linux`

You are running on macOS or another non-Linux builder. Use a Linux VM, Linux CI
runner, or Linux build machine. This script is not a cross-packager.

### `missing PostgreSQL binary: .../bin/postgres`

`SUPER_DOLPHIN_POSTGRES_DIST` is unset, points to a placeholder, or points to a
split distro directory that is not a prefix. Build or assemble a prefix with
`bin/` and `share/`, then export `SUPER_DOLPHIN_POSTGRES_DIST`.

### `missing postgres.bki under .../share`

The PostgreSQL prefix contains binaries but not server share files. Copy
`$(pg_config --sharedir)` into the prefix `share/` directory.

### `packaged LSP bundle is required`

Run `scripts/prepare_lsp_bundle_linux.sh` first and export
`SUPER_DOLPHIN_LSP_BUNDLE_DIR` to the generated directory.

### `packaged LSP bundle checksum mismatch`

The bundle was modified after `lsp-checksums.sha256` was written, or the wrong
bundle directory was passed. Re-run `scripts/prepare_lsp_bundle_linux.sh` and
package again.

### `packaged Codex CLI artifact is required`

Set `SUPER_DOLPHIN_CODEX_ARTIFACT`, `SUPER_DOLPHIN_CODEX_SHA256`, and
`SUPER_DOLPHIN_CODEX_VERSION`. For local-only smoke, use
`scripts/package_linux_local.sh`.

### `Codex CLI artifact checksum mismatch`

The artifact on disk does not match the trusted SHA-256. Stop and fetch/verify
the correct artifact; do not update the expected digest just to make the package
pass.

### `SUPER_DOLPHIN_CODEX_RELAY_API_KEY must not be set for packaging`

Unset the privileged API key. Use the public bootstrap token variables instead.

### `missing bundled executable: ...`

`run.sh` validates bundled peer/LSP executables before launching. This usually
means the package was manually edited or the LSP bundle was incomplete.

## Release Evidence To Keep

For each Linux release candidate, keep:

```text
package command log with secrets redacted
scripts/prepare_lsp_bundle_linux.sh log
scripts/package_linux.sh log
scripts/verify_packaged_app_linux.sh <stage> log
scripts/verify_packaged_app_linux.sh <tar.gz> log
clean VM smoke notes/log
Codex artifact provenance and SHA-256 source
PostgreSQL runtime provenance
LSP/JDK provenance for full profile
```

Do not mark a Linux package release-ready from script success alone. Release
readiness requires verifier evidence plus a clean VM smoke for the target distro
baseline.
