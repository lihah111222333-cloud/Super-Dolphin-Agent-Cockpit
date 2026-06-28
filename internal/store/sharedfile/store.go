package sharedfile

import (
	"context"
	"database/sql"
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
	pathLocks              sharedfilefs.PathLocks
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
	if !s.cfg.Enabled() {
		return s.upsertInline(ctx, cleaned, params)
	}
	return s.upsertDiskBacked(ctx, cleaned, params)
}

// upsertInline 只更新 DB 内联正文；磁盘模式关闭时没有跨介质回滚需求。
func (s *store) upsertInline(ctx context.Context, cleaned string, params UpsertParams) (*SharedFile, error) {
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            cleaned,
		Content:         params.Content,
		ContentLocation: contentLocationInline,
		UpdatedBy:       params.UpdatedBy,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	mapped := mapUpsertResult(row, params.Content)
	s.publishSharedFilesChanged("write", mapped.Path)
	return mapped, nil
}

// upsertDiskBacked 在同一路径锁内完成 staging、DB upsert 和 publish。
// 任何失败都会保留旧正式正文；publish 失败会把 DB 索引恢复到写入前快照。
func (s *store) upsertDiskBacked(ctx context.Context, cleaned string, params UpsertParams) (*SharedFile, error) {
	abs, resolveErr := s.cfg.ResolveWriteAbs(cleaned)
	if resolveErr != nil {
		return nil, platformdb.WrapStoreError(resolveErr, "upsert", "shared_file")
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
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	s.publishSharedFilesChanged("write", mapped.Path)
	return mapped, nil
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

// currentSharedFileIndex 读取写入前的 DB 索引快照。
// 只有 publish 失败需要用它回滚，缺行不是错误，其他 DB 错误必须阻断写入。
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

// rollbackSharedFileIndex 撤销 publish 失败前已经提交的 DB 索引。
// 这一步让调用方收到错误时不会留下“DB 指向新版本、磁盘仍是旧版本”的半提交状态。
func (s *store) rollbackSharedFileIndex(ctx context.Context, cleaned string, previous sqlc.SharedFile, hadPrevious bool) error {
	if !hadPrevious {
		_, err := s.q.DeleteSharedFile(ctx, sqlc.DeleteSharedFileParams{Path: cleaned})
		return err
	}
	path := strings.TrimSpace(previous.Path)
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

// Delete 删除指定路径的 shared file。
// 磁盘模式先 staged rename 正文，DB 删除成功后才最终清理 tombstone，任何失败都返回错误。
func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	if !s.cfg.Enabled() {
		return s.deleteInline(ctx, cleaned)
	}
	return s.deleteDiskBacked(ctx, cleaned)
}

func (s *store) deleteInline(ctx context.Context, cleaned string) (int64, error) {
	count, err := s.deleteSharedFileIndex(ctx, cleaned)
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	if count > 0 {
		s.publishSharedFilesChanged("delete", cleaned)
	}
	return count, nil
}

// deleteDiskBacked 在同一路径锁内先 stage 磁盘正文，再删除 DB 索引。
// 磁盘 stage/commit 失败时 DB 行保持可见，DB 删除失败时会恢复 staged 正文。
func (s *store) deleteDiskBacked(ctx context.Context, cleaned string) (int64, error) {
	abs, resolveErr := s.cfg.ResolveDeleteAbs(cleaned)
	if resolveErr != nil {
		return 0, platformdb.WrapStoreError(resolveErr, "delete", "shared_file")
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
		return count, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	if count > 0 {
		s.publishSharedFilesChanged("delete", cleaned)
	}
	return count, nil
}

func (s *store) deleteSharedFileIndex(ctx context.Context, cleaned string) (int64, error) {
	return s.q.DeleteSharedFile(ctx, sqlc.DeleteSharedFileParams{Path: cleaned})
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

func fromSQLCRow(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt: platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func mapUpsertResult(row sqlc.SharedFile, content string) *SharedFile {
	mapped := fromSQLCRow(row)
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
