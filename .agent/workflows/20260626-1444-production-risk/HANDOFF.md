# Handoff

## Current Status
All 18 blank-context workers have returned. The controller reviewed each lane, reran lane-appropriate gates, and left every lane uncommitted in its isolated worktree.

No lane has been merged, staged, committed, pushed, or replayed onto a shared integration branch.

## How To Continue
1. Read `.agent/workflows/20260626-1444-production-risk/STATE.json`.
2. Read `.agent/workflows/20260626-1444-production-risk/CHECKS/EVIDENCE.md`.
3. Choose an integration order and merge/replay one lane at a time.
4. After each lane integration, rerun that lane's recorded verification command from the integration branch.
5. Run repository-level gates only after the selected integration set is assembled.

## Immediate Commands
```bash
git worktree list --porcelain
git status --short
jq '.overall_status, .lane_status, .controller_verified' .agent/workflows/20260626-1444-production-risk/STATE.json
sed -n '1,240p' .agent/workflows/20260626-1444-production-risk/CHECKS/EVIDENCE.md
```

## Controller Rules
- Do not merge a lane until its changed paths are compared against `FILE_OWNERSHIP.tsv`.
- Do not accept a lane without fresh RED/GREEN/guard/lane verification evidence.
- Do not approve write-set expansion without real file paths, reason, and risk if rejected.
- Do not treat `make sqlc-verify` failures in L02/L03/L09 as resolved until the approved generated diffs are integrated and the command is rerun clean.

## Top Risks
- Merge conflicts between lanes that touch `internal/provider/codexapp`.
- SQLC generated diffs in L02/L03/L09 must be rechecked after integration.
- Full repository verification has not been run on a combined branch.
- Main worktree has unrelated untracked files; keep staging narrow.
