//go:build !windows

package main

import (
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func runtimeServerUsesSharedGoplsDaemonPlatform(base string, args []string) bool {
	return base == "gopls" && slices.ContainsFunc(args, func(arg string) bool {
		return strings.HasPrefix(arg, "-remote=auto;")
	})
}

// runtimeServerCloseGoplsRootCohortClient 保持原生 auto daemon 的延迟 owner 关闭顺序。
func runtimeServerCloseGoplsRootCohortClient(client multilsp.Client, lease *multilsp.GoplsRootCohortLease) error {
	if lease == nil {
		return client.Close()
	}
	return lease.ReleaseWithOwner(func() error {
		return client.Close()
	})
}
