# macOS Gray Release Packaging

Use `scripts/package_macos.sh` with `SUPER_DOLPHIN_RELEASE_PROFILE=gray` to produce the macOS gray release artifact.

The signed `gray` profile is intentionally stricter than the default `dev-local` profile:

- The macOS update artifact is `dist/package/macos/Super-Dolphin-<platform>.dmg`.
- The signed update manifest release asset is `Super-Dolphin-<platform>.update.json`.
- The DMG is signed through a Developer ID Application identity, notarized, stapled, checked with `spctl`, and then checksummed.
- The packaged app includes update configuration in `Contents/Resources/.env`.

For pre-release tester distribution without an Apple Developer ID, use `SUPER_DOLPHIN_RELEASE_PROFILE=gray-unsigned`. That profile still embeds update configuration, requires the Ed25519-signed update manifest, verifies the DMG SHA-256 from the manifest before install, and enables the updater helper's unsigned mode. It does not notarize the DMG or require a Developer ID Team ID, so macOS Gatekeeper can require testers to manually approve the app on first install.

## Required Environment

Set these variables before running the package script:

```bash
export SUPER_DOLPHIN_RELEASE_PROFILE=gray
export VERSION=0.1.0
export CODESIGN_IDENTITY="Developer ID Application: Example, Inc. (TEAMID1234)"
export NOTARY_PROFILE=super-dolphin-notary

export SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-
export SUPER_DOLPHIN_UPDATE_APP_ID=super-dolphin
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=<base64-encoded-32-byte-public-key>
export SUPER_DOLPHIN_UPDATE_SIGNING_KEY=<private-update-signing-key-for-manifest-generation>
export SUPER_DOLPHIN_UPDATE_ARTIFACT_URL=https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.0/Super-Dolphin-darwin-arm64.dmg
export SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.1.0
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray

export SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres/darwin-arm64
export SUPER_DOLPHIN_LSP_BUNDLE_DIR=/absolute/path/to/lsp-bundle
export SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
export SUPER_DOLPHIN_CODEX_SHA256=<trusted-codex-sha256>
export SUPER_DOLPHIN_CODEX_VERSION=<codex-version>

export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.com
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<bootstrap-token>
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=<bootstrap-proof>
```

`SUPER_DOLPHIN_UPDATE_GITHUB_REPO` must be exactly `xiaoxiaotest9527-bit/-`. `SUPER_DOLPHIN_UPDATE_ARTIFACT_URL` must be the GitHub release asset URL for the current platform, such as `Super-Dolphin-darwin-arm64.dmg` for this Mac. `SUPER_DOLPHIN_UPDATE_APP_ID` must stay `super-dolphin`, matching the runtime verifier. `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` must base64-decode to exactly 32 bytes. Keep `SUPER_DOLPHIN_UPDATE_SIGNING_KEY` out of the packaged app; it is required for signing the update manifest, not for runtime verification. `SUPER_DOLPHIN_UPDATE_MANIFEST_URL` is legacy compatibility only; do not use it for new GitHub Releases publishing.

For the GitHub Releases `v1.0` page at `https://github.com/xiaoxiaotest9527-bit/-/releases/tag/v1.0`, use:

```bash
export VERSION=1.0
export SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-
export SUPER_DOLPHIN_UPDATE_ARTIFACT_URL=https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.0/Super-Dolphin-darwin-arm64.dmg
export SUPER_DOLPHIN_UPDATE_APP_ID=super-dolphin
export SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.1.0
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray
```

As of June 8, 2026, GitHub reports the `v1.0` release as public with only the old `Super.Dolphin.dmg` asset. That release cannot satisfy the app update flow. Re-upload standardized assets such as `Super-Dolphin-darwin-arm64.dmg` and `Super-Dolphin-darwin-arm64.update.json` before enabling packaged updates. `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` and `SUPER_DOLPHIN_UPDATE_SIGNING_KEY` come from the Ed25519 update signing keypair and cannot be derived from GitHub release metadata.

## GitHub Release Asset Format

Use a semver tag such as `v1.2.3`. The artifact URL must use the same tag and the exact platform asset name:

```text
https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.2.3/Super-Dolphin-windows-amd64.exe
```

Upload these paired assets to the release:

```text
Super-Dolphin-darwin-arm64.dmg
Super-Dolphin-darwin-arm64.update.json
Super-Dolphin-darwin-amd64.dmg
Super-Dolphin-darwin-amd64.update.json
Super-Dolphin-windows-amd64.exe
Super-Dolphin-windows-amd64.update.json
Super-Dolphin-windows-arm64.exe
Super-Dolphin-windows-arm64.update.json
```

The update client chooses `.dmg` for `darwin-*`, `.exe` for `windows-*`, then verifies the matching `Super-Dolphin-<platform>.update.json` from the same release. Do not upload or reference `Super.Dolphin.dmg` for automatic updates.

## Build

Run from the repository root on macOS:

```bash
./scripts/package_macos.sh
```

Expected outputs:

```text
dist/package/macos/Super-Dolphin-darwin-arm64.dmg
dist/package/macos/Super-Dolphin-darwin-arm64.dmg.sha256
```

Do not publish `.app.zip` artifacts for gray releases.

## Unsigned Tester Build

Use this only before public release:

```bash
export SUPER_DOLPHIN_RELEASE_PROFILE=gray-unsigned
export VERSION=0.1.0-test.1
export SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=<base64-encoded-32-byte-public-key>
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray

./scripts/package_macos.sh
```

The output is still:

```text
dist/package/macos/Super-Dolphin-darwin-arm64.dmg
dist/package/macos/Super-Dolphin-darwin-arm64.dmg.sha256
```

Users can install it manually after approving Gatekeeper prompts. App-internal updates work only between builds that include `SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED=1`; production `gray` builds keep Developer ID and `spctl` enforcement.

## Smoke

After producing the DMG, generate the signed platform manifest with `cmd/super-dolphin-release-manifest`, then run the release smoke script:

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh manifest
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh relay-turn
```

`manifest` verifies the DMG, `Super-Dolphin-<platform>.dmg.sha256`, local `Super-Dolphin-<platform>.update.json`, GitHub repo/public key env, packaged update `.env`, and a fresh `go run ./cmd/super-dolphin-release-manifest` output. It does not publish the manifest, fetch GitHub Releases, or install an update.

To publish update assets, run the explicit upload script after the platform manifest matches the artifact:

```bash
./scripts/publish_github_update_assets.sh
```

The script requires `SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-`, chooses `.dmg` for `darwin-*` and `.exe` for `windows-*`, uploads the platform artifact plus `Super-Dolphin-<platform>.update.json` with `gh release upload --clobber`, then checks that both asset names exist in the target GitHub release. On macOS platforms it also runs `docs/scripts/macos_release_smoke.sh manifest`; on Windows platforms it regenerates the signed manifest with `cmd/super-dolphin-release-manifest` and compares it before uploading.

Use `docs/scripts/macos_release_smoke.sh update-loop` only after separately recording a real update download, verification, install, and relaunch. Without `SUPER_DOLPHIN_UPDATE_LOOP_SMOKE=1`, the mode exits with a `BLOCKER` instead of reporting an unexecuted update install as passed.

## Verify Update DMG

The packaged app verifier can validate the app directly or through the DMG update artifact:

```bash
UPDATE_DMG="dist/package/macos/Super-Dolphin-darwin-arm64.dmg" ./scripts/verify_packaged_app_macos.sh
```

The verifier mounts the DMG read-only, locates the `.app`, validates the full app structure, and detaches the mounted image on exit.
