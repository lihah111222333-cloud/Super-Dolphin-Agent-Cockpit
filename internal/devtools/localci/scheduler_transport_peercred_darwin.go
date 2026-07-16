//go:build darwin

package localci

import "golang.org/x/sys/unix"

func schedulerPlatformPeerUID(fd int) (int, error) {
	credentials, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(credentials.Uid), nil
}
