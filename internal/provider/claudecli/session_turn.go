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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
	shared.SafeGo(s.logger, func() {
		s.executeRetry(retryCtx, retry, handle, payload)
	})
	return true
}

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

func (s *session) sendRetryLocked(retry *turnRetryState, handle *turnHandle, payload []byte) error {
	if s.pendingRetry != retry || s.activeTurn != handle || s.transport == nil || !s.transport.readyForSend() {
		s.takeActiveTurnLocked()
		return nil
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

func (s *session) prepareTurnLocked(ctx context.Context, req dto.TurnRequest) ([]byte, string, *turnHandle, error) {
	text := composeTurnText(req)
	if text == "" {
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
	payload, err := marshalTurnPayload(text)
	if err != nil {
		s.takeActiveTurnLocked()
		return nil, "", nil, err
	}
	return payload, currentTurnID(handle), handle, nil
}

func buildSteerPayload(req dto.SteerRequest) ([]byte, error) {
	text := composeTurnText(dto.TurnRequest{
		ThreadID:             req.ThreadID,
		Inputs:               req.Inputs,
		Skills:               req.Skills,
		TurnAssembly:         req.TurnAssembly,
		ManualSkillSelection: req.ManualSkillSelection,
		OutputSchema:         req.OutputSchema,
		Overrides:            req.Overrides,
	})
	if text == "" {
		return nil, errors.New("claudecli: empty steer input")
	}
	return marshalTurnPayload(text)
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
	return json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": text,
			}},
		},
	})
}

func composeTurnText(req dto.TurnRequest) string {
	return strings.TrimSpace(strings.Join(
		nonEmptyStrings(
			contract.FormatSystemContextBlock(req.TurnAssembly.SystemContext),
			req.TurnAssembly.RenderUserContextMessage(),
			buildAttachmentText(req.TurnAssembly.Attachments),
			buildTurnText(req),
		),
		"\n\n",
	))
}

func buildAttachmentText(attachments []dto.AttachmentEnvelope) string {
	blocks := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if text := strings.TrimSpace(attachment.RenderText()); text != "" {
			blocks = append(blocks, text)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func buildTurnText(req dto.TurnRequest) string {
	parts := make([]string, 0, len(req.Inputs)+len(req.Skills)+2)
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
	if section := buildSkillSection(req.Skills); section != "" {
		parts = append(parts, section)
	}
	if len(req.OutputSchema) > 0 {
		parts = append(parts, "output_schema:\n"+strings.TrimSpace(string(req.OutputSchema)))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildSkillSection(skills []dto.SkillRef) string {
	sections := make([]string, 0, 2)
	if list := buildSkillList(skills); list != "" {
		sections = append(sections, list)
	}
	if prompt := buildSkillPromptText(skills); prompt != "" {
		sections = append(sections, prompt)
	}
	return strings.Join(sections, "\n\n")
}

func buildSkillList(skills []dto.SkillRef) string {
	lines := []string{"skills:"}
	for _, skill := range skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			lines = append(lines, "- "+name)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func buildSkillPromptText(skills []dto.SkillRef) string {
	sections := make([]string, 0, len(skills))
	for _, skill := range skills {
		section := strings.TrimSpace(skill.Prompt)
		if section == "" {
			continue
		}
		if name := strings.TrimSpace(skill.Name); name != "" {
			section = "[skill:" + name + "]\n" + section
		}
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
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
