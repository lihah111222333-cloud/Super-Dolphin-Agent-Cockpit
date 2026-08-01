//go:build windows

package gateprivate

import "os"

func trustedExecutableOwnedByRoot(os.FileInfo) bool {
	return false
}

func trustedExecutableOwnedByCurrentOrRoot(os.FileInfo) bool {
	return false
}

func trustedExecutableOwnedByCurrent(os.FileInfo) bool {
	return false
}
