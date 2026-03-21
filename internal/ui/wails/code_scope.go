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
)

const (
	codeSearchMaxDepth = 10
	codeSearchTimeout  = 2 * time.Second
)

var errSearchLimit = errors.New("wails ui code search limit")

type scopedPath struct {
	Root     string
	Abs      string
	Relative string
}

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

func resolveSaveTarget(raw string, roots []string, createNew bool) (scopedPath, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return scopedPath{}, errors.New("ui/code/save: filePath is required")
	}
	if filepath.IsAbs(value) {
		return matchAbsoluteTarget(value, roots, createNew)
	}
	if target, ok := firstExistingRelativeTarget(value, roots); ok {
		return target, nil
	}
	if !createNew {
		return scopedPath{}, fmt.Errorf("ui/code/save: file %q does not exist; pass createNew=true to create it", value)
	}
	return scopedCandidate(roots[0], filepath.Join(roots[0], value), true)
}

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

func matchAbsoluteTarget(raw string, roots []string, allowMissing bool) (scopedPath, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return scopedPath{}, err
	}
	var lastErr error
	for _, root := range roots {
		target, err := scopedCandidate(root, absPath, allowMissing)
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

func firstExistingRelativeTarget(raw string, roots []string) (scopedPath, bool) {
	for _, root := range roots {
		target, err := scopedCandidate(root, filepath.Join(root, raw), false)
		if err == nil {
			return target, true
		}
	}
	return scopedPath{}, false
}

func findScopedFiles(ctx context.Context, raw string, roots []string, limit int) ([]scopedPath, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false, errors.New("ui/code/locate: filePath is required")
	}
	if filepath.IsAbs(value) {
		target, err := matchAbsoluteTarget(value, roots, false)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return []scopedPath{}, false, nil
			}
			return nil, false, err
		}
		return []scopedPath{target}, false, nil
	}
	return walkScopedMatches(ctx, value, roots, limit)
}

func walkScopedMatches(ctx context.Context, raw string, roots []string, limit int) ([]scopedPath, bool, error) {
	searchCtx, cancel := context.WithTimeout(ctx, codeSearchTimeout)
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
			return nil
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
			return nil
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

func shouldSkipCodeSearchDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".cache", "__pycache__", "node_modules", "vendor", "dist", "build", "coverage":
		return true
	default:
		return false
	}
}

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

func matchesScopedTarget(root, candidate, target, base string) bool {
	return matchesScopedTargetForOS(runtime.GOOS, root, candidate, target, base)
}

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

func pathEqual(left, right string, ignoreCase bool) bool {
	if ignoreCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathHasSuffix(value, suffix string, ignoreCase bool) bool {
	if ignoreCase {
		return strings.HasSuffix(strings.ToLower(value), strings.ToLower(suffix))
	}
	return strings.HasSuffix(value, suffix)
}

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

func scopedCandidate(root, candidate string, allowMissing bool) (scopedPath, error) {
	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return scopedPath{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil && (!allowMissing || !errors.Is(err, os.ErrNotExist)) {
		return scopedPath{}, err
	}
	if err == nil && info.IsDir() {
		return scopedPath{}, fmt.Errorf("path %q is a directory", absPath)
	}
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

func secureRelativeToRoot(root, candidate string) (string, error) {
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
