package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// autoDreamIntentFileName is the on-disk record of the user's last manual
// toggle for the auto-dream stop hook. It lives next to the memory root so
// NewConfig can pick it up without taking a dependency on uistate.
const autoDreamIntentFileName = "auto-dream-intent.json"

type autoDreamIntentFile struct {
	Enabled bool `json:"enabled"`
}

// ReadAutoDreamIntent returns the user's persisted auto-dream toggle.
// (nil, nil) means "no manual override" — env defaults still apply.
// ReadAutoDreamIntent 读取autodreamintent。
func ReadAutoDreamIntent(rootDir string) (*bool, error) {
	path := autoDreamIntentPath(rootDir)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, err
	}
	var payload autoDreamIntentFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	v := payload.Enabled
	return &v, nil
}

// WriteAutoDreamIntent persists the user's auto-dream toggle atomically.
// WriteAutoDreamIntent 写入autodreamintent。
func WriteAutoDreamIntent(rootDir string, enabled bool) error {
	path := autoDreamIntentPath(rootDir)
	if path == "" {
		return errors.New("auto-dream intent: empty root dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(autoDreamIntentFile{Enabled: enabled})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auto-dream-intent-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func autoDreamIntentPath(rootDir string) string {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, autoDreamIntentFileName)
}

// DetectSaveIntent 处理detectsaveintent。
func DetectSaveIntent(userText string) SaveIntent {
	response := normalizeIntentText(userText)
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

// DetectForgetIntent 处理detectforgetintent。
func DetectForgetIntent(userText string) ForgetIntent {
	response := normalizeIntentText(userText)
	if response == "" {
		return ForgetIntent{}
	}
	for _, pattern := range forgetIntentPatterns {
		matches := pattern.FindStringSubmatch(response)
		if len(matches) == 0 {
			continue
		}
		query := normalizeHookContent(matches[len(matches)-1])
		if !hasMeaningfulMemoryContent(query) || isGenericForgetTarget(query) {
			continue
		}
		return ForgetIntent{Detected: true, Query: query}
	}
	return ForgetIntent{}
}

// inferMemoryType 处理infer记忆type。
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

func (h *MemoryLifecycleHooks) intentDiskStores(ctx context.Context, threadID string, memoryType MemoryType) (memoryStructuredStore, memoryStructuredStore, error) {
	privateStore, err := h.diskStore()
	if err != nil {
		return nil, nil, err
	}
	teamStore, err := h.teamDiskStore(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if teamStore == nil {
		return privateStore, nil, nil
	}
	if defaultTeamMemoryType(memoryType) {
		return teamStore, privateStore, nil
	}
	return privateStore, teamStore, nil
}

func (h *MemoryLifecycleHooks) teamDiskStore(ctx context.Context, threadID string) (memoryStructuredStore, error) {
	if h == nil || h.team == nil {
		return nil, nil
	}
	buildCtx := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(threadID)).buildCtx()
	if !h.team.IsTeamMemoryEnabled(buildCtx) {
		return nil, nil
	}
	root, err := configuredTeamMemPath(h.team, buildCtx)
	if err != nil {
		return nil, err
	}
	return newDiskStoreWithGuard(root, NewTeamMemoryGuard(h.team), h.memoryCoordinator())

}

// selectExplicitWriteStore 选择explicitwrite存储。
func selectExplicitWriteStore(name string, primary, secondary memoryStructuredStore) (memoryStructuredStore, error) {
	for _, store := range []memoryStructuredStore{primary, secondary} {
		if store == nil {
			continue
		}
		if _, err := store.Read(name); err == nil {
			return store, nil
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return nil, err
		}
	}
	if primary != nil {
		return primary, nil
	}

	return nil, errors.New("memory store is nil")
}

// upsertStructuredMemory writes the entry atomically via the store's
// UpsertStructured implementation, which acquires the disk store lock
// once for the full check-and-write sequence. Phase 自有.1a replaced the
// previous Create-then-Update pattern, where two independent lock
// acquisitions left a window for a racing writer to convert a
// Create-failed-with-AlreadyExists into an Update that overwrote
// concurrently-written content.
func upsertStructuredMemory(store memoryStructuredStore, entry MemoryWriteRequest, options WriteOptions) error {
	_, err := upsertStructuredMemoryReturningEntry(store, entry, options)
	return err
}

// deleteMemoryAcrossStores 删除记忆acrossstores。
func deleteMemoryAcrossStores(name string, options WriteOptions, stores ...memoryStructuredStore) error {
	deleted := false
	for _, store := range stores {
		if store == nil {
			continue
		}
		if err := store.Delete(name, options); err == nil {
			deleted = true
			continue
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return err
		}
	}
	if deleted {
		return nil
	}
	return ErrMemoryNotFound
}

func defaultTeamMemoryType(memoryType MemoryType) bool {
	switch ParseMemoryType(string(memoryType)) {
	case MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}
