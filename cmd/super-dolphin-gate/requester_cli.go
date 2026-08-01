package main

import (
	"io"
)

// runRequesterCLI 管理入口无关的 requester fingerprint 及其远程运行查询。
func runRequesterCLI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("requester subcommand is required (create or runs)")
	}
	switch args[0] {
	case "create":
		if len(args) != 1 {
			return protocolError("requester create accepts no arguments")
		}
		return createRequesterFingerprint(stdout)
	case "runs":
		return listRequesterRuns(args[1:], stdout)
	default:
		return protocolError("requester subcommand must be create or runs")
	}
}
