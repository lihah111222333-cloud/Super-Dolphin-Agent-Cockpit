package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// HookRegistry 按 lease 管理 hook topic 订阅。
// 内部同时维护 topic 索引和 lease 到订阅的映射，所有读写都通过同一把锁保持一致。
type HookRegistry struct {
	mu            sync.RWMutex
	byTopic       map[string]map[mcp.LeaseKey]struct{}
	subscriptions map[mcp.LeaseKey]*Subscription
}

// Subscription 保存某个 lease 当前生效的 hook 订阅。
// 公共字段用于响应和调试，requestHash 只在包内判断重复订阅是否幂等。
type Subscription struct {
	SubscriptionID string
	Topics         []string
	Scope          mcp.Selector
	Filters        json.RawMessage
	Mode           string
	Version        int64

	requestHash string
}

// NewHookRegistry 创建空的 hook 订阅注册表。
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		byTopic:       make(map[string]map[mcp.LeaseKey]struct{}),
		subscriptions: make(map[mcp.LeaseKey]*Subscription),
	}
}

// Subscribe 校验并注册 hook 订阅。
// 相同 subscriptionID 和请求哈希会按幂等响应返回旧版本；内容变化会递增版本并重建 topic 索引。
func (r *HookRegistry) Subscribe(lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error) {
	var err error
	lease, err = validateLease(lease, hookSubscriptionLeaseValidation)
	if err != nil {
		return mcp.HookSubscribeResponse{}, err
	}

	subscriptionID := strings.TrimSpace(req.SubscriptionID)
	if subscriptionID == "" {
		return mcp.HookSubscribeResponse{}, fmt.Errorf("hook subscription requires subscription_id")
	}
	topics := normalizeTopics(req.Topics)
	if len(topics) == 0 {
		return mcp.HookSubscribeResponse{}, fmt.Errorf("hook subscription requires at least one topic")
	}
	scope := shared.CloneSelector(req.Scope)
	filters, err := canonicalizeFilters(req.Filters)
	if err != nil {
		return mcp.HookSubscribeResponse{}, err
	}
	mode := strings.TrimSpace(req.Mode)
	requestHash, err := hashSubscriptionRequest(topics, scope, filters, mode)
	if err != nil {
		return mcp.HookSubscribeResponse{}, err
	}

	subscription := &Subscription{
		SubscriptionID: subscriptionID,
		Topics:         topics,
		Scope:          scope,
		Filters:        filters,
		Mode:           mode,
		requestHash:    requestHash,
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if previous := r.subscriptions[lease]; previous != nil {
		if previous.SubscriptionID == subscription.SubscriptionID && previous.requestHash == subscription.requestHash {
			return mcp.HookSubscribeResponse{
				Accepted:            true,
				SubscriptionVersion: previous.Version,
				EffectiveTopics:     shared.CloneStrings(previous.Topics),
				EffectiveScope:      shared.CloneSelector(previous.Scope),
			}, nil
		}
		subscription.Version = previous.Version + 1
		r.unsubscribeLocked(lease, previous)
	} else {
		subscription.Version = 1
	}

	r.subscriptions[lease] = subscription
	for _, topic := range subscription.Topics {
		addTopicIndex(r.byTopic, topic, lease)
	}

	return mcp.HookSubscribeResponse{
		Accepted:            true,
		SubscriptionVersion: subscription.Version,
		EffectiveTopics:     shared.CloneStrings(subscription.Topics),
		EffectiveScope:      shared.CloneSelector(subscription.Scope),
	}, nil
}

// Unsubscribe 删除 lease 对应订阅并同步清理 topic 索引。
func (r *HookRegistry) Unsubscribe(lease mcp.LeaseKey) {
	lease = trimLease(lease)

	r.mu.Lock()
	defer r.mu.Unlock()

	subscription := r.subscriptions[lease]
	if subscription == nil {
		return
	}
	r.unsubscribeLocked(lease, subscription)
}

// GetSubscribers 返回订阅指定 topic 的 lease 列表。
func (r *HookRegistry) GetSubscribers(topic string) []mcp.LeaseKey {
	return r.GetSubscribersBySelector(mcp.Selector{Subscription: topic})
}

// GetSubscribersBySelector 按 topic 和可选 selector scope 返回匹配 lease。
// 返回结果稳定排序，避免 fanout 顺序因 map 遍历漂移。
func (r *HookRegistry) GetSubscribersBySelector(sel mcp.Selector) []mcp.LeaseKey {
	normalized := strings.TrimSpace(sel.Subscription)
	if normalized == "" {
		return nil
	}

	requestedScope := shared.NormalizeSelectorScope(sel.Scope)
	filterByScope := hasSelectorScope(requestedScope)

	r.mu.RLock()
	current := r.byTopic[normalized]
	subscribers := make([]mcp.LeaseKey, 0, len(current))
	for lease := range current {
		if filterByScope && !subscriptionMatchesSelectorScope(r.subscriptions[lease], requestedScope) {
			continue
		}
		subscribers = append(subscribers, lease)
	}
	r.mu.RUnlock()

	sortLeaseKeys(subscribers)
	return subscribers
}

// GetSubscription 返回 lease 当前订阅的副本。
// 返回副本是为了阻断调用方修改注册表内部 slice、scope 或 raw message。
func (r *HookRegistry) GetSubscription(lease mcp.LeaseKey) (*Subscription, bool) {
	lease = trimLease(lease)

	r.mu.RLock()
	subscription := cloneSubscription(r.subscriptions[lease])
	r.mu.RUnlock()
	if subscription == nil {
		return nil, false
	}
	return subscription, true
}

func (r *HookRegistry) unsubscribeLocked(lease mcp.LeaseKey, subscription *Subscription) {
	delete(r.subscriptions, lease)
	for _, topic := range subscription.Topics {
		removeTopicIndex(r.byTopic, topic, lease)
	}
}

func addTopicIndex(index map[string]map[mcp.LeaseKey]struct{}, topic string, lease mcp.LeaseKey) {
	current := index[topic]
	if current == nil {
		current = make(map[mcp.LeaseKey]struct{})
		index[topic] = current
	}
	current[lease] = struct{}{}
}

func removeTopicIndex(index map[string]map[mcp.LeaseKey]struct{}, topic string, lease mcp.LeaseKey) {
	current := index[topic]
	if len(current) == 0 {
		return
	}
	delete(current, lease)
	if len(current) == 0 {
		delete(index, topic)
	}
}

func cloneSubscription(subscription *Subscription) *Subscription {
	if subscription == nil {
		return nil
	}
	cloned := *subscription
	cloned.Topics = shared.CloneStrings(subscription.Topics)
	cloned.Scope = shared.CloneSelector(subscription.Scope)
	cloned.Filters = shared.CloneRawMessage(subscription.Filters)
	return &cloned
}

func hasSelectorScope(scope mcp.SelectorScope) bool {
	return scope != (mcp.SelectorScope{})
}

func subscriptionMatchesSelectorScope(subscription *Subscription, requested mcp.SelectorScope) bool {
	if subscription == nil {
		return false
	}
	return scopeMatches(requested, shared.NormalizeSelectorScope(subscription.Scope.Scope))
}

// normalizeTopics 清理、排序并去重订阅 topic。
// 空 topic 会被丢弃，调用方据此判断订阅请求是否有效。
func normalizeTopics(topics []string) []string {
	normalized := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		normalized = append(normalized, topic)
	}
	sort.Strings(normalized)

	deduped := normalized[:0]
	for _, topic := range normalized {
		if len(deduped) > 0 && deduped[len(deduped)-1] == topic {
			continue
		}
		deduped = append(deduped, topic)
	}
	return deduped
}

func sortLeaseKeys(values []mcp.LeaseKey) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].InstanceID == values[j].InstanceID {
			return values[i].Generation < values[j].Generation
		}
		return values[i].InstanceID < values[j].InstanceID
	})
}

func trimLease(lease mcp.LeaseKey) mcp.LeaseKey {
	lease.InstanceID = strings.TrimSpace(lease.InstanceID)
	return lease
}

func canonicalizeFilters(message json.RawMessage) (json.RawMessage, error) {
	message = bytes.TrimSpace(message)
	if len(message) == 0 {
		return nil, nil
	}

	var value any
	if err := json.Unmarshal(message, &value); err != nil {
		return nil, fmt.Errorf("hook subscription has invalid filters: %w", err)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("hook subscription has invalid filters: %w", err)
	}
	return shared.CloneRawMessage(encoded), nil
}

func hashSubscriptionRequest(topics []string, scope mcp.Selector, filters json.RawMessage, mode string) (string, error) {
	payload := struct {
		Topics  []string        `json:"topics"`
		Scope   mcp.Selector    `json:"scope,omitempty"`
		Filters json.RawMessage `json:"filters,omitempty"`
		Mode    string          `json:"mode,omitempty"`
	}{
		Topics:  shared.CloneStrings(topics),
		Scope:   shared.CloneSelector(scope),
		Filters: shared.CloneRawMessage(filters),
		Mode:    mode,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("hash hook subscription request: %w", err)
	}

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
