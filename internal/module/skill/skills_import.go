package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	importModeAuto   = "auto"
	importModeSingle = "single"
	importModeBatch  = "batch"
)

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

func (s *service) importSources(sources []string, singleName, cwd, scope, mode string) ([]map[string]any, []map[string]any) {
	results := make([]map[string]any, 0, len(sources))
	failures := make([]map[string]any, 0)
	for _, source := range sources {
		sourceResults, sourceFailures := s.importSource(source, singleName, cwd, scope, mode)
		results = append(results, sourceResults...)
		failures = append(failures, sourceFailures...)
	}
	return results, failures
}

func (s *service) importSource(source, name, cwd, scope, mode string) ([]map[string]any, []map[string]any) {
	resolvedSource, err := validateImportSource(source)
	if err != nil {
		return nil, []map[string]any{importFailure(source, err)}
	}
	resolvedMode, err := detectImportMode(resolvedSource, mode)
	if err != nil {
		return nil, []map[string]any{importFailure(source, err)}
	}
	if resolvedMode == importModeSingle {
		result, err := s.importSkillUnit(resolvedSource, name, cwd, scope, source)
		if err != nil {
			return nil, []map[string]any{importFailure(source, err)}
		}
		return []map[string]any{result}, nil
	}
	if strings.TrimSpace(name) != "" {
		return nil, []map[string]any{importFailure(source, errors.New("name is not allowed in batch mode"))}
	}
	return s.importBatchSource(resolvedSource, cwd, scope)
}

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

func (s *service) importBatchSource(container, cwd, scope string) ([]map[string]any, []map[string]any) {
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
		result, err := s.importSkillUnit(skillDir, "", cwd, scope, skillDir)
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

func (s *service) importSkillUnit(resolvedSource, name, cwd, scope, originalSource string) (map[string]any, error) {
	normalizedScope, err := normalizeSkillScope(scope)
	if err != nil {
		return nil, err
	}
	targetName := strings.TrimSpace(name)
	if targetName == "" {
		targetName = filepath.Base(resolvedSource)
	}
	if err := RequireSkillSystemReview(normalizedScope, skillSlug(targetName), skillDirContentHash(resolvedSource), RepoFingerprint(cwd), "", ""); err != nil {
		return nil, err
	}
	root, err := s.prepareScopedSkillsRoot(cwd, scope)
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
	files, bytes, err := copySkillDir(resolvedSource, targetDir)
	if err != nil {
		_ = os.RemoveAll(targetDir)
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
