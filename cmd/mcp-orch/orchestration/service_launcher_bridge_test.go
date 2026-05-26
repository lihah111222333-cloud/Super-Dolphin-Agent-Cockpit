package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"empty", errors.New(""), false},
		{"rate limit phrase", errors.New("rate limit exceeded"), true},
		{"rate-limit hyphen", errors.New("rate-limit hit, retry later"), true},
		{"rate_limit underscore", errors.New("provider returned rate_limit_error"), true},
		{"too many requests", errors.New("Too Many Requests"), true},
		{"http 429 phrase", errors.New("upstream returned http 429"), true},
		{"status 429", errors.New("status 429: throttled"), true},
		{"status colon 429", errors.New("status: 429"), true},
		{"bare 429 with spaces", errors.New("got 429 from upstream"), true},
		{"transient timeout (not rate limited)", errors.New("i/o timeout"), false},
		{"connection refused (not rate limited)", errors.New("connection refused"), false},
		{"random 4290 (not 429)", errors.New("error 4290 something"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRateLimited(tc.err); got != tc.want {
				t.Fatalf("isRateLimited(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestComputeRetryBackoff(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		err     error
		want    time.Duration
	}{
		{"linear attempt 1 transient", 1, errors.New("i/o timeout"), 2 * time.Second},
		{"linear attempt 2 transient", 2, errors.New("connection refused"), 4 * time.Second},
		{"rate limit attempt 1 -> 60s", 1, errors.New("rate limit hit"), rateLimitBackoff},
		{"rate limit attempt 2 -> still 60s", 2, errors.New("HTTP 429"), rateLimitBackoff},
		{"too many requests -> 60s", 1, errors.New("Too Many Requests"), rateLimitBackoff},
		{"nil err falls through to linear", 1, nil, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeRetryBackoff(tc.attempt, tc.err); got != tc.want {
				t.Fatalf("computeRetryBackoff(%d, %v) = %v, want %v", tc.attempt, tc.err, got, tc.want)
			}
		})
	}
}

func TestClassifyLaunchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want launchErrorClass
	}{
		// nil 当 transient（保守 default：让重试逻辑遇到 nil 不主动跳出）
		{"nil -> transient", nil, launchClassTransient},
		// transient · context cancellation / timeout
		{"context deadline -> transient", context.DeadlineExceeded, launchClassTransient},
		{"context canceled -> transient", context.Canceled, launchClassTransient},
		{"deadline exceeded msg -> transient", errors.New("deadline exceeded"), launchClassTransient},
		// transient · 连接级 / 启动竞态
		{"connection refused -> transient", errors.New("connection refused"), launchClassTransient},
		{"transport unavailable -> transient", errors.New("transport unavailable"), launchClassTransient},
		{"empty thread id -> transient", errors.New("empty thread id"), launchClassTransient},
		{"i/o timeout -> transient", errors.New("i/o timeout"), launchClassTransient},
		{"timed out -> transient", errors.New("timed out"), launchClassTransient},
		// transient · rate limit (1.8c)
		{"rate limit -> transient", errors.New("rate limit exceeded"), launchClassTransient},
		{"http 429 -> transient", errors.New("upstream http 429"), launchClassTransient},
		{"too many requests -> transient", errors.New("Too Many Requests"), launchClassTransient},
		// permanent · 401
		{"401 unauthorized -> permanent", errors.New("401 unauthorized"), launchClassPermanent},
		{"invalid api key -> permanent", errors.New("invalid api key"), launchClassPermanent},
		{"invalid_api_key -> permanent", errors.New("invalid_api_key"), launchClassPermanent},
		{"authentication failed login again -> permanent", errors.New("Authentication failed. Please log in again."), launchClassPermanent},
		{"not authenticated -> permanent", errors.New("provider is not authenticated"), launchClassPermanent},
		{"not logged in -> permanent", errors.New("codex is not logged in; run codex login"), launchClassPermanent},
		{"login required -> permanent", errors.New("login required before starting provider session"), launchClassPermanent},
		{"slash login prompt -> permanent", errors.New("please run /login to authenticate"), launchClassPermanent},
		{"sign in prompt -> permanent", errors.New("sign in to continue"), launchClassPermanent},
		{"auth expired -> permanent", errors.New("provider auth token expired"), launchClassPermanent},
		{"session expired -> permanent", errors.New("provider session expired"), launchClassPermanent},
		{"login-required -> permanent", errors.New("provider login-required before continuing"), launchClassPermanent},
		{"login_required -> permanent", errors.New("provider login_required before continuing"), launchClassPermanent},
		{"claude api connection refused -> permanent", errors.New("API Error: Unable to connect to API (ConnectionRefused)"), launchClassPermanent},
		{"claude selected model unavailable -> permanent", errors.New("There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."), launchClassPermanent},
		// permanent · 403
		{"403 forbidden -> permanent", errors.New("403 forbidden"), launchClassPermanent},
		{"permission denied -> permanent", errors.New("permission denied"), launchClassPermanent},
		// permanent · quota
		{"quota_exhausted -> permanent", errors.New("quota_exhausted"), launchClassPermanent},
		{"insufficient_quota -> permanent", errors.New("insufficient_quota"), launchClassPermanent},
		{"usage limit -> permanent", errors.New("daily usage limit reached"), launchClassPermanent},
		{"out of credits -> permanent", errors.New("out of credits"), launchClassPermanent},
		// permanent · payment
		{"402 payment required -> permanent", errors.New("402 payment_required"), launchClassPermanent},
		{"subscription expired -> permanent", errors.New("subscription expired"), launchClassPermanent},
		// permanent · context_length
		{"context_length_exceeded -> permanent", errors.New("context_length_exceeded"), launchClassPermanent},
		{"context length exceeded msg -> permanent", errors.New("context length exceeded"), launchClassPermanent},
		{"maximum context -> permanent", errors.New("maximum context tokens"), launchClassPermanent},
		{"prompt is too long -> permanent", errors.New("prompt is too long"), launchClassPermanent},
		// permanent · launch contract
		{"missing launch cwd -> permanent", contract.ErrLaunchCWDRequired, launchClassPermanent},
		{"invalid launch cwd -> permanent", contract.ErrLaunchCWDInvalid, launchClassPermanent},
		// permanent 优先级高于 transient（同时含两类关键字时归 permanent）
		{"401 + timeout -> permanent", errors.New("401 unauthorized after i/o timeout"), launchClassPermanent},
		// unknown · 不在任何已知关键字列表里
		{"random unknown -> unknown", errors.New("some_unrecognized_failure"), launchClassUnknown},
		{"empty msg -> unknown", errors.New(""), launchClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLaunchError(tc.err); got != tc.want {
				t.Fatalf("classifyLaunchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestSubmitRemoteTurnPermanentAuthFailureStopsAgent(t *testing.T) {
	launcher := &recordingSettledStopLauncher{recordingStallLauncher: recordingStallLauncher{submitErr: errors.New("401 unauthorized: login required")}}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	err := svc.submitTurnViaLauncher(context.Background(), TurnSubmission{
		AgentID:  agent.id,
		ThreadID: agent.remoteThreadID,
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "continue"}},
	})

	if err == nil || !strings.Contains(err.Error(), "401 unauthorized") {
		t.Fatalf("submitTurnViaLauncher() error = %v, want auth failure", err)
	}
	if launcher.stopCalls != 1 || !containsString(launcher.stopThreads, "thread-remote") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want auth failure cleanup stop", launcher.stopCalls, launcher.stopThreads)
	}
	if agent.state == agentdto.StateTurnRunning || agent.state == agentdto.StateTurnQueued || agent.state == agentdto.StateTurnStarting {
		t.Fatalf("agent.state = %q, want not left in running/pending lifecycle after auth failure", agent.state)
	}
}

func TestSubmitRemoteTurnClaudeAPIConnectionRefusedStopsAgent(t *testing.T) {
	launcher := &recordingSettledStopLauncher{recordingStallLauncher: recordingStallLauncher{submitErr: errors.New("API Error: Unable to connect to API (ConnectionRefused)")}}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	err := svc.submitTurnViaLauncher(context.Background(), TurnSubmission{
		AgentID:  agent.id,
		ThreadID: agent.remoteThreadID,
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "continue"}},
	})

	if err == nil || !strings.Contains(err.Error(), "Unable to connect to API") {
		t.Fatalf("submitTurnViaLauncher() error = %v, want Claude API connection refusal", err)
	}
	if launcher.stopCalls != 1 || !containsString(launcher.stopThreads, "thread-remote") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want Claude API connection refusal cleanup stop", launcher.stopCalls, launcher.stopThreads)
	}
	if !agent.stopRequested || agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("agent after cleanup = state:%q stopRequested:%v activeTurnID:%q, want stopped with cleared active turn", agent.state, agent.stopRequested, agent.activeTurnID)
	}
}

func TestSubmitRemoteTurnClaudeModelUnavailableStopsAgent(t *testing.T) {
	launcher := &recordingSettledStopLauncher{recordingStallLauncher: recordingStallLauncher{submitErr: errors.New("There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model.")}}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	err := svc.submitTurnViaLauncher(context.Background(), TurnSubmission{
		AgentID:  agent.id,
		ThreadID: agent.remoteThreadID,
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "continue"}},
	})

	if err == nil || !strings.Contains(err.Error(), "selected model") {
		t.Fatalf("submitTurnViaLauncher() error = %v, want Claude model unavailable", err)
	}
	if launcher.stopCalls != 1 || !containsString(launcher.stopThreads, "thread-remote") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want Claude model unavailable cleanup stop", launcher.stopCalls, launcher.stopThreads)
	}
	if !agent.stopRequested || agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("agent after cleanup = state:%q stopRequested:%v activeTurnID:%q, want stopped with cleared active turn", agent.state, agent.stopRequested, agent.activeTurnID)
	}
}

func TestStopAgentViaLauncherSettlesRemoteStopWithoutRunner(t *testing.T) {
	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	if err := svc.stopAgentViaLauncher(context.Background(), agent.id, "test_stop"); err != nil {
		t.Fatalf("stopAgentViaLauncher() error = %v", err)
	}
	report, err := svc.GetReport(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.State != string(agentdto.StateStopped) {
		t.Fatalf("GetReport().State = %q, want %q", report.State, agentdto.StateStopped)
	}
}

func TestRemoteLauncherDeclaresStopSettled(t *testing.T) {
	settler, ok := NewRemoteLauncher("127.0.0.1:1").(interface{ StopSettlesAgent() bool })
	if !ok || !settler.StopSettlesAgent() {
		t.Fatal("remoteLauncher must declare Stop settled so launcher-owned stops do not remain stopping without a runner")
	}
}

func TestStopAgentViaLauncherLeavesNonSettledLauncherStoppingWithoutExitSignal(t *testing.T) {
	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	if err := svc.stopAgentViaLauncher(context.Background(), agent.id, "test_stop"); err != nil {
		t.Fatalf("stopAgentViaLauncher() error = %v", err)
	}
	report, err := svc.GetReport(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.State != string(agentdto.StateStopping) {
		t.Fatalf("GetReport().State = %q, want %q until launcher provides an exit signal", report.State, agentdto.StateStopping)
	}
}

type recordingSettledStopLauncher struct {
	recordingStallLauncher
}

func (*recordingSettledStopLauncher) StopSettlesAgent() bool {
	return true
}
