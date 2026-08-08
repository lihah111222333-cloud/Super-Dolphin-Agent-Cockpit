package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	if explicitPresent && inheritedPresent {
		_ = os.Unsetenv(cicontract.AgentTokenEnvironment)
		return "", protocolError("CI agent token must be supplied by exactly one source")
	}
	if err := executeRemoteCIAgentTokenPhase(command, args, stdout, explicit, explicitPresent, inherited, inheritedPresent); err != nil {
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
	if inheritedPresent && inherited == cicontract.AgentTokenIssueValue {
		return protocolError("remote hook must inherit an actual %s; issue is not allowed", cicontract.AgentTokenEnvironment)
	}
	return nil
}

// executeRemoteCIAgentTokenPhase 分派无 token、issue 与实际 token 三个握手阶段。
func executeRemoteCIAgentTokenPhase(
	command []string,
	args []string,
	stdout io.Writer,
	explicit string,
	explicitPresent bool,
	inherited string,
	inheritedPresent bool,
) error {
	if !explicitPresent && !inheritedPresent {
		return writeRemoteCIAgentTokenGuidance(command, args, stdout)
	}
	issueRequested := explicitPresent && explicit == cicontract.AgentTokenIssueValue || inheritedPresent && inherited == cicontract.AgentTokenIssueValue
	if !issueRequested {
		return nil
	}
	return issueRemoteCIAgentToken(command, args, stdout, explicitPresent, inheritedPresent)
}

// writeRemoteCIAgentTokenGuidance 返回不执行 CI 的阶段一结构化指引。
func writeRemoteCIAgentTokenGuidance(command []string, args []string, stdout io.Writer) error {
	result := remoteCIAgentTokenGuidance{
		SchemaVersion: remoteCIAgentTokenBootstrapSchemaVersion,
		Kind:          "remote_ci_agent_token_guidance",
		RetryRequired: true,
		ExecuteCI:     false,
		Guidance:      "request a remote CI agent token with the provided issue flag or issue environment; this request did not run CI",
		IssueFlag:     cicontract.AgentTokenFlag + "=" + cicontract.AgentTokenIssueValue,
		IssueEnvName:  cicontract.AgentTokenEnvironment,
		IssueEnvValue: cicontract.AgentTokenIssueValue,
		UseFlag:       cicontract.AgentTokenFlag + "=<token>",
		UseEnvName:    cicontract.AgentTokenEnvironment,
		UseEnvValue:   "<token>",
		IssueArgv:     remoteCIAgentTokenIssueArgv(command, args),
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return infrastructureError("write remote CI agent token guidance: %v", err)
	}
	return protocolError("remote CI agent token issuance required; retry required with --agent-token=issue")
}

// issueRemoteCIAgentToken 签发阶段二 token，且不读取配置或执行 CI。
func issueRemoteCIAgentToken(command []string, args []string, stdout io.Writer, explicitPresent bool, inheritedPresent bool) error {
	if inheritedPresent {
		_ = os.Unsetenv(cicontract.AgentTokenEnvironment)
	}
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		return infrastructureError("generate remote CI agent token: %v", err)
	}
	retryArgv := append([]string{os.Args[0]}, command...)
	if explicitPresent {
		retryArgv = append(retryArgv, withoutRemoteCIAgentTokenArgument(args)...)
	} else {
		retryArgv = append(retryArgv, args...)
	}
	retryArgv = append(retryArgv, cicontract.AgentTokenFlag, token)
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		return infrastructureError("digest generated remote CI agent token: %v", err)
	}
	result := remoteCIAgentTokenBootstrap{
		SchemaVersion:    remoteCIAgentTokenBootstrapSchemaVersion,
		Kind:             "remote_ci_agent_token_bootstrap",
		AgentToken:       token,
		AgentTokenDigest: digest,
		Issued:           true,
		RetryRequired:    true,
		ExecuteCI:        false,
		Guidance:         "reuse the issued token with the provided environment fields or retry_argv; this bootstrap did not run CI",
		ReuseEnvName:     cicontract.AgentTokenEnvironment,
		ReuseEnvValue:    token,
		RetryArgv:        retryArgv,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return infrastructureError("write remote CI agent token bootstrap: %v", err)
	}
	return protocolError("remote CI agent token bootstrap issued; retry required")
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
		defer os.Unsetenv(cicontract.AgentTokenEnvironment)
	}
	if explicit != "" && inheritedPresent {
		return "", errors.New("CI agent token must be supplied by exactly one source")
	}
	value := explicit
	if inheritedPresent {
		value = inherited
	}
	if value == "" {
		return "", nil
	}
	token, err := cicontract.ParseAgentToken(value)
	if err != nil {
		return "", fmt.Errorf("invalid CI agent token: %w", err)
	}
	return token, nil
}
