package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (s *service) writeProjectLocal(ctx context.Context, cwd, path, content, scope, personalType string) (any, error) {
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
	name := filepath.Base(filepath.Dir(path))
	s.publishSkillsChanged(ctx, "local_write", name, scope)
	result := map[string]any{"ok": true, "path": path, "dir": filepath.Dir(path), "bytes": len(content)}
	return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, cwd, scope, personalType, name)), nil
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

// importSource 导入source。
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

// detectImportMode 根据请求模式和目录内容判断 skill 导入方式。
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

// importBatchSource 导入batchsource。
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

// importSkillUnit 导入技能unit。
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

// copySkillDir 复制技能目录。
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
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
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
