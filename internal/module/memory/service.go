package memory

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"golang.org/x/text/unicode/norm"
)

type Service interface {
	Config() Config
	RootDir() string
	EnsureRoot(ctx context.Context) error
}

type service struct {
	cfg    *Config
	logger *slog.Logger
}

type MemoryLifecycleHooks struct {
	enabled             bool
	extractOnStop       bool
	rootDir             string
	projectRoot         string
	autoMemPathOverride string
	consolidator        *AutoDreamConsolidator
	extractFn           ExtractFunc
	logger              *slog.Logger
}

var saveIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*(?:i(?:'|’)ll remember|i will remember|i(?:'|’)ve noted|noted|saved to memory|saved in memory|i(?:'|’)ll save this to memory)\s*(?:that\s+)?(?:[:：\-—,，]\s*|\s+)?(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:记住了|已记住|我会记住|已经记住|已保存到记忆|保存到记忆了|帮你记住了)\s*(?:这个|这点|了)?\s*(?:[:：\-—,，]\s*|\s+)?(.+?)\s*$`),
}

func NewService(cfg *Config, logger *slog.Logger) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	return &service{cfg: cfg, logger: logger}
}

func NewMemoryLifecycleHooks(cfg *Config, consolidator *AutoDreamConsolidator, logger *slog.Logger) *MemoryLifecycleHooks {
	if cfg == nil {
		cfg = &Config{}
	}
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	return &MemoryLifecycleHooks{
		enabled:             ResolveMemoryGate(contract.BuildCtx{}, cfg).AutoEnabled,
		extractOnStop:       cfg.ExtractOnStop,
		rootDir:             strings.TrimSpace(cfg.RootDir),
		projectRoot:         strings.TrimSpace(cfg.ProjectRoot),
		autoMemPathOverride: strings.TrimSpace(cfg.AutoMemPathOverride),
		consolidator:        consolidator,
		extractFn:           mockAutoDreamExtractFunc,
		logger:              logger,
	}
}

func (s *service) Config() Config {
	if s == nil || s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

func (s *service) RootDir() string {
	return strings.TrimSpace(s.Config().RootDir)
}

func (s *service) EnsureRoot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cfg := s.Config()
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return err
	}
	if root == "" {
		return errors.New("memory root dir is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Debug("memory root ready", "root_dir", filepath.Clean(root))
	}
	return nil
}

func (h *MemoryLifecycleHooks) onThreadStart(_ context.Context, evt threaddto.Started) {
	if h == nil || !h.enabled || h.logger == nil {
		return
	}
	h.logger.Debug("memory thread hook ready", "thread_id", strings.TrimSpace(evt.ThreadID))
}

func (h *MemoryLifecycleHooks) onTurnEnd(ctx context.Context, evt turndto.TurnCompleted) {
	if h == nil || !h.enabled || !evt.Success {
		return
	}
	if err := contextErr(ctx); err != nil {
		return
	}
	intent := DetectSaveIntent(platformshared.FirstTrimmed(evt.Message, evt.Result, evt.Summary))
	if !intent.Detected || strings.TrimSpace(intent.Content) == "" {
		return
	}
	if err := h.writeIntent(intent); err != nil && h.logger != nil {
		h.logger.Warn("memory explicit save failed", "thread_id", strings.TrimSpace(evt.ThreadID), "error", err)
	}
}

func (h *MemoryLifecycleHooks) onThreadStopped(ctx context.Context, evt threaddto.Stopped) {
	if h == nil || !h.extractOnStop {
		return
	}
	if err := h.ExtractAndSave(ctx); err != nil && h.logger != nil {
		h.logger.Warn("memory stop extraction failed", "thread_id", strings.TrimSpace(evt.ThreadID), "error", err)
	}
}

func (h *MemoryLifecycleHooks) ExtractAndSave(ctx context.Context) error {
	if h == nil || !h.extractOnStop {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	store, err := h.diskStore()
	if err != nil {
		return err
	}
	consolidator := h.consolidator
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	return consolidator.Consolidate(ctx, store.Root(), h.extractFn)
}

func (h *MemoryLifecycleHooks) writeIntent(intent SaveIntent) error {
	store, err := h.diskStore()
	if err != nil {
		return err
	}
	entry := buildExplicitMemoryEntry(intent)
	if _, err := store.Create(entry); err != nil {
		if !errors.Is(err, ErrMemoryAlreadyExists) {
			return err
		}
		_, err = store.Update(entry)
		return err
	}
	return nil
}

func (h *MemoryLifecycleHooks) diskStore() (*DiskStore, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return nil, err
	}
	return NewDiskStore(root)
}

func DetectSaveIntent(assistantResponse string) SaveIntent {
	response := strings.TrimSpace(strings.ReplaceAll(assistantResponse, "\r\n", "\n"))
	if response == "" {
		return SaveIntent{}
	}
	for _, pattern := range saveIntentPatterns {
		matches := pattern.FindStringSubmatch(response)
		if len(matches) == 0 {
			continue
		}
		content := normalizeHookContent(matches[len(matches)-1])
		if !hasMeaningfulMemoryContent(content) {
			continue
		}
		return SaveIntent{Detected: true, Content: content, Type: inferMemoryType(content)}
	}
	return SaveIntent{}
}

func resolvedStoreRoot(baseRoot, projectRoot, autoMemPathOverride string) (string, error) {
	if override := strings.TrimSpace(autoMemPathOverride); override != "" {
		validatedOverride, err := ValidateMemoryRoot(override)
		if err != nil {
			return "", err
		}
		if validatedOverride == "" {
			return "", errors.New("memory path override is empty")
		}
		return strings.TrimSuffix(validatedOverride, string(os.PathSeparator)), nil
	}
	baseRoot = strings.TrimSpace(baseRoot)
	if baseRoot == "" {
		return "", errors.New("memory root dir is empty")
	}
	if projectRoot = strings.TrimSpace(projectRoot); projectRoot != "" {
		if root, err := GetAutoMemPath(baseRoot, projectRoot); err == nil && strings.TrimSpace(root) != "" {
			return root, nil
		}
	}
	return baseRoot, nil
}

func buildExplicitMemoryEntry(intent SaveIntent) MemoryEntry {
	content := normalizeHookContent(intent.Content)
	memoryType := intent.Type
	if !memoryType.IsKnown() {
		memoryType = inferMemoryType(content)
	}
	description := truncateRunes(firstNonEmptyLine(content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(content, memoryHookMaxRunes)
	}
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        buildExplicitMemoryName(memoryType, description),
			Description: description,
			Type:        cloneMemoryType(memoryType),
		},
		Content: content,
	}
}

func buildExplicitMemoryName(memoryType MemoryType, description string) string {
	prefix := "Saved memory"
	switch memoryType {
	case MemoryTypeUser:
		prefix = "User note"
	case MemoryTypeFeedback:
		prefix = "Feedback rule"
	case MemoryTypeProject:
		prefix = "Project note"
	case MemoryTypeReference:
		prefix = "Reference note"
	}
	if description == "" {
		return prefix
	}
	return truncateRunes(prefix+": "+description, 96)
}

func normalizeHookContent(text string) string {
	lines := make([]string, 0, 4)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		line = strings.TrimSpace(strings.TrimLeft(line, ":：-—,，。.!！?？;；"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "that "))
		line = strings.TrimSpace(strings.TrimPrefix(line, "关于 "))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func hasMeaningfulMemoryContent(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) >= 0
}

func inferMemoryType(text string) MemoryType {
	normalized := CanonicalName(strings.ToLower(strings.ReplaceAll(text, "\n", " ")))
	if normalized == "" {
		return MemoryTypeUser
	}
	if strings.Contains(normalized, "http://") || strings.Contains(normalized, "https://") {
		return MemoryTypeReference
	}
	typeScores := map[MemoryType]int{
		MemoryTypeUser:      keywordScore(normalized, "prefer", "preference", "style", "tone", "habit", "偏好", "风格", "习惯", "喜欢", "回答尽量", "回复尽量"),
		MemoryTypeFeedback:  keywordScore(normalized, "rule", "workflow", "always", "never", "avoid", "规范", "规则", "流程", "约定", "务必", "不要", "how to apply"),
		MemoryTypeProject:   keywordScore(normalized, "project", "phase", "milestone", "deadline", "owner", "incident", "项目", "阶段", "里程碑", "截止", "负责人", "事故", "发布日期"),
		MemoryTypeReference: keywordScore(normalized, "docs", "doc", "wiki", "runbook", "notion", "slack", "grafana", "dashboard", "文档", "链接", "地址", "手册"),
	}
	bestType := MemoryTypeUser
	bestScore := -1
	for _, candidate := range []MemoryType{MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference} {
		if score := typeScores[candidate]; score > bestScore {
			bestType = candidate
			bestScore = score
		}
	}
	return bestType
}

func keywordScore(text string, keywords ...string) int {
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(text, CanonicalName(keyword)) {
			score++
		}
	}
	return score
}

func cleanAbsolutePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	cleaned := filepath.Clean(norm.NFC.String(strings.TrimSpace(raw)))
	if !filepath.IsAbs(cleaned) {
		absolute, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = filepath.Clean(absolute)
	}
	return cleaned, nil
}

func realPathDeepestExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(cleaned); err == nil {
		return filepath.EvalSymlinks(cleaned)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current := cleaned
	var suffix []string
	for {
		next := filepath.Dir(current)
		if next == current {
			return "", os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(current))
		current = next
		if _, err := os.Stat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				real = filepath.Join(real, suffix[index])
			}
			return real, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

func ensureResolvablePath(path string) error {
	for probe := filepath.Clean(path); ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				if _, err := filepath.EvalSymlinks(probe); err != nil {
					return err
				}
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
	}
}
