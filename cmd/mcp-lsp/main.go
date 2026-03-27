package main

import (
	"fmt"
	"os"
)

const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	return 0
}
