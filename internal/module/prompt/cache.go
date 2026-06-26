package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
)

// sectionInputCacheKey 根据 section 缓存策略生成缓存键。
// InputScoped 会把请求上下文哈希进 key，避免 enabled tools、output style 等输入变化时复用旧内容。
func sectionInputCacheKey(section PromptSection, input SectionContext) (string, bool) {
	switch section.CachePolicy {
	case Uncached:
		return strings.TrimSpace(section.Name), strings.TrimSpace(section.Name) != ""
	case InputScoped:
		encoded, err := json.Marshal(inputScopedCacheDependency(section, input))
		if err != nil {
			return "", false
		}
		digest := sha256.Sum256(encoded)
		return section.Name + ":" + hex.EncodeToString(digest[:]), true
	default:
		if dependency := cacheByNameSectionDependency(section, input); dependency != nil {
			encoded, err := json.Marshal(dependency)
			if err == nil {
				digest := sha256.Sum256(encoded)
				return section.Name + ":" + hex.EncodeToString(digest[:]), true
			}
		}
		return section.Name, true
	}
}

// inputScopedCacheDependency 返回 InputScoped section 参与哈希的依赖快照。
func inputScopedCacheDependency(section PromptSection, input SectionContext) any {
	return inputScopedSectionDependency(section, input)
}

// sectionCache 按名称缓存已解析的 prompt section 内容，generation 自增触发全量失效。
type sectionCache struct {
	mu         sync.RWMutex
	generation uint64
	values     map[string]*string
}

// newSectionCache 创建 section 缓存。
// V3 对输入敏感的 section 使用依赖哈希，优先避免陈旧 prompt；稳定 section 仍可按名称复用。
func newSectionCache() *sectionCache {
	return &sectionCache{values: map[string]*string{}}
}

// Generation 返回当前缓存代际，用于调用方在计算前后检测是否已失效。
func (c *sectionCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// Lookup 在 generation 未变化时读取缓存值，并返回深拷贝避免调用方改写内部状态。
func (c *sectionCache) Lookup(name string, generation uint64) (*string, bool) {
	key := strings.TrimSpace(name)
	if key == "" {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if generation != c.generation {
		return nil, false
	}
	value, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return cloneStringPtr(value), true
}

// Store 在 generation 仍匹配时保存缓存值。
// 如果计算期间发生失效，本次结果会被丢弃，避免旧 section 覆盖新一代缓存。
func (c *sectionCache) Store(name string, generation uint64, value *string) bool {
	key := strings.TrimSpace(name)
	if key == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return false
	}
	c.values[key] = cloneStringPtr(value)
	return true
}

// ObserveVolatile 记录不可缓存 section 的最近值，并返回本轮是否变化。
// 这让调用方能感知 volatile 内容变化，同时仍不把它纳入普通 Lookup 命中路径。
func (c *sectionCache) ObserveVolatile(name string, generation uint64, value *string) (*string, bool) {
	key := strings.TrimSpace(name)
	if key == "" {
		return cloneStringPtr(value), false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return cloneStringPtr(value), false
	}
	current, ok := c.values[key]
	changed := !ok || !stringPtrEqual(current, value)
	if changed {
		c.values[key] = cloneStringPtr(value)
		current = c.values[key]
	}
	return cloneStringPtr(current), changed
}

// InvalidateAll 提升代际并清空全部 section 缓存。
func (c *sectionCache) InvalidateAll(_ InvalidateReason) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.values = map[string]*string{}
	return c.generation
}

// InvalidateSections 提升代际，并删除指定 section 的名称缓存和输入哈希缓存。
func (c *sectionCache) InvalidateSections(names ...string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for key := range c.values {
			if key == name || strings.HasPrefix(key, name+":") {
				delete(c.values, key)
			}
		}
	}
	return c.generation
}

// cloneStringPtr 深拷贝字符串指针，nil 安全。
func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// stringPtrEqual 比较两个字符串指针的实际内容，nil 与空字符串不同。
func stringPtrEqual(left, right *string) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}
