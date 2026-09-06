package unifiedv2

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
)

const (
	// Name 是候选工具的稳定名称。
	Name = "list_agents"
	// RouteSchema 标识 route 响应 schema。
	RouteSchema = "v3.orchestration.list_agents.route"
	// RouteVersion 标识 unified_v2 route 版本。
	RouteVersion          = "unified_v2"
	maxRouteAgents        = 100
	maxRouteSnapshotNodes = 1000
	maxRouteResultSize    = 192 * 1024
	maxRouteItemSize      = 32 * 1024
	maxRouteCursors       = 256
	maxRouteCursorLength  = 512
	routeCursorTTL        = 10 * time.Minute
)

var (
	// ErrListDisabled 表示 composition 尚未提供完整依赖。
	ErrListDisabled = errors.New("unified_v2 list_agents is disabled")
	// ErrProfileUnverified 表示 profile/manifest 尚未验证。
	ErrProfileUnverified = errors.New("unified_v2 list_agents profile_unverified")
	// ErrReconciliation 表示 canonical source 或 capability 需要重协调。
	ErrReconciliation = errors.New("unified_v2 list_agents reconciliation_required")
	// ErrListInvalidRequest 表示 route 入参不符合严格 schema。
	ErrListInvalidRequest = errors.New("unified_v2 list_agents request is invalid")
	// ErrResultItemTooLarge 表示结果超过 route 字节边界。
	ErrResultItemTooLarge = errors.New("unified_v2 list_agents result_item_too_large")
	// ErrListNilClock 表示候选处理器缺少显式安全时间 seam。
	ErrListNilClock = errors.New("unified_v2 list_agents security clock is nil")
	// ErrListInvalidClock 表示安全时间观察不是非零 UTC 时间。
	ErrListInvalidClock = errors.New("unified_v2 list_agents security clock is invalid")
)

// ListAgentsInput is deliberately narrower than the legacy list input.
type ListAgentsInput struct {
	View   string `json:"view"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// LaunchProfile is a compact, non-authorizing runtime profile.
type LaunchProfile struct {
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	ProviderLabel          string   `json:"provider_label,omitempty"`
	RuntimeLabel           string   `json:"runtime_label,omitempty"`
	GatewayLabel           string   `json:"gateway_label,omitempty"`
	SupportedCreationModes []string `json:"supported_creation_modes,omitempty"`
	Enabled                bool     `json:"enabled"`
	Status                 string   `json:"status"`
	MissingRequirements    []string `json:"missing_requirements,omitempty"`
}

// RouteSource is internal source material; it is never serialized.
type RouteSource struct {
	Kind string `json:"-"`
	ID   string `json:"-"`
}

// NextAction describes an action without granting authorization.
type NextAction struct {
	Action         string       `json:"action"`
	SourceKind     string       `json:"source_kind,omitempty"`
	RequiredInputs []string     `json:"required_inputs,omitempty"`
	Source         *RouteSource `json:"-"`
}

// RouteNode is the canonical identity projection consumed by the route view.
type RouteNode struct {
	LogicalID           string        `json:"logical_id"`
	TopologyID          string        `json:"topology_id,omitempty"`
	Kind                string        `json:"kind"`
	DisplayLabel        string        `json:"display_label,omitempty"`
	State               string        `json:"state"`
	ControlStatus       string        `json:"control_status"`
	ControlLevel        string        `json:"control_level,omitempty"`
	SupportedActions    []string      `json:"supported_actions,omitempty"`
	MissingRequirements []string      `json:"missing_requirements,omitempty"`
	LastCursor          string        `json:"last_cursor,omitempty"`
	ActiveExecutionRef  string        `json:"-"`
	ActiveTurnRef       string        `json:"-"`
	ExecutionKind       string        `json:"-"`
	TurnKind            string        `json:"-"`
	Sources             []RouteSource `json:"-"`
	NextActions         []NextAction  `json:"next_actions,omitempty"`
}

// RouteExecution 是只包含 server-minted handle 的活动 execution 投影。
type RouteExecution struct {
	Handle          string `json:"execution_handle"`
	HandleExpiresAt string `json:"handle_expires_at"`
}

// RouteTurn 是只包含 server-minted handle 的活动 turn 投影。
type RouteTurn struct {
	Handle          string `json:"turn_handle"`
	HandleExpiresAt string `json:"handle_expires_at"`
}

// RouteNodeDTO is the wire-safe node shape.
type RouteNodeDTO struct {
	LogicalID           string          `json:"logical_id"`
	TopologyID          string          `json:"topology_id,omitempty"`
	Kind                string          `json:"kind"`
	DisplayLabel        string          `json:"display_label,omitempty"`
	State               string          `json:"state"`
	ControlStatus       string          `json:"control_status"`
	ControlLevel        string          `json:"control_level,omitempty"`
	SupportedActions    []string        `json:"supported_actions,omitempty"`
	MissingRequirements []string        `json:"missing_requirements,omitempty"`
	ActiveExecution     *RouteExecution `json:"active_execution,omitempty"`
	ActiveTurn          *RouteTurn      `json:"active_turn,omitempty"`
	LastCursor          string          `json:"last_cursor,omitempty"`
	NextActions         []NextActionDTO `json:"next_actions,omitempty"`
}

// NextActionDTO 是不携带内部 source fence 的 action 投影。
type NextActionDTO struct {
	Action          string   `json:"action"`
	SourceKind      string   `json:"source_kind,omitempty"`
	SourceHandle    string   `json:"source_handle,omitempty"`
	HandleExpiresAt string   `json:"handle_expires_at,omitempty"`
	RequiredInputs  []string `json:"required_inputs,omitempty"`
}

// RouteSnapshot is returned by the canonical identity route reader.
type RouteSnapshot struct {
	Schema            string          `json:"schema"`
	Version           string          `json:"version"`
	Profile           string          `json:"profile"`
	ManifestHash      string          `json:"manifest_hash"`
	TargetSetDigest   string          `json:"target_set_digest"`
	Cursor            string          `json:"cursor"`
	LaunchProfiles    []LaunchProfile `json:"launch_profiles"`
	Nodes             []RouteNode     `json:"nodes"`
	ManifestVerified  bool            `json:"-"`
	CapabilitiesReady bool            `json:"-"`
}

// ListAgentsOutput is the stable route response. Handles are server minted.
type ListAgentsOutput struct {
	Schema          string          `json:"schema"`
	Version         string          `json:"version"`
	Profile         string          `json:"profile"`
	ManifestHash    string          `json:"manifest_hash"`
	TargetSetDigest string          `json:"target_set_digest"`
	Cursor          string          `json:"cursor,omitempty"`
	NextCursor      string          `json:"next_cursor,omitempty"`
	HasMore         bool            `json:"has_more"`
	LaunchProfiles  []LaunchProfile `json:"launch_profiles"`
	Nodes           []RouteNodeDTO  `json:"nodes"`
}

// RouteReader is the preferred canonical identity route port.
type RouteReader interface {
	ReadRoute(context.Context, taskprincipal.TaskPrincipal, taskeventsource.Source) (RouteSnapshot, error)
}

// SourceReader marks the canonical event source validation port.
type SourceReader interface {
	ReadSource(context.Context, taskeventsource.Source) error
}

// Handler is a disabled-by-composition candidate and is never registered.
type ListHandler struct {
	resolver *taskprincipal.Resolver
	route    RouteReader
	source   SourceReader
	clock    ListClock
	mu       sync.Mutex
	cursors  map[string]cursorState
}

// ListClock 是候选消费的显式安全时间 seam。
type ListClock func() time.Time

type cursorState struct {
	Digest                        string
	Offset                        int
	Principal                     string
	SourceID                      string
	SourceEpoch                   string
	BindingVersion                int64
	BindingDigest, SourceFenceRef string
	ExpiresAt                     time.Time
}

// NewListAgents 构造保留给既有内部测试的非权威 wall-clock 兼容候选。
// Deprecated: candidate composition should use NewListAgentsWithClock。
func NewListAgents(resolver *taskprincipal.Resolver, routeReader RouteReader, sourceReader SourceReader) (*ListHandler, error) {
	return newListAgents(resolver, routeReader, sourceReader, compatibilityListClock)
}

// NewListAgentsWithClock 构造绑定显式 security-time seam 的 route 候选。
func NewListAgentsWithClock(resolver *taskprincipal.Resolver, routeReader RouteReader, sourceReader SourceReader, clock ListClock) (*ListHandler, error) {
	return newListAgents(resolver, routeReader, sourceReader, clock)
}

func newListAgents(resolver *taskprincipal.Resolver, routeReader RouteReader, sourceReader SourceReader, clock ListClock) (*ListHandler, error) {
	if resolver == nil || isNil(routeReader) || isNil(sourceReader) {
		return nil, ErrListDisabled
	}
	if clock == nil {
		return nil, ErrListNilClock
	}
	return &ListHandler{resolver: resolver, route: routeReader, source: sourceReader, clock: clock, cursors: make(map[string]cursorState)}, nil
}

// NewList 是 route 候选构造器的简短别名。
func NewList(resolver *taskprincipal.Resolver, routeReader RouteReader, sourceReader SourceReader) (*ListHandler, error) {
	return NewListAgents(resolver, routeReader, sourceReader)
}

// ToolHandler 返回不改变 registry 的 MCP 处理函数。
func (h *ListHandler) ToolHandler() toolspkg.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) { return h.Handle(ctx, raw) }
}

// Handle 先验证 task principal，再读取 canonical source 与 route projection。
func (h *ListHandler) Handle(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := h.readyForList(ctx); err != nil {
		return nil, err
	}
	now, err := h.observeClock()
	if err != nil {
		return nil, err
	}
	principal, err := h.resolveListPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := decodeListInput(raw)
	if err != nil {
		return nil, err
	}
	source, err := taskeventsource.FromCurrentBinding(principal.Binding)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReconciliation, err)
	}
	if err := h.readSource(ctx, principal, source); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReconciliation, err)
	}
	snapshot, err := h.readRoute(ctx, principal, source)
	if err != nil {
		if errors.Is(err, ErrReconciliation) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrProfileUnverified, err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	return h.assembleForAt(snapshot, in, principal.Principal.PrincipalRef, source, now)
}

func (h *ListHandler) resolveListPrincipal(ctx context.Context) (taskprincipal.TaskPrincipal, error) {
	principal, err := h.resolver.RequireCurrent(ctx)
	if err != nil {
		return taskprincipal.TaskPrincipal{}, fmt.Errorf("%w: %v", ErrReconciliation, err)
	}
	if err := h.validateCaller(principal); err != nil {
		return taskprincipal.TaskPrincipal{}, err
	}
	return principal, nil
}

// rejectOperator 阻止 operator principal 冒充 task route 调用者。
// validateCaller 拒绝 operator 并确认 canonical readers 均已装配。
func (h *ListHandler) validateCaller(principal taskprincipal.TaskPrincipal) error {
	if strings.EqualFold(principal.Principal.Profile, "operator") {
		return fmt.Errorf("%w: operator principal has no task route", ErrReconciliation)
	}
	return h.readersReady()
}

// readersReady 检查 canonical route 与 source reader 是否已装配。
func (h *ListHandler) readersReady() error {
	if isNil(h.route) {
		return ErrProfileUnverified
	}
	if isNil(h.source) {
		return ErrReconciliation
	}
	return nil
}

// readyForList 检查处理器依赖和上下文，阻断不完整 composition。
func (h *ListHandler) readyForList(ctx context.Context) error {
	if h == nil || h.resolver == nil {
		return ErrListDisabled
	}
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrReconciliation)
	}
	return nil
}

// readSource 通过 canonical event source 端口执行只读归因校验。
func (h *ListHandler) readSource(ctx context.Context, _ taskprincipal.TaskPrincipal, source taskeventsource.Source) error {
	return h.source.ReadSource(ctx, source)
}

// readRoute 读取 canonical identity owner 的 route projection。
func (h *ListHandler) readRoute(ctx context.Context, principal taskprincipal.TaskPrincipal, source taskeventsource.Source) (RouteSnapshot, error) {
	return h.route.ReadRoute(ctx, principal, source)
}

// validateSnapshot 检查 profile、manifest 与 capability 快照闭合性。
func validateSnapshot(s RouteSnapshot) error {
	if err := validateSnapshotTuple(s); err != nil {
		return err
	}
	if !s.ManifestVerified {
		return fmt.Errorf("%w: manifest verification absent", ErrProfileUnverified)
	}
	if !s.CapabilitiesReady {
		return fmt.Errorf("%w: capability snapshot absent", ErrReconciliation)
	}
	if len(s.LaunchProfiles) > 32 {
		return fmt.Errorf("%w: launch profile page exceeds 32", ErrResultItemTooLarge)
	}
	if len(s.Nodes) > maxRouteSnapshotNodes {
		return fmt.Errorf("%w: route snapshot exceeds %d nodes", ErrResultItemTooLarge, maxRouteSnapshotNodes)
	}
	return nil
}

// validateSnapshotTuple 校验版本化 profile 与摘要字段。
func validateSnapshotTuple(s RouteSnapshot) error {
	if s.Schema == "" || s.Profile == "" || s.ManifestHash == "" || s.TargetSetDigest == "" {
		return fmt.Errorf("%w: manifest/profile tuple incomplete", ErrProfileUnverified)
	}
	if s.Schema != RouteSchema || s.Version != RouteVersion {
		return fmt.Errorf("%w: schema/version mismatch", ErrProfileUnverified)
	}
	if err := validateRouteDigest("manifest_hash", s.ManifestHash); err != nil {
		return err
	}
	if err := validateRouteDigest("target_set_digest", s.TargetSetDigest); err != nil {
		return err
	}
	return nil
}

func validateRouteDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: invalid %s", ErrProfileUnverified, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: invalid %s", ErrProfileUnverified, name)
	}
	return nil
}

// assemble 按稳定顺序分页并检查 route 响应字节上限。
func (h *ListHandler) assemble(s RouteSnapshot, in ListAgentsInput) (ListAgentsOutput, error) {
	now, err := h.observeClock()
	if err != nil {
		return ListAgentsOutput{}, err
	}
	return h.assembleAt(s, in, now)
}

// assembleFor 绑定 caller/source 后组装分页 route 响应。
func (h *ListHandler) assembleFor(s RouteSnapshot, in ListAgentsInput, principal string, source taskeventsource.Source) (ListAgentsOutput, error) {
	now, err := h.observeClock()
	if err != nil {
		return ListAgentsOutput{}, err
	}
	return h.assembleForAt(s, in, principal, source, now)
}

// assembleAt 使用调用方已捕获的单次 UTC 安全时间观察组装 route 响应。
func (h *ListHandler) assembleAt(s RouteSnapshot, in ListAgentsInput, now time.Time) (ListAgentsOutput, error) {
	return h.assembleForAt(s, in, "", taskeventsource.Source{}, now)
}

// assembleForAt 使用单次 UTC 观察执行 caller/source 绑定的分页组装。
func (h *ListHandler) assembleForAt(s RouteSnapshot, in ListAgentsInput, principal string, source taskeventsource.Source, now time.Time) (ListAgentsOutput, error) {
	if err := validateListClock(now); err != nil {
		return ListAgentsOutput{}, err
	}
	profiles, nodes := sortedRoute(s)
	offset, err := h.pageOffsetAt(in.Cursor, s.TargetSetDigest, len(nodes), principal, source, now)
	if err != nil {
		return ListAgentsOutput{}, err
	}
	end := min(offset+in.Limit, len(nodes))
	version := s.Version
	if version == "" {
		version = RouteVersion
	}
	out := ListAgentsOutput{Schema: s.Schema, Version: version, Profile: s.Profile, ManifestHash: s.ManifestHash, TargetSetDigest: s.TargetSetDigest, Cursor: s.Cursor, LaunchProfiles: profiles, Nodes: make([]RouteNodeDTO, 0, end-offset)}
	if err := appendRouteNodesAt(&out, nodes[offset:end], now); err != nil {
		return ListAgentsOutput{}, err
	}
	if end < len(nodes) {
		if err := h.setNextCursorAt(&out, s.TargetSetDigest, end, principal, source, now); err != nil {
			return ListAgentsOutput{}, err
		}
	}
	return boundedRouteOutput(out)
}

func sortedRoute(s RouteSnapshot) ([]LaunchProfile, []RouteNode) {
	profiles := append([]LaunchProfile(nil), s.LaunchProfiles...)
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	nodes := append([]RouteNode(nil), s.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].LogicalID == nodes[j].LogicalID {
			return nodes[i].TopologyID < nodes[j].TopologyID
		}
		return nodes[i].LogicalID < nodes[j].LogicalID
	})
	return profiles, nodes
}

// pageOffset 校验 opaque cursor 的摘要、归因和有效期。
func (h *ListHandler) pageOffset(cursor, digest string, count int, principal string, source taskeventsource.Source) (int, error) {
	now, err := h.observeClock()
	if err != nil {
		return 0, err
	}
	return h.pageOffsetAt(cursor, digest, count, principal, source, now)
}

// pageOffsetAt 使用调用方观察的时间检查 cursor 有效期与 caller/source fence。
func (h *ListHandler) pageOffsetAt(cursor, digest string, count int, principal string, source taskeventsource.Source, now time.Time) (int, error) {
	if err := validateListClock(now); err != nil {
		return 0, err
	}
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > maxRouteCursorLength {
		return 0, fmt.Errorf("%w: cursor exceeds maximum length %d", ErrListInvalidRequest, maxRouteCursorLength)
	}
	if h == nil {
		return 0, ErrReconciliation
	}
	h.mu.Lock()
	h.pruneExpiredCursorsLocked(now)
	state, ok := h.cursors[cursor]
	h.mu.Unlock()
	if !ok || !cursorValidAt(state, digest, principal, source, count, now) {
		return 0, fmt.Errorf("%w: cursor is stale", ErrReconciliation)
	}
	return state.Offset, nil
}

// pruneExpiredCursorsLocked removes expired entries while the cursor mutex is held.
// Expired entries must not consume the bounded in-memory cursor budget.
func (h *ListHandler) pruneExpiredCursorsLocked(now time.Time) {
	for token, state := range h.cursors {
		if !now.Before(state.ExpiresAt) {
			delete(h.cursors, token)
		}
	}
}

// cursorValid 比较 cursor 与当前 caller/source fence 并检查有效期。
func cursorValid(state cursorState, digest, principal string, source taskeventsource.Source, count int) bool {
	return cursorValidAt(state, digest, principal, source, count, compatibilityListClock())
}

// cursorValidAt 比较显式观察时间下的 caller/source fence 与 cursor 有效期。
func cursorValidAt(state cursorState, digest, principal string, source taskeventsource.Source, count int, now time.Time) bool {
	return state.Digest == digest && state.Principal == principal && state.SourceID == source.SourceID && state.SourceEpoch == source.SourceEpoch && state.BindingVersion == source.BindingVersion && state.BindingDigest == source.BindingDigest && state.SourceFenceRef == source.SourceFenceRef && now.Before(state.ExpiresAt) && state.Offset >= 0 && state.Offset <= count
}

func appendRouteNodes(out *ListAgentsOutput, nodes []RouteNode) error {
	return appendRouteNodesAt(out, nodes, compatibilityListClock())
}

// appendRouteNodesAt 使用单次 UTC 观察为节点句柄生成统一 expiry。
func appendRouteNodesAt(out *ListAgentsOutput, nodes []RouteNode, now time.Time) error {
	if err := validateListClock(now); err != nil {
		return err
	}
	for _, node := range nodes {
		dto, err := mintNodeAt(node, now)
		if err != nil {
			return err
		}
		b, err := json.Marshal(dto)
		if err != nil {
			return err
		}
		if len(b) > maxRouteItemSize {
			return ErrResultItemTooLarge
		}
		out.Nodes = append(out.Nodes, dto)
	}
	return nil
}

func (h *ListHandler) setNextCursor(out *ListAgentsOutput, digest string, offset int, principal string, source taskeventsource.Source) error {
	now, err := h.observeClock()
	if err != nil {
		return err
	}
	return h.setNextCursorAt(out, digest, offset, principal, source, now)
}

// setNextCursorAt 使用单次 UTC 观察签发并清理分页 cursor。
func (h *ListHandler) setNextCursorAt(out *ListAgentsOutput, digest string, offset int, principal string, source taskeventsource.Source, now time.Time) error {
	if h == nil || out == nil {
		return ErrReconciliation
	}
	if err := validateListClock(now); err != nil {
		return err
	}
	token, err := mintHandle()
	if err != nil {
		return err
	}
	h.mu.Lock()
	if h.cursors == nil {
		h.mu.Unlock()
		return ErrReconciliation
	}
	h.pruneExpiredCursorsLocked(now)
	if len(h.cursors) >= maxRouteCursors {
		h.mu.Unlock()
		return ErrReconciliation
	}
	h.cursors[token] = cursorState{Digest: digest, Offset: offset, Principal: principal, SourceID: source.SourceID, SourceEpoch: source.SourceEpoch, BindingVersion: source.BindingVersion, BindingDigest: source.BindingDigest, SourceFenceRef: source.SourceFenceRef, ExpiresAt: now.Add(routeCursorTTL)}
	h.mu.Unlock()
	out.HasMore, out.NextCursor = true, token
	return nil
}

func boundedRouteOutput(out ListAgentsOutput) (ListAgentsOutput, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return ListAgentsOutput{}, err
	}
	if len(b) > maxRouteResultSize {
		return ListAgentsOutput{}, ErrResultItemTooLarge
	}
	return out, nil
}

// mintNode 将内部节点投影为只含 opaque handle 的 wire DTO。
func mintNode(n RouteNode) (RouteNodeDTO, error) {
	return mintNodeAt(n, compatibilityListClock())
}

// mintNodeAt 使用单次 UTC 观察投影节点及其 opaque handles。
func mintNodeAt(n RouteNode, now time.Time) (RouteNodeDTO, error) {
	if err := validateListClock(now); err != nil {
		return RouteNodeDTO{}, err
	}
	d := RouteNodeDTO{LogicalID: n.LogicalID, TopologyID: n.TopologyID, Kind: n.Kind, DisplayLabel: n.DisplayLabel, State: n.State, ControlStatus: n.ControlStatus, ControlLevel: n.ControlLevel, SupportedActions: append([]string(nil), n.SupportedActions...), MissingRequirements: append([]string(nil), n.MissingRequirements...), LastCursor: n.LastCursor}
	if n.ActiveExecutionRef != "" {
		h, err := mintHandle()
		if err != nil {
			return d, err
		}
		d.ActiveExecution = &RouteExecution{Handle: h, HandleExpiresAt: handleExpiryAt(now)}
	}
	if n.ActiveTurnRef != "" {
		h, err := mintHandle()
		if err != nil {
			return d, err
		}
		d.ActiveTurn = &RouteTurn{Handle: h, HandleExpiresAt: handleExpiryAt(now)}
	}
	d.NextActions = make([]NextActionDTO, 0, len(n.NextActions))
	for _, a := range n.NextActions {
		item := NextActionDTO{Action: a.Action, SourceKind: a.SourceKind, RequiredInputs: append([]string(nil), a.RequiredInputs...)}
		if a.Source != nil {
			h, err := mintHandle()
			if err != nil {
				return d, err
			}
			item.SourceHandle = h
			item.HandleExpiresAt = handleExpiryAt(now)
		}
		d.NextActions = append(d.NextActions, item)
	}
	return d, nil
}

func mintHandle() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "rh1_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func handleExpiry() string { return handleExpiryAt(compatibilityListClock()) }

func handleExpiryAt(now time.Time) string { return now.Add(10 * time.Minute).Format(time.RFC3339Nano) }

func (h *ListHandler) observeClock() (time.Time, error) {
	if h == nil || h.clock == nil {
		return time.Time{}, ErrListNilClock
	}
	now := h.clock()
	if err := validateListClock(now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

func validateListClock(now time.Time) error {
	if now.IsZero() || now.Location() != time.UTC {
		return ErrListInvalidClock
	}
	return nil
}

func compatibilityListClock() time.Time { return time.Now().UTC() }

// decodeListInput 对 route 入参执行重复、未知和范围校验。
func decodeListInput(raw json.RawMessage) (ListAgentsInput, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || !strings.HasPrefix(trimmed, "{") {
		return ListAgentsInput{}, fmt.Errorf("%w: input must be an object", ErrListInvalidRequest)
	}
	if err := scanListJSON([]byte(trimmed)); err != nil {
		return ListAgentsInput{}, fmt.Errorf("%w: %v", ErrListInvalidRequest, err)
	}
	var in ListAgentsInput
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return ListAgentsInput{}, fmt.Errorf("%w: %v", ErrListInvalidRequest, err)
	}
	if in.Limit == 0 {
		in.Limit = maxRouteAgents
	}
	if err := validateListInput(in); err != nil {
		return ListAgentsInput{}, err
	}
	return in, nil
}

// validateListInput 校验 route view、cursor 格式与分页范围。
func validateListInput(in ListAgentsInput) error {
	if in.View != "route" {
		return fmt.Errorf("%w: view must be route", ErrListInvalidRequest)
	}
	if len(in.Cursor) > maxRouteCursorLength {
		return fmt.Errorf("%w: cursor exceeds maximum length %d", ErrListInvalidRequest, maxRouteCursorLength)
	}
	if strings.TrimSpace(in.Cursor) != in.Cursor {
		return fmt.Errorf("%w: cursor has surrounding whitespace", ErrListInvalidRequest)
	}
	if in.Limit < 1 || in.Limit > maxRouteAgents {
		return fmt.Errorf("%w: limit must be 1..100", ErrListInvalidRequest)
	}
	return nil
}

func scanListJSON(raw []byte) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := scanListValue(d); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err == nil {
		return errors.New("trailing JSON payload")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// scanListValue 检测所有 JSON 对象的 null 与重复字段。
func scanListValue(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return errors.New("null values are not allowed")
	}
	if delim, ok := tok.(json.Delim); ok {
		if delim == '{' {
			return scanListObject(d)
		}
		if delim == '[' {
			return scanListArray(d)
		}
	}
	return nil
}

func scanListObject(d *json.Decoder) error {
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		name := key.(string)
		if seen[name] {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		if err := scanListValue(d); err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}

func scanListArray(d *json.Decoder) error {
	for d.More() {
		if err := scanListValue(d); err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}

// ListDefinition 返回独立于 composition 与 registry 的工具定义。
func ListDefinition() toolspkg.ToolDefinition {
	limit := toolspkg.IntegerSchema("Maximum 100 nodes; zero selects the default page size.")
	limit["minimum"], limit["maximum"] = 0, maxRouteAgents
	cursor := toolspkg.StringSchema("Opaque page cursor.")
	cursor["maxLength"] = maxRouteCursorLength
	return toolspkg.ToolDefinition{Name: Name, Description: "List canonical orchestration route state.", InputSchema: toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"view":   toolspkg.EnumStringSchema("Route view only.", "route"),
		"cursor": cursor,
		"limit":  limit,
	}, "view")}
}
