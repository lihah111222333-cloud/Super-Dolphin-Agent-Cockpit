//go:build windows

package main

// normalizeReviewFrontendDependencySeed 在 Windows 上拒绝保留缓存的 seed，避免不可靠的二次链接。
func normalizeReviewFrontendDependencySeed(sourceRoot, _ string) (string, func() error, error) {
	return cleanWindowsReviewFrontendDependencySeed(sourceRoot)
}
