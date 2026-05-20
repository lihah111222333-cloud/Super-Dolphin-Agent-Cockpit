package skillhash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Content(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func Dir(root string) (string, error) {
	var parts []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts = append(parts, filepath.ToSlash(rel)+"\x00"+Content(string(data)))
		return nil
	}); err != nil {
		return "", fmt.Errorf("skill dir content hash %q: %w", root, err)
	}
	sort.Strings(parts)
	return Content(strings.Join(parts, "\x00")), nil
}

func ExistingDir(root string) (string, error) {
	hash, err := Dir(root)
	switch {
	case err == nil:
		return hash, nil
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	default:
		return "", err
	}
}
