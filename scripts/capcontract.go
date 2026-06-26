//go:build ignore

package main

import (
	"os"
	"os/exec"
)

// main 保留旧入口文件名，并转发到 scripts/capcontract 子包执行。
// 这样调用方仍可使用 go run scripts/capcontract.go，同时实际逻辑集中在可测试包内。
func main() {
	args := append([]string{"run", "./scripts/capcontract"}, os.Args[1:]...)
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
