package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"text/template"
)

// AutomationExecutor wires node_type=automation through command_get and a
// command-card runner. F2.2 will consume decoded Inputs/Outputs; this task only
// resolves and runs command_ref, then returns a compact NodeOutcome.Result.
type AutomationExecutor struct {
	commandGetter AutomationCommandGetter
	runner        AutomationCommandRunner
}

type AutomationCommandGetter interface {
	GetCommandCard(ctx context.Context, cardKey string) (AutomationCommandCard, error)
}

type AutomationCommandRunner interface {
	RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error)
}

type AutomationCommandCard struct {
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title,omitempty"`
	Description     string          `json:"description,omitempty"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel       string          `json:"risk_level,omitempty"`
	Enabled         bool            `json:"enabled"`
}

type AutomationCommandResult struct {
	CardKey  string          `json:"card_key"`
	ExitCode int             `json:"exit_code"`
	Stdout   string          `json:"stdout,omitempty"`
	Stderr   string          `json:"stderr,omitempty"`
	Command  string          `json:"command,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
}

type ShellCommandRunner struct{}

func NewShellCommandRunner() *ShellCommandRunner { return &ShellCommandRunner{} }

func (ShellCommandRunner) RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	command, normalizedArgs, err := renderCommandTemplate(card.CommandTemplate, args)
	if err != nil {
		return AutomationCommandResult{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	result := AutomationCommandResult{
		CardKey:  card.CardKey,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Command:  command,
		Args:     normalizedArgs,
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, CommandExitError{ExitCode: result.ExitCode, Err: err}
	}
	return result, nil
}

type CommandExitError struct {
	ExitCode int
	Err      error
}

func (e CommandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
}

func (e CommandExitError) Unwrap() error { return e.Err }

func NewAutomationExecutor(getter AutomationCommandGetter, runner AutomationCommandRunner) *AutomationExecutor {
	return &AutomationExecutor{commandGetter: getter, runner: runner}
}

func (e *AutomationExecutor) Execute(ctx context.Context, node Node, _ RunContext) (NodeOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, failure := parseExecutableAutomationConfig(node.Config)
	if failure != nil {
		return *failure, nil
	}
	if failure := e.validateWiring(); failure != nil {
		return *failure, nil
	}

	card, failure := e.loadCommandCard(ctx, cfg)
	if failure != nil {
		return *failure, nil
	}
	result, err := e.runner.RunCommandCard(ctx, card, cfg.Exec.Args)
	if err != nil {
		return failedAutomationOutcome(classifyAutomationError(err), "run command card: "+err.Error()), nil
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return failedAutomationOutcome(FailureClassValidation, "marshal automation result: "+err.Error()), nil
	}
	return NodeOutcome{Status: NodeStatusDone, Result: payload}, nil
}

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
		outcome := failedAutomationOutcome(FailureClassValidation, fmt.Sprintf("unsupported automation.kind: %q", cfg.Exec.Kind))
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

func (e *AutomationExecutor) validateWiring() *NodeOutcome {
	if e == nil || e.commandGetter == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "automation executor: command_get client not wired")
		return &outcome
	}
	if e.runner == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "automation executor: command runner not wired")
		return &outcome
	}
	return nil
}

func (e *AutomationExecutor) loadCommandCard(ctx context.Context, cfg *AutomationNodeConfig) (AutomationCommandCard, *NodeOutcome) {
	commandRef := strings.TrimSpace(cfg.Exec.CommandRef)
	card, err := e.commandGetter.GetCommandCard(ctx, commandRef)
	if err != nil {
		outcome := failedAutomationOutcome(classifyAutomationError(err), "command_get: "+err.Error())
		return AutomationCommandCard{}, &outcome
	}
	if !card.Enabled {
		outcome := failedAutomationOutcome(FailureClassHard, fmt.Sprintf("command card %q is disabled", commandRef))
		return AutomationCommandCard{}, &outcome
	}
	return card, nil
}

func (e *AutomationExecutor) Hooks() map[HookPoint]HookHandler { return nil }

func failedAutomationOutcome(class FailureClass, summary string) NodeOutcome {
	return NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: class,
		ErrorSummary: truncateErrSummary(summary),
	}
}

func classifyAutomationError(err error) FailureClass {
	if err == nil {
		return FailureClassTransient
	}
	var exitErr CommandExitError
	if errors.As(err, &exitErr) {
		return FailureClassHard
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return FailureClassTransient
	}
	msg := strings.ToLower(err.Error())
	switch {
	case containsAny(msg, automationValidationKeywords):
		return FailureClassValidation
	case containsAny(msg, automationNotFoundKeywords):
		return FailureClassHard
	case containsAny(msg, automationTransientKeywords):
		return FailureClassTransient
	case containsAny(msg, automationInfrastructureKeywords):
		return FailureClassInfrastructure
	default:
		return FailureClassHard
	}
}

var (
	automationValidationKeywords = []string{
		"parse", "decode", "unmarshal", "marshal", "json", "template", "required", "missing key",
	}
	automationNotFoundKeywords  = []string{"not found", "no such command", "unknown command"}
	automationTransientKeywords = []string{
		"deadline exceeded", "timeout", "timed out", "i/o timeout", "connection refused", "connection reset",
		"temporary", "temporarily", "rate limit", "rate-limit", "rate_limit", "too many requests", "http 429", "status 429",
	}
	automationInfrastructureKeywords = []string{
		"database", "postgres", "pgx", "sql", "transport unavailable", "service unavailable", "bad gateway", "gateway timeout", "http 500", "http 502", "http 503", "http 504",
	}
)

func containsAny(msg string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

func renderCommandTemplate(commandTemplate string, args json.RawMessage) (string, json.RawMessage, error) {
	if strings.TrimSpace(commandTemplate) == "" {
		return "", nil, errors.New("command_template is required")
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
