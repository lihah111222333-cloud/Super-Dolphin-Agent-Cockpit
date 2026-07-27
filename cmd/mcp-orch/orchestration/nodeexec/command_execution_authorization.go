package nodeexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrCommandExecutionAuthorizerUnavailable = errors.New("automation command execution authorizer is unavailable")
	ErrCommandExecutionPrincipalMissing      = errors.New("automation command execution authorization has no trusted principal")
	ErrCommandExecutionDenied                = errors.New("automation command execution is denied")
)

// CommandExecutionAuthorizationRequest 只描述待执行对象，不携带 permission/grant。
// 授权方必须从可信 context 或外部 resolver 解析 principal 与当前 grants，禁止请求参数自证权限。
type CommandExecutionAuthorizationRequest struct {
	CardKey    string
	CWD        string
	Executable string
}

// CommandExecutionAuthorization 是可信授权方返回的实时判定。
type CommandExecutionAuthorization struct {
	Subject string
	Allowed bool
}

// CommandExecutionAuthorizer 在真实进程 spawn 前解析可信主体并判定 command.execute。
type CommandExecutionAuthorizer interface {
	AuthorizeCommandExecution(context.Context, CommandExecutionAuthorizationRequest) (CommandExecutionAuthorization, error)
}

// ShellCommandRunnerOption 配置命令执行边界的可信依赖。
type ShellCommandRunnerOption func(*ShellCommandRunner)

// WithCommandExecutionAuthorizer 注入 command.execute 授权方。
func WithCommandExecutionAuthorizer(authorizer CommandExecutionAuthorizer) ShellCommandRunnerOption {
	return func(runner *ShellCommandRunner) {
		runner.authorizer = authorizer
	}
}

// authorizeCommandExecution 在进程启动前 fail-closed；缺授权源、主体或 deny 均阻断。
func (r *ShellCommandRunner) authorizeCommandExecution(ctx context.Context, card AutomationCommandCard, prepared preparedAutomationCommand) error {
	if r == nil || r.authorizer == nil {
		return ErrCommandExecutionAuthorizerUnavailable
	}
	decision, err := r.authorizer.AuthorizeCommandExecution(ctx, CommandExecutionAuthorizationRequest{
		CardKey:    strings.TrimSpace(card.CardKey),
		CWD:        prepared.cmd.Dir,
		Executable: prepared.cmd.Path,
	})
	if err != nil {
		return fmt.Errorf("authorize automation command execution: %w", err)
	}
	subject := strings.TrimSpace(decision.Subject)
	if subject == "" {
		return ErrCommandExecutionPrincipalMissing
	}
	if !decision.Allowed {
		return fmt.Errorf("%w for subject %q", ErrCommandExecutionDenied, subject)
	}
	return nil
}
