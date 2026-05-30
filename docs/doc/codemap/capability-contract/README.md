# Capability Contract

`capability_manifest.json` is a generated Go AST capability manifest for Super-Dolphin's high-value contract surfaces.

Default scanned roots:

- `internal/contract`
- `internal/provider`
- `cmd/mcp-orch/orchestration`
- `cmd/mcp-orch/tools`

Refresh and check:

```bash
go run ./scripts/capcontract
go run ./scripts/capcontract --check
```

The manifest records packages, functions, methods, interfaces, interface methods, structs, parameters, return types, and export status. It complements the file-level AI project map with symbol-level contract visibility.
