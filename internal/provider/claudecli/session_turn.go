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

type turnRetryState struct {
	payload []byte
	attempt int
	cancel  context.CancelFunc
}

func isTransientTerminalReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "rate_limited", "overloaded", "server_error":
		return true
	default:
		return false
	}
}

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

func isTransientTurnError(raw dto.RawProviderEvent) bool {
	if dataBool(raw.Data, "success") {
		return false
	}
	if reason := dataString(raw.Data, "terminal_reason"); reason != "" {
		return isTransientTerminalReason(reason)
	}
	return isTransientErrorText(dataString(raw.Data, "error"))
}

// shouldRetryTransientError 判断重试transient错误是否可用。
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

// executeRetry 执行重试。
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

// sendRetryLocked 处理send重试locked。
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

// prepareTurnLocked 准备turnlocked。
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

func (s *session) sendSteer(payload []byte, expectedTurnID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnID, err := s.activeSteerTurnLocked(expectedTurnID)
	if err != nil {
		return "", err
	}
	return turnID, s.transport.Send(payload)
}

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

// marshalTurnContentPayload wraps an Anthropic-style content blocks array in
// the claude CLI stream-json user-message envelope.
func marshalTurnContentPayload(blocks []map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": blocks,
		},
	})
}

// composeTurnContent splits image-typed inputs into native Anthropic image
// content blocks and routes everything else through composeTurnText into a
// single trailing text block. Image blocks are emitted first because the model
// reads content in order and most prompts ask about the image afterwards.
//
// If an image cannot be encoded (oversize, read error, unsupported MIME), the
// input falls through to the text-hint path so the turn is not lost.
//
// When tracker is non-nil, identical images sent earlier in the same session
// (matched by sha256 of the raw bytes) are replaced with a small text
// placeholder so the API does not re-process the same vision payload.
// composeTurnContent 处理composeturn内容。
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
			// Preserve any caller-provided caption text that came riding on
			// the same image input. The frontend currently emits text in a
			// separate {type:'text'} item, but other callers may inline a
			// caption with the image; passing it through keeps that text in
			// the prompt instead of silently dropping it.
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

// dedupedOrOriginalBlock returns a small text placeholder when the image
// block's bytes have already been sent in this session, otherwise it returns
// the original block (and records the bytes so future dupes hit the cache).
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
