//go:build !darwin && !linux && !windows

package securefs

import (
	"errors"
	"os"
)

type pinnedDirectoryPlatformLock struct{}

// ensurePinnedDirectoryPlatformSupported 在任何路径访问前拒绝未实现锁语义的平台。
func ensurePinnedDirectoryPlatformSupported() error {
	return errors.New("pinned directory lock is unsupported on this platform")
}

// acquirePinnedDirectoryPlatform 在未实现内核锁语义的平台上 fail-fast。
func acquirePinnedDirectoryPlatform(
	_ string,
	_ *os.Root,
	_ *os.File,
	_ string,
) (pinnedDirectoryPlatformLock, *os.File, error) {
	return pinnedDirectoryPlatformLock{}, nil, ensurePinnedDirectoryPlatformSupported()
}

// closePinnedDirectoryPlatform 对 unsupported 平台保持不可达且无兜底锁行为。
func closePinnedDirectoryPlatform(_ pinnedDirectoryPlatformLock, _ *os.File) error {
	return nil
}
