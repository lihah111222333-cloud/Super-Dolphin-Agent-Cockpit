package main

import (
	"bytes"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestFrontendCodeSizeCLIRejectsMissingTree 验证命令不接受隐式工作区候选源。
func TestFrontendCodeSizeCLIRejectsMissingTree(t *testing.T) {
	code := runCLI([]string{"frontend-code-size", "check"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != int(gatecontract.ExitProtocol) {
		t.Fatalf("exit=%d, want protocol", code)
	}
}
