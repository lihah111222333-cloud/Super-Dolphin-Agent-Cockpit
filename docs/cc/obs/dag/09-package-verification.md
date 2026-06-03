# DAG Task 09 - Package Verification

Worker: W5 (`work/obs-tail-verify`)

Depends on: 03, 04, 05, 06, 08

Purpose: run affected-package verification after all accepted code branches are merged into the integration branch.

Files:

- No production edits expected.
- May update `docs/cc/obs/backend-observability-readiness-review-2026-06-03.md` with exact verification output summary after commands pass.

Commands:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/module/observability -count=1
```

If frontend tests changed:

```bash
cd frontend-app
npm test -- ObservabilityPage.test.jsx backendApi.test.js
```

Expected: all commands exit 0.

Constraints:

- Do not mark the integration ready if any command fails.
- If failures are unrelated to this change, capture exact evidence before deciding.
