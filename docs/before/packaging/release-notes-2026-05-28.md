# Release Notes — T1 Runtime Validation Scope (2026-05-28)

## Scope

- macOS local packaged runtime smoke is in scope for this validation packet.
- Linux tarball: out-of-scope for this validation packet. No Linux tarball is release-qualified by these notes. If a Linux artifact is included in the release, Linux tarball build/start smoke becomes a P1 release blocker again.

## Status

Not release-qualified and not mergeable under the original release-smoke acceptance. The following P1 release blocker remains unless the plan/acceptance is explicitly changed:

- True macOS 断网启动 on a clean VM was not executed.
- Full post-start Codex 可用 validation was not executed as an end-to-end Codex turn against production relay. The local smoke only proves the bundled Codex binary starts `app-server --help` with external Codex paths hidden.
- Production relay credentials, notarized DMG, and clean-VM validation were not executed.

Fifth-fix note: packaged runtime now fails fast when bundled relay config is
missing, and the embedded PostgreSQL packaged smoke is backed by committed test
source. These fixes do not close the release-smoke blocker above.

Sixth-fix note: local macOS package, mounted DMG structure, bundled relay env,
bundled Codex help with external Codex paths hidden, and a local packaged app
startup window were re-run from committed scripts. Release-only preconditions are
now checked by `docs/scripts/macos_release_smoke.sh`, but the clean VM,
notarized DMG, production relay turn, and complete GUI Codex-turn blockers still
failed preflight and remain release blockers.

## Evidence captured

- `docs/reviews/smoke-logs/2026-05-28/macos-package-build-fourth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-app-structure-verify-fourth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-dmg-mount-verify-fourth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-no-external-codex-help.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-packaged-app-no-external-codex-startup-attempt.log`
- `docs/reviews/smoke-logs/2026-05-28/embedded-postgres-packaged-smoke.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-package-build-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-release-local-smoke-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-packaged-app-startup-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-release-blockers-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-notarized-dmg-smoke-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-production-relay-turn-sixth.log`
