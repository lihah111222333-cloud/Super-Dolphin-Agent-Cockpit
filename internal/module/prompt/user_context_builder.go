package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const runtimeExtrasRelevanceDisclaimer = "Only use the following runtime extras when they are directly relevant to the user's current request."

type userContextPayload map[string]string

type userContextCache struct {
	mu         sync.RWMutex
	generation uint64
	values     map[string]userContextPayload
}

func newUserContextCache() *userContextCache {
	return &userContextCache{values: map[string]userContextPayload{}}
}

func (c *userContextCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

func (c *userContextCache) Lookup(key string, generation uint64) (userContextPayload, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if generation != c.generation {
		return nil, false
	}
	payload, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return cloneUserContextPayload(payload), true
}

func (c *userContextCache) Store(key string, generation uint64, payload userContextPayload) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return false
	}
	c.values[key] = cloneUserContextPayload(payload)
	return true
}

func (c *userContextCache) InvalidateAll() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.values = map[string]userContextPayload{}
	return c.generation
}

func BuildBaseUserContext(sources []contract.ClaudeMdSource) map[string]string {
	block := renderClaudeMdSources(sources)
	if strings.TrimSpace(block) == "" {
		return nil
	}
	return userContextPayload{"claudeMd": block}
}

func CollectRuntimeUserContext(input TurnInput, resolved []ResolvedPromptSection) map[string]string {
	currentDateValue := strings.TrimSpace(input.CurrentDate)
	if currentDateValue == "" {
		currentDateValue = time.Now().Format("2006-01-02")
	}
	extras := userContextPayload{
		"currentDate": fmt.Sprintf("Today's date is %s.", currentDateValue),
		"runtimeExtras": strings.TrimSpace(joinBlocks(
			runtimeExtrasRelevanceDisclaimer,
			joinBlocks(runtimeExtraContents(resolved)...),
		)),
	}
	return MergeRuntimeUserContext(extras, input.RuntimeUserContext)
}

func MergeRuntimeUserContext(base, extras map[string]string) map[string]string {
	merged := cloneUserContextPayload(base)
	if merged == nil {
		merged = userContextPayload{}
	}
	for key, value := range extras {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			merged[strings.TrimSpace(key)] = trimmed
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func FormatUserContextMessage(payload map[string]string) string {
	return contract.RenderUserContextMessage(dto.TurnAssembly{
		UserContext: map[string]string(cloneUserContextPayload(payload)),
	})
}

func FormatUserContextText(payload map[string]string) string {
	return contract.FormatUserContextText(payload)
}

func includeRuntimeExtraSection(section ResolvedPromptSection) bool {
	if strings.TrimSpace(section.Content) == "" {
		return false
	}
	switch strings.TrimSpace(section.Name) {
	case DynamicSectionSessionGuidance,
		DynamicSectionEnvInfoSimple,
		DynamicSectionLanguage:
		return false
	default:
		return true
	}
}

func runtimeExtraContents(resolved []ResolvedPromptSection) []string {
	runtimeBlocks := make([]string, 0, len(resolved))
	for _, section := range resolved {
		if includeRuntimeExtraSection(section) {
			runtimeBlocks = append(runtimeBlocks, strings.TrimSpace(section.Content))
		}
	}
	return runtimeBlocks
}

func (s *service) buildBaseUserContext(_ context.Context, sources []contract.ClaudeMdSource) userContextPayload {
	cacheKey := baseUserContextCacheKey(sources)
	generation := s.userContextCache.Generation()
	if cached, ok := s.userContextCache.Lookup(cacheKey, generation); ok {
		return cached
	}
	base := BuildBaseUserContext(sources)
	s.userContextCache.Store(cacheKey, generation, base)
	return userContextPayload(base)
}

func (s *service) resolveClaudeMdSources(ctx context.Context, buildCtx BuildCtx) []contract.ClaudeMdSource {
	if s == nil || s.claudeMdProvider == nil {
		return nil
	}
	return cloneClaudeMdSources(s.claudeMdProvider.ResolveClaudeMdSources(ctx, buildCtx))
}

func baseUserContextCacheKey(sources []contract.ClaudeMdSource) string {
	visible := visibleClaudeMdSources(sources)
	if len(visible) == 0 {
		return "base-user-context:empty"
	}
	hasher := sha256.New()
	for _, source := range visible {
		hasher.Write([]byte(strings.TrimSpace(source.Path)))
		hasher.Write([]byte("\n" + strings.TrimSpace(source.Type)))
		hasher.Write([]byte("\n" + strings.TrimSpace(source.Origin)))
		hasher.Write([]byte("\n" + sourceDigest(source) + "\n"))
	}
	return "base-user-context:" + hex.EncodeToString(hasher.Sum(nil))
}

func visibleClaudeMdSources(sources []contract.ClaudeMdSource) []contract.ClaudeMdSource {
	visible := make([]contract.ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		if source.Conditional || strings.TrimSpace(source.Content) == "" {
			continue
		}
		visible = append(visible, source)
	}
	return visible
}

func renderClaudeMdSources(sources []contract.ClaudeMdSource) string {
	blocks := make([]string, 0, len(sources))
	for _, source := range visibleClaudeMdSources(sources) {
		blocks = append(blocks, renderClaudeMdSource(source))
	}
	return strings.TrimSpace(joinBlocks(blocks...))
}

func renderClaudeMdSource(source contract.ClaudeMdSource) string {
	header := "Contents of " + strings.TrimSpace(source.Path)
	if description := strings.TrimSpace(source.Description); description != "" {
		header += " (" + description + ")"
	}
	content := strings.TrimSpace(source.Content)
	if strings.TrimSpace(source.Type) == "teammem" {
		content = strings.Join([]string{
			"<team-memory-content source=\"shared\">",
			content,
			"</team-memory-content>",
		}, "\n")
	}
	return header + ":\n" + content
}

func sourceDigest(source contract.ClaudeMdSource) string {
	if digest := strings.TrimSpace(source.Digest); digest != "" {
		return digest
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(source.Content)))
	return hex.EncodeToString(sum[:])
}

func cloneUserContextPayload(payload map[string]string) userContextPayload {
	if len(payload) == 0 {
		return nil
	}
	cloned := make(userContextPayload, len(payload))
	for key, value := range payload {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cloned[key] = value
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneClaudeMdSources(sources []contract.ClaudeMdSource) []contract.ClaudeMdSource {
	if len(sources) == 0 {
		return nil
	}
	cloned := make([]contract.ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		source.Globs = append([]string(nil), source.Globs...)
		cloned = append(cloned, source)
	}
	return cloned
}
