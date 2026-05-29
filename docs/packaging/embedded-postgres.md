# Packaging Super Dolphin With Embedded PostgreSQL

This is the MVP packaging path for macOS first, then Linux. The app keeps the
existing `sqlc + PostgreSQL + pgx` store layer and starts a bundled PostgreSQL
runtime when `DATABASE_URL` is not supplied.

## Runtime Behavior

1. If `DATABASE_URL` or `POSTGRES_CONNECTION_STRING` exists, the app uses that
   external database and does not start embedded PostgreSQL.
2. If no external database is configured, the app builds a local socket DSN and
   starts bundled PostgreSQL before the db pool runs migrations.
3. macOS data lives under:
   `~/Library/Application Support/Super Dolphin/postgres/data`.
4. Linux data lives under:
   `$XDG_DATA_HOME/super-dolphin/postgres/data` or
   `~/.local/share/super-dolphin/postgres/data`.
5. The packaged app looks for PostgreSQL binaries under:
   `Contents/Resources/postgres/<goos-goarch>/bin` on macOS.

## PostgreSQL Runtime Input

Provide a PostgreSQL runtime prefix that contains at least:

```text
bin/postgres
bin/initdb
bin/pg_ctl
share/
lib/        # when the distribution uses dynamic libraries
```

Default source path:

```bash
third_party/postgres/$(go env GOOS)-$(go env GOARCH)
```

Override:

```bash
export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
```

## macOS Package

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg
export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=rust-v0.0.0
./scripts/package_macos.sh
```

The macOS package script writes the relay settings into
`Contents/Resources/.env` so GUI launches can bootstrap the app-managed Codex
home without user shell environment variables. A release package must be built
with production relay URL and public bootstrap credential values; the script fails fast when either value is
missing.

Output:

```text
dist/package/macos/Super Dolphin.app
dist/package/macos/Super Dolphin.dmg
```

Before calling the macOS package ready, verify the staged app bundle:

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

For local testing the script ad-hoc signs the app. For distribution:

```bash
export CODESIGN_IDENTITY="Developer ID Application: Your Team"
export NOTARY_PROFILE="notarytool-profile-name"
./scripts/package_macos.sh
```

## Clean VM Acceptance

Before calling the package ready for users, run the clean VM checklist:

- `docs/packaging/macos-clean-vm-checklist.md`

## Linux Package

Run on Linux or inside a Linux builder:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg
export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com/v1
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=rust-v0.0.0
./scripts/package_linux.sh
```

Output:

```text
dist/package/linux/super-dolphin-0.1.0-linux-amd64.tar.gz
```

The Linux package includes `run.sh`, which sets `PROJECT_ROOT` and
`SUPER_DOLPHIN_POSTGRES_BIN_DIR` before launching `bin/agent-terminal`. It also
writes `runtime-manifest.json` at the tarball root with relocatable relative
paths for `bundled_codex_path`, `model_registry_path`, and
`embedded_postgres_resource_path`.

## Codex Provider

Release packaging is bundled-first. The macOS and Linux package scripts require
`SUPER_DOLPHIN_CODEX_ARTIFACT`, `SUPER_DOLPHIN_CODEX_SHA256`, and
`SUPER_DOLPHIN_CODEX_VERSION` by default through
`SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1`. The checksum must come from a trusted
release manifest or signature verification path, not from the same untrusted
download channel as the artifact.

The verified artifact is copied to `Contents/Resources/bin/codex` on macOS and
`bin/codex` on Linux. Packaging also writes `codex-manifest.json` containing the
bundled Codex version and SHA-256 digest. Release packaging must not discover a
local `command -v codex` or `/Applications/Codex.app` binary as a trusted source.

At runtime, the packaged `bin` directory is the highest-priority controlled
lookup path, so bundled `codex` is used before any user-writable PATH entry. If a
bundled Codex binary exists but is not executable or fails validation, startup
fails fast as a damaged package asset instead of falling through to a local or
downloaded binary.

Runtime download is only a fallback for non-bundled/dev cases. It is allowed only
from the official OpenAI Codex release endpoint or an explicitly configured
trusted mirror, and it requires `SUPER_DOLPHIN_CODEX_RELEASE_SHA256` before the
downloaded asset is extracted or executed.

The packaged desktop preflight also requires bundled relay configuration. The
package scripts source this from build-time
`SUPER_DOLPHIN_CODEX_RELAY_BASE_URL` and `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN`
and write it into the app resources `.env`; users should not set these manually
on a clean VM. At packaged runtime, missing relay config is a startup-blocking
preflight error instead of a silent Codex bootstrap skip.

For local package smoke, run the committed harness instead of relying on manual
notes:

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh startup
```

The `local` mode verifies the app bundle, mounted DMG contents, packaged relay
`.env`, runtime manifest, and bundled `codex app-server --help` with external
Codex paths hidden. The `startup` mode launches the packaged app with a
temporary `HOME`, a sanitized `PATH`, and
`SUPER_DOLPHIN_CODEX_RELEASE_API_URL=http://127.0.0.1:9/latest` so a release
smoke cannot silently rely on a host Codex install or an external Codex
download.

For release-only blockers, run the fail-fast preflight modes and keep their logs
with the release packet:

```bash
docs/scripts/macos_release_smoke.sh blockers
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh relay-turn
```

These commands do not make the package release-qualified by themselves; they
only turn missing clean-VM, notarization, production relay, or GUI Codex-turn
preconditions into explicit blocker logs.

For tests or controlled staging, override the fallback release endpoint or
install root:

```bash
export SUPER_DOLPHIN_CODEX_RELEASE_API_URL=http://127.0.0.1:8080/latest
export SUPER_DOLPHIN_CODEX_TRUSTED_RELEASE_MIRROR=1
export SUPER_DOLPHIN_CODEX_RELEASE_SHA256=<trusted-sha256>
export SUPER_DOLPHIN_CODEX_INSTALL_ROOT=/tmp/super-dolphin-codex
```
