package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// 本 store 统一维护 shared file 的数据库索引和可选磁盘正文。
// Config.CWD 为空时只读写数据库，便于单元测试；启用磁盘模式时路径必须先通过 sharedfilepath 校验。

const (
	contentLocationInline = "inline"
	contentLocationDisk   = "disk"
)

type querier interface {
	GetSharedFile(ctx context.Context, arg sqlc.GetSharedFileParams) (sqlc.SharedFile, error)
	ListSharedFiles(ctx context.Context, arg sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error)
	DeleteSharedFile(ctx context.Context, arg sqlc.DeleteSharedFileParams) (int64, error)
	UpsertSharedFile(ctx context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error)
}

type store struct {
	q                      querier
	cfg                    sharedfilefs.Config
	emitSharedFilesChanged func(uidto.UISharedFilesChanged)
}

// NewStore 创建只使用数据库内联内容的 shared file 存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// NewStoreWithConfig 创建带磁盘正文配置的 shared file 存储。
// cfg 未启用时会退回数据库内联模式，不额外访问文件系统。
func NewStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) Store {
	return &store{q: q, cfg: cfg}
}

// NewStoreWithConfigAndEmitter 创建带 UI 变更通知的 shared file 存储。
// 写入或删除成功后会发布变更事件，供前端刷新共享文件列表。
func NewStoreWithConfigAndEmitter(q *sqlc.Queries, cfg sharedfilefs.Config, emit func(uidto.UISharedFilesChanged)) Store {
	return &store{q: q, cfg: cfg, emitSharedFilesChanged: emit}
}

// Get 读取指定路径的 shared file。
// 启用磁盘模式时根据 DB 标记选择正文来源；disk 行缺正文必须报错，避免返回空内容。
func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "get", "shared_file")
	}
	row, dbErr := s.q.GetSharedFile(ctx, sqlc.GetSharedFileParams{Path: cleaned})
	mapped := SharedFile{}
	dbHit := dbErr == nil
	if dbHit {
		mapped = fromSQLCRow(row)
	} else if !errors.Is(dbErr, platformdb.ErrNotFound) {
		return nil, platformdb.WrapStoreError(dbErr, "get", "shared_file")
	}
	if !s.cfg.Enabled() {
		if !dbHit {
			return nil, platformdb.WrapStoreError(dbErr, "get", "shared_file")
		}
		return &mapped, nil
	}
	if !dbHit {
		return nil, platformdb.WrapStoreError(dbErr, "get", "shared_file")
	}
	if row.ContentLocation == contentLocationInline {
		return &mapped, nil
	}
	return s.diskBackedSharedFile(cleaned, mapped, row.ContentLocation)
}

// Upsert 写入 shared file 并更新数据库索引。
// 大文件正文只写磁盘，数据库保留路径和元数据；小文件会同时内联保存便于快速读取。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	dbContent, contentLocation, writeErr := writeDiskAndDecideInline(s.cfg, cleaned, params.Content)
	if writeErr != nil {
		return nil, platformdb.WrapStoreError(writeErr, "upsert", "shared_file")
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            cleaned,
		Content:         dbContent,
		ContentLocation: contentLocation,
		UpdatedBy:       params.UpdatedBy,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	mapped := fromSQLCRow(row)
	if mapped.Content == "" && len(params.Content) > 0 {
		mapped.Content = params.Content
	}
	s.publishSharedFilesChanged("write", mapped.Path)
	return &mapped, nil
}

// Delete 删除指定路径的 shared file。
// 数据库索引先删除；启用磁盘模式时再删除正文文件，并把磁盘错误返回给调用方。
func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	count, err := s.q.DeleteSharedFile(ctx, sqlc.DeleteSharedFileParams{Path: cleaned})
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	if s.cfg.Enabled() {
		abs, resolveErr := s.cfg.ResolveDeleteAbs(cleaned)
		if resolveErr != nil {
			return count, platformdb.WrapStoreError(resolveErr, "delete", "shared_file")
		}
		if removeErr := sharedfilefs.RemoveDisk(abs); removeErr != nil {
			return count, platformdb.WrapStoreError(removeErr, "delete", "shared_file")
		}
	}
	if count > 0 {
		s.publishSharedFilesChanged("delete", cleaned)
	}
	return count, nil
}

// List 按前缀列出 shared file 索引。
// 列表只返回数据库索引内容，不读取磁盘正文，避免列表页触发大量文件 IO。
func (s *store) List(ctx context.Context, filter ListFilter) ([]SharedFile, error) {
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{
		Prefix:     filter.Prefix,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "shared_file")
	}
	files := make([]SharedFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, fromSQLCListRow(row))
	}
	return files, nil
}

func (s *store) diskBackedSharedFile(cleaned string, mapped SharedFile, contentLocation string) (*SharedFile, error) {
	if contentLocation != contentLocationDisk {
		err := fmt.Errorf("invalid content_location %q for %q", contentLocation, cleaned)
		return nil, platformdb.WrapStoreError(err, "get", "shared_file")
	}
	abs, resolveErr := s.cfg.ResolveReadAbs(cleaned)
	if resolveErr != nil {
		return nil, platformdb.WrapStoreError(resolveErr, "get", "shared_file")
	}
	data, _, readErr := sharedfilefs.ReadDisk(abs)
	if readErr == nil {
		mapped.Path = cleaned
		mapped.Content = string(data)
		return &mapped, nil
	}
	if !errors.Is(readErr, fs.ErrNotExist) {
		return nil, platformdb.WrapStoreError(readErr, "get", "shared_file")
	}
	err := fmt.Errorf("disk content %q missing: %w", cleaned, readErr)
	return nil, platformdb.WrapStoreError(err, "get", "shared_file")
}

func writeDiskAndDecideInline(cfg sharedfilefs.Config, cleanedRel, content string) (string, string, error) {
	if !cfg.Enabled() {
		return content, contentLocationInline, nil
	}
	abs, resolveErr := cfg.ResolveWriteAbs(cleanedRel)
	if resolveErr != nil {
		return "", "", resolveErr
	}
	// .gitignore 维护是旁路卫生检查，不参与 shared file 写入正确性；
	// helper 内部用 Sync.Once 保证重复调用成本很低，失败也不能阻断正文落盘。
	_ = sharedfilegitignore.Ensure(cfg.CWD, nil)
	if writeErr := sharedfilefs.WriteAtomic(abs, []byte(content)); writeErr != nil {
		return "", "", writeErr
	}
	if len(content) > cfg.ResolvedThreshold() {
		return "", contentLocationDisk, nil
	}
	return content, contentLocationInline, nil
}

func fromSQLCRow(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt: platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func fromSQLCListRow(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt: platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func (s *store) publishSharedFilesChanged(action, path string) {
	if s == nil || s.emitSharedFilesChanged == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	s.emitSharedFilesChanged(uidto.UISharedFilesChanged{
		EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
		Path:        path,
		Action:      strings.TrimSpace(action),
	})
}
