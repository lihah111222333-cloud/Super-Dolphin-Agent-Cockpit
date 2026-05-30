# A15: Integration Review and Final Gate

**Goal:** 在所有节点完成后做最终 diff、baseline、codemap、release gate 审查，防止把未覆盖改动混入主干。A15 是 review-only / verification-only 节点，不要求新增红测；完成依据是完整验证证据和分类报告。

**Files:**
- Read: `git diff --name-status origin/main...HEAD`
- Read: `git diff --name-status`
- Read: `git diff --cached --name-status`
- Read: `git status --short -uall`
- Read: `internal/archtest/baseline.json` diff if present
- Modify: `docs/reviews/package-embedded-pg-merge-readiness.md`
- Read/Modify: codemap docs only if `make codemap-check` reports required updates

**Steps:**
- [ ] Classify every committed file from `git diff --name-status origin/main...HEAD` as covered by A01-A14, generated/expected, or unrelated.
- [ ] Classify every unstaged file from `git diff --name-status` as covered, unrelated, or must-not-merge.
- [ ] Classify every staged file from `git diff --cached --name-status` as covered, unrelated, or must-not-merge.
- [ ] Classify every untracked file from `git status --short -uall` as covered, generated/ignored candidate, unrelated, or must-not-merge.
- [ ] For unrelated files, either create follow-up issue/plan or remove from merge scope after user approval.
- [ ] Verify A14 evidence includes internal affected packages, command packages, scripts, full frontend gate, broad Go gate when applicable, and release smoke logs.
- [ ] Treat missing/skipped macOS package, macOS built-app verifier, macOS fresh app-data startup smoke, or missing built artifact as Not ready to merge/release.
- [ ] Treat Linux as ready only with package+verifier evidence; if an explicit out-of-scope gate is used, report Linux release readiness as Deferred/Not ready with owner/date/reason/re-enable condition.
- [ ] Inspect and report any `internal/archtest/baseline.json` diff; do not freeze baseline without explicit user approval.
- [ ] Run archtest gate if baseline or guard rules changed, and record output.
- [ ] Run `make codemap-check` unconditionally as the final codemap gate; if it reports required updates, inspect and include the codemap diff.
- [ ] Apply clean worktree gate: before reporting Ready to merge, `git status --short -uall` must be empty. If any staged, unstaged, or untracked file remains, the report must say Not ready and list ownership/next action.
- [ ] Produce merge readiness report with Done / Deferred / Not covered / Release smoke evidence / Worktree state / Not ready sections.

**Validation:**
```bash
git status --short --branch -uall
git diff --name-status origin/main...HEAD
git diff --name-status
git diff --cached --name-status
git ls-files --others --exclude-standard
git diff --check
git diff -- internal/archtest/baseline.json
./scripts/test_with_guard.sh ./internal/archtest -count=1
make codemap-check
```

Release smoke evidence is a hard gate. A15 may reuse A14 evidence only when the report records artifact path, command, exit code, and log path; otherwise run and record:

```bash
scripts/package_macos.sh
mac_app_name="${APP_NAME:-Super Dolphin}"
mac_app="dist/package/macos/${mac_app_name}.app"
mac_dmg="dist/package/macos/${mac_app_name}.dmg"
scripts/verify_packaged_app_macos.sh "$mac_app"
APP_PATH="$mac_app" DMG_PATH="$mac_dmg" docs/scripts/macos_release_smoke.sh local
APP_PATH="$mac_app" STARTUP_WINDOW_SECONDS="${STARTUP_WINDOW_SECONDS:-20}" docs/scripts/macos_release_smoke.sh startup

scripts/package_linux.sh
linux_app_name="${APP_NAME:-super-dolphin}"
linux_stage="dist/package/linux/${linux_app_name}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"
scripts/verify_packaged_app_linux.sh "$linux_stage"
```

If Linux is out of this release scope, A15 may accept the explicit A14 out-of-scope gate only for merge readiness, but the report must label Linux release readiness as Deferred/Not ready and must not present Linux as smoke-covered.
