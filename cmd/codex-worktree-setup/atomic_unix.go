//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// atomicReplace 在 Unix 上原子替换目标并同步父目录元数据。
func atomicReplace(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
