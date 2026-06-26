package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	maxTurnRetries = 2
	retryBaseDelay = 2 * time.Second
	retryMaxDelay  = 10 * time.Second
)

// turnRetryState 保存一次 Claude turn 自动重试所需的原始 payload 和取消函数。
// payload 必须复制保存，后续重试不能依赖调用方输入切片仍然有效。
type turnRetryState struct {
	payload []byte
	attempt int
	cancel  context.CancelFunc
}

// isTransientTerminalReason 判断 Claude terminal_reason 是否适合自动重试。
func isTransientTerminalReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "rate_limited", "overloaded", "server_error":
		return true
	default:
		return false
	}
}

// isTransientErrorText 用错误文本兜住 Claude 未结构化的临时故障。
// 空错误通常来自 CLI 对上游临时错误的折叠事件，也按 transient 处理。
func isTransientErrorText(errStr string) bool {
	text := strings.ToLower(strings.TrimSpace(errStr))
	if text == "" {
		return true
	}
	return strings.Contains(text, "overloaded") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "503") ||
		strings.Contains(text, "529")
}

// isTransientTurnError 从 turn:complete 事件中识别可重试失败。
func isTransientTurnError(raw dto.RawProviderEvent) bool {
	if dataBool(raw.Data, "success") {
		return false
	}
	if reason := dataString(raw.Data, "terminal_reason"); reason != "" {
		return isTransientTerminalReason(reason)
	}
	return isTransientErrorText(dataString(raw.Data, "error"))
}

// shouldRetryTransientError 在 Claude turn 临时失败时安排一次异步重试。
// 它只在 active turn 与 pendingRetry 都未漂移时生效，并先发布 retrying 状态给前端。
func (s *session) shouldRetryTransientError(raw dto.RawProviderEvent) bool {
	if raw.EventType != "turn:complete" || dataBool(raw.Data, "success") {
		return false
	}
	var (
		retry            *turnRetryState
		handle           *turnHandle
		payload          []byte
		retryCtx         context.Context
		statusPatchEvent dto.RawProviderEvent
	)
	s.mu.Lock()
	if s.pendingRetry == nil || s.activeTurn == nil || !isTransientTurnError(raw) || s.pendingRetry.attempt >= maxTurnRetries {
		s.mu.Unlock()
		return false
	}
	retry = s.pendingRetry
	if retry.cancel != nil {
		retry.cancel()
	}
	retry.attempt++
	retryCtx, retry.cancel = context.WithCancel(context.Background())
	handle = s.activeTurn
	payload = append([]byte(nil), retry.payload...)
	statusPatchEvent = s.turnRawEventLocked("agent:status_patch", currentTurnID(handle), map[string]any{
		"status":         "retrying",
		"status_header":  "Retrying...",
		"status_details": fmt.Sprintf("Claude API error, retry attempt %d of %d...", retry.attempt, maxTurnRetries),
		"source":         "claude/retry",
		"partial":        true,
	})
	s.mu.Unlock()

	select {
	case <-retryCtx.Done():
		return false
	default:
	}

	s.dispatch(statusPatchEvent)
	runtimesafe.SafeGo(retryCtx, s.logger, "claudecli.session.executeRetry", func(context.Context) {
		s.executeRetry(retryCtx, retry, handle, payload)
	})
	return true
}

// executeRetry 等待退避时间后复用原始 payload 重新发送。
// 等待期间如果 turn 结束或 retry context 被取消，必须静默退出，避免旧请求写入新 turn。
func (s *session) executeRetry(retryCtx context.Context, retry *turnRetryState, handle *turnHandle, payload []byte) {
	if retryCtx == nil || retry == nil || handle == nil {
		return
	}
	if !waitRetryDelay(retryCtx, handle, retryDelay(retry.attempt)) {
		return
	}

	s.mu.Lock()
	err := s.sendRetryLocked(retry, handle, payload)
	s.mu.Unlock()
	if err != nil {
		s.finishTurnWithError(handle, err)
	}
}

// waitRetryDelay 等待退避时间，并把 turn 完成或 retry 取消视为放弃重试。
func waitRetryDelay(retryCtx context.Context, handle *turnHandle, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-handle.Done():
	case <-retryCtx.Done():
	}
	return false
}

// sendRetryLocked 在持锁状态下确认 turn/transport 未漂移后重发 payload。
// 任一检查失败都会清掉 active turn，让调用方用错误完成原 handle。
func (s *session) sendRetryLocked(retry *turnRetryState, handle *turnHandle, payload []byte) error {
	if s.pendingRetry != retry || s.activeTurn != handle || s.transport == nil || !s.transport.readyForSend() {
		s.takeActiveTurnLocked()
		return fmt.Errorf("claudecli: turn state drifted, cannot retry")
	}
	if err := s.transport.Send(payload); err != nil {
		s.takeActiveTurnLocked()
		return err
	}
	return nil
}

// retryDelay 根据重试次数计算指数退避，并受 retryMaxDelay 限制。
func retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return retryBaseDelay
	}
	delay := retryBaseDelay << (attempt - 1)
	if delay > retryMaxDelay {
		return retryMaxDelay
	}
	return delay
}

// errorMessageFromTerminalReason 将 Claude terminal_reason 转成用户可读错误。
func errorMessageFromTerminalReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "rate_limited":
		return "Claude API rate limited"
	case "overloaded":
		return "Claude API temporarily overloaded"
	case "server_error":
		return "Claude API server error"
	default:
		return "Claude API temporarily unavailable"
	}
}

// prepareTurnLocked 在持锁状态下完成 turn payload、重启检查和 active turn 绑定。
// 任何编码或 transport 检查失败都会回滚 active turn，避免留下不可完成的 handle。
func (s *session) prepareTurnLocked(ctx context.Context, req dto.TurnRequest) ([]byte, string, *turnHandle, error) {
	blocks := composeTurnContent(req, s.imageTracker)
	if len(blocks) == 0 {
		return nil, "", nil, errors.New("claudecli: empty turn input")
	}
	if err := ensureTurnAvailable(s.activeTurn); err != nil {
		return nil, "", nil, err
	}
	if err := s.restartIfNeededLocked(ctx, req); err != nil {
		return nil, "", nil, err
	}
	if err := ensureTurnAvailable(s.activeTurn); err != nil {
		return nil, "", nil, err
	}
	if err := ensureTransportReady(s.transport); err != nil {
		return nil, "", nil, err
	}
	localID := strings.TrimSpace(req.LocalID)
	if localID == "" {
		localID = shared.NewID("turn")
	}
	handle := newTurnHandle(localID, localID)
	s.activeTurn = handle
	payload, err := marshalTurnContentPayload(blocks)
	if err != nil {
		s.takeActiveTurnLocked()
		return nil, "", nil, err
	}
	return payload, currentTurnID(handle), handle, nil
}

// buildSteerPayload 把 steer 请求复用普通 turn 的内容组装规则编码为 CLI payload。
func buildSteerPayload(req dto.SteerRequest, tracker *imageHashTracker) ([]byte, error) {
	blocks := composeTurnContent(dto.TurnRequest{
		ThreadID:             req.ThreadID,
		Inputs:               req.Inputs,
		Skills:               req.Skills,
		TurnAssembly:         req.TurnAssembly,
		ManualSkillSelection: req.ManualSkillSelection,
		OutputSchema:         req.OutputSchema,
		Overrides:            req.Overrides,
	}, tracker)
	if len(blocks) == 0 {
		return nil, errors.New("claudecli: empty steer input")
	}
	return marshalTurnContentPayload(blocks)
}

// sendSteer 校验目标 turn 后把 steer payload 写入当前 Claude transport。
func (s *session) sendSteer(payload []byte, expectedTurnID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnID, err := s.activeSteerTurnLocked(expectedTurnID)
	if err != nil {
		return "", err
	}
	return turnID, s.transport.Send(payload)
}

// activeSteerTurnLocked 在持锁状态下确认 steer 目标仍是当前打开的 turn。
func (s *session) activeSteerTurnLocked(expectedTurnID string) (string, error) {
	if err := ensureTurnOpen(s.activeTurn); err != nil {
		return "", err
	}
	if err := validateExpectedTurn(expectedTurnID, s.activeTurn.ProviderID()); err != nil {
		return "", err
	}
	if err := ensureTransportReady(s.transport); err != nil {
		return "", err
	}
	return currentTurnID(s.activeTurn), nil
}

// ensureTurnOpen 确认 turn handle 存在且尚未完成。
func ensureTurnOpen(handle *turnHandle) error {
	if handle == nil {
		return errors.New("claudecli: no active turn")
	}
	select {
	case <-handle.Done():
		return errors.New("claudecli: no active turn")
	default:
		return nil
	}
}

// validateExpectedTurn 防止 steer 请求写入已经切换的 active turn。
func validateExpectedTurn(expectedTurnID, activeTurnID string) error {
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	if expectedTurnID == "" || strings.EqualFold(expectedTurnID, activeTurnID) {
		return nil
	}
	return fmt.Errorf("claudecli: expected turn %s, active %s", expectedTurnID, activeTurnID)
}

func marshalTurnPayload(text string) ([]byte, error) {
	return marshalTurnContentPayload([]map[string]any{{
		"type": "text",
		"text": text,
	}})
}

// marshalTurnContentPayload 把 Anthropic content block 数组包装成 Claude CLI stream-json 用户消息。
// 这里保持 wire 形态集中，避免图片和纯文本路径各自拼 envelope 时漂移。
func marshalTurnContentPayload(blocks []map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": blocks,
		},
	})
}

// composeTurnContent 将图片输入拆成 Anthropic image block，并把其余输入合并成尾部文本 block。
// 图片编码失败时降为文本提示以保住 turn；同一会话内重复图片会换成占位文本，避免重复处理 vision payload。
func composeTurnContent(req dto.TurnRequest, tracker *imageHashTracker) []map[string]any {
	blocks := make([]map[string]any, 0, len(req.Inputs)+1)
	passthrough := make([]dto.InputItem, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		blk, err := imageBlockFromInput(input)
		if err != nil {
			pkglogger.Warn("claudecli: image attach degraded to text",
				"err", err,
				"path", input.Path,
				"url", input.URL,
			)
			passthrough = append(passthrough, input)
			continue
		}
		if blk != nil {
			blocks = append(blocks, dedupedOrOriginalBlock(blk, tracker))
			// 保留随图片同行传入的说明文字，避免非前端调用方的 caption 被静默丢弃。
			if text := strings.TrimSpace(input.Content); text != "" {
				passthrough = append(passthrough, dto.InputItem{Type: "text", Content: text})
			}
			continue
		}
		passthrough = append(passthrough, input)
	}
	textReq := req
	textReq.Inputs = passthrough
	if text := composeTurnText(textReq); text != "" {
		blocks = append(blocks, map[string]any{
			"type": "text",
			"text": text,
		})
	}
	return blocks
}

// dedupedOrOriginalBlock 在本会话已发送过同图时返回文本占位 block。
// 首次出现的图片会记录原始字节 hash，后续重复图片不再触发 provider 的 vision 处理。
func dedupedOrOriginalBlock(block map[string]any, tracker *imageHashTracker) map[string]any {
	if tracker == nil {
		return block
	}
	raw := imageBlockBytes(block)
	if len(raw) == 0 {
		return block
	}
	hash, isNew := tracker.markIfNew(raw)
	if isNew {
		return block
	}
	return dedupedImagePlaceholderBlock(hash)
}

func composeTurnText(req dto.TurnRequest) string {
	return strings.TrimSpace(strings.Join(
		nonEmptyStrings(
			// NOTE: system-reminder (currentDate, runtimeExtras) and SystemContext (git status)
			// are now injected once via baseInstructions at session start.
			buildAttachmentText(req.TurnAssembly.Attachments),
			buildTurnText(req),
		),
		"\n\n",
	))
}

func buildAttachmentText(attachments []dto.AttachmentEnvelope) string {
	blocks := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if text := strings.TrimSpace(contract.RenderAttachmentText(attachment)); text != "" {
			blocks = append(blocks, text)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func buildTurnText(req dto.TurnRequest) string {
	parts := make([]string, 0, len(req.Inputs)+2)

	attachmentHints := make([]string, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		appendTurnInput(&parts, &attachmentHints, input)
	}
	if len(attachmentHints) > 0 {
		parts = append([]string{
			"The user has attached the following files. Use the Read tool to view them:\n" +
				strings.Join(attachmentHints, "\n"),
		}, parts...)
	}
	if len(req.OutputSchema) > 0 {
		parts = append(parts, "output_schema:\n"+strings.TrimSpace(string(req.OutputSchema)))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func appendTurnInput(parts *[]string, attachmentHints *[]string, input dto.InputItem) {
	if text := strings.TrimSpace(input.Content); text != "" {
		*parts = append(*parts, text)
	}
	target := strings.TrimSpace(input.Path)
	if target == "" {
		target = strings.TrimSpace(input.URL)
	}
	if target == "" {
		return
	}
	if hint := encodeAttachmentHint(input); hint != "" {
		*attachmentHints = append(*attachmentHints, hint)
	}
}
