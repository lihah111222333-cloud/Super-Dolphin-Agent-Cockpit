package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	skillidentity "github.com/anthropic-ai/super-agent-v3/internal/module/skill/identity"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

type skillNotFoundError string

// Error 返回错误文本。
func (e skillNotFoundError) Error() string {
	return fmt.Sprintf("skill not found: %s", string(e))
}

// Unwrap 返回底层错误。
func (skillNotFoundError) Unwrap() error {
	return os.ErrNotExist
}

func requireCWDOrLog(ctx context.Context, op string) string {
	cwd, err := requireCWD(ctx)
	if err != nil {
		pkglogger.Warn("skill: requireCWD failed", "op", op, "error", err)
	}
	return cwd
}

// ListSkills 列出skills。
func (s *service) ListSkills(ctx context.Context) ([]SkillInfo, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	records, conflicts, err := s.canonicalEffectiveSet(ctx, cwd)
	if err != nil {
		return nil, err
	}
	skills := make([]SkillInfo, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.info)
	}
	if len(conflicts) > 0 {
		return skills, skillSameNameConflictError{Conflicts: conflicts}
	}
	return skills, nil
}

// ListSkillInventory 列出技能inventory。
func (s *service) ListSkillInventory(ctx context.Context) ([]SkillInfo, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	store := newCanonicalStoreForOwner(strings.TrimSpace(s.superDolphinHome), defaultOwnerOSUID(), defaultAppProfile())
	records, err := store.scan(cwd)
	if err != nil {
		return nil, err
	}
	skills := make([]SkillInfo, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.info)
	}
	return skills, nil
}

// resolveSkillRecordByName 按名称解析技能记录。
func (s *service) resolveSkillRecordByName(name, cwd string) (skillRecord, error) {
	trimmed := strings.TrimSpace(name)
	normalized, _, err := normalizeSkillIdentityName(trimmed, "")
	if err != nil {
		return skillRecord{}, err
	}
	records, conflicts, err := s.canonicalEffectiveSet(context.Background(), cwd)
	if err != nil {
		return skillRecord{}, err
	}
	if conflict, ok := canonicalConflictByName(conflicts, normalized); ok {
		return skillRecord{}, skillSameNameConflictError{Conflicts: []canonicalSkillConflict{conflict}}
	}
	matches := make([]skillRecord, 0, 1)
	for _, record := range records {
		if skillidentity.MatchesSkillCandidate(record.info, trimmed, normalized) {
			matches = append(matches, skillRecordFromCanonical(record))
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return skillRecord{}, fmt.Errorf("ambiguous skill name alias: %s", trimmed)
	}
	return skillRecord{}, skillNotFoundError(normalized)
}

// resolveCanonicalRecordByNameInTarget 按名称target解析canonical记录。
func (s *service) resolveCanonicalRecordByNameInTarget(name, cwd, scope, personalType string) (canonicalSkillRecord, error) {
	trimmed := strings.TrimSpace(name)
	normalized, _, err := normalizeSkillIdentityName(trimmed, "")
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	store := newCanonicalStoreForOwner(strings.TrimSpace(s.superDolphinHome), defaultOwnerOSUID(), defaultAppProfile())
	records, err := store.scan(cwd)
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	matches := make([]canonicalSkillRecord, 0, 1)
	for _, record := range records {
		if record.Scope != scope || record.PersonalType != personalType {
			continue
		}
		if skillidentity.MatchesSkillCandidate(record.info, trimmed, normalized) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return canonicalSkillRecord{}, fmt.Errorf("ambiguous skill name alias: %s", trimmed)
	}
	return canonicalSkillRecord{}, skillNotFoundError(normalized)
}

func (s *service) canonicalEffectiveSet(ctx context.Context, cwd string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	store := newCanonicalStoreForOwner(strings.TrimSpace(s.superDolphinHome), defaultOwnerOSUID(), defaultAppProfile())
	return store.EffectiveSet(ctx, cwd)
}

func canonicalConflictByName(conflicts []canonicalSkillConflict, name string) (canonicalSkillConflict, bool) {
	for _, conflict := range conflicts {
		if strings.EqualFold(conflict.Name, name) {
			return conflict, true
		}
	}
	return canonicalSkillConflict{}, false
}

func skillRecordFromCanonical(record canonicalSkillRecord) skillRecord {
	return skillRecord{
		info: record.info,
		path: record.SkillFile,
		rel:  filepath.Base(record.Dir),
	}
}

// ReadLocal 读取local。
func (s *service) ReadLocal(ctx context.Context, path string) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	path, err = s.resolveReadLocalPath(path, cwd)
	if err != nil {
		return nil, err
	}
	if err := s.ensurePathInEffectiveSet(ctx, cwd, path); err != nil {
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
	content := string(data)
	summary, summarySource := summarizeReadLocalSkill(content)
	return map[string]any{"skill": map[string]any{"path": path, "content": content, "summary": summary, "summary_source": summarySource}}, nil
}

// summarizeReadLocalSkill 处理summarizereadlocal技能。
func summarizeReadLocalSkill(content string) (string, string) {
	body := content
	if frontmatter, parsedBody, ok := splitFrontmatter(content); ok {
		body = parsedBody
		var info SkillInfo
		lines := strings.Split(frontmatter, "\n")
		for i := 0; i < len(lines); i++ {
			key, value, ok := parseMetaLine(lines[i])
			if !ok {
				continue
			}
			i += applyMetaLine(&info, key, value, lines[i+1:])
		}
		if summary := strings.TrimSpace(info.Summary); summary != "" && !isInternalSkillMarkerSummary(summary) {
			return truncateRunes(summary, 220), "frontmatter"
		}
		if description := strings.TrimSpace(info.Description); description != "" {
			return truncateRunes(description, 220), "description"
		}
	}
	return truncateRunes(summarizeSkillBody(body, ""), 220), "generated"
}

func (s *service) resolveReadLocalPath(path, cwd string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || filepath.IsAbs(trimmed) || strings.Contains(filepath.Clean(trimmed), string(filepath.Separator)) {
		return s.resolveSkillPath(trimmed, cwd, "")
	}
	record, err := s.resolveSkillRecordByName(trimmed, cwd)
	if err != nil {
		return "", err
	}
	return record.path, nil
}

// ListLocalFiles 列出local文件。
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
	if err := s.ensurePathInEffectiveSet(ctx, cwd, dir); err != nil {
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
// CreateSkill 创建技能。
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

// WriteLocal 写入local。
func (s *service) WriteLocal(ctx context.Context, path, content string, scopeAndType ...string) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.prepareWriteLocalTarget(cwd, path, content, scopeAndType...)
	if err != nil {
		return nil, err
	}
	if target.scope == skillScopePersonal {
		return s.writePersonalLocal(ctx, target.path, content, target.scope, target.personalType)
	}
	return s.writeProjectLocal(ctx, cwd, target.path, content, target.scope, target.personalType)
}

// writePersonalLocal 写入personallocal。
func (s *service) writePersonalLocal(ctx context.Context, path, content, scope, personalType string) (any, error) {
	name := filepath.Base(filepath.Dir(path))
	record, err := s.preparePersonalMutation(ctx, "personal_write", name, filepath.Dir(path), scope, personalType)
	if err != nil {
		return nil, err
	}
	mode, err := writableSkillFileMode(path)
	if err != nil {
		return nil, err
	}
	backupDir, err := backupExistingPersonalSkill(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		if rollbackErr := rollbackPersonalSkillDir(filepath.Dir(path), backupDir); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback personal write: %w", rollbackErr))
		}
		return nil, err
	}
	if err := s.finalizePersonalMutation(ctx, "personal_write", filepath.Dir(path), record); err != nil {
		if rollbackErr := rollbackPersonalSkillDir(filepath.Dir(path), backupDir); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback personal write: %w", rollbackErr))
		}
		return nil, err
	}
	s.publishSkillsChanged(ctx, "local_write", name, scope)
	result := map[string]any{"ok": true, "path": path, "dir": filepath.Dir(path), "bytes": len(content)}
	return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, requireCWDOrLog(ctx, "WriteLocal"), scope, personalType, name)), nil
}

// ImportLocalDir 导入local目录。
func (s *service) ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	sources, mode, err := validateImportLocalDirParams(p)
	if err != nil {
		return nil, err
	}
	results, failures := s.importSources(sources, p.Name, cwd, p.Scope, p.PersonalType, mode)
	response := buildImportLocalDirResponse(sources, results, failures)
	if len(results) > 0 {
		name := strings.TrimSpace(p.Name)
		if name == "" && len(results) == 1 {
			name, _ = results[0]["name"].(string)
		}
		resolvedScope, resolvedPersonalType, _ := normalizeSkillTarget(p.Scope, p.PersonalType)
		s.publishSkillsChanged(ctx, "import_dir", name, resolvedScope)
		response["mirror_publish"] = s.publishWriteTimeMirrors(ctx, cwd, resolvedScope, resolvedPersonalType, name)
	}
	return response, nil
}

// validateImportLocalDirParams 校验importlocal目录params。
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

// DeleteLocal 删除local。
func (s *service) DeleteLocal(ctx context.Context, p DeleteSkillParams) (any, error) {
	cwd, err := requireCWD(ctx)
	if err != nil {
		return nil, err
	}
	_, scope, personalType, err := s.canonicalRootForTarget(cwd, p.Scope, p.PersonalType)
	if err != nil {
		return nil, err
	}
	record, err := s.resolveCanonicalRecordByNameInTarget(p.Name, cwd, scope, personalType)
	if err != nil {
		return nil, err
	}
	name := record.Name
	dir := record.Dir
	if err := ensureSkillMainFilePresent(dir); err != nil {
		return nil, err
	}
	if scope == skillScopePersonal {
		return s.deletePersonalLocal(ctx, name, dir, scope, personalType)
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "delete_local", name, scope)
	result := map[string]any{"ok": true, "name": name, "dir": dir, "removed_agent_bindings": 0}
	return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, cwd, scope, personalType, name)), nil
}

// deletePersonalLocal 删除personallocal。
func (s *service) deletePersonalLocal(ctx context.Context, name, dir, scope, personalType string) (any, error) {
	archiveDir := s.personalSkillArchiveDir(scope, personalType, name)
	canonicalHash, err := skillDirContentHash(dir)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	record, err := s.personalDeleteArchiveRecord(name, scope, personalType, archiveDir, canonicalHash, now)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0o700); err != nil {
		return nil, err
	}
	if err := s.writeSkillMutationAudit(ctx, "personal_delete_intent", record); err != nil {
		return nil, err
	}
	if err := os.Rename(dir, archiveDir); err != nil {
		return nil, err
	}
	if err := s.writePersonalArchiveRecord(record, archiveDir); err != nil {
		if restoreErr := restoreDeletedPersonalSkill(dir, archiveDir); restoreErr != nil {
			return nil, errors.Join(err, fmt.Errorf("restore personal delete: %w", restoreErr))
		}
		return nil, err
	}
	if err := s.writeSkillMutationAudit(ctx, "personal_delete_finalize", record); err != nil {
		if restoreErr := restoreDeletedPersonalSkill(dir, archiveDir); restoreErr != nil {
			return nil, errors.Join(err, fmt.Errorf("restore personal delete: %w", restoreErr))
		}
		return nil, err
	}
	s.publishSkillsChanged(ctx, "delete_local", name, scope)
	result := map[string]any{"ok": true, "name": name, "dir": dir, "archive_dir": archiveDir, "removed_agent_bindings": 0}
	return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, requireCWDOrLog(ctx, "DeleteLocal"), scope, personalType, name)), nil
}

// ReadRemote 读取remote。
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

// WriteRemote 写入remote。
func (s *service) WriteRemote(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}

// ReadConfig 读取配置。
func (s *service) ReadConfig(_ context.Context, agentID string) (any, error) {
	// Preserve the current unconfigured response until agent-scoped skill
	// bindings have a persisted storage contract.
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

// WriteSkillContent 写入技能内容。
func (s *service) WriteSkillContent(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}

// WriteSummary 写入摘要。
func (s *service) WriteSummary(ctx context.Context, name, summary string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}
