# Skill Progressive Disclosure Authenticated Claude CLI E2E Evidence

Evidence type: authenticated-claudecli-e2e
Status: P25-HIGH-02k evidence template. Attach one filled copy before Phase 3
provider default policy formalization or `overrideSkillsToSummary` deletion.

## Required fields

| Field | Required value |
|---|---|
| Evidence date | YYYY-MM-DD |
| Version / commit | short SHA or release tag |
| Operator | release owner / on-call |
| Authenticated environment | `true` |
| Command | `go test ./cmd/agent-terminal -run '^TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E$' -count=1` or equivalent authenticated run |
| Test name | `TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E` |
| Result | `PASS` |
| Skip status | must not contain `SKIP` |

## Filled evidence

Evidence date: TODO
Version / commit: TODO
Operator: TODO
Authenticated environment: false
Command: TODO
Test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E
Result: TODO
Skip status: TODO

## Raw E2E output

Use `skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh` to generate this file from complete authenticated Claude CLI test output whenever possible.

Paste the complete authenticated Claude CLI test output here only when manually filling reviewed evidence.

```text
TODO
```
