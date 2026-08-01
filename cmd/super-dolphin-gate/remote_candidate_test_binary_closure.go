package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type remoteCandidateGoListPackage struct {
	ImportPath, Dir                                          string
	GoFiles, CgoFiles, TestGoFiles, XTestGoFiles, EmbedFiles []string
}

// semanticGoTestCompileClosure hashes only candidate-tree inputs from a Go
// list closure. Toolchain, standard-library and module-cache paths are never
// read: they have independent runtime/toolchain/module identities.
func semanticGoTestCompileClosure(root string, data []byte) (string, error) {
	cleanRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(cleanRoot) || filepath.Clean(cleanRoot) != cleanRoot {
		return "", errors.New("candidate test compile closure root is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	records := make([]string, 0)
	for decoder.More() {
		var pkg remoteCandidateGoListPackage
		if err := decoder.Decode(&pkg); err != nil {
			return "", err
		}
		if pkg.ImportPath == "" || pkg.Dir == "" {
			return "", errors.New("go list compile closure package is incomplete")
		}
		inside, err := remoteCandidatePathInside(cleanRoot, pkg.Dir)
		if err != nil {
			return "", err
		}
		if !inside {
			continue
		}
		files := append(append(append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...), pkg.TestGoFiles...), append(pkg.XTestGoFiles, pkg.EmbedFiles...)...)
		for _, name := range files {
			if name == "" || filepath.Base(name) != name {
				return "", errors.New("go list compile closure file is invalid")
			}
			filePath := filepath.Join(pkg.Dir, name)
			if inside, err := remoteCandidatePathInside(cleanRoot, filePath); err != nil || !inside {
				return "", errors.New("go list compile closure file escaped candidate tree")
			}
			info, err := os.Lstat(filePath)
			if err != nil || !info.Mode().IsRegular() {
				return "", errors.New("go list compile closure file is unavailable")
			}
			contents, err := os.ReadFile(filePath)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(cleanRoot, filePath)
			if err != nil {
				return "", err
			}
			records = append(records, pkg.ImportPath+"\x00"+filepath.ToSlash(rel)+"\x00"+fmt.Sprintf("%x", sha256.Sum256(contents)))
		}
	}
	if len(records) == 0 {
		return "", errors.New("go list compile closure has no candidate files")
	}
	sort.Strings(records)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(records, "\n")))), nil
}

func remoteCandidatePathInside(root, candidate string) (bool, error) {
	clean, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel), nil
}
