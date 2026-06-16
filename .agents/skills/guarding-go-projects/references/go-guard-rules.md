# Go Guard Rules

## Purpose

Project guards are executable evidence. They are stronger than code review comments or architectural intent because they fail when the project drifts.

## Level 1: Change Guard

Run after every Go code edit:

```bash
make guard-change
```

Required checks:

- Secret scan passes.
- Configuration and secret-file policy passes.
- GitHub Actions workflows use minimum permissions and stable action refs.
- Go files are formatted by `gofmt`.
- Go source files stay within size budgets.
- Required package/exported identifier/long function comments exist.
- `go test ./...` passes.
- Import-level architecture boundary checks pass.
- AST architecture checks pass.
- `golangci-lint run` enforces complexity and quality budgets when installed.
- `staticcheck ./...` is a local fallback only when `golangci-lint` is unavailable and strict mode is off.

Completion rule: no successful guard output means no completion claim.

## Level 2: Commit Guard

Run before commit, handoff, or PR:

```bash
make guard-commit
```

Required checks:

- All `change` checks pass.
- `go mod tidy` does not leave unexpected `go.mod` or `go.sum` changes.
- `go mod verify` passes.
- The local `.project-map/` AI navigation index is regenerated and remains untracked.
- Generated artifacts are reproducible when `GO_GUARD_GENERATE_CMD` or a `make generate` target exists.
- SQL migrations follow naming, reversibility, and destructive-DDL rules.
- `go vet ./...` passes.
- `go build ./cmd/...` succeeds when command packages exist.
- Git commit messages pass the Chinese commit-message policy.

Set `GO_GUARD_REQUIRE_GOLANGCI=1` in CI to make missing `golangci-lint` fail.

## Commit Message Policy

`.githooks/commit-msg` and CI range checks enforce:

- Commit subject must contain Chinese.
- Commit detail/body is required and must contain Chinese.
- Commit subject must not start with English Conventional Commit prefixes such as `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, `build:`, or `perf:`.

Valid example:

```bash
git commit -m '守卫：强制提交信息使用中文' -m '新增 commit-msg 校验，确保主题和详情均为中文。'
```

Invalid examples:

```bash
git commit -m 'chore: update guard'
git commit -m '更新守卫'
```

## Level 3: Release Guard

Run before merge, release, or deployment:

```bash
make guard-release
```

Required checks:

- All `commit` checks pass.
- `go test -race ./...` passes.
- `govulncheck ./...` runs when installed.
- `gosec ./...` runs when installed.
- Coverage is enforced when `GO_GUARD_COVERAGE_MIN` is set.

CI sets `GO_GUARD_REQUIRE_GOLANGCI=1` and `GO_GUARD_STRICT_TOOLS=1`, so release guards require `golangci-lint`, `govulncheck`, and `gosec`.

## Complexity And Size Budgets

`.golangci.yml` enforces:

- Cyclomatic complexity: `gocyclo` minimum 15 and `cyclop` maximum 12.
- Cognitive complexity: `gocognit` minimum 18.
- Function length: `funlen` maximum 80 lines or 50 statements.
- Duplicate code: `dupl` threshold 100.
- Long lines: `lll` maximum 120 columns.
- Naked returns: maximum 20 function lines.

`scripts/check_go_size.py` enforces:

- `GO_GUARD_MAX_FILE_LINES`, default `400`.
- `GO_GUARD_MAX_TEST_FILE_LINES`, default `700`.
- `GO_GUARD_MAX_PACKAGE_GO_FILES`, default `30`.

Set a limit to `0` only for a narrow, documented exception.

## Comment Budgets

`scripts/check_go_comments.py` enforces:

- Every non-test package has a package comment, preferably in `doc.go`.
- Every exported top-level type, function, method, const, and var has a doc comment starting with the identifier.
- Long functions, default 40 lines via `GO_GUARD_COMMENT_LONG_FUNC_LINES`, have an intent comment.

Use `guard:allow-missing-comment` only for documented false positives.

## Supply Chain And Secret Budgets

The guard rejects:

- High-risk token formats such as GitHub, OpenAI, Anthropic, Slack, AWS, JWT, private keys, URL credentials, and suspicious credential assignments.
- Tracked or staged local env files such as `.env`, `.env.local`, and `.env.production`.
- Sensitive config keys with literal non-placeholder values in config files.
- Repositories with config files but no `.env.example`.
- Missing `.gitignore` rules for `.env`, `.env.*`, and `.env.example` exceptions.
- Missing GitHub Actions top-level `permissions: contents: read`.
- Workflow actions pinned to moving refs such as `main`, `master`, or `latest`.
- Failed `go mod verify`.

Use `guard:allow-secret` only for documented false positives.
Use `guard:allow-config-secret` only for documented config false positives.

## Dependency Boundary Budgets

`scripts/check_go_boundaries.py` rejects:

- `domain` importing project internal packages, transport APIs, database APIs, logging/config packages, ORMs, Redis clients, or HTTP frameworks.
- `app` importing adapters, bootstrap, platform, command packages, HTTP frameworks, database helpers, cache clients, or config packages.
- `app/port` exposing adapter/framework/database/cache types.
- `adapter` importing `cmd`, `bootstrap`, or a different adapter such as `adapter/http -> adapter/postgres`.
- `platform` importing business packages.
- `cmd` importing business packages directly instead of `bootstrap`.
- Cross-context adapter imports.
- Broad package names under `internal/` or `pkg/`: `common`, `utils`, `shared`, `models`, `types`, `helpers`.

`scripts/check_go_ast_rules.py` rejects:

- `domain` calling `time.Now`, environment variables, randomness, direct logging, or stdout/stderr output.
- `app` opening databases, calling HTTP directly, opening ORM connections, or reading environment variables.
- Direct `fmt.Print*`, `log.Print*`, `log.Fatal*`, direct `slog.*`, `panic`, or `os.Exit` outside command/bootstrap/platform logging/test code.
- Error anti-patterns such as `errors.New(fmt.Sprintf(...))`, `fmt.Errorf` with `%v` instead of `%w`, swallowing `err` with `return nil`, and logging then returning the same error.
- Repository adapters assembling services, use cases, or handlers.

## Project Map

`scripts/go_project_guard.sh commit` regenerates `.project-map/` through `mapping-go-projects/scripts/generate_go_project_map.go`.

Rules:

- `.project-map/` must be ignored by git.
- `.project-map/` must not be staged or tracked.
- The map is generated during commit and release guards.
- The map is a navigation index only; it does not replace package docs, architecture docs, tests, or compiler checks.

Generated files:

- `.project-map/PROJECT_MAP.md`
- `.project-map/project-map.json`
- `.project-map/symbols.tsv`
- `.project-map/packages.tsv`
- `.project-map/imports.tsv`

## Generated Artifact Drift

`scripts/check_generated_drift.sh` runs when either condition is true:

- `GO_GUARD_GENERATE_CMD` is set.
- The repository `Makefile` defines a `generate` target.

The guard fails if the generator changes the git worktree status. This catches stale OpenAPI, protobuf, SQLC, mock, and other generated files.

## Migration Budgets

`scripts/check_migrations.py` checks `migrations/`, `migration/`, `db/migrations/`, and `database/migrations/`.

Allowed SQL migration styles:

- Paired files: `NNN_name.up.sql` and `NNN_name.down.sql`.
- Goose-style files: `NNN_name.sql` with `-- +goose Up` and `-- +goose Down`.

Destructive DDL such as `DROP TABLE`, `DROP COLUMN`, `DROP DATABASE`, `DROP SCHEMA`, `DROP INDEX`, and `TRUNCATE TABLE` requires `guard:allow-destructive-migration` and review.

## Exceptions

- Exceptions must be explicit and narrow.
- Document architecture exceptions in `docs/architecture/dependency-rules.md`.
- Document temporary tool skips in the task result.
- Prefer installing missing tools in CI over making checks permanently optional.

## Recommended Tooling

- Formatting: `gofmt`; optionally `gofumpt` if the project standardizes on it.
- Tests: `go test ./...`, `go test -race ./...`.
- Static checks: `go vet`, `staticcheck`, or `golangci-lint`.
- Security: `govulncheck`, `gosec`.
- Secrets: `scripts/check_secrets.py`.
- GitHub Actions: `scripts/check_github_actions.py`.
- Configuration policy: `scripts/check_config_policy.py`.
- Source size: `scripts/check_go_size.py`.
- Comments: `scripts/check_go_comments.py`.
- Generated drift: `scripts/check_generated_drift.sh`.
- Migrations: `scripts/check_migrations.py`.
- Boundaries: `scripts/check_go_boundaries.py`, `scripts/check_go_ast_rules.py`.
