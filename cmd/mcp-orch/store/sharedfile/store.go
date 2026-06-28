package sharedfile

import (
	"context"
	"database/sql"
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
	q         *sqlc.Queries
	cfg       sharedfilefs.Config
	pathLocks sharedfilefs.PathLocks
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

// Upsert 写入 sharedfile；磁盘启用时先 staging，DB 成功后才发布正文。
func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	if !s.cfg.Enabled() {
		return s.upsertInline(ctx, cleaned, params)
	}
	return s.upsertDiskBacked(ctx, cleaned, params)
}

func (s *store) upsertInline(ctx context.Context, cleaned string, params UpsertParams) (*SharedFile, error) {
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            cleaned,
		Content:         params.Content,
		ContentLocation: contentLocationInline,
		UpdatedBy:       params.UpdatedBy,
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	return mapUpsertResult(row, params.Content), nil
}

// upsertDiskBacked 串行化同一路径的 staging、DB upsert、publish 和回滚。
// DB upsert 失败只删除本次 temp，不覆盖旧正式正文；publish 失败会恢复 DB 快照。
func (s *store) upsertDiskBacked(ctx context.Context, cleaned string, params UpsertParams) (*SharedFile, error) {
	abs, resolveErr := s.cfg.ResolveWriteAbs(cleaned)
	if resolveErr != nil {
		return nil, wrapSharedFileError(resolveErr, "upsert")
	}
	_ = sharedfilegitignore.Ensure(s.cfg.CWD, nil)
	var mapped *SharedFile
	err := s.pathLocks.WithPathLock(abs, func() error {
		previous, hadPrevious, previousErr := s.currentSharedFileIndex(ctx, cleaned)
		if previousErr != nil {
			return previousErr
		}
		staged, stageErr := sharedfilefs.StageWrite(abs, []byte(params.Content))
		if stageErr != nil {
			return stageErr
		}
		row, upsertErr := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
			Path:            cleaned,
			Content:         dbContentFor(params.Content, s.cfg),
			ContentLocation: contentLocationFor(params.Content, s.cfg),
			UpdatedBy:       params.UpdatedBy,
		})
		if upsertErr != nil {
			return cleanupStagedWriteAfterError(staged, upsertErr)
		}
		if publishErr := staged.Publish(); publishErr != nil {
			rollbackErr := s.rollbackSharedFileIndex(ctx, cleaned, previous, hadPrevious)
			cleanupErr := staged.Cleanup()
			return combineStagedWriteError(publishErr, rollbackErr, cleanupErr)
		}
		mapped = mapUpsertResult(row, params.Content)
		return nil
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	return mapped, nil
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

// Delete 删除 sharedfile；磁盘模式先 stage 正文，DB 删除成功后才最终删除 tombstone。
func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	if !s.cfg.Enabled() {
		return s.deleteInline(ctx, cleaned)
	}
	return s.deleteDiskBacked(ctx, cleaned)
}

func (s *store) deleteInline(ctx context.Context, cleaned string) (int64, error) {
	count, err := s.deleteSharedFileIndex(ctx, cleaned)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	return count, nil
}

// deleteDiskBacked 保证磁盘删除失败时 DB 索引仍保留，DB 删除失败时正文回到原路径。
func (s *store) deleteDiskBacked(ctx context.Context, cleaned string) (int64, error) {
	abs, resolveErr := s.cfg.ResolveDeleteAbs(cleaned)
	if resolveErr != nil {
		return 0, wrapSharedFileError(resolveErr, "delete")
	}
	var count int64
	err := s.pathLocks.WithPathLock(abs, func() error {
		previous, hadPrevious, previousErr := s.currentSharedFileIndex(ctx, cleaned)
		if previousErr != nil {
			return previousErr
		}
		staged, stageErr := sharedfilefs.StageDelete(abs)
		if stageErr != nil {
			return stageErr
		}
		deleted, deleteErr := s.deleteSharedFileIndex(ctx, cleaned)
		count = deleted
		if deleteErr != nil {
			return rollbackStagedDeleteAfterError(staged, deleteErr)
		}
		if commitErr := staged.Commit(); commitErr != nil {
			restoreErr := staged.Rollback()
			var rollbackErr error
			if hadPrevious {
				rollbackErr = s.rollbackSharedFileIndex(ctx, cleaned, previous, true)
			}
			return combineStagedDeleteError(commitErr, restoreErr, rollbackErr)
		}
		return nil
	})
	if err != nil {
		return count, wrapSharedFileError(err, "delete")
	}
	return count, nil
}

func (s *store) currentSharedFileIndex(ctx context.Context, cleaned string) (sqlc.SharedFile, bool, error) {
	row, err := s.q.GetSharedFile(ctx, sqlc.GetSharedFileParams{Path: cleaned})
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, platformdb.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return sqlc.SharedFile{}, false, nil
	}
	return sqlc.SharedFile{}, false, err
}

func (s *store) rollbackSharedFileIndex(ctx context.Context, cleaned string, previous sqlc.SharedFile, hadPrevious bool) error {
	if !hadPrevious {
		_, err := s.deleteSharedFileIndex(ctx, cleaned)
		return err
	}
	path := previous.Path
	if path == "" {
		path = cleaned
	}
	_, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            path,
		Content:         previous.Content,
		ContentLocation: previous.ContentLocation,
		UpdatedBy:       previous.UpdatedBy,
	})
	return err
}

func (s *store) deleteSharedFileIndex(ctx context.Context, cleaned string) (int64, error) {
	return s.q.DeleteSharedFile(ctx, sqlc.DeleteSharedFileParams{Path: cleaned})
}

func dbContentFor(content string, cfg sharedfilefs.Config) string {
	if len(content) > cfg.ResolvedThreshold() {
		return ""
	}
	return content
}

func contentLocationFor(content string, cfg sharedfilefs.Config) string {
	if len(content) > cfg.ResolvedThreshold() {
		return contentLocationDisk
	}
	return contentLocationInline
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

func mapUpsertResult(row sqlc.SharedFile, content string) *SharedFile {
	mapped := mapSharedFile(row)
	if mapped.Content == "" && len(content) > 0 {
		mapped.Content = content
	}
	return &mapped
}

func cleanupStagedWriteAfterError(staged *sharedfilefs.StagedWrite, cause error) error {
	if cleanupErr := staged.Cleanup(); cleanupErr != nil {
		return fmt.Errorf("%w; cleanup staged write failed: %v", cause, cleanupErr)
	}
	return cause
}

func rollbackStagedDeleteAfterError(staged *sharedfilefs.StagedDelete, cause error) error {
	if rollbackErr := staged.Rollback(); rollbackErr != nil {
		return fmt.Errorf("%w; rollback staged delete failed: %v", cause, rollbackErr)
	}
	return cause
}

func combineStagedWriteError(publishErr, rollbackErr, cleanupErr error) error {
	err := publishErr
	if rollbackErr != nil {
		err = fmt.Errorf("%w; rollback shared file index failed: %v", err, rollbackErr)
	}
	if cleanupErr != nil {
		err = fmt.Errorf("%w; cleanup staged write failed: %v", err, cleanupErr)
	}
	return err
}

func combineStagedDeleteError(commitErr, restoreErr, rollbackErr error) error {
	err := commitErr
	if restoreErr != nil {
		err = fmt.Errorf("%w; rollback staged delete failed: %v", err, restoreErr)
	}
	if rollbackErr != nil {
		err = fmt.Errorf("%w; rollback shared file index failed: %v", err, rollbackErr)
	}
	return err
}

// wrapSharedFileError 统一 sharedfile store 错误域。
func wrapSharedFileError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "shared_file")
}
