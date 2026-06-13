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
	traceDirPerm  os.FileMode = 0o700
	traceFilePerm os.FileMode = 0o600
	bytesPerMB                = 1024 * 1024
)

var traceJSONLNamePattern = regexp.MustCompile(`^trace-.+\.jsonl$`)

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

// NewJSONLSink 创建JSONLsink。
func NewJSONLSink(project string, cfg Config) (*JSONLSink, error) {
	dir, err := TraceDirectory(project)
	if err != nil {
		return nil, err
	}
	return NewJSONLSinkInDir(dir, cfg)
}

// NewJSONLSinkInDir 在目录创建JSONLsink。
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

// TraceDirectory 处理tracedirectory。
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

// Append 追加平台observability。
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

// Close 关闭平台observability资源。
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

// Stats 处理stats。
func (s *JSONLSink) Stats() SinkStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ApplyRetention 应用retention。
func (s *JSONLSink) ApplyRetention(now time.Time) error {
	return ApplyTraceRetention(s.dir, s.cfg, now)
}

func (s *JSONLSink) recordWriteError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.WriteErrors++
}

// rotateLocked 处理rotatelocked。
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

func tracePathForDate(dir, date string) string {
	return filepath.Join(dir, "trace-"+date+".jsonl")
}

func rotatedTracePath(dir, date string, now time.Time) string {
	stamp := now.Format("150405.000000000")
	return filepath.Join(dir, fmt.Sprintf("trace-%s-%s.jsonl", date, stamp))
}

func ensureTraceDirectory(dir string) error {
	if err := os.MkdirAll(dir, traceDirPerm); err != nil {
		return fmt.Errorf("create trace directory %s: %w", dir, err)
	}
	return chmodOwnerOnly(dir, traceDirPerm)
}

func chmodOwnerOnly(path string, perm os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("set owner-only permissions on %s: %w", path, err)
	}
	return nil
}

// ApplyTraceRetention 应用traceretention。
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

func traceRetentionTotalBytes(files []traceRetentionFile) int64 {
	var total int64
	for _, file := range files {
		total += file.size
	}
	return total
}

func sortTraceRetentionFiles(files []traceRetentionFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].path < files[j].path
		}
		return files[i].modTime.Before(files[j].modTime)
	})
}

func removeTraceFile(path, reason string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s trace file %s: %w", reason, path, err)
	}
	return nil
}

type traceRetentionFile struct {
	path    string
	modTime time.Time
	size    int64
}

func isTraceJSONLName(name string) bool {
	return traceJSONLNamePattern.MatchString(name)
}
