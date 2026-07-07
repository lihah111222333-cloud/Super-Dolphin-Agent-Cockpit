package interaction

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义 agent 间交互消息和人工 review 的持久化边界。
// 实现层负责把状态变更写入同一张交互表，调用方只通过该接口读写。
type Store interface {
	Create(ctx context.Context, interaction Interaction) (*Interaction, error)
	Get(ctx context.Context, id int64) (*Interaction, error)
	List(ctx context.Context, filter ListFilter) ([]Interaction, error)
	Review(ctx context.Context, input ReviewInput) (*Interaction, error)
}

// ListFilter 限定交互消息列表查询的线程、关键词和数量。
// 空线程表示按关键词跨线程查找，调用方应显式控制 Limit。
type ListFilter struct {
	ThreadID string
	Keyword  string
	Limit    int32
}

// ReviewInput 表示一次人工 review 对交互消息的状态更新。
// ID 定位目标行，Status/ReviewedBy/ReviewNote 一起写入审查结果。
type ReviewInput struct {
	ID         int64
	Status     string
	ReviewedBy string
	ReviewNote string
}

// Interaction 是 agent 间消息或审批请求的跨模块 DTO。
// Payload 保留原始 JSON，状态和 review 字段用于 UI 与编排层判断是否需要人工处理。
type Interaction struct {
	ID             int64
	ThreadID       string
	ParentID       *int64
	Sender         string
	Receiver       string
	MsgType        string
	Status         string
	RequiresReview bool
	ReviewedBy     string
	ReviewNote     string
	ReviewedAt     *time.Time
	Payload        json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
