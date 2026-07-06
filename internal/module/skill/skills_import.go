package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/skillhash"
)

const importModeAuto, importModeSingle, importModeBatch = "auto", "single", "batch"

var errNoSkillDirectoriesFound = errors.New("no skill directories found")
var errSkillMainFileNotFound = fmt.Errorf("%s not found", skillMainFile)

func normalizeImportMode(mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", importModeAuto:
		return importModeAuto, nil
	case importModeSingle, importModeBatch:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid import mode: %s", mode)
	}
}

type writeLocalTarget struct{ path, scope, personalType string }

func (s *service) prepareWriteLocalTarget(cwd, path, content string, scopeAndType ...string) (writeLocalTarget, error) {
	requestedScope, requestedPersonalType := resolveRequestedSkillTarget(scopeAndType...)
	scope, personalType, err := normalizeSkillTarget(requestedScope, requestedPersonalType)
	if err != nil {
		return writeLocalTarget{}, err
	}
	if err := RequireSkillSystemReview(scope, skillSlug(path), skillContentHash(content), RepoFingerprint(projectRootForCWD(cwd, "")), "", ""); err != nil {
		return writeLocalTarget{}, err
	}
	resolvedPath, err := s.resolveWriteLocalPath(path, cwd, scope, personalType)
	if err != nil {
		return writeLocalTarget{}, err
	}
	if err := s.ensureWriteLocalTargetAllowed(cwd, resolvedPath, content, scope, personalType); err != nil {
		return writeLocalTarget{}, err
	}
	return writeLocalTarget{path: resolvedPath, scope: scope, personalType: personalType}, nil
}

func (s *service) ensureWriteLocalTargetAllowed(cwd, path, content, scope, personalType string) error {
	root, err := s.resolveScopeRoot(cwd, scope, personalType)
	if err != nil {
		return err
	}
	if err := ensureWritableSkillPathInsideRoot(root, path); err != nil {
		return err
	}
	if len(content) > maxSkillFileBytes {
		return fmt.Errorf("content too large: %d bytes", len(content))
	}
	if err := validateWritableSkillMainContent(root, path, content); err != nil {
		return err
	}
	return nil
}

func validateWritableSkillMainContent(root, path, content string) error {
	if !strings.EqualFold(filepath.Base(path), skillMainFile) {
		return nil
	}
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	info := parseSkillInfo(rel, filepath.Dir(path), content, TrustProject)
	if _, _, err := normalizeSkillIdentityName(info.Name, info.DisplayName); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidSkillName, info.Name)
	}
	return nil
}

// writeProjectLocal 写入 project skill 并在 mirror 发布阻断时回滚 canonical 目录。
func (s *service) writeProjectLocal(ctx context.Context, cwd, path, content, scope, personalType string) (any, error) {
	name := filepath.Base(filepath.Dir(path))
	if err := s.ensureWriteTimeMirrorPublishAllowed(ctx, cwd, scope, personalType, name); err != nil {
		return nil, err
	}
	backupDir, err := backupExistingProjectSkill(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	mode, err := writableSkillFileMode(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		if rollbackErr := rollbackProjectSkillDir(filepath.Dir(path), backupDir); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback project write: %w", rollbackErr))
		}
		return nil, err
	}
	s.publishSkillsChanged(ctx, "local_write", name, scope)
	result := map[string]any{"ok": true, "path": path, "dir": filepath.Dir(path), "bytes": len(content)}
	report, err := s.publishWriteTimeMirrorsBlocking(ctx, cwd, scope, personalType, name)
	if err != nil {
		if rollbackErr := rollbackProjectSkillDir(filepath.Dir(path), backupDir); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback project write: %w", rollbackErr))
		}
		return nil, err
	}
	if err := cleanupProjectSkillBackup(backupDir); err != nil {
		return nil, err
	}
	return attachMirrorPublish(result, report), nil
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

func (s *service) importSources(sources []string, singleName, cwd, scope, personalType, mode string) ([]map[string]any, []map[string]any) {
	results := make([]map[string]any, 0, len(sources))
	failures := make([]map[string]any, 0)
	for _, source := range sources {
		sourceResults, sourceFailures := s.importSource(source, singleName, cwd, scope, personalType, mode)
		results = append(results, sourceResults...)
		failures = append(failures, sourceFailures...)
	}
	return results, failures
}

// importSource 导入单个本地来源。
// auto 模式会根据 SKILL.md 是否存在选择单 skill 或 batch，非法来源被记录为单项失败。
func (s *service) importSource(source, name, cwd, scope, personalType, mode string) ([]map[string]any, []map[string]any) {
	resolvedSource, err := validateImportSource(source)
	if err != nil {
		return nil, []map[string]any{importFailure(source, err)}
	}
	resolvedMode, err := detectImportMode(resolvedSource, mode)
	if err != nil {
		return nil, []map[string]any{importFailure(source, err)}
	}
	if resolvedMode == importModeSingle {
		result, err := s.importSkillUnit(resolvedSource, name, cwd, scope, personalType, source)
		if err != nil {
			return nil, []map[string]any{importFailure(source, err)}
		}
		return []map[string]any{result}, nil
	}
	if strings.TrimSpace(name) != "" {
		return nil, []map[string]any{importFailure(source, errors.New("name is not allowed in batch mode"))}
	}
	return s.importBatchSource(resolvedSource, cwd, scope, personalType)
}

// detectImportMode 根据请求模式和目录形态决定导入策略。
// auto 模式只接受含 SKILL.md 的单目录，或一层子目录中包含 skill 的容器。
func detectImportMode(resolvedSource, requestedMode string) (string, error) {
	switch requestedMode {
	case importModeSingle, importModeBatch:
		return requestedMode, nil
	case importModeAuto:
		if skillMainFileExists(resolvedSource) {
			return importModeSingle, nil
		}
		hasChildren, err := hasDirectSkillDirs(resolvedSource)
		if err != nil {
			return "", err
		}
		if hasChildren {
			return importModeBatch, nil
		}
		return "", errNoSkillDirectoriesFound
	default:
		return "", fmt.Errorf("invalid import mode: %s", requestedMode)
	}
}

// importBatchSource 导入容器目录下的一组直接子 skill。
// 单个子目录失败会进入 failures，其他合法 skill 仍可继续导入并返回部分结果。
func (s *service) importBatchSource(container, cwd, scope, personalType string) ([]map[string]any, []map[string]any) {
	skillDirs, failures, err := collectBatchSkillDirs(container)
	if err != nil {
		return nil, []map[string]any{importFailure(container, err)}
	}
	if len(skillDirs) == 0 {
		if len(failures) == 0 {
			failures = append(failures, importFailure(container, errNoSkillDirectoriesFound))
		}
		return nil, failures
	}
	results := make([]map[string]any, 0, len(skillDirs))
	for _, skillDir := range skillDirs {
		result, err := s.importSkillUnit(skillDir, "", cwd, scope, personalType, skillDir)
		if err != nil {
			failures = append(failures, importFailure(skillDir, err))
			continue
		}
		results = append(results, result)
	}
	return results, failures
}

func collectBatchSkillDirs(container string) ([]string, []map[string]any, error) {
	entries, err := os.ReadDir(container)
	if err != nil {
		return nil, nil, err
	}
	skillDirs := make([]string, 0, len(entries))
	failures := make([]map[string]any, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childDir := filepath.Join(container, entry.Name())
		if skillMainFileExists(childDir) {
			skillDirs = append(skillDirs, childDir)
			continue
		}
		failures = append(failures, importFailure(childDir, errSkillMainFileNotFound))
	}
	return skillDirs, failures, nil
}

func hasDirectSkillDirs(container string) (bool, error) {
	entries, err := os.ReadDir(container)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && skillMainFileExists(filepath.Join(container, entry.Name())) {
			return true, nil
		}
	}
	return false, nil
}

func skillMainFileExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, skillMainFile))
	return err == nil && !info.IsDir()
}

// importSkillUnit 把一个已验证来源目录复制到目标 skill 根。
// 目标名、系统写入审批、来源越界和同名目录都在复制前 fail-fast。
func (s *service) importSkillUnit(resolvedSource, name, cwd, scope, personalType, originalSource string) (map[string]any, error) {
	normalizedScope, normalizedPersonalType, err := normalizeSkillTarget(scope, personalType)
	if err != nil {
		return nil, err
	}
	targetName, displayName, err := importTargetSkillIdentity(resolvedSource, name)
	if err != nil {
		return nil, err
	}
	sourceHash, err := skillDirContentHash(resolvedSource)
	if err != nil {
		return nil, err
	}
	if err := RequireSkillSystemReview(normalizedScope, skillSlug(targetName), sourceHash, RepoFingerprint(projectRootForCWD(cwd, "")), "", ""); err != nil {
		return nil, err
	}
	root, err := s.prepareScopedSkillsRoot(cwd, normalizedScope, normalizedPersonalType)
	if err != nil {
		return nil, err
	}
	if err := ensureSourceOutsideRoots(s.allSkillRoots(cwd), resolvedSource, originalSource); err != nil {
		return nil, err
	}
	targetDir := filepath.Join(root, skillSlug(targetName))
	if err := ensureSkillDirAbsent(targetDir, targetName); err != nil {
		return nil, err
	}
	if normalizedScope == skillScopePersonal {
		return s.importPersonalSkillUnit(resolvedSource, targetName, displayName, targetDir, normalizedScope, normalizedPersonalType)
	}
	files, bytes, err := copyImportedSkillDir(resolvedSource, targetDir, targetName, displayName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":       targetName,
		"dir":        targetDir,
		"skill_file": filepath.Join(targetDir, skillMainFile),
		"source":     resolvedSource,
		"files":      files,
		"bytes":      bytes,
	}, nil
}

func (s *service) importPersonalSkillUnit(resolvedSource, targetName, displayName, targetDir, scope, personalType string) (map[string]any, error) {
	record, err := s.preparePersonalMutation(context.Background(), "personal_import", targetName, targetDir, scope, personalType)
	if err != nil {
		return nil, err
	}
	files, bytes, err := copyImportedSkillDir(resolvedSource, targetDir, targetName, displayName)
	if err != nil {
		return nil, err
	}
	if err := s.finalizePersonalMutation(context.Background(), "personal_import", targetDir, record); err != nil {
		if rollbackErr := rollbackPersonalSkillDir(targetDir, ""); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback personal import: %w", rollbackErr))
		}
		return nil, err
	}
	return map[string]any{
		"name":       targetName,
		"dir":        targetDir,
		"skill_file": filepath.Join(targetDir, skillMainFile),
		"source":     resolvedSource,
		"files":      files,
		"bytes":      bytes,
	}, nil
}

func importTargetSkillIdentity(resolvedSource, requestedName string) (string, string, error) {
	if requestedName = strings.TrimSpace(requestedName); requestedName != "" {
		name, err := validateSkillName(requestedName)
		return name, "", err
	}
	if name, err := validateSkillName(filepath.Base(resolvedSource)); err == nil {
		return name, "", nil
	}
	record, err := parseSkillRecord(filepath.Dir(resolvedSource), filepath.Join(resolvedSource, skillMainFile), TrustProject)
	if err != nil {
		return "", "", err
	}
	return normalizeSkillIdentityName(record.info.Name, record.info.DisplayName)
}

func copyImportedSkillDir(resolvedSource, targetDir, targetName, displayName string) (int, int64, error) {
	files, bytes, err := copySkillDir(resolvedSource, targetDir)
	if err != nil {
		_ = os.RemoveAll(targetDir)
		return 0, 0, err
	}
	if err := rewriteCopiedSkillIdentity(targetDir, targetName, displayName); err != nil {
		_ = os.RemoveAll(targetDir)
		return 0, 0, err
	}
	return files, bytes, nil
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

func importFailure(source string, err error) map[string]any {
	return map[string]any{"source": source, "error": err.Error()}
}

// ensureSourceOutsideRoots 防止将任一技能根内的目录再套娃导入成新技能。
func ensureSourceOutsideRoots(roots []string, resolvedSource, originalSource string) error {
	for _, root := range roots {
		rootPath, err := canonicalProjectPath(root)
		if err != nil {
			return fmt.Errorf("resolve skills root %q: %w", root, err)
		}
		outside, err := pathEscapesRoot(rootPath, resolvedSource)
		if err != nil {
			return fmt.Errorf("check source against skills root %q: %w", rootPath, err)
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
	return skillhash.CopyDirWithLimits(source, target)
}

// backupExistingProjectSkill 在 project skill 写入或删除前复制可恢复快照。
func backupExistingProjectSkill(targetDir string) (string, error) {
	if !skillMainFileExists(targetDir) {
		return "", nil
	}
	backupParent, err := os.MkdirTemp(filepath.Dir(targetDir), ".super-dolphin-project-skill-backup-*")
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(backupParent, filepath.Base(targetDir))
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupParent)
		return "", err
	}
	return backupDir, nil
}

// rollbackProjectSkillDir 恢复 project skill 写入或删除前的 canonical 目录状态。
func rollbackProjectSkillDir(targetDir, backupDir string) error {
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if strings.TrimSpace(backupDir) == "" {
		return nil
	}
	if _, _, err := copySkillDir(backupDir, targetDir); err != nil {
		return err
	}
	return cleanupProjectSkillBackup(backupDir)
}

// cleanupProjectSkillBackup 清理 project skill 备份父目录。
func cleanupProjectSkillBackup(backupDir string) error {
	if strings.TrimSpace(backupDir) == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(backupDir))
}

// rollbackImportedSkillResults 删除本轮 import 已落盘的 canonical skill。
func rollbackImportedSkillResults(results []map[string]any) error {
	var joined error
	for _, result := range results {
		dir, _ := result["dir"].(string)
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func copyCanonicalSkillMainFile(src, dst, rel string, mode os.FileMode, tracker *skillhash.ContentLimitTracker) error {
	data, err := skillhash.ReadFileWithLimits(src, tracker.Limits())
	if err != nil {
		return err
	}
	data = []byte(capProjectMirrorTrustFrontmatter(string(data)))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mirrorFileMode(rel, mode, data))
}

func copyCanonicalResourceFile(src, dst, rel string, mode os.FileMode, tracker *skillhash.ContentLimitTracker) error {
	dstMode, err := skillhash.MirrorFileModeFromSource(rel, mode, src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_, err = skillhash.CopyFileWithLimits(src, dst, dstMode, tracker.Limits())
	return err
}
