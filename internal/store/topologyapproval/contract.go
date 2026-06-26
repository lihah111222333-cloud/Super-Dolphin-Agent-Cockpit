package topologyapproval

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义拓扑变更审批的持久化接口。
// 审批状态由数据库抢占，Approve/Reject 返回受影响行数供调用方判断是否抢到待处理记录。
type Store interface {
	// Create 写入待审批拓扑变更，并返回数据库补齐时间和状态后的记录。
	Create(ctx context.Context, approval TopologyApproval) (*TopologyApproval, error)
	// Approve 将待处理记录标记为通过；返回 0 行通常表示已被别人处理或不存在。
	Approve(ctx context.Context, reviewer, id string) (int64, error)
	// Reject 将待处理记录标记为拒绝；返回 0 行通常表示已被别人处理或不存在。
	Reject(ctx context.Context, reviewer, id string) (int64, error)
	// ListPending 只返回仍等待人工处理的拓扑变更。
	ListPending(ctx context.Context) ([]TopologyApproval, error)
}

// TopologyApproval 表示一次待人工确认的拓扑架构变更请求。
// ProposedArchitecture 以原始 JSON 保存，避免审批层绑定具体前端或 DAG 拓扑版本。
type TopologyApproval struct {
	ID                   string
	Status               string
	RequestedBy          string
	Reason               string
	CreatedAt            time.Time
	ExpireAt             time.Time
	ReviewedAt           *time.Time
	Reviewer             string
	ReviewNote           string
	ArchHash             string
	ProposedArchitecture json.RawMessage
}
