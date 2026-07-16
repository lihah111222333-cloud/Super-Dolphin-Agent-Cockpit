//go:build linux

package localci

import "golang.org/x/sys/unix"

func schedulerPlatformPeerUID(fd int) (int, error) {
	credentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(credentials.Uid), nil
}
