package remoteportable

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// ParseGitTreeEntries 严格解析零分隔的 exact Git tree listing。
func ParseGitTreeEntries(output []byte) ([]GitTreeEntry, error) {
	if len(output) == 0 {
		return nil, errors.New("portable exact tree listing is empty")
	}
	entries := make([]GitTreeEntry, 0)
	seen := make(map[string]struct{})
	for record := range bytes.SplitSeq(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		metadata, filePath, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(metadata))
		if !ok || len(fields) != 3 || fields[1] != "blob" || !validPortableTreePath(string(filePath)) {
			return nil, errors.New("portable exact tree listing entry is invalid")
		}
		name := string(filePath)
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("portable exact tree repeats %q", name)
		}
		seen[name] = struct{}{}
		entries = append(entries, GitTreeEntry{Mode: fields[0], Kind: fields[1], Path: name})
	}
	slices.SortFunc(entries, func(left, right GitTreeEntry) int { return strings.Compare(left.Path, right.Path) })
	return entries, nil
}

func validPortableTreePath(value string) bool {
	return value != "" && !strings.HasPrefix(value, "/") && path.Clean(value) == value && !strings.ContainsAny(value, "\\\x00\r\n\t")
}
