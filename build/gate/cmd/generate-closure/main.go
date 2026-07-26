package main

import (
	"flag"
	"fmt"
	"os"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
)

func main() {
	tree := flag.String("tree", "HEAD", "Git tree or commit used as the only source input")
	check := flag.Bool("check", false, "verify generated files in the Git tree without writing the worktree")
	refreshDependencies := flag.Bool("refresh-dependencies", false, "refresh the node-local runtime dependency lock from the Git tree")
	flag.Parse()
	if *refreshDependencies {
		if *check {
			fmt.Fprintln(os.Stderr, "-refresh-dependencies and -check are mutually exclusive")
			os.Exit(2)
		}
		if err := gateclosure.RefreshDependencyClosure(*tree); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := gateclosure.Generate(*tree, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
