package codemapindex

// GeneratorAnchor is the canonical truth value embedded into ai-index.json
// by the Go generator (scripts/codemap_index.go). Any test or CI check
// MUST verify that the on-disk index contains exactly this string in its
// "generator" field. If a different toolchain (Python, Node, etc.) produces
// the file, the anchor will be missing or wrong and tests will fail.
const GeneratorAnchor = "go:scripts/codemap_index.go:v1"
