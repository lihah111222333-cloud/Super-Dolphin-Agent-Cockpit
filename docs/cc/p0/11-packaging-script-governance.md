# A11: Packaging Script Governance

**Goal:** macOS/Linux release scripts 和 local helpers 不再硬编码真实私人 URL、`/Users/ai`、交互输入 release key/token 或打包机固定路径；所有 release 输入来自显式 env/profile/manifest，并保留现有 release env 名直到全链路同步完成。

**Files:**
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_linux.sh`
- Modify: `scripts/package_macos_local.sh`
- Modify: `scripts/package_linux_local.sh`
- Add/Modify: `.env.packaging.example`
- Test: `scripts/package_macos_guard_test.go`
- Test: `scripts/package_linux_guard_test.go`

**Steps:**
- [ ] Write red test: release scripts and local helpers containing `/Users/ai` or another private absolute home path fail guard.
- [ ] Write red test: release scripts and local helpers containing real/private relay URL defaults fail guard; examples/placeholders must be non-secret and clearly non-production.
- [ ] Write red test: `read -s` / `read -p` or equivalent interactive secret prompt for release key/token fails guard in both release scripts and local helpers.
- [ ] Keep and govern the existing release env names: `SUPER_DOLPHIN_POSTGRES_DIST`, `SUPER_DOLPHIN_LSP_BUNDLE_DIR`, `SUPER_DOLPHIN_CODEX_ARTIFACT`, `SUPER_DOLPHIN_CODEX_SHA256`, `SUPER_DOLPHIN_CODEX_VERSION`, `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL`, `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN`, and `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF`.
- [ ] Do not introduce new env names such as `SUPER_DOLPHIN_POSTGRES_BUNDLE_DIR` or `SUPER_DOLPHIN_CODEX_BUNDLE_PATH` unless the same change updates macOS release, Linux release, both local helpers, guard tests, `.env.packaging.example`, docs, and A14 release smoke commands in one patch.
- [ ] Reject privileged or raw API key inputs for packaging (`SUPER_DOLPHIN_CODEX_RELAY_API_KEY`, `OPUSCLAW_API_KEY`, or similar) unless they are converted to a non-privileged bootstrap token before package creation and are never written into artifacts.
- [ ] Add `.env.packaging.example` with names and descriptions only, no real secrets.
- [ ] Keep `dev-local` helper explicit and separate from release flow, but enforce the same no-private-URL, no-private-path, and no-secret-prompt guard rules.

**Validation:**
```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackage.*Guard|TestPackage.*Governance' -count=1
bash -n scripts/package_macos.sh scripts/package_linux.sh scripts/package_macos_local.sh scripts/package_linux_local.sh
```
