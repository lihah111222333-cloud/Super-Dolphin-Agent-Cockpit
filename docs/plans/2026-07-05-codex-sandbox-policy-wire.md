# Codex Sandbox Policy Wire Guard

## Review Scope

- r36 was based on remote `main` at `5ea00f8b2eaa6911239d1e9605f62a9c50a30a68`.
- Baseline validation passed before review: `npm ci`, `npm run lint`, `npm test`, and `npm run build` in `frontend-app`.
- 20 read-only review agents covered frontend state, API contracts, Wails/native boundaries, datasource import, provider runtime config, packaging, observability/logging, workflow/DAG, and accessibility.
- 5 cross-decision agents ranked the reviewed candidates.

## Evidence Summary

`internal/provider/codexapp/support.go` builds `threadStartParams.SandboxPolicy` from runtime sandbox config, but `internal/provider/codexapp/driver.go` tags the field as `json:"-"`. Existing tests prove the in-memory struct contains the policy, but not that the JSON-RPC `thread/start` payload carries it.

Impact: restricted read-only or workspace-write sandbox policy can be silently omitted from Codex app-server startup, weakening the intended permission boundary while local code appears to have configured it.

## Final Decision

Winner: fix the Codex `thread/start` wire payload so `sandboxPolicy` is serialized.

Decision result: 3 of 5 cross-decision agents selected this candidate as the best r36 fix. The other winners were logger relay nested secret redaction and datasource picker grants, both retained as future candidates.

## Unique Fix

- Add a wire-level regression test that starts a Codex session through the test websocket server, captures actual `thread/start` params, and asserts restricted `sandboxPolicy.access.readableRoots` is present.
- Change `threadStartParams.SandboxPolicy` from `json:"-"` to `json:"sandboxPolicy,omitempty"`.

## Rejected Candidates

- Datasource local file picker grant: high security value, but needs a cross-module grant contract between Wails file selection and datasource RPC.
- Logger relay nested `slog.Any` redaction: high security value and bounded, but less directly permission-bearing than sandbox startup policy.
- Binding rollback Codex identity fields: important failed-launch recovery issue, but affects narrower failure windows.
- Thread `SetConfig` configure-before-persist: real consistency risk, but fix ordering and rollback semantics are more delicate.
- Frontend sequence poisoning and project/window races: production value, but lower security impact than sandbox policy omission.

## Upper Defense

The upper defense is the wire-level `StartSession` regression test. It prevents future changes from only preserving the in-memory `SandboxPolicy` field while omitting the JSON-RPC payload field.

## Tasks

- [x] RED: add a `StartSession` wire regression for restricted sandbox policy.
- [x] GREEN: serialize `sandboxPolicy` in `threadStartParams`.
- [x] Verify focused Codex provider tests and package guard.
- [x] Run required frontend validation for the loop gate.
- [ ] Commit, push directly to remote `main`, and clean the r36 local worktree/branch.

Observed RED: `go test ./internal/provider/codexapp -run TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire -count=1` failed because the captured `thread/start` params had no `sandboxPolicy`.

Observed GREEN: the same focused test passed after changing `SandboxPolicy` to `json:"sandboxPolicy,omitempty"`.

LSP diagnostics blocker: Go diagnostics for `driver.go` and `driver_session_test.go` could not run because the LSP tool reported that `gopls` was still not found in `PATH` after auto-install.

Observed verification:

- `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire|TestBuildThreadStartParams.*SandboxPolicy' -count=1`: passed.
- `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`: passed.
- `npm run lint`: passed.
- `npm test`: 79 files and 1015 tests passed.
- `npm run build`: passed.
- `git diff --check`: passed.

## Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire|TestBuildThreadStartParams.*SandboxPolicy' -count=1
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
git diff --cached --check
```

## Stop Boundary

This round fixes one unique winner only. Other evidenced candidates remain for future rounds. The loop only stops after two full review and cross-decision rounds produce no new evidenced, production-value, implementable fix.
