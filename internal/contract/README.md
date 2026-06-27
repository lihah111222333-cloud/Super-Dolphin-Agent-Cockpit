# internal/contract store port convention

`internal/contract` only holds stable cross-module contracts. For the D01 module-store decoupling work, new ports that replace direct `internal/module` to `internal/store` imports should use `store_<domain>.go`.

## Naming

- `store_<domain>.go` is the stable store port file for a module-facing persistence capability.
- `<domain>` comes from the source domain or the store subpackage name. For example, a prompt store port belongs in `store_prompt.go`.
- Interface names should prefer `<Domain>Reader`, `<Domain>Writer`, or `<Domain>Store`.
- Split read and write interfaces when consumers only need one side.
- Do not create consumer aggregate interfaces that combine unrelated store responsibilities across multiple store domains.

## DTO Placement

- Wire DTOs should prefer `internal/dto`.
- Port-only models may live in `internal/contract` when they are part of the stable cross-module port.
- Store persistence DTOs should not leak by default. Convert them at the store adapter or contract boundary.

## Scope

This `store_<domain>.go` convention is only for new module-to-store decoupling ports introduced by D01. It does not require reshuffling existing domain contracts such as `HookReviewStore`, `ThreadMetadataStore`, or other already-established contract files.
