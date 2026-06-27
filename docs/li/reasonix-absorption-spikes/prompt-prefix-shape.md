# Prompt Prefix Shape Spike

## Existing Facts

- `StartAssembly` already contains `Boundary`, `ResolvedSections`, `Snapshot`, `SuppressedTools`, `UserContext`, and `SystemContext`.
- `PromptAssemblySnapshot` already contains `Hash` and `SectionSnapshot`.
- No prefix-shape field exists on the current start assembly before this plan.

## Decision

Add `PrefixShape` to `internal/dto/provider.StartAssembly`. Build it in `internal/module/prompt` from base instructions, developer instructions, boundary, resolved sections, and suppressed tool names. Provider logs must include only shape metadata and hash, not prompt contents.
