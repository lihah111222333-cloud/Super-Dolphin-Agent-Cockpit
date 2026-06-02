# A03: Dev Entrypoints DB DSN

**Goal:** `run-debug.sh`、`run-debug.ps1`、Makefile 显式进入 dev mode，并把 preflight 使用的同一 dev DSN 传给后端。

**Files:**
- Modify: `run-debug.sh`
- Modify: `run-debug.ps1`
- Modify: `Makefile`
- Test: `internal/platform/config/*_test.go`
- Test: script guard tests if present under `scripts/`

**Steps:**
- [ ] Write red script guard test: when `DATABASE_URL` is unset, `run-debug.sh`, `run-debug.ps1`, and `make run-agent-terminal-debug` all derive the same dev DSN for preflight and backend env.
- [ ] Write red script guard test: `make run-agent-terminal-debug` exports explicit dev runtime mode.
- [ ] Write red script guard test: PowerShell path preserves explicit user `DATABASE_URL` and exports explicit dev runtime mode.
- [ ] Write red test: explicit dev mode is accepted only from trusted dev entrypoints (`run-debug.sh`, `run-debug.ps1`, `make run-agent-terminal-debug*`), not from arbitrary ambient env that could downgrade packaged launcher/root.
- [ ] Update shell and PowerShell paths symmetrically.
- [ ] Ensure explicit user `DATABASE_URL` wins over default dev DSN.
- [ ] Error message names the DSN and how to set `DATABASE_URL` or start local PostgreSQL.

**Validation:**
```bash
bash -n run-debug.sh
pwsh -NoProfile -Command '$errors=$null; [System.Management.Automation.PSParser]::Tokenize((Get-Content -Raw run-debug.ps1), [ref]$errors) > $null; if ($errors) { $errors; exit 1 }'
make -n run-agent-terminal-debug
./scripts/test_with_guard.sh ./internal/platform/config ./scripts -run 'Test.*(RunDebug|Dev.*DSN|Make.*Debug|PowerShell)' -count=1
```
