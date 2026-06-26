package fxadapter

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// storeNodeSpawnRecorderAdapter 把 taskdag store 的写回能力收窄为 nodeexec 需要的端口。
type storeNodeSpawnRecorderAdapter struct {
	store taskdag.NodeSpawnRecorderStore
}

// NewStoreNodeSpawnRecorder 创建节点 spawn 记录器；store 缺失时构造期直接报错。
func NewStoreNodeSpawnRecorder(store taskdag.NodeSpawnRecorderStore) (nodeexec.NodeSpawnRecorder, error) {
	if store == nil {
		return nil, errors.New("store node spawn recorder: nil store")
	}
	return &storeNodeSpawnRecorderAdapter{store: store}, nil
}

// RecordNodeSpawn 把子 agent threadID 写回 DAG runtime node，供 UI 和后续回收链路追踪。
func (a *storeNodeSpawnRecorderAdapter) RecordNodeSpawn(ctx context.Context, dagKey, nodeKey string, runID int64, threadID string) error {
	if a == nil || a.store == nil {
		return errors.New("store node spawn recorder: nil receiver")
	}
	_, err := a.store.RecordNodeSpawn(ctx, taskdag.RecordNodeSpawnInput{DagKey: dagKey, NodeKey: nodeKey, RunID: runID, ThreadID: threadID})
	return err
}
