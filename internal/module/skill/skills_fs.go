package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	skillidentity "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/identity"
)

type skillNotFoundError string

// Error 返回错误文本。
func (e skillNotFoundError) Error() string { return fmt.Sprintf("skill not found: %s", string(e)) }

// Unwrap 返回底层错误。
func (skillNotFoundError) Unwrap() error { return os.ErrNotExist }

// ListSkills 列出当前 cwd 可见的有效 skill。
// 同名冲突会 fail-fast 返回错误，避免调用方在不确定来源时读取错误内容。
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

// ListSkillInventory 返回当前 cwd 的完整 skill inventory。
// inventory 保留原始 canonical 记录，用于治理界面查看被策略隐藏或冲突的来源。
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

// resolveSkillRecordByName 在当前 cwd 的有效 skill 集合里解析名称或别名。
// 同名冲突会直接返回冲突错误，调用方不能绕过策略读取任意目录。
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

// resolveCanonicalRecordByNameInTarget 在指定 scope/personalType 内解析 canonical 记录。
// 删除、覆盖等写操作只允许命中唯一真实来源，别名歧义会 fail-fast。
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

// ReadLocal 按路径读取本地 skill 文件。
// 读取前会确认路径属于当前有效 skill 集合，防止通过手写路径绕过同名冲突和范围策略。
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

// summarizeReadLocalSkill 从 SKILL.md 内容中提取前端列表需要的摘要字段。
// frontmatter 摘要优先；缺失时使用 description 或正文摘要，但不影响文件读取本身。
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

// ListLocalFiles 列出当前有效 skill 目录下的可编辑文件。
// dir/path 必须先解析到有效 skill 集合内，避免 UI 通过路径枚举未暴露来源。
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

// CreateSkill 是 host 侧创建项目 skill 的入口。
// 它只接受 project scope，并复用 WriteLocal 的路径校验和 mirror 发布流程；system scope 必须走带审批的写入入口。
func (s *service) CreateSkill(ctx context.Context, p createSkillParams) (any, error) {
	name, err := validateSkillName(p.Name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Content) == "" {
		return nil, errors.Join(ErrInvalidSkillName, errors.New("content is required"))
	}
	// CWD 校验统一留给 WriteLocal，保证 ErrMissingCWD 只有一个来源。
	return s.WriteLocal(ctx, name, p.Content, skillScopeProject)
}

// WriteLocal 写入本地 skill 内容。
// project 和 personal 走不同持久化边界；目标解析失败或 system scope 未审批时直接返回错误。
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
		return s.writePersonalLocal(ctx, cwd, target.path, content, target.scope, target.personalType)
	}
	return s.writeProjectLocal(ctx, cwd, target.path, content, target.scope, target.personalType)
}

// writePersonalLocal 写入 personal skill 并保留可回滚备份。
// 审计、目录写入或 mirror 发布前校验失败都会返回错误，不把半写状态当成功。
func (s *service) writePersonalLocal(ctx context.Context, cwd, path, content, scope, personalType string) (any, error) {
	name := filepath.Base(filepath.Dir(path))
	if err := s.ensureWriteTimeMirrorPublishAllowed(ctx, cwd, scope, personalType, name); err != nil {
		return nil, err
	}
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
		return nil, rollbackPersonalWriteError(err, filepath.Dir(path), backupDir)
	}
	if err := s.finalizePersonalMutation(ctx, "personal_write", filepath.Dir(path), record); err != nil {
		return nil, rollbackPersonalWriteError(err, filepath.Dir(path), backupDir)
	}
	s.publishSkillsChanged(ctx, "local_write", name, scope)
	result := map[string]any{"ok": true, "path": path, "dir": filepath.Dir(path), "bytes": len(content)}
	report, err := s.publishWriteTimeMirrorsBlocking(ctx, cwd, scope, personalType, name)
	if err != nil {
		return nil, rollbackPersonalWriteError(err, filepath.Dir(path), backupDir)
	}
	return attachMirrorPublish(result, report), nil
}

// ImportLocalDir 从本地目录导入一个或一批 skill。
// 目标 scope 先走系统写入审批和路径校验，成功后再发布 mirror 变更事件。
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
		report, err := s.publishWriteTimeMirrorsBlocking(ctx, cwd, resolvedScope, resolvedPersonalType, name)
		if err != nil {
			if rollbackErr := rollbackImportedSkillResults(results); rollbackErr != nil {
				return nil, errors.Join(err, fmt.Errorf("rollback imported skills: %w", rollbackErr))
			}
			return nil, err
		}
		response["mirror_publish"] = report
	}
	return response, nil
}

// validateImportLocalDirParams 规范化导入模式并收集本地来源目录。
// batch 模式不能指定单一 name，多来源导入也不能共享一个目标名。
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

// DeleteLocal 删除指定 canonical skill。
// personal skill 会先归档再删除；project skill 直接移除目录并发布 mirror 刷新结果。
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
		return s.deletePersonalLocal(ctx, cwd, name, dir, scope, personalType)
	}
	return s.deleteProjectLocal(ctx, cwd, name, dir, scope, personalType)
}

// deleteProjectLocal 删除 project skill，并在 mirror 发布失败时恢复原目录。
func (s *service) deleteProjectLocal(ctx context.Context, cwd, name, dir, scope, personalType string) (any, error) {
	backupDir, err := backupExistingProjectSkill(dir)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	s.publishSkillsChanged(ctx, "delete_local", name, scope)
	result := map[string]any{"ok": true, "name": name, "dir": dir, "removed_agent_bindings": 0}
	report, err := s.publishWriteTimeMirrorsBlocking(ctx, cwd, scope, personalType, name)
	if err != nil {
		if rollbackErr := rollbackProjectSkillDir(dir, backupDir); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback project delete: %w", rollbackErr))
		}
		return nil, err
	}
	if err := cleanupProjectSkillBackup(backupDir); err != nil {
		return nil, err
	}
	return attachMirrorPublish(result, report), nil
}

// deletePersonalLocal 归档删除 personal skill。
// intent/finalize 审计任一步失败都会尝试恢复原目录，防止删除状态不可追踪。
func (s *service) deletePersonalLocal(ctx context.Context, cwd, name, dir, scope, personalType string) (any, error) {
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
		return nil, restorePersonalDeleteError(err, dir, archiveDir)
	}
	if err := s.writeSkillMutationAudit(ctx, "personal_delete_finalize", record); err != nil {
		return nil, restorePersonalDeleteError(err, dir, archiveDir)
	}
	s.publishSkillsChanged(ctx, "delete_local", name, scope)
	result := map[string]any{"ok": true, "name": name, "dir": dir, "archive_dir": archiveDir, "removed_agent_bindings": 0}
	report, err := s.publishWriteTimeMirrorsBlocking(ctx, cwd, scope, personalType, name)
	if err != nil {
		return nil, restorePersonalDeleteError(err, dir, archiveDir)
	}
	return attachMirrorPublish(result, report), nil
}

func rollbackPersonalWriteError(err error, targetDir, backupDir string) error {
	if rollbackErr := rollbackPersonalSkillDir(targetDir, backupDir); rollbackErr != nil {
		return errors.Join(err, fmt.Errorf("rollback personal write: %w", rollbackErr))
	}
	return err
}

func restorePersonalDeleteError(err error, dir, archiveDir string) error {
	if restoreErr := restoreDeletedPersonalSkill(dir, archiveDir); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("restore personal delete: %w", restoreErr))
	}
	return err
}
