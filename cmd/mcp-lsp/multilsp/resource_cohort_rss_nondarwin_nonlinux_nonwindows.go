//go:build !darwin && !linux && !windows

package multilsp

import (
	"fmt"
	"runtime"
)

// refreshStaleResourceCohortRSS 对未验证平台保持 fail-fast。
func refreshStaleResourceCohortRSS(resourceCohortMember) (uint64, error) {
	return 0, fmt.Errorf("refresh stale LSP process-tree RSS: unsupported platform %s", runtime.GOOS)
}
