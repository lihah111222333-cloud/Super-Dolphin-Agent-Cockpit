//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// atomicReplace 在 Windows 上使用 replace-existing/write-through 语义替换目标。
func atomicReplace(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
