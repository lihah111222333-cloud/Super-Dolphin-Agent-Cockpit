package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type documentParamsBuilder func(documentRef) any

type documentDecodeFunc[T any] func(json.RawMessage) (T, error)

type documentMissingFunc[T any] func(documentRef) (T, error)

type unionDecodeFunc[T any] func(json.RawMessage) (T, bool, error)

type hierarchyResolveFunc[I any, R any] func(context.Context, Client, I, string) (R, error)

type hierarchyMissingFunc[R any] func(documentRef) ([]R, error)

type hierarchyDirectionStep[I any, R any] struct {
	enabled func(string) bool
	method  string
	params  func(I) any
	label   string
	assign  func(*R, json.RawMessage) error
}

const emptyHierarchyPrepareMaxRetries = 2

var emptyHierarchyPrepareRetryDelay = 120 * time.Millisecond

type snapshotSyncRequest struct {
	key                     lspCacheKey
	version                 int
	cached                  bool
	previous                bootstrapStatus
	openOnly                bool
	forceReopen             bool
	refreshStaleDiagnostics bool
	scope                   ResolvedLSPToolScope
}

// requestDocument 为单文档 LSP 工具租用 client、构造参数并解码响应。
// 缺少真实 client 时只走调用方提供的 missing 分支，避免把不支持能力伪装成空结果。
func requestDocument[T any](
	ctx context.Context,
	m *manager,
	uri string,
	method string,
	build documentParamsBuilder,
	decode documentDecodeFunc[T],
	missing documentMissingFunc[T],
) (T, error) {
	var zero T
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return zero, err
	}
	if client == nil {
		if missing == nil {
			return zero, nil
		}
		return missing(ref)
	}
	raw, err := m.request(ctx, client, method, buildDocumentParams(ref, build))
	if err != nil {
		return zero, unsupportedCapabilityError(err)
	}
	if decode == nil {
		return zero, nil
	}
	return decode(raw)
}

func buildDocumentParams(ref documentRef, build documentParamsBuilder) any {
	if build == nil {
		return nil
	}
	return build(ref)
}

func unsupportedDocument[T any](operation string) documentMissingFunc[T] {
	return func(ref documentRef) (T, error) {
		var zero T
		return zero, fmt.Errorf("%s is unsupported for %s", operation, ref.languageID)
	}
}

func fallbackDocument[T any](value T) documentMissingFunc[T] {
	return func(documentRef) (T, error) {
		return value, nil
	}
}

func (m *manager) notifyDocument(
	ctx context.Context,
	uri string,
	languageID string,
	notify func(context.Context, Client, documentRef) error,
) error {
	ref, err := m.resolveDocumentRef(ctx, uri, languageID)
	if err != nil {
		return err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	m.touchWorkspaceActivity(client)
	return m.withPooledClient(client, func() error {
		return notify(ctx, client, ref)
	})
}

func (m *manager) withPooledClient(client Client, fn func() error) error {
	if client == nil {
		return fn()
	}
	leased, ok, err := m.leaseBoundClient(client)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("LSP client is no longer bound to an active workspace")
	}
	defer leased.Release()
	return fn()
}

// queryHierarchy 统一执行 call/type hierarchy 的 prepare 和方向查询。
// prepare 为空时可按语言策略重试，最终仍为空则返回空结果而不是猜测层级。
func queryHierarchy[I any, R any](
	ctx context.Context,
	m *manager,
	uri string,
	prepareMethod string,
	position protocol.Position,
	direction string,
	resolve hierarchyResolveFunc[I, R],
	missing hierarchyMissingFunc[R],
) ([]R, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		if missing == nil {
			return nil, nil
		}
		return missing(ref)
	}
	items, err := prepareHierarchy[I](ctx, m, client, prepareMethod, ref.uri, position)
	if err != nil {
		return nil, unsupportedCapabilityError(err)
	}
	if len(items) == 0 && m.shouldRetryEmptyHierarchyPrepare(ref.languageID, prepareMethod) {
		items, err = retryEmptyHierarchyPrepare[I](ctx, m, ref, client, prepareMethod, position)
		if err != nil {
			return nil, unsupportedCapabilityError(err)
		}
	}
	results := make([]R, 0, len(items))
	for _, item := range items {
		result, err := resolve(ctx, client, item, direction)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (m *manager) shouldRetryEmptyHierarchyPrepare(languageID, method string) bool {
	if method != protocol.MethodPrepareCallHierarchy {
		return false
	}
	return m.capabilityPolicy(languageID).RetryEmptyCallHierarchyPrepare
}

// retryEmptyHierarchyPrepare 在服务端短暂未建好索引时重试 hierarchy prepare。
// 每次重试前都会 open-only 预热目标文档，超过上限仍返回最后一次结果。
func retryEmptyHierarchyPrepare[T any](
	ctx context.Context,
	m *manager,
	ref documentRef,
	client Client,
	method string,
	position protocol.Position,
) ([]T, error) {
	var items []T
	for attempt := range emptyHierarchyPrepareMaxRetries {
		if err := waitBeforeEmptyHierarchyPrepareRetry(ctx, attempt); err != nil {
			return nil, err
		}
		if err := m.bootstrapDocumentOpenOnly(ctx, ref.uri); err != nil {
			return nil, err
		}
		retryItems, err := prepareHierarchy[T](ctx, m, client, method, ref.uri, position)
		if err != nil {
			return nil, err
		}
		items = retryItems
		if len(items) > 0 {
			return items, nil
		}
	}
	return items, nil
}

func waitBeforeEmptyHierarchyPrepareRetry(ctx context.Context, attempt int) error {
	delay := emptyHierarchyPrepareRetryDelay
	for range attempt {
		delay *= 2
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func unsupportedHierarchy[R any](operation string) hierarchyMissingFunc[R] {
	return func(ref documentRef) ([]R, error) {
		return nil, fmt.Errorf("%s is unsupported for %s", operation, ref.languageID)
	}
}

// resolveHierarchyDirections 按请求方向调用 incoming/outgoing 等层级方法。
// 每一步独立解码并写入结果对象，server 不支持能力时会转换成统一错误。
func resolveHierarchyDirections[I any, R any](
	ctx context.Context,
	m *manager,
	client Client,
	item I,
	direction string,
	result R,
	steps []hierarchyDirectionStep[I, R],
) (R, error) {
	for _, step := range steps {
		if step.enabled != nil && !step.enabled(direction) {
			continue
		}
		raw, err := m.request(ctx, client, step.method, step.params(item))
		if err != nil {
			return result, unsupportedCapabilityError(err)
		}
		if err := step.assign(&result, raw); err != nil {
			return result, fmt.Errorf("decode %s: %w", step.label, err)
		}
	}
	return result, nil
}

func isLSPMethodNotFound(err error) bool {
	var responseErr *responseError
	return errors.As(err, &responseErr) && responseErr.Code == jsonRPCMethodNotFound
}

func unsupportedCapabilityError(err error) error {
	if !isLSPMethodNotFound(err) {
		return err
	}
	return fmt.Errorf("%w: %w", lspmanager.ErrUnsupportedCapability, err)
}

func decodeUnionList[T any](raw json.RawMessage, decode unionDecodeFunc[T]) ([]T, error) {
	return decodeUnionListWithMode(raw, false, decode)
}

func decodeUnionListWithMode[T any](raw json.RawMessage, allowSingle bool, decode unionDecodeFunc[T]) ([]T, error) {
	payloads, err := decodeRawMessages(raw, allowSingle)
	if err != nil {
		return nil, err
	}
	results := make([]T, 0, len(payloads))
	for _, payload := range payloads {
		item, ok, err := decode(payload)
		if err != nil {
			return nil, err
		}
		if ok {
			results = append(results, item)
		}
	}
	return results, nil
}

// decodeRawMessages 将 LSP 原始响应规整为列表形态。
// allowSingle 打开时会把单个对象包装成数组，兼容不同 server 的返回差异。
func decodeRawMessages(raw json.RawMessage, allowSingle bool) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if allowSingle && trimmed[0] != '[' {
		trimmed = append([]byte{'['}, append(trimmed, ']')...)
	}
	var payloads []json.RawMessage
	if err := json.Unmarshal(trimmed, &payloads); err != nil {
		return nil, err
	}
	return payloads, nil
}

func withReadLock[T any](mu *sync.RWMutex, fn func() T) T {
	mu.RLock()
	defer mu.RUnlock()
	return fn()
}

func withWriteLock[T any](mu *sync.RWMutex, fn func() T) T {
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

func (s *lspCacheStore) persistOnMutation(changed bool) error {
	if !changed {
		return nil
	}
	if err := s.persistLocked(); err != nil {
		return fmt.Errorf("persistent cache write: %w", err)
	}
	return nil
}

// syncSnapshotToClient 将文档快照写入 LSP client 并更新缓存状态。
// 同步失败不会落缓存，确保下一次请求仍能重新 bootstrap。
func (c *bootstrapCoordinator) syncSnapshotToClient(
	ctx context.Context,
	m *manager,
	cfg workspaceConfig,
	snapshot documentSnapshot,
	req snapshotSyncRequest,
) error {
	scope := req.scope
	if scope.WorkspaceKey != "" || scope.ManagerKey != "" {
		if err := c.cache.RememberDocumentScope(snapshot.ref.uri, scope, snapshot.fingerprint); err != nil {
			return err
		}
	}
	if req.refreshStaleDiagnostics {
		m.deleteStaleDiagnosticsForSnapshot(scope, snapshot)
	}
	client, err := m.ensureClient(ctx, cfg)
	if err != nil {
		return err
	}
	err = m.withPooledClient(client, func() error {
		return c.applySnapshotUpdate(ctx, m, client, snapshot, req)
	})
	if err != nil {
		return err
	}
	if err := c.cache.Upsert(cacheValueFromSnapshot(req.key, snapshot, req.version)); err != nil {
		return err
	}
	m.deleteDiagnosticsOlderThanVersion(scope, snapshot.ref.uri, req.version)
	if err := c.cache.RememberDocumentScope(snapshot.ref.uri, scope, snapshot.fingerprint); err != nil {
		return err
	}
	c.states.complete(scope.bootstrapKey(), snapshot.ref.uri, snapshot.fingerprint, req.version)
	return nil
}

// applySnapshotUpdate 根据当前同步模式发送 didOpen/didChange。
// 已打开文档更新失败时会尝试 close+open，仍失败则重建 client 后再打开。
func (c *bootstrapCoordinator) applySnapshotUpdate(
	ctx context.Context,
	m *manager,
	client Client,
	snapshot documentSnapshot,
	req snapshotSyncRequest,
) error {
	var err error
	if req.openOnly {
		err = client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, req.version, snapshot.text)
	} else {
		err = applyBootstrapUpdate(ctx, client, snapshot, req)
	}
	if err == nil {
		return nil
	}
	if !req.openOnly {
		if reopenErr := reopenSnapshot(ctx, client, snapshot, req.version); reopenErr == nil {
			return nil
		}
	}
	replacement, rebuildErr := m.rebuildClientAfterFailure(ctx, client, false)
	if rebuildErr != nil {
		return errors.Join(err, rebuildErr)
	}
	if replacement == nil {
		return errors.Join(err, ErrClientClosed)
	}
	return m.withPooledClient(replacement, func() error {
		return replacement.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, req.version, snapshot.text)
	})
}

func reopenSnapshot(ctx context.Context, client Client, snapshot documentSnapshot, version int) error {
	if err := client.DidClose(ctx, snapshot.ref.uri); err != nil {
		return err
	}
	return client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, version, snapshot.text)
}

// cacheValueMatchesSnapshot 判断缓存记录是否仍对应当前磁盘快照。
// fingerprint 可用时优先比较 fingerprint，缺失时退到 size，避免误用旧版本号。
func cacheValueMatchesSnapshot(value lspCacheValue, snapshot documentSnapshot) bool {
	if value.Fingerprint != "" && snapshot.fingerprint != "" && value.Fingerprint != snapshot.fingerprint {
		return false
	}
	if value.Size > 0 && snapshot.size > 0 && value.Size != snapshot.size {
		return false
	}
	return true
}

func cacheValueFromSnapshot(key lspCacheKey, snapshot documentSnapshot, version int) lspCacheValue {
	return lspCacheValue{
		Key:             key,
		Version:         version,
		Fingerprint:     snapshot.fingerprint,
		ModTimeUnixNano: snapshot.modTimeNano,
		Size:            snapshot.size,
	}
}

func requestMessage(
	ctx context.Context,
	method string,
	params any,
	request func(context.Context, string, any) (json.RawMessage, error),
) (json.RawMessage, error) {
	raw, err := request(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func notifyMessage(
	ctx context.Context,
	method string,
	params any,
	notify func(context.Context, string, any) error,
) error {
	return notify(ctx, method, params)
}

func (c *client) notifyTextDocument(ctx context.Context, method string, params any) error {
	return c.Notify(ctx, method, params)
}

func decodeDocumentSymbolUnion(payload json.RawMessage) (protocol.DocumentSymbol, bool, error) {
	var symbol protocol.DocumentSymbol
	if err := json.Unmarshal(payload, &symbol); err == nil {
		return symbol, true, nil
	}
	var info protocol.SymbolInformation
	if err := json.Unmarshal(payload, &info); err != nil {
		return protocol.DocumentSymbol{}, false, fmt.Errorf("decode document symbols: %w", err)
	}
	return protocol.DocumentSymbol{
		Name:           info.Name,
		Kind:           info.Kind,
		Range:          info.Location.Range,
		SelectionRange: info.Location.Range,
	}, true, nil
}

func decodeWorkspaceSymbolUnion(payload json.RawMessage) (protocol.WorkspaceSymbolResult, bool, error) {
	var symbol protocol.WorkspaceSymbol
	if err := json.Unmarshal(payload, &symbol); err == nil && symbol.Name != "" {
		return protocol.WorkspaceSymbolResult{WorkspaceSymbol: &symbol}, true, nil
	}
	var info protocol.SymbolInformation
	if err := json.Unmarshal(payload, &info); err != nil {
		return protocol.WorkspaceSymbolResult{}, false, fmt.Errorf("decode workspace symbols: %w", err)
	}
	if info.Name == "" {
		return protocol.WorkspaceSymbolResult{}, false, nil
	}
	return protocol.WorkspaceSymbolResult{SymbolInformation: &info}, true, nil
}

// decodeCodeActionUnion 解码 LSP codeAction 可能返回的 Command 或 CodeAction。
// 未识别的空项返回 ok=false，由上层过滤而不是报错中断整个结果集。
func decodeCodeActionUnion(payload json.RawMessage) (protocol.CodeActionResult, bool, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(payload, &keys); err != nil {
		return protocol.CodeActionResult{}, false, fmt.Errorf("decode code action entry: %w", err)
	}
	if rawCommand, ok := keys["command"]; ok && len(rawCommand) > 0 && rawCommand[0] == '"' {
		var cmd protocol.Command
		if err := json.Unmarshal(payload, &cmd); err != nil {
			return protocol.CodeActionResult{}, false, fmt.Errorf("decode command action: %w", err)
		}
		return protocol.CodeActionResult{Command: &cmd}, true, nil
	}
	var action protocol.CodeAction
	if err := json.Unmarshal(payload, &action); err != nil {
		return protocol.CodeActionResult{}, false, fmt.Errorf("decode structured code action: %w", err)
	}
	return protocol.CodeActionResult{CodeAction: &action}, true, nil
}

func decodeLocationUnion(payload json.RawMessage) (protocol.LocationResult, bool, error) {
	var location protocol.Location
	if err := json.Unmarshal(payload, &location); err == nil && location.URI != "" {
		return protocol.LocationResult{Location: &location, Canonical: &location}, true, nil
	}
	var link protocol.LocationLink
	if err := json.Unmarshal(payload, &link); err != nil {
		return protocol.LocationResult{}, false, fmt.Errorf("decode locations: %w", err)
	}
	if link.TargetURI == "" {
		return protocol.LocationResult{}, false, nil
	}
	return protocol.LocationResult{LocationLink: &link}, true, nil
}
