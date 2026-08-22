package heterociexec

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

// Binding 将一个完整 ExecutorKey 绑定到唯一 executor、目标平台和命令编码器。
type Binding struct {
	Kind     heteroci.ExecutorKind
	Key      heterociworkflow.ExecutorKey
	Platform heteroci.Platform
	Executor heterociworkflow.BatchExecutor
	Encoder  CommandEncoder
}

// Registry 是不可变的严格 executor registry；构造后不允许追加第二 executor。
type Registry struct {
	mu       sync.RWMutex
	bindings map[heterociworkflow.ExecutorKey]Binding
}

// ErrRegistry 表示 executor registry 的结构或绑定契约不合法。
var ErrRegistry = errors.New("heteroci executor registry is invalid")

// NewRegistry 构建本地节点能力 registry，要求每个绑定 key 精确指向一个本地节点。
func NewRegistry(bindings []Binding) (*Registry, error) {
	return newRegistry(bindings, []heteroci.ExecutorKind{heteroci.ExecutorKindLocalNode}, nil, nil)
}

// NewRegistryForManifest 按 manifest 的节点选择严格 required key 集合。
func NewRegistryForManifest(manifest heteroci.Manifest, bindings []Binding) (*Registry, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("%w: manifest validation: %w", ErrRegistry, err)
	}
	requiredKinds := []heteroci.ExecutorKind{heteroci.ExecutorKindLocalNode}
	nodes := make(map[string]heteroci.Platform, len(manifest.Nodes))
	requiredKeys := make([]heterociworkflow.ExecutorKey, 0, len(manifest.Nodes)+3)
	for _, node := range manifest.Nodes {
		nodes[node.NodeID] = node.Platform
		requiredKeys = append(requiredKeys, heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: node.NodeID})
	}
	return newRegistry(bindings, requiredKinds, nodes, requiredKeys)
}

// newRegistry 执行重复 key、required kind、节点覆盖和 executor identity 的统一严格检查。
func newRegistry(bindings []Binding, requiredKinds []heteroci.ExecutorKind, manifestNodes map[string]heteroci.Platform, requiredKeys []heterociworkflow.ExecutorKey) (*Registry, error) {
	if len(bindings) == 0 {
		return nil, fmt.Errorf("%w: at least one binding is required", ErrRegistry)
	}
	required := requiredKindSet(requiredKinds)
	registered, err := registerBindings(bindings, manifestNodes)
	if err != nil {
		return nil, err
	}
	if err := validateRequiredKinds(required, registered); err != nil {
		return nil, err
	}
	if err := validateRequiredKeys(requiredKeys, registered); err != nil {
		return nil, err
	}
	if err := validateManifestNodeCoverage(manifestNodes, registered); err != nil {
		return nil, err
	}
	if !manifestAllowsKinds(required, registered) {
		return nil, fmt.Errorf("%w: binding kind set does not match required manifest capability", ErrRegistry)
	}
	return &Registry{bindings: registered}, nil
}

// requiredKindSet 将 required kind 列表转为不可变语义的集合。
func requiredKindSet(kinds []heteroci.ExecutorKind) map[heteroci.ExecutorKind]struct{} {
	set := make(map[heteroci.ExecutorKind]struct{}, len(kinds))
	for _, kind := range kinds {
		set[kind] = struct{}{}
	}
	return set
}

// registerBindings 校验并登记每个完整 executor key，拒绝 executor 实例跨 key 复用。
func registerBindings(bindings []Binding, manifestNodes map[string]heteroci.Platform) (map[heterociworkflow.ExecutorKey]Binding, error) {
	registered := make(map[heterociworkflow.ExecutorKey]Binding, len(bindings))
	executorIdentities := make(map[executorIdentity]struct{}, len(bindings))
	for _, binding := range bindings {
		if err := validateBindingEntry(binding, registered, manifestNodes); err != nil {
			return nil, err
		}
		identity, comparable := identityOfExecutor(binding.Executor)
		if comparable {
			if _, exists := executorIdentities[identity]; exists {
				return nil, fmt.Errorf("%w: executor instance is reused across keys", ErrRegistry)
			}
			executorIdentities[identity] = struct{}{}
		}
		registered[binding.Key] = binding
	}
	return registered, nil
}

// validateBindingEntry 校验单条绑定的 kind、key、平台和 manifest 节点身份。
func validateBindingEntry(binding Binding, registered map[heterociworkflow.ExecutorKey]Binding, manifestNodes map[string]heteroci.Platform) error {
	if !isSupportedExecutorKind(binding.Kind) {
		return fmt.Errorf("%w: unsupported executor kind %q", ErrRegistry, binding.Kind)
	}
	if err := validateExecutorKey(binding.Key); err != nil {
		return fmt.Errorf("%w: binding key: %w", ErrRegistry, err)
	}
	if binding.Key.Kind != binding.Kind {
		return fmt.Errorf("%w: binding kind %q does not match key kind %q", ErrRegistry, binding.Kind, binding.Key.Kind)
	}
	if _, exists := registered[binding.Key]; exists {
		return fmt.Errorf("%w: duplicate executor key %v", ErrRegistry, binding.Key)
	}
	if err := validateBinding(binding); err != nil {
		return err
	}
	return validateManifestBinding(binding, manifestNodes)
}

// validateManifestBinding 将 local key 与 manifest 节点平台一一对齐。
func validateManifestBinding(binding Binding, manifestNodes map[string]heteroci.Platform) error {
	if len(manifestNodes) == 0 || binding.Kind != heteroci.ExecutorKindLocalNode {
		return nil
	}
	platform, exists := manifestNodes[binding.Key.NodeID]
	if !exists {
		return fmt.Errorf("%w: local binding references unknown node %q", ErrRegistry, binding.Key.NodeID)
	}
	if platform != binding.Platform {
		return fmt.Errorf("%w: node %q platform drift: manifest=%q binding=%q", ErrRegistry, binding.Key.NodeID, platform, binding.Platform)
	}
	return nil
}

// validateRequiredKinds 确认每个 required kind 至少有一个精确 key 绑定。
func validateRequiredKinds(required map[heteroci.ExecutorKind]struct{}, registered map[heterociworkflow.ExecutorKey]Binding) error {
	for kind := range required {
		for key := range registered {
			if key.Kind == kind {
				goto nextKind
			}
		}
		return fmt.Errorf("%w: missing executor kind %q", ErrRegistry, kind)
	nextKind:
	}
	return nil
}

// validateRequiredKeys 确认 manifest 要求的每个节点都有精确绑定。
func validateRequiredKeys(required []heterociworkflow.ExecutorKey, registered map[heterociworkflow.ExecutorKey]Binding) error {
	for _, key := range required {
		if _, exists := registered[key]; !exists {
			return fmt.Errorf("%w: missing executor key %v", ErrRegistry, key)
		}
	}
	return nil
}

// validateManifestNodeCoverage 确认 manifest 每个节点都有且仅有已登记 local key。
func validateManifestNodeCoverage(manifestNodes map[string]heteroci.Platform, registered map[heterociworkflow.ExecutorKey]Binding) error {
	if len(manifestNodes) == 0 {
		return nil
	}
	for nodeID := range manifestNodes {
		key := heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: nodeID}
		if _, exists := registered[key]; !exists {
			return fmt.Errorf("%w: manifest node %q has no local binding", ErrRegistry, nodeID)
		}
	}
	for key := range registered {
		if key.Kind == heteroci.ExecutorKindLocalNode {
			if _, exists := manifestNodes[key.NodeID]; !exists {
				return fmt.Errorf("%w: local binding references unknown node %q", ErrRegistry, key.NodeID)
			}
		}
	}
	return nil
}

// Bind 实现 workflow.ExecutorBinder，一次返回计划 key 的 exact executor map。
func (registry *Registry) Bind(plan heterociworkflow.ExecutorBindingPlan) (map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: registry is nil", ErrRegistry)
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("%w: binding plan: %w", ErrRegistry, err)
	}
	bound := make(map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, len(plan.Bindings))
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for _, requested := range plan.Bindings {
		key := heterociworkflow.ExecutorKey{Kind: requested.ExecutorKind, NodeID: requested.NodeID}
		binding, exists := registry.bindings[key]
		if !exists {
			return nil, fmt.Errorf("%w: executor key %v is not registered", ErrRegistry, key)
		}
		if isNilInterface(binding.Executor) {
			return nil, fmt.Errorf("%w: executor key %v is nil", ErrRegistry, key)
		}
		bound[key] = binding.Executor
	}
	return bound, nil
}

// EncodeCommand 使用 kind 唯一绑定的显式编码器；多 key kind 必须改用 EncodeCommandForKey。
func (registry *Registry) EncodeCommand(kind heteroci.ExecutorKind, argv []string) (Command, error) {
	if registry == nil {
		return Command{}, fmt.Errorf("%w: registry is nil", ErrRegistry)
	}
	registry.mu.RLock()
	var binding Binding
	count := 0
	for key, candidate := range registry.bindings {
		if key.Kind == kind {
			binding = candidate
			count++
		}
	}
	registry.mu.RUnlock()
	if count == 0 {
		return Command{}, fmt.Errorf("%w: executor kind %q is not registered", ErrRegistry, kind)
	}
	if count != 1 {
		return Command{}, fmt.Errorf("%w: executor kind %q has %d keys; key is required", ErrRegistry, kind, count)
	}
	return encodeWithBinding(binding, kind, argv)
}

// EncodeCommandForKey 使用完整 executor key 的显式编码器，禁止跨节点或跨 tier fallback。
func (registry *Registry) EncodeCommandForKey(key heterociworkflow.ExecutorKey, argv []string) (Command, error) {
	if registry == nil {
		return Command{}, fmt.Errorf("%w: registry is nil", ErrRegistry)
	}
	if err := validateExecutorKey(key); err != nil {
		return Command{}, err
	}
	registry.mu.RLock()
	binding, exists := registry.bindings[key]
	registry.mu.RUnlock()
	if !exists {
		return Command{}, fmt.Errorf("%w: executor key %v is not registered", ErrRegistry, key)
	}
	return encodeWithBinding(binding, key.Kind, argv)
}

// encodeWithBinding 调用绑定编码器并验证平台字段及 argv 边界完整性。
func encodeWithBinding(binding Binding, kind heteroci.ExecutorKind, argv []string) (Command, error) {
	encoded, err := binding.Encoder.Encode(binding.Platform, argv)
	if err != nil {
		return Command{}, fmt.Errorf("%w: encode %q: %w", ErrRegistry, kind, err)
	}
	if err := validateEncodedCommand(binding, argv, encoded); err != nil {
		return Command{}, err
	}
	encoded.Argv = slices.Clone(encoded.Argv)
	return encoded, nil
}

// Platform 返回 kind 的固定部署平台，未知 kind 立即失败。
func (registry *Registry) Platform(kind heteroci.ExecutorKind) (heteroci.Platform, error) {
	if registry == nil {
		return "", fmt.Errorf("%w: registry is nil", ErrRegistry)
	}
	registry.mu.RLock()
	var binding Binding
	count := 0
	for key, candidate := range registry.bindings {
		if key.Kind == kind {
			binding = candidate
			count++
		}
	}
	registry.mu.RUnlock()
	if count == 0 {
		return "", fmt.Errorf("%w: executor kind %q is not registered", ErrRegistry, kind)
	}
	if count != 1 {
		return "", fmt.Errorf("%w: executor kind %q has %d keys; key is required", ErrRegistry, kind, count)
	}
	return binding.Platform, nil
}

// PlatformForKey 返回完整 executor key 的固定部署平台。
func (registry *Registry) PlatformForKey(key heterociworkflow.ExecutorKey) (heteroci.Platform, error) {
	if registry == nil {
		return "", fmt.Errorf("%w: registry is nil", ErrRegistry)
	}
	if err := validateExecutorKey(key); err != nil {
		return "", err
	}
	registry.mu.RLock()
	binding, exists := registry.bindings[key]
	registry.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("%w: executor key %v is not registered", ErrRegistry, key)
	}
	return binding.Platform, nil
}

// SupportedExecutorKinds 返回严格 registry 要求的稳定 kind 集合副本。
func SupportedExecutorKinds() []heteroci.ExecutorKind {
	return supportedExecutorKinds()
}

// supportedExecutorKinds 返回 registry 允许的固定 kind 集合的新副本。
func supportedExecutorKinds() []heteroci.ExecutorKind {
	return []heteroci.ExecutorKind{
		heteroci.ExecutorKindLocalNode,
	}
}

// validateBinding 检查单条 binding 的 executor、encoder、平台和 kind 组合。
func validateBinding(binding Binding) error {
	if isNilInterface(binding.Executor) {
		return fmt.Errorf("%w: executor is required for %q", ErrRegistry, binding.Kind)
	}
	if isNilInterface(binding.Encoder) {
		return fmt.Errorf("%w: encoder is required for %q", ErrRegistry, binding.Kind)
	}
	if err := validatePlatform(binding.Platform); err != nil {
		return fmt.Errorf("%w: binding %q: %w", ErrRegistry, binding.Kind, err)
	}
	return nil
}

// validateExecutorKey 检查 workflow key 的 kind-specific identity，不猜测缺失字段。
func validateExecutorKey(key heterociworkflow.ExecutorKey) error {
	if err := key.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrRegistry, err)
	}
	return nil
}

// validateEncodedCommand 强制平台字段形状，防止注入编码器改变 argv 边界语义。
func validateEncodedCommand(binding Binding, argv []string, encoded Command) error {
	if encoded.Platform != binding.Platform {
		return fmt.Errorf("%w: encoder returned platform %q for expected %q", ErrCommandEncoding, encoded.Platform, binding.Platform)
	}
	if !slices.Equal(encoded.Argv, argv) {
		return fmt.Errorf("%w: encoder changed argv boundaries", ErrCommandEncoding)
	}
	if binding.Platform == heteroci.PlatformWindowsAMD64 {
		if encoded.CommandLine == "" || encoded.CommandBase64 == "" {
			return fmt.Errorf("%w: Windows encoder must return CommandLine and CommandBase64", ErrCommandEncoding)
		}
		return nil
	}
	if encoded.CommandLine != "" || encoded.CommandBase64 != "" {
		return fmt.Errorf("%w: Unix encoder must not return string command fields", ErrCommandEncoding)
	}
	return nil
}

// isSupportedExecutorKind 判断 registry 允许的固定 kind 集合。
func isSupportedExecutorKind(kind heteroci.ExecutorKind) bool {
	return slices.Contains(supportedExecutorKinds(), kind)
}

// manifestAllowsKinds 拒绝 manifest 未声明的 executor binding。
func manifestAllowsKinds(required map[heteroci.ExecutorKind]struct{}, bindings map[heterociworkflow.ExecutorKey]Binding) bool {
	for key := range bindings {
		if _, allowed := required[key.Kind]; !allowed {
			return false
		}
	}
	return true
}

type executorIdentity struct {
	typ reflect.Type
	ptr uintptr
}

func identityOfExecutor(executor heterociworkflow.BatchExecutor) (executorIdentity, bool) {
	value := reflect.ValueOf(executor)
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return executorIdentity{typ: value.Type(), ptr: value.Pointer()}, true
	default:
		return executorIdentity{}, false
	}
}

// isNilInterface 识别 typed nil executor 或 encoder，避免 nil interface 绕过注册检查。
func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
