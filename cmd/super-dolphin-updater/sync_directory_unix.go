//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

// syncUpdaterDirectory 在 Unix 上 fsync recovery capsule 目录。
func syncUpdaterDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open recovery capsule directory for fsync %s: %w", path, err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}
