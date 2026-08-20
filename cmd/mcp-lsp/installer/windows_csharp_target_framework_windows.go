//go:build windows

package installer

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var windowsCSharpTargetFrameworkPattern = regexp.MustCompile(`^(?:net|netcoreapp)([0-9]+)\.([0-9]+)(?:-.+)?$`)

type windowsCSharpProjectPropertyGroup struct {
	TargetFramework  string `xml:"TargetFramework"`
	TargetFrameworks string `xml:"TargetFrameworks"`
}

type windowsCSharpProjectFile struct {
	PropertyGroups []windowsCSharpProjectPropertyGroup `xml:"PropertyGroup"`
}

// WindowsCSharpReferencePackError 表示项目 TargetFramework 没有对应的 .NET reference pack。
type WindowsCSharpReferencePackError struct {
	ProjectPath     string
	TargetFramework string
	ReferenceRoot   string
}

func (e *WindowsCSharpReferencePackError) Error() string {
	if e == nil {
		return "C# reference pack is unavailable"
	}
	return fmt.Sprintf("C# project %s targets %s but cohort has no matching Microsoft.NETCore.App.Ref under %s", securefs.RedactPath(e.ProjectPath), e.TargetFramework, securefs.RedactPath(e.ReferenceRoot))
}

// ValidateWindowsCSharpTargetFrameworkReferencePacks 读取 workspace 下唯一的项目根 csproj，
// 严格解析 TargetFramework/TargetFrameworks，并要求产品 .NET cohort 提供对应的 Core reference pack。
// 不读取 solution、不猜测项目，也不把不同 major/minor 的 pack 当作兼容版本。
func ValidateWindowsCSharpTargetFrameworkReferencePacks(cohortRoot, workspaceRoot string) error {
	rawCohortRoot := strings.TrimSpace(cohortRoot)
	if rawCohortRoot == "" {
		return fmt.Errorf("resolve .NET cohort root: value is empty")
	}
	cohortRoot, err := filepath.Abs(rawCohortRoot)
	if err != nil {
		return fmt.Errorf("resolve .NET cohort root: %w", err)
	}
	rawWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	if rawWorkspaceRoot == "" {
		return fmt.Errorf("resolve C# workspace root: value is empty")
	}
	workspaceRoot, err = filepath.Abs(rawWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve C# workspace root: %w", err)
	}
	projectPath, err := windowsCSharpProjectRoot(workspaceRoot)
	if err != nil {
		return err
	}
	frameworks, err := windowsCSharpProjectTargetFrameworks(projectPath)
	if err != nil {
		return err
	}
	for _, framework := range frameworks {
		baseFramework, major, minor, supported, err := windowsCSharpTargetFrameworkParts(framework)
		if err != nil {
			return fmt.Errorf("parse C# project %s TargetFramework %q: %w", securefs.RedactPath(projectPath), framework, err)
		}
		if !supported {
			continue
		}
		if _, err := windowsCSharpMatchingReferencePack(cohortRoot, projectPath, framework, baseFramework, major, minor); err != nil {
			return err
		}
	}
	return nil
}

func windowsCSharpProjectRoot(workspaceRoot string) (string, error) {
	info, err := os.Lstat(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("inspect C# workspace root %s: %w", securefs.RedactPath(workspaceRoot), err)
	}
	if isUnsafeAssetFile(info) {
		return "", fmt.Errorf("C# workspace root is a symlink or reparse point: %s", securefs.RedactPath(workspaceRoot))
	}
	if !info.IsDir() {
		if strings.EqualFold(filepath.Ext(workspaceRoot), ".csproj") && info.Mode().IsRegular() {
			return workspaceRoot, nil
		}
		return "", fmt.Errorf("C# workspace root is not a directory or csproj: %s", securefs.RedactPath(workspaceRoot))
	}
	var projects []string
	err = filepath.WalkDir(workspaceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(entryInfo) {
			return fmt.Errorf("C# workspace contains a symlink or reparse point: %s", securefs.RedactPath(path))
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if path != workspaceRoot && windowsCSharpGeneratedDirectory(workspaceRoot, path, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".csproj") {
			if !entryInfo.Mode().IsRegular() {
				return fmt.Errorf("C# project root is not a regular file: %s", securefs.RedactPath(path))
			}
			projects = append(projects, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("discover C# project root under %s: %w", securefs.RedactPath(workspaceRoot), err)
	}
	sort.Strings(projects)
	switch len(projects) {
	case 0:
		return "", fmt.Errorf("C# workspace %s has no project-root csproj", securefs.RedactPath(workspaceRoot))
	case 1:
		return projects[0], nil
	default:
		return "", fmt.Errorf("C# workspace %s has %d project-root csproj files; refusing to guess", securefs.RedactPath(workspaceRoot), len(projects))
	}
}

func windowsCSharpGeneratedDirectory(workspaceRoot, path, name string) bool {
	if name == "bin" && filepath.Clean(path) == filepath.Join(filepath.Clean(workspaceRoot), "bin") {
		// A workspace-level source container may itself be named bin; only nested
		// project output directories are generated trees.
		return false
	}
	switch name {
	case "bin", "obj", ".git", ".build-cache", ".super-dolphin", ".worktrees", ".workspace", ".claude", ".agent", ".agents", "node_modules", "dist", "target":
		return true
	default:
		return false
	}
}

func windowsCSharpProjectTargetFrameworks(projectPath string) ([]string, error) {
	input, err := os.Open(projectPath)
	if err != nil {
		return nil, fmt.Errorf("open C# project %s: %w", securefs.RedactPath(projectPath), err)
	}
	var project windowsCSharpProjectFile
	decodeErr := xml.NewDecoder(input).Decode(&project)
	closeErr := input.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("parse C# project %s: %w", securefs.RedactPath(projectPath), decodeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close C# project %s: %w", securefs.RedactPath(projectPath), closeErr)
	}
	seen := make(map[string]struct{})
	var frameworks []string
	for _, group := range project.PropertyGroups {
		for _, raw := range []string{group.TargetFramework, group.TargetFrameworks} {
			for _, value := range strings.Split(raw, ";") {
				value = strings.TrimSpace(strings.ToLower(value))
				if value == "" {
					continue
				}
				if _, exists := seen[value]; exists {
					continue
				}
				seen[value] = struct{}{}
				frameworks = append(frameworks, value)
			}
		}
	}
	if len(frameworks) == 0 {
		return nil, fmt.Errorf("C# project %s has no TargetFramework or TargetFrameworks", securefs.RedactPath(projectPath))
	}
	sort.Strings(frameworks)
	return frameworks, nil
}

func windowsCSharpTargetFrameworkParts(framework string) (string, int, int, bool, error) {
	if strings.Contains(framework, "$ (") || strings.Contains(framework, "$(") {
		return "", 0, 0, false, fmt.Errorf("TargetFramework is unresolved")
	}
	if strings.HasPrefix(framework, "netstandard") {
		return "", 0, 0, false, nil
	}
	matches := windowsCSharpTargetFrameworkPattern.FindStringSubmatch(framework)
	if len(matches) != 3 {
		return "", 0, 0, false, fmt.Errorf("unsupported TargetFramework syntax")
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("invalid major version: %w", err)
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("invalid minor version: %w", err)
	}
	return fmt.Sprintf("net%d.%d", major, minor), major, minor, true, nil
}

func windowsCSharpMatchingReferencePack(cohortRoot, projectPath, framework, baseFramework string, major, minor int) (string, error) {
	referenceRoot := filepath.Join(cohortRoot, "packs", "Microsoft.NETCore.App.Ref")
	entries, err := os.ReadDir(referenceRoot)
	if err != nil {
		return "", &WindowsCSharpReferencePackError{ProjectPath: projectPath, TargetFramework: framework, ReferenceRoot: referenceRoot}
	}
	prefix := fmt.Sprintf("%d.%d.", major, minor)
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != fmt.Sprintf("%d.%d", major, minor) && !strings.HasPrefix(entry.Name(), prefix)) {
			continue
		}
		candidate := filepath.Join(referenceRoot, entry.Name(), "ref", baseFramework)
		info, statErr := os.Lstat(candidate)
		if statErr != nil || isUnsafeAssetFile(info) || !info.IsDir() {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) == 0 {
		return "", &WindowsCSharpReferencePackError{ProjectPath: projectPath, TargetFramework: framework, ReferenceRoot: referenceRoot}
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}
