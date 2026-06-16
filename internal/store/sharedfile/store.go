package sharedfile

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// Phase 3.6 / 3C 落地后，桌面端 sharedfile store 与 mcp-orch 端共用同一份
// 磁盘 source / DB 索引语义；详见 cmd/mcp-orch/store/sharedfile/store.go
// 头部注释。Config.CWD 为空时退化到 DB-only（兼容老 fixture）。

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

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// NewStoreWithConfig 创建带配置的存储。
func NewStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) Store {
	return &store{q: q, cfg: cfg}
}

// NewStoreWithConfigAndEmitter 创建带配置emitter的存储。
func NewStoreWithConfigAndEmitter(q *sqlc.Queries, cfg sharedfilefs.Config, emit func(uidto.UISharedFilesChanged)) Store {
	return &store{q: q, cfg: cfg, emitSharedFilesChanged: emit}
}

// Get 读取sharedfile存储。
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
	abs, resolveErr := s.cfg.ResolveAbs(cleaned)
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
	if !dbHit {
		return nil, platformdb.WrapStoreError(dbErr, "get", "shared_file")
	}
	return &mapped, nil
}

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	dbContent, writeErr := writeDiskAndDecideInline(s.cfg, cleaned, params.Content)
	if writeErr != nil {
		return nil, platformdb.WrapStoreError(writeErr, "upsert", "shared_file")
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:      cleaned,
		Content:   dbContent,
		UpdatedBy: params.UpdatedBy,
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

// Delete 删除sharedfile存储。
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
		abs, resolveErr := s.cfg.ResolveAbs(cleaned)
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

// List 列出sharedfile存储。
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

func writeDiskAndDecideInline(cfg sharedfilefs.Config, cleanedRel, content string) (string, error) {
	if !cfg.Enabled() {
		return content, nil
	}
	abs, resolveErr := cfg.ResolveAbs(cleanedRel)
	if resolveErr != nil {
		return "", resolveErr
	}
	// Best-effort .gitignore hygiene (Phase 3.8): ensure
	// `.agnet/shared/_internal/` is ignored. Sync.Once inside the helper
	// makes the second-and-later calls free; failures are swallowed so
	// the actual sharedfile write still proceeds — git hygiene is not a
	// correctness invariant.
	_ = sharedfilegitignore.Ensure(cfg.CWD, nil)
	if writeErr := sharedfilefs.WriteAtomic(abs, []byte(content)); writeErr != nil {
		return "", writeErr
	}
	if len(content) > cfg.ResolvedThreshold() {
		return "", nil
	}
	return content, nil
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
