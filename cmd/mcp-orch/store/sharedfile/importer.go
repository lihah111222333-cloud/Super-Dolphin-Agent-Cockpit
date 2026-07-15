package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilepath"
)

// sharedfile 导入错误分类哨兵。
var (
	ErrImportValidation     = errors.New("sharedfile: import validation failed")
	ErrImportInfrastructure = errors.New("sharedfile: import infrastructure failed")
)

type importTempFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type importSyncCloser interface {
	Sync() error
	Close() error
}

type importFileOps struct {
	createTemp func(string, string) (importTempFile, error)
	openSource func(string) (io.ReadCloser, error)
	openDir    func(string) (importSyncCloser, error)
	mkdirAll   func(string, fs.FileMode) error
	lstat      func(string) (fs.FileInfo, error)
	rename     func(string, string) error
	remove     func(string) error
}

func defaultImportFileOps() importFileOps {
	return importFileOps{
		createTemp: func(dir, pattern string) (importTempFile, error) { return os.CreateTemp(dir, pattern) },
		openSource: func(path string) (io.ReadCloser, error) { return os.Open(path) },
		openDir:    func(path string) (importSyncCloser, error) { return os.Open(path) },
		mkdirAll:   os.MkdirAll,
		lstat:      os.Lstat,
		rename:     os.Rename,
		remove:     os.Remove,
	}
}

// ImportLocalFile 校验本地源文件后复制到 sharedfile 磁盘区，并写入 DB 索引。
// DB 写失败会删除已复制目标，避免磁盘和索引长期不一致。
func (s *store) ImportLocalFile(ctx context.Context, params ImportLocalFileParams) (*SharedFile, error) {
	if s == nil || s.q == nil {
		return nil, importInfrastructure("store not configured", nil)
	}
	cleanedTarget, err := sharedfilepath.ValidateWritePath(params.TargetPath)
	if err != nil {
		return nil, importValidation("target path: " + err.Error())
	}
	if !s.cfg.Enabled() {
		return nil, importInfrastructure("disk source not configured", nil)
	}
	targetAbs, err := s.cfg.ResolveWriteAbs(cleanedTarget)
	if err != nil {
		return nil, importInfrastructure("resolve target", err)
	}
	return s.importLocalFileToTarget(ctx, params, cleanedTarget, targetAbs)
}

// importLocalFileToTarget 在目标路径已通过策略校验后完成源文件校验、复制和 DB 索引写入。
// 这里仍然在复制前确认 .gitignore 可写，避免磁盘或 DB 留下半成品。
func (s *store) importLocalFileToTarget(ctx context.Context, params ImportLocalFileParams, cleanedTarget, targetAbs string) (*SharedFile, error) {
	return s.importLocalFileToTargetWithOps(ctx, params, cleanedTarget, targetAbs, defaultImportFileOps())
}

func (s *store) importLocalFileToTargetWithOps(ctx context.Context, params ImportLocalFileParams, cleanedTarget, targetAbs string, ops importFileOps) (*SharedFile, error) {
	sourceAbs, info, err := validateImportSource(params)
	if err != nil {
		return nil, err
	}
	if err := ensureImportAllowed(params, sourceAbs, cleanedTarget, info.Size()); err != nil {
		return nil, err
	}
	if err := sharedfilegitignore.Ensure(s.cfg.CWD, nil); err != nil {
		return nil, importInfrastructure("sharedfilegitignore ensure", err)
	}
	var mapped *SharedFile
	err = s.pathLocks.WithPathLock(targetAbs, func() error {
		row, importErr := s.importLocked(ctx, params, cleanedTarget, sourceAbs, targetAbs, ops)
		if importErr != nil {
			return importErr
		}
		value := mapSharedFile(row)
		mapped = &value
		return nil
	})
	return mapped, err
}

type importStateSnapshot struct {
	index        sqlc.SharedFile
	hadIndex     bool
	hadFile      bool
	tmpPath      string
	backupPath   string
	published    bool
	indexMutated bool
}

// importLocked 在单路径锁内按 staging、DB、publish、目录持久化顺序完成导入。
func (s *store) importLocked(ctx context.Context, params ImportLocalFileParams, cleanedTarget, sourceAbs, targetAbs string, ops importFileOps) (sqlc.SharedFile, error) {
	snapshot, err := s.snapshotImportState(ctx, cleanedTarget, targetAbs, ops)
	if err != nil {
		return sqlc.SharedFile{}, err
	}
	if err := ensureImportOverwrite(targetAbs, params.Overwrite); err != nil {
		return sqlc.SharedFile{}, err
	}
	snapshot.tmpPath, err = writeImportTempFileWithOps(sourceAbs, targetAbs, params.MaxBytes, ops)
	if err != nil {
		return sqlc.SharedFile{}, err
	}
	row, err := s.upsertSharedFileIndex(ctx, sqlc.InsertSharedFileParams{
		Path: cleanedTarget, Content: "", ContentLocation: contentLocationDisk,
		UpdatedBy: importUpdatedBy(params.UpdatedBy),
	})
	snapshot.indexMutated = true
	if err != nil {
		primary := importInfrastructure("upsert DB index", wrapSharedFileError(err, "import"))
		return sqlc.SharedFile{}, errors.Join(primary, s.rollbackImport(ctx, cleanedTarget, targetAbs, snapshot, ops))
	}
	if err := publishImport(targetAbs, &snapshot, ops); err != nil {
		return sqlc.SharedFile{}, errors.Join(err, s.rollbackImport(ctx, cleanedTarget, targetAbs, snapshot, ops))
	}
	if err := syncImportDirectory(filepath.Dir(targetAbs), ops); err != nil {
		return sqlc.SharedFile{}, errors.Join(err, s.rollbackImport(ctx, cleanedTarget, targetAbs, snapshot, ops))
	}
	if snapshot.backupPath != "" {
		if err := ops.remove(snapshot.backupPath); err != nil {
			primary := importInfrastructure("remove import backup", err)
			return sqlc.SharedFile{}, errors.Join(primary, s.rollbackImport(ctx, cleanedTarget, targetAbs, snapshot, ops))
		}
		snapshot.backupPath = ""
	}
	return row, nil
}

func (s *store) snapshotImportState(ctx context.Context, cleanedTarget, targetAbs string, ops importFileOps) (importStateSnapshot, error) {
	index, hadIndex, err := s.currentSharedFileIndex(ctx, cleanedTarget)
	if err != nil {
		return importStateSnapshot{}, importInfrastructure("snapshot DB index", err)
	}
	info, err := ops.lstat(targetAbs)
	if errors.Is(err, fs.ErrNotExist) {
		return importStateSnapshot{index: index, hadIndex: hadIndex}, nil
	}
	if err != nil {
		return importStateSnapshot{}, importInfrastructure("snapshot target", err)
	}
	if !info.Mode().IsRegular() {
		return importStateSnapshot{}, importValidation("overwrite target must be a regular file")
	}
	return importStateSnapshot{index: index, hadIndex: hadIndex, hadFile: true}, nil
}

// publishImport 原子发布 staging 文件，并为已有正式文件保留可回滚备份。
func publishImport(targetAbs string, snapshot *importStateSnapshot, ops importFileOps) error {
	if snapshot == nil || snapshot.tmpPath == "" {
		return importInfrastructure("publish import", errors.New("staged import is missing"))
	}
	if snapshot.hadFile {
		backupPath := snapshot.tmpPath + ".backup"
		if err := ops.rename(targetAbs, backupPath); err != nil {
			return importInfrastructure("backup previous target", err)
		}
		snapshot.backupPath = backupPath
	}
	if err := ops.rename(snapshot.tmpPath, targetAbs); err != nil {
		return importInfrastructure("rename import temp", err)
	}
	snapshot.tmpPath = ""
	snapshot.published = true
	return nil
}

// rollbackImport 同时恢复 DB、正式文件和目录持久化状态，并保留全部补偿错误。
func (s *store) rollbackImport(ctx context.Context, cleanedTarget, targetAbs string, snapshot importStateSnapshot, ops importFileOps) error {
	var rollbackErrs []error
	if snapshot.indexMutated {
		if err := s.rollbackSharedFileIndex(ctx, cleanedTarget, snapshot.index, snapshot.hadIndex); err != nil {
			rollbackErrs = append(rollbackErrs, importInfrastructure("rollback DB index", err))
		}
	}
	if err := rollbackImportFile(targetAbs, snapshot, ops); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}
	if snapshot.tmpPath != "" || snapshot.backupPath != "" || snapshot.published {
		if err := syncImportDirectory(filepath.Dir(targetAbs), ops); err != nil {
			rollbackErrs = append(rollbackErrs, importInfrastructure("persist import rollback", err))
		}
	}
	return errors.Join(rollbackErrs...)
}

// rollbackImportFile 恢复导入前的正式文件状态，并清理本次 staging。
func rollbackImportFile(targetAbs string, snapshot importStateSnapshot, ops importFileOps) error {
	var rollbackErrs []error
	if snapshot.published {
		if err := ops.remove(targetAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			rollbackErrs = append(rollbackErrs, importInfrastructure("rollback remove published target", err))
		}
	}
	if snapshot.backupPath != "" {
		if err := ops.rename(snapshot.backupPath, targetAbs); err != nil {
			rollbackErrs = append(rollbackErrs, importInfrastructure("rollback restore previous target", err))
		}
	}
	if snapshot.tmpPath != "" {
		if err := ops.remove(snapshot.tmpPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			rollbackErrs = append(rollbackErrs, importInfrastructure("rollback remove import temp", err))
		}
	}
	return errors.Join(rollbackErrs...)
}

// validateImportSource 校验源路径存在、非 symlink、非目录且是普通文件。
func validateImportSource(params ImportLocalFileParams) (string, fs.FileInfo, error) {
	source := strings.TrimSpace(params.SourcePath)
	if source == "" {
		return "", nil, importValidation("source path is required")
	}
	sourceAbs, err := filepath.Abs(os.ExpandEnv(source))
	if err != nil {
		return "", nil, importValidation("source path: " + err.Error())
	}
	lstat, err := os.Lstat(sourceAbs)
	if err != nil {
		return "", nil, importInfrastructure("lstat source", err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		return "", nil, importValidation("source symlink not allowed")
	}
	info, err := os.Stat(sourceAbs)
	if err != nil {
		return "", nil, importInfrastructure("stat source", err)
	}
	if info.IsDir() {
		return "", nil, importValidation("source directory not allowed")
	}
	if !info.Mode().IsRegular() {
		return "", nil, importValidation("source must be a regular file")
	}
	return sourceAbs, info, nil
}

// ensureImportAllowed 校验大小、扩展名和源路径白名单。
func ensureImportAllowed(params ImportLocalFileParams, sourceAbs, targetRel string, size int64) error {
	if params.MaxBytes < 0 {
		return importValidation("max_bytes must be non-negative")
	}
	if params.MaxBytes > 0 && size > params.MaxBytes {
		return importValidation(fmt.Sprintf("source exceeds max_bytes (%d > %d)", size, params.MaxBytes))
	}
	if err := ensureImportExtension(params.AllowedExtensions, sourceAbs, targetRel); err != nil {
		return err
	}
	if err := ensureImportSourceRoot(params.AllowedSourceRoots, sourceAbs); err != nil {
		return err
	}
	return nil
}

// ensureImportExtension 要求源文件和目标路径扩展名都在允许列表内。
func ensureImportExtension(allowed []string, sourceAbs, targetRel string) error {
	if len(allowed) == 0 {
		return nil
	}
	allowedSet := map[string]struct{}{}
	for _, ext := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(ext))
		if normalized == "" {
			continue
		}
		if !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		allowedSet[normalized] = struct{}{}
	}
	if len(allowedSet) == 0 {
		return nil
	}
	sourceExt := strings.ToLower(filepath.Ext(sourceAbs))
	targetExt := strings.ToLower(filepath.Ext(targetRel))
	if _, ok := allowedSet[sourceExt]; !ok {
		return importValidation("source extension not in allowed_extensions")
	}
	if _, ok := allowedSet[targetExt]; !ok {
		return importValidation("target extension not in allowed_extensions")
	}
	return nil
}

// ensureImportSourceRoot 解析真实路径后确认源文件位于允许根目录内。
func ensureImportSourceRoot(roots []string, sourceAbs string) error {
	if len(roots) == 0 {
		return importValidation("allowed_source_roots is required")
	}
	sourceReal, err := filepath.EvalSymlinks(sourceAbs)
	if err != nil {
		return importInfrastructure("eval source path", err)
	}
	for _, root := range roots {
		rootAbs, err := filepath.Abs(os.ExpandEnv(strings.TrimSpace(root)))
		if err != nil || strings.TrimSpace(root) == "" {
			continue
		}
		rootReal, err := filepath.EvalSymlinks(rootAbs)
		if err != nil {
			continue
		}
		if pathWithinRoot(sourceReal, rootReal) {
			return nil
		}
	}
	return importValidation("source path outside allowed_source_roots")
}

// pathWithinRoot 判断清理后的路径是否位于指定根目录下。
func pathWithinRoot(absPath, root string) bool {
	cleanPath := filepath.Clean(absPath)
	cleanRoot := filepath.Clean(root)
	return cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator))
}

// ensureImportOverwrite 校验 overwrite 策略，默认 fail 防止误覆盖。
func ensureImportOverwrite(targetAbs, rawOverwrite string) error {
	overwrite := strings.TrimSpace(rawOverwrite)
	if overwrite == "" {
		overwrite = "fail"
	}
	switch overwrite {
	case "fail":
		if _, err := os.Stat(targetAbs); err == nil {
			return importValidation("overwrite fail: target already exists")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return importInfrastructure("stat target", err)
		}
		return nil
	case "replace":
		return nil
	default:
		return importValidation("overwrite must be fail or replace")
	}
}

// writeImportTempFileWithOps 流式写入同目录临时文件，并严格检查 fsync、close 和失败清理。
func writeImportTempFileWithOps(sourceAbs, targetAbs string, maxBytes int64, ops importFileOps) (string, error) {
	if err := ops.mkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		return "", importInfrastructure("mkdir target", err)
	}
	tmp, err := ops.createTemp(filepath.Dir(targetAbs), filepath.Base(targetAbs)+".tmp-")
	if err != nil {
		return "", importInfrastructure("create temp", err)
	}
	tmpPath := tmp.Name()
	copyErr := streamCopyWithLimitOps(tmp, sourceAbs, maxBytes, ops)
	var syncErr error
	if copyErr == nil {
		syncErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		primary := errors.Join(copyErr, importInfrastructureIf("fsync temp", syncErr), importInfrastructureIf("close temp", closeErr))
		return "", errors.Join(primary, removeImportPath(tmpPath, "remove failed import temp", ops))
	}
	return tmpPath, nil
}

// streamCopyWithLimitOps 在不整文件入内存的前提下复制并检查源文件关闭错误。
func streamCopyWithLimitOps(dst io.Writer, sourceAbs string, maxBytes int64, ops importFileOps) (resultErr error) {
	src, err := ops.openSource(sourceAbs)
	if err != nil {
		return importInfrastructure("open source", err)
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, importInfrastructure("close source", closeErr))
		}
	}()
	reader := io.Reader(src)
	if maxBytes > 0 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	written, err := io.Copy(dst, reader)
	if err != nil {
		return importInfrastructure("copy source", err)
	}
	if maxBytes > 0 && written > maxBytes {
		return importValidation(fmt.Sprintf("source exceeds max_bytes (%d > %d)", written, maxBytes))
	}
	return nil
}

func syncImportDirectory(dirPath string, ops importFileOps) error {
	dir, err := ops.openDir(dirPath)
	if err != nil {
		return importInfrastructure("open import directory", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(importInfrastructureIf("fsync import directory", syncErr), importInfrastructureIf("close import directory", closeErr))
}

func removeImportPath(path, reason string, ops importFileOps) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := ops.remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return importInfrastructure(reason, err)
	}
	return nil
}

func importInfrastructureIf(reason string, err error) error {
	if err == nil {
		return nil
	}
	return importInfrastructure(reason, err)
}

// importUpdatedBy 返回导入记录的 updated_by，空值使用固定系统身份。
func importUpdatedBy(raw string) string {
	if updatedBy := strings.TrimSpace(raw); updatedBy != "" {
		return updatedBy
	}
	return "sharedfile-importer"
}

type importDrift struct {
	TempPaths           []string
	MissingIndexedPaths []string
	UnindexedPaths      []string
}

// detectImportDrift 只读比对 DB 索引与 sharedfile 磁盘，不执行自动修复。
func (s *store) detectImportDrift(ctx context.Context) (importDrift, error) {
	if s == nil || s.q == nil || !s.cfg.Enabled() {
		return importDrift{}, importInfrastructure("detect import drift: store not configured", nil)
	}
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{Prefix: "", LimitCount: math.MaxInt64})
	if err != nil {
		return importDrift{}, importInfrastructure("list sharedfile indexes", err)
	}
	indexed := make(map[string]sqlc.SharedFile, len(rows))
	for _, row := range rows {
		indexed[filepath.ToSlash(filepath.Clean(row.Path))] = row
	}
	drift, err := detectMissingImportFiles(s.cfg, indexed)
	if err != nil {
		return importDrift{}, err
	}
	if err := filepath.WalkDir(s.cfg.SandboxRoot(), func(path string, entry fs.DirEntry, walkErr error) error {
		return collectImportDiskDrift(s.cfg.SandboxRoot(), path, entry, walkErr, indexed, &drift)
	}); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return importDrift{}, importInfrastructure("walk sharedfile sandbox", err)
	}
	sort.Strings(drift.TempPaths)
	sort.Strings(drift.MissingIndexedPaths)
	sort.Strings(drift.UnindexedPaths)
	return drift, nil
}

// detectMissingImportFiles 找出 DB 指向但磁盘正文缺失的 disk 索引。
func detectMissingImportFiles(cfg sharedfilefs.Config, indexed map[string]sqlc.SharedFile) (importDrift, error) {
	drift := importDrift{}
	for rel, row := range indexed {
		if row.ContentLocation != contentLocationDisk {
			continue
		}
		cleaned, err := sharedfilepath.ValidateReadPath(rel)
		if err != nil {
			return importDrift{}, importInfrastructure("validate indexed sharedfile path", err)
		}
		abs, err := cfg.ResolveAbs(cleaned)
		if err != nil {
			return importDrift{}, importInfrastructure("resolve indexed sharedfile path", err)
		}
		if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
			drift.MissingIndexedPaths = append(drift.MissingIndexedPaths, rel)
		} else if err != nil {
			return importDrift{}, importInfrastructure("stat indexed sharedfile", err)
		}
	}
	return drift, nil
}

// collectImportDiskDrift 将临时文件和无 DB 索引的正式文件归入只读漂移结果。
func collectImportDiskDrift(root, path string, entry fs.DirEntry, walkErr error, indexed map[string]sqlc.SharedFile, drift *importDrift) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if importTempPath(rel) {
		drift.TempPaths = append(drift.TempPaths, rel)
		return nil
	}
	if _, ok := indexed[rel]; !ok {
		drift.UnindexedPaths = append(drift.UnindexedPaths, rel)
	}
	return nil
}

func importTempPath(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(base, ".tmp-") || strings.HasSuffix(base, ".backup")
}

// importValidation 包装用户输入校验错误，便于上层区分可修正问题。
func importValidation(reason string) error {
	return fmt.Errorf("%w: %s", ErrImportValidation, reason)
}

// importInfrastructure 包装文件系统或 DB 等基础设施错误。
func importInfrastructure(reason string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrImportInfrastructure, reason)
	}
	return fmt.Errorf("%w: %s: %w", ErrImportInfrastructure, reason, err)
}
