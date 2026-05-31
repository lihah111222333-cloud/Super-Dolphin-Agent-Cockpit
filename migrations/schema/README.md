# Schema Migrations

This directory is reserved for schema-only migrations.

Use this directory for DDL that creates or changes the database shape:

- tables
- columns
- indexes
- constraints
- functions and triggers required by schema behavior

Do not put user data, local development rows, prompt drafts, memories, task runs, or thread history here.

Current status: runtime migration loading still scans top-level `migrations/*.sql`.
Do not move existing migration files into this directory until the migration runner, sqlc schema inputs, packaging scripts, and guards are updated together.
