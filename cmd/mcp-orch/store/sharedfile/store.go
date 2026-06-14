package sharedfile

import (
	"context"
	"errors"
	"io/fs"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// Phase 3.6 / 3C 落地后，store 同时维护磁盘正文（source of truth）+ DB 索引
// （updated_by / 时间戳；正文超过 cfg.InlineThresholdBytes 时 DB content 为
// 空字符串）。Config.CWD 为空时退化到 DB-only 模式（兼容老测试与未注入
// platformconfig 的调用方）。

type store struct {
	q   *sqlc.Queries
	cfg sharedfilefs.Config
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return newStoreWithConfig(q, sharedfilefs.Config{}) }

// NewStoreWithConfig 创建带配置的存储。
func NewStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) Store {
	return newStoreWithConfig(q, cfg)
}

func newStoreWithConfig(q *sqlc.Queries, cfg sharedfilefs.Config) *store {
	return &store{q: q, cfg: cfg}
}

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	dbContent, writeErr := writeDiskAndDecideInline(s.cfg, cleaned, params.Content)
	if writeErr != nil {
		return nil, wrapSharedFileError(writeErr, "upsert")
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:      cleaned,
		Content:   dbContent,
		UpdatedBy: params.UpdatedBy,
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	mapped := mapSharedFile(row)
	// Caller expects to read back the content they wrote (regardless of
	// whether it landed in DB inline). Restore from the in-memory copy so
	// large files don't surface as empty strings.
	if mapped.Content == "" && len(params.Content) > 0 {
		mapped.Content = params.Content
	}
	return &mapped, nil
}

// Get 读取编排。
func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return nil, wrapSharedFileError(err, "get")
	}
	row, dbErr := s.q.GetSharedFile(ctx, cleaned)
	mapped := SharedFile{}
	dbHit := dbErr == nil
	if dbHit {
		mapped = mapSharedFile(row)
	} else if !errors.Is(dbErr, platformdb.ErrNotFound) {
		return nil, wrapSharedFileError(dbErr, "get")
	}
	if !s.cfg.Enabled() {
		if !dbHit {
			return nil, wrapSharedFileError(dbErr, "get")
		}
		return &mapped, nil
	}
	abs, resolveErr := s.cfg.ResolveAbs(cleaned)
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
	if !dbHit {
		return nil, wrapSharedFileError(dbErr, "get")
	}
	return &mapped, nil
}

// List 列出编排。
func (s *store) List(ctx context.Context, filter ListFilter) ([]SharedFile, error) {
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{
		Column1: filter.Prefix,
		Limit:   filter.Limit,
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

// Delete 删除编排。
func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	count, err := s.q.DeleteSharedFile(ctx, cleaned)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	if s.cfg.Enabled() {
		abs, resolveErr := s.cfg.ResolveAbs(cleaned)
		if resolveErr != nil {
			return count, wrapSharedFileError(resolveErr, "delete")
		}
		if removeErr := sharedfilefs.RemoveDisk(abs); removeErr != nil {
			return count, wrapSharedFileError(removeErr, "delete")
		}
	}
	return count, nil
}

// writeDiskAndDecideInline writes the content to disk (when cfg enabled) and
// returns the value that should land in DB. Files larger than the inline
// threshold get an empty string in DB; the disk file is the canonical
// source. When cfg is disabled the function is a no-op and returns the
// original content so legacy callers still have inline DB rows.
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

func mapSharedFile(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: sqlc.TimeValue(row.CreatedAt),
		UpdatedAt: sqlc.TimeValue(row.UpdatedAt),
	}
}

func wrapSharedFileError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "shared_file")
}
