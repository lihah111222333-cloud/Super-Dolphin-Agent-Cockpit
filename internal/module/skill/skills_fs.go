package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *service) ListSkills(context.Context) ([]SkillInfo, error) {
	records, err := s.scanSkills()
	if err != nil {
		return nil, err
	}
	skills := make([]SkillInfo, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.info)
	}
	return skills, nil
}

func (s *service) ReadLocal(_ context.Context, path string) (any, error) {
	path, err := s.resolveProjectPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is directory: %s", path)
	}
	if info.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill": map[string]any{"path": path, "content": string(data), "summary": summarizeSkillBody(string(data), ""), "summary_source": "generated"}}, nil
}

func (s *service) ListLocalFiles(_ context.Context, p listSkillFilesParams) (any, error) {
	dir := strings.TrimSpace(p.Dir)
	if dir == "" && strings.TrimSpace(p.Path) != "" {
		dir = filepath.Dir(strings.TrimSpace(p.Path))
	}
	if dir == "" {
		return nil, errors.New("dir or path is required")
	}
	dir, err := s.resolveProjectPath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := listSkillFiles(dir, entries)
	return map[string]any{"dir": dir, "files": files}, nil
}

func (s *service) WriteLocal(_ context.Context, path, content string) (any, error) {
	path, err := s.resolveProjectPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is directory: %s", path)
	}
	if len(content) > maxSkillFileBytes {
		return nil, fmt.Errorf("content too large: %d bytes", len(content))
	}
	if err := os.WriteFile(path, []byte(content), info.Mode().Perm()); err != nil {
		return nil, err
	}
	s.publishSkillsChanged("local_write", filepath.Base(filepath.Dir(path)))
	return map[string]any{"ok": true, "path": path, "bytes": len(content)}, nil
}

func (s *service) ImportLocalDir(_ context.Context, p importSkillDirParams) (any, error) {
	sources, err := collectImportSources(p.Path, p.Paths)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, errors.New("path or paths is required")
	}
	if len(sources) > 1 && strings.TrimSpace(p.Name) != "" {
		return nil, errors.New("name is only supported for single directory import")
	}
	results, failures := s.importSources(sources, p.Name)
	response := map[string]any{"requested": len(sources), "imported": results}
	if len(failures) > 0 {
		response["failures"] = failures
	}
	if len(results) == 1 {
		response["skill"] = results[0]
	}
	if len(results) > 0 {
		name := strings.TrimSpace(p.Name)
		if name == "" && len(results) == 1 {
			name, _ = results[0]["name"].(string)
		}
		s.publishSkillsChanged("import_dir", name)
	}
	return response, nil
}

func (s *service) DeleteLocal(_ context.Context, name string) (any, error) {
	record, err := s.resolveSkill(name)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(record.info.Dir); err != nil {
		return nil, err
	}
	s.publishSkillsChanged("delete_local", record.info.Name)
	return map[string]any{"ok": true, "name": record.info.Name, "dir": record.info.Dir, "removed_agent_bindings": 0}, nil
}

func (s *service) ReadRemote(ctx context.Context, url string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(url), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("fetch remote skill failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillFileBytes))
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill": map[string]any{"url": url, "content": string(body)}}, nil
}

func (s *service) WriteRemote(_ context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	path, err := s.writeSkill(name, content)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged("remote_write", name)
	return map[string]any{"ok": true, "path": path}, nil
}

func (s *service) ReadConfig(_ context.Context, agentID string) (any, error) {
	// TODO(P7): replace this placeholder response with persisted agent-scoped skill bindings when the config storage contract exists.
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent_id is required")
	}
	return map[string]any{
		"agent_id":       agentID,
		"skills":         []string{},
		"session_bound":  false,
		"configured":     false,
		"binding_count":  0,
		"binding_source": "stub",
	}, nil
}

func (s *service) WriteSkillContent(_ context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	path, err := s.writeSkill(name, content)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged("config_write", name)
	return map[string]any{"ok": true, "path": path}, nil
}

func (s *service) WriteSummary(_ context.Context, name, summary string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	path, resolvedName, err := s.updateSkillSummary(name, summary)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged("summary_write", resolvedName)
	return map[string]any{"ok": true, "path": path, "name": resolvedName, "summary": strings.TrimSpace(summary)}, nil
}

func listSkillFiles(dir string, entries []os.DirEntry) []map[string]any {
	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, map[string]any{"name": entry.Name(), "path": filepath.Join(dir, entry.Name()), "size": info.Size(), "is_main": strings.EqualFold(entry.Name(), skillMainFile)})
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]["name"].(string)) < strings.ToLower(files[j]["name"].(string))
	})
	return files
}

func collectImportSources(primary string, extra []string) ([]string, error) {
	raw := append([]string{primary}, extra...)
	sources := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if matches, err := filepath.Glob(item); err == nil && len(matches) > 0 {
			sources = append(sources, matches...)
			continue
		}
		sources = append(sources, item)
	}
	return uniqStrings(sources), nil
}

func (s *service) importSources(sources []string, singleName string) ([]map[string]any, []map[string]any) {
	results := make([]map[string]any, 0, len(sources))
	failures := make([]map[string]any, 0)
	for _, source := range sources {
		name := singleName
		if len(sources) > 1 {
			name = filepath.Base(source)
		}
		result, err := s.importSource(source, name)
		if err != nil {
			failures = append(failures, map[string]any{"source": source, "error": err.Error()})
			continue
		}
		results = append(results, result)
	}
	return results, failures
}

func (s *service) importSource(source, name string) (map[string]any, error) {
	source, err := s.resolveProjectPath(source)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", source)
	}
	targetName := strings.TrimSpace(name)
	if targetName == "" {
		targetName = filepath.Base(source)
	}
	root := strings.TrimSpace(s.root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	targetDir := filepath.Join(root, skillSlug(targetName))
	if _, err := os.Stat(targetDir); err == nil {
		return nil, fmt.Errorf("skill already exists: %s", targetName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files, bytes, err := copySkillDir(source, targetDir)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": targetName, "dir": targetDir, "skill_file": filepath.Join(targetDir, skillMainFile), "source": source, "files": files, "bytes": bytes}, nil
}

func copySkillDir(source, target string) (int, int64, error) {
	files, total := 0, int64(0)
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.Mkdir(target, 0o755)
		}
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files, total = files+1, total+int64(len(data))
		return os.WriteFile(dst, data, 0o644)
	})
	return files, total, err
}

func (s *service) resolveProjectPath(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("path is required")
	}
	root := strings.TrimSpace(s.projectRoot)
	if root == "" {
		return "", errors.New("project root is not configured")
	}
	rootPath, err := canonicalProjectPath(root)
	if err != nil {
		return "", err
	}
	targetPath, err := canonicalProjectPath(target)
	if err != nil {
		return "", err
	}
	outside, err := pathEscapesRoot(rootPath, targetPath)
	if err != nil {
		return "", err
	}
	if outside {
		return "", fmt.Errorf("path escapes project root: %s", target)
	}
	return targetPath, nil
}

func canonicalProjectPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return resolveExistingPath(absolutePath)
}

func resolveExistingPath(path string) (string, error) {
	resolvedPath, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		return resolvedPath, nil
	case errors.Is(err, os.ErrNotExist):
		return path, nil
	default:
		return "", err
	}
}

func pathEscapesRoot(rootPath, targetPath string) (bool, error) {
	relativePath, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return false, err
	}
	if relativePath == ".." {
		return true, nil
	}
	return strings.HasPrefix(relativePath, ".."+string(filepath.Separator)), nil
}
