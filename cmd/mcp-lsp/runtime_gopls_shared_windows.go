//go:build windows

package main

import (
	"errors"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func runtimeServerUsesSharedGoplsDaemonPlatform(base string, args []string) bool {
	return base == "gopls" && slices.ContainsFunc(args, func(arg string) bool {
		return strings.HasPrefix(arg, "-remote.listen.timeout=")
	})
}

// runtimeServerCloseGoplsRootCohortClient 先关闭当前 forwarder，再释放共享 daemon lease。
func runtimeServerCloseGoplsRootCohortClient(client multilsp.Client, lease *multilsp.GoplsRootCohortLease) error {
	if lease == nil {
		return client.Close()
	}
	closeErr := client.Close()
	return errors.Join(closeErr, lease.Release())
}
