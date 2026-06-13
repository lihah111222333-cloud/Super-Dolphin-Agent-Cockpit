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

// GetProjects 读取projects。
func (s *service) GetProjects(ctx context.Context) (*ProjectsState, error) {
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	return buildProjectsState(prefs), nil
}

// SetActiveProject 设置active项目。
func (s *service) SetActiveProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.setActiveProjectLocked(ctx, path)
}

// setActiveProjectLocked 设置active项目locked。
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

// AddProject 添加项目。
func (s *service) AddProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.addProjectLocked(ctx, path)
}

// addProjectLocked 添加项目locked。
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

// RemoveProject 移除项目。
func (s *service) RemoveProject(ctx context.Context, path string) (*ProjectsState, error) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	return s.removeProjectLocked(ctx, path)
}

// removeProjectLocked 移除项目locked。
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

// projectPathsFromValue 从值处理项目路径。
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
