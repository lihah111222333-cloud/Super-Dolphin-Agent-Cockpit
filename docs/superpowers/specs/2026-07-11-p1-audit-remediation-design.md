# P1 Audit Remediation Design

## Scope

This branch fixes only the five source-code P1 findings confirmed by the 2026-07-11 frontend/backend cross-audit:

1. Workflow mutations can time out in the UI while continuing on the backend, then a retry uses a new idempotency key and can create a duplicate run.
2. Malformed Codex terminal events are logged and dropped without forcing the active local turn/session into an explicit failure state.
3. Prompt assets accept unknown or missing item fields and synthesize an enabled expert prompt.
4. Codex resume silently keeps local approval settings when `thread/config/get` fails, returns malformed JSON, or omits required fields.
5. The frontend RPC contract audit reads a shadow method registry and can report green without checking the production method constants and all P0 response policies.

The license conflict and every P2/P3 item are explicitly out of scope.

## Baseline and isolation

- Base: `origin/main@e3d108327471b7e8acf21a5feb6fe556fdcaea65`.
- Branch: `codex/fix-audit-p1-20260711-v2`.
- Worktree: current feature worktree.
- The original checkout's tracked and untracked files are not read as implementation truth and are never modified, staged, or committed.
- Product changes remain uncommitted until the user reviews the complete diff and explicitly approves atomic commits.

## Design

### 1. Workflow mutation completion contract

The UI must not represent an elapsed client timer as a failed backend mutation unless it can actually cancel that mutation. Run creation will retain one idempotency key for the complete user intent. If transport completion is uncertain, the action remains in an explicit unknown/pending state and reconciles through the backend using that same key before another run can be started.

The change is limited to the workflow action/service boundary and its tests. It will not introduce generic retry infrastructure or alter unrelated page timeouts.

### 2. Codex terminal-event failure propagation

Codex event translation will keep rejecting malformed events, but rejection of a terminal event must become an explicit error path instead of warn-and-drop. The dispatcher/session boundary will receive a structured error carrying event type and available thread/turn identity, then complete or fail the affected local handle exactly once.

Raw payloads, local absolute paths, prompts, and secrets must not be added to logs. Non-terminal malformed events retain existing behavior unless the same invariant requires failure propagation.

### 3. Strict prompt item parsing

Prompt list items will use a strict Zod schema matching the canonical backend response. Required identifiers, booleans, numeric priority, scope, bucket/category, and prompt type must be validated before normalization. Invalid items fail the response instead of receiving generated IDs or permissive defaults. Valid optional presentation fields may retain explicit, documented presentation defaults only where the backend contract permits omission.

The change will not alter prompt creation, launch behavior, or the intentional read-only compatibility surface.

### 4. Fail-fast Codex resume configuration

`thread/config/get` becomes a required resume step whenever the resumed remote thread owns effective approval configuration. RPC failure, decode failure, missing `effective`, or missing approval fields must return a contextual error and stop resume before the session is exposed as ready.

No local-policy fallback, default approval policy, or warning-only continuation is allowed. The error must preserve enough operation context for diagnosis without logging sensitive configuration values.

### 5. RPC contract audit uses production truth

There will be one frontend `RPC_METHODS` source imported by the production factory, public facade, contract matrix, and audit. The audit must compute registry/method mismatches rather than initialize the result to an empty array.

Every P0/P1 registry entry must declare either a response validator or a non-empty, reviewed passthrough reason. The audit fails when a production method is absent from the matrix, a matrix method differs from production truth, a required backend handler is absent, or a P0/P1 response policy is missing.

The design does not generate Go DTO bindings or expand public RPC contracts.

## Test strategy

Each fix follows a separate red-green cycle:

- Workflow: deferred mutation exceeds the UI timing boundary, later succeeds, and a retry cannot create a second idempotency identity/run.
- Codex events: malformed terminal event makes the active handle fail exactly once; malformed non-terminal behavior remains bounded.
- Prompt schema: `{}`, string booleans, unknown enums, and missing IDs fail parsing; canonical items still render.
- Resume: RPC error, malformed response, and missing required configuration each fail resume; valid effective config succeeds.
- RPC audit: fixtures prove shadow-method drift and missing P0/P1 response policy make the audit fail.

After focused tests, verification includes LSP diagnostics for every changed file, affected Go guarded packages, frontend lint/test/build, RPC audit, `git diff --check`, owned-file review, secret/local-path scan, and generated-file inspection.

## Git and acceptance

No staging or commits occur before user approval of the complete uncommitted diff and fresh verification evidence. After approval, fixes are split into atomic commits by independent problem domain, with each regression test committed alongside its implementation. Only then may an isolated integration worktree refresh `origin/main`, verify local main can safely synchronize, merge without guessing conflicts, rerun full validation on the merged result, recheck the remote race, and perform a non-force push to `origin/main`.
