package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type prepareInputSpec struct {
	Inputs               []InputItem
	Prompt               string
	Images               []string
	Files                []string
	CandidateSkills      []dto.SkillRef
	ManualSkillSelection bool
	Model                string
	Effort               string
	OutputSchema         json.RawMessage
	AgentID              string
	CWD                  string
	BinaryDir            string
}

type prepareSkillSpec struct {
	Selected []string
	Derived  []string
}

func buildPrepareInput(spec prepareInputSpec, skills prepareSkillSpec, caps dto.CapabilitySet) PrepareInput {
	return PrepareInput{
		Inputs:               append([]InputItem(nil), spec.Inputs...),
		Prompt:               spec.Prompt,
		Images:               append([]string(nil), spec.Images...),
		Files:                append([]string(nil), spec.Files...),
		Skills:               normalizeSkillNames(skills.Selected, skills.Derived),
		CandidateSkills:      cloneSkillRefs(spec.CandidateSkills),
		ManualSkillSelection: spec.ManualSkillSelection,
		Model:                spec.Model,
		Effort:               spec.Effort,
		OutputSchema:         append(json.RawMessage(nil), spec.OutputSchema...),
		AgentID:              strings.TrimSpace(spec.AgentID),
		CWD:                  spec.CWD,
		ThreadCaps:           caps,
		BinaryDir:            spec.BinaryDir,
	}
}

func requireTurnContext(
	ctx context.Context,
	session contract.Session,
	requestedThreadID ...string,
) (context.Context, string, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return ctx, "", err
	}
	if session == nil {
		return ctx, "", errors.New("session is required")
	}
	threadID := ""
	if len(requestedThreadID) > 0 {
		threadID = requestedThreadID[0]
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = strings.TrimSpace(session.ThreadID())
	}
	if threadID == "" {
		return ctx, "", errors.New("thread id is required")
	}
	return ctx, threadID, nil
}

func interruptAndWait(
	ctx context.Context,
	session contract.Session,
	tracker *turnTracker,
	active activeTurn,
	threadID string,
	source string,
	wait func() error,
) (bool, error) {
	if err := session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   strings.TrimSpace(source),
	}); err != nil {
		return false, err
	}
	if tracker == nil || !tracker.MarkInterruptRequested(active.localID) {
		return false, nil
	}
	if wait == nil {
		return true, nil
	}
	return true, wait()
}

func buildInterruptResult(status TurnStatus, envelope turnInterruptEnvelope) turnInterruptResult {
	result := turnInterruptResult{OK: true, TurnID: status.LocalID, Status: status.State}
	if envelope.mode == "" {
		envelope = buildTurnInterruptEnvelope(status.State, status.State, false, false, 0, false)
	}
	result.Confirmed = envelope.confirmed
	result.Mode = envelope.mode
	result.InterruptSent = envelope.interruptSent
	result.StateBefore = envelope.stateBefore
	result.StateAfter = envelope.stateAfter
	if envelope.interruptSent {
		waitedMS := envelope.waitedMS
		activeObserved := envelope.activeObserved
		result.WaitedMS = &waitedMS
		result.ActiveObserved = &activeObserved
	}
	return result
}

func normalizeSkillNames(groups ...[]string) []dto.SkillRef {
	refGroups := make([][]dto.SkillRef, 0, len(groups))
	for _, names := range groups {
		refs := make([]dto.SkillRef, 0, len(names))
		for _, raw := range names {
			refs = append(refs, dto.SkillRef{Name: raw})
		}
		refGroups = append(refGroups, refs)
	}
	return normalizeSkillRefs(refGroups...)
}

func decodeLegacyTurnParams[T any, L any](
	data []byte,
	target *T,
	legacy *L,
	merge func(current *T, legacy *L) error,
) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	if legacy == nil || merge == nil {
		return nil
	}
	if err := json.Unmarshal(data, legacy); err != nil {
		return err
	}
	return merge(target, legacy)
}

func newSteerRequest(req dto.TurnRequest, expectedTurnID string) dto.SteerRequest {
	return dto.SteerRequest{
		ThreadID:             req.ThreadID,
		ExpectedTurnID:       strings.TrimSpace(expectedTurnID),
		Inputs:               append([]dto.InputItem(nil), req.Inputs...),
		Skills:               cloneSkillRefs(req.Skills),
		ManualSkillSelection: req.ManualSkillSelection,
		OutputSchema:         append([]byte(nil), req.OutputSchema...),
		Overrides:            req.Overrides,
	}
}

func cloneSkillRefs(refs []dto.SkillRef) []dto.SkillRef {
	if len(refs) == 0 {
		return nil
	}
	cloned := make([]dto.SkillRef, len(refs))
	copy(cloned, refs)
	return cloned
}
