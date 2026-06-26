package sharedfile

import (
	"context"
	"time"
)

// Reader 提供 sharedfile 的只读查询能力。
type Reader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
}

// ListFilter 是 sharedfile 列表查询过滤条件。
type ListFilter struct {
	Prefix string
	Limit  int32
}

// SharedFile 是共享文件索引和正文的运行时投影。
type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store 提供 sharedfile 索引和正文的读写能力。
type Store interface {
	Reader
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
	Delete(ctx context.Context, path string) (int64, error)
}

// UpsertParams 是 sharedfile 写入入参。
type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

// Importer 提供从本地文件系统导入 sharedfile 的能力。
type Importer interface {
	ImportLocalFile(ctx context.Context, params ImportLocalFileParams) (*SharedFile, error)
}

// ImportLocalFileParams 是本地文件导入 sharedfile 的校验和写入参数。
type ImportLocalFileParams struct {
	SourcePath         string
	TargetPath         string
	ContentType        string
	AllowedExtensions  []string
	AllowedSourceRoots []string
	MaxBytes           int64
	Overwrite          string
	UpdatedBy          string
}
