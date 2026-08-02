package main

import (
	"context"
	"errors"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// materializeRemoteBaseline accepts only the source already fixed in the OCI image.
// Candidate changes are applied later from their separately verified patch artifact.
func materializeRemoteBaseline(context.Context, string, string, string, remoteci.ShardRequest, remoteObjectDownload) error {
	return nil
}

func protectRemoteExpandedBaselineRoot(expandedRoot string) error {
	info, err := os.Lstat(expandedRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("remote expanded baseline root is not a physical directory"), err)
	}
	return nil
}
