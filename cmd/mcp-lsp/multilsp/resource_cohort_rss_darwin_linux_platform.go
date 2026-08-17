//go:build darwin || linux

package multilsp

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

// refreshStaleResourceCohortRSS 在 POSIX 上重采样进程树 RSS。
func refreshStaleResourceCohortRSS(member resourceCohortMember) (uint64, error) {
	rssBytes, err := hiddenexec.ProcessTreeRSSBytes(member.ClientPID)
	if err != nil {
		return 0, fmt.Errorf("refresh stale LSP process-tree RSS: %w", err)
	}
	if rssBytes == 0 {
		return 0, errors.New("refresh stale LSP process-tree RSS: zero-byte sample")
	}
	return rssBytes, nil
}
