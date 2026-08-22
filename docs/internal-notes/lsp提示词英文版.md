# LSP Prompt Notes (English)

The canonical shared agent guidance is [`shared-developer-instructions.md`](../../shared-developer-instructions.md). The public MCP-LSP surface contains only three semantic tools:

- `structure` finds symbol definitions and structural outlines.
- `xref` finds references, callers, call hierarchies, and type impact.
- `diagnostics` checks syntax and type diagnostics after a change.

Use native `cat` / `head` for file reading, native `grep` / `rg` for text search, and native `apply_patch` for edits. A local single-file bug should stay on native tools; do not start broad cross-file semantic exploration unless symbol ownership or impact is genuinely unclear.

There are no MCP-LSP `file`, `read_file`, `inspect`, `grep`, `edit`, `patch_edit`, or `completion` tools or aliases.
