package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var errInvalidSkillExpandRequest = errors.New("invalid skill expand request")

type skillExpandSection struct{ responseSection, anchor, resourcePath string }

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

func (s *service) Expand(ctx context.Context, p SkillExpandParams) (SkillExpandResult, error) {
	record, err := s.findSkillRecordByName(p.Name)
	if err != nil {
		return SkillExpandResult{}, normalizeExpandLookupError(err)
	}
	maxBytes, err := resolveExpandMaxBytesParam(p.MaxBytes)
	if err != nil {
		return SkillExpandResult{}, err
	}
	section, err := normalizeExpandSection(p.Section)
	if err != nil {
		return SkillExpandResult{}, err
	}
	approvalMeta, err := s.ensureExpandApproved(ctx, record, p)
	if err != nil {
		return SkillExpandResult{}, err
	}
	var result SkillExpandResult
	if section.resourcePath != "" {
		result, err = s.expandSkillResource(record, section, maxBytes)
		if err != nil {
			return SkillExpandResult{}, err
		}
		return withExpandApprovalMeta(result, approvalMeta), nil
	}
	result, err = s.expandSkillBody(record, section, maxBytes)
	if err != nil {
		return SkillExpandResult{}, err
	}
	return withExpandApprovalMeta(result, approvalMeta), nil
}

func wrapInvalidExpand(err error) error {
	if err == nil {
		return errInvalidSkillExpandRequest
	}
	return fmt.Errorf("%w: %v", errInvalidSkillExpandRequest, err)
}

func resolveExpandMaxBytesParam(raw *int64) (int64, error) {
	if raw == nil {
		return defaultExpandMaxBytes, nil
	}
	if *raw <= 0 {
		return 0, wrapInvalidExpand(errors.New("max_bytes must be greater than zero"))
	}
	if *raw > int64(maxSkillFileBytes) {
		return 0, wrapInvalidExpand(fmt.Errorf("max_bytes must be <= %d", maxSkillFileBytes))
	}
	return *raw, nil
}

func normalizeExpandSection(raw string) (skillExpandSection, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return skillExpandSection{}, nil
	}
	if strings.HasPrefix(trimmed, "#") {
		_, title, ok := parseMarkdownHeading(trimmed)
		if !ok {
			return skillExpandSection{}, wrapInvalidExpand(fmt.Errorf("section must be a markdown heading: %q", trimmed))
		}
		return skillExpandSection{responseSection: trimmed, anchor: title}, nil
	}
	relPath, err := NormalizeArtifactLocator(ArtifactKindResource, trimmed)
	if err != nil {
		return skillExpandSection{}, wrapInvalidExpand(err)
	}
	return skillExpandSection{responseSection: relPath, resourcePath: relPath}, nil
}

func normalizeExpandLookupError(err error) error {
	if err == nil || errors.Is(err, ErrInvalidSkillName) {
		return err
	}
	if strings.HasPrefix(err.Error(), "skill not found:") {
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	}
	return err
}

func (s *service) expandSkillBody(record skillRecord, section skillExpandSection, maxBytes int64) (SkillExpandResult, error) {
	stat, err := os.Stat(record.path)
	if err != nil {
		return SkillExpandResult{}, err
	}
	if stat.Size() > maxSkillFileBytes {
		return SkillExpandResult{}, fmt.Errorf("skill file too large: %s is %d bytes, limit %d", record.path, stat.Size(), maxSkillFileBytes)
	}
	data, err := os.ReadFile(record.path)
	if err != nil {
		return SkillExpandResult{}, err
	}
	full := string(data)
	_, body, hasFrontmatter := splitFrontmatter(full)
	if !hasFrontmatter {
		body = full
	}
	slice := body
	if section.anchor != "" {
		sliceText, ok := sliceMarkdownSection(body, section.anchor)
		if !ok {
			return SkillExpandResult{}, wrapInvalidExpand(fmt.Errorf("section not found: %q", section.responseSection))
		}
		slice = sliceText
	}
	return buildExpandResult(record, record.path, section.responseSection, slice, maxBytes), nil
}

func (s *service) expandSkillResource(record skillRecord, section skillExpandSection, maxBytes int64) (SkillExpandResult, error) {
	targetPath, err := resolveExpandResourcePath(record.info.Dir, section.resourcePath)
	if err != nil {
		return SkillExpandResult{}, err
	}
	stat, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SkillExpandResult{}, wrapInvalidExpand(fmt.Errorf("resource not found: %s", section.resourcePath))
		}
		return SkillExpandResult{}, err
	}
	if stat.IsDir() {
		return SkillExpandResult{}, wrapInvalidExpand(fmt.Errorf("resource path is directory: %s", section.resourcePath))
	}
	if stat.Size() > maxSkillFileBytes {
		return SkillExpandResult{}, fmt.Errorf("resource file too large: %s is %d bytes, limit %d", section.resourcePath, stat.Size(), maxSkillFileBytes)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return SkillExpandResult{}, err
	}
	return buildExpandResult(record, targetPath, section.responseSection, string(data), maxBytes), nil
}

func resolveExpandResourcePath(skillDir, relPath string) (string, error) {
	skillDir = filepath.Clean(strings.TrimSpace(skillDir))
	if skillDir == "" {
		return "", errors.New("skill dir is required")
	}
	if resolved, err := filepath.EvalSymlinks(skillDir); err == nil {
		skillDir = resolved
	}
	targetPath := filepath.Clean(filepath.Join(skillDir, relPath))
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil {
		targetPath = resolved
	}
	if !platformshared.ContainsPath(skillDir, targetPath) {
		return "", wrapInvalidExpand(fmt.Errorf("resource path escapes skill dir: %s", relPath))
	}
	return targetPath, nil
}

func buildExpandResult(record skillRecord, path, section, fullContent string, maxBytes int64) SkillExpandResult {
	totalBytes := int64(len(fullContent))
	content, truncated := truncateBytes(fullContent, maxBytes)
	sum := sha256.Sum256([]byte(fullContent))
	return SkillExpandResult{
		Name:        record.info.Name,
		Section:     section,
		Path:        path,
		Summary:     record.info.Summary,
		Content:     content,
		Truncated:   truncated,
		TotalBytes:  totalBytes,
		ContentHash: hex.EncodeToString(sum[:]),
		Trust:       record.info.Trust,
	}
}

func withExpandApprovalMeta(result SkillExpandResult, meta skillExpandApprovalMeta) SkillExpandResult {
	result.ApprovalScope = meta.Scope
	result.ApprovalSource = meta.Source
	result.ApprovalResult = meta.Result
	return result
}

func (s *service) ReadLocal(_ context.Context, path string) (any, error) {
	path, err := s.resolveSkillPath(path)
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
	dir, err := s.resolveSkillPath(dir)
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
	path, err := s.resolveSkillPath(path)
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
	s.invalidateSkillCatalog()
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
		s.invalidateSkillCatalog()
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
	s.invalidateSkillCatalog()
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
	s.invalidateSkillCatalog()
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
	s.invalidateSkillCatalog()
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
	s.invalidateSkillCatalog()
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
	resolvedSource, err := validateImportSource(source)
	if err != nil {
		return nil, err
	}
	root, err := s.prepareSkillsRoot()
	if err != nil {
		return nil, err
	}
	if err := ensureSourceOutsideRoots(s.skillRoots(), resolvedSource, source); err != nil {
		return nil, err
	}
	targetName := strings.TrimSpace(name)
	if targetName == "" {
		targetName = filepath.Base(resolvedSource)
	}
	targetDir := filepath.Join(root, skillSlug(targetName))
	if err := ensureSkillDirAbsent(targetDir, targetName); err != nil {
		return nil, err
	}
	files, bytes, err := copySkillDir(resolvedSource, targetDir)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": targetName, "dir": targetDir, "skill_file": filepath.Join(targetDir, skillMainFile), "source": resolvedSource, "files": files, "bytes": bytes}, nil
}

func validateImportSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("path is required")
	}
	resolved, err := canonicalProjectPath(source)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", resolved)
	}
	return resolved, nil
}

func (s *service) prepareSkillsRoot() (string, error) {
	root := strings.TrimSpace(s.root)
	if root == "" {
		return "", errors.New("skills root is not configured")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// ensureSourceOutsideRoots 防止将任一技能根内的目录再套娃导入成新技能。
func ensureSourceOutsideRoots(roots []string, resolvedSource, originalSource string) error {
	for _, root := range roots {
		rootPath, err := canonicalProjectPath(root)
		if err != nil {
			continue
		}
		outside, err := pathEscapesRoot(rootPath, resolvedSource)
		if err != nil {
			continue
		}
		if !outside {
			return fmt.Errorf("source is inside skills root: %s", originalSource)
		}
	}
	return nil
}

func ensureSkillDirAbsent(targetDir, targetName string) error {
	_, err := os.Stat(targetDir)
	if err == nil {
		return fmt.Errorf("skill already exists: %s", targetName)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

func (s *service) resolveSkillPath(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("path is required")
	}
	roots := s.skillRoots()
	if len(roots) == 0 {
		return "", errors.New("skills root is not configured")
	}
	targetPath, err := canonicalProjectPath(target)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		rootPath, err := canonicalProjectPath(root)
		if err != nil {
			return "", err
		}
		outside, err := pathEscapesRoot(rootPath, targetPath)
		if err != nil {
			return "", err
		}
		if !outside {
			return targetPath, nil
		}
	}
	return "", fmt.Errorf("path escapes skills root: %s", target)
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
	if _, err := filepath.Rel(rootPath, targetPath); err != nil {
		return false, err
	}
	return !platformshared.ContainsPath(rootPath, targetPath), nil
}
