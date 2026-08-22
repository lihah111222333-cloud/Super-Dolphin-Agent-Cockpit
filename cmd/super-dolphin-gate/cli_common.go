package main

import (
	"fmt"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func infrastructureError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf(format, args...))
}

// canonicalizeUniqueAuthorityArgs 统一受信 CLI 参数拼写并拒绝重复 authority 字段。
func canonicalizeUniqueAuthorityArgs(args []string, names ...string) ([]string, error) {
	canonical, err := catalog.CanonicalizeAuthorityFlags(args, names...)
	if err != nil {
		return nil, err
	}
	if err := catalog.ValidateNoDuplicateAuthorityFlags(canonical, names...); err != nil {
		return nil, err
	}
	return canonical, nil
}
