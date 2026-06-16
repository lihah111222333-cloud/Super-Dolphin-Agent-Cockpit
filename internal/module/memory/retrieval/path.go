package retrieval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"golang.org/x/text/unicode/norm"
)

const memoryIndexFileName = "MEMORY.md"

var (
	errInvalidMemoryReadPath = errors.New("invalid memory read path")
)

func normalizeStoreRoot(root string) (string, error) {
	return shared.ValidateMemoryRoot(root)
}

// validateMemoryReadPath 校验记忆read路径。
func validateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveExistingMemoryPath(rootDir)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	candidateReal, err := resolveExistingMemoryPath(candidate)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	if !kernel.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryReadPath("path escapes root")
	}
	if info, err := os.Stat(candidateReal); err != nil {
		return "", invalidMemoryReadPath(err.Error())
	} else if info.IsDir() {
		return "", invalidMemoryReadPath("path is a directory")
	}
	return candidateReal, nil
}

// prepareMemoryPath 准备记忆路径。
func prepareMemoryPath(validatedRoot, file string) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", invalidMemoryReadPath("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", invalidMemoryReadPath("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := shared.EnsureResolvablePath(rootDir); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	return rootDir, candidate, nil
}

func resolveExistingMemoryPath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

func invalidMemoryReadPath(reason string) error {
	return fmt.Errorf("%w: %s", errInvalidMemoryReadPath, reason)
}
