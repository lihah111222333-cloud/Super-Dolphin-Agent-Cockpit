package fxadapter

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

type storeNodeSpawnRecorderAdapter struct {
	store taskdag.NodeSpawnRecorderStore
}

// NewStoreNodeSpawnRecorder 创建存储节点spawnrecorder。
func NewStoreNodeSpawnRecorder(store taskdag.NodeSpawnRecorderStore) (nodeexec.NodeSpawnRecorder, error) {
	if store == nil {
		return nil, errors.New("store node spawn recorder: nil store")
	}
	return &storeNodeSpawnRecorderAdapter{store: store}, nil
}

// RecordNodeSpawn 记录节点spawn。
func (a *storeNodeSpawnRecorderAdapter) RecordNodeSpawn(ctx context.Context, dagKey, nodeKey string, runID int64, threadID string) error {
	if a == nil || a.store == nil {
		return errors.New("store node spawn recorder: nil receiver")
	}
	_, err := a.store.RecordNodeSpawn(ctx, taskdag.RecordNodeSpawnInput{DagKey: dagKey, NodeKey: nodeKey, RunID: runID, ThreadID: threadID})
	return err
}
