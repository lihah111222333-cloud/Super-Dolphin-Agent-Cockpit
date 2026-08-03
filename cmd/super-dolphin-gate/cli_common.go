package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func infrastructureError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf(format, args...))
}

// newHookDeliveryID creates an independent identifier for a remote Git action.
func newHookDeliveryID() (string, error) {
	entropy := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, entropy); err != nil {
		return "", fmt.Errorf("read remote hook delivery entropy: %w", err)
	}
	return "delivery:v1:" + hex.EncodeToString(entropy), nil
}
