# RecoveryCustody reconcile coordinator candidate

Status: Candidate only — `LOCAL_NON_AUTHORITATIVE`, not production-ready.

`cmd/mcp-orch/orchestration/recoverycustody/reconcilecoordinator` owns a separate, explicitly bootstrapped SQLite database. It is intentionally not the authority database, the product SQLite database, an MCP endpoint, or an Fx/public orchestration component.

The candidate provides:

- startup `ListActiveResolutionHandles` scan and idempotent enqueue;
- strict durable `reconcile_jobs`, append-only `reconcile_journal`, append-only `reconcile_audit`, and singleton owner-fence tables;
- one bounded `BEGIN IMMEDIATE` claim at a time, owner epoch/token fencing, lease expiry takeover, and successor reopen after takeover;
- bounded exponential retry scheduling; terminal `available|aborted` requires a non-nil receipt, while errors, pending, malformed, or indeterminate results remain retry/indeterminate and never become accepted or dispatch;
- synchronous `RunOnce` and an explicit-period `Run` loop with context cancellation.

`Bootstrap` must be called explicitly for a fresh coordinator path; `Open` refuses an absent, malformed, or owner-mismatched database. No migration or default path is supplied. `Takeover` fences the caller immediately; a successor must reopen with the higher epoch/token. Old claim completion is rejected by owner epoch and claim-token compare-and-swap.

This closes the local coordinator shape only. It does not prove an independently deployed service, external EvidenceFinalizer/anchor, receipt signing trust root, network-filesystem safety, supervisor/CLI lifecycle, production orch client/Fx wiring, or release acceptance. Until those external proofs exist, production composition must keep custody unavailable/pending/indeterminate and must not create dispatch attempts.
