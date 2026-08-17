//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// runtimeServerPublishGoplsRootCohortRecord 在 POSIX 平台替换记录并同步目录项。
func runtimeServerPublishGoplsRootCohortRecord(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("publish gopls root cohort record: %w", err)
	}
	return runtimeServerSyncGoplsRootCohortDirectory(filepath.Dir(target))
}

// runtimeServerSyncGoplsRootCohortDirectory 同步 durable cohort 目录。
func runtimeServerSyncGoplsRootCohortDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open gopls root cohort directory for sync: %w", err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}
