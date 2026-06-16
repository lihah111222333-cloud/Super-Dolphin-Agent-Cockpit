# Go Code Standards

## Source Organization

- One package owns one responsibility. Package names must describe that responsibility.
- Keep files cohesive: split by behavior when a file becomes hard to scan, not by arbitrary type categories.
- Use `doc.go` for package ownership, architecture role, and forbidden responsibilities.
- Command packages may use `Command <name> ...` package comments; library packages should use `Package <name> ...`.
- Avoid `init` except for isolated registration with no hidden I/O.

## Naming

- Use domain language over technical placeholders: `Account`, `Credential`, `LaunchSpec`, not `Data`, `Manager`, `Info`.
- Avoid package stutter: `user.Service`, not `user.UserService` unless the distinction matters.
- Avoid broad names: `common`, `utils`, `shared`, `models`, `types`, `helpers`.
- Name booleans as predicates: `isActive`, `hasPermission`, `allowRetry`.

## Comments

Good comments describe contracts:

```go
// Package account owns account lifecycle use cases and domain rules.
//
// It must not import HTTP, SQL, Redis, or configuration packages.
package account

// UserID identifies a user inside the account bounded context.
// It is opaque outside this package to prevent accidental cross-context reuse.
type UserID string
```

Bad comments restate code:

```go
// UserID is a string.
type UserID string
```

Comment checklist:

- Package ownership and forbidden imports are documented.
- Exported identifiers have Go doc comments starting with the identifier.
- Business invariants are written near the type or constructor that enforces them.
- Concurrency and cancellation responsibilities are documented near goroutine/channel code.
- Error wrapping explains the failed operation, not just the underlying error.

## Error Handling

- Return errors; do not hide them in logs.
- Wrap with operation context: `fmt.Errorf("save user %s: %w", id, err)`.
- Use `errors.Is` / `errors.As` compatible sentinel or typed errors for decisions.
- Domain errors should describe business failure, not transport status codes.
- Do not return partially valid objects unless the contract explicitly says so.
- Do not log and return the same error in internal layers; that creates duplicate logs.
- Do not swallow errors with `return nil`, empty branches, or log-only handling.
- Do not use `errors.New(fmt.Sprintf(...))`; use `fmt.Errorf`.
- Do not wrap errors with `%v`; use `%w` so callers can use `errors.Is` / `errors.As`.

## Logging

- Default concrete logger: standard library `log/slog`, wrapped by `internal/platform/logging`.
- Domain packages must not log.
- App/usecase packages normally return errors and events, not logs. If observability is a business requirement, depend on a small app-owned port.
- Protocol adapters, command entrypoints, bootstrap, and background worker owners may log boundary outcomes once.
- Log messages must include operation, stable identifiers, result, and duration when useful.
- Never log secrets, raw credentials, tokens, private keys, request bodies containing credentials, or unredacted external payloads.
- Prefer structured fields over string concatenation.
- Do not use `fmt.Print*`, `log.Print*`, `log.Fatal*`, direct `slog.*`, `panic`, or `os.Exit` outside command/bootstrap/platform logging code.

Boundary logging pattern:

```go
// HandleCreateUser maps HTTP input to the account use case and logs the boundary outcome once.
func (h *Handler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
    started := h.clock.Now()
    id, err := h.createUser.Handle(r.Context(), cmd)
    if err != nil {
        h.log.ErrorContext(r.Context(), "create user failed", "error", err, "duration", h.clock.Since(started))
        writeError(w, err)
        return
    }
    h.log.InfoContext(r.Context(), "create user succeeded", "user_id", id, "duration", h.clock.Since(started))
}
```

## Context And Time

- Accept `context.Context` as the first parameter for I/O, blocking operations, transactions, and use cases.
- Do not store context in structs.
- Domain code must not call `time.Now`; inject time through app ports or pass values in.
- Timers, tickers, goroutines, and channel lifetimes must be cancellable and documented.

## Testing

- Test behavior, not implementation details.
- Keep domain tests pure and fast.
- Use fake ports for app/usecase tests.
- Use adapter integration tests only when validating persistence/protocol mapping.
- Add regression tests for every bug fix.
- Prefer table tests for validation matrices, permission rules, and state transitions.

## Concurrency

- Prefer ownership over shared mutable state.
- Keep locks private to the struct that owns the protected state.
- Document lock ordering when more than one lock can be held.
- Only the sender closes a channel.
- Every goroutine must have a clear shutdown path through context, done channel, or owner lifecycle.

## Security

- Validate external input at adapter boundaries and re-check business invariants in domain/app.
- Never log secrets, raw credentials, tokens, or private keys.
- Keep secret material out of domain types unless the domain explicitly owns secret lifecycle.
- Treat generated IDs, signatures, crypto, and auth decisions as security-sensitive and comment assumptions.

## Persistence And Adapters

- Repositories map storage rows to domain/app contracts; they do not orchestrate use cases.
- HTTP handlers map requests/responses and call app services; they do not open transactions or query repositories directly.
- Persistence models and transport DTOs must not become domain entities.
- Keep SQL, ORM, Redis, and framework-specific types out of domain/app APIs.
