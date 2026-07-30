package main

import (
	"fmt"
	"io"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// createRequesterFingerprint 生成不具备授权能力的随机请求关联键。
func createRequesterFingerprint(stdout io.Writer) error {
	fingerprint, err := gatecontract.GenerateRequesterFingerprint()
	if err != nil {
		return infrastructureError("generate requester fingerprint: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, fingerprint); err != nil {
		return infrastructureError("write requester fingerprint: %w", err)
	}
	return nil
}
