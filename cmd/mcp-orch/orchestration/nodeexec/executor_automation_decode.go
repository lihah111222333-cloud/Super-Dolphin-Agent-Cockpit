package nodeexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func parseExecutableAutomationConfig(raw json.RawMessage) (*AutomationNodeConfig, *NodeOutcome) {
	cfg, parseErr := ParseAutomationConfig(raw)
	if parseErr != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "decode automation config: "+parseErr.Error())
		return nil, &outcome
	}
	if cfg == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "decode automation config: nil parsed config")
		return nil, &outcome
	}
	if cfg.Exec.Kind != AutomationKindCommandCard {
		outcome := failedAutomationOutcome(
			FailureClassValidation,
			fmt.Sprintf("unsupported automation.kind: %q", cfg.Exec.Kind),
		)
		return nil, &outcome
	}
	if strings.TrimSpace(cfg.Exec.CommandRef) == "" {
		outcome := failedAutomationOutcome(FailureClassValidation, "command_ref required in node.config.exec")
		return nil, &outcome
	}
	_ = cfg.Inputs
	_ = cfg.Outputs
	return cfg, nil
}

// ValidateAutomationCommandDispatchConfig 校验 automation command 节点进入执行或模板写入前的硬边界。
// 入口层必须显式给 cwd/workspace_roots，且不能声明 shell mode；历史空配置仍由旧解析路径兼容读取。
func ValidateAutomationCommandDispatchConfig(raw json.RawMessage) error {
	if err := rejectAutomationShellModeFields(raw); err != nil {
		return err
	}
	cfg, err := ParseAutomationConfig(raw)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Exec.CommandRef) == "" {
		return errors.New("automation command exec.command_ref is required")
	}
	if strings.TrimSpace(cfg.Exec.CWD) == "" {
		return errors.New("automation command exec.cwd is required")
	}
	if len(cfg.Exec.WorkspaceRoots) == 0 {
		return errors.New("automation command exec.workspace_roots is required")
	}
	if err := validateAutomationCommandEnv(cfg.Exec.Env); err != nil {
		return err
	}
	return nil
}

// rejectAutomationShellModeFields 读取原始 config，拦截 typed 解码会忽略的 shell-mode 兼容字段。
// 这些字段一旦被接受会让调用方误以为命令仍经 shell 执行，所以入口层必须直接报错。
func rejectAutomationShellModeFields(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	exec, _ := payload["exec"].(map[string]any)
	if exec == nil {
		return nil
	}
	for _, key := range []string{"shell", "shell_mode"} {
		if _, ok := exec[key]; ok {
			return fmt.Errorf("automation command exec.%s is not supported; command cards run via argv", key)
		}
	}
	if mode, ok := exec["mode"].(string); ok && strings.EqualFold(strings.TrimSpace(mode), "shell") {
		return errors.New("automation command exec.mode=shell is not supported; command cards run via argv")
	}
	return nil
}

// automationOutputsForbiddenKeys 列出 automation outputs 中禁止出现的 agent prompt 和路由字段。
// automation 节点只负责命令卡执行与输出落地，不能替下游 agent 决定 prompt、模型、provider 或工具名单。
func automationOutputsForbiddenKeys() []string {
	return []string{
		// prompt 注入字段。
		"prompt", "first_turn", "agent_prompt", "system_prompt", "append_error",
		// agent 路由字段。
		"agent_key", "model", "provider", "language", "tool_choice", "tools",
	}
}

// validateAutomationOutputs 验证 outputs 配置未包含 agent prompt 或路由字段。
// typed OutputsConfig 会忽略未知 key，因此这里必须重读 raw JSON；形状错误留给 typed 解码路径报告。
func validateAutomationOutputs(raw json.RawMessage, _ *AutomationNodeConfig) *NodeOutcome {
	return validateOutputsForbiddenKeys(raw, automationOutputsForbiddenKeys(), func(key string) NodeOutcome {
		return failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("automation outputs cannot include agent-prompt or agent-routing field %q", key))
	})
}

// validateOutputsForbiddenKeys 在 raw outputs 对象里查找禁止字段。
// 该函数只做语义字段拦截；outputs 不是 object 时交给 typed schema 的验证路径处理。
func validateOutputsForbiddenKeys(
	raw json.RawMessage,
	forbiddenKeys []string,
	buildOutcome func(string) NodeOutcome,
) *NodeOutcome {
	if len(raw) == 0 {
		return nil
	}
	var envelope struct {
		Outputs json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Outputs) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Outputs, &fields); err != nil {
		// outputs 不是 object（例如 null 或数组）时交给 typed 解码路径报错，这里不重复报告。
		return nil
	}
	for _, key := range forbiddenKeys {
		if _, ok := fields[key]; ok {
			outcome := buildOutcome(key)
			return &outcome
		}
	}
	return nil
}
