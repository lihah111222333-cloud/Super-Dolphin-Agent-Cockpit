package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const remoteCIAgentTokenBootstrapSchemaVersion uint32 = 1

const retiredRequesterFingerprintEnvironment = "SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT"

type remoteCIAgentTokenBootstrap struct {
	SchemaVersion    uint32   `json:"schema_version"`
	Kind             string   `json:"kind"`
	AgentToken       string   `json:"agent_token"`
	AgentTokenDigest string   `json:"agent_token_digest"`
	Issued           bool     `json:"issued"`
	RetryRequired    bool     `json:"retry_required"`
	ExecuteCI        bool     `json:"execute_ci"`
	Guidance         string   `json:"guidance"`
	ReuseEnvName     string   `json:"reuse_environment_name"`
	ReuseEnvValue    string   `json:"reuse_environment_value"`
	RetryArgv        []string `json:"retry_argv"`
}

type remoteCIAgentTokenGuidance struct {
	SchemaVersion uint32   `json:"schema_version"`
	Kind          string   `json:"kind"`
	RetryRequired bool     `json:"retry_required"`
	ExecuteCI     bool     `json:"execute_ci"`
	Guidance      string   `json:"guidance"`
	IssueFlag     string   `json:"issue_flag"`
	IssueEnvName  string   `json:"issue_environment_name"`
	IssueEnvValue string   `json:"issue_environment_value"`
	UseFlag       string   `json:"use_flag"`
	UseEnvName    string   `json:"use_environment_name"`
	UseEnvValue   string   `json:"use_environment_value"`
	IssueArgv     []string `json:"issue_argv"`
}

// requireRemoteCIAgentToken 在远程 CI 前执行三阶段 token 握手并 fail-closed：
// 无 token 只返回申请指引，issue 只签发 token，实际 token 才允许进入 CI。
func requireRemoteCIAgentToken(
	command []string,
	args []string,
	stdout io.Writer,
) error {
	_, err := requireRemoteCIAgentTokenDigest(command, args, stdout)
	return err
}

// requireRemoteCIAgentTokenDigest 在握手成功后返回已验证的摘要，供 hook 直接传递给解析器。
func requireRemoteCIAgentTokenDigest(
	command []string,
	args []string,
	stdout io.Writer,
) (string, error) {
	if err := validateRetiredRequesterFingerprint(args); err != nil {
		return "", err
	}
	remoteHook := isRemoteHookCommand(command)
	explicit, explicitPresent, err := remoteCIAgentTokenArgument(args)
	if err != nil {
		return "", protocolError("parse CI agent token flag: %v", err)
	}
	inherited, inheritedPresent := os.LookupEnv(cicontract.AgentTokenEnvironment)
	if err := validateRemoteHookTokenBoundary(remoteHook, args, inherited, inheritedPresent); err != nil {
		return "", err
	}
	if err := validateRemoteCIAgentTokenInputs(explicit, explicitPresent, inherited, inheritedPresent); err != nil {
		return "", err
	}
	phase, err := cicontract.ClassifyAgentTokenRequest(explicit, inherited)
	if err != nil {
		return "", protocolError("classify CI agent token request: %v", err)
	}
	if err := executeRemoteCIAgentTokenPhase(command, args, stdout, phase, explicitPresent, inheritedPresent); err != nil {
		return "", err
	}
	token := explicit
	if inheritedPresent {
		token = inherited
	}
	parsed, err := cicontract.ParseAgentToken(token)
	if err != nil {
		return "", protocolError("invalid CI agent token: %v", err)
	}
	digest, err := cicontract.AgentTokenDigest(parsed)
	if err != nil {
		return "", protocolError("digest CI agent token: %v", err)
	}
	return digest, nil
}

func validateRemoteCIAgentTokenInputs(explicit string, explicitPresent bool, inherited string, inheritedPresent bool) error {
	if explicitPresent && explicit == "" {
		return protocolError("%s requires a non-empty value", cicontract.AgentTokenFlag)
	}
	if inheritedPresent && inherited == "" {
		return protocolError("%s requires a non-empty value", cicontract.AgentTokenEnvironment)
	}
	return nil
}

// validateRetiredRequesterFingerprint 拒绝已经退役的请求者身份入口。
func validateRetiredRequesterFingerprint(args []string) error {
	if hasRetiredRequesterFingerprintArgument(args) {
		return protocolError("--requester-fingerprint is retired; use --agent-token")
	}
	if _, present := os.LookupEnv(retiredRequesterFingerprintEnvironment); present {
		return protocolError("%s is retired; use %s", retiredRequesterFingerprintEnvironment, cicontract.AgentTokenEnvironment)
	}
	return nil
}

// validateRemoteHookTokenBoundary 强制 hook 只继承调用方实际 token。
func validateRemoteHookTokenBoundary(remoteHook bool, args []string, inherited string, inheritedPresent bool) error {
	if !remoteHook {
		return nil
	}
	if hasRemoteCIAgentTokenArgument(args) {
		return protocolError("remote hook must inherit %s; --agent-token is not allowed", cicontract.AgentTokenEnvironment)
	}
	if inheritedPresent {
		if err := cicontract.ValidateGitHookAgentToken(inherited); err != nil {
			return protocolError("remote hook token boundary: issue is not allowed: %v", err)
		}
	}
	return nil
}

// executeRemoteCIAgentTokenPhase 分派无 token、issue 与实际 token 三个握手阶段。
func executeRemoteCIAgentTokenPhase(
	command []string,
	args []string,
	stdout io.Writer,
	phase cicontract.AgentTokenPhase,
	explicitPresent bool,
	inheritedPresent bool,
) error {
	switch phase {
	case cicontract.AgentTokenPhaseApplication:
		return writeRemoteCIAgentTokenGuidance(command, args, stdout)
	case cicontract.AgentTokenPhaseIssued:
		return issueRemoteCIAgentToken(command, args, stdout, explicitPresent, inheritedPresent)
	case cicontract.AgentTokenPhaseAuthenticated:
		return nil
	default:
		return protocolError("unknown CI agent token phase %q", phase)
	}
}

// writeRemoteCIAgentTokenGuidance 返回不执行 CI 的阶段一结构化指引。
func writeRemoteCIAgentTokenGuidance(command []string, args []string, stdout io.Writer) error {
	application := cicontract.AgentTokenApplicationResponse()
	issueEnvName, issueEnvValue, err := splitAgentTokenAssignment(application.Guidance.IssueEnvironment)
	if err != nil {
		return protocolError("canonical agent token guidance: %v", err)
	}
	if issueEnvValue != cicontract.AgentTokenIssueValue {
		return protocolError("canonical agent token guidance issue value = %q, want %q", issueEnvValue, cicontract.AgentTokenIssueValue)
	}
	result := remoteCIAgentTokenGuidance{
		SchemaVersion: remoteCIAgentTokenBootstrapSchemaVersion,
		Kind:          "remote_ci_agent_token_guidance",
		RetryRequired: true,
		ExecuteCI:     false,
		Guidance:      "request a remote CI agent token with the provided issue flag or issue environment; this request did not run CI",
		IssueFlag:     application.Guidance.IssueArgument,
		IssueEnvName:  issueEnvName,
		IssueEnvValue: issueEnvValue,
		UseFlag:       application.Guidance.ReuseFlag + "=<token>",
		UseEnvName:    application.Guidance.ReuseEnvironment,
		UseEnvValue:   "<token>",
		IssueArgv:     remoteCIAgentTokenIssueArgv(command, args),
	}
	if err := validateRemoteCIAgentTokenGuidanceWire(result); err != nil {
		return protocolError("canonical agent token guidance wire: %v", err)
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return infrastructureError("write remote CI agent token guidance: %v", err)
	}
	return protocolError("remote CI agent token issuance required; retry required with --agent-token=issue")
}

// issueRemoteCIAgentToken 签发阶段二 token，且不读取配置或执行 CI。
func issueRemoteCIAgentToken(command []string, args []string, stdout io.Writer, explicitPresent bool, inheritedPresent bool) error {
	if inheritedPresent {
		if err := os.Unsetenv(cicontract.AgentTokenEnvironment); err != nil {
			return infrastructureError("clear issued CI agent token environment: %v", err)
		}
	}
	bootstrap, err := cicontract.IssueAgentTokenBootstrap()
	if err != nil {
		return infrastructureError("issue remote CI agent token bootstrap: %v", err)
	}
	token := bootstrap.AgentToken
	if bootstrap.Phase != cicontract.AgentTokenPhaseIssued || bootstrap.ExecuteCI {
		return protocolError("canonical agent token bootstrap has invalid phase %q or execute_ci=%t", bootstrap.Phase, bootstrap.ExecuteCI)
	}
	retryArgv := append([]string{os.Args[0]}, command...)
	if explicitPresent {
		retryArgv = append(retryArgv, withoutRemoteCIAgentTokenArgument(args)...)
	} else {
		retryArgv = append(retryArgv, args...)
	}
	retryArgv = append(retryArgv, cicontract.AgentTokenFlag, token)
	result := remoteCIAgentTokenBootstrap{
		SchemaVersion:    remoteCIAgentTokenBootstrapSchemaVersion,
		Kind:             "remote_ci_agent_token_bootstrap",
		AgentToken:       token,
		AgentTokenDigest: bootstrap.AgentTokenDigest,
		Issued:           true,
		RetryRequired:    true,
		ExecuteCI:        false,
		Guidance:         "reuse the issued token with the provided environment fields or retry_argv; this bootstrap did not run CI",
		ReuseEnvName:     cicontract.AgentTokenEnvironment,
		ReuseEnvValue:    token,
		RetryArgv:        retryArgv,
	}
	if err := validateRemoteCIAgentTokenBootstrapWire(result); err != nil {
		return protocolError("canonical agent token bootstrap wire: %v", err)
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return infrastructureError("write remote CI agent token bootstrap: %v", err)
	}
	return protocolError("remote CI agent token bootstrap issued; retry required")
}

// splitAgentTokenAssignment 将 cicontract 的 canonical NAME=value 指引映射为 CLI wire 的 name/value 字段。
func splitAgentTokenAssignment(assignment string) (string, string, error) {
	name, value, ok := strings.Cut(assignment, "=")
	if !ok || name == "" || value == "" || strings.Contains(value, "=") {
		return "", "", errors.New("canonical agent token guidance assignment is malformed")
	}
	return name, value, nil
}

// validateRemoteCIAgentTokenGuidanceWire 检查 CLI 专属 wire adapter 的关键映射不为空。
func validateRemoteCIAgentTokenGuidanceWire(result remoteCIAgentTokenGuidance) error {
	if result.SchemaVersion == 0 {
		return errors.New("guidance wire schema_version is required")
	}
	if result.Kind == "" || !result.RetryRequired || result.ExecuteCI {
		return errors.New("guidance wire identity or phase is invalid")
	}
	if err := validateAgentTokenWireStrings(result.Guidance, result.IssueFlag, result.IssueEnvName, result.IssueEnvValue, result.UseFlag, result.UseEnvName, result.UseEnvValue); err != nil {
		return fmt.Errorf("guidance wire: %w", err)
	}
	if len(result.IssueArgv) == 0 {
		return errors.New("guidance wire issue_argv is required")
	}
	return nil
}

// validateRemoteCIAgentTokenBootstrapWire 检查 CLI 专属 bootstrap wire 的关键映射不为空。
func validateRemoteCIAgentTokenBootstrapWire(result remoteCIAgentTokenBootstrap) error {
	if result.SchemaVersion == 0 {
		return errors.New("bootstrap wire schema_version is required")
	}
	if result.Kind == "" || !result.Issued || !result.RetryRequired || result.ExecuteCI {
		return errors.New("bootstrap wire identity or phase is invalid")
	}
	if err := validateAgentTokenWireStrings(result.AgentToken, result.AgentTokenDigest, result.Guidance, result.ReuseEnvName, result.ReuseEnvValue); err != nil {
		return fmt.Errorf("bootstrap wire: %w", err)
	}
	if len(result.RetryArgv) == 0 {
		return errors.New("bootstrap wire retry_argv is required")
	}
	if err := cicontract.ValidateAgentTokenDigest(result.AgentTokenDigest); err != nil {
		return err
	}
	return cicontract.ValidateAgentTokenContinuation(result.AgentToken, result.AgentTokenDigest)
}

func validateAgentTokenWireStrings(fields ...string) error {
	if slices.Contains(fields, "") {
		return errors.New("required field is empty")
	}
	return nil
}

// isRemoteHookCommand 判断调用是否处于无状态 Git hook 边界。
func isRemoteHookCommand(command []string) bool {
	return len(command) == 3 && command[0] == "remote" && command[1] == "hook"
}

// hasRemoteCIAgentTokenArgument 识别任意 agent-token flag，供 hook 严格拒绝。
func hasRemoteCIAgentTokenArgument(args []string) bool {
	for _, arg := range args {
		if arg == cicontract.AgentTokenFlag || strings.HasPrefix(arg, cicontract.AgentTokenFlag+"=") {
			return true
		}
	}
	return false
}

// remoteCIAgentTokenIssueArgv 为 hook 指向 hook 外的显式签发入口。
func remoteCIAgentTokenIssueArgv(command []string, args []string) []string {
	if isRemoteHookCommand(command) {
		return []string{os.Args[0], "remote", "run", cicontract.AgentTokenFlag + "=" + cicontract.AgentTokenIssueValue}
	}
	issueArgv := append([]string{os.Args[0]}, command...)
	issueArgv = append(issueArgv, args...)
	return append(issueArgv, cicontract.AgentTokenFlag+"="+cicontract.AgentTokenIssueValue)
}

func hasRetiredRequesterFingerprintArgument(args []string) bool {
	for _, arg := range args {
		if arg == "--requester-fingerprint" || (len(arg) > len("--requester-fingerprint=") && arg[:len("--requester-fingerprint=")] == "--requester-fingerprint=") {
			return true
		}
	}
	return false
}

// remoteCIAgentTokenArgument 解析唯一的显式 agent-token flag。
func remoteCIAgentTokenArgument(args []string) (string, bool, error) {
	var value string
	found := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == cicontract.AgentTokenFlag {
			if index+1 == len(args) {
				return "", false, errors.New("--agent-token requires a value")
			}
			index++
			arg = args[index]
		} else if value, present := strings.CutPrefix(arg, cicontract.AgentTokenFlag+"="); present {
			arg = value
		} else {
			continue
		}
		if found {
			return "", false, errors.New("--agent-token must be supplied once")
		}
		value, found = arg, true
	}
	return value, found, nil
}

func withoutRemoteCIAgentTokenArgument(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == cicontract.AgentTokenFlag {
			index++
			continue
		}
		if strings.HasPrefix(arg, cicontract.AgentTokenFlag+"=") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

// resolveRemoteCIAgentToken 合并唯一 flag 与唯一环境变量，拒绝冲突和非规范输入。
func resolveRemoteCIAgentToken(explicit string) (string, error) {
	if _, present := os.LookupEnv(retiredRequesterFingerprintEnvironment); present {
		return "", fmt.Errorf("%s is retired", retiredRequesterFingerprintEnvironment)
	}
	inherited, inheritedPresent := os.LookupEnv(cicontract.AgentTokenEnvironment)
	if inheritedPresent {
		if err := clearInheritedRemoteCIAgentToken(); err != nil {
			return "", err
		}
	}
	value, err := selectRemoteCIAgentTokenInput(explicit, inherited, inheritedPresent)
	if err != nil {
		return "", err
	}
	phase, err := cicontract.ClassifyAgentTokenRequest(explicit, inherited)
	if err != nil {
		return "", err
	}
	if phase != cicontract.AgentTokenPhaseAuthenticated {
		return "", fmt.Errorf("CI agent token phase %q cannot continue remote run", phase)
	}
	token, err := cicontract.ParseAgentToken(value)
	if err != nil {
		return "", fmt.Errorf("invalid CI agent token: %w", err)
	}
	return token, nil
}

func clearInheritedRemoteCIAgentToken() error {
	if err := os.Unsetenv(cicontract.AgentTokenEnvironment); err != nil {
		return fmt.Errorf("clear inherited CI agent token environment: %w", err)
	}
	return nil
}

func selectRemoteCIAgentTokenInput(explicit, inherited string, inheritedPresent bool) (string, error) {
	if explicit == "" {
		if !inheritedPresent {
			return "", nil
		}
		if inherited == "" {
			return "", errors.New("CI agent token environment requires a non-empty value")
		}
		return inherited, nil
	}
	if inheritedPresent {
		return "", errors.New("CI agent token must be supplied by exactly one source")
	}
	return explicit, nil
}
