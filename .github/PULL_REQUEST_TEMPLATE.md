# Pull Request

## Summary

<!-- Explain the outcome of this change in a few concrete sentences. -->

## Problem and Scope

<!-- Describe the demonstrated problem, the intended behavior, and the files or surfaces intentionally changed. -->

## Reproduction or Acceptance Evidence

<!-- For a fix, show the failing behavior before the change and the passing behavior after it. For other changes, provide reproducible acceptance evidence. -->

## Verification

<!-- List every command run, its result, and any check that was skipped or blocked. Preserve relevant failure evidence even when a later rerun passes. -->

## Risk and Rollback

<!-- Describe compatibility, security, privacy, data, concurrency, provider, UI, and rollback risk that applies to this change. -->

## Governance Impact

<!-- Explain changes to contracts, architecture boundaries, guards, baselines, generated maps, skills, workflows, or release surfaces. -->

## Checklist

- [ ] This pull request contains one focused logical change.
- [ ] Every commit title contains Chinese text; every non-empty commit body also contains Chinese text.
- [ ] Fix-classified commits include a bug-locking test, fixture, golden file, or snapshot in the same commit.
- [ ] I installed and ran the repository hooks without using `--no-verify` to bypass a failure.
- [ ] I ran the checks required for each changed surface and recorded exact results above.
- [ ] Generated artifacts were refreshed through their owning generator, and intentional baseline changes are explained.
- [ ] Documentation and public behavior match the implementation and tests.
- [ ] The change contains no credentials, user data, private traces, unredacted logs, provider homes, local databases, or machine-specific paths.
- [ ] Security vulnerabilities were reported privately under `SECURITY.md`, not disclosed in this pull request.
