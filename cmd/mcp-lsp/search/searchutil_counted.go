package search

import (
	"cmp"
	"fmt"
	"sort"
	"strings"
)

// CountedSearchResult 保存限制前精确总数和实际保留的命中。
type CountedSearchResult struct {
	Matches   []SearchMatch
	Total     int
	Truncated bool
}

// searchMatchCollector 让旧的遇限停止语义和精确计数语义复用同一遍历。
type searchMatchCollector struct {
	maxResults  int
	stopOnLimit bool
	filter      bool
	matches     []SearchMatch
	total       int
	seen        map[string]struct{}
}

func newSearchMatchCollector(maxResults int, stopOnLimit, filter bool) *searchMatchCollector {
	collector := &searchMatchCollector{
		maxResults:  maxResults,
		stopOnLimit: stopOnLimit,
		filter:      filter,
		matches:     make([]SearchMatch, 0, maxInt(maxResults, 8)),
	}
	if filter {
		collector.seen = make(map[string]struct{})
	}
	return collector
}

func (collector *searchMatchCollector) add(match SearchMatch, explicitRoot string) error {
	match.explicitHiddenRoot = explicitRoot
	if collector.filter && !collector.keep(match) {
		return nil
	}
	collector.total++
	if !collector.limitReached() {
		collector.matches = append(collector.matches, match)
		return nil
	}
	if collector.stopOnLimit {
		collector.markLimitReached()
		return errSearchResultsLimitReached
	}
	return nil
}

func (collector *searchMatchCollector) keep(match SearchMatch) bool {
	if strings.TrimSpace(match.File) == "" || shouldExcludeSearchMatch(match) {
		return false
	}
	key := fmt.Sprintf("%s:%d:%d:%s", match.AbsPath, match.Line, match.Col, match.Text)
	if _, duplicate := collector.seen[key]; duplicate {
		return false
	}
	collector.seen[key] = struct{}{}
	return true
}

func (collector *searchMatchCollector) limitReached() bool {
	return maxResultsReached(len(collector.matches), collector.maxResults)
}

func (collector *searchMatchCollector) stopBeforeNextEntry() bool {
	if !collector.stopOnLimit || !collector.limitReached() {
		return false
	}
	collector.markLimitReached()
	return true
}

func (collector *searchMatchCollector) markLimitReached() {
	if len(collector.matches) > 0 {
		collector.matches[len(collector.matches)-1].limitReached = true
	}
}

func (collector *searchMatchCollector) countedResult() CountedSearchResult {
	sort.Slice(collector.matches, func(i, j int) bool {
		a, b := collector.matches[i], collector.matches[j]
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col), strings.Compare(a.Text, b.Text)) < 0
	})
	return CountedSearchResult{
		Matches: collector.matches, Total: collector.total, Truncated: len(collector.matches) < collector.total,
	}
}
