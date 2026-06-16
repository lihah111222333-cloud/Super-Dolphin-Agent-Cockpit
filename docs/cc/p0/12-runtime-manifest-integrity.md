# A12: Runtime Manifest Integrity

**Goal:** packaged runtime manifest、LSP/Codex/embedded PG 资源路径、digest、权限和 package-root 归属必须严格校验；dev/debug 入口不能被 repo 残留 manifest 污染。

**Files:**
- Modify: `internal/platform/runtimeenv/**`
- Modify: `scripts/verify_packaged_app_macos.sh`
- Modify/Add: Linux package verifier or guard tests
- Test: `internal/platform/runtimeenv/*manifest*_test.go`
- Test: `scripts/*verify*_test.go`

**Boundary:**
- A02 负责判定 owner/dev、owner/packaged、sidecar contract。A12 只在 packaged intent/root 或 valid packaged sentinel 边界内做完整性校验。
- 显式 packaged launcher/root 下，manifest 缺失、损坏、缺必需字段或 path escape 必须 fail-fast，不能降级 dev。
- debug `.app` 或 dev repo 中残留 manifest/env 不能触发 packaged 完整性校验。
- Linux package root 必须由 package launcher 显式传入；不能靠当前工作目录或 repo-root manifest 猜 packaged。

**Steps:**
- [ ] Write red test: explicit macOS packaged launcher/root with missing manifest fails packaged startup instead of resolving to dev.
- [ ] Write red test: explicit Linux package root with missing manifest fails packaged startup instead of resolving to dev.
- [ ] Write red test: manifest missing required fields fails packaged startup.
- [ ] Write red test: manifest path escaping package root fails, including `..`, absolute paths, symlinks that resolve outside package root, and private paths such as `/Users/ai` or a real developer home.
- [ ] Write red test: dev repo-root `runtime-manifest.json` does not trigger packaged integrity checks for a dev executable.
- [ ] Write red test: Linux package root explicitly passed with valid manifest passes sentinel/integrity checks.
- [ ] Write red test: Linux explicit package root with malformed manifest fails fast.
- [ ] Write red test: LSP digest mismatch fails.
- [ ] Write red test: Codex binary/manifest missing fails.
- [ ] Write red test: Codex `source_sha256` or packaged binary digest mismatch fails.
- [ ] Write red test: embedded PG binaries/share missing fails.
- [ ] Write red test: embedded PG inventory or digest mismatch fails for required binaries and `share` assets.
- [ ] Write red test: packaged executable/resource permission mismatch fails, including bundled binary not executable and required data/share path not readable.
- [ ] Write red test: runtime manifest or verifier refuses symlinked resources that escape the package root even when the manifest path string is relative.
- [ ] Implement verifier and runtime checks without weakening dev defaults.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv ./scripts -run 'Test.*Manifest|Test.*Verify' -count=1
bash -n scripts/verify_packaged_app_macos.sh
if [ -f scripts/verify_packaged_app_linux.sh ]; then bash -n scripts/verify_packaged_app_linux.sh; fi
```
