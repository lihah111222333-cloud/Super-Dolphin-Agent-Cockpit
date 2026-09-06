# Runtime Event Producer Candidate

Status: Local candidate only — production disabled

`taskeventsource.RuntimeEventCandidate` is a deliberately unregistered adapter
for proving the minimum mapping from a lifecycle observation to the canonical
event owner:

```text
active identity snapshot (one identity-owner transaction)
  -> task/{agent_id} + incarnation source epoch
  -> durable source/lane NextSequence
  -> canonical EventEnvelope + SHA-256 envelope hash
  -> taskeventsource.Producer.Append
  -> EventStore.Append (the sole event writer)
```

The adapter never accepts caller identity, source sequence, cursor, or envelope
hash. It reads all three identity rows through `identity.Store.GetActiveSnapshot`
and obtains the sequence from the existing EventStore source watermark. A
missing watermark is an error; sequence one is not guessed or hand-written.

This is not a production lifecycle integration. Identity snapshot and event
append are separate database transactions, so a concurrent binding revoke or
incarnation rollover can occur between them. The candidate therefore requires
an explicit `enabled` test flag, is not registered by Fx or any public tool
registry, and must remain disabled until an owner can atomically fence identity,
sequence allocation, and event append in one transaction. It does not touch
legacy/standalone terminal projections, direct-peer outboxes, provider
callbacks, or task wait/send tools.

The focused tests cover disabled-by-default behavior, complete snapshot/source
mapping, deterministic lowercase SHA-256 envelope hash construction, missing
durable sequence rejection, and nil dependency fail-fast behavior.

## Transaction-bound candidate

`taskeventsource.RuntimeEventTxCandidate` is a separate, stricter candidate for
the next ID-2 slice. Its transaction owner passes one transaction-bound `DBTX`
to the identity snapshot reader, source watermark reader and canonical appender;
the candidate rejects bare `*sql.DB`/`*sql.Conn`, source gaps and malformed watermarks,
and permanently fences itself after an unknown commit so a caller cannot replay
the callback. It still does not allocate a sequence, persist a custody receipt,
or enter Fx/provider/runtime wiring. The original split-transaction candidate
remains disabled and is not silently replaced by this seam.

## 2026-09-04 bound terminal ingress candidate

`runtimeeventwriter.Writer.AppendBound` now requires a transaction-bound
`BindingVerifierTx`; it re-reads the complete provider binding and appends the event under the same
owner transaction, fencing the writer after an unknown commit. `ValidateMaterializedResult` checks
receipt status, event/frame identity, canonical cursor and source watermark before a result may be
passed to a bridge.

`terminaleventbridge.Bridge` wraps only an ingress implementing the atomic bound seam and a durable
transaction-bound verifier. It rejects context-only verifiers, non-atomic ingress, malformed or late
generation observations, source gaps and non-materialized results; no provider callback, notification
or projection is hidden behind it. The bridge and writer remain `Active` candidate code,
`LOCAL_NON_AUTHORITATIVE`/`NOT_VERIFIED`, unregistered by Fx/provider/public registry. A real
provider session-incarnation receipt, canonical runtime caller, cross-process recovery and production
terminal ingress are still `NO-GO`.
