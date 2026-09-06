//go:build linux

package securefs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type statfsLinuxFunc func(string, *unix.Statfs_t) error

func checkLocalFilesystemNative(path string) error {
	return checkLocalFilesystemLinux(path, unix.Statfs)
}

func checkLocalFilesystemLinux(path string, statfs statfsLinuxFunc) error {
	if statfs == nil {
		return fmt.Errorf("statfs detector unavailable")
	}
	var state unix.Statfs_t
	if err := statfs(path, &state); err != nil {
		return fmt.Errorf("statfs failed")
	}
	if linuxNetworkFilesystem(uint64(state.Type)) {
		return fmt.Errorf("network filesystem")
	}
	if !linuxKnownLocalFilesystem(uint64(state.Type)) {
		return fmt.Errorf("unknown filesystem type")
	}
	return nil
}

// Linux superblock types. Keep this allowlist conservative: an unrecognized
// type is not evidence of locality and therefore fails closed.
func linuxKnownLocalFilesystem(kind uint64) bool {
	switch kind {
	case 0xEF53, // ext2/3/4
		0x58465342, // xfs
		0x9123683E, // btrfs
		0x01021994, // tmpfs
		0x858458f6, // ramfs
		0x794c7630: // overlayfs (container-local backing mount)
		return true
	default:
		return false
	}
}

func linuxNetworkFilesystem(kind uint64) bool {
	switch kind {
	case 0x6969, // NFS
		0xFF534D42, // CIFS/SMB
		0x01021997, // 9p
		0x73757245, // CODA
		0x5346414F, // AFS
		0x00C36400, // CEPH
		0x01161970, // GFS2
		0x7461636F: // OCFS2
		return true
	default:
		return false
	}
}
