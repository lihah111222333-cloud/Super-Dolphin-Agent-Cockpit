package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// sharedfile 导入错误分类哨兵。
var (
	ErrImportValidation     = errors.New("sharedfile: import validation failed")
	ErrImportInfrastructure = errors.New("sharedfile: import infrastructure failed")
)

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
	sourceAbs, info, err := validateImportSource(params)
	if err != nil {
		return nil, err
	}
	if err := ensureImportAllowed(params, sourceAbs, cleanedTarget, info.Size()); err != nil {
		return nil, err
	}
	_ = sharedfilegitignore.Ensure(s.cfg.CWD, nil)
	if err := copyImportToTarget(sourceAbs, targetAbs, params); err != nil {
		return nil, err
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:            cleanedTarget,
		Content:         "",
		ContentLocation: contentLocationDisk,
		UpdatedBy:       importUpdatedBy(params.UpdatedBy),
	})
	if err != nil {
		_ = os.Remove(targetAbs)
		return nil, importInfrastructure("upsert DB index", wrapSharedFileError(err, "import"))
	}
	mapped := mapSharedFile(row)
	return &mapped, nil
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

// copyImportToTarget 通过同目录临时文件复制导入内容，再 rename 到目标路径。
func copyImportToTarget(sourceAbs, targetAbs string, params ImportLocalFileParams) error {
	if err := ensureImportOverwrite(targetAbs, params.Overwrite); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		return importInfrastructure("mkdir target", err)
	}
	tmpPath, err := writeImportTempFile(sourceAbs, targetAbs, params.MaxBytes)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetAbs); err != nil {
		_ = os.Remove(tmpPath)
		return importInfrastructure("rename temp", err)
	}
	if dir, err := os.Open(filepath.Dir(targetAbs)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
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

// writeImportTempFile 写入并 fsync 临时文件；返回后由调用方负责 rename。
func writeImportTempFile(sourceAbs, targetAbs string, maxBytes int64) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(targetAbs), filepath.Base(targetAbs)+".tmp-")
	if err != nil {
		return "", importInfrastructure("create temp", err)
	}
	tmpPath := tmp.Name()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := streamCopyWithLimit(tmp, sourceAbs, maxBytes); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", importInfrastructure("fsync temp", err)
	}
	if err := tmp.Close(); err != nil {
		return "", importInfrastructure("close temp", err)
	}
	keepTmp = true
	return tmpPath, nil
}

// streamCopyWithLimit 以流式复制源文件，并在超过 maxBytes 时返回 validation 错误。
func streamCopyWithLimit(dst *os.File, sourceAbs string, maxBytes int64) error {
	src, err := os.Open(sourceAbs)
	if err != nil {
		return importInfrastructure("open source", err)
	}
	defer func() { _ = src.Close() }()
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

// importUpdatedBy 返回导入记录的 updated_by，空值使用固定系统身份。
func importUpdatedBy(raw string) string {
	if updatedBy := strings.TrimSpace(raw); updatedBy != "" {
		return updatedBy
	}
	return "sharedfile-importer"
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
