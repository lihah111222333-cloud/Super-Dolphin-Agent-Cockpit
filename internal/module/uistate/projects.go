package uistate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const preferenceProjectsState = "projects.state"

type ProjectsState struct {
	Projects []string `json:"projects"`
	Active   string   `json:"active"`
}

// GetProjects 从 UI 偏好中读取项目列表和当前 active 项目。
func (s *service) GetProjects(ctx context.Context) (*ProjectsState, error) {
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	return buildProjectsState(prefs), nil
}

// SetActiveProject 切换当前 active 项目，并用 projectsMu 串行化偏好写入。
func (s *service) SetActiveProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.setActiveProjectLocked(ctx, path)
}

// setActiveProjectLocked 在持锁状态下更新 active 项目。
// 未注册或空路径会回到 "."，避免前端持有不可达项目引用。
func (s *service) setActiveProjectLocked(ctx context.Context, path string) (*ProjectsState, error) {
	state, err := s.GetProjects(ctx)
	if err != nil {
		return nil, err
	}
	next := normalizeProjectPath(path)
	if next == "" {
		next = "."
	}
	if next != "." && !containsProjectPath(state.Projects, next) {
		next = "."
	}
	state.Active = next
	if err := s.storeProjectsState(ctx, state); err != nil {
		return nil, err
	}
	return cloneProjectsState(*state), nil
}

// AddProject 解析并加入项目目录，重复或当前目录不会产生新偏好写入。
func (s *service) AddProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.addProjectLocked(ctx, path)
}

// addProjectLocked 在持锁状态下追加项目目录，并在路径非法时返回错误。
func (s *service) addProjectLocked(ctx context.Context, path string) (*ProjectsState, error) {
	state, err := s.GetProjects(ctx)
	if err != nil {
		return nil, err
	}
	next, err := resolveProjectDirectory(path)
	if err != nil {
		return nil, err
	}
	if next == "" || next == "." || containsProjectPath(state.Projects, next) {
		return cloneProjectsState(*state), nil
	}
	state.Projects = append(state.Projects, next)
	if err := s.storeProjectsState(ctx, state); err != nil {
		return nil, err
	}
	return cloneProjectsState(*state), nil
}

// RemoveProject 从偏好中移除项目，并在必要时重置 active 项目。
func (s *service) RemoveProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.removeProjectLocked(ctx, path)
}

// removeProjectLocked 在持锁状态下删除项目目录。
// 删除当前 active 项目时会回到 "."，保持前端 active 字段始终可消费。
func (s *service) removeProjectLocked(ctx context.Context, path string) (*ProjectsState, error) {
	state, err := s.GetProjects(ctx)
	if err != nil {
		return nil, err
	}
	target := normalizeProjectPath(path)
	if target == "" || target == "." {
		return cloneProjectsState(*state), nil
	}
	state.Projects = removeProjectPath(state.Projects, target)
	if state.Active == target {
		state.Active = "."
	}
	if err := s.storeProjectsState(ctx, state); err != nil {
		return nil, err
	}
	return cloneProjectsState(*state), nil
}

func (s *service) storeProjectsState(ctx context.Context, state *ProjectsState) error {
	if state == nil {
		state = &ProjectsState{}
	}
	normalized := normalizeProjectsState(*state)
	return s.SetPreference(ctx, preferenceProjectsState, normalized)
}

func buildProjectsState(prefs *Preferences) *ProjectsState {
	if prefs == nil {
		return cloneProjectsState(ProjectsState{Projects: []string{}, Active: "."})
	}
	return buildProjectsStateValue(preferenceRawValue(prefs.Values, preferenceProjectsState))
}

func buildProjectsStateValue(value any) *ProjectsState {
	raw, ok := value.(map[string]any)
	if !ok {
		return cloneProjectsState(ProjectsState{Projects: []string{}, Active: "."})
	}
	state := ProjectsState{
		Projects: projectPathsFromValue(raw["projects"]),
		Active:   normalizeProjectPath(stringValue(raw["active"])),
	}
	return cloneProjectsState(normalizeProjectsState(state))
}

func normalizeProjectsState(state ProjectsState) ProjectsState {
	state.Projects = projectPathsFromValue(state.Projects)
	if state.Active == "" {
		state.Active = "."
	}
	if state.Active != "." && !containsProjectPath(state.Projects, state.Active) {
		state.Active = "."
	}
	return state
}

// projectPathsFromValue 将偏好里的 string slice 或 JSON 数组归一化成项目路径列表。
// 空值、当前目录和重复项会被丢弃，保证写回前端的 projects 字段稳定可消费。
func projectPathsFromValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return normalizeProjectPaths(typed)
	case []any:
		paths := make([]string, 0, len(typed))
		for _, item := range typed {
			path := normalizeProjectPath(stringValue(item))
			if path == "" || path == "." || containsProjectPath(paths, path) {
				continue
			}
			paths = append(paths, path)
		}
		return paths
	default:
		return []string{}
	}
}

func normalizeProjectPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, item := range paths {
		path := normalizeProjectPath(item)
		if path == "" || path == "." || containsProjectPath(result, path) {
			continue
		}
		result = append(result, path)
	}
	return result
}

func normalizeProjectPath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if value == "." {
		return "."
	}
	cleaned := filepath.Clean(value)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = real
	}
	return filepath.ToSlash(cleaned)
}

func resolveProjectDirectory(raw string) (string, error) {
	path := normalizeProjectPath(raw)
	if path == "" || path == "." {
		return path, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("project path must be a directory")
	}
	return path, nil
}

func removeProjectPath(paths []string, target string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != target {
			result = append(result, path)
		}
	}
	return result
}

func containsProjectPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func cloneProjectsState(value ProjectsState) *ProjectsState {
	projects := make([]string, len(value.Projects))
	copy(projects, value.Projects)
	return &ProjectsState{
		Projects: projects,
		Active:   value.Active,
	}
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}
