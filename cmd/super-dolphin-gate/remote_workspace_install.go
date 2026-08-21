package main

import (
	"errors"
	"io"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// runRemoteInstallWorkspace 在 candidate Gate 编译后为唯一 worker 安装 tree-owned workspace。
func runRemoteInstallWorkspace(args []string, _ io.Writer) error {
	if len(args) != 0 {
		return errors.New("remote workspace installer does not accept arguments")
	}
	return gatecontract.InstallPortableGoWorkspace(gatecontract.ExecutorSourcePath)
}
