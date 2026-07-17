//go:build windows

package gate

import "io/fs"

func fileOwnerUID(fs.FileInfo) (int, bool) {
	return 0, false
}
