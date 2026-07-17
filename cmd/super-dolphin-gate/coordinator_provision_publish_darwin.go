//go:build darwin

package main

import "golang.org/x/sys/unix"

// publishProductionProvisionRoot 在 macOS 上原子发布且绝不替换并发出现的目标。
func publishProductionProvisionRoot(staging string, installRoot string) error {
	return unix.RenamexNp(staging, installRoot, unix.RENAME_EXCL)
}
