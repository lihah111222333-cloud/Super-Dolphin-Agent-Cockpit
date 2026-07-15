//go:build ignore

package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/typednil"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/passes/nilness"
)

func main() {
	multichecker.Main(nilness.Analyzer, typednil.Analyzer)
}
