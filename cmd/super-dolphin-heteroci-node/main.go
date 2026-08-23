// Package main 提供跨平台 heteroci 节点 probe 可执行壳。
package main

import "os"

func main() {
	os.Exit(runNodeCLI(os.Args[1:], os.Stdout, os.Stderr, defaultNodeProviderFactory))
}
