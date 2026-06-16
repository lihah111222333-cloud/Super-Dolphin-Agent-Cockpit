# Go Architecture Rules

## Package Responsibilities

| Package | Owns | Must not own |
| --- | --- | --- |
| `cmd/<app>` | process entry point, signal handling, final run call | business rules, repositories, migrations |
| `internal/bootstrap` | dependency assembly and wiring | domain decisions, request parsing, SQL |
| `internal/platform` | technical primitives: config, logger, DB, transaction runner, metrics | business workflows, bounded-context state |
| `internal/<context>/domain` | entities, value objects, invariants, pure domain services, domain errors | HTTP, SQL, config, logging, transactions |
| `internal/<context>/app` | use cases, command/query handlers, orchestration, transaction boundaries | protocol parsing, database row mapping |
| `internal/<context>/app/port` | interfaces consumed by app use cases | implementations, framework-specific types |
| `internal/<context>/adapter/http` | HTTP handlers, request validation, response mapping | business decisions, SQL |
| `internal/<context>/adapter/postgres` | SQL persistence, row mapping, repository implementation | use-case orchestration, HTTP concerns |
| `internal/<context>/adapter/event` | message mapping, consumers, producers | core business decisions |

## Allowed Import Direction

```text
cmd/<app>                         -> internal/bootstrap
internal/bootstrap                -> internal/platform, internal/<context>/app, internal/<context>/adapter/*
internal/<context>/adapter/*      -> internal/<context>/app, internal/<context>/app/port, internal/<context>/domain, internal/platform
internal/<context>/app            -> internal/<context>/domain, internal/<context>/app/port
internal/<context>/app/port       -> internal/<context>/domain
internal/<context>/domain         -> stdlib only, or tiny dependency-free helper libraries after explicit review
internal/platform                 -> stdlib and third-party infrastructure libraries
```

Any import not matching the direction above is suspicious and must be justified in `docs/architecture/package-map.md`.

## Cross-Context Rules

- Context A may call Context B through B's public app service only when the product flow is synchronous and truly needs a direct response.
- Prefer domain/application events for state propagation between contexts.
- Never import another context's `adapter`, repository, database model, or HTTP DTO.
- Do not share mutable domain entities across contexts. Use explicit contracts or projections.

## Naming Rules

- Name packages after business capability or technical responsibility: `account`, `billing`, `identity`, `postgres`, `http`, `clock`.
- Avoid empty abstraction names: `service`, `manager`, `processor`, `helper`, `common`, `utils`.
- Use `app` for application orchestration, not `service` as a catch-all.
- Keep package names short, lowercase, and singular unless the domain language strongly says otherwise.

## Interface Rules

- Define interfaces where they are consumed.
- Keep ports small and behavior-based:

```go
type UserRepository interface {
    Save(ctx context.Context, user *domain.User) error
    FindByID(ctx context.Context, id domain.UserID) (*domain.User, error)
}
```

- Do not create `IUserRepository`, `BaseRepository`, or generic CRUD ports before use cases require them.
- Do not return persistence records or HTTP DTOs from ports.

## Error Rules

- Domain errors represent business failures and live in `domain`.
- Use cases translate domain and port errors into application-level outcomes.
- Transport adapters map app outcomes to protocol status codes.
- Repositories wrap infrastructure errors but do not decide user-facing messages.

## Transaction Rules

- Transaction boundaries belong in `app` because use cases define consistency.
- Repositories receive an execution context or transaction handle through a port decided by the app layer.
- HTTP handlers must not open transactions.
- Domain code must not know transactions exist.

## AI-Readable Files

Create or maintain these files when architecture is established:

```text
docs/architecture/package-map.md
docs/architecture/dependency-rules.md
```

Each package entry should include:

```markdown
## internal/<context>/app

Responsibility: orchestrates <context> use cases and transaction boundaries.
Allowed imports: internal/<context>/domain, internal/<context>/app/port.
Forbidden imports: adapter packages, database drivers, HTTP frameworks, other contexts' adapters.
Public entry points: CreateXHandler, GetXQuery, Service.
```

## Red Flags

- `domain` imports `gorm`, `sqlx`, `gin`, `echo`, `zap`, `viper`, `redis`, or project `platform`.
- A package named `common`, `utils`, `shared`, `models`, or `types` grows business logic.
- Two bounded contexts import each other's repositories.
- Handlers contain transaction code or business branching.
- Repositories call other repositories to implement workflows.
- A DTO is used simultaneously as HTTP request, database row, and domain entity.
- Import aliases hide that a package depends on the wrong layer.
- Fixing an import cycle by moving code into `common` instead of correcting ownership.

## Guard Integration

Use `guarding-go-projects` after architecture-affecting code changes. The architecture skill defines package ownership; the guard skill executes formatting, tests, builds, lint, security checks, and boundary checks.
