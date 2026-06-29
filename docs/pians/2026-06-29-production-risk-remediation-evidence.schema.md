# Production Risk Remediation Evidence Schema

This file defines the ledger shape only. The controller writes final evidence rows after lane commits are known.

## Active Evidence

| ID | Lane | RED | GREEN | Commit | Residual Risk |
|---|---|---|---|---|---|
| P0-00 | lane-name | command and failing reason | command and exit code | commit sha | none or concrete concern |

## Adjusted Readiness Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P1-07 | readiness or diagnostic outcome | command, file, or commit reference |

## Guard-Only Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P1-32 | guard/test governance outcome | command, file, or commit reference |

## Evidence-Only Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P3-07 | evidence index outcome | validator command or controller ledger reference |
