# Result Gates

## Gate 0: Dispatch Readiness
- [x] DAG initialized.
- [x] Each lane has a task card.
- [x] Each lane has verification command.
- [x] File ownership matrix initialized.
- [x] Risks and rollback notes initialized.

## Gate 1: Lane Completion
For every lane:
- [x] Only authorized RW files changed.
- [x] RED evidence recorded.
- [x] GREEN evidence recorded.
- [x] Per-changed-Go-file guard evidence recorded.
- [x] Lane verification command passed.
- [x] Residual risk stated.

## Gate 2: Integration
- [ ] Merge order followed: L08, L03, L02, L04, L07, L10, L18, L17, then remaining MED lanes.
- [ ] Each lane verification rerun after merge.
- [ ] `make sqlc-verify` passed.
- [ ] `make guard` passed.
- [ ] `make test` passed.
- [ ] `make build-plain` passed.
- [ ] `cd frontend-app && npm run lint && npm test && npm run build` passed.

## Gate 3: Handoff
- [x] `STATE.json` updated with worker ids and lane statuses.
- [x] `CHECKS/EVIDENCE.md` contains commands and exit codes.
- [x] Blockers have owners and next steps.
- [x] Handoff has continuation commands.
