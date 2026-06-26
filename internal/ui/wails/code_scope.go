package wails

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const (
	// 代码路径搜索的深度和超时上限，防止 UI 请求扫穿整个仓库。
	codeSearchMaxDepth = 10
	codeSearchTimeout  = 2 * time.Second
)

var (
	// errSearchLimit 表示搜索结果达到上限，调用方应返回 truncated。
	errSearchLimit           = errors.New("wails ui code search limit")
	errCodeSaveFileMustExist = errors.New("file does not exist; saving new files is not supported via this API")
)

// scopedPath 表示已经通过项目根校验的本地路径。
type scopedPath struct {
	Root     string
	Abs      string
	Relative string
}

// resolveScopeRoots 根据前端传入的项目选择解析可访问根目录。
func resolveScopeRoots(project string, projects []string, catalog scopeCatalog) ([]string, error) {
	roots := make([]string, 0, len(projects)+1)
	seen := map[string]struct{}{}
	for _, entry := range scopeEntries(project, projects) {
		root, err := catalog.resolve(entry)
		if err != nil {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no valid project root found")
	}
	return roots, nil
}

// scopeEntries 合并单项目和多项目选择，空选择时使用当前项目。
func scopeEntries(project string, projects []string) []string {
	entries := make([]string, 0, len(projects)+1)
	if strings.TrimSpace(project) != "" {
		entries = append(entries, project)
	}
	entries = append(entries, projects...)
	if len(entries) == 0 {
		entries = append(entries, ".")
	}
	return entries
}

// resolveSaveTarget 解析保存目标；当前 API 只允许覆盖已存在文件。
func resolveSaveTarget(raw string, roots []string, _ bool) (scopedPath, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return scopedPath{}, errors.New("ui/code/save: filePath is required")
	}
	if filepath.IsAbs(value) {
		target, err := matchAbsoluteTarget(value, roots, false)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return scopedPath{}, errCodeSaveFileMustExist
			}
			return scopedPath{}, err
		}
		return target, nil
	}
	if target, ok := firstExistingRelativeTarget(value, roots); ok {
		return target, nil
	}
	return scopedPath{}, errCodeSaveFileMustExist
}

// resolveOpenTarget 解析打开目标，支持相对路径模糊查找但仍限制在 roots 内。
func resolveOpenTarget(ctx context.Context, raw string, roots []string) (scopedPath, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return scopedPath{}, errors.New("ui/code/open: filePath is required")
	}
	if filepath.IsAbs(value) {
		return matchAbsoluteTarget(value, roots, false)
	}
	if target, ok := firstExistingRelativeTarget(value, roots); ok {
		return target, nil
	}
	matches, _, err := findScopedFiles(ctx, value, roots, 1)
	if err != nil {
		return scopedPath{}, err
	}
	if len(matches) == 0 {
		return scopedPath{}, fs.ErrNotExist
	}
	return matches[0], nil
}

// matchAbsoluteTarget 校验绝对路径是否属于任一允许根目录。
func matchAbsoluteTarget(raw string, roots []string, createNew bool) (scopedPath, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return scopedPath{}, err
	}
	var lastErr error
	for _, root := range roots {
		// 误判防护：requestScopeRoots 产生的每个 root 都必须再经 scopedCandidate 校验。
		target, err := scopedCandidate(root, absPath, createNew)
		if err == nil {
			return target, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fs.ErrNotExist
	}
	return scopedPath{}, lastErr
}

// firstExistingRelativeTarget 在多个根目录中寻找第一个已存在的相对路径。
func firstExistingRelativeTarget(raw string, roots []string) (scopedPath, bool) {
	for _, root := range roots {
		target, err := scopedCandidate(root, filepath.Join(root, raw), false)
		if err == nil {
			return target, true
		}
	}
	return scopedPath{}, false
}

// findScopedFiles 在允许根目录内查找匹配文件，并返回是否因为 limit 截断。
func findScopedFiles(ctx context.Context, raw string, roots []string, limit int) ([]scopedPath, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false, errors.New("ui/code/locate: filePath is required")
	}
	if filepath.IsAbs(value) {
		target, err := matchAbsoluteTarget(value, roots, false)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false, err
			}
			return nil, false, err
		}
		return []scopedPath{target}, false, nil
	}
	return walkScopedMatches(ctx, value, roots, limit)
}

// walkScopedMatches 在受限时间内遍历 roots，收集相对路径或文件名匹配。
func walkScopedMatches(ctx context.Context, raw string, roots []string, limit int) ([]scopedPath, bool, error) {
	searchCtx, cancel := platformconfig.WithTimeout(ctx, codeSearchTimeout)
	defer cancel()
	target := filepath.ToSlash(filepath.Clean(raw))
	base := path.Base(target)
	seen := map[string]struct{}{}
	matches := make([]scopedPath, 0, limit)
	truncated := false
	for _, root := range roots {
		err := collectRootMatches(searchCtx, root, target, base, limit, seen, &matches, &truncated)
		if err != nil && !errors.Is(err, errSearchLimit) {
			return nil, false, err
		}
		if errors.Is(err, errSearchLimit) {
			break
		}
	}
	sortScopedPaths(matches)
	return matches, truncated, nil
}

// collectRootMatches 遍历单个根目录并收集安全校验后的候选文件。
func collectRootMatches(
	ctx context.Context,
	root, target, base string,
	limit int,
	seen map[string]struct{},
	matches *[]scopedPath,
	truncated *bool,
) error {
	return filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if shouldSkipCodeSearchDir(entry.Name()) || exceedsSearchDepth(root, candidate) {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchesScopedTarget(root, candidate, target, base) {
			return nil
		}
		pathInfo, err := scopedCandidate(root, candidate, false)
		if err != nil {
			return err
		}
		if _, ok := seen[pathInfo.Abs]; ok {
			return nil
		}
		seen[pathInfo.Abs] = struct{}{}
		*matches = append(*matches, pathInfo)
		if len(*matches) >= limit {
			*truncated = true
			return errSearchLimit
		}
		return nil
	})
}

// shouldSkipCodeSearchDir 判断代码搜索应跳过的目录。
func shouldSkipCodeSearchDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".agent", ".agents", ".build-cache", ".cache", ".claude", ".git", ".workspace", ".worktrees", "__pycache__", "build", "coverage", "dist", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

// exceedsSearchDepth 判断候选路径是否超过代码搜索最大深度。
func exceedsSearchDepth(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	depth := 0
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part != "" && part != "." {
			depth++
		}
	}
	return depth > codeSearchMaxDepth
}

// matchesScopedTarget 判断候选文件是否匹配用户输入的目标路径。
func matchesScopedTarget(root, candidate, target, base string) bool {
	return matchesScopedTargetForOS(runtime.GOOS, root, candidate, target, base)
}

// matchesScopedTargetForOS 按平台大小写规则匹配候选路径。
func matchesScopedTargetForOS(goos, root, candidate, target, base string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	ignoreCase := goos == "windows"
	if pathEqual(rel, target, ignoreCase) || pathHasSuffix(rel, "/"+target, ignoreCase) {
		return true
	}
	return !strings.Contains(target, "/") && pathEqual(path.Base(rel), base, ignoreCase)
}

// pathEqual 按需忽略大小写比较路径片段。
func pathEqual(left, right string, ignoreCase bool) bool {
	if ignoreCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// pathHasSuffix 按需忽略大小写判断路径后缀。
func pathHasSuffix(value, suffix string, ignoreCase bool) bool {
	if ignoreCase {
		return strings.HasSuffix(strings.ToLower(value), strings.ToLower(suffix))
	}
	return strings.HasSuffix(value, suffix)
}

// sortScopedPaths 按相对路径短优先、字典序次之稳定排序候选。
func sortScopedPaths(items []scopedPath) {
	sort.Slice(items, func(i, j int) bool {
		left := items[i].Relative
		right := items[j].Relative
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		return left < right
	})
}

// scopedCandidate 构造已验证的 scopedPath，并拒绝目录或越界路径。
func scopedCandidate(root, candidate string, allowCreate bool) (scopedPath, error) {
	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return scopedPath{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil && (!allowCreate || !errors.Is(err, os.ErrNotExist)) {
		return scopedPath{}, err
	}
	if err == nil && info.IsDir() {
		return scopedPath{}, fmt.Errorf("path %q is a directory", absPath)
	}
	// 误判防护：scopedCandidate 始终调用 secureRelativeToRoot 阻断路径穿越。
	relative, err := secureRelativeToRoot(root, absPath)
	if err != nil {
		return scopedPath{}, err
	}
	return scopedPath{
		Root:     root,
		Abs:      absPath,
		Relative: relative,
	}, nil
}

// secureRelativeToRoot 返回候选路径相对 root 的安全路径，拒绝 root 外路径。
func secureRelativeToRoot(root, candidate string) (string, error) {
	// 守卫规则：secureRelativeToRoot 拒绝 "."、".." 和 root 外路径。
	rootReal, err := realPathForCheck(root)
	if err != nil {
		return "", err
	}
	candidateReal, err := realPathForCheck(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootReal, candidateReal)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside project cwd %q", candidate, root)
	}
	return filepath.ToSlash(rel), nil
}

// realPathForCheck 解析用于越界判断的真实路径，缺失尾部会从最近存在父目录拼回。
func realPathForCheck(path string) (string, error) {
	clean := filepath.Clean(path)
	if _, err := os.Stat(clean); err == nil {
		return filepath.EvalSymlinks(clean)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current := clean
	var suffix []string
	for {
		next := filepath.Dir(current)
		if next == current {
			return "", os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(current))
		current = next
		if _, err := os.Stat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				real = filepath.Join(real, suffix[index])
			}
			return real, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}
