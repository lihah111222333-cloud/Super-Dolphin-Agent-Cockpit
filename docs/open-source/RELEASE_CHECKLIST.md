# Open-Source Release Checklist

This checklist separates preparation from publication. Completing preparation does not authorize a push, visibility change, tag, or GitHub Release. Publication always requires an explicit decision from the repository owner.

## 1. Freeze the Candidate Input

- [ ] Select one committed source SHA.
- [ ] Confirm the source worktree and index are clean.
- [ ] Confirm product name, canonical repository, Go module, and `Apache-2.0` identity agree.
- [ ] Confirm all required root and community files exist.
- [ ] Confirm no private development plan, archive, local workspace, provider home, database, log, or credential is intended for publication.

## 2. Verify Documentation and Generated Truth

- [ ] Check all six README language links and stable section markers.
- [ ] Check every relative link in README and `docs/open-source`.
- [ ] Confirm planned work is not described as implemented.
- [ ] Run `make codemap-check`.
- [ ] Run `make project-map-check`.
- [ ] Run `make capcontract-check`.
- [ ] Run `git diff --check`.

If an owning source intentionally changed, run the corresponding explicit refresh target, inspect the generated diff, and rerun the check. Do not hand-edit generated facts.

## 3. Verify the Repository

- [ ] Run `make guard`.
- [ ] Run focused guarded Go tests for every changed package.
- [ ] Run `make test` before claiming a complete Go verification.
- [ ] Run `make sqlc-verify` when migrations, SQL, or store code changed.
- [ ] Run `cd frontend-app && npm run lint && npm test && npm run build` when frontend or shared release behavior changed.
- [ ] Record command, exit status, source SHA, and any retained blocker.

## 4. Build the Public-Source Candidate

Publication remains blocked until an end-to-end source-export command exists and proves all of the following:

- [ ] It reads a committed Git tree rather than recursively copying the worktree.
- [ ] Unclassified paths fail closed.
- [ ] Denied paths, symlinks, submodules, case collisions, and forbidden identities fail closed.
- [ ] Input-only deny rules and candidate-required generated files have explicit, tested phase semantics; no generated file is both required and unconditionally denied in the same phase.
- [ ] Forbidden-identity checks reject real machine-specific or legacy identity leaks without rejecting documented synthetic test fixtures, and the policy does not trigger on its own rule values.
- [ ] Public-profile generated maps are rebuilt inside the candidate.
- [ ] The public CI workflow exists in the candidate and is required by the candidate validator, not merely listed as an allowed path.
- [ ] A deterministic receipt records every file, mode, size, and SHA-256 digest.
- [ ] Independent verification rejects a missing, extra, modified, or chmod-changed file.
- [ ] A redacted secret scan passes on the candidate tree.

Do not replace this step with a manual copy, archive command, blacklist-only filter, or ignored warning.

## 5. Human Review Gate

- [ ] Review the complete candidate file list.
- [ ] Review `LICENSE`, `NOTICE`, third-party licenses, and dependency attribution.
- [ ] Review security reporting instructions and confirm no secret contact is fabricated.
- [ ] Review known limitations and roadmap wording.
- [ ] Confirm the candidate contains no absolute local paths, private issue data, user memory, traces, or credentials.
- [ ] Record an explicit owner decision: `approved for publication` or `blocked` with reasons.

Without this decision, stop here.

## 6. Publication Actions

These actions require separate explicit authorization:

- [ ] Create or rename the canonical public repository.
- [ ] Push only the reviewed public candidate and its intended public history.
- [ ] Enable GitHub Private Vulnerability Reporting.
- [ ] Configure branch protection and required public checks.
- [ ] Verify repository description, topics, default branch, license detection, and community profile.
- [ ] Clone the public repository into a clean directory and rerun its documented setup and governance checks.

Do not create a tag or GitHub Release until versioning, artifacts, checksums, and platform verification have their own reviewed evidence.

## 7. After Publication

- [ ] Verify every README language link on GitHub.
- [ ] Verify issue forms, pull-request template, security reporting, and license rendering.
- [ ] Record the public commit SHA and publication evidence.
- [ ] Create issues for known non-blocking limitations rather than silently omitting them.
- [ ] Update the changelog only with facts that were actually published.

## Stop Conditions

Stop the release when any of the following is true:

- a required check is red or was skipped;
- the source SHA or candidate changed after review;
- a secret or private path is suspected;
- the public-source receipt cannot be reproduced;
- a document claims an unavailable command, platform, release, or support channel;
- repository identity or license facts disagree;
- publication authority is absent or ambiguous.
