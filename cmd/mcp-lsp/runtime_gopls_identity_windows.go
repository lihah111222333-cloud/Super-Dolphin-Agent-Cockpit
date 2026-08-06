//go:build windows

package main

import (
	"errors"
	"os"
)

// runtimeServerStableFilesystemIdentity 明确拒绝 Windows 的未证明 root cohort identity。
func runtimeServerStableFilesystemIdentity(os.FileInfo) (string, error) {
	return "", errors.New("gopls root cohort filesystem identity is unsupported on windows")
}
