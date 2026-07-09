package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// renderCommandTemplate 渲染命令template。
func renderCommandTemplate(commandTemplate string, args json.RawMessage) (string, json.RawMessage, error) {
	if strings.TrimSpace(commandTemplate) == "" {
		return "", nil, errors.New("command_template is required")
	}
	if err := validateCommandTemplateActionsQuoted(commandTemplate); err != nil {
		return "", nil, err
	}
	data := map[string]any{}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, &data); err != nil {
		return "", nil, fmt.Errorf("parse command args: %w", err)
	}
	normalizedArgs, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("marshal command args: %w", err)
	}
	tpl, err := template.New("command_card").Option("missingkey=error").Parse(commandTemplate)
	if err != nil {
		return "", nil, fmt.Errorf("parse command template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", nil, fmt.Errorf("render command template: %w", err)
	}
	command := strings.TrimSpace(rendered.String())
	if command == "" {
		return "", nil, errors.New("rendered command is empty")
	}
	return command, normalizedArgs, nil
}

var unsafeRenderedShellTokens = []string{
	"\x00", "\r", "\n", "$(", "$", "`", "&&", "||", ";", "|", "&", ">", "<", "*", "?", "[", "]",
}

func validateRenderedCommandShellSafety(command string) error {
	for _, token := range unsafeRenderedShellTokens {
		if strings.Contains(command, token) {
			return fmt.Errorf(
				"unsafe shell metacharacter %q in rendered command; automation command cards run via argv and do not support shell expansion",
				token,
			)
		}
	}
	return nil
}

// validateCommandTemplateActionsQuoted 要求模板动作放在引号内。
// 未引号包裹的动态值可能被空白拆成多个 argv，因此在渲染前阻断。
func validateCommandTemplateActionsQuoted(commandTemplate string) error {
	state := commandTemplateQuoteState{}
	for i := 0; i < len(commandTemplate); i++ {
		if state.accept(commandTemplate[i]) {
			continue
		}
		if startsTemplateAction(commandTemplate, i) && !state.inQuote() {
			return errors.New("shell argv command template actions must be quoted to prevent whitespace splitting")
		}
	}
	return nil
}

type commandTemplateQuoteState struct {
	inSingle, inDouble, escaped bool
}

// accept 消费一个模板字符并维护引号状态。
// 返回 true 表示该字符已经作为引号/转义控制字符处理，调用方不再当作模板动作检查。
func (s *commandTemplateQuoteState) accept(ch byte) bool {
	if s.escaped {
		s.escaped = false
		return true
	}
	switch {
	case ch == '\\' && !s.inSingle:
		s.escaped = true
	case ch == '\'' && !s.inDouble:
		s.inSingle = !s.inSingle
	case ch == '"' && !s.inSingle:
		s.inDouble = !s.inDouble
	default:
		return false
	}
	return true
}

func (s commandTemplateQuoteState) inQuote() bool {
	return s.inSingle || s.inDouble
}

func startsTemplateAction(commandTemplate string, idx int) bool {
	return commandTemplate[idx] == '{' && idx+1 < len(commandTemplate) && commandTemplate[idx+1] == '{'
}

// buildAutomationRunArgs 把 cfg.Inputs 声明的上游结果和 sharedfile 内容合并到 args.__inputs。
// 原 cfg.Exec.Args 不会被修改；如果用户参数已占用 "__inputs"，直接 validation，避免隐式覆盖。
// 缺上游结果或缺 reader 是配置/wiring 错误；实际读取失败按底层错误分类。
func buildAutomationRunArgs(ctx context.Context, cfg *AutomationNodeConfig, runCtx RunContext) (json.RawMessage, *NodeOutcome) {
	in := cfg.Inputs
	if len(in.FromNodes) == 0 && len(in.FromSharedfiles) == 0 {
		return cfg.Exec.Args, nil
	}
	argsMap, failure := decodeArgsForInjection(cfg.Exec.Args)
	if failure != nil {
		return nil, failure
	}
	injected, failure := buildInputsPayload(ctx, in, runCtx)
	if failure != nil {
		return nil, failure
	}
	argsMap["__inputs"] = injected
	merged, err := json.Marshal(argsMap)
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal merged command args: "+err.Error())
		return nil, &outcome
	}
	return merged, nil
}

// decodeArgsForInjection 将 cfg.Exec.Args 解码为可注入的 map。
// "__inputs" 是执行器保留 key；用户原始 args 占用该 key 时必须失败，避免上下文被覆盖。
func decodeArgsForInjection(raw json.RawMessage) (map[string]any, *NodeOutcome) {
	argsMap := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &argsMap); err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation, "decode command args: "+err.Error())
			return nil, &outcome
		}
	}
	if _, conflict := argsMap["__inputs"]; conflict {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"command args already define reserved key \"__inputs\"; remove it before injecting Inputs config")
		return nil, &outcome
	}
	return argsMap, nil
}

// buildInputsPayload 生成最终写入 args.__inputs 的上下文对象。
// 只有配置声明的来源会被读取，避免 executor 主动扩大 store/sharedfile 访问面。
func buildInputsPayload(ctx context.Context, in InputsConfig, runCtx RunContext) (map[string]any, *NodeOutcome) {
	injected := map[string]any{}
	fromNodes, failure := collectPrevResults(in.FromNodes, runCtx.PrevResults)
	if failure != nil {
		return nil, failure
	}
	if len(fromNodes) > 0 {
		injected["from_nodes"] = fromNodes
	}
	fromShared, failure := collectSharedfileInputs(ctx, in.FromSharedfiles, runCtx.SharedFileReader)
	if failure != nil {
		return nil, failure
	}
	if len(fromShared) > 0 {
		injected["from_sharedfiles"] = fromShared
	}
	return injected, nil
}

// collectPrevResults 收集上游节点 result 并解码为 template 可访问的值。
// 缺少声明的 node_key 是 validation 失败；非 JSON result 保留为原始字符串，保证命令模板仍能读取。
func collectPrevResults(fromNodes []string, prev map[string]json.RawMessage) (map[string]any, *NodeOutcome) {
	if len(fromNodes) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(fromNodes))
	for _, key := range fromNodes {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		raw, ok := prev[k]
		if !ok {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("inputs.from_nodes: missing prev result for node_key %q", k))
			return nil, &outcome
		}
		var decoded any
		if len(raw) == 0 || string(raw) == "null" {
			decoded = nil
		} else if err := json.Unmarshal(raw, &decoded); err != nil {
			// 不可解析作 JSON 时退为原始字符串，让 command_template 仍能拿到内容。
			decoded = string(raw)
		}
		out[k] = decoded
	}
	return out, nil
}

// collectSharedfileInputs 读取 inputs.from_sharedfiles 声明的文件内容。
// reader 未注入或路径不存在是 validation 失败；读取端口返回错误时按 automation 错误分类。
func collectSharedfileInputs(ctx context.Context, paths []string, reader SharedFileReader) (map[string]string, *NodeOutcome) {
	if len(paths) == 0 {
		return nil, nil
	}
	if reader == nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"inputs.from_sharedfiles configured but SharedFileReader not wired in RunContext")
		return nil, &outcome
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		path := strings.TrimSpace(p)
		if path == "" {
			continue
		}
		content, exists, err := reader.ReadSharedFile(ctx, path)
		if err != nil {
			outcome := failedAutomationOutcome(classifyAutomationError(err),
				fmt.Sprintf("inputs.from_sharedfiles[%q]: %v", path, err))
			return nil, &outcome
		}
		if !exists {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("inputs.from_sharedfiles references unknown path %q", path))
			return nil, &outcome
		}
		out[path] = content
	}
	return out, nil
}
