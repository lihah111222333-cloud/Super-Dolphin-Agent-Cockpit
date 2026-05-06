package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errInvalidSkillExpandParam = errors.New("invalid skill expand params")

type skillNotFoundError string

func (e skillNotFoundError) Error() string {
	return fmt.Sprintf("skill not found: %s", string(e))
}

func (skillNotFoundError) Unwrap() error {
	return os.ErrNotExist
}

func (s *service) ListSkills(ctx context.Context) ([]SkillInfo, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	records, err := s.scanSkills(cwd)
	if err != nil {
		return nil, err
	}
	skills := make([]SkillInfo, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.info)
	}
	return skillsWithDisclosureTiers(skills, s.disclosureTiers), nil
}

type skillExpandPrepared struct {
	record    skillRecord
	result    skillExpandResult
	cacheable bool
}

func (s *service) Expand(ctx context.Context, p skillExpandParams) (skillExpandResult, error) {
	prepared, err := s.prepareSkillExpand(ctx, p)
	if err != nil {
		return skillExpandResult{}, err
	}
	return prepared.result, nil
}

func (s *service) prepareSkillExpand(ctx context.Context, p skillExpandParams) (skillExpandPrepared, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return skillExpandPrepared{}, err
	}
	record, err := s.resolveSkillRecordByName(p.Name, cwd)
	if err != nil {
		return skillExpandPrepared{}, err
	}
	maxBytes, err := normalizeSkillExpandMaxBytes(p.MaxBytes)
	if err != nil {
		return skillExpandPrepared{}, err
	}
	section := strings.TrimSpace(p.Section)
	switch {
	case section == "":
		result, err := s.expandSkillFile(record, maxBytes)
		if err != nil {
			return skillExpandPrepared{}, err
		}
		return skillExpandPrepared{record: record, result: result, cacheable: true}, nil
	case strings.HasPrefix(section, "#"):
		result, err := s.expandSkillSection(record, section, maxBytes)
		if err != nil {
			return skillExpandPrepared{}, err
		}
		return skillExpandPrepared{record: record, result: result}, nil
	default:
		result, err := s.expandSkillResource(record, section, maxBytes)
		if err != nil {
			return skillExpandPrepared{}, err
		}
		return skillExpandPrepared{record: record, result: result}, nil
	}
}

func (s *service) resolveSkillRecordByName(name, cwd string) (skillRecord, error) {
	normalized, err := validateSkillName(name)
	if err != nil {
		return skillRecord{}, err
	}
	records, err := s.scanSkills(cwd)
	if err != nil {
		return skillRecord{}, err
	}
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.info.Name), normalized) {
			return record, nil
		}
	}
	return skillRecord{}, skillNotFoundError(normalized)
}

func normalizeSkillExpandMaxBytes(maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, fmt.Errorf("%w: max_bytes must be >= 0", errInvalidSkillExpandParam)
	}
	return resolveMaxBytes(maxBytes), nil
}

func (s *service) expandSkillFile(record skillRecord, maxBytes int64) (skillExpandResult, error) {
	data, err := readSkillExpandBytes(record.path, "skill file")
	if err != nil {
		return skillExpandResult{}, err
	}
	return buildSkillExpandResult(record, "", record.path, data, maxBytes), nil
}

func (s *service) expandSkillSection(record skillRecord, section string, maxBytes int64) (skillExpandResult, error) {
	normalizedSection, headingTitle, err := parseSkillExpandSection(section)
	if err != nil {
		return skillExpandResult{}, err
	}
	data, err := readSkillExpandBytes(record.path, "skill file")
	if err != nil {
		return skillExpandResult{}, err
	}
	_, body, hasFrontmatter := splitFrontmatter(string(data))
	if !hasFrontmatter {
		body = string(data)
	}
	slice, ok := sliceMarkdownSection(body, headingTitle)
	if !ok {
		return skillExpandResult{}, fmt.Errorf("%w: section not found: %s", errInvalidSkillExpandParam, normalizedSection)
	}
	return buildSkillExpandResult(record, normalizedSection, record.path, []byte(slice), maxBytes), nil
}

func parseSkillExpandSection(section string) (string, string, error) {
	trimmed := strings.TrimSpace(section)
	level, title, ok := parseMarkdownHeading(trimmed)
	if !ok || level < 2 || level > 3 {
		return "", "", fmt.Errorf("%w: section must be an H2/H3 heading", errInvalidSkillExpandParam)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", "", fmt.Errorf("%w: section heading is empty", errInvalidSkillExpandParam)
	}
	return strings.Repeat("#", level) + " " + title, title, nil
}

func (s *service) expandSkillResource(record skillRecord, section string, maxBytes int64) (skillExpandResult, error) {
	relPath, err := NormalizeArtifactLocator(ArtifactKindResource, section)
	if err != nil {
		return skillExpandResult{}, fmt.Errorf("%w: %v", errInvalidSkillExpandParam, err)
	}
	target, _, err := resolveResourceTarget(record.info.Dir, relPath)
	if err != nil {
		return skillExpandResult{}, fmt.Errorf("%w: %v", errInvalidSkillExpandParam, err)
	}
	data, err := readSkillExpandBytes(target, "resource file")
	if err != nil {
		return skillExpandResult{}, err
	}
	return buildSkillExpandResult(record, relPath, target, data, maxBytes), nil
}

func readSkillExpandBytes(path, label string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s is a directory: %s", errInvalidSkillExpandParam, label, path)
	}
	if info.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("%s too large: %s is %d bytes, limit %d", label, path, info.Size(), maxSkillFileBytes)
	}
	return os.ReadFile(path)
}

func buildSkillExpandResult(record skillRecord, section, path string, data []byte, maxBytes int64) skillExpandResult {
	content, truncated := truncateBytes(string(data), maxBytes)
	return skillExpandResult{
		Name:        record.info.Name,
		Section:     section,
		Path:        path,
		Summary:     record.info.Summary,
		Content:     content,
		Truncated:   truncated,
		TotalBytes:  int64(len(data)),
		ContentHash: hashSkillExpandContent(data),
		Trust:       record.info.Trust,
	}
}

func hashSkillExpandContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *service) ReadLocal(ctx context.Context, path string) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	path, err = s.resolveSkillPath(path, cwd, "")
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

func (s *service) ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	dir := strings.TrimSpace(p.Dir)
	if dir == "" && strings.TrimSpace(p.Path) != "" {
		dir = filepath.Dir(strings.TrimSpace(p.Path))
	}
	if dir == "" {
		return nil, errors.New("dir or path is required")
	}
	dir, err = s.resolveSkillPath(dir, cwd, "")
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

// CreateSkill is the host-side project-scope self-learning entry. It is a
// thin wrapper: all writes MUST land through WriteLocal(..., scope=project)
// so the one-writer rule in the P21 plan holds. system-scope writes are not
// accepted from this entry; they must go through skills/local/write plus the
// review gate that the plan defines.
func (s *service) CreateSkill(ctx context.Context, p createSkillParams) (any, error) {
	name, err := validateSkillName(p.Name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Content) == "" {
		return nil, errors.Join(ErrInvalidSkillName, errors.New("content is required"))
	}
	// requireCWD is checked inside WriteLocal; we rely on it rather than
	// duplicating the check so there is a single source of truth for the
	// ErrMissingCWD path.
	return s.WriteLocal(ctx, name, p.Content, skillScopeProject)
}

func (s *service) WriteLocal(ctx context.Context, path, content string, scope ...string) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	requestedScope := resolveRequestedSkillScope(scope...)
	normalizedScope, err := normalizeSkillScope(requestedScope)
	if err != nil {
		return nil, err
	}
	if err := RequireSkillSystemReview(normalizedScope, skillSlug(path), skillContentHash(content), RepoFingerprint(cwd), "", ""); err != nil {
		return nil, err
	}
	path, err = s.resolveSkillPath(path, cwd, requestedScope)
	if err != nil {
		return nil, err
	}
	if len(content) > maxSkillFileBytes {
		return nil, fmt.Errorf("content too large: %d bytes", len(content))
	}
	mode, err := writableSkillFileMode(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "local_write", filepath.Base(filepath.Dir(path)), normalizedScope)
	return map[string]any{"ok": true, "path": path, "dir": filepath.Dir(path), "bytes": len(content)}, nil
}

func (s *service) ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	sources, mode, err := validateImportLocalDirParams(p)
	if err != nil {
		return nil, err
	}
	results, failures := s.importSources(sources, p.Name, cwd, p.Scope, mode)
	response := buildImportLocalDirResponse(sources, results, failures)
	if len(results) > 0 {
		name := strings.TrimSpace(p.Name)
		if name == "" && len(results) == 1 {
			name, _ = results[0]["name"].(string)
		}
		resolvedScope, _ := normalizeSkillScope(p.Scope)
		s.publishSkillsChanged(ctx, "import_dir", name, resolvedScope)
	}
	return response, nil
}

func validateImportLocalDirParams(p importSkillDirParams) ([]string, string, error) {
	mode, err := normalizeImportMode(p.Mode)
	if err != nil {
		return nil, "", err
	}
	if mode == importModeBatch && strings.TrimSpace(p.Name) != "" {
		return nil, "", errors.New("name is not allowed in batch mode")
	}
	sources, err := collectImportSources(p.Path, p.Paths)
	if err != nil {
		return nil, "", err
	}
	if len(sources) == 0 {
		return nil, "", errors.New("path or paths is required")
	}
	if len(sources) > 1 && strings.TrimSpace(p.Name) != "" {
		return nil, "", errors.New("name is only supported for single directory import")
	}
	return sources, mode, nil
}

func buildImportLocalDirResponse(sources []string, results []map[string]any, failures []map[string]any) map[string]any {
	response := map[string]any{"requested": len(sources), "imported": results}
	if len(failures) > 0 {
		response["failures"] = failures
	}
	if len(results) == 1 {
		response["skill"] = results[0]
	}
	return response
}

func (s *service) DeleteLocal(ctx context.Context, name string) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	record, err := s.resolveSkill(name, cwd)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(record.info.Dir); err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "delete_local", record.info.Name, scopeFromTrust(record.info.Trust))
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

func (s *service) WriteRemote(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	if err := RequireSkillSystemReview(skillScopeSystem, skillSlug(name), skillContentHash(content), RepoFingerprint(s.projectRoot), "", ""); err != nil {
		return nil, err
	}
	path, err := s.writeSkill(name, content)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "remote_write", name, skillScopeSystem)
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

func (s *service) WriteSkillContent(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	if err := RequireSkillSystemReview(skillScopeSystem, skillSlug(name), skillContentHash(content), RepoFingerprint(s.projectRoot), "", ""); err != nil {
		return nil, err
	}
	path, err := s.writeSkill(name, content)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "config_write", name, skillScopeSystem)
	return map[string]any{"ok": true, "path": path}, nil
}

func (s *service) WriteSummary(ctx context.Context, name, summary string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	if err := RequireSkillSystemReview(skillScopeSystem, skillSlug(name), skillContentHash(summary), RepoFingerprint(s.projectRoot), "", ""); err != nil {
		return nil, err
	}
	path, resolvedName, err := s.updateSkillSummary(name, summary)
	if err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "summary_write", resolvedName, skillScopeSystem)
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
	return !pathutil.ContainsPath(rootPath, targetPath), nil
}
