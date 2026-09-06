//go:build darwin

package securefs

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

type statfsDarwinFunc func(string, *unix.Statfs_t) error

func checkLocalFilesystemNative(path string) error {
	return checkLocalFilesystemDarwin(path, unix.Statfs)
}

func checkLocalFilesystemDarwin(path string, statfs statfsDarwinFunc) error {
	if statfs == nil {
		return fmt.Errorf("statfs detector unavailable")
	}
	var state unix.Statfs_t
	if err := statfs(path, &state); err != nil {
		return fmt.Errorf("statfs failed")
	}
	name := strings.TrimRight(string(state.Fstypename[:]), "\x00")
	name = strings.ToLower(name)
	if darwinNetworkFilesystem(name) {
		return fmt.Errorf("network filesystem")
	}
	if !darwinKnownLocalFilesystem(name) {
		return fmt.Errorf("unknown filesystem type")
	}
	return nil
}

func darwinKnownLocalFilesystem(name string) bool {
	switch name {
	case "apfs", "hfs", "hfs+", "ufs", "zfs", "tmpfs", "ramfs":
		return true
	default:
		return false
	}
}

func darwinNetworkFilesystem(name string) bool {
	switch name {
	case "nfs", "nfs4", "smbfs", "afpfs", "webdav", "sshfs", "fuse", "fusefs":
		return true
	default:
		return strings.HasPrefix(name, "fuse.")
	}
}
