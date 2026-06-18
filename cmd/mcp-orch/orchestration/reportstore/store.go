package reportstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportgc"
)

var ErrNotFound = errors.New("agent report not found")

type Record struct {
	AgentID   string
	Name      string
	Cwd       string
	Report    string
	ReportSeq int64
	UpdatedAt time.Time
}

type PersistedRecord struct {
	Report    string
	ReportSeq int64
	UpdatedAt time.Time
}

type agentReportFileCandidate struct {
	Path    string
	Name    string
	ModTime int64
}

// Persist 持久化编排。
func Persist(record Record) error {
	report := strings.TrimSpace(record.Report)
	if report == "" || strings.TrimSpace(record.Cwd) == "" {
		return nil
	}
	path, err := agentReportFilePath(record)
	if err != nil {
		return err
	}
	content, err := formatPersistedReport(record)
	if err != nil {
		return err
	}
	return writeReportFileAtomically(path, content)
}

// writeReportFileAtomically 通过同目录临时文件替换 report，避免读到半截内容。
func writeReportFileAtomically(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("persist agent report: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("persist agent report: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("persist agent report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("persist agent report: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("persist agent report: %w", err)
	}
	cleanup = false
	return nil
}

// ReadPersisted 读取persisted。
func ReadPersisted(record Record) (string, error) {
	persisted, err := ReadPersistedRecord(record)
	if err != nil {
		return "", err
	}
	return persisted.Report, nil
}

// ReadPersistedRecord 读取 report 正文和版本元数据；老纯文本 report 视为 seq=0。
func ReadPersistedRecord(record Record) (PersistedRecord, error) {
	report, err := readAgentReportFile(record)
	if err == nil {
		return report, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return PersistedRecord{}, err
	}
	fallbackPath, fallbackErr := newestAgentReportFilePath(record)
	if fallbackErr != nil {
		return PersistedRecord{}, fallbackErr
	}
	report, err = readReportFileAtPath(fallbackPath, strings.TrimSpace(record.AgentID))
	if err != nil {
		return PersistedRecord{}, err
	}
	return report, nil
}

func readAgentReportFile(record Record) (PersistedRecord, error) {
	path, err := agentReportFilePath(record)
	if err != nil {
		return PersistedRecord{}, err
	}
	return readReportFileAtPath(path, strings.TrimSpace(record.AgentID))
}

func readReportFileAtPath(path, agentID string) (PersistedRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PersistedRecord{}, fmt.Errorf("%w: %s", ErrNotFound, agentID)
		}
		return PersistedRecord{}, fmt.Errorf("read agent report: %w", err)
	}
	report, err := parsePersistedReport(raw)
	if err != nil {
		return PersistedRecord{}, fmt.Errorf("read agent report: %w", err)
	}
	if strings.TrimSpace(report.Report) == "" {
		return PersistedRecord{}, fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	return report, nil
}

func formatPersistedReport(record Record) (string, error) {
	if record.ReportSeq < 0 {
		return "", errors.New("report_seq must be non-negative")
	}
	if record.UpdatedAt.IsZero() {
		return "", errors.New("updated_at is required")
	}
	report := strings.TrimSpace(record.Report)
	updatedAt := record.UpdatedAt.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf("---\nreport_seq: %d\nupdated_at: %q\n---\n\n%s", record.ReportSeq, updatedAt, report), nil
}

// parsePersistedReport 解析单文件 report；没有元数据头时保持老纯文本兼容。
func parsePersistedReport(raw []byte) (PersistedRecord, error) {
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n"))
	if text == "" {
		return PersistedRecord{}, nil
	}
	const opening = "---\n"
	if !strings.HasPrefix(text, opening) {
		return PersistedRecord{Report: text}, nil
	}
	rest := text[len(opening):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return PersistedRecord{Report: text}, nil
	}
	header := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n---\n"):])
	metadata, ok, err := parseReportFrontMatter(header)
	if err != nil {
		return PersistedRecord{}, err
	}
	if !ok {
		return PersistedRecord{Report: text}, nil
	}
	metadata.Report = body
	return metadata, nil
}

// parseReportFrontMatter 只识别 report_seq 和 updated_at，字段损坏时直接报错阻断。
func parseReportFrontMatter(header string) (PersistedRecord, bool, error) {
	var record PersistedRecord
	foundSeq := false
	foundUpdatedAt := false
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch key {
		case "report_seq":
			foundSeq = true
			seq, err := strconv.ParseInt(value, 10, 64)
			if err != nil || seq < 0 {
				return PersistedRecord{}, true, errors.New("invalid report_seq")
			}
			record.ReportSeq = seq
		case "updated_at":
			foundUpdatedAt = true
			updatedAt, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return PersistedRecord{}, true, errors.New("invalid updated_at")
			}
			record.UpdatedAt = updatedAt
		}
	}
	if foundSeq != foundUpdatedAt {
		return PersistedRecord{}, true, errors.New("incomplete report metadata")
	}
	return record, foundSeq || foundUpdatedAt, nil
}

func newestAgentReportFilePath(record Record) (string, error) {
	dir, filenamePrefix, err := agentReportFileDirAndPrefix(record)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", agentReportReadDirError(err, strings.TrimSpace(record.AgentID))
	}
	best, err := newestAgentReportFileCandidate(dir, filenamePrefix, entries)
	if err != nil {
		return "", err
	}
	if best.Path == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(record.AgentID))
	}
	return best.Path, nil
}

func agentReportReadDirError(err error, agentID string) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	return fmt.Errorf("read agent report: %w", err)
}

func newestAgentReportFileCandidate(dir, filenamePrefix string, entries []os.DirEntry) (agentReportFileCandidate, error) {
	var best agentReportFileCandidate
	for _, entry := range entries {
		candidate, ok, err := agentReportCandidateFromDirEntry(dir, filenamePrefix, entry)
		if err != nil {
			return agentReportFileCandidate{}, err
		}
		if !ok {
			continue
		}
		if candidate.newerThan(best) {
			best = candidate
		}
	}
	return best, nil
}

func agentReportCandidateFromDirEntry(dir, filenamePrefix string, entry os.DirEntry) (agentReportFileCandidate, bool, error) {
	name := entry.Name()
	if entry.IsDir() || !strings.HasPrefix(name, filenamePrefix) {
		return agentReportFileCandidate{}, false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return agentReportFileCandidate{}, false, fmt.Errorf("read agent report: %w", err)
	}
	return agentReportFileCandidate{
		Path:    filepath.Join(dir, name),
		Name:    name,
		ModTime: info.ModTime().UnixNano(),
	}, true, nil
}

func (candidate agentReportFileCandidate) newerThan(other agentReportFileCandidate) bool {
	if other.Path == "" {
		return true
	}
	if candidate.ModTime != other.ModTime {
		return candidate.ModTime > other.ModTime
	}
	return candidate.Name > other.Name
}

func agentReportFilePath(record Record) (string, error) {
	dir, filenamePrefix, err := agentReportFileDirAndPrefix(record)
	if err != nil {
		return "", err
	}
	filename := filenamePrefix + reportgc.Sanitize(record.Name)
	return filepath.Join(dir, filename), nil
}

func agentReportFileDirAndPrefix(record Record) (string, string, error) {
	cwd := strings.TrimSpace(record.Cwd)
	agentID := strings.TrimSpace(record.AgentID)
	if cwd == "" {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	idPart := reportgc.Sanitize(agentID)
	if idPart == "" {
		return "", "", errors.New("agent id is required")
	}
	return filepath.Join(cwd, ".agnet", "report"), idPart + "+", nil
}
