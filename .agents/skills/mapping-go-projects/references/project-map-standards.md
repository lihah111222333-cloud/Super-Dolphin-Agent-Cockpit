# Project Map Standards

## Why This Repository Uses A Local Map

AI coding sessions need a low-latency way to answer "where is this symbol?" without reading the whole repository. The map is a generated navigation aid, not a replacement for compiler checks, tests, LSP, or code review.

## Industry Patterns Considered

| Pattern | Strength | Cost | Repository Decision |
| --- | --- | --- | --- |
| Go `go/parser`, `go/ast`, `go/token` | Standard-library parsing of declarations, comments, imports, and source positions | Syntactic only, no cross-package type resolution | Use for map generation because it is reliable and dependency-free |
| LSP `workspace/symbol` and `textDocument/documentSymbol` | Mature editor model for symbol lookup, definition, and references | Requires a language server session and editor/runtime state | Mirror the lookup shape in generated JSON/TSV, but do not require `gopls` in hooks |
| ctags / Universal Ctags | Fast flat symbol index, easy to grep | Cross-language tags can be shallow and inconsistent by language | Use TSV files with ctags-like rows for AI-friendly grep |
| LSIF / SCIP | Rich code intelligence for large repos and Sourcegraph-style navigation | Heavier index, more tooling, usually not suited for uncommitted local hook output | Keep as a future option if the project becomes large enough |
| Architecture package map / ADRs | Explains ownership and design decisions | Manual docs drift unless guarded | Keep docs for intent, and generate project map for current code shape |

## Chosen Standard

The project map must be:

- Generated from source, not hand-written.
- Local-only and gitignored.
- Stable enough for `rg`, JSON parsing, and AI summarization.
- Fast enough to run during commit and merge guards.
- Based on Go AST declarations and source positions.
- Able to index packages, imports, functions, methods, structs, interfaces, consts, vars, and type declarations.
- Explicit about limitations: no full call graph, no type inference, no runtime dependency proof.

## When To Consider Heavier Tools

Consider `gopls`, SCIP, LSIF, or Sourcegraph-style indexing when the project needs:

- Cross-repository navigation.
- Precise references and definitions after type checking.
- Large-team web code search.
- Historical symbol tracking.
- IDE-level rename and refactor support.

Even then, keep this local project map because it is cheap, deterministic, and useful inside automated AI workflows.

## External References

- Go AST package: https://pkg.go.dev/go/ast
- Go parser package: https://pkg.go.dev/go/parser
- Language Server Protocol specification: https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
- SCIP: https://github.com/sourcegraph/scip
- Universal Ctags: https://docs.ctags.io/
