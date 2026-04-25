package orchestration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

var errAgentReportNotFound = errors.New("agent report not found")

type agentReportFileRecord struct {
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

func agentReportFileRecordFromRuntime(agent *agentRuntime) agentReportFileRecord {
	if agent == nil {
		return agentReportFileRecord{}
	}
	return agentReportFileRecord{
		AgentID: agent.id,
		Name:    agent.name,
		Cwd:     agent.cwd,
		Report:  agent.lastReport,
	}
}

func agentReportFileRecordFromSnapshot(snapshot AgentSnapshot) agentReportFileRecord {
	return agentReportFileRecord{
		AgentID: snapshot.AgentID,
		Name:    snapshot.Name,
		Cwd:     snapshot.Cwd,
		Report:  snapshot.LastReport,
	}
}

func persistAgentReportFile(record agentReportFileRecord) error {
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

func readPersistedAgentReportFile(record agentReportFileRecord) (string, error) {
	report, err := readAgentReportFile(record)
	if err == nil {
		return report, nil
	}
	if !errors.Is(err, errAgentReportNotFound) {
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

func readAgentReportFile(record agentReportFileRecord) (string, error) {
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
			return "", fmt.Errorf("%w: %s", errAgentReportNotFound, agentID)
		}
		return "", fmt.Errorf("read agent report: %w", err)
	}
	report := strings.TrimSpace(string(raw))
	if report == "" {
		return "", fmt.Errorf("%w: %s", errAgentReportNotFound, agentID)
	}
	return report, nil
}

func newestAgentReportFilePath(record agentReportFileRecord) (string, error) {
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
		return "", fmt.Errorf("%w: %s", errAgentReportNotFound, strings.TrimSpace(record.AgentID))
	}
	return best.Path, nil
}

func agentReportReadDirError(err error, agentID string) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", errAgentReportNotFound, agentID)
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

func agentReportFilePath(record agentReportFileRecord) (string, error) {
	dir, filenamePrefix, err := agentReportFileDirAndPrefix(record)
	if err != nil {
		return "", err
	}
	filename := filenamePrefix + sanitizeAgentReportFilenamePart(record.Name)
	return filepath.Join(dir, filename), nil
}

func agentReportFileDirAndPrefix(record agentReportFileRecord) (string, string, error) {
	cwd := strings.TrimSpace(record.Cwd)
	agentID := strings.TrimSpace(record.AgentID)
	if cwd == "" {
		return "", "", fmt.Errorf("%w: %s", errAgentReportNotFound, agentID)
	}
	idPart := sanitizeAgentReportFilenamePart(agentID)
	if idPart == "" {
		return "", "", errors.New("agent id is required")
	}
	return filepath.Join(cwd, ".agnet", "report"), idPart + "+", nil
}

func sanitizeAgentReportFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		builder.WriteRune(r)
		lastUnderscore = false
	}
	return strings.TrimSpace(builder.String())
}
