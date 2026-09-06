//go:build windows

package securefs

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// checkLocalFilesystemNative 通过 Windows 驱动器类型确认路径不是远程或未知卷。
func checkLocalFilesystemNative(path string) error {
	if networkPathSyntax(path) {
		return fmt.Errorf("network path syntax")
	}
	volume := filepath.VolumeName(path)
	if volume == "" {
		return fmt.Errorf("volume unavailable")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return fmt.Errorf("volume path unavailable")
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_RAMDISK:
		return nil
	case windows.DRIVE_REMOTE:
		return fmt.Errorf("remote drive")
	default:
		return fmt.Errorf("drive locality unavailable")
	}
}
