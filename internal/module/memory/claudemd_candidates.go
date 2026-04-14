package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type claudeRuleMetadata struct {
	Description string
	Globs       []string
}

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

func resolveClaudeMdCandidatePath(path string) (string, string, bool) {
	path = cleanClaudeMdPath(path)
	if path == "" {
		return "", "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", "", false
	}
	resolved := path
	if symlinked, err := filepath.EvalSymlinks(path); err == nil {
		resolved = cleanClaudeMdPath(symlinked)
	}
	digestInput := resolved + "\n" + info.ModTime().UTC().Format(timeLayoutRFC3339Nano) + "\n" + int64String(info.Size())
	digest := sha256.Sum256([]byte(digestInput))
	return resolved, hex.EncodeToString(digest[:]), true
}

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

func ruleMarkdownFiles(root string) []string {
	root = cleanClaudeMdPath(root)
	if root == "" {
		return nil
	}
	files := make([]string, 0, 8)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			files = append(files, cleanClaudeMdPath(path))
		}
		return nil
	})
	sort.Strings(files)
	return files
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

func autoMemRoot(cfg ClaudeMdResolveConfig) string {
	memoryCfg := memoryConfig(cfg.MemoryConfig)
	projectRoot := strings.TrimSpace(cfg.BuildCtx.GitRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.BuildCtx.CWD)
	}
	root, err := resolvedStoreRoot(memoryCfg.RootDir, projectRoot, memoryCfg.AutoMemPathOverride)
	if err != nil {
		return ""
	}
	return cleanClaudeMdPath(root)
}

func providerTeamMemPath(team *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if team == nil {
		return ""
	}
	return cleanClaudeMdPath(team.GetTeamMemPath(buildCtx))
}

func providerTeamMemEntrypoint(team *TeamMemoryManager, buildCtx contract.BuildCtx) string {
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

const timeLayoutRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
