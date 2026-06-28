package sharedfile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	previous, hadPrevious, previousErr := s.currentSharedFileIndex(ctx, cleaned)
	if previousErr != nil {
		return nil, platformdb.WrapStoreError(previousErr, "upsert", "shared_file")
	}
	dbContent, contentLocation, staged, writeErr := s.stageDiskAndDecideInline(cleaned, params.Content)
	if writeErr != nil {
		return nil, platformdb.WrapStoreError(writeErr, "upsert", "shared_file")
	}
	if staged != nil {
		defer staged.cleanup()
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
	if publishErr := s.publishStagedDiskWrite(ctx, cleaned, staged, previous, hadPrevious); publishErr != nil {
		return nil, platformdb.WrapStoreError(publishErr, "upsert", "shared_file")
	}
	mapped := fromSQLCRow(row)
	if mapped.Content == "" && len(params.Content) > 0 {
		mapped.Content = params.Content
	}
	s.publishSharedFilesChanged("write", mapped.Path)
	return &mapped, nil
}

// publishStagedDiskWrite 在 DB upsert 成功后发布磁盘正文。
// publish 失败会先回滚 DB 索引再返回错误，避免调用方看到半提交的 shared file。
func (s *store) publishStagedDiskWrite(ctx context.Context, cleaned string, staged *stagedDiskWrite, previous sqlc.SharedFile, hadPrevious bool) error {
	if staged == nil {
		return nil
	}
	publishErr := staged.publish()
	if publishErr == nil {
		return nil
	}
	if rollbackErr := s.rollbackSharedFileIndex(ctx, cleaned, previous, hadPrevious); rollbackErr != nil {
		return fmt.Errorf("%w; rollback shared file index failed: %v", publishErr, rollbackErr)
	}
	return publishErr
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

type stagedDiskWrite struct {
	finalAbs string
	tempAbs  string
}

// stageDiskAndDecideInline 先把正文写入同目录临时文件，并返回 DB 应保存的正文和位置标记。
// 调用方必须在 DB upsert 成功后 publish；失败路径只 cleanup，不能覆盖已有正式文件。
func (s *store) stageDiskAndDecideInline(cleanedRel, content string) (string, string, *stagedDiskWrite, error) {
	if !s.cfg.Enabled() {
		return content, contentLocationInline, nil, nil
	}
	abs, resolveErr := s.cfg.ResolveWriteAbs(cleanedRel)
	if resolveErr != nil {
		return "", "", nil, resolveErr
	}
	// .gitignore 维护是旁路卫生检查，不参与 shared file 写入正确性；
	// helper 内部用 Sync.Once 保证重复调用成本很低，失败也不能阻断正文落盘。
	_ = sharedfilegitignore.Ensure(s.cfg.CWD, nil)
	if removed, cleanupErr := cleanupStagedTemps(abs); cleanupErr != nil {
		return "", "", nil, cleanupErr
	} else if removed > 0 {
		s.publishSharedFilesChanged("write-conflict", cleanedRel)
	}
	tempAbs, writeErr := writeStagedDiskContent(abs, []byte(content))
	if writeErr != nil {
		return "", "", nil, writeErr
	}
	staged := &stagedDiskWrite{finalAbs: abs, tempAbs: tempAbs}
	if len(content) > s.cfg.ResolvedThreshold() {
		return "", contentLocationDisk, staged, nil
	}
	return content, contentLocationInline, staged, nil
}

// writeStagedDiskContent 写入并 fsync staging temp，但不 rename 到最终路径。
// temp 文件与最终文件同目录，保证后续 publish 仍是同文件系统内的原子 rename。
func writeStagedDiskContent(absPath string, data []byte) (string, error) {
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("sharedfilefs: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(absPath)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("sharedfilefs: open staged tmp: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
	}()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sharedfilefs: write staged tmp: %w", writeErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sharedfilefs: fsync staged tmp: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		closed = true
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sharedfilefs: close staged tmp: %w", closeErr)
	}
	closed = true
	return tmpPath, nil
}

func cleanupStagedTemps(absPath string) (int, error) {
	matches, err := filepath.Glob(absPath + ".*.tmp")
	if err != nil {
		return 0, fmt.Errorf("sharedfilefs: glob staged tmp: %w", err)
	}
	removed := 0
	for _, match := range matches {
		if err := os.Remove(match); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("sharedfilefs: remove staged tmp %s: %w", match, err)
		}
		removed++
	}
	return removed, nil
}

// publish 在 DB 索引写入成功后才把 staged 正文原子发布到最终路径。
// 若 DB 失败，调用方只需 cleanup，旧正文不会被覆盖。
func (w *stagedDiskWrite) publish() error {
	if w == nil || w.tempAbs == "" {
		return nil
	}
	if err := os.Rename(w.tempAbs, w.finalAbs); err != nil {
		return fmt.Errorf("sharedfilefs: publish staged %s → %s: %w", w.tempAbs, w.finalAbs, err)
	}
	w.tempAbs = ""
	if dirHandle, openErr := os.Open(filepath.Dir(w.finalAbs)); openErr == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

func (w *stagedDiskWrite) cleanup() {
	if w != nil && w.tempAbs != "" {
		_ = os.Remove(w.tempAbs)
		w.tempAbs = ""
	}
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
