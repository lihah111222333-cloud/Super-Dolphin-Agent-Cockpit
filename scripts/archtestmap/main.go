package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/archtestmap"
)

func main() {
	check := flag.Bool("check", false, "fail when generated archtest documentation is stale")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "archtestmap does not accept positional arguments")
		os.Exit(2)
	}
	if err := archtestmap.Generate(".", *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
