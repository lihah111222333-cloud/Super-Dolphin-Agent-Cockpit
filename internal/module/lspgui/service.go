package lspgui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var errProjectRootRequired = errors.New("project root is required")

type service struct {
	root string
}

var _ Service = (*service)(nil)

func NewService(cfg *config.Config) Service {
	root := ""
	if cfg != nil {
		root = strings.TrimSpace(cfg.ProjectRoot)
	}
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	return &service{root: normalizeProjectRoot(root)}
}

func normalizeProjectRoot(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return real
	}
	return abs
}

func (s *service) resolvePath(raw string) (string, error) {
	if strings.TrimSpace(s.root) == "" {
		return "", errProjectRootRequired
	}
	candidate := s.root
	if value := strings.TrimSpace(raw); value != "" {
		if filepath.IsAbs(value) {
			candidate = value
		} else {
			candidate = filepath.Join(s.root, value)
		}
	}
	resolved, err := canonicalPath(candidate)
	if err != nil {
		return "", err
	}
	return resolved, ensureWithinRoot(s.root, resolved)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parentReal, filepath.Base(abs)), nil
}

func ensureWithinRoot(root, candidate string) error {
	if !platformshared.ContainsPath(root, candidate) {
		return fmt.Errorf("path %q is outside project root %q", candidate, root)
	}
	return nil
}

func requireExistingFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("file_path must reference a file")
	}
	return info, nil
}

func defaultLimit(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}
