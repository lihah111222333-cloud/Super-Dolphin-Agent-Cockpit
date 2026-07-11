package memory

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"time"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const backgroundExtractTimeout = 5 * time.Second

func (h *MemoryLifecycleHooks) memoryLogger() *pkglogger.Logger {
	if h != nil && h.logger != nil {
		return h.logger
	}
	return pkglogger.Get()
}

// onTurnStarted 记录 thread 当前活跃 turn，用于后续 tool diff 无 turnID 时补齐归属。
// 只写内存状态，不做磁盘 I/O，因此可以从订阅回调的轻量路径调用。
func (h *MemoryLifecycleHooks) onTurnStarted(evt turndto.TurnStarted) {
	threadID := strings.TrimSpace(evt.ThreadID)
	turnID := strings.TrimSpace(evt.TurnID)
	if h == nil || threadID == "" || turnID == "" {
		return
	}
	h.stateMu.Lock()
	h.activeTurns[threadID] = turnID
	h.stateMu.Unlock()
}

// onTurnCompleted 在 worker goroutine 上处理 turn 结束后的记忆写入和抽取。
// 它会消费本轮工具写入追踪；若 AutoMem 已由工具改写，则让 prompt 区块失效，
// 再按 thread 抽取状态机启动后台抽取。
func (h *MemoryLifecycleHooks) onTurnCompleted(ctx context.Context, evt turndto.TurnCompleted) {
	if h == nil {
		return
	}
	h.onTurnEnd(ctx, evt)
	handled := h.consumeTurnTracking(evt.ThreadID, evt.TurnID)
	if handled {
		// 本轮工具已写入 AutoMem，启动时缓存的 MEMORY.md 入口可能不再等于磁盘内容；
		// 立即失效可让下一次 AssembleStart 重新渲染。
		h.invalidateMemorySections()
	}
	if !h.shouldExtractThread(ctx, evt) {
		return
	}
	h.enqueueBackgroundExtraction(strings.TrimSpace(evt.ThreadID), handled)
}

// onTurnInputReceived 在 worker goroutine 上处理用户显式记忆指令。
// 先把输入文本登记到内存追踪表，再执行可能写盘的 remember/forget；成功处理后会标记本轮，
// 避免 turn completed 时再次把同一段文本走自动抽取。
func (h *MemoryLifecycleHooks) onTurnInputReceived(ctx context.Context, ev turndto.TurnInputReceived) {
	if h == nil || !h.enabled {
		return
	}
	if err := contextErr(ctx); err != nil {
		return
	}
	key, text, ok := h.rememberTurnInput(ev)
	if !ok {
		return
	}
	turn := turnCompletedFromInput(ev)
	handled, action, err := h.handleExplicitUserMemoryIntent(ctx, turn, text)
	h.handleExplicitIntentError(turn, handled, action, err)
	if handled && err == nil {
		h.markHandledTurnInput(key)
	}
}

// rememberTurnInput 记录一条用户显式记忆输入并返回追踪键。
// 它只接收用户来源的文本消息，非用户来源或缺少 thread/turn 标识时直接忽略。
func (h *MemoryLifecycleHooks) rememberTurnInput(ev turndto.TurnInputReceived) (string, string, bool) {
	if !isExplicitMemoryUserInput(ev) {
		return "", "", false
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	turnID := strings.TrimSpace(ev.TurnID)
	if threadID == "" || turnID == "" {
		return "", "", false
	}
	text := strings.TrimSpace(ev.Text)
	key := turnTrackingKey(threadID, turnID)
	h.stateMu.Lock()
	if h.turnInputs == nil {
		h.turnInputs = map[string]string{}
	}
	h.turnInputs[key] = text
	h.stateMu.Unlock()
	return key, text, true
}

// consumeTurnInput 取出并删除指定显式记忆输入。
// 删除和读取在同一把 stateMu 下完成，避免同一 turn 被重复消费。
func (h *MemoryLifecycleHooks) consumeTurnInput(key string) (string, bool) {
	if h == nil || key == "" {
		return "", false
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	text, ok := h.turnInputs[key]
	if ok {
		delete(h.turnInputs, key)
	}
	return text, ok
}

// clearTurnInput 删除尚未消费的显式记忆输入。
// 失败路径会调用它清理临时追踪，避免后续 turn 误用旧文本。
func (h *MemoryLifecycleHooks) clearTurnInput(key string) {
	if h == nil || key == "" {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	delete(h.turnInputs, key)
}

// markHandledTurnInput 标记显式记忆输入已完成业务处理。
// 该标记会在 turn 完成阶段被消费，用来区分“用户已明确写记忆”和普通对话抽取。
func (h *MemoryLifecycleHooks) markHandledTurnInput(key string) {
	if h == nil || key == "" {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.handledTurnInputs == nil {
		h.handledTurnInputs = map[string]struct{}{}
	}
	h.handledTurnInputs[key] = struct{}{}
}

// consumeHandledTurnInput 读取并删除显式记忆已处理标记。
// 返回值只对当前 key 有效；删除后同一输入不会再次影响后台抽取。
func (h *MemoryLifecycleHooks) consumeHandledTurnInput(key string) bool {
	if h == nil || key == "" {
		return false
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	_, ok := h.handledTurnInputs[key]
	if ok {
		delete(h.handledTurnInputs, key)
	}
	return ok
}

// turnCompletedFromInput 生成显式记忆处理所需的最小 turn 完成上下文。
// 这里不伪造失败状态；真正的写入错误由 handleExplicitIntentError 统一记录。
func turnCompletedFromInput(ev turndto.TurnInputReceived) turndto.TurnCompleted {
	return turndto.TurnCompleted{TurnHeader: ev.TurnHeader, Success: true}
}

// isExplicitMemoryUserInput 判断事件是否可作为显式记忆指令处理。
// 只接受用户文本类输入，工具、系统或附件类事件不能进入写记忆路径。
func isExplicitMemoryUserInput(ev turndto.TurnInputReceived) bool {
	if strings.TrimSpace(ev.Text) == "" {
		return false
	}
	source := strings.TrimSpace(ev.Source)
	if source != "" && !strings.EqualFold(source, "user") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ev.InputType)) {
	case "", "message", "text":
		return true
	default:
		return false
	}
}

// normalizeIntentText 统一用户指令文本的换行和外层空白。
// 意图识别基于规范化后的文本，避免 CRLF 或尾部空格影响 remember/forget 判断。
func normalizeIntentText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// isGenericForgetTarget 拒绝过于泛化的 forget 目标。
// 这类代词无法稳定映射到具体记忆条目，继续删除会造成不可预期的数据丢失。
func isGenericForgetTarget(text string) bool {
	switch CanonicalName(text) {
	case "it", "this", "that", "memory", "这", "这个", "这点", "这条":
		return true
	default:
		return false
	}
}

// onTurnTerminated 清理未正常完成 turn 的工具写入追踪。
// 中断和 stalled 都走这里，确保 callID 到 turn 的映射不会跨 turn 残留。
func (h *MemoryLifecycleHooks) onTurnTerminated(threadID, turnID string) {
	if h == nil {
		return
	}
	h.consumeTurnTracking(threadID, turnID)
}

// onToolCallBegin 记录工具调用归属的 thread/turn。
// 后续 diff 事件可能只带 callID，必须依赖这张表判断是否写入了 AutoMem。
func (h *MemoryLifecycleHooks) onToolCallBegin(ev tooldto.ToolCallBegin) {
	callID := strings.TrimSpace(ev.CallID)
	threadID := strings.TrimSpace(ev.ThreadID)
	turnID := strings.TrimSpace(ev.TurnID)
	if h == nil || callID == "" || threadID == "" || turnID == "" {
		return
	}
	h.stateMu.Lock()
	h.callTurns[callID] = toolCallScope{threadID: threadID, turnID: turnID}
	h.activeTurns[threadID] = turnID
	h.stateMu.Unlock()
}

// onToolDiffUpdated 收集工具 diff 触达的文件路径，用于 turn 完成时判断记忆是否已被工具写入。
// 事件缺少文件列表时会从 diff 文本解析；无法定位 thread/turn 或文件为空则忽略。
func (h *MemoryLifecycleHooks) onToolDiffUpdated(ev tooldto.ToolDiffUpdated) {
	if h == nil {
		return
	}
	scope, ok := h.resolveToolScope(ev)
	if !ok {
		return
	}
	files := uniqueNonEmptyStrings(append([]string(nil), ev.Files...))
	if len(files) == 0 {
		files = extractDiffFiles(ev.DiffText)
	}
	if len(files) == 0 {
		return
	}
	key := turnTrackingKey(scope.threadID, scope.turnID)
	h.stateMu.Lock()
	if _, ok := h.turnWrites[key]; !ok {
		h.turnWrites[key] = map[string]struct{}{}
	}
	for _, file := range files {
		h.turnWrites[key][file] = struct{}{}
	}
	h.stateMu.Unlock()
}

// DrainPendingExtraction 关闭后台抽取入口并等待已入队任务结束。
// 关闭标志必须先于 Wait 设置，避免并发 Add 和 Wait 在计数归零时触发 panic。
func (h *MemoryLifecycleHooks) DrainPendingExtraction(ctx context.Context) error {
	if h == nil {
		return nil
	}
	// 在 drainMu 下设置 drainClosed：已经拿到锁的 enqueue 会先完成 Add(1)，
	// 后续 enqueue 会看到关闭标志并跳过 Add。否则 WaitGroup 计数刚归零时
	// 再 Add 会与 Wait 竞态并触发 panic。
	h.drainMu.Lock()
	h.drainClosed = true
	h.drainMu.Unlock()
	done := make(chan struct{})
	safego.Go(ctx, h.memoryLogger(), "memory.extraction.drain", func(context.Context) {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				h.memoryLogger().Error("memory: recovered drain panic", "panic", r)
			}
		}()
		h.extractWG.Wait()
	})
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExtractAndSave 对外暴露一次同步抽取并写入记忆的入口。
// 调用方传入已经读取好的 transcript 和 manifest；写入选项仍由 hooks 统一补齐。
func (h *MemoryLifecycleHooks) ExtractAndSave(
	ctx context.Context,
	transcript []providerdto.Message,
	manifest []MemoryEntry,
) error {
	return h.extractAndSave(ctx, transcript, manifest, h.writeOptions(ctx, ""))
}

// extractAndSave 执行抽取器、写入磁盘，并在成功写入后失效记忆 prompt 区块。
// extractOnStop 关闭时直接跳过；抽取错误和写入错误原样返回给调用方处理。
func (h *MemoryLifecycleHooks) extractAndSave(
	ctx context.Context,
	transcript []providerdto.Message,
	manifest []MemoryEntry,
	options WriteOptions,
) error {
	if h == nil || !h.extractOnStop {
		return nil
	}
	items, err := h.extractorOrDefault().Extract(ctx, h.extractFn, ExtractParams{
		Transcript: transcript,
		Manifest:   manifest,
	})
	if err != nil || len(items) == 0 {
		return err
	}
	if err := h.saveExtractedMemories(items, options); err != nil {
		return err
	}
	h.invalidateMemorySections()
	return nil
}

// enqueueBackgroundExtraction 为 thread 启动或合并后台抽取任务。
// ExtractionState 保证同一 thread 同时只有一个 goroutine；drainClosed 后会回滚 pending 状态，
// 防止服务关闭期间留下永远 in-progress 的状态。
func (h *MemoryLifecycleHooks) enqueueBackgroundExtraction(threadID string, handled bool) {
	if h == nil || threadID == "" {
		return
	}
	state := h.extractionState(threadID)
	start := state.markPending(handled)
	if !start {
		return
	}
	// 保护 Add(1) 不与 Drain 的 Wait 并发。markPending 已经把状态置为
	// in-progress；如果服务正在关闭而放弃入队，必须 finish 回滚，否则该 thread
	// 后续抽取会被永久拒绝。
	h.drainMu.Lock()
	if h.drainClosed {
		h.drainMu.Unlock()
		state.finish()
		return
	}
	h.extractWG.Add(1)
	h.drainMu.Unlock()
	safego.Go(context.Background(), h.memoryLogger(), "memory.background_extraction", func(context.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				h.memoryLogger().Error("memory: recovered background extraction panic",
					"thread_id", threadID,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				state.fail(errors.New("memory: background extraction panicked"))
			}
		}()
		h.runBackgroundExtraction(threadID, state)
	})
}

// runBackgroundExtraction 持续处理同一 thread 上被合并的抽取周期。
// 每轮成功提交游标后才继续下一轮，确保新 transcript 不会因并发入队被跳过。
func (h *MemoryLifecycleHooks) runBackgroundExtraction(threadID string, state *ExtractionState) {
	defer h.extractWG.Done()
	for {
		cursor, handled, ok := state.beginCycle()
		if !ok {
			state.finish()
			return
		}
		nextCursor, err := h.executeBackgroundExtraction(threadID, cursor, handled)
		if err != nil {
			state.fail(err)
			return
		}
		if !state.commit(nextCursor) {
			return
		}
	}
}

// executeBackgroundExtraction 读取 thread 历史、按游标截取新增窗口并写入抽取结果。
// 若本轮已有显式或工具记忆写入，只推进游标不再自动抽取，避免重复保存同一事实。
func (h *MemoryLifecycleHooks) executeBackgroundExtraction(
	threadID string,
	cursor int64,
	handled bool,
) (int64, error) {
	ctx, cancel := ctxutil.WithTimeout(context.Background(), backgroundExtractTimeout)
	defer cancel()
	messages, err := h.readTranscript(ctx, threadID)
	if err != nil {
		return cursor, err
	}
	latest := latestTranscriptCursor(messages)
	if latest <= cursor {
		return cursor, nil
	}
	if handled {
		return latest, nil
	}
	window := transcriptWindow(messages, cursor)
	if len(window) == 0 {
		return latest, nil
	}
	manifest, err := h.buildManifest(ctx)
	if err != nil {
		return cursor, err
	}
	if err := h.extractAndSave(ctx, window, manifest, h.writeOptions(ctx, threadID)); err != nil {
		return cursor, err
	}
	return latest, nil
}

// readTranscript 从线程存储读取完整历史并转成抽取器输入格式。
// 缺少线程存储是配置错误，必须返回错误而不是悄悄跳过抽取。
func (h *MemoryLifecycleHooks) readTranscript(ctx context.Context, threadID string) ([]providerdto.Message, error) {
	if h == nil || h.threads == nil {
		return nil, errors.New("memory transcript source is not configured")
	}
	messages, err := h.threads.ReadHistory(ctx, strings.TrimSpace(threadID), 0)
	if err != nil {
		return nil, err
	}
	return normalizeTranscriptMessages(messages), nil
}

// buildManifest 构建当前磁盘记忆索引快照。
// 上下文取消会在读盘前失败返回，避免关闭流程中继续扫描记忆目录。
func (h *MemoryLifecycleHooks) buildManifest(ctx context.Context) ([]MemoryEntry, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	store, err := h.diskStore()
	if err != nil {
		return nil, err
	}
	manifest, err := h.manifestBuilderOrDefault().BuildManifest(store.Root())
	if err != nil {
		return nil, newMemoryExtractManifestError(err)
	}
	return manifest, nil
}

// saveExtractedMemories 将抽取结果逐条写入磁盘记忆存储。
// 已存在同名条目时改走更新，其它写入错误立即返回，避免部分失败被吞掉。
func (h *MemoryLifecycleHooks) saveExtractedMemories(items []ExtractedMemory, options WriteOptions) error {
	store, err := h.diskStore()
	if err != nil {
		return err
	}
	for _, item := range items {
		entry := buildConsolidatedMemoryEntry(item)
		if _, err := store.Create(entry, options); err == nil {
			continue
		} else if !errors.Is(err, ErrMemoryAlreadyExists) {
			return err
		}
		if _, err := store.Update(entry, options); err != nil {
			return err
		}
	}
	return nil
}

// invalidateMemorySections 失效所有持久化记忆相关 prompt 区块。
// 抽取、显式写入或工具改写成功后都要调用，保证下一次汇编读取最新磁盘内容。
func (h *MemoryLifecycleHooks) invalidateMemorySections() {
	if h == nil {
		return
	}
	invalidateDurableMemorySections(h.sections)
}

// extractorOrDefault 返回可替换的抽取器实例。
// 测试可注入定制抽取器；生产路径缺省使用标准 MemoryExtractor。
func (h *MemoryLifecycleHooks) extractorOrDefault() *MemoryExtractor {
	if h != nil && h.extractor != nil {
		return h.extractor
	}
	return NewMemoryExtractor()
}

// manifestBuilderOrDefault 返回可替换的 manifest 构建器。
// 后台抽取依赖它获取当前记忆索引，测试中可替换以固定输入或错误。
func (h *MemoryLifecycleHooks) manifestBuilderOrDefault() *ManifestBuilder {
	if h != nil && h.manifestBuilder != nil {
		return h.manifestBuilder
	}
	return NewManifestBuilder()
}

// extractionState 获取 thread 级后台抽取状态，缺失时在锁内创建。
// 状态对象负责 pending、游标和 in-progress 协调，调用方不得自行跨 thread 复用。
func (h *MemoryLifecycleHooks) extractionState(threadID string) *ExtractionState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	state, ok := h.states[threadID]
	if ok {
		return state
	}
	state = &ExtractionState{}
	h.states[threadID] = state
	return state
}

// consumeTurnTracking 清理 turn 级工具写入追踪并返回是否触达 AutoMem。
// 它同时移除 callID 映射和 active turn，防止下一轮 diff 继承旧归属。
func (h *MemoryLifecycleHooks) consumeTurnTracking(threadID, turnID string) bool {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if h == nil || threadID == "" || turnID == "" {
		return false
	}
	key := turnTrackingKey(threadID, turnID)
	cfg := &Config{RootDir: h.rootDir, ProjectRoot: h.projectRoot, AutoMemPathOverride: h.autoMemPathOverride}
	h.stateMu.Lock()
	delete(h.activeTurns, threadID)
	files := turnWriteFiles(h.turnWrites[key])
	delete(h.turnWrites, key)
	for callID, scope := range h.callTurns {
		if scope.threadID == threadID && scope.turnID == turnID {
			delete(h.callTurns, callID)
		}
	}
	h.stateMu.Unlock()
	return hasHandledAutoMemoryWrite(cfg, files)
}

// resolveToolScope 为 diff 事件恢复 thread/turn 归属。
// 优先使用 callID 精确匹配；缺失时退回当前活跃 turn，仍无法定位则拒绝记录。
func (h *MemoryLifecycleHooks) resolveToolScope(ev tooldto.ToolDiffUpdated) (toolCallScope, bool) {
	threadID := strings.TrimSpace(ev.ThreadID)
	callID := strings.TrimSpace(ev.CallID)
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if callID != "" {
		if scope, ok := h.callTurns[callID]; ok {
			return scope, true
		}
	}
	turnID := strings.TrimSpace(h.activeTurns[threadID])
	if threadID == "" || turnID == "" {
		return toolCallScope{}, false
	}
	return toolCallScope{threadID: threadID, turnID: turnID}, true
}

// hasHandledAutoMemoryWrite 判断本轮工具写入是否已经覆盖 AutoMem 路径。
// 该结果会抑制自动抽取并触发 prompt 区块失效，避免同一轮重复写记忆。
func hasHandledAutoMemoryWrite(cfg *Config, files []string) bool {
	for _, file := range files {
		if !isAutoMemPath(cfg, file) {
			continue
		}
		return true
	}
	return false
}
