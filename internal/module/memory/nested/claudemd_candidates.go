package nested

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

type claudeRuleMetadata struct {
	Description string
	Globs       []string
}

// parseClaudeRuleMetadata 解析clauderule元数据。
func parseClaudeRuleMetadata(frontmatter string) claudeRuleMetadata {
	metadata := claudeRuleMetadata{Globs: parseFrontmatterPaths(frontmatter)}
	for line := range strings.SplitSeq(strings.ReplaceAll(frontmatter, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "description", "name":
			if metadata.Description == "" {
				metadata.Description = parseScalar(value)
			}
		}
	}
	return metadata
}

func digestClaudeMdCandidates(candidates []claudeMdCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	hasher := sha256.New()
	for _, candidate := range candidates {
		hasher.Write([]byte(strings.TrimSpace(candidate.Path)))
		hasher.Write([]byte("\n" + candidate.Type + "\n" + candidate.Origin + "\n" + candidate.Digest + "\n"))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// resolveClaudeMdCandidates 解析claudemd候选项。
func resolveClaudeMdCandidates(cfg ClaudeMdResolveConfig, gate GateSnapshot) ([]claudeMdCandidate, error) {
	seen := make(map[string]struct{}, 16)
	candidates := make([]claudeMdCandidate, 0, 16)
	if err := appendManagedClaudeMdCandidates(&candidates, seen, cfg); err != nil {
		return nil, err
	}
	if err := appendUserClaudeMdCandidates(&candidates, seen, cfg); err != nil {
		return nil, err
	}
	if !gate.BareMode {
		if err := appendProjectClaudeMdCandidates(&candidates, seen, cfg); err != nil {
			return nil, err
		}
	}
	if !gate.BareMode || gate.HasAdditionalDirsForBare {
		if err := appendAdditionalDirClaudeMdCandidates(&candidates, seen, cfg); err != nil {
			return nil, err
		}
	}
	// Phase 1.6: AutoMem / TeamMem MEMORY.md are NOT appended here anymore.
	// MemoryEntrypointProvider (in the parent memory package) is the sole
	// owner of the prompt-injected MEMORY.md; duplicating it through the
	// ClaudeMd source pipeline produced double injection with divergent
	// stripping rules (frontmatter was stripped only on the entrypoint path).
	//
	// Do NOT re-add via nested unless sanitization (BOM / frontmatter / HTML
	// comments / truncation) and team secret scanning are unified with
	// MemoryEntrypointProvider; otherwise the divergence regresses.
	return candidates, nil
}

func appendManagedClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) error {
	for _, root := range managedClaudeMdRoots(cfg) {
		if err := appendProjectStyleCandidates(candidates, seen, root, sourceTypeManaged, sourceOriginManaged, false); err != nil {
			return err
		}
	}
	return nil
}

func appendUserClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) error {
	root := strings.TrimSpace(cfg.UserRoot)
	if root == "" {
		root = defaultUserClaudeMdRoot()
	}
	return appendProjectStyleCandidates(candidates, seen, root, sourceTypeUser, sourceOriginUser, false)
}

func appendProjectClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) error {
	for _, dir := range ancestorWalkDirs(cfg.BuildCtx.GitRoot, cfg.BuildCtx.CWD) {
		if err := appendProjectStyleCandidates(candidates, seen, dir, sourceTypeProject, sourceOriginProject, true); err != nil {
			return err
		}
	}
	return nil
}

func appendAdditionalDirClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) error {
	if !parseBoolEnv(envAdditionalDirectoriesClaudeMd, false) {
		return nil
	}
	for _, dir := range normalizeStringSlice(cfg.BuildCtx.AdditionalWorkingDirectories) {
		if err := appendProjectStyleCandidates(candidates, seen, dir, sourceTypeProject, sourceOriginAddDir, true); err != nil {
			return err
		}
	}
	return nil
}

// appendProjectStyleCandidates 追加项目style候选项。
func appendProjectStyleCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, dir, sourceType, origin string, includeLocal bool) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	baseDir := cleanClaudeMdPath(dir)
	if err := appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, claudeMdFileName), sourceType, origin, baseDir, "", false); err != nil {
		return err
	}
	if err := appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, ".claude", claudeMdFileName), sourceType, origin, baseDir, "", false); err != nil {
		return err
	}
	if err := appendRuleCandidates(candidates, seen, filepath.Join(baseDir, ".claude", "rules"), sourceType, origin, baseDir); err != nil {
		return err
	}
	if includeLocal {
		if err := appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, claudeLocalMdFileName), sourceTypeLocal, origin, baseDir, "", false); err != nil {
			return err
		}
	}
	return nil
}

func appendRuleCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, root, sourceType, origin, baseDir string) error {
	files, err := ruleMarkdownFiles(root)
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := appendClaudeMdCandidateAtPath(candidates, seen, path, sourceType, origin, baseDir, origin, true); err != nil {
			return err
		}
	}
	return nil
}

func appendClaudeMdCandidateAtPath(candidates *[]claudeMdCandidate, seen map[string]struct{}, path, sourceType, origin, baseDir, ruleScope string, isRule bool) error {
	return appendClaudeMdCandidate(candidates, seen, claudeMdCandidate{Path: path, Type: sourceType, Origin: origin, BaseDir: baseDir, RuleScope: ruleScope, IsRule: isRule})
}

// appendClaudeMdCandidate 追加claudemd候选项。
func appendClaudeMdCandidate(candidates *[]claudeMdCandidate, seen map[string]struct{}, candidate claudeMdCandidate) error {
	resolvedPath, digest, ok, err := resolveClaudeMdCandidatePath(candidate.Path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// Phase 2.1.B: resolveClaudeMdCandidatePath EvalSymlinks the path but
	// does NOT confirm the resolved location stays inside the candidate's
	// intended base. A symlink at <baseDir>/CLAUDE.md pointing at
	// /etc/passwd would otherwise enqueue an out-of-base file for nested
	// injection. Resolve BaseDir through symlinks too (e.g. macOS /var
	// -> /private/var) so the containment compare uses two canonical
	// paths, then reject candidates whose resolved path escapes the
	// resolved BaseDir. loadStandardClaudeMdSource performs the same check
	// at load time as defense-in-depth.
	if candidate.BaseDir != "" {
		resolvedBase, err := filepath.EvalSymlinks(candidate.BaseDir)
		if err != nil {
			return fmt.Errorf("ClaudeMd base resolve %q: %w", candidate.BaseDir, err)
		}
		if !pathutil.ContainsPath(resolvedBase, resolvedPath) {
			return fmt.Errorf("ClaudeMd candidate containment %q under %q: %w", resolvedPath, resolvedBase, memshared.ErrSafeReadContainment)
		}
	}
	if _, exists := seen[resolvedPath]; exists {
		return nil
	}
	seen[resolvedPath] = struct{}{}
	candidate.Path = resolvedPath
	candidate.Digest = digest
	*candidates = append(*candidates, candidate)
	return nil
}

// resolveClaudeMdCandidatePath 解析claudemd候选项路径。
func resolveClaudeMdCandidatePath(path string) (string, string, bool, error) {
	path = cleanClaudeMdPath(path)
	if path == "" {
		return "", "", false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("ClaudeMd candidate stat %q: %w", path, err)
	}
	if info.IsDir() {
		return "", "", false, nil
	}
	resolved := path
	if symlinked, err := filepath.EvalSymlinks(path); err == nil {
		resolved = cleanClaudeMdPath(symlinked)
	} else {
		return "", "", false, fmt.Errorf("ClaudeMd candidate resolve %q: %w", path, err)
	}
	digestInput := resolved + "\n" + info.ModTime().UTC().Format(timeLayoutRFC3339Nano) + "\n" + int64String(info.Size())
	digest := sha256.Sum256([]byte(digestInput))
	return resolved, hex.EncodeToString(digest[:]), true, nil
}

// ancestorWalkDirs 处理ancestorwalk目录。
func ancestorWalkDirs(root, cwd string) []string {
	cwd = cleanClaudeMdPath(cwd)
	if cwd == "" {
		return nil
	}
	root = cleanClaudeMdPath(root)
	if root == "" || !isAncestorOrSame(root, cwd) {
		return []string{cwd}
	}
	stack := make([]string, 0, 8)
	for dir := cwd; dir != ""; dir = filepath.Dir(dir) {
		stack = append(stack, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack
}

// ruleMarkdownFiles 处理rulemarkdown文件。
func ruleMarkdownFiles(root string) ([]string, error) {
	root = cleanClaudeMdPath(root)
	if root == "" {
		return nil, nil
	}
	if _, statFailure := os.Stat(root); statFailure != nil {
		if os.IsNotExist(statFailure) {
			return nil, nil
		}
		return nil, fmt.Errorf("nested rule markdown stat %q: %w", root, statFailure)
	}
	files := make([]string, 0, 8)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			files = append(files, cleanClaudeMdPath(path))
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("nested rule markdown walk %q: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

func managedClaudeMdRoots(cfg ClaudeMdResolveConfig) []string {
	roots := normalizeStringSlice(cfg.ManagedRoots)
	if len(roots) > 0 {
		return roots
	}
	return defaultManagedClaudeMdRoots()
}

func defaultManagedClaudeMdRoots() []string {
	if override := strings.TrimSpace(os.Getenv(envManagedClaudeMdRoot)); override != "" {
		return []string{cleanClaudeMdPath(override)}
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{cleanClaudeMdPath("/Library/Application Support/ClaudeCode")}
	case "windows":
		base := strings.TrimSpace(os.Getenv("ProgramFiles"))
		if base == "" {
			base = `C:\Program Files`
		}
		return []string{cleanClaudeMdPath(filepath.Join(base, "ClaudeCode"))}
	default:
		return []string{cleanClaudeMdPath("/etc/claude-code")}
	}
}

func defaultUserClaudeMdRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return cleanClaudeMdPath(filepath.Join(home, ".claude"))
}

func resolveTeamMemPath(team contract.TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if team == nil {
		return ""
	}
	return cleanClaudeMdPath(team.GetTeamMemPath(buildCtx))
}

func resolveTeamMemEntrypoint(team contract.TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if team == nil {
		return ""
	}
	return cleanClaudeMdPath(team.GetTeamMemEntrypoint(buildCtx))
}

func cleanClaudeMdPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

// isAncestorOrSame 判断ancestorsame是否可用。
func isAncestorOrSame(root, child string) bool {
	root = cleanClaudeMdPath(root)
	child = cleanClaudeMdPath(child)
	if root == "" || child == "" {
		return false
	}
	if root == child {
		return true
	}
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneClaudeMdSources(sources []ClaudeMdSource) []ClaudeMdSource {
	if len(sources) == 0 {
		return nil
	}
	cloned := make([]ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		cloned = append(cloned, cloneClaudeMdSource(source))
	}
	return cloned
}

func cloneClaudeMdSource(source ClaudeMdSource) ClaudeMdSource {
	source.Globs = append([]string(nil), source.Globs...)
	return source
}

func boolToken(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func int64String(value int64) string {
	return strings.TrimSpace(strconv.FormatInt(value, 10))
}

const (
	timeLayoutRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
	memoryIndexFileName   = "MEMORY.md"
)
