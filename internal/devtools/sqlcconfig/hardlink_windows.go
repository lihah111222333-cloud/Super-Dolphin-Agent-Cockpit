//go:build windows

package sqlcconfig

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func hardLinkCount(path string, info os.FileInfo) (uint64, error) {
	if info == nil {
		return 0, errors.New("output link metadata is missing")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &handleInfo); err != nil {
		return 0, err
	}
	if handleInfo.NumberOfLinks == 0 {
		return 0, errors.New("output link count is unavailable")
	}
	return uint64(handleInfo.NumberOfLinks), nil
}
