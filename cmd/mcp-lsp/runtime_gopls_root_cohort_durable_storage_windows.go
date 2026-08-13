//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// runtimeServerPublishGoplsRootCohortRecord 在 Windows 上以 write-through 语义替换记录。
func runtimeServerPublishGoplsRootCohortRecord(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(
		from,
		to,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return fmt.Errorf("publish gopls root cohort record: %w", err)
	}
	return nil
}

// runtimeServerSyncGoplsRootCohortDirectory 在 Windows 上不调用只读目录句柄的 FlushFileBuffers。
func runtimeServerSyncGoplsRootCohortDirectory(_ string) error {
	return nil
}
