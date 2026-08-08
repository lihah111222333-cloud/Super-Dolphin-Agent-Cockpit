//go:build !unix

package trustedlauncher

import "os"

// trustedFileOwnerUID 在无 Unix owner 语义的平台 fail closed。
func trustedFileOwnerUID(os.FileInfo) (int, bool) {
	return 0, false
}
