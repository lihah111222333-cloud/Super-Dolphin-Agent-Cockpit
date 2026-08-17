// Package main retains a fail-closed tombstone for the retired candidate-built guard.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "candidate-built mcp-lsp catalog guard is retired; use the compiled super-dolphin-gate worker")
	os.Exit(2)
}
