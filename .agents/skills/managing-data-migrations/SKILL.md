---
name: managing-data-migrations
description: Use when designing, writing, reviewing, testing, or operating database schemas, SQL migrations, indexes, constraints, transactions, backfills, rollbacks, data retention, audit fields, repository persistence models, or migration safety gates.
---

# Managing Data Migrations

## Overview

Treat schema and data migrations as production changes, not incidental files. A safe migration is reversible or forward-fixable, tested against realistic state, and compatible with the application version that runs before, during, and after deployment.

## Industry Baseline

Use these practices:

- Versioned migration files with deterministic order.
- Expand-and-contract for zero-downtime incompatible changes.
- Backward-compatible schema changes before application rollout.
- Explicit indexes, constraints, and transaction boundaries.
- Migration checks for destructive DDL.
- Data backfills designed for batching, retries, and observability.
- Repository adapters own persistence models; domain types do not become database rows by default.

## Migration Layout

Supported directories are checked by the project guard:

```text
migrations/
migration/
db/migrations/
database/migrations/
```

Preferred styles:

- Paired files: `NNN_name.up.sql` and `NNN_name.down.sql`.
- Goose-style files: `NNN_name.sql` with `-- +goose Up` and `-- +goose Down`.

## Required Rules

- Use stable, ordered migration names.
- Every schema change must explain rollback or forward-fix behavior.
- Avoid destructive DDL in the same deploy as code that still depends on old data.
- Add indexes for new query paths and verify cardinality assumptions.
- Use database constraints for invariants that must survive concurrent writers.
- Keep domain invariants in domain code; keep persistence mapping in repository adapters.
- Do not let handlers or app use cases hand-write SQL when a repository adapter owns persistence.
- Backfills must be resumable, idempotent, and bounded in batch size.
- Migrations must not depend on local developer data or secrets.
- Seed data must be clearly separated from schema migrations.

## Compatibility Pattern

Use expand-and-contract for risky changes:

1. Expand: add nullable column, table, index, or dual-write support.
2. Migrate: backfill data in bounded batches.
3. Switch: read from the new structure after verification.
4. Contract: remove old structure only after no running version depends on it.

## Review Checklist

- Does the migration lock large tables?
- Does it rewrite existing rows?
- Is there a rollback or forward-fix plan?
- Are indexes added before new query paths rely on them?
- Are constraints compatible with existing data?
- Does application code remain compatible during rolling deployment?
- Are audit fields, timestamps, and ownership fields handled consistently?

## Verification

Run:

```bash
make guard-change
make guard-commit
```

For real database changes, also run migrations up and down against a disposable database with representative data before release.

## Common Mistakes

- Dropping columns before all code stops reading them.
- Adding a NOT NULL column with no default or backfill plan.
- Creating indexes that block writes on large tables without an online strategy.
- Mixing seed data, schema migration, and data backfill in one opaque file.
- Treating ORM models as the architecture boundary instead of adapter details.
