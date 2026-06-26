package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// sharedfile store 同时维护磁盘正文和 DB 索引。
// 磁盘配置启用时正文以磁盘为准，DB 保存索引和可内联的小正文；Config.CWD 为空时保持 DB-only 兼容。

// sharedfile 正文位置常量，对应 shared_files.content_location CHECK 约束。
const (
	contentLocationInline = "inline"
	contentLocationDisk   = "disk"
)

type store struct {
	q   *sqlc.Queries
	cfg sharedfilefs.Config
}

// NewStore 创建 DB-only sharedfile 存储，供未注入磁盘配置的旧路径使用。
func NewStore(q *sqlc.Queries) Store { return newStoreWithConfig(q, sharedfilefs.Config{}) }

// NewStoreWithConfig 创建带磁盘正文配置的 sharedfile 存储。
func NewStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) Store {
	return newStoreWithConfig(q, cfg)
}

// newStoreWithConfig 创建具体 store，保留给 provider wiring 和测试复用。
func newStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) *store {
	return &store{q: q, cfg: cfg}
}

// Upsert 写入 sharedfile；磁盘启用时正文先落磁盘，DB 只保留索引和可内联的小正文。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	dbContent, contentLocation, writeErr := writeDiskAndDecideInline(s.cfg, cleaned, params.Content)
	if writeErr != nil {
		return nil, wrapSharedFileError(writeErr, "upsert")
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            cleaned,
		Content:         dbContent,
		ContentLocation: contentLocation,
		UpdatedBy:       params.UpdatedBy,
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	mapped := mapSharedFile(row)
	// Upsert 的返回值需要携带调用方刚写入的正文；大文件只落磁盘时，用内存副本补回响应内容。
	if mapped.Content == "" && len(params.Content) > 0 {
		mapped.Content = params.Content
	}
	return &mapped, nil
}

// Get 读取 sharedfile；inline 记录以 DB content 为准，disk 记录必须读到磁盘正文。
func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return nil, wrapSharedFileError(err, "get")
	}
	row, dbErr := s.q.GetSharedFile(ctx, sqlc.GetSharedFileParams{Path: cleaned})
	mapped := SharedFile{}
	dbHit := dbErr == nil
	if dbHit {
		mapped = mapSharedFile(row)
	} else if !errors.Is(dbErr, platformdb.ErrNotFound) {
		return nil, wrapSharedFileError(dbErr, "get")
	}
	if !s.cfg.Enabled() {
		return dbSharedFile(mapped, dbHit, dbErr)
	}
	if !dbHit {
		return nil, wrapSharedFileError(dbErr, "get")
	}
	if row.ContentLocation == contentLocationInline {
		return &mapped, nil
	}
	return s.diskBackedSharedFile(cleaned, mapped, row.ContentLocation)
}

// dbSharedFile 返回 DB-only 模式下的 sharedfile 记录。
func dbSharedFile(mapped SharedFile, dbHit bool, dbErr error) (*SharedFile, error) {
	if !dbHit {
		return nil, wrapSharedFileError(dbErr, "get")
	}
	return &mapped, nil
}

// diskBackedSharedFile 读取 disk 位置正文；正文缺失时直接报错，不能回退 DB 空串。
func (s *store) diskBackedSharedFile(cleaned string, mapped SharedFile, contentLocation string) (*SharedFile, error) {
	if contentLocation != contentLocationDisk {
		return nil, wrapSharedFileError(fmt.Errorf("invalid content_location %q for %q", contentLocation, cleaned), "get")
	}
	abs, resolveErr := s.cfg.ResolveReadAbs(cleaned)
	if resolveErr != nil {
		return nil, wrapSharedFileError(resolveErr, "get")
	}
	data, _, readErr := sharedfilefs.ReadDisk(abs)
	if readErr == nil {
		mapped.Path = cleaned
		mapped.Content = string(data)
		return &mapped, nil
	}
	if !errors.Is(readErr, fs.ErrNotExist) {
		return nil, wrapSharedFileError(readErr, "get")
	}
	return nil, wrapSharedFileError(fmt.Errorf("disk content %q missing: %w", cleaned, readErr), "get")
}

// List 按前缀列出 sharedfile 索引，不读取磁盘正文。
func (s *store) List(ctx context.Context, filter ListFilter) ([]SharedFile, error) {
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{
		Prefix:     filter.Prefix,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "list")
	}
	result := make([]SharedFile, len(rows))
	for i, row := range rows {
		result[i] = mapSharedFile(row)
	}
	return result, nil
}

// Delete 删除 sharedfile DB 索引，并在磁盘模式下同步删除正文文件。
func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	count, err := s.q.DeleteSharedFile(ctx, sqlc.DeleteSharedFileParams{Path: cleaned})
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	if s.cfg.Enabled() {
		abs, resolveErr := s.cfg.ResolveDeleteAbs(cleaned)
		if resolveErr != nil {
			return count, wrapSharedFileError(resolveErr, "delete")
		}
		if removeErr := sharedfilefs.RemoveDisk(abs); removeErr != nil {
			return count, wrapSharedFileError(removeErr, "delete")
		}
	}
	return count, nil
}

// writeDiskAndDecideInline 在磁盘模式下先写正文，再决定 DB content 和 content_location。
// 超过阈值时 DB content 留空且标记 disk；未启用磁盘配置时保持 inline 行为。
func writeDiskAndDecideInline(cfg sharedfilefs.Config, cleanedRel, content string) (string, string, error) {
	if !cfg.Enabled() {
		return content, contentLocationInline, nil
	}
	abs, resolveErr := cfg.ResolveWriteAbs(cleanedRel)
	if resolveErr != nil {
		return "", "", resolveErr
	}
	// .gitignore 维护不参与正文正确性：失败不阻断写入，helper 内部用 Sync.Once 降低重复成本。
	_ = sharedfilegitignore.Ensure(cfg.CWD, nil)
	if writeErr := sharedfilefs.WriteAtomic(abs, []byte(content)); writeErr != nil {
		return "", "", writeErr
	}
	if len(content) > cfg.ResolvedThreshold() {
		return "", contentLocationDisk, nil
	}
	return content, contentLocationInline, nil
}

// mapSharedFile 将 sqlc 行映射为 sharedfile DTO。
func mapSharedFile(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: sqlc.TimeValue(row.CreatedAt),
		UpdatedAt: sqlc.TimeValue(row.UpdatedAt),
	}
}

// wrapSharedFileError 统一 sharedfile store 错误域。
func wrapSharedFileError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "shared_file")
}
