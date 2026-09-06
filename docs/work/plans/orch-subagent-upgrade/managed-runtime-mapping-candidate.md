# Managed lifecycle mapping candidate

Status: Candidate / isolated / not wired to production

`cmd/mcp-orch/orchestration/managedlaunch` owns the command/attempt lifecycle contract;
`cmd/mcp-orch/standalone/managedruntime` owns the future process-supervisor contract.
The sidecar dependency boundary does not allow the orchestration package to import the
sibling standalone package, so this candidate deliberately provides projection validators
instead of wiring a backend or runtime client.

`ValidatePrepareProjection`, `ValidateCommitProjection`, `ValidateAbortProjection`, and
`ValidateQueryProjection` enforce the one-way mapping. Operation IDs and source binding digests
are carried explicitly from the managedlaunch operation; the runtime request/operation digest
remains a separate domain and is only accepted as an explicit, well-formed value until the
managedruntime owner verifies its own canonical digest. COMMIT/ABORT now also require the target
`PreparedRequestDigest` to equal the source operation's digest exactly; a merely well-formed but
foreign digest is rejected. Prepared receipt references, executable/argv/environment digests,
and runtime-writer epoch must be supplied explicitly by the verified owner. The source bootstrap
fence fields are validated by managedlaunch but are not silently reinterpreted as a runtime
writer epoch. Missing values, mismatched prepared receipts, and unknown-to-mutation conversions
fail closed.

The projections contain only bounded references and digests. They do not carry provider IDs,
provider payloads, credential material, process handles, or raw launch arguments. The candidate
does not start processes, connect providers, use Fx/Execution, or perform durable writes. A future
adapter may replace these projections with managedruntime DTOs only after the package boundary and
the independent supervisor prerequisites are explicitly changed and verified.
