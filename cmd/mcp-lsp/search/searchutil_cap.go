package search

func capSearchMatchesPerFile(matches []SearchMatch, total, perFileLimit int) ([]SearchMatch, int, bool) {
	if perFileLimit <= 0 {
		return matches, total, false
	}
	capped := make([]SearchMatch, 0, len(matches))
	counts := make(map[string]int)
	truncated := false
	for _, match := range matches {
		if counts[match.File] >= perFileLimit {
			truncated = true
			continue
		}
		counts[match.File]++
		capped = append(capped, match)
	}
	if !truncated {
		return matches, total, false
	}
	return capped, total, true
}
