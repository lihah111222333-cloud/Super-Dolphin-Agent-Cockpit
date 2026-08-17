//go:build !windows

package installer

import "os"

func isUnsafeAssetFile(info os.FileInfo) bool {
	return info == nil || info.Mode()&os.ModeSymlink != 0
}
