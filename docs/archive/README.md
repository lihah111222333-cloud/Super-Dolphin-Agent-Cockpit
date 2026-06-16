# Documentation Archive

This directory stores historical material that should not be part of the
default agent reading path.

## Rules

- Archived files are retained for provenance, not as current implementation
  truth.
- Prefer `git mv` when moving files here so history remains easy to inspect.
- Do not edit archived reports to make them match current code. Add a new
  current document elsewhere when the project needs a fresh source of truth.
- Do not scan this directory recursively unless the task asks for historical
  reports, old agent notes, migration evidence, or provenance.

## Layout

- `root-agent-notes/`: notes that previously lived at repository root.
- `reports/`: root-level review reports and similar historical outputs.
- `reviews/`: historical review reports and review evidence.
- `lsp-investigations/`: old LSP investigation rounds and reproductions.
- `generated-analysis/`: generated repository analysis snapshots.
- `research/`: raw research notes and source-material captures.
- `evidence/`: raw logs and other evidence that are useful only when tracing
  past verification.
