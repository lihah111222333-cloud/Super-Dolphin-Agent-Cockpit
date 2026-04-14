package memory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultManifestFileLimit = 200

type ManifestBuilder struct {
	MaxFiles int
}

func NewManifestBuilder() *ManifestBuilder {
	return &ManifestBuilder{MaxFiles: defaultManifestFileLimit}
}

func (b *ManifestBuilder) BuildManifest(memoryRoot string) ([]MemoryEntry, error) {
	root := strings.TrimSpace(memoryRoot)
	if root == "" {
		return nil, errors.New("memory root dir is empty")
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := scanManifestEntries(root)
	if err != nil {
		return nil, err
	}
	sortManifestEntries(entries)
	if maxFiles := b.maxFiles(); len(entries) > maxFiles {
		entries = entries[:maxFiles]
	}
	return cloneEntries(entries), nil
}

func (b *ManifestBuilder) maxFiles() int {
	if b == nil || b.MaxFiles <= 0 {
		return defaultManifestFileLimit
	}
	return b.MaxFiles
}

func isMemoryFile(path string) bool {
	return filepath.Ext(path) == ".md" && filepath.Base(path) != memoryIndexFileName
}

func scanManifestEntries(root string) ([]MemoryEntry, error) {
	entries := make([]MemoryEntry, 0, 16)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		entry, ok := manifestEntryFromPath(path, d, walkErr)
		if ok {
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}

func manifestEntryFromPath(path string, d fs.DirEntry, walkErr error) (MemoryEntry, bool) {
	if walkErr != nil || d == nil || d.IsDir() || !isMemoryFile(path) {
		return MemoryEntry{}, false
	}
	entry, err := readMemoryEntryFile(path)
	if err != nil {
		return MemoryEntry{}, false
	}
	entry.Content = ""
	return entry, true
}

func sortManifestEntries(entries []MemoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].FilePath < entries[j].FilePath
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
}
