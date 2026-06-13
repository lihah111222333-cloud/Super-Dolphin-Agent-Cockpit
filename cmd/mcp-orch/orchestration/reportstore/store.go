package reportstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportgc"
)

var ErrNotFound = errors.New("agent report not found")

type Record struct {
	AgentID string
	Name    string
	Cwd     string
	Report  string
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
	if _, err := tmp.WriteString(report); err != nil {
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
	report, err := readAgentReportFile(record)
	if err == nil {
		return report, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	fallbackPath, fallbackErr := newestAgentReportFilePath(record)
	if fallbackErr != nil {
		return "", fallbackErr
	}
	report, err = readReportFileAtPath(fallbackPath, strings.TrimSpace(record.AgentID))
	if err != nil {
		return "", err
	}
	return report, nil
}

func readAgentReportFile(record Record) (string, error) {
	path, err := agentReportFilePath(record)
	if err != nil {
		return "", err
	}
	return readReportFileAtPath(path, strings.TrimSpace(record.AgentID))
}

func readReportFileAtPath(path, agentID string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, agentID)
		}
		return "", fmt.Errorf("read agent report: %w", err)
	}
	report := strings.TrimSpace(string(raw))
	if report == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	return report, nil
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
