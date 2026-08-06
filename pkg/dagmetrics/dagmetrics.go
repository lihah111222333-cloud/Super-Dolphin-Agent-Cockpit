// Package dagmetrics 记录 DAG 调度的进程内观测计数。
// Prometheus wiring 在 internal/platform/metrics；计数器放在 leaf 包可避免 platform 反向导入 cmd/mcp-orch。
package dagmetrics

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const retryAlertThreshold uint64 = 3
const maxTrackedRetryNodes = 256

// NodeRetryCount 是对外暴露的单节点重试计数，字段名会进入指标导出和测试快照。
type NodeRetryCount struct {
	DagKey  string // DAG 稳定标识。
	NodeKey string // 节点稳定标识。
	Count   uint64 // 已观察到的最大重试次数。
}

// Snapshot 是 DAG 指标的一次读取快照；读取过程中并发增量允许落到下一次快照。
type Snapshot struct {
	DispatchFailedTotal       uint64           // dispatch 失败总数。
	RetryCountPerNode         []NodeRetryCount // 按 DAG/node 排序后的有限节点重试计数。
	RetryCountPerNodeOverflow uint64           // 超出 maxTrackedRetryNodes 后被丢弃的节点计数。
	RetryAlertTotal           uint64           // 达到告警阈值的重试事件总数。
}

// RetryRecord 是 RecordRetry 返回给调用方的本次重试判定结果。
type RetryRecord struct {
	DagKey       string // 清理后的 DAG key。
	NodeKey      string // 清理后的节点 key。
	Count        uint64 // 记录中的最大重试次数。
	AttemptCount int32  // 调用方本次上报的 attempt 数。
	ShouldAlert  bool   // 本次 attempt 是否达到告警阈值。
}

// Registry 保存 DAG 指标的进程内计数状态。
type Registry struct {
	dispatchFailedTotal atomic.Uint64
	retryAlertTotal     atomic.Uint64
	retryOverflowTotal  atomic.Uint64

	mu          sync.RWMutex
	retryByNode map[string]NodeRetryCount
}

// NewRegistry 创建独立 DAG 指标 registry。
func NewRegistry() *Registry {
	return &Registry{retryByNode: map[string]NodeRetryCount{}}
}

// IncDispatchFailed 记录一次 DAG dispatch 失败。
func (r *Registry) IncDispatchFailed() {
	r.dispatchFailedTotal.Add(1)
}

// RecordRetry 记录节点重试，并在达到阈值时返回 ShouldAlert。
// 空 DAG 或节点 key 会被忽略，节点序列过多时只增加 overflow，避免指标基数失控。
func (r *Registry) RecordRetry(dagKey, nodeKey string, attemptCount int32) RetryRecord {
	dagKey = strings.TrimSpace(dagKey)
	nodeKey = strings.TrimSpace(nodeKey)
	if dagKey == "" || nodeKey == "" {
		return RetryRecord{}
	}
	key := dagKey + "\x00" + nodeKey
	r.mu.Lock()
	count := r.retryByNode[key]
	if count.DagKey == "" && len(r.retryByNode) < maxTrackedRetryNodes {
		count.DagKey = dagKey
		count.NodeKey = nodeKey
	} else if count.DagKey == "" {
		r.retryOverflowTotal.Add(1)
	}
	if count.DagKey != "" {
		count.Count = maxUint64(count.Count, uint64(maxInt32(attemptCount, 0)))
		r.retryByNode[key] = count
	}
	shouldAlert := uint64(maxInt32(attemptCount, 0)) >= retryAlertThreshold
	if shouldAlert {
		r.retryAlertTotal.Add(1)
	}
	r.mu.Unlock()
	return RetryRecord{
		DagKey:       dagKey,
		NodeKey:      nodeKey,
		Count:        count.Count,
		AttemptCount: attemptCount,
		ShouldAlert:  shouldAlert,
	}
}

// Read 返回当前 DAG 指标快照，并稳定排序节点计数便于测试和导出。
func (r *Registry) Read() Snapshot {
	r.mu.RLock()
	retries := make([]NodeRetryCount, 0, len(r.retryByNode))
	for _, count := range r.retryByNode {
		retries = append(retries, count)
	}
	r.mu.RUnlock()
	sort.Slice(retries, func(i, j int) bool {
		if retries[i].DagKey == retries[j].DagKey {
			return retries[i].NodeKey < retries[j].NodeKey
		}
		return retries[i].DagKey < retries[j].DagKey
	})
	return Snapshot{
		DispatchFailedTotal:       r.dispatchFailedTotal.Load(),
		RetryCountPerNode:         retries,
		RetryCountPerNodeOverflow: r.retryOverflowTotal.Load(),
		RetryAlertTotal:           r.retryAlertTotal.Load(),
	}
}

// RetryCountForNode 返回指定 DAG/node 已记录的最大重试次数。
func (r *Registry) RetryCountForNode(dagKey, nodeKey string) uint64 {
	dagKey = strings.TrimSpace(dagKey)
	nodeKey = strings.TrimSpace(nodeKey)
	if dagKey == "" || nodeKey == "" {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.retryByNode[dagKey+"\x00"+nodeKey].Count
}

// ResetForTesting 清空 registry 所有 DAG 指标，仅供测试隔离使用。
func (r *Registry) ResetForTesting() {
	r.dispatchFailedTotal.Store(0)
	r.retryAlertTotal.Store(0)
	r.retryOverflowTotal.Store(0)
	r.mu.Lock()
	r.retryByNode = map[string]NodeRetryCount{}
	r.mu.Unlock()
}

// maxInt32 返回两个 int32 中的较大值。
func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// maxUint64 返回两个 uint64 中的较大值。
func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
