package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *session) prepareTurnLocked(ctx context.Context, req dto.TurnRequest) ([]byte, string, *turnHandle, error) {
	text := buildTurnText(req)
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
	text := buildTurnText(dto.TurnRequest{
		ThreadID:             req.ThreadID,
		Inputs:               req.Inputs,
		Skills:               req.Skills,
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
