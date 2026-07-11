package cron

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func assertStartTurnHappyResult(t *testing.T, res StartTurnResult) {
	t.Helper()
	if res.TurnID != "turn-local-1" {
		t.Fatalf("TurnID = %q, want turn-local-1; result=%+v", res.TurnID, res)
	}
	if res.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1; result=%+v", res.ThreadID, res)
	}
	if res.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1; result=%+v", res.AgentID, res)
	}
}

func requireSinglePrepareCall(t *testing.T, svc *fakeTurnService) contract.CronPrepareInput {
	t.Helper()
	if len(svc.prepareCalls) != 1 {
		t.Fatalf("want 1 CronPrepareTurn call, got %d", len(svc.prepareCalls))
	}
	return svc.prepareCalls[0]
}

func assertPreparedStartTurnInput(t *testing.T, got contract.CronPrepareInput) {
	t.Helper()
	if got.Prompt != "daily check" {
		t.Fatalf("Prompt = %q, want daily check; input=%+v", got.Prompt, got)
	}
	if got.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex; input=%+v", got.Provider, got)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("Model = %q, want gpt-5; input=%+v", got.Model, got)
	}
	if got.CWD != "/repo" {
		t.Fatalf("CWD = %q, want /repo; input=%+v", got.CWD, got)
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1; input=%+v", got.AgentID, got)
	}
}

func assertPreparedStartTurnSkills(t *testing.T, got contract.CronPrepareInput) {
	t.Helper()
	if len(got.Skills) != 2 {
		t.Fatalf("Skills = %+v, want two trimmed entries", got.Skills)
	}
	if got.Skills[0].Name != "skill-a" {
		t.Fatalf("Skills[0] = %+v, want skill-a", got.Skills[0])
	}
	if got.Skills[1].Name != "skill-b" {
		t.Fatalf("Skills[1] = %+v, want skill-b", got.Skills[1])
	}
}

func assertPreparedRuntimeConfig(t *testing.T, got contract.CronPrepareInput) {
	t.Helper()
	if got.ThreadRuntimeConfig == nil {
		t.Fatalf("ThreadRuntimeConfig = nil, want decoded map")
	}
	if got.ThreadRuntimeConfig["k"] != "v" {
		t.Fatalf("ThreadRuntimeConfig not decoded: %+v", got.ThreadRuntimeConfig)
	}
}

func assertBootstrapStartTurnResult(t *testing.T, res StartTurnResult) {
	t.Helper()
	if res.ThreadID != "thread-fresh" {
		t.Fatalf("ThreadID = %q, want thread-fresh; result=%+v", res.ThreadID, res)
	}
	if res.AgentID != "agent-fresh" {
		t.Fatalf("AgentID = %q, want agent-fresh; result=%+v", res.AgentID, res)
	}
	if res.TurnID == "" {
		t.Fatalf("TurnID = empty, want started turn; result=%+v", res)
	}
}

func requireSingleBootstrapCall(t *testing.T, bs *recordingBootstrapper) BootstrapRequest {
	t.Helper()
	if len(bs.calls) != 1 {
		t.Fatalf("want 1 bootstrap call, got %d", len(bs.calls))
	}
	return bs.calls[0]
}

func assertBootstrapRequestProjection(t *testing.T, got BootstrapRequest) {
	t.Helper()
	if got.JobID != "job-42" {
		t.Fatalf("JobID = %q, want job-42; request=%+v", got.JobID, got)
	}
	if got.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex; request=%+v", got.Provider, got)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("Model = %q, want gpt-5; request=%+v", got.Model, got)
	}
	if got.CWD != "/repo" {
		t.Fatalf("CWD = %q, want /repo; request=%+v", got.CWD, got)
	}
}

func assertBootstrapConfigForwarded(t *testing.T, got BootstrapRequest) {
	t.Helper()
	if string(got.Config) != `{"codexHome":"/tmp/home"}` {
		t.Fatalf("Config not forwarded verbatim, got %q", string(got.Config))
	}
}

func assertThreadBootstrapResult(t *testing.T, res BootstrapResult) {
	t.Helper()
	if res.ThreadID != "thread-new" {
		t.Fatalf("ThreadID = %q, want thread-new; result=%+v", res.ThreadID, res)
	}
	if res.AgentID != "agent-new" {
		t.Fatalf("AgentID = %q, want agent-new; result=%+v", res.AgentID, res)
	}
}

func requireSingleThreadStartCall(t *testing.T, ts *fakeThreadStarter) contract.CronStartThreadRequest {
	t.Helper()
	if len(ts.calls) != 1 {
		t.Fatalf("want 1 CronStartThread call, got %d", len(ts.calls))
	}
	return ts.calls[0]
}

func assertThreadStartRequestProjection(t *testing.T, got contract.CronStartThreadRequest) {
	t.Helper()
	if got.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex; request=%+v", got.Provider, got)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("Model = %q, want gpt-5; request=%+v", got.Model, got)
	}
	if got.CWD != "/repo" {
		t.Fatalf("CWD = %q, want /repo; request=%+v", got.CWD, got)
	}
	if got.Name != "nightly" {
		t.Fatalf("Name = %q, want nightly; request=%+v", got.Name, got)
	}
}

func assertThreadStartConfig(t *testing.T, got contract.CronStartThreadRequest) {
	t.Helper()
	if got.Config == nil {
		t.Fatalf("Config = nil, want decoded map")
	}
	if got.Config["codexHome"] != "/tmp/home" {
		t.Fatalf("Config[codexHome] = %q, want /tmp/home; config=%+v", got.Config["codexHome"], got.Config)
	}
	if got.Config["codexInstanceKey"] != "glm" {
		t.Fatalf("Config[codexInstanceKey] = %q, want glm; config=%+v", got.Config["codexInstanceKey"], got.Config)
	}
}
