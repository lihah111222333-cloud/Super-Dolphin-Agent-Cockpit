package skill

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/ownerperms"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

const (
	skillScopeProject         = "project"
	skillScopePersonal        = "personal"
	skillScopeSystem          = "system"
	personalSkillTypeUser     = "user"
	personalSkillTypeAgent    = "agent"
	personalSkillTypeImported = "imported"
	// hub 只放 catalog/marketplace 数据。
	// 运行时 skill 来源只看 activePersonalSkillTypes，不能把 hub 同步给 provider。
	personalSkillTypeHub  = "hub"
	ownerIdentitySaltFile = "owner_identity.salt"
)

type ownerIdentity struct {
	OwnerKey string
	SaltPath string
}
type skillRootTarget struct {
	root         string
	scope        string
	personalType string
}
type resolvedSkillPathTarget struct {
	path         string
	root         string
	scope        string
	personalType string
	skillDir     string
	underSkill   bool
}

// resolveCanonicalRoots 返回运行时真正读取的 skill 目录。
// .claude/.agents 这类 provider mirror 是生成物，不在这里。
// activePersonalSkillTypes 是运行时会读取的 personal 类型。
// hub 不在这里；新增类型前要先想清楚怎么同步和清理 mirror。
func activePersonalSkillTypes() []string {
	return []string{personalSkillTypeUser, personalSkillTypeAgent, personalSkillTypeImported}
}
func defaultProjectSkillsRoot(projectRoot string) string {
	if projectRoot = strings.TrimSpace(projectRoot); projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".agent", "skills")
}
func defaultSuperDolphinHome() string {
	if override := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".super-dolphin")
	}
	return filepath.Join(os.TempDir(), "super-dolphin")
}
func defaultOwnerOSUID() string { return fmt.Sprint(os.Getuid()) }
func defaultAppProfile() string {
	if profile := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_APP_PROFILE")); profile != "" {
		return profile
	}
	return "default"
}
func resolveOwnerIdentity(superDolphinHome, osUID, appProfile string) (ownerIdentity, error) {
	normalizedHome, err := normalizedSuperDolphinHome(superDolphinHome)
	if err != nil {
		return ownerIdentity{}, err
	}
	saltPath := filepath.Join(normalizedHome, ownerIdentitySaltFile)
	salt, err := readOrCreateOwnerIdentitySalt(normalizedHome, saltPath)
	if err != nil {
		return ownerIdentity{}, err
	}
	return ownerIdentity{
		OwnerKey: deriveOwnerKey(salt, normalizedHome, osUID, appProfile),
		SaltPath: saltPath,
	}, nil
}
func normalizedSuperDolphinHome(superDolphinHome string) (string, error) {
	home := strings.TrimSpace(superDolphinHome)
	if home == "" {
		return "", fmt.Errorf("super dolphin home is required")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("normalize super dolphin home: %w", err)
	}
	return filepath.Clean(abs), nil
}

func readOrCreateOwnerIdentitySalt(superDolphinHome, saltPath string) ([]byte, error) {
	if err := os.MkdirAll(superDolphinHome, 0o700); err != nil {
		return nil, fmt.Errorf("create super dolphin home: %w", err)
	}
	salt, err := readOwnerIdentitySalt(saltPath)
	if err == nil {
		return salt, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	return createOwnerIdentitySalt(saltPath)
}
func readOwnerIdentitySalt(saltPath string) ([]byte, error) {
	info, err := os.Stat(saltPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("owner identity salt is not a regular file")
	}
	if err := ownerperms.ValidateOwnerIdentitySaltPermissions(saltPath, info); err != nil {
		return nil, err
	}
	return readNonEmptyOwnerIdentitySalt(saltPath)
}
func readNonEmptyOwnerIdentitySalt(saltPath string) ([]byte, error) {
	salt, err := os.ReadFile(saltPath)
	if err != nil {
		return nil, fmt.Errorf("read owner identity salt: %w", err)
	}
	if len(salt) == 0 {
		return nil, fmt.Errorf("owner identity salt is empty")
	}
	return salt, nil
}
func createOwnerIdentitySalt(saltPath string) ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate owner identity salt: %w", err)
	}
	if err := writeOwnerIdentitySalt(saltPath, salt); err != nil {
		return nil, err
	}
	return salt, nil
}
func writeOwnerIdentitySalt(saltPath string, salt []byte) error {
	file, err := os.OpenFile(saltPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create owner identity salt: %w", err)
	}
	if _, err := file.Write(salt); err != nil {
		return closeOwnerIdentitySaltAfterWrite(file, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close owner identity salt: %w", err)
	}
	if err := ownerperms.SecureOwnerIdentitySaltPermissions(saltPath); err != nil {
		return err
	}
	return nil
}
func closeOwnerIdentitySaltAfterWrite(file *os.File, writeErr error) error {
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("write owner identity salt: %w; close: %v", writeErr, closeErr)
	}
	return fmt.Errorf("write owner identity salt: %w", writeErr)
}
func deriveOwnerKey(salt []byte, normalizedHome, osUID, appProfile string) string {
	payload := normalizedHome + "\n" + strings.TrimSpace(osUID) + "\n" + strings.TrimSpace(appProfile)
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write([]byte(payload))
	return "sd_owner:" + hex.EncodeToString(mac.Sum(nil))
}

// normalizeSkillTarget 规范化用户传入的 skill 目标路径。
func normalizeSkillTarget(scope, personalType string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", skillScopeProject:
		return skillScopeProject, "", nil
	case skillScopePersonal:
		normalizedType := strings.ToLower(strings.TrimSpace(personalType))
		for _, activeType := range activePersonalSkillTypes() {
			if normalizedType == activeType {
				return skillScopePersonal, normalizedType, nil
			}
		}
		return "", "", fmt.Errorf("%w: personal_type %q", ErrInvalidSkillScope, personalType)
	case skillScopeSystem:
		return "", "", ErrSkillSystemScopeRemoved
	default:
		return "", "", fmt.Errorf("%w: %s", ErrInvalidSkillScope, scope)
	}
}

// listSkillFiles 列出 skill 目录下可编辑的文件。
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
		ni, _ := files[i]["name"].(string)
		nj, _ := files[j]["name"].(string)
		return strings.ToLower(ni) < strings.ToLower(nj)
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
	return !pathutil.ContainsPath(rootPath, targetPath), nil
}

// ensureWritableSkillPathInsideRoot 确认待写路径仍在允许的 skill 根目录内。
func ensureWritableSkillPathInsideRoot(root, target string) error {
	rootAbs, targetAbs, err := cleanWritableRootAndTarget(root, target)
	if err != nil {
		return err
	}
	if rel, relErr := filepath.Rel(rootAbs, targetAbs); relErr == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if err := rejectWritableSymlinkPath(rootAbs, rel); err != nil {
			return err
		}
	}
	if !pathutil.ContainsPath(rootAbs, targetAbs) {
		return fmt.Errorf("path escapes skills root: %s", target)
	}
	rel, err := filepath.Rel(rootAbs, filepath.Dir(targetAbs))
	if err != nil {
		return err
	}
	return rejectWritableSymlinkPath(rootAbs, rel)
}

func cleanWritableRootAndTarget(root, target string) (string, string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(rootAbs), filepath.Clean(targetAbs), nil
}

// rejectWritableSymlinkPath 拒绝会穿出根目录的符号链接路径。
func rejectWritableSymlinkPath(rootAbs, rel string) error {
	if err := rejectWritableSymlinkComponentIfExists(rootAbs); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		rootAbs = filepath.Join(rootAbs, part)
		if err := rejectWritableSymlinkComponentIfExists(rootAbs); err != nil {
			return err
		}
	}
	return nil
}

func rejectWritableSymlinkComponentIfExists(path string) error {
	err := rejectWritableSymlinkComponent(path)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return err
	}
}

func rejectWritableSymlinkComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill write path contains symlink: %s", path)
	}
	return nil
}

func (s *service) allSkillRoots(cwd string) []string {
	targets := s.allSkillRootTargets(cwd)
	roots := make([]string, 0, len(targets))
	for _, target := range targets {
		roots = append(roots, target.root)
	}
	return roots
}

func (s *service) allSkillRootTargets(cwd string) []skillRootTarget {
	targets := make([]skillRootTarget, 0, 5)
	seen := make(map[string]struct{}, 5)
	appendUniqueSkillRoot := func(root, scope, personalType string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		cleaned := filepath.Clean(root)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		targets = append(targets, skillRootTarget{root: cleaned, scope: scope, personalType: personalType})
	}
	appendUniqueSkillRoot(s.projectSkillsRootForCWD(cwd), skillScopeProject, "")
	for _, personalType := range activePersonalSkillTypes() {
		appendUniqueSkillRoot(s.personalSkillsRoot(personalType), skillScopePersonal, personalType)
	}
	return targets
}

// resolveExistingSkillPathTarget 解析已存在 skill 路径的真实目标。
func (s *service) resolveExistingSkillPathTarget(target, cwd string) (resolvedSkillPathTarget, error) {
	roots := s.allSkillRootTargets(cwd)
	if len(roots) == 0 {
		return resolvedSkillPathTarget{}, errors.New("skills root is not configured")
	}
	targetPath, err := canonicalProjectPath(target)
	if err != nil {
		return resolvedSkillPathTarget{}, err
	}
	for _, root := range roots {
		rootPath, err := canonicalProjectPath(root.root)
		if err != nil {
			return resolvedSkillPathTarget{}, err
		}
		outside, err := pathEscapesRoot(rootPath, targetPath)
		if err != nil {
			return resolvedSkillPathTarget{}, err
		}
		if !outside {
			skillDir, underSkill, err := skillDirForResolvedPath(rootPath, targetPath)
			if err != nil {
				return resolvedSkillPathTarget{}, err
			}
			return resolvedSkillPathTarget{
				path:         targetPath,
				root:         rootPath,
				scope:        root.scope,
				personalType: root.personalType,
				skillDir:     skillDir,
				underSkill:   underSkill,
			}, nil
		}
	}
	return resolvedSkillPathTarget{}, fmt.Errorf("path escapes skills root: %s", target)
}

// skillDirForResolvedPath 根据解析后的路径找到所属 skill 目录。
func skillDirForResolvedPath(rootPath, targetPath string) (string, bool, error) {
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return "", false, err
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return "", false, nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "." || parts[0] == ".." || parts[0] == "" {
		return "", false, fmt.Errorf("path escapes skills root: %s", targetPath)
	}
	return filepath.Join(rootPath, parts[0]), true, nil
}

// ensurePathInEffectiveSet 确认路径属于当前有效 skill 集合。
func (s *service) ensurePathInEffectiveSet(ctx context.Context, cwd, path string) error {
	resolved, err := s.resolveExistingSkillPathTarget(path, cwd)
	if err != nil {
		return err
	}
	if !resolved.underSkill {
		return nil
	}
	records, conflicts, err := s.canonicalEffectiveSet(ctx, cwd)
	if err != nil {
		return err
	}
	if conflict, ok := conflictForSkillDir(conflicts, resolved.skillDir); ok {
		return skillSameNameConflictError{Conflicts: []canonicalSkillConflict{conflict}}
	}
	for _, record := range records {
		if sameCanonicalDir(record.Dir, resolved.skillDir) {
			return nil
		}
	}
	return fmt.Errorf("skill path is not in effective skill set: %s", path)
}

// conflictForSkillDir 构造指定 skill 目录的冲突描述。
func conflictForSkillDir(conflicts []canonicalSkillConflict, skillDir string) (canonicalSkillConflict, bool) {
	for _, conflict := range conflicts {
		for _, source := range conflict.Sources {
			if sameCanonicalDir(source.Dir, skillDir) {
				return conflict, true
			}
		}
	}
	return canonicalSkillConflict{}, false
}

func sameCanonicalDir(left, right string) bool {
	leftResolved, leftErr := canonicalProjectPath(left)
	rightResolved, rightErr := canonicalProjectPath(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (s *service) resolveWriteLocalPath(path, cwd, scope, personalType string) (string, error) {
	if filepath.IsAbs(strings.TrimSpace(path)) {
		resolved, err := s.resolveExistingSkillPathTarget(path, cwd)
		if err != nil {
			return "", err
		}
		if err := validateResolvedSkillPathTarget(resolved, scope, personalType); err != nil {
			return "", err
		}
		return resolved.path, nil
	}
	return s.resolveScopedSkillPath(cwd, path, scope, personalType)
}

func validateResolvedSkillPathTarget(resolved resolvedSkillPathTarget, scope, personalType string) error {
	if resolved.scope == scope && resolved.personalType == personalType {
		return nil
	}
	return fmt.Errorf("%w: path scope %s/%s does not match requested scope %s/%s",
		ErrInvalidSkillScope, resolved.scope, resolved.personalType, scope, personalType)
}
