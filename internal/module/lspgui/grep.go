package lspgui

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var errStopSearch = errors.New("stop search")

const (
	maxSearchResults   = 50
	maxSearchFileBytes = 1024 * 1024
)

type lineMatcher struct {
	needle        string
	regex         *regexp.Regexp
	caseSensitive bool
}

func (s *service) HandleGrep(ctx context.Context, p grepParams) (any, error) {
	switch strings.TrimSpace(p.Action) {
	case "text_search":
		return s.textSearch(ctx, p)
	case "ast_search":
		return stubSearchResult(), nil
	default:
		return nil, errors.New("unsupported lsp/gui_grep action")
	}
}

func (s *service) textSearch(ctx context.Context, p grepParams) (searchResult, error) {
	matcher, err := newLineMatcher(p.Query, p.Regex, p.CaseSensitive)
	if err != nil {
		return searchResult{}, err
	}
	root, err := s.resolvePath(p.Path)
	if err != nil {
		return searchResult{}, err
	}
	results, err := searchPath(
		ctx,
		root,
		strings.TrimSpace(p.Glob),
		min(defaultLimit(p.MaxResults, 30), maxSearchResults),
		matcher,
	)
	if err != nil {
		return searchResult{}, err
	}
	return searchResult{Results: results}, nil
}

func newLineMatcher(query string, regexMode, caseSensitive bool) (lineMatcher, error) {
	needle := strings.TrimSpace(query)
	if needle == "" {
		return lineMatcher{}, errors.New("query is required")
	}
	if regexMode {
		if !caseSensitive {
			needle = "(?i)" + needle
		}
		re, err := regexp.Compile(needle)
		if err != nil {
			return lineMatcher{}, err
		}
		return lineMatcher{regex: re, caseSensitive: caseSensitive}, nil
	}
	if caseSensitive {
		return lineMatcher{needle: needle, caseSensitive: true}, nil
	}
	return lineMatcher{needle: strings.ToLower(needle)}, nil
}

func (m lineMatcher) find(line string) (int, bool) {
	if m.regex != nil {
		loc := m.regex.FindStringIndex(line)
		if loc == nil {
			return 0, false
		}
		return utf8.RuneCountInString(line[:loc[0]]), true
	}
	haystack := line
	needle := m.needle
	if !m.caseSensitive {
		haystack = strings.ToLower(line)
	}
	idx := strings.Index(haystack, needle)
	if idx < 0 {
		return 0, false
	}
	return utf8.RuneCountInString(line[:idx]), true
}

func searchPath(
	ctx context.Context,
	root, glob string,
	maxResults int,
	matcher lineMatcher,
) ([]searchMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return searchFile(ctx, root, root, glob, maxResults, matcher)
	}
	results := make([]searchMatch, 0, maxResults)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		found, err := searchFile(ctx, root, path, glob, maxResults-len(results), matcher)
		if err != nil {
			return nil
		}
		results = append(results, found...)
		if len(results) >= maxResults {
			return errStopSearch
		}
		return nil
	})
	if errors.Is(err, errStopSearch) {
		return results, nil
	}
	return results, err
}

func searchFile(
	ctx context.Context,
	root, path, glob string,
	remaining int,
	matcher lineMatcher,
) ([]searchMatch, error) {
	if remaining <= 0 || !matchesGlob(root, path, glob) {
		return []searchMatch{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSearchFileBytes {
		return []searchMatch{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	results := make([]searchMatch, 0, remaining)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Text()
		col, ok := matcher.find(line)
		if !ok {
			continue
		}
		results = append(results, searchMatch{
			File: filepath.ToSlash(path),
			Line: lineNum,
			Col:  col + 1,
			Text: strings.TrimSpace(line),
		})
		if len(results) >= remaining {
			break
		}
	}
	return results, scanner.Err()
}

func matchesGlob(root, candidate, glob string) bool {
	if glob == "" {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	slashGlob := filepath.ToSlash(strings.TrimSpace(glob))
	slashRel := filepath.ToSlash(rel)
	if ok, err := path.Match(slashGlob, slashRel); err == nil && ok {
		return true
	}
	ok, err := filepath.Match(glob, rel)
	return err == nil && ok
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".cache", "__pycache__", "build", "coverage", "dist", "node_modules", "vendor":
		return true
	default:
		return false
	}
}
