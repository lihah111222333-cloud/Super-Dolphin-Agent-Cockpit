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

// autoDreamIntentFileName 是用户手动切换 auto-dream stop hook 后写入 memory root 的记录文件。
// NewConfig 只读取这个本地文件，不依赖 uistate 模块，避免配置层反向依赖 UI 状态。
const autoDreamIntentFileName = "auto-dream-intent.json"

// autoDreamIntentFile 是磁盘上的最小 JSON 结构，只保存用户是否显式开启 auto-dream。
type autoDreamIntentFile struct {
	Enabled bool `json:"enabled"`
}

// ReadAutoDreamIntent 读取用户持久化的 auto-dream 开关。
// 返回 nil 表示没有手动覆盖，调用方继续使用环境变量或默认配置；解析失败会直接返回错误。
func ReadAutoDreamIntent(rootDir string) (*bool, error) {
	path := autoDreamIntentPath(rootDir)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	default:
		return nil, err
	}
	var payload autoDreamIntentFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	v := payload.Enabled
	return &v, nil
}

// autoDreamIntentErrorSummary 把本地 intent 读取错误压缩成 UI/配置可公开展示的诊断。
// 摘要不包含磁盘路径，避免把用户本机目录泄漏到快照或配置日志外的边界。
func autoDreamIntentErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return "auto-dream intent file is invalid JSON"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "auto-dream intent file cannot be read"
	}
	return "auto-dream intent read failed"
}

// WriteAutoDreamIntent 原子写入用户的 auto-dream 开关。
// 它先写临时文件再 rename 到目标路径，避免进程中断留下半截 JSON。
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

// autoDreamIntentPath 根据 memory root 计算 intent 文件路径。
// 空 root 返回空字符串，由读写入口分别解释为“未配置”或配置错误。
func autoDreamIntentPath(rootDir string) string {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, autoDreamIntentFileName)
}

// DetectSaveIntent 从用户文本中识别显式“记住/保存”意图。
// 只有捕获到有意义内容时才返回 Detected=true，避免把空泛指令写入长期记忆。
func DetectSaveIntent(userText string) SaveIntent {
	response := normalizeIntentText(userText)
	if response == "" {
		return SaveIntent{}
	}
	for _, pattern := range newSaveIntentPatterns() {
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

// DetectForgetIntent 从用户文本中识别显式“忘记/删除”意图。
// 泛化目标会被拒绝，避免一句宽泛表达误删大量记忆。
func DetectForgetIntent(userText string) ForgetIntent {
	response := normalizeIntentText(userText)
	if response == "" {
		return ForgetIntent{}
	}
	for _, pattern := range newForgetIntentPatterns() {
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

// inferMemoryType 根据关键词把显式写入内容归类到 user、feedback、project 或 reference。
// 未命中关键词时默认写入 user，保持最保守的个人偏好作用域。
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

// keywordScore 统计归一化文本命中特定关键词的数量。
// 调用方只比较同一批候选的相对分数，不把该值当作概率或置信度。
func keywordScore(text string, keywords ...string) int {
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(text, CanonicalName(keyword)) {
			score++
		}
	}
	return score
}

// intentDiskStores 选择显式记忆写入时可用的 private/team store。
// team store 不可用时只返回 private；project/reference 默认优先进入 team 作用域并保留 private 作为回退。
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

// teamDiskStore 根据当前线程 build context 创建带 TeamMemoryGuard 的磁盘 store。
// team memory 未启用时返回 nil，路径配置错误则直接返回错误，避免写到未受保护目录。
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

// selectExplicitWriteStore 选择显式写入应该覆盖的 store。
// 已存在条目优先原地更新；两个 store 都没有命中时写入 primary，避免跨作用域创建重复文件。
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

// upsertStructuredMemory 通过 store.UpsertStructured 原子写入结构化记忆。
// UpsertStructured 在一次磁盘锁内完成检查和写入，避免并发写入把新内容误覆盖成旧内容。
func upsertStructuredMemory(store memoryStructuredStore, entry MemoryWriteRequest, options WriteOptions) error {
	_, err := upsertStructuredMemoryReturningEntry(store, entry, options)
	return err
}

// deleteMemoryAcrossStores 在多个 store 中删除同名记忆。
// 至少一个 store 删除成功即视为成功；非 NotFound 错误会立即返回，避免隐藏权限或路径问题。
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

// defaultTeamMemoryType 判断指定记忆类型是否默认落到 team memory。
// 只有 project/reference 进入团队作用域，个人偏好和反馈默认留在 private。
func defaultTeamMemoryType(memoryType MemoryType) bool {
	switch ParseMemoryType(string(memoryType)) {
	case MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}
