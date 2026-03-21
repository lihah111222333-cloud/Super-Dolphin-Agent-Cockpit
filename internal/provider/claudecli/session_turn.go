package claudecli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *session) prepareTurn(req dto.TurnRequest) ([]byte, string, *turnHandle, error) {
	text := buildTurnText(req)
	if text == "" {
		return nil, "", nil, errors.New("claudecli: empty turn input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureTurnAvailable(s.activeTurn); err != nil {
		return nil, "", nil, err
	}
	if err := s.restartIfNeededLocked(req); err != nil {
		return nil, "", nil, err
	}
	if s.transport == nil {
		return nil, "", nil, errors.New("claudecli: session transport is closed")
	}
	localID := strings.TrimSpace(req.LocalID)
	if localID == "" {
		localID = shared.NewID("turn")
	}
	handle := newTurnHandle(localID, localID)
	s.activeTurn = handle
	payload, err := marshalTurnPayload(text)
	if err != nil {
		s.activeTurn = nil
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
	if s.activeTurn == nil {
		return "", errors.New("claudecli: no active turn")
	}
	if err := ensureTurnOpen(s.activeTurn); err != nil {
		return "", err
	}
	if err := validateExpectedTurn(expectedTurnID, s.activeTurn.ProviderID()); err != nil {
		return "", err
	}
	if s.transport == nil {
		return "", errors.New("claudecli: session transport is closed")
	}
	return currentTurnID(s.activeTurn), nil
}

func ensureTurnAvailable(handle *turnHandle) error {
	if handle == nil {
		return nil
	}
	if err := ensureTurnOpen(handle); err != nil {
		return errors.New("claudecli: turn already running")
	}
	return nil
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
	label := "File"
	if strings.EqualFold(strings.TrimSpace(input.Type), "image") {
		label = "Image"
	}
	name := strings.TrimSpace(input.Name)
	if name != "" && name != target {
		target = name + " -> " + target
	}
	*attachmentHints = append(*attachmentHints, "["+label+": "+target+"]")
}
