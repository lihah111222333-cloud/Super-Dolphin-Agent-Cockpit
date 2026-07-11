package topologyapproval

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 描述拓扑审批 store 依赖的 sqlc 查询集合。
// 测试可替换为 fake querier，生产路径仍由 NewStore 注入完整 *sqlc.Queries。
type querier interface {
	CreateTopologyApproval(ctx context.Context, arg sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error)
	ApproveTopologyApproval(ctx context.Context, arg sqlc.ApproveTopologyApprovalParams) (int64, error)
	RejectTopologyApproval(ctx context.Context, arg sqlc.RejectTopologyApprovalParams) (int64, error)
	ListPendingTopologyApprovals(ctx context.Context) ([]sqlc.TopologyApproval, error)
}

// store 实现拓扑审批的 SQLite 持久化边界。
type store struct {
	q querier
}

// NewStore 创建 sqlc 支撑的拓扑审批 store。
// 调用方必须传入已初始化的查询器，这里不做 nil 兜底，避免审批写入时才暴露装配错误。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// Create 写入新的拓扑审批请求，并返回数据库标准化后的记录。
// ProposedArchitecture 入库前按原始 JSON 字节保存，避免审批层解释拓扑内容。
func (s *store) Create(ctx context.Context, approval TopologyApproval) (*TopologyApproval, error) {
	row, err := s.q.CreateTopologyApproval(ctx, sqlc.CreateTopologyApprovalParams{
		ID:                   approval.ID,
		RequestedBy:          approval.RequestedBy,
		Reason:               approval.Reason,
		CreatedAt:            platformdb.Millis(approval.CreatedAt),
		ExpireAt:             platformdb.Millis(approval.ExpireAt),
		ArchHash:             approval.ArchHash,
		ProposedArchitecture: string(approval.ProposedArchitecture),
	})
	if err != nil {
		return nil, wrapTopologyApprovalError(err, "create")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

// Approve 记录审批通过结果，返回受影响行数供调用方判断是否抢到待处理记录。
func (s *store) Approve(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.ApproveTopologyApproval(ctx, sqlc.ApproveTopologyApprovalParams{Reviewer: reviewer, ID: id})
	if err != nil {
		return 0, wrapTopologyApprovalError(err, "approve")
	}
	return count, nil
}

// Reject 记录审批拒绝结果，返回受影响行数供调用方判断是否抢到待处理记录。
func (s *store) Reject(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.RejectTopologyApproval(ctx, sqlc.RejectTopologyApprovalParams{Reviewer: reviewer, ID: id})
	if err != nil {
		return 0, wrapTopologyApprovalError(err, "reject")
	}
	return count, nil
}

// ListPending 列出仍处于待处理状态的审批请求，供人工审批入口刷新待办列表。
func (s *store) ListPending(ctx context.Context) ([]TopologyApproval, error) {
	rows, err := s.q.ListPendingTopologyApprovals(ctx)
	if err != nil {
		return nil, wrapTopologyApprovalError(err, "list_pending")
	}
	approvals := make([]TopologyApproval, 0, len(rows))
	for _, row := range rows {
		approvals = append(approvals, fromSQLC(row))
	}
	return approvals, nil
}

// reviewedAtPtr 将可空毫秒时间戳转为可空 time.Time。
func reviewedAtPtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := platformdb.TimeFromMillis(*ms)
	return &t
}

// fromSQLC 将 sqlc 行映射为拓扑审批 domain DTO。
func fromSQLC(row sqlc.TopologyApproval) TopologyApproval {
	return TopologyApproval{
		ID:                   row.ID,
		Status:               row.Status,
		RequestedBy:          row.RequestedBy,
		Reason:               row.Reason,
		CreatedAt:            platformdb.TimeFromMillis(row.CreatedAt),
		ExpireAt:             platformdb.TimeFromMillis(row.ExpireAt),
		ReviewedAt:           reviewedAtPtr(row.ReviewedAt),
		Reviewer:             row.Reviewer,
		ReviewNote:           row.ReviewNote,
		ArchHash:             row.ArchHash,
		ProposedArchitecture: json.RawMessage(row.ProposedArchitecture),
	}
}

// wrapTopologyApprovalError 统一给拓扑审批 store 错误补充操作名和存储名。
func wrapTopologyApprovalError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "topology_approval")
}
