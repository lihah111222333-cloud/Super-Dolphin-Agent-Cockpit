package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// traceDirPerm 和 traceFilePerm 约束 trace 落盘只能被当前用户读取。
	traceDirPerm  os.FileMode = 0o700
	traceFilePerm os.FileMode = 0o600
	bytesPerMB                = 1024 * 1024
)

// traceJSONLNamePattern 只匹配本模块生成的 trace JSONL 文件，避免 retention 删除旁路文件。
var traceJSONLNamePattern = regexp.MustCompile(`^trace-.+\.jsonl$`)

// JSONLSink 将脱敏后的 trace event 按日期写入 JSONL 文件。
// 文件句柄、大小和统计信息都由 mu 保护，调用方必须通过 Append/Close 进入。
type JSONLSink struct {
	mu        sync.Mutex
	cfg       Config
	sanitizer Sanitizer
	now       func() time.Time
	dir       string
	file      *os.File
	filePath  string
	fileDate  string
	fileSize  int64
	stats     SinkStats
}

// NewJSONLSink 根据项目名解析默认 trace 目录并创建 JSONL sink。
func NewJSONLSink(project string, cfg Config) (*JSONLSink, error) {
	dir, err := TraceDirectory(project)
	if err != nil {
		return nil, err
	}
	return NewJSONLSinkInDir(dir, cfg)
}

// NewJSONLSinkInDir 在指定目录创建 JSONL sink，并确保目录权限限制为当前用户。
func NewJSONLSinkInDir(dir string, cfg Config) (*JSONLSink, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("observability trace directory is empty")
	}
	if err := ensureTraceDirectory(dir); err != nil {
		return nil, err
	}
	return &JSONLSink{cfg: cfg, sanitizer: NewSanitizer(cfg), now: time.Now, dir: dir}, nil
}

// TraceDirectory 根据项目名返回 trace 文件目录。
// project 必须是单个路径段，避免调用方把任意路径拼进用户日志目录。
func TraceDirectory(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", errors.New("observability trace project is empty")
	}
	if filepath.Base(project) != project || project == "." || project == string(filepath.Separator) {
		return "", fmt.Errorf("observability trace project %q must be a single path element", project)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for trace directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve user home for trace directory: empty home")
	}
	return filepath.Join(home, ".multi-agent", "log", project, "traces"), nil
}

// Append 脱敏并追加一条 trace event。
// 调用方 context 已取消时立即失败；写入失败会增加 stats 并返回原始错误。
func (s *JSONLSink) Append(ctx context.Context, event TraceEvent) error {
	if ctx == nil {
		return errors.New("observability trace append context is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	event = s.sanitizer.SanitizeEvent(event)
	data, err := MarshalSanitizedJSON(event)
	if err != nil {
		s.recordWriteError()
		return fmt.Errorf("marshal trace event: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rotateLocked(int64(len(data))); err != nil {
		s.stats.WriteErrors++
		return err
	}
	if _, err := s.file.Write(data); err != nil {
		s.stats.WriteErrors++
		return fmt.Errorf("append trace event: %w", err)
	}
	s.fileSize += int64(len(data))
	s.stats.EventsWritten++
	return nil
}

// Close 关闭当前打开的 trace 文件，之后下一次 Append 会按需重新打开。
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

// Stats 返回当前 sink 的写入统计快照。
func (s *JSONLSink) Stats() SinkStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ApplyRetention 对 sink 所在目录应用 trace 文件保留策略。
func (s *JSONLSink) ApplyRetention(now time.Time) error {
	return ApplyTraceRetention(s.dir, s.cfg, now)
}

// recordWriteError 在编码阶段失败时记录写入错误计数。
func (s *JSONLSink) recordWriteError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.WriteErrors++
}

// rotateLocked 在持锁状态下按日期或大小切换 trace 文件。
// 调用方必须持有 s.mu，确保 fileSize 与 filePath 不会被并发写入打乱。
func (s *JSONLSink) rotateLocked(nextWriteBytes int64) error {
	now := s.now()
	date := now.Format("2006-01-02")
	path := tracePathForDate(s.dir, date)
	if s.file == nil {
		return s.openLocked(path, date)
	}
	if s.fileDate != date {
		if err := s.closeLocked(); err != nil {
			return err
		}
		return s.openLocked(path, date)
	}
	maxBytes := int64(s.cfg.JSONLMaxFileMB) * bytesPerMB
	if maxBytes > 0 && s.fileSize > 0 && s.fileSize+nextWriteBytes > maxBytes {
		if err := s.closeLocked(); err != nil {
			return err
		}
		rotated := rotatedTracePath(s.dir, date, now)
		if err := os.Rename(path, rotated); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rotate trace file %s: %w", path, err)
		}
		return s.openLocked(path, date)
	}
	return nil
}

// openLocked 在持锁状态下打开或创建当前 trace 文件，并刷新文件元数据。
func (s *JSONLSink) openLocked(path, date string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, traceFilePerm)
	if err != nil {
		return fmt.Errorf("open trace file %s: %w", path, err)
	}
	if err := chmodOwnerOnly(path, traceFilePerm); err != nil {
		_ = file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat trace file %s: %w", path, err)
	}
	s.file = file
	s.filePath = path
	s.fileDate = date
	s.fileSize = info.Size()
	return nil
}

// closeLocked 在持锁状态下关闭当前文件并清空文件状态。
func (s *JSONLSink) closeLocked() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.filePath = ""
	s.fileDate = ""
	s.fileSize = 0
	if err != nil {
		return fmt.Errorf("close trace file: %w", err)
	}
	return nil
}

// tracePathForDate 返回某个日期对应的主 trace JSONL 路径。
func tracePathForDate(dir, date string) string {
	return filepath.Join(dir, "trace-"+date+".jsonl")
}

// rotatedTracePath 为同一天的超大 trace 文件生成带时间戳的轮转路径。
func rotatedTracePath(dir, date string, now time.Time) string {
	stamp := now.Format("150405.000000000")
	return filepath.Join(dir, fmt.Sprintf("trace-%s-%s.jsonl", date, stamp))
}

// ensureTraceDirectory 创建 trace 目录并收紧目录权限。
func ensureTraceDirectory(dir string) error {
	if err := os.MkdirAll(dir, traceDirPerm); err != nil {
		return fmt.Errorf("create trace directory %s: %w", dir, err)
	}
	return chmodOwnerOnly(dir, traceDirPerm)
}

// chmodOwnerOnly 在支持 chmod 的平台上限制文件或目录仅当前用户可访问。
func chmodOwnerOnly(path string, perm os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("set owner-only permissions on %s: %w", path, err)
	}
	return nil
}

// ApplyTraceRetention 删除过期 trace 文件，并按总大小继续裁剪最旧文件。
// dir 必须指向项目 traces 目录，防止保留策略误删非 trace 数据。
func ApplyTraceRetention(dir string, cfg Config, now time.Time) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("observability trace retention directory is empty")
	}
	if filepath.Base(filepath.Clean(dir)) != "traces" {
		return fmt.Errorf("observability trace retention directory %s must be the project traces directory", dir)
	}
	files, err := collectRetainedTraceFiles(dir, now.Add(-time.Duration(cfg.JSONLRetentionDays)*24*time.Hour))
	if err != nil {
		return err
	}
	return pruneTraceFilesToMaxBytes(files, int64(cfg.JSONLRetentionMaxMB)*bytesPerMB)
}

// collectRetainedTraceFiles 删除过期文件，并返回仍需参与大小裁剪的 trace 文件。
func collectRetainedTraceFiles(dir string, cutoff time.Time) ([]traceRetentionFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read trace retention directory %s: %w", dir, err)
	}
	files := make([]traceRetentionFile, 0, len(entries))
	for _, entry := range entries {
		file, keep, err := retentionFileForEntry(dir, entry, cutoff)
		if err != nil {
			return nil, err
		}
		if keep {
			files = append(files, file)
		}
	}
	return files, nil
}

// retentionFileForEntry 判断单个目录项是否是需要保留或删除的 trace 文件。
func retentionFileForEntry(dir string, entry os.DirEntry, cutoff time.Time) (traceRetentionFile, bool, error) {
	if entry.IsDir() || !isTraceJSONLName(entry.Name()) {
		return traceRetentionFile{}, false, nil
	}
	path := filepath.Join(dir, entry.Name())
	info, err := entry.Info()
	if err != nil {
		return traceRetentionFile{}, false, fmt.Errorf("stat trace retention file %s: %w", path, err)
	}
	if info.ModTime().Before(cutoff) {
		return traceRetentionFile{}, false, removeTraceFile(path, "expired")
	}
	return traceRetentionFile{path: path, modTime: info.ModTime(), size: info.Size()}, true, nil
}

// pruneTraceFilesToMaxBytes 按修改时间从旧到新删除文件，直到总大小不超过上限。
func pruneTraceFilesToMaxBytes(files []traceRetentionFile, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	total := traceRetentionTotalBytes(files)
	sortTraceRetentionFiles(files)
	for _, file := range files {
		if total <= maxBytes {
			return nil
		}
		if err := removeTraceFile(file.path, "oversized-retention"); err != nil {
			return err
		}
		total -= file.size
	}
	return nil
}

// traceRetentionTotalBytes 统计保留候选文件的总字节数。
func traceRetentionTotalBytes(files []traceRetentionFile) int64 {
	var total int64
	for _, file := range files {
		total += file.size
	}
	return total
}

// sortTraceRetentionFiles 按修改时间排序，时间相同则按路径稳定排序。
func sortTraceRetentionFiles(files []traceRetentionFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].path < files[j].path
		}
		return files[i].modTime.Before(files[j].modTime)
	})
}

// removeTraceFile 删除 trace 文件，并把删除原因写入错误上下文。
func removeTraceFile(path, reason string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s trace file %s: %w", reason, path, err)
	}
	return nil
}

// traceRetentionFile 是 retention 阶段使用的文件元数据快照。
type traceRetentionFile struct {
	path    string
	modTime time.Time
	size    int64
}

// isTraceJSONLName 判断文件名是否符合本模块 trace JSONL 命名。
func isTraceJSONLName(name string) bool {
	return traceJSONLNamePattern.MatchString(name)
}
