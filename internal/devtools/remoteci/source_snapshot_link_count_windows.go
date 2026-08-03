//go:build windows

package remoteci

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const sourceSnapshotOpenReparsePoint = 0x00200000

func sourceSnapshotFileHasSingleLink(filePath string, _ os.FileInfo) (singleLink bool, err error) {
	path, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		return false, fmt.Errorf("encode source snapshot path: %w", err)
	}
	handle, err := windows.CreateFile(
		path,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|sourceSnapshotOpenReparsePoint,
		0,
	)
	if err != nil {
		return false, fmt.Errorf("open source snapshot file metadata: %w", err)
	}
	defer func() {
		if closeErr := windows.CloseHandle(handle); err == nil && closeErr != nil {
			err = fmt.Errorf("close source snapshot file metadata: %w", closeErr)
		}
	}()
	var metadata windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &metadata); err != nil {
		return false, fmt.Errorf("read source snapshot file metadata: %w", err)
	}
	if metadata.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return false, nil
	}
	return metadata.NumberOfLinks == 1, nil
}
