//go:build ignore

// 命令 codemap_index 生成或检查代码地图索引。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/codemapindex"
)

func main() {
	check := flag.Bool("check", false, "verify docs/doc/codemap generated files without modifying the worktree")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	if err := codemapindex.Generate(root, *check); err != nil {
		fmt.Fprintf(os.Stderr, "codemap index: %v\n", err)
		os.Exit(1)
	}
}
