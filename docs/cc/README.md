# CC Working Docs

This directory now keeps current coordination documents that are still tied to
scripts, CI, or active release gates.

## Current Content

- `数据库切换/`: SQLite switch and release-gate documentation. Some files in
  this subtree are referenced by `.github/workflows/sqlite-release-gates.yml`
  and `scripts/sqlite_release_gates*`, so do not move it without updating those
  callers and tests.

Historical CC review reports, task packs, and evidence that are no longer part
of the default reading path live under `docs/archive/reviews/cc/`.
