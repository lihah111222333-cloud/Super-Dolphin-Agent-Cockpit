# Embedded PostgreSQL Packaging Is Retired

Super Dolphin no longer ships or starts an embedded PostgreSQL runtime. Product
startup uses the SQLite configuration path, and `DATABASE_URL` /
`POSTGRES_CONNECTION_STRING` are preserved only as legacy environment names for
redaction and explicit ignore-path tests.

Do not package PostgreSQL binaries, `pg_ctl`, `initdb`, `postgres.bki`, or an
`embedded_postgres_resource_path` runtime manifest entry for current releases.
Use the SQLite package smoke and release gate checks instead.
