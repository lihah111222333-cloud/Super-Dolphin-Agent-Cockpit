package tools

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

const (
	orchPosAgent     = "agent"
	orchPosCommand   = "command"
	orchPosDAG       = "dag"
	orchPosNode      = "node"
	orchPosPrompt    = "prompt"
	orchPosRun       = "run"
	orchPosRunID     = "run_id"
	orchPosShared    = "shared"
	orchPosWorkspace = "workspace"
)

type orchPos struct {
	Raw             string
	AgentID         string
	CommandKey      string
	DagKey          string
	NodeKey         string
	PromptKey       string
	RunKey          string
	RunID           int64
	SharedPath      string
	WorkspaceRunKey string
}

// parseOrchPos 解析orchpos。
func parseOrchPos(raw string) (orchPos, error) {
	pos := orchPos{Raw: strings.TrimSpace(raw)}
	if pos.Raw == "" {
		return pos, nil
	}
	for _, kind := range []string{orchPosShared, orchPosPrompt, orchPosCommand} {
		if value, ok := parseSinglePayloadPos(pos.Raw, kind); ok {
			return assignSinglePayloadPos(pos, kind, value)
		}
	}
	for _, segment := range strings.Split(pos.Raw, "/") {
		key, value, err := parseOrchPosSegment(segment)
		if err != nil {
			return orchPos{}, invalidOrchPos(pos.Raw, "next: use pos like agent:<agent_id>, dag:<dag_key>, dag:<dag_key>/run:<run_key>, or workspace:<run_key>")
		}
		if err := assignOrchPosSegment(&pos, key, value); err != nil {
			return orchPos{}, invalidOrchPos(pos.Raw, err.Error())
		}
	}
	if err := validateOrchPosShape(pos); err != nil {
		return orchPos{}, invalidOrchPos(pos.Raw, err.Error())
	}
	return pos, nil
}

func parseSinglePayloadPos(raw, kind string) (string, bool) {
	prefix := kind + ":"
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, prefix)), true
}

func assignSinglePayloadPos(pos orchPos, kind, value string) (orchPos, error) {
	if value == "" {
		return orchPos{}, invalidOrchPos(pos.Raw, fmt.Sprintf("next: pass %s:<key>", kind))
	}
	switch kind {
	case orchPosShared:
		pos.SharedPath = value
	case orchPosPrompt:
		pos.PromptKey = value
	case orchPosCommand:
		pos.CommandKey = value
	default:
		return orchPos{}, invalidOrchPos(pos.Raw, "next: use a supported orch pos kind")
	}
	return pos, nil
}

// parseOrchPosSegment 解析orchpossegment。
func parseOrchPosSegment(segment string) (string, string, error) {
	if strings.TrimSpace(segment) != segment || segment == "" {
		return "", "", errors.New("empty pos segment")
	}
	key, value, ok := strings.Cut(segment, ":")
	if !ok {
		return "", "", errors.New("missing kind separator")
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", errors.New("blank pos kind or value")
	}
	return key, value, nil
}

// assignOrchPosSegment 处理assignorchpossegment。
func assignOrchPosSegment(pos *orchPos, key, value string) error {
	switch key {
	case orchPosAgent:
		return assignUnique(&pos.AgentID, value, "next: use agent:<agent_id>")
	case orchPosDAG:
		return assignUnique(&pos.DagKey, value, "next: use dag:<dag_key>")
	case orchPosRun:
		return assignUnique(&pos.RunKey, value, "next: use run:<run_key> or dag:<dag_key>/run:<run_key>")
	case orchPosRunID:
		return assignUniqueInt64(&pos.RunID, value, "next: use dag:<dag_key>/run_id:<run_id>/node:<node_key>")
	case orchPosNode:
		return assignUnique(&pos.NodeKey, value, "next: use dag:<dag_key>/node:<node_key>")
	case orchPosWorkspace:
		return assignUnique(&pos.WorkspaceRunKey, value, "next: use workspace:<run_key>")
	default:
		return fmt.Errorf("next: unsupported pos kind %q", key)
	}
}

func assignUnique(dst *string, value, hint string) error {
	if *dst != "" {
		return fmt.Errorf("%s; duplicate kind is not allowed", hint)
	}
	*dst = value
	return nil
}

func assignUniqueInt64(dst *int64, value, hint string) error {
	if *dst != 0 {
		return fmt.Errorf("%s; duplicate kind is not allowed", hint)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fmt.Errorf("%s; run_id must be a positive integer", hint)
	}
	*dst = parsed
	return nil
}

// validateOrchPosShape 校验orchposshape。
func validateOrchPosShape(pos orchPos) error {
	nonEmpty := countOrchPosFields(pos)
	if nonEmpty == 0 {
		return errors.New("next: pass a non-empty pos")
	}
	if done, err := validateStandaloneOrchPos(pos, nonEmpty); done || err != nil {
		return err
	}
	if done, err := validateRuntimeOrchPos(pos); done || err != nil {
		return err
	}
	if pos.NodeKey != "" && pos.DagKey == "" {
		return errors.New("next: node pos must include dag:<dag_key>")
	}
	if pos.DagKey != "" {
		return nil
	}
	return errors.New("next: use agent:<agent_id>, dag:<dag_key>, run:<run_key>, workspace:<run_key>, prompt:<key>, command:<key>, or shared:<path>")
}

func countOrchPosFields(pos orchPos) int {
	nonEmpty := 0
	for _, value := range []string{
		pos.AgentID,
		pos.CommandKey,
		pos.DagKey,
		pos.NodeKey,
		pos.PromptKey,
		pos.RunKey,
		pos.SharedPath,
		pos.WorkspaceRunKey,
	} {
		if value != "" {
			nonEmpty++
		}
	}
	if pos.RunID > 0 {
		nonEmpty++
	}
	return nonEmpty
}

func validateStandaloneOrchPos(pos orchPos, nonEmpty int) (bool, error) {
	for _, rule := range []struct {
		value string
		hint  string
	}{
		{pos.AgentID, "next: agent pos must be exactly agent:<agent_id>"},
		{pos.WorkspaceRunKey, "next: workspace pos must be exactly workspace:<run_key>"},
		{runOnlyPosValue(pos), "next: run-only pos must be exactly run:<run_key>"},
	} {
		if rule.value == "" {
			continue
		}
		if nonEmpty != 1 {
			return true, errors.New(rule.hint)
		}
		return true, nil
	}
	return false, nil
}

func runOnlyPosValue(pos orchPos) string {
	if pos.RunKey != "" && pos.DagKey == "" {
		return pos.RunKey
	}
	return ""
}

func validateRuntimeOrchPos(pos orchPos) (bool, error) {
	if pos.RunID <= 0 {
		return false, nil
	}
	if pos.RunKey != "" {
		return true, errors.New("next: choose run:<run_key> or run_id:<run_id>, not both")
	}
	if pos.DagKey == "" || pos.NodeKey == "" {
		return true, errors.New("next: runtime node pos must be dag:<dag_key>/run_id:<run_id>/node:<node_key>")
	}
	return true, nil
}

func resolveAgentIDInput(agentID, pos string) (string, error) {
	return resolveLegacyFieldWithPos(agentID, "agent_id", pos, "agent:<agent_id>", func(parsed orchPos) string {
		return parsed.AgentID
	})
}

func resolveDAGKeyInput(dagKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(dagKey, "dag_key", pos, "dag:<dag_key>", func(parsed orchPos) string {
		return parsed.DagKey
	})
}

func resolveRunKeyInput(runKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(runKey, "run_key", pos, "run:<run_key> or dag:<dag_key>/run:<run_key>", func(parsed orchPos) string {
		return parsed.RunKey
	})
}

func resolveNodeKeyInput(nodeKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(nodeKey, "node_key", pos, "dag:<dag_key>/node:<node_key>", func(parsed orchPos) string {
		return parsed.NodeKey
	})
}

// resolveRunIDInput 解析运行记录IDinput。
func resolveRunIDInput(runID int64, pos string) (int64, error) {
	parsed, err := parseOrchPos(pos)
	if err != nil {
		return 0, err
	}
	if parsed.Raw == "" {
		if runID <= 0 {
			return 0, errors.New("run_id is required for runtime node status update")
		}
		return runID, nil
	}
	if parsed.RunID <= 0 {
		if runID > 0 {
			return runID, nil
		}
		return 0, invalidOrchPos(parsed.Raw, "next: use pos=dag:<dag_key>/run_id:<run_id>/node:<node_key> or pass run_id")
	}
	if runID > 0 && runID != parsed.RunID {
		return 0, mcpcommon.NewCodedToolError(
			"pos_conflict",
			fmt.Errorf("run_id %d conflicts with pos value %d", runID, parsed.RunID),
			false,
			"next: provide either pos or the legacy run_id field, or make both values match",
		)
	}
	return parsed.RunID, nil
}

func resolveWorkspaceRunKeyInput(runKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(runKey, "run_key", pos, "workspace:<run_key>", func(parsed orchPos) string {
		return parsed.WorkspaceRunKey
	})
}

func resolveSharedPathInput(path, pos string) (string, error) {
	return resolveLegacyFieldWithPos(path, "path", pos, "shared:<path>", func(parsed orchPos) string {
		return parsed.SharedPath
	})
}

func resolvePromptKeyInput(promptKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(promptKey, "prompt_key", pos, "prompt:<prompt_key>", func(parsed orchPos) string {
		return parsed.PromptKey
	})
}

func resolveCommandKeyInput(cardKey, pos string) (string, error) {
	return resolveLegacyFieldWithPos(cardKey, "card_key", pos, "command:<card_key>", func(parsed orchPos) string {
		return parsed.CommandKey
	})
}

func resolveOptionalDAGKeyInput(dagKey, pos string) (string, error) {
	trimmed := strings.TrimSpace(dagKey)
	if strings.TrimSpace(pos) == "" {
		return trimmed, nil
	}
	resolved, err := resolveDAGKeyInput(dagKey, pos)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// resolveLegacyFieldWithPos 解析带pos的legacy字段。
func resolveLegacyFieldWithPos(
	legacyValue string,
	legacyField string,
	rawPos string,
	posExample string,
	extract func(orchPos) string,
) (string, error) {
	legacy := strings.TrimSpace(legacyValue)
	parsed, err := parseOrchPos(rawPos)
	if err != nil {
		return "", err
	}
	posValue := strings.TrimSpace(extract(parsed))
	if parsed.Raw == "" {
		return requireTrimmed(legacyValue, legacyField)
	}
	if posValue == "" {
		return "", invalidOrchPos(parsed.Raw, "next: use pos="+posExample)
	}
	if legacy != "" && legacy != posValue {
		return "", mcpcommon.NewCodedToolError(
			"pos_conflict",
			fmt.Errorf("%s %q conflicts with pos value %q", legacyField, legacy, posValue),
			false,
			"next: provide either pos or the legacy field, or make both values match",
		)
	}
	return posValue, nil
}

func invalidOrchPos(raw, hint string) error {
	return mcpcommon.NewCodedToolError("invalid_pos", fmt.Errorf("invalid pos %q", raw), false, strings.TrimSpace(hint))
}
