package wails

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// projectStateReader 是 UI 项目状态读取窄接口，避免 ui/wails 依赖 uistate 具体服务。
type projectStateReader interface {
	GetProjects(ctx context.Context) (*contract.ProjectsSnapshot, error)
}

// scopeCatalog 记录当前允许前端访问的项目根集合。
// 所有前端路径请求都必须落在这张目录表内，避免 UI helper 打开任意本地路径。
type scopeCatalog struct {
	defaultRoot string
	knownRoots  map[string]struct{}
}

// requestScopeRoots 加载项目根目录目录表，并解析前端请求的访问范围。
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

// loadScopeCatalog 从配置和 UI 项目状态构建允许访问的根目录表。
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

// addProjectsStateRoots 把 UI 项目状态中的 active 和已知项目加入目录表。
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

// configProjectRoot 读取配置中的项目根目录。
func configProjectRoot(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProjectRoot)
}

// knownScopeRoot 解析并校验一个可登记的项目根目录。
// 无效、不可访问或非目录路径不会进入目录表，后续请求也就无法命中。
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

// resolveScopePath 解析项目范围路径，支持 "."、绝对路径和相对默认根的路径。
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

// resolve 在已登记目录表中解析前端传入的项目范围。
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
