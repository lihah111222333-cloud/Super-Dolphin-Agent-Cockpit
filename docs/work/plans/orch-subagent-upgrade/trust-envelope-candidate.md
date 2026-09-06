# Trust envelope candidate

Status: Candidate-only — `LOCAL_NON_AUTHORITATIVE`; production `NO-GO`.

## Scope

`cmd/mcp-orch/orchestration/trustcandidate` is a transport-neutral, stdlib-only
candidate seam. It exercises immutable `TrustEnvelope` values, bounded
in-memory `ContentTicket` copies, strict canonical JSON, quoted-output framing,
and zero-effect sink decisions. Provider/peer/A2A output and MCP/A2A metadata
remain `untrusted_external_data` plus `quoted_data`; control instructions are
rejected. Links and images remain inert and are never fetched automatically.
The canonical-event and sink values produced here are DTO/decision candidates;
they are not durable events or executed effects.

This package is intentionally unregistered and unwired. It has no real trust
root, controller signature verifier, or anti-rollback authority; no CONTENT
key/object backend or durable content ticket; no canonical `EventStore` writer;
no provider/CLI/runtime bridge; no UI renderer or UI sink; and no network,
file, navigation, tool, or A2A execution. It also does not own OUT, an event
bus, Fx composition, a public tool registry, grants, or effect receipts.
The architecture guard keeps production imports stdlib-only and checks that
production code does not import this package.

## Local test evidence

The focused package tests cover only the candidate boundary:

- `TestProviderOutputRoundTripAndQuote` preserves the quoted taint and keeps
  plaintext out of the canonical event representation.
- `TestUnknownUnverifiedMixedAndControlFailClosed` rejects unverified,
  unknown/mixed, and control tuples.
- `TestStrictJSONRejectsBoundaries` and
  `TestTicketAndCanonicalEventRejectMismatchOrOversize` cover strict JSON,
  size, digest, and ticket-binding failures.
- `TestMetadataLinkImageAndSinkRemainInert` and
  `TestProviderMetadataCannotAuthorizeControlOrTool` show inert metadata/link
  handling and zero-effect sink decisions.
- `TestJSONFieldGuardEnumeratesProducerFields` and
  `TestEnvelopeDigestFieldGuard` cover field/tag and digest coverage.
- `TestProductionImportsRemainStdlibOnly` and
  `TestCandidateIsNotWiredIntoProduction` guard the isolation boundary.

Run from the repository root:

```text
go test ./cmd/mcp-orch/orchestration/trustcandidate
```

These tests prove local construction and fail-closed behavior only. They do
not prove a trust root, CONTENT custody, EventStore durability, provider
execution, UI/network safety, cross-process recovery, signed receipts, or
release acceptance.

## Production decision

Production remains `NO-GO`. Enabling would require separately owned and
reviewed trust-root/signature and anti-rollback evidence, CONTENT key/object
and custody integration, the canonical EventStore append path, provider/UI/
network adapters with explicit grants and receipts, runtime/Fx wiring, and
crash/replay/unknown-outcome plus release-gate evidence. Until those proofs
exist, this candidate may only construct local values and must not be treated
as authorization, custody, or an enabled trust policy.
