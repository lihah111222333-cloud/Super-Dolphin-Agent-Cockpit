# DAG Task 10 - Integration Merge Checklist

Worker: W5 (`work/obs-tail-verify`)

Depends on: 09

Purpose: prepare the integration branch for final review and merge after all worktree branches are accepted.

Files:

- No code edits expected.
- May add a short merge summary under `docs/cc/obs/` if needed.

Checklist:

1. Confirm each worker branch has been reviewed by two review agents.
2. Confirm each approved worker branch was committed in its worktree.
3. Merge approved worker branches into `integration/obs-tail-cache-freshness`.
4. Run Task 09 verification.
5. Check:

```bash
git status --short
git log --oneline --decorate -n 12
```

Expected:

- Integration branch contains only OBS-F01-related code/tests/docs.
- No unrelated untracked files are staged or committed.
- Verification evidence is fresh.

Constraints:

- Do not use `git add .`.
- Do not commit the two unrelated root-level Chinese `.txt` files.
