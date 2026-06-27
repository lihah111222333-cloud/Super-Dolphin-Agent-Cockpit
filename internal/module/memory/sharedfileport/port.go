// Package sharedfileport 定义 memory 模块消费 shared file 持久化能力的窄端口。
package sharedfileport

import (
	"context"
	"time"
)

// Reader 是 memory 模块读取 shared file 索引和内容的窄端口。
// 具体 store 负责路径校验和磁盘/数据库来源选择，模块侧只消费稳定 DTO。
type Reader interface {
	Get(ctx context.Context, path string) (*File, error)
	List(ctx context.Context, filter ListFilter) ([]File, error)
}

// Deleter 是 memory 模块删除 shared file 的窄端口。
type Deleter interface {
	Delete(ctx context.Context, path string) (int64, error)
}

// ListFilter 限定 shared file 列表查询范围。
type ListFilter struct {
	Prefix string
	Limit  int32
}

// File 是 shared file store 暴露给 memory 模块的稳定视图。
// Content 可能由 store 从磁盘回填，调用方不能据此推断真实落盘位置。
type File struct {
	Path      string
	Content   string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
