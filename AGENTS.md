## Project Map Policy

- The local Go project map is generated under `.project-map/` and must not be committed.
- Before searching for a Go function, method, struct, interface, package, import, or entry point, query the project map first when it exists:
    - `python3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . <Name>`
- If `.project-map/project-map.json` is missing or stale, run `make project-map` before broad repository searches.
- The map is only an index; open the referenced source file before changing code or making claims.

## Go Project Guard Policy

- All Git commit messages must use Chinese for both subject and detail/body.
- Commit subject must be a Chinese subject and must not start with English Conventional Commit prefixes such as `feat:`, `fix:`, `docs:`, or `chore:`.
- Commit detail/body is required and must describe the change and reason in Chinese.
- Example: `git commit -m '守卫：强制提交信息使用中文' -m '新增提交信息校验，确保主题和详情均为中文。'`
- For any task that writes, edits, refactors, reviews, or tests Go code, use the `writing-go-code` skill.
- For any task that creates, edits, deletes, reviews, commits, merges, or releases Go code or Go project configuration, use the `guarding-go-projects` skill.
- For any task that designs or changes package layout, bounded contexts, dependency direction, or architecture boundaries, also use the `designing-go-architecture` skill.
- For Go test strategy, unit tests, integration tests, contract tests, fuzzing, benchmarks, race tests, or coverage policy, use the `testing-go-projects` skill.
- For REST, OpenAPI, gRPC, protobuf, AsyncAPI, webhooks, DTOs, pagination, idempotency, error codes, or compatibility work, use the `designing-api-contracts` skill.
- For dependencies, GitHub Actions security, vulnerability scans, SBOMs, SLSA provenance, artifact signing, container image security, or supply-chain controls, use the `securing-go-supply-chain` skill.
- For configuration loading, environment variables, `.env.example`, secret injection, redaction, config precedence, credential rotation, or config validation, use the `managing-go-configuration` skill.
- For logging, metrics, traces, health checks, readiness, configuration, graceful shutdown, workers, runbooks, alerts, or OpenTelemetry, use the `operating-go-services` skill.
- For versioning, changelogs, release notes, tags, artifacts, Conventional Commits, Semantic Versioning, or release readiness, use the `releasing-go-projects` skill.
- For SQL migrations, database schemas, indexes, constraints, transactions, backfills, rollback strategy, retention, audit fields, or persistence model rules, use the `managing-data-migrations` skill.
- After every Go code edit, run `make guard-change` before claiming the change is complete.
- Before committing, handoff, or pull request creation, run `make guard-commit`.
- Before merging, release, or deployment readiness claims, run `make guard-release`.
- If a guard fails, fix the failure or report the exact failing command and reason. Do not weaken or bypass a guard to finish a task.
- If the repository has no `go.mod`, guard commands may skip Go checks; report the skip explicitly.
