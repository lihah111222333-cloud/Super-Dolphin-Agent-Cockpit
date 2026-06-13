// Package dagmetrics provides process-local counters for DAG dispatch
// observability. Prometheus wiring lives in internal/platform/metrics; keeping
// the mutable counters here avoids internal/platform importing cmd/mcp-orch.
package dagmetrics

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const retryAlertThreshold uint64 = 3
const maxTrackedRetryNodes = 256

type NodeRetryCount struct {
	DagKey  string
	NodeKey string
	Count   uint64
}

type Snapshot struct {
	DispatchFailedTotal       uint64
	RetryCountPerNode         []NodeRetryCount
	RetryCountPerNodeOverflow uint64
	RetryAlertTotal           uint64
}

type RetryRecord struct {
	DagKey       string
	NodeKey      string
	Count        uint64
	AttemptCount int32
	ShouldAlert  bool
}

var (
	dispatchFailedTotal atomic.Uint64
	retryAlertTotal     atomic.Uint64
	retryOverflowTotal  atomic.Uint64

	mu          sync.RWMutex
	retryByNode = map[string]NodeRetryCount{}
)

// IncDispatchFailed 累加dispatchfailed。
func IncDispatchFailed() {
	dispatchFailedTotal.Add(1)
}

// RecordRetry 记录重试。
func RecordRetry(dagKey, nodeKey string, attemptCount int32) RetryRecord {
	dagKey = strings.TrimSpace(dagKey)
	nodeKey = strings.TrimSpace(nodeKey)
	if dagKey == "" || nodeKey == "" {
		return RetryRecord{}
	}
	key := dagKey + "\x00" + nodeKey
	mu.Lock()
	count := retryByNode[key]
	if count.DagKey == "" && len(retryByNode) < maxTrackedRetryNodes {
		count.DagKey = dagKey
		count.NodeKey = nodeKey
	} else if count.DagKey == "" {
		retryOverflowTotal.Add(1)
	}
	if count.DagKey != "" {
		count.Count = maxUint64(count.Count, uint64(maxInt32(attemptCount, 0)))
		retryByNode[key] = count
	}
	shouldAlert := uint64(maxInt32(attemptCount, 0)) >= retryAlertThreshold
	if shouldAlert {
		retryAlertTotal.Add(1)
	}
	mu.Unlock()
	return RetryRecord{
		DagKey:       dagKey,
		NodeKey:      nodeKey,
		Count:        count.Count,
		AttemptCount: attemptCount,
		ShouldAlert:  shouldAlert,
	}
}

// Read 读取DAG 指标。
func Read() Snapshot {
	mu.RLock()
	retries := make([]NodeRetryCount, 0, len(retryByNode))
	for _, count := range retryByNode {
		retries = append(retries, count)
	}
	mu.RUnlock()
	sort.Slice(retries, func(i, j int) bool {
		if retries[i].DagKey == retries[j].DagKey {
			return retries[i].NodeKey < retries[j].NodeKey
		}
		return retries[i].DagKey < retries[j].DagKey
	})
	return Snapshot{
		DispatchFailedTotal:       dispatchFailedTotal.Load(),
		RetryCountPerNode:         retries,
		RetryCountPerNodeOverflow: retryOverflowTotal.Load(),
		RetryAlertTotal:           retryAlertTotal.Load(),
	}
}

// RetryCountForNode 为节点重试count。
func RetryCountForNode(dagKey, nodeKey string) uint64 {
	dagKey = strings.TrimSpace(dagKey)
	nodeKey = strings.TrimSpace(nodeKey)
	if dagKey == "" || nodeKey == "" {
		return 0
	}
	mu.RLock()
	defer mu.RUnlock()
	return retryByNode[dagKey+"\x00"+nodeKey].Count
}

// ResetForTesting 为testing重置DAG 指标。
func ResetForTesting() {
	dispatchFailedTotal.Store(0)
	retryAlertTotal.Store(0)
	retryOverflowTotal.Store(0)
	mu.Lock()
	retryByNode = map[string]NodeRetryCount{}
	mu.Unlock()
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
