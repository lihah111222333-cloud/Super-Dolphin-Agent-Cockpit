package orchestration

import (
	"encoding/json"
)

// Phase 3.5 / 3B · 节点失败重试策略
//
// dispatcher 在 launch 失败后判断「再 retry 还是直接 fail」时，必须能从 DAG
// metadata 拿到 default_retry / fail_fast，以及 node 级 execution.retry 覆盖。
// 把解析逻辑独立出来：
//   - 结构化字段（DAGSchedulePolicy / NodeExecutionPolicy）就地用 omitempty
//     反序列化，缺字段就走默认值，不抛错；
//   - 公开 ResolveRetryPolicy(dagMetadata, nodeConfig) → RetryPolicy 给调用
//     方使用；store 层 FailNodeAndCancelDownstream 不依赖此函数（它只接受
//     最终的 fail_fast 布尔），但 dispatcher / RPC 层接通时会经它派生。
//
// SQL 层 RetryTaskDagWakeup 仍保留 attempt_count<8 硬上限作为 paranoid 保护，
// 即使 default_retry 配得比 8 还大，也只能跑到 8。该上限和本策略并不冲突：
// 本策略给的是「软上限」，SQL 给的是「不可越过的物理上限」。

// RetryPolicy 是 dispatcher 派生出的最终决策参数。
type RetryPolicy struct {
	// MaxAttempts 是包含首发的总尝试次数。default_retry=0 → MaxAttempts=1
	// (只跑一次即终态)；default_retry=2 → MaxAttempts=3。MaxAttempts<1 视
	// 同 1，避免 0 导致永远 fail 走不通。
	MaxAttempts int
	// FailFast 决定节点 failed 后是否级联取消下游 pending 节点。来自
	// metadata.schedule.fail_fast；node 层暂无覆盖（execution.on_failure
	// 是节点级 retry/skip 策略，不是图级中断；后续 gate 再处理）。
	FailFast bool
}

// DAGSchedulePolicy 对应 DAG metadata 内 `schedule` 子对象的策略字段子集。
// 与 cmd/mcp-orch/tools/task_tools.go::DAGScheduleInput 对齐，但只取本步用
// 得到的两项；新增字段不影响反序列化。
type DAGSchedulePolicy struct {
	DefaultRetry int  `json:"default_retry,omitempty"`
	FailFast     bool `json:"fail_fast,omitempty"`
}

// dagMetadataPolicy 是 DAG metadata 的最外层（仅取 schedule 子树）。
type dagMetadataPolicy struct {
	Schedule DAGSchedulePolicy `json:"schedule"`
}

// NodeExecutionPolicy 对应 node config 内 `execution` 子对象的策略字段子集。
// HasRetry 显式区分「未设置」和「设置为 0」，以便覆盖 DAG 默认值。
type NodeExecutionPolicy struct {
	Retry    int
	HasRetry bool
}

// nodeExecutionEnvelope 是 task_dag_node.config 的 schema：execution 在
// 一个嵌套 key 下；执行时的 retry 字段允许显式 0（表示「不重试」），所以
// 用 *int 而非 int 解码，再翻成 NodeExecutionPolicy.HasRetry。
type nodeExecutionEnvelope struct {
	Execution struct {
		Retry *int `json:"retry,omitempty"`
	} `json:"execution"`
}

// ResolveRetryPolicy 综合 DAG metadata + node config 解出最终 RetryPolicy。
// 解析失败的字段安静走默认值（DefaultRetry=0 / FailFast=false / 无 node 覆
// 盖），不返回 error：dispatcher 不应因为元数据 JSON 异常就把任务卡死，
// 应当退化到「不再重试」让节点尽快终态。
func ResolveRetryPolicy(dagMetadata, nodeConfig json.RawMessage) RetryPolicy {
	dagPolicy := decodeDAGSchedulePolicy(dagMetadata)
	nodePolicy := decodeNodeExecutionPolicy(nodeConfig)
	retryCount := dagPolicy.DefaultRetry
	if nodePolicy.HasRetry {
		retryCount = nodePolicy.Retry
	}
	maxAttempts := retryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return RetryPolicy{MaxAttempts: maxAttempts, FailFast: dagPolicy.FailFast}
}

func decodeDAGSchedulePolicy(raw json.RawMessage) DAGSchedulePolicy {
	if len(raw) == 0 {
		return DAGSchedulePolicy{}
	}
	var envelope dagMetadataPolicy
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DAGSchedulePolicy{}
	}
	return envelope.Schedule
}

func decodeNodeExecutionPolicy(raw json.RawMessage) NodeExecutionPolicy {
	if len(raw) == 0 {
		return NodeExecutionPolicy{}
	}
	var envelope nodeExecutionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return NodeExecutionPolicy{}
	}
	if envelope.Execution.Retry == nil {
		return NodeExecutionPolicy{}
	}
	return NodeExecutionPolicy{Retry: *envelope.Execution.Retry, HasRetry: true}
}
