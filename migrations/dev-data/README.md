# Development Data Seeds

This directory is reserved for optional development data snapshots.

Use this directory for sanitized data that helps teammates reproduce the same local product state:

- sample threads
- sample prompts
- sample memories
- sample DAGs and runs
- shared files metadata used for development demos

Rules:

- Never store API keys, OAuth tokens, provider credentials, personal secrets, or private customer data.
- Prefer deterministic IDs and `ON CONFLICT` upserts so imports are repeatable.
- Keep local machine paths portable, or document required path rewrites.
- Do not apply these files in the zero-data user-experience database.

Current status: runtime migration loading still scans top-level `migrations/*.sql`.
Files in this directory are intentionally opt-in until a dedicated seed/import command is implemented.
