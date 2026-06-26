package sharedfile

import (
	"context"
	"time"
)

// Reader 提供 shared file 的只读访问边界。
// 内部模块和 mcp-orch 共用该接口，路径校验由具体 store 实现负责。
type Reader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
}

// Upserter 按路径写入或覆盖 shared file。
// 实现可以选择同时写磁盘和数据库索引，但对调用方保持同一个 DTO。
type Upserter interface {
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
}

// Deleter 按路径删除 shared file。
// 返回删除的数据库行数；磁盘清理失败仍会作为错误返回给调用方。
type Deleter interface {
	Delete(ctx context.Context, path string) (int64, error)
}

// Store 组合 shared file 的读取、写入和删除能力。
// 该接口跨桌面端和 orchestration 端复用，不能暴露本地磁盘实现细节。
type Store interface {
	Reader
	Upserter
	Deleter
}

// UpsertParams 是 shared file 写入请求。
// Path 走平台路径校验，Content 可按配置决定是否内联写入数据库。
type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

// ListFilter 限定 shared file 列表查询。
// Prefix 用于路径前缀过滤，Limit 防止无界读取共享文件索引。
type ListFilter struct {
	Prefix string
	Limit  int32
}

// SharedFile 是 shared file 的跨模块 DTO。
// Content 可能来自数据库内联字段，也可能由 store 从磁盘读取后回填。
type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
