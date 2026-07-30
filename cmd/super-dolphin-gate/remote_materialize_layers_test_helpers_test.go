package main

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func runRemoteBaselineLayerStageAsync(t *testing.T, layers []remoteci.BaselineLayer, stage string, action remoteBaselineLayerAction) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	cacheRoot := t.TempDir()
	expandedRoot := t.TempDir()
	safego.Go(t.Context(), nil, "test.remoteBaselineLayerStage", func(ctx context.Context) {
		result <- runRemoteBaselineLayerStage(ctx, cacheRoot, expandedRoot, layers, stage, action)
	})
	return result
}
