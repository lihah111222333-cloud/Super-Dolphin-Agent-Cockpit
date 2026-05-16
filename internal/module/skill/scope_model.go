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
	"strings"
)

const (
	skillScopeProject  = "project"
	skillScopePersonal = "personal"
	skillScopeSystem   = "system"

	personalSkillTypeUser     = "user"
	personalSkillTypeAgent    = "agent"
	personalSkillTypeImported = "imported"
	personalSkillTypeHub      = "hub"

	ownerIdentitySaltFile = "owner_identity.salt"
)

type canonicalRoots struct {
	Project  string
	Personal map[string]string
}

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

func resolveCanonicalRoots(projectRoot, home string) canonicalRoots {
	superDolphinHome := filepath.Join(strings.TrimSpace(home), ".super-dolphin")
	return canonicalRoots{
		Project: defaultProjectSkillsRoot(projectRoot),
		Personal: map[string]string{
			personalSkillTypeUser:     filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser),
			personalSkillTypeAgent:    filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeAgent),
			personalSkillTypeImported: filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeImported),
			personalSkillTypeHub:      filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeHub),
		},
	}
}

func defaultProjectSkillsRoot(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
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

func defaultOwnerOSUID() string {
	return fmt.Sprint(os.Getuid())
}

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
	if got := info.Mode().Perm(); got != 0o600 {
		return nil, fmt.Errorf("owner identity salt permissions %v are not 0600", got)
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

func normalizeSkillTarget(scope, personalType string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", skillScopeProject:
		return skillScopeProject, "", nil
	case skillScopePersonal:
		normalizedType := strings.ToLower(strings.TrimSpace(personalType))
		switch normalizedType {
		case personalSkillTypeUser, personalSkillTypeAgent, personalSkillTypeImported, personalSkillTypeHub:
			return skillScopePersonal, normalizedType, nil
		default:
			return "", "", fmt.Errorf("%w: personal_type %q", ErrInvalidSkillScope, personalType)
		}
	case skillScopeSystem:
		return "", "", ErrSkillSystemScopeRemoved
	default:
		return "", "", fmt.Errorf("%w: %s", ErrInvalidSkillScope, scope)
	}
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
	for _, personalType := range []string{personalSkillTypeUser, personalSkillTypeAgent, personalSkillTypeImported, personalSkillTypeHub} {
		appendUniqueSkillRoot(s.personalSkillsRoot(personalType), skillScopePersonal, personalType)
	}
	return targets
}

func (s *service) resolveExistingSkillPath(target, cwd string) (string, error) {
	resolved, err := s.resolveExistingSkillPathTarget(target, cwd)
	if err != nil {
		return "", err
	}
	return resolved.path, nil
}

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
