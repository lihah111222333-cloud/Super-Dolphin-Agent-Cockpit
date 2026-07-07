package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherwire"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/kelindar/event"
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
			if got := launcherrors.IsRateLimited(tc.err); got != tc.want {
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
		{"rate limit attempt 1 -> 60s", 1, errors.New("rate limit hit"), launcherrors.RateLimitBackoff},
		{"rate limit attempt 2 -> still 60s", 2, errors.New("HTTP 429"), launcherrors.RateLimitBackoff},
		{"too many requests -> 60s", 1, errors.New("Too Many Requests"), launcherrors.RateLimitBackoff},
		{"nil err falls through to linear", 1, nil, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := launcherrors.ComputeRetryBackoff(tc.attempt, tc.err); got != tc.want {
				t.Fatalf("computeRetryBackoff(%d, %v) = %v, want %v", tc.attempt, tc.err, got, tc.want)
			}
		})
	}
}

func TestClassifyLaunchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want launcherrors.Class
	}{
		// nil 当 transient（保守 default：让重试逻辑遇到 nil 不主动跳出）
		{"nil -> transient", nil, launcherrors.ClassTransient},
		// transient · context cancellation / timeout
		{"context deadline -> transient", context.DeadlineExceeded, launcherrors.ClassTransient},
		{"context canceled -> transient", context.Canceled, launcherrors.ClassTransient},
		{"deadline exceeded msg -> transient", errors.New("deadline exceeded"), launcherrors.ClassTransient},
		// transient · 连接级 / 启动竞态
		{"connection refused -> transient", errors.New("connection refused"), launcherrors.ClassTransient},
		{"transport unavailable -> transient", errors.New("transport unavailable"), launcherrors.ClassTransient},
		{"empty thread id -> transient", errors.New("empty thread id"), launcherrors.ClassTransient},
		{"i/o timeout -> transient", errors.New("i/o timeout"), launcherrors.ClassTransient},
		{"timed out -> transient", errors.New("timed out"), launcherrors.ClassTransient},
		// transient · rate limit (1.8c)
		{"rate limit -> transient", errors.New("rate limit exceeded"), launcherrors.ClassTransient},
		{"http 429 -> transient", errors.New("upstream http 429"), launcherrors.ClassTransient},
		{"too many requests -> transient", errors.New("Too Many Requests"), launcherrors.ClassTransient},
		// permanent · 401
		{"401 unauthorized -> permanent", errors.New("401 unauthorized"), launcherrors.ClassPermanent},
		{"invalid api key -> permanent", errors.New("invalid api key"), launcherrors.ClassPermanent},
		{"invalid_api_key -> permanent", errors.New("invalid_api_key"), launcherrors.ClassPermanent},
		{"authentication failed login again -> permanent", errors.New("Authentication failed. Please log in again."), launcherrors.ClassPermanent},
		{"not authenticated -> permanent", errors.New("provider is not authenticated"), launcherrors.ClassPermanent},
		{"not logged in -> permanent", errors.New("codex is not logged in; run codex login"), launcherrors.ClassPermanent},
		{"login required -> permanent", errors.New("login required before starting provider session"), launcherrors.ClassPermanent},
		{"slash login prompt -> permanent", errors.New("please run /login to authenticate"), launcherrors.ClassPermanent},
		{"sign in prompt -> permanent", errors.New("sign in to continue"), launcherrors.ClassPermanent},
		{"auth expired -> permanent", errors.New("provider auth token expired"), launcherrors.ClassPermanent},
		{"session expired -> permanent", errors.New("provider session expired"), launcherrors.ClassPermanent},
		{"login-required -> permanent", errors.New("provider login-required before continuing"), launcherrors.ClassPermanent},
		{"login_required -> permanent", errors.New("provider login_required before continuing"), launcherrors.ClassPermanent},
		{"claude api connection refused -> permanent", errors.New("API Error: Unable to connect to API (ConnectionRefused)"), launcherrors.ClassPermanent},
		{"claude selected model unavailable -> permanent", errors.New("There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."), launcherrors.ClassPermanent},
		// permanent · 403
		{"403 forbidden -> permanent", errors.New("403 forbidden"), launcherrors.ClassPermanent},
		{"permission denied -> permanent", errors.New("permission denied"), launcherrors.ClassPermanent},
		// permanent · quota
		{"quota_exhausted -> permanent", errors.New("quota_exhausted"), launcherrors.ClassPermanent},
		{"insufficient_quota -> permanent", errors.New("insufficient_quota"), launcherrors.ClassPermanent},
		{"usage limit -> permanent", errors.New("daily usage limit reached"), launcherrors.ClassPermanent},
		{"out of credits -> permanent", errors.New("out of credits"), launcherrors.ClassPermanent},
		// permanent · payment
		{"402 payment required -> permanent", errors.New("402 payment_required"), launcherrors.ClassPermanent},
		{"subscription expired -> permanent", errors.New("subscription expired"), launcherrors.ClassPermanent},
		// permanent · context_length
		{"context_length_exceeded -> permanent", errors.New("context_length_exceeded"), launcherrors.ClassPermanent},
		{"context length exceeded msg -> permanent", errors.New("context length exceeded"), launcherrors.ClassPermanent},
		{"maximum context -> permanent", errors.New("maximum context tokens"), launcherrors.ClassPermanent},
		{"prompt is too long -> permanent", errors.New("prompt is too long"), launcherrors.ClassPermanent},
		// permanent · launch contract
		{"missing launch cwd -> permanent", contract.ErrLaunchCWDRequired, launcherrors.ClassPermanent},
		{"invalid launch cwd -> permanent", contract.ErrLaunchCWDInvalid, launcherrors.ClassPermanent},
		// permanent 优先级高于 transient（同时含两类关键字时归 permanent）
		{"401 + timeout -> permanent", errors.New("401 unauthorized after i/o timeout"), launcherrors.ClassPermanent},
		// unknown · 不在任何已知关键字列表里
		{"random unknown -> unknown", errors.New("some_unrecognized_failure"), launcherrors.ClassUnknown},
		{"empty msg -> unknown", errors.New(""), launcherrors.ClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := launcherrors.Classify(tc.err); got != tc.want {
				t.Fatalf("classifyLaunchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestDAGAgentExecutorLocalLauncherFailsFastWithRemoteLauncherDiagnostic(t *testing.T) {
	svc := NewService(silentLogger(), nil, NewLocalLauncher(nil, silentLogger()), nil, nil, nil)
	agentExec := nodeexec.NewAgentExecutor(NewServiceAgentLauncher(svc), nodeexec.WithRecorder(noopNodeSpawnRecorder{}))

	out, err := agentExec.Execute(context.Background(), serviceLauncherBridgeAgentNode(t), nodeexec.RunContext{
		DagKey:  "dag-local",
		NodeKey: "agent-node",
		RunID:   42,
	})

	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != nodeexec.NodeStatusFailed || out.FailureClass != nodeexec.FailureClassHard {
		t.Fatalf("outcome = (%q,%q), want failed hard; summary=%q", out.Status, out.FailureClass, out.ErrorSummary)
	}
	for _, want := range []string{
		"DAG agent launch requires remote launcher",
		"dag_key=dag-local",
		"node_key=agent-node",
		"thread_id",
		"spawning_thread_id",
		"fix:",
	} {
		if !strings.Contains(out.ErrorSummary, want) {
			t.Fatalf("ErrorSummary = %q, want substring %q", out.ErrorSummary, want)
		}
	}
	if strings.Contains(out.ErrorSummary, "command is required") {
		t.Fatalf("ErrorSummary = %q, must not expose generic command validation", out.ErrorSummary)
	}
	if got := len(svc.agents); got != 0 {
		t.Fatalf("service created %d agents before local DAG-agent fail-fast, want 0", got)
	}
}

func TestDAGAgentExecutorRemoteLauncherKeepsCommandlessSpawnWritebackPath(t *testing.T) {
	events := []string{}
	recorder := &recordingServiceBridgeSpawnRecorder{events: &events}
	var turnStart map[string]any
	launcher := serviceBridgeRemoteLauncher(t, &events, &turnStart)
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agentExec := nodeexec.NewAgentExecutor(NewServiceAgentLauncher(svc), nodeexec.WithRecorder(recorder))

	out, err := agentExec.Execute(context.Background(), serviceLauncherBridgeAgentNode(t), nodeexec.RunContext{
		DagKey:  "dag-remote",
		NodeKey: "agent-node",
		RunID:   84,
	})

	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != nodeexec.NodeStatusDone || out.ErrorSummary != "" {
		t.Fatalf("outcome = %+v, want done without summary", out)
	}
	if got, want := strings.Join(events, ","), "thread/start,record,turn/start"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	if recorder.threadID != "thread-remote" || recorder.dagKey != "dag-remote" || recorder.nodeKey != "agent-node" || recorder.runID != 84 {
		t.Fatalf("recorded spawn = %#v, want dag/thread writeback for remote launch", recorder)
	}
	if got := turnStart[launcherwire.ParamThreadID]; got != "thread-remote" {
		t.Fatalf("turn/start thread_id = %#v, want thread-remote", got)
	}
}

func TestForkedLaunchRejectsPayloadParentThreadID(t *testing.T) {
	var forked map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		launcherwire.MethodThreadFork: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			forked = req
			t.Fatalf("thread/fork should not be called when payload parent_thread_id is supplied: %#v", req)
			return map[string]any{launcherwire.RespNewThreadID: "thread-child", launcherwire.RespAgentID: "remote-child"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("agent-parent")
	parent.state = agentdto.StateIdle
	parent.threadID = "thread-parent-trusted"
	parent.remoteThreadID = "thread-parent-trusted"
	parent.launchSeq = 1
	svc.agents[parent.id] = parent

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:        "agent-child",
		Name:           "forked child",
		ParentID:       "agent-parent",
		ParentThreadID: "thread-parent-payload",
		ContextMode:    "forked",
		Cwd:            t.TempDir(),
	})

	if err == nil || !strings.Contains(err.Error(), "parent_thread_id") {
		t.Fatalf("LaunchAgent() error = %v, want parent_thread_id trust-boundary rejection", err)
	}
	if forked != nil {
		t.Fatalf("thread/fork request = %#v, want no fork", forked)
	}
}

func TestForkedLaunchRequiresTrustedParentBinding(t *testing.T) {
	var forked map[string]any
	var named map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		launcherwire.MethodThreadFork: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			forked = req
			return map[string]any{launcherwire.RespNewThreadID: "thread-child", launcherwire.RespAgentID: "remote-child"}, nil
		}),
		launcherwire.MethodThreadNameSet: handler.New(func(_ context.Context, req map[string]any) (struct{}, error) {
			named = req
			return struct{}{}, nil
		}),
	}), nil, nil, nil)
	svc.agentBindings = fakeAgentBindingStore{binding: &PersistedBinding{
		AgentID:       "agent-parent",
		Provider:      "codex",
		CodexThreadID: "thread-parent-trusted",
		Cwd:           t.TempDir(),
	}}
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{{
		ThreadID: "thread-parent-trusted",
		AgentID:  "agent-parent",
		Status:   "running",
	}}}

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:     "agent-child",
		Name:        "forked child",
		ParentID:    "agent-parent",
		ContextMode: "forked",
		Cwd:         t.TempDir(),
	})

	if err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if got := forked[launcherwire.ParamThreadID]; got != "thread-parent-trusted" {
		t.Fatalf("thread/fork thread_id = %#v, want trusted persisted parent thread", got)
	}
	if got := named[launcherwire.ParamThreadID]; got != "thread-child" {
		t.Fatalf("thread/name/set thread_id = %#v, want thread-child", got)
	}
	agent := svc.agents["remote-child"]
	if agent == nil || agent.remoteThreadID != "thread-child" || agent.state != agentdto.StateIdle {
		t.Fatalf("launched agent = %#v, want idle remote-child on thread-child", agent)
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

func TestRecoverStoppedLauncherAgentAllowsFollowUpSubmit(t *testing.T) {
	launcher, svc, agent := newStoppedLauncherRecoverTest(t)
	archiveAndRecoverLauncherAgent(t, svc, agent)
	assertRecoveredLauncherSubmit(t, svc, launcher, agent)
}

func newStoppedLauncherRecoverTest(t *testing.T) (*recordingStallLauncher, *service, *agentRuntime) {
	t.Helper()
	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.command = longRunningTestCommandLine()
	agent.provider = "codex"
	agent.providerSource = "inferred"
	agent.state = agentdto.StateIdle
	agent.activeTurnID = ""
	agent.launchSeq = 1
	t.Cleanup(func() { cleanupAgentProcess(agent) })
	return launcher, svc, agent
}

func archiveAndRecoverLauncherAgent(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	archived, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "archived")
	if err != nil || !archived {
		t.Fatalf("archiveAgentViaLauncher() = (%v, %v), want archived nil", archived, err)
	}
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
}

func assertRecoveredLauncherSubmit(t *testing.T, svc *service, launcher *recordingStallLauncher, agent *agentRuntime) {
	t.Helper()
	if launcher.launchCalls != 1 {
		t.Fatalf("launcher launch calls = %d, want recover to relaunch stopped remote agent", launcher.launchCalls)
	}
	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.ThreadID != "thread-recovered" || snapshot.State != string(agentdto.StateIdle) {
		t.Fatalf("Snapshot() = %#v, want idle recovered remote thread", snapshot)
	}
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: snapshot.ThreadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "follow-up"}}}); err != nil {
		t.Fatalf("SubmitTurn() after recover error = %v", err)
	}
	if launcher.submitCalls != 1 || launcher.submitThreadID != "thread-recovered" {
		t.Fatalf("launcher submit = calls:%d thread:%q, want one submit on recovered thread", launcher.submitCalls, launcher.submitThreadID)
	}
}

func TestRemoteLauncherDeclaresStopSettled(t *testing.T) {
	settler, ok := NewRemoteLauncher("127.0.0.1:1").(interface{ StopSettlesAgent() bool })
	if !ok || !settler.StopSettlesAgent() {
		t.Fatal("remoteLauncher must declare Stop settled so launcher-owned stops do not remain stopping without a runner")
	}
}

func TestStopAgentViaLauncherSettlesNonSettledLauncherAfterStopReturns(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), dispatcher, launcher, nil, nil, nil)
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
	requireStopTestEvent(t, stopped, "test_stop", "agent-remote")
}

type recordingSettledStopLauncher struct {
	recordingStallLauncher
}

func (*recordingSettledStopLauncher) StopSettlesAgent() bool {
	return true
}

func serviceBridgeRemoteLauncher(t *testing.T, events *[]string, turnStart *map[string]any) *remoteLauncher {
	t.Helper()
	return remoteLocalLauncher(t, handler.Map{
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			*events = append(*events, "thread/start")
			rejectServiceBridgeCommandParam(t, req)
			return map[string]any{"thread": map[string]any{"id": "thread-remote"}, "agent_id": "remote-agent"}, nil
		}),
		launcherwire.MethodTurnStart: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			*events = append(*events, "turn/start")
			*turnStart = req
			return map[string]any{launcherwire.RespTurnID: "turn-remote"}, nil
		}),
	})
}

func rejectServiceBridgeCommandParam(t *testing.T, req map[string]any) {
	t.Helper()
	if _, ok := req["command"]; ok {
		t.Fatalf("thread/start req unexpectedly carried command: %#v", req["command"])
	}
}

type recordingServiceBridgeSpawnRecorder struct {
	events   *[]string
	dagKey   string
	nodeKey  string
	threadID string
	runID    int64
}

func (r *recordingServiceBridgeSpawnRecorder) RecordNodeSpawn(_ context.Context, dagKey, nodeKey string, runID int64, threadID string) error {
	if r.events != nil {
		*r.events = append(*r.events, "record")
	}
	r.dagKey = dagKey
	r.nodeKey = nodeKey
	r.runID = runID
	r.threadID = threadID
	return nil
}

func serviceLauncherBridgeAgentNode(t *testing.T) nodeexec.Node {
	t.Helper()
	return nodeexec.Node{
		DagKey:   "dag-from-node",
		NodeKey:  "node-from-node",
		NodeType: "agent",
		Title:    "Agent node",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"implementer","cwd":"/tmp/agent-launch"},"first_turn":"start work"}`),
	}
}
