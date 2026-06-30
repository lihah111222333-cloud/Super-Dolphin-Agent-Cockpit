# Risk Register

## R1: Stage Source Is Missing

- Severity: high.
- Risk: Lane A could invent a planning/execution stage that no runtime owner actually supplies.
- Control: A0 must prove the stage source with LSP references and call hierarchy before A2 wires runtime blocking.
- Stop condition: if no stage source exists, A2 is closed as `not_applicable_with_evidence`, runtime blocking remains absent, and only A1 toolpolicy tests/package may land.

## R2: ReadOnly Is Treated As PlanSafe

- Severity: high.
- Risk: read-only but post-approval-only tools such as planning finalizers or workflow state mutators enter planning surfaces.
- Control: A1 tests must prove `PlanSafe => ReadOnly` and `ReadOnly != PlanSafe`.

## R3: External MCP Hints Become Trusted

- Severity: high.
- Risk: external or unknown MCP tools self-report read-only and bypass V3 owner policy.
- Control: external/untrusted hint must fail closed until V3 owner policy marks it trusted.

## R4: MCP Lifecycle Policy Is Bypassed

- Severity: high.
- Risk: plan policy allow path accidentally revives disabled, suspended, or removed tools.
- Control: A2 tests must prove lifecycle deny remains authoritative and schema validation still precedes handler calls.

## R5: Shell Policy Allows Process Control

- Severity: high.
- Risk: bash wrapper or shell syntax bypasses planning stage restrictions through background jobs, process control, or dangerous arguments.
- Control: `shellsafe` tests must cover background/process-control syntax and dangerous parameters.

## R6: Path Helper Changes Behavior

- Severity: medium.
- Risk: sessionpaths extraction changes rollout discovery, Codex home fallback, scratchpad slug, or cleanup semantics.
- Control: B1 golden tests must capture current behavior before B2 migrates callers.

## R7: Leaf Helper Gains Internal Dependencies

- Severity: medium.
- Risk: `sessionpaths` becomes a hidden data-owner or imports module/provider packages.
- Control: archtest must fail on internal repo imports from `internal/platform/sessionpaths`.

## R8: Writer Preview Spike Turns Into Runtime API

- Severity: medium.
- Risk: C1 designs a generic preview interface before inventory proves which writers can preview safely.
- Control: C1 output is ADR or plan amendment only. Production interface work needs separate approval.

## R9: Dirty Worktree Boundary Is Lost

- Severity: medium.
- Risk: unrelated `.githooks`, older plan, and guard test edits are staged with this workflow or later lane commits.
- Control: implementation lanes must use isolated worktrees; final staging must include only files owned by the active task.

## R10: Generated Or Historical Artifacts Drift

- Severity: low.
- Risk: codemap, archive, frontend dist, or generated embed files get touched during implementation.
- Control: mark generated/historical paths NO-TOUCH unless user explicitly expands scope.
