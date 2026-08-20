package heteroci

import (
	"fmt"
	"math"
	"sort"
)

// nodeReservationKey 绑定节点及其当前探针生命周期，避免跨重启复用预留。
type nodeReservationKey struct {
	nodeID        string
	probeIdentity string
	probeBootID   string
}

// nodeCapacityReservation 保存单个节点在整次运行中的保守峰值预留。
type nodeCapacityReservation struct {
	key      nodeReservationKey
	capacity ResourceCapacity
}

// routingNodeCapacityReservations 从节点波次提取整次运行的保守容量预留。
func routingNodeCapacityReservations(plan PackingPlan, snapshot SnapshotSet) ([]nodeCapacityReservation, error) {
	snapshots, err := indexReservationSnapshots(snapshot.Nodes)
	if err != nil {
		return nil, err
	}

	reservations := make([]nodeCapacityReservation, 0, len(plan.NodeShards))
	seenNodes := make(map[string]struct{}, len(plan.NodeShards))
	for index, shard := range plan.NodeShards {
		if _, exists := seenNodes[shard.NodeID]; exists {
			return nil, fmt.Errorf("packing node shard node %q is duplicated", shard.NodeID)
		}
		seenNodes[shard.NodeID] = struct{}{}
		reservation, err := reservationForShard(index, shard, snapshots)
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, reservation)
	}

	sort.Slice(reservations, func(left, right int) bool {
		return reservations[left].key.nodeID < reservations[right].key.nodeID
	})
	return reservations, nil
}

// indexReservationSnapshots 建立节点快照索引并拒绝空标识或重复节点。
func indexReservationSnapshots(nodes []NodeSnapshot) (map[string]NodeSnapshot, error) {
	snapshots := make(map[string]NodeSnapshot, len(nodes))
	for index, node := range nodes {
		if node.NodeID == "" {
			return nil, fmt.Errorf("snapshot node at index %d has an empty node id", index)
		}
		if _, exists := snapshots[node.NodeID]; exists {
			return nil, fmt.Errorf("snapshot node %q is duplicated", node.NodeID)
		}
		snapshots[node.NodeID] = node
	}
	return snapshots, nil
}

// reservationForShard 校验节点状态并构造该节点的整次运行预留。
func reservationForShard(index int, shard NodeShard, snapshots map[string]NodeSnapshot) (nodeCapacityReservation, error) {
	if shard.NodeID == "" {
		return nodeCapacityReservation{}, fmt.Errorf("packing node shard at index %d has an empty node id", index)
	}
	node, exists := snapshots[shard.NodeID]
	if !exists {
		return nodeCapacityReservation{}, fmt.Errorf("packing node shard node %q is missing from snapshot", shard.NodeID)
	}
	if node.Status != NodeStatusLive {
		return nodeCapacityReservation{}, fmt.Errorf("packing node shard node %q is not live", shard.NodeID)
	}
	peak, err := reservationPeak(shard.NodeID, shard.Waves)
	if err != nil {
		return nodeCapacityReservation{}, err
	}
	return nodeCapacityReservation{
		key: nodeReservationKey{
			nodeID:        node.NodeID,
			probeIdentity: node.ProbeIdentity,
			probeBootID:   node.ProbeBootID,
		},
		capacity: peak,
	}, nil
}

// reservationPeak 计算波次资源的 componentwise 最大值并拒绝非正输入。
func reservationPeak(nodeID string, waves []ExecutionWave) (ResourceCapacity, error) {
	if len(waves) == 0 {
		return ResourceCapacity{}, fmt.Errorf("packing node shard node %q waves must not be empty", nodeID)
	}
	peak := ResourceCapacity{}
	for waveIndex, wave := range waves {
		if wave.CPUMilli <= 0 || wave.MemoryBytes <= 0 || wave.Concurrency <= 0 {
			return ResourceCapacity{}, fmt.Errorf("packing node shard node %q wave %d has non-positive resources", nodeID, waveIndex)
		}
		peak.CPUMilli = reservationMaxInt64(peak.CPUMilli, wave.CPUMilli)
		peak.MemoryBytes = reservationMaxInt64(peak.MemoryBytes, wave.MemoryBytes)
		peak.MaxConcurrency = reservationMaxInt(peak.MaxConcurrency, wave.Concurrency)
	}
	return peak, nil
}

// reservationMaxInt64 返回两个 int64 中的较大值。
func reservationMaxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

// reservationMaxInt 返回两个 int 中的较大值。
func reservationMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// checkedAddReservation 以非负资源和上限执行不会溢出且不超限的加法。
func checkedAddReservation(current, requested, limit ResourceCapacity) (ResourceCapacity, error) {
	if err := validateReservationCapacities(current, requested, limit); err != nil {
		return ResourceCapacity{}, err
	}
	cpuMilli, err := checkedReservationInt64Add(current.CPUMilli, requested.CPUMilli, "CPU")
	if err != nil {
		return ResourceCapacity{}, err
	}
	memoryBytes, err := checkedReservationInt64Add(current.MemoryBytes, requested.MemoryBytes, "memory")
	if err != nil {
		return ResourceCapacity{}, err
	}
	maxConcurrency, err := checkedReservationIntAdd(current.MaxConcurrency, requested.MaxConcurrency)
	if err != nil {
		return ResourceCapacity{}, err
	}
	result := ResourceCapacity{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, MaxConcurrency: maxConcurrency}
	if result.CPUMilli > limit.CPUMilli || result.MemoryBytes > limit.MemoryBytes || result.MaxConcurrency > limit.MaxConcurrency {
		return ResourceCapacity{}, fmt.Errorf("reservation capacity exceeds limit")
	}
	return result, nil
}

// validateReservationCapacities 校验预留加法的三组资源均为非负值。
func validateReservationCapacities(current, requested, limit ResourceCapacity) error {
	if capacityHasNegative(current) {
		return fmt.Errorf("current reservation capacity must be non-negative")
	}
	if capacityHasNegative(requested) {
		return fmt.Errorf("requested reservation capacity must be non-negative")
	}
	if capacityHasNegative(limit) {
		return fmt.Errorf("reservation capacity limit must be non-negative")
	}
	return nil
}

// capacityHasNegative 判断资源容量是否包含负数分量。
func capacityHasNegative(capacity ResourceCapacity) bool {
	return capacity.CPUMilli < 0 || capacity.MemoryBytes < 0 || capacity.MaxConcurrency < 0
}

// checkedReservationInt64Add 检查非负 int64 加法并返回和。
func checkedReservationInt64Add(current, requested int64, name string) (int64, error) {
	if current > math.MaxInt64-requested {
		return 0, fmt.Errorf("reservation %s addition overflows int64", name)
	}
	return current + requested, nil
}

// checkedReservationIntAdd 检查非负 int 加法并返回和。
func checkedReservationIntAdd(current, requested int) (int, error) {
	maxValue := int(^uint(0) >> 1)
	if current > maxValue-requested {
		return 0, fmt.Errorf("reservation concurrency addition overflows int")
	}
	return current + requested, nil
}
