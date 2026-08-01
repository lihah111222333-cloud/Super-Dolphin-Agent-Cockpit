//go:build linux

package gateprivate

import "golang.org/x/sys/unix"

func trustedExecutableOnReadOnlyMount(path string) bool {
	var state unix.Statfs_t
	return unix.Statfs(path, &state) == nil && state.Flags&unix.ST_RDONLY != 0
}
