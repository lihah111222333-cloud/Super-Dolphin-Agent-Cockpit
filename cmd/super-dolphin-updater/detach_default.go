//go:build !darwin

package main

import "os/exec"

// configureDetachedCommand 在非 macOS 平台保留空实现。
func configureDetachedCommand(cmd *exec.Cmd) {
	_ = cmd
}
