package wails

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// projectStateReader mirrors contract.UIProjectStateFacade so local
// callers can be written against a locally-visible interface name. P22
// P4 S1b: the underlying type is the contract facade, not uistate's
// Service — ui/wails no longer imports internal/module/uistate.
type projectStateReader interface {
	GetProjects(ctx context.Context) (*contract.ProjectsSnapshot, error)
}

type scopeCatalog struct {
	defaultRoot string
	knownRoots  map[string]struct{}
}

func requestScopeRoots(
	ctx context.Context,
	cfg *config.Config,
	state projectStateReader,
	project string,
	projects []string,
) ([]string, error) {
	catalog, err := loadScopeCatalog(ctx, cfg, state)
	if err != nil {
		return nil, err
	}
	return resolveScopeRoots(project, projects, catalog)
}

func loadScopeCatalog(ctx context.Context, cfg *config.Config, state projectStateReader) (scopeCatalog, error) {
	catalog := scopeCatalog{knownRoots: map[string]struct{}{}}
	if root, ok := knownScopeRoot(configProjectRoot(cfg), ""); ok {
		catalog.defaultRoot = root
		catalog.knownRoots[root] = struct{}{}
	}
	if state == nil {
		return catalog, nil
	}
	projects, err := state.GetProjects(ctx)
	if err != nil {
		return scopeCatalog{}, err
	}
	addProjectsStateRoots(&catalog, projects)
	return catalog, nil
}

func addProjectsStateRoots(catalog *scopeCatalog, state *contract.ProjectsSnapshot) {
	if catalog == nil || state == nil {
		return
	}
	for _, raw := range append([]string{state.Active}, state.Projects...) {
		if root, ok := knownScopeRoot(raw, catalog.defaultRoot); ok {
			catalog.knownRoots[root] = struct{}{}
		}
	}
}

func configProjectRoot(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProjectRoot)
}

func knownScopeRoot(raw, defaultRoot string) (string, bool) {
	root, err := resolveScopePath(raw, defaultRoot)
	if err != nil {
		return "", false
	}
	root, err = realPathForCheck(root)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

// resolveScopePath 解析作用域路径。
func resolveScopePath(raw, defaultRoot string) (string, error) {
	value := strings.TrimSpace(raw)
	switch {
	case value == "":
		return "", errors.New("project root is required")
	case value == ".":
		if strings.TrimSpace(defaultRoot) == "" {
			return "", errors.New("default project root is not configured")
		}
		return filepath.Abs(defaultRoot)
	case filepath.IsAbs(value):
		return filepath.Abs(value)
	case strings.TrimSpace(defaultRoot) == "":
		return "", errors.New("default project root is not configured")
	default:
		return filepath.Abs(filepath.Join(defaultRoot, value))
	}
}

// resolve 解析桌面 UI 桥接。
func (c scopeCatalog) resolve(raw string) (string, error) {
	root, err := resolveScopePath(raw, c.defaultRoot)
	if err != nil {
		return "", err
	}
	root, err = realPathForCheck(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("project root is not a directory")
	}
	if _, ok := c.knownRoots[root]; !ok {
		return "", errors.New("project root is not registered")
	}
	return root, nil
}
