# macOS Gray Release Packaging

Use `scripts/package_macos.sh` with `SUPER_DOLPHIN_RELEASE_PROFILE=gray` to
produce the signed macOS gray release artifact. This release path uses SQLite
and must not package embedded PostgreSQL.

For the GitHub latest-release update flow used by the in-app updater, use
`scripts/package_macos_github_release.sh` and the cross-platform staging/publish
process in `docs/运维发布/打包发布/github-release-update.md`.

## Required Environment

```bash
export SUPER_DOLPHIN_RELEASE_PROFILE=gray
export VERSION=0.1.0
export CODESIGN_IDENTITY="Developer ID Application: Example, Inc. (TEAMID1234)"
export NOTARY_PROFILE=super-dolphin-notary

export SUPER_DOLPHIN_UPDATE_MANIFEST_URL=https://updates.example.com/super-dolphin/gray/latest.json
export SUPER_DOLPHIN_UPDATE_APP_ID=super-dolphin
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=<base64-encoded-32-byte-public-key>
export SUPER_DOLPHIN_UPDATE_SIGNING_KEY=<private-update-signing-key-for-manifest-generation>
export SUPER_DOLPHIN_UPDATE_ARTIFACT_URL=https://updates.example.com/super-dolphin/gray/Super%20Dolphin.dmg
export SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.1.0
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray

export SUPER_DOLPHIN_LSP_BUNDLE_DIR=/absolute/path/to/lsp-bundle
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-codex-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<public-bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<relay-owner-attestation-or-config-id>
```

`SUPER_DOLPHIN_UPDATE_MANIFEST_URL` and
`SUPER_DOLPHIN_UPDATE_ARTIFACT_URL` must be HTTPS URLs with real hosts.
`SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` must decode to exactly 32 bytes. Keep
`SUPER_DOLPHIN_UPDATE_SIGNING_KEY` out of the packaged app.

## Build

```bash
./scripts/prepare_lsp_bundle_macos.sh
./scripts/package_macos.sh
```

Expected outputs:

```text
dist/package/macos/Super Dolphin.dmg
dist/package/macos/Super Dolphin.dmg.sha256
```

Do not publish `.app.zip` artifacts for gray releases.

## Smoke

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh manifest
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh relay-turn
```

Use `docs/scripts/macos_release_smoke.sh update-loop` only after separately
recording a real update download, verification, install, and relaunch.
