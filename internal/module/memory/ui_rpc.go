package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

const uiMemoryPreviewLimit = 320

type uiMemoryGetParams struct {
	CWD string `json:"cwd,omitempty"`
}

type UIMemorySnapshot struct {
	Overview    UIMemoryOverview     `json:"overview"`
	Private     UIMemoryScopeSection `json:"private"`
	Team        UIMemoryScopeSection `json:"team"`
	AgentScopes []UIAgentMemoryScope `json:"agentScopes"`
}

type UIMemoryOverview struct {
	Enabled             bool   `json:"enabled"`
	ToolsEnabled        bool   `json:"toolsEnabled"`
	RootDir             string `json:"rootDir,omitempty"`
	ProjectRoot         string `json:"projectRoot,omitempty"`
	PrivateRoot         string `json:"privateRoot,omitempty"`
	AutoMemPathOverride string `json:"autoMemPathOverride,omitempty"`
	TeamFeatureEnabled  bool   `json:"teamFeatureEnabled"`
}

type UIMemoryScopeSection struct {
	Label     string          `json:"label"`
	RootPath  string          `json:"rootPath,omitempty"`
	IndexPath string          `json:"indexPath,omitempty"`
	Notice    string          `json:"notice,omitempty"`
	Entries   []UIMemoryEntry `json:"entries"`
}

type UIMemoryEntry struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type,omitempty"`
	Path        string    `json:"path,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	Preview     string    `json:"preview,omitempty"`
}

type UIAgentMemoryScope struct {
	Scope    string               `json:"scope"`
	RootPath string               `json:"rootPath,omitempty"`
	Notice   string               `json:"notice,omitempty"`
	Entries  []UIAgentMemoryEntry `json:"entries"`
}

type UIAgentMemoryEntry struct {
	AgentType string    `json:"agentType"`
	Path      string    `json:"path,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	Empty     bool      `json:"empty,omitempty"`
}

func buildUIMemorySnapshot(ctx context.Context, svc Service, cwd string) (UIMemorySnapshot, error) {
	if svc == nil {
		return UIMemorySnapshot{}, errors.New("memory service is not configured")
	}
	cfg := svc.Config()
	projectRoot := strings.TrimSpace(cwd)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	buildCtx := contract.BuildCtx{CWD: projectRoot}
	if gitRoot, err := FindCanonicalGitRoot(ctx, projectRoot); err == nil && strings.TrimSpace(gitRoot) != "" {
		buildCtx.GitRoot = strings.TrimSpace(gitRoot)
	}

	privateRoot, privateErr := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	privateSection := loadUIMemoryScope("Private durable memory", privateRoot, privateErr, true)

	teamSection := UIMemoryScopeSection{
		Label:   "Team durable memory",
		Notice:  "当前未启用 Team memory。",
		Entries: []UIMemoryEntry{},
	}
	if teamMemoryConfigured(cfg) {
		teamRoot, err := configuredTeamMemRoot(&cfg, buildCtx)
		teamSection = loadUIMemoryScope("Team durable memory", teamRoot, err, false)
	}

	agentScopes := loadUIAgentMemoryScopes(cfg, projectRoot)
	return UIMemorySnapshot{
		Overview: UIMemoryOverview{
			Enabled:             cfg.Enabled,
			ToolsEnabled:        cfg.EnableTools,
			RootDir:             strings.TrimSpace(cfg.RootDir),
			ProjectRoot:         projectRoot,
			PrivateRoot:         strings.TrimSpace(privateRoot),
			AutoMemPathOverride: strings.TrimSpace(cfg.AutoMemPathOverride),
			TeamFeatureEnabled:  cfg.Features.TeamMemory,
		},
		Private:     privateSection,
		Team:        teamSection,
		AgentScopes: agentScopes,
	}, nil
}

func loadUIMemoryScope(label, root string, rootErr error, filterPrivateTeam bool) UIMemoryScopeSection {
	section := UIMemoryScopeSection{
		Label:   label,
		Entries: []UIMemoryEntry{},
	}
	if rootErr != nil {
		section.Notice = rootErr.Error()
		return section
	}
	root = strings.TrimSpace(root)
	if root == "" {
		section.Notice = "未解析到目录。"
		return section
	}
	section.RootPath = root
	section.IndexPath = memoryIndexPath(root)
	entries, err := scanMemoryEntries(root)
	if err != nil {
		section.Notice = err.Error()
		return section
	}
	for _, entry := range entries {
		rel := memoryEntryDisplayPath(root, entry.FilePath)
		if filterPrivateTeam && strings.HasPrefix(rel, "team/") {
			continue
		}
		section.Entries = append(section.Entries, UIMemoryEntry{
			Name:        strings.TrimSpace(entry.Frontmatter.Name),
			Description: strings.TrimSpace(entry.Frontmatter.Description),
			Type:        strings.TrimSpace(string(entry.Type())),
			Path:        rel,
			UpdatedAt:   entry.UpdatedAt,
			Preview:     uiPreviewText(entry.Content),
		})
	}
	if len(section.Entries) == 0 {
		section.Notice = firstNonEmptyUI(section.Notice, "当前目录下还没有可读的记忆条目。")
	}
	return section
}

func loadUIAgentMemoryScopes(cfg Config, projectRoot string) []UIAgentMemoryScope {
	effective := cfg
	if strings.TrimSpace(projectRoot) != "" {
		effective.ProjectRoot = strings.TrimSpace(projectRoot)
	}
	manager := NewAgentMemoryManager(&effective)
	scopes := []MemoryScope{MemoryScopeProject, MemoryScopeUser, MemoryScopeLocal}
	items := make([]UIAgentMemoryScope, 0, len(scopes))
	for _, scope := range scopes {
		root, err := manager.GetAgentMemoryScopeRoot(scope)
		item := UIAgentMemoryScope{
			Scope:   string(scope),
			Entries: []UIAgentMemoryEntry{},
		}
		if err != nil {
			item.Notice = err.Error()
			items = append(items, item)
			continue
		}
		item.RootPath = root
		entries, readErr := scanUIAgentScope(root)
		if readErr != nil {
			item.Notice = readErr.Error()
		} else {
			item.Entries = entries
			if len(entries) == 0 {
				item.Notice = "当前 scope 下还没有可读的 MEMORY.md。"
			}
		}
		items = append(items, item)
	}
	return items
}

func scanUIAgentScope(root string) ([]UIAgentMemoryEntry, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]UIAgentMemoryEntry, 0, len(dirs))
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		entryPath := filepath.Join(root, dir.Name(), memoryIndexFileName)
		info, err := os.Stat(entryPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(entryPath)
		if err != nil {
			return nil, err
		}
		content := strings.TrimSpace(stripUTF8BOM(string(raw)))
		items = append(items, UIAgentMemoryEntry{
			AgentType: dir.Name(),
			Path:      filepath.ToSlash(filepath.Join(dir.Name(), memoryIndexFileName)),
			UpdatedAt: info.ModTime(),
			Preview:   uiPreviewText(content),
			Empty:     strings.TrimSpace(content) == "",
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].AgentType) < strings.ToLower(items[j].AgentType)
	})
	return items, nil
}

func memoryEntryDisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func uiPreviewText(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	runes := []rune(text)
	if len(runes) > uiMemoryPreviewLimit {
		return strings.TrimSpace(string(runes[:uiMemoryPreviewLimit])) + "…"
	}
	return text
}

func firstNonEmptyUI(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func registerUIMemoryHandlers(p memoryHandlerDeps) handler.Map {
	return handler.Map{
		"ui/memory/get": rpc.StrictHandler(func(ctx context.Context, req uiMemoryGetParams) (UIMemorySnapshot, error) {
			return buildUIMemorySnapshot(ctx, p.Service, req.CWD)
		}),
	}
}
