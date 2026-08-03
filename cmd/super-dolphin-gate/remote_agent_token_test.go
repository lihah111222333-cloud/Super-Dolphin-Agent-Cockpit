package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRunRemoteInvocationWithoutTokenBootstrapsAndDoesNotRunCI(t *testing.T) {
	var output bytes.Buffer
	err := runRemoteInvocation([]string{"--config", "/path/that-must-not-be-read.json"}, &output)
	if err == nil || !strings.Contains(err.Error(), "retry required") {
		t.Fatalf("runRemoteInvocation() error = %v, want retry-required protocol error", err)
	}
	assertRemoteCIAgentTokenGuidance(t, output.Bytes(), []string{"remote", "run"})
}

func TestRemoteHookWithoutTokenBootstrapsAndFailsClosed(t *testing.T) {
	var output bytes.Buffer
	err := runRemote([]string{"hook", "pre-commit"}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), "retry required") {
		t.Fatalf("runRemote(hook) error = %v, want retry-required protocol error", err)
	}
	assertRemoteCIAgentTokenGuidance(t, output.Bytes(), []string{"remote", "hook", "pre-commit"})
	var result remoteCIAgentTokenGuidance
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode hook guidance: %v", err)
	}
	wantIssueArgv := []string{os.Args[0], "remote", "run", "--agent-token=issue"}
	if fmt.Sprintf("%q", result.IssueArgv) != fmt.Sprintf("%q", wantIssueArgv) {
		t.Fatalf("hook issue argv = %#v, want %#v", result.IssueArgv, wantIssueArgv)
	}
}

func TestRemoteCIAgentTokenIssueBootstrapsWithoutRunningCI(t *testing.T) {
	var output bytes.Buffer
	err := runRemoteInvocation([]string{"--config", "/path/that-must-not-be-read.json", "--agent-token=issue"}, &output)
	if err == nil || !strings.Contains(err.Error(), "retry required") {
		t.Fatalf("runRemoteInvocation(issue) error = %v, want retry-required protocol error", err)
	}
	result := assertRemoteCIAgentTokenBootstrap(t, output.Bytes(), []string{"remote", "run"})
	retryArgs := result.RetryArgv[len([]string{"remote", "run"})+1:]
	options, err := parseRemoteRunOptions(retryArgs)
	if err != nil {
		t.Fatalf("parse bootstrap retry argv: %v", err)
	}
	if options.AgentTokenDigest != result.AgentTokenDigest {
		t.Fatalf("retry digest = %q, want %q", options.AgentTokenDigest, result.AgentTokenDigest)
	}
}

func TestRemoteHookRejectsIssueEnvironment(t *testing.T) {
	t.Setenv(cicontract.AgentTokenEnvironment, "issue")
	var output bytes.Buffer
	err := runRemote([]string{"hook", "pre-commit"}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), "issue is not allowed") {
		t.Fatalf("runRemote(hook issue env) error = %v, want hook issue rejection", err)
	}
	if output.Len() != 0 {
		t.Fatalf("hook issue environment output = %q, want none", output.String())
	}
}

func TestRemoteHookRejectsAgentTokenFlag(t *testing.T) {
	var output bytes.Buffer
	err := runRemote([]string{"hook", "pre-commit", "--agent-token=issue"}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), "--agent-token is not allowed") {
		t.Fatalf("runRemote(hook issue flag) error = %v, want hook flag rejection", err)
	}
	if output.Len() != 0 {
		t.Fatalf("hook issue flag output = %q, want none", output.String())
	}
}

func TestRemoteHookAcceptsInheritedActualToken(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cicontract.AgentTokenEnvironment, token)
	var output bytes.Buffer
	err = requireRemoteCIAgentToken([]string{"remote", "hook", "pre-commit"}, nil, &output)
	if err != nil {
		t.Fatalf("requireRemoteCIAgentToken(hook inherited token) error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("hook inherited token output = %q, want none", output.String())
	}
}

// TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated 验证独立 hook
// 进程只能观察自己的继承令牌；投递身份在同一进程生成，防止两个 hook 调用被合并为共享 job。
func TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated(t *testing.T) {
	tokens := issueRemoteHookTokens(t, 2)
	results := runConcurrentRemoteHookProcesses(t, tokens)
	assertConcurrentRemoteHookProcessIsolation(t, tokens, results)
}

type remoteHookProcessResult struct {
	output string
	err    error
}

func issueRemoteHookTokens(t *testing.T, count int) []string {
	t.Helper()
	tokens := make([]string, count)
	for index := range tokens {
		var err error
		tokens[index], err = cicontract.GenerateAgentToken()
		if err != nil {
			t.Fatal(err)
		}
	}
	return tokens
}

func runConcurrentRemoteHookProcesses(t *testing.T, tokens []string) []remoteHookProcessResult {
	t.Helper()
	results := make(chan remoteHookProcessResult, len(tokens))
	inputs := make([]io.WriteCloser, 0, len(tokens))
	var waiters sync.WaitGroup
	for _, token := range tokens {
		input := startConcurrentRemoteHookProcess(t, token, results, &waiters)
		inputs = append(inputs, input)
	}
	for _, input := range inputs {
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
	}
	waiters.Wait()
	close(results)
	collected := make([]remoteHookProcessResult, 0, len(tokens))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func startConcurrentRemoteHookProcess(
	t *testing.T,
	token string,
	results chan<- remoteHookProcessResult,
	waiters *sync.WaitGroup,
) io.WriteCloser {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestRemoteHookConcurrentProcessHelper$")
	command.Env = append(os.Environ(),
		"SUPER_DOLPHIN_REMOTE_HOOK_PROCESS_HELPER=1",
		cicontract.AgentTokenEnvironment+"="+token,
	)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waiters.Go(func() {
		err := command.Wait()
		results <- remoteHookProcessResult{output: output.String(), err: err}
	})
	return input
}

func assertConcurrentRemoteHookProcessIsolation(t *testing.T, tokens []string, results []remoteHookProcessResult) {
	t.Helper()
	wantDigests := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		digest, err := cicontract.AgentTokenDigest(token)
		if err != nil {
			t.Fatal(err)
		}
		wantDigests[digest] = struct{}{}
	}
	seenDeliveries := make(map[string]struct{}, len(tokens))
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent hook helper failed: %v (%s)", result.err, result.output)
		}
		fields := strings.Fields(result.output)
		if len(fields) < 2 {
			t.Fatalf("concurrent hook helper output = %q, want token digest and delivery ID", result.output)
		}
		if _, ok := wantDigests[fields[0]]; !ok {
			t.Fatalf("concurrent hook helper inherited unexpected token digest %q", fields[0])
		}
		delete(wantDigests, fields[0])
		if _, exists := seenDeliveries[fields[1]]; exists {
			t.Fatalf("concurrent hook helpers reused delivery ID %q", fields[1])
		}
		seenDeliveries[fields[1]] = struct{}{}
	}
	if len(wantDigests) != 0 || len(seenDeliveries) != len(tokens) {
		t.Fatalf("concurrent hook isolation incomplete: unmatched digests=%d deliveries=%d", len(wantDigests), len(seenDeliveries))
	}
}

func TestRemoteHookConcurrentProcessHelper(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_REMOTE_HOOK_PROCESS_HELPER") != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if err := requireRemoteCIAgentToken([]string{"remote", "hook", "pre-push"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	token, err := resolveRemoteCIAgentToken("")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	deliveryID, err := newHookDeliveryID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "%s %s\n", digest, deliveryID); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCIAgentTokenActualValueContinuesPastHandshake(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runRemoteInvocation([]string{"--config", "/path/that-must-not-be-read.json", "--agent-token=" + token}, &output)
	if err == nil || strings.Contains(err.Error(), "retry required") {
		t.Fatalf("runRemoteInvocation(token) error = %v, want post-handshake configuration error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("runRemoteInvocation(token) output = %q, want no bootstrap", output.String())
	}
}

func TestRemoteCIAgentTokenEnvironmentIsReused(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cicontract.AgentTokenEnvironment, token)
	var output bytes.Buffer
	if err := requireRemoteCIAgentToken([]string{"remote", "run"}, []string{"--config", "/tmp/config.json"}, &output); err != nil {
		t.Fatalf("requireRemoteCIAgentToken() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("requireRemoteCIAgentToken() output = %q, want none", output.String())
	}
	options, err := parseRemoteRunOptions([]string{"--config", "/tmp/remote-ci.json"})
	if err != nil {
		t.Fatalf("parseRemoteRunOptions() error = %v", err)
	}
	if _, present := os.LookupEnv(cicontract.AgentTokenEnvironment); present {
		t.Fatal("parsed CI agent token environment remains inherited")
	}
	if options.AgentTokenDigest == token || strings.Contains(fmt.Sprintf("%#v", options), token) {
		t.Fatalf("parsed options retain raw CI agent token: %#v", options)
	}
}

func TestRemoteCIAgentTokenRejectsFlagAndEnvironmentEvenWhenEqual(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cicontract.AgentTokenEnvironment, token)
	_, err = parseRemoteRunOptions([]string{"--config", "/tmp/remote-ci.json", cicontract.AgentTokenFlag, token})
	if err == nil || !strings.Contains(err.Error(), "exactly one source") || strings.Contains(err.Error(), token) {
		t.Fatalf("parseRemoteRunOptions(flag+env) error = %v", err)
	}
	if _, present := os.LookupEnv(cicontract.AgentTokenEnvironment); present {
		t.Fatal("conflicting CI agent token environment remains inherited")
	}
}

func TestParseRemoteRunOptionsRejectsRequesterFingerprintFlag(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--agent-token", token,
		"--requester-fingerprint", "sha256:" + strings.Repeat("a", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("parseRemoteRunOptions(requester fingerprint) error = %v", err)
	}
}

func TestRemoteCIAgentTokenRejectsRetiredRequesterEnvironment(t *testing.T) {
	t.Setenv(retiredRequesterFingerprintEnvironment, "sha256:"+strings.Repeat("a", 64))
	var output bytes.Buffer
	err := requireRemoteCIAgentToken([]string{"remote", "run"}, []string{"--config", "/tmp/config.json"}, &output)
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("requireRemoteCIAgentToken() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("retired requester environment output = %q, want none", output.String())
	}
}

func assertRemoteCIAgentTokenBootstrap(t *testing.T, output []byte, command []string) remoteCIAgentTokenBootstrap {
	t.Helper()
	var result remoteCIAgentTokenBootstrap
	decodeRemoteCIAgentTokenJSON(t, output, "bootstrap", &result)
	assertRemoteCIAgentTokenBootstrapIdentity(t, result)
	token := assertRemoteCIAgentTokenBootstrapToken(t, result)
	assertRemoteCIAgentTokenBootstrapRetry(t, result, command, token)
	return result
}

// assertRemoteCIAgentTokenBootstrapIdentity 校验阶段二结果的固定协议字段。
func assertRemoteCIAgentTokenBootstrapIdentity(t *testing.T, result remoteCIAgentTokenBootstrap) {
	t.Helper()
	if result.SchemaVersion != remoteCIAgentTokenBootstrapSchemaVersion ||
		result.Kind != "remote_ci_agent_token_bootstrap" || !result.Issued || !result.RetryRequired || result.ExecuteCI {
		t.Fatalf("bootstrap identity = %#v", result)
	}
}

// assertRemoteCIAgentTokenBootstrapToken 校验阶段二原始 token 与摘要的一致性。
func assertRemoteCIAgentTokenBootstrapToken(t *testing.T, result remoteCIAgentTokenBootstrap) string {
	t.Helper()
	token, err := cicontract.ParseAgentToken(result.AgentToken)
	if err != nil {
		t.Fatalf("bootstrap agent token: %v", err)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatalf("bootstrap digest: %v", err)
	}
	if result.AgentTokenDigest != digest {
		t.Fatalf("bootstrap digest = %q, want %q", result.AgentTokenDigest, digest)
	}
	if result.ReuseEnvName != cicontract.AgentTokenEnvironment || result.ReuseEnvValue != token {
		t.Fatalf("bootstrap reuse environment = %q=%q", result.ReuseEnvName, result.ReuseEnvValue)
	}
	return token
}

// assertRemoteCIAgentTokenBootstrapRetry 校验阶段二 retry argv 的命令和 token 后缀。
func assertRemoteCIAgentTokenBootstrapRetry(t *testing.T, result remoteCIAgentTokenBootstrap, command []string, token string) {
	t.Helper()
	if len(result.RetryArgv) < len(command)+3 || !strings.EqualFold(result.RetryArgv[1], command[0]) {
		t.Fatalf("bootstrap retry argv = %#v", result.RetryArgv)
	}
	for index, value := range command {
		if result.RetryArgv[index+1] != value {
			t.Fatalf("bootstrap retry argv command = %#v, want %#v", result.RetryArgv, command)
		}
	}
	if got := result.RetryArgv[len(result.RetryArgv)-2:]; got[0] != cicontract.AgentTokenFlag || got[1] != token {
		t.Fatalf("bootstrap retry argv token suffix = %#v", got)
	}
}

func assertRemoteCIAgentTokenGuidance(t *testing.T, output []byte, command []string) {
	t.Helper()
	var raw map[string]json.RawMessage
	decodeRemoteCIAgentTokenJSON(t, output, "guidance", &raw)
	assertRemoteCIAgentTokenGuidanceDoesNotIssue(t, raw, output)
	var result remoteCIAgentTokenGuidance
	decodeRemoteCIAgentTokenJSON(t, output, "typed guidance", &result)
	assertRemoteCIAgentTokenGuidanceIdentity(t, result)
	assertRemoteCIAgentTokenGuidanceIssueArgv(t, result, command)
}

// decodeRemoteCIAgentTokenJSON 断言输出为单行 JSON 后解码为目标结构。
func decodeRemoteCIAgentTokenJSON(t *testing.T, output []byte, kind string, target any) {
	t.Helper()
	if bytes.Count(output, []byte("\n")) != 1 {
		t.Fatalf("%s output must contain exactly one JSON line, got %q", kind, output)
	}
	if err := json.Unmarshal(output, target); err != nil {
		t.Fatalf("decode %s JSON: %v", kind, err)
	}
}

// assertRemoteCIAgentTokenGuidanceDoesNotIssue 确保阶段一没有泄露 token 或摘要。
func assertRemoteCIAgentTokenGuidanceDoesNotIssue(t *testing.T, raw map[string]json.RawMessage, output []byte) {
	t.Helper()
	if _, present := raw["agent_token"]; present {
		t.Fatalf("guidance must not issue agent_token: %s", output)
	}
	if _, present := raw["agent_token_digest"]; present {
		t.Fatalf("guidance must not issue agent_token_digest: %s", output)
	}
}

// assertRemoteCIAgentTokenGuidanceIdentity 校验阶段一固定协议字段。
func assertRemoteCIAgentTokenGuidanceIdentity(t *testing.T, result remoteCIAgentTokenGuidance) {
	t.Helper()
	if result.SchemaVersion != remoteCIAgentTokenBootstrapSchemaVersion || result.Kind != "remote_ci_agent_token_guidance" || !result.RetryRequired || result.ExecuteCI {
		t.Fatalf("guidance = %#v", result)
	}
	assertRemoteCIAgentTokenGuidanceIssueFields(t, result)
	assertRemoteCIAgentTokenGuidanceUseFields(t, result)
}

// assertRemoteCIAgentTokenGuidanceIssueFields 校验阶段一的显式签发字段。
func assertRemoteCIAgentTokenGuidanceIssueFields(t *testing.T, result remoteCIAgentTokenGuidance) {
	t.Helper()
	if result.IssueFlag != cicontract.AgentTokenFlag+"="+cicontract.AgentTokenIssueValue || result.IssueEnvName != cicontract.AgentTokenEnvironment || result.IssueEnvValue != cicontract.AgentTokenIssueValue {
		t.Fatalf("guidance issue fields = %#v", result)
	}
}

// assertRemoteCIAgentTokenGuidanceUseFields 校验阶段一的实际 token 使用字段。
func assertRemoteCIAgentTokenGuidanceUseFields(t *testing.T, result remoteCIAgentTokenGuidance) {
	t.Helper()
	if result.UseFlag != cicontract.AgentTokenFlag+"=<token>" || result.UseEnvName != cicontract.AgentTokenEnvironment || result.UseEnvValue != "<token>" {
		t.Fatalf("guidance use fields = %#v", result)
	}
}

// assertRemoteCIAgentTokenGuidanceIssueArgv 校验普通调用或 hook 的显式签发入口。
func assertRemoteCIAgentTokenGuidanceIssueArgv(t *testing.T, result remoteCIAgentTokenGuidance, command []string) {
	t.Helper()
	if isRemoteHookCommand(command) {
		want := []string{os.Args[0], "remote", "run", cicontract.AgentTokenFlag + "=" + cicontract.AgentTokenIssueValue}
		if fmt.Sprintf("%q", result.IssueArgv) != fmt.Sprintf("%q", want) {
			t.Fatalf("hook guidance issue argv = %#v, want %#v", result.IssueArgv, want)
		}
		return
	}
	if len(result.IssueArgv) < len(command)+2 {
		t.Fatalf("guidance issue argv = %#v", result.IssueArgv)
	}
	for index, value := range command {
		if result.IssueArgv[index+1] != value {
			t.Fatalf("guidance issue command = %#v, want %#v", result.IssueArgv, command)
		}
	}
}
