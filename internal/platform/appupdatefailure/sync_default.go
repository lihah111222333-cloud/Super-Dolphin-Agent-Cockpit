//go:build !windows

package appupdatefailure

import (
	"fmt"
	"os"
)

// syncDirectory 将原子 rename 的目录项持久化到非 Windows 文件系统。
func syncDirectory(stageDir string) error {
	directory, err := os.Open(stageDir)
	if err != nil {
		return fmt.Errorf("open app update stage dir for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync app update stage dir: %w", err)
	}
	return nil
}
