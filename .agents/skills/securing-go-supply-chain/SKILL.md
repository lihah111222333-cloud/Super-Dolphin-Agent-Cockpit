---
name: securing-go-supply-chain
description: Use when adding, updating, reviewing, releasing, or auditing dependencies, GitHub Actions, build provenance, SBOMs, vulnerability scans, secret handling, dependency update automation, artifact signing, container images, or software supply-chain controls.
---

# Securing Go Supply Chain

## Overview

Treat source, dependencies, build tools, CI workflows, generated artifacts, and release artifacts as one supply chain. The goal is reproducible, reviewable, least-privilege delivery with vulnerability visibility and tamper evidence.

## Industry Baseline

Use these standards and tools where they fit:

- Go modules with `go mod verify` and minimal dependency surface.
- `govulncheck` for Go vulnerability reachability.
- `gosec` and `golangci-lint` security linters.
- OpenSSF Scorecard for repository security posture.
- SLSA provenance for build integrity.
- SPDX or CycloneDX SBOMs for dependency inventory.
- Sigstore/cosign for artifact or container image signing.
- Dependabot or Renovate for dependency update automation.
- GitHub Actions least privilege, pinned actions, and protected branches.

## Required Rules

- Prefer standard library and small, maintained dependencies.
- Add a dependency only when it removes real risk or complexity.
- Pin tool versions in CI unless a deliberate update policy exists.
- Do not commit secrets, private keys, tokens, local `.env`, or generated credential material.
- GitHub Actions must use minimum permissions and stable action refs.
- Public release artifacts should have checksum files and, when configured, signatures.
- Dependencies with known vulnerabilities require upgrade, mitigation, or documented acceptance.
- Generated files must be reproducible and checked for drift.
- Container images, when added, must use minimal bases, non-root users, and vulnerability scanning.

## Review Checklist

Before adding or upgrading a dependency:

- Is the package maintained and recently released?
- Is the license acceptable for the project?
- Does it introduce transitive dependencies that exceed the need?
- Is it used in a security-sensitive path?
- Can the standard library or existing project package do the job?
- Are tests covering the integration and failure modes?

Before release:

- `go mod verify` passes.
- `govulncheck ./...` passes or findings are documented.
- `gosec ./...` passes or findings are documented.
- SBOM and provenance are generated when release automation exists.
- CI uses least-privilege permissions.

## Verification

Local guard:

```bash
make guard-release
```

Recommended release additions once artifacts exist:

```bash
govulncheck ./...
gosec ./...
```

Use OpenSSF Scorecard, SBOM generation, and signing in CI once the repository has release artifacts.

## Common Mistakes

- Treating dependency updates as harmless because tests pass.
- Letting GitHub Actions run with broad default write permissions.
- Installing CI tools with floating versions in critical release workflows.
- Producing release binaries without checksums or provenance.
- Hiding vulnerability findings instead of documenting mitigation.
