//go:build windows

package memory

import (
	"testing"

	"golang.org/x/sys/windows"
)

func makeAbsorbedEntryDeleteFail(t *testing.T, absorbedPath string) {
	t.Helper()
	path, err := windows.UTF16PtrFromString(absorbedPath)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q) error = %v", absorbedPath, err)
	}
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile(%q) error = %v", absorbedPath, err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
}
