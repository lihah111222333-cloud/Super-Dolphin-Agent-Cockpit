# macOS Gray Release Packaging

Use `scripts/package_macos.sh` with `SUPER_DOLPHIN_RELEASE_PROFILE=gray` to produce the macOS gray release artifact.

For the GitHub latest-release update flow used by the in-app updater, use
`scripts/package_macos_github_release.sh` and the cross-platform staging/publish
process in `docs/packaging/github-release-update.md`.

The signed `gray` profile is intentionally stricter than the default `dev-local` profile:

- The only user-facing and update artifact is `dist/package/macos/Super Dolphin.dmg`.
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

export SUPER_DOLPHIN_UPDATE_MANIFEST_URL=https://updates.example.com/super-dolphin/gray/latest.json
export SUPER_DOLPHIN_UPDATE_APP_ID=super-dolphin
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=<base64-encoded-32-byte-public-key>
export SUPER_DOLPHIN_UPDATE_SIGNING_KEY=<private-update-signing-key-for-manifest-generation>
export SUPER_DOLPHIN_UPDATE_ARTIFACT_URL=https://updates.example.com/super-dolphin/gray/Super%20Dolphin.dmg
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

`SUPER_DOLPHIN_UPDATE_MANIFEST_URL` and `SUPER_DOLPHIN_UPDATE_ARTIFACT_URL` must be HTTPS URLs with real hosts. `SUPER_DOLPHIN_UPDATE_APP_ID` must stay `super-dolphin`, matching the runtime verifier. `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` must base64-decode to exactly 32 bytes. Keep `SUPER_DOLPHIN_UPDATE_SIGNING_KEY` out of the packaged app; it is required for signing the update manifest, not for runtime verification.

For the GitHub Releases `v1.0` page at `https://github.com/xiaoxiaotest9527-bit/-/releases/tag/v1.0`, use:

```bash
export VERSION=1.0
export SUPER_DOLPHIN_UPDATE_MANIFEST_URL=https://github.com/xiaoxiaotest9527-bit/-/releases/latest/download/latest.json
export SUPER_DOLPHIN_UPDATE_ARTIFACT_URL=https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.0/Super.Dolphin.dmg
export SUPER_DOLPHIN_UPDATE_APP_ID=super-dolphin
export SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.1.0
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray
```

GitHub reports the `v1.0` release as public and published with a `Super.Dolphin.dmg` asset. Upload the generated `latest.json` manifest to the same release before enabling packaged updates; the manifest URL above must return JSON, not GitHub's 404 asset page. `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY` and `SUPER_DOLPHIN_UPDATE_SIGNING_KEY` come from the Ed25519 update signing keypair and cannot be derived from GitHub release metadata.

## Build

Run from the repository root on macOS:

```bash
./scripts/package_macos.sh
```

Expected outputs:

```text
dist/package/macos/Super Dolphin.dmg
dist/package/macos/Super Dolphin.dmg.sha256
```

Do not publish `.app.zip` artifacts for gray releases.

## Unsigned Tester Build

Use this only before public release:

```bash
export SUPER_DOLPHIN_RELEASE_PROFILE=gray-unsigned
export VERSION=0.1.0-test.1
export SUPER_DOLPHIN_UPDATE_MANIFEST_URL=https://updates.example.com/super-dolphin/gray/latest.json
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=<base64-encoded-32-byte-public-key>
export SUPER_DOLPHIN_UPDATE_CHANNEL=gray

./scripts/package_macos.sh
```

The output is still:

```text
dist/package/macos/Super Dolphin.dmg
dist/package/macos/Super Dolphin.dmg.sha256
```

Users can install it manually after approving Gatekeeper prompts. App-internal updates work only between builds that include `SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED=1`; production `gray` builds keep Developer ID and `spctl` enforcement.

## Smoke

After producing the DMG, generate the signed local `latest.json` with `cmd/super-dolphin-release-manifest`, then run the release smoke script:

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh manifest
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh relay-turn
```

`manifest` verifies the DMG, `Super Dolphin.dmg.sha256`, local `latest.json`, update manifest URL/public key env, packaged update `.env`, and a fresh `go run ./cmd/super-dolphin-release-manifest` output. It does not publish the manifest, fetch from the manifest URL, or install an update.

Use `docs/scripts/macos_release_smoke.sh update-loop` only after separately recording a real update download, verification, install, and relaunch. Without `SUPER_DOLPHIN_UPDATE_LOOP_SMOKE=1`, the mode exits with a `BLOCKER` instead of reporting an unexecuted update install as passed.

## Verify Update DMG

The packaged app verifier can validate the app directly or through the DMG update artifact:

```bash
UPDATE_DMG="dist/package/macos/Super Dolphin.dmg" ./scripts/verify_packaged_app_macos.sh
```

The verifier mounts the DMG read-only, locates the `.app`, validates the full app structure, and detaches the mounted image on exit.
