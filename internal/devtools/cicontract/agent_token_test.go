package cicontract

import (
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestNoTokenOnlyReturnsApplicationGuidance(t *testing.T) {
	application := AgentTokenApplicationResponse()
	if application.Phase != AgentTokenPhaseApplication || application.ExecuteCI {
		t.Fatalf("application phase/execution = %q/%t, want application/false", application.Phase, application.ExecuteCI)
	}
	if application.Guidance.IssueArgument != AgentTokenFlag+"="+AgentTokenIssueValue || application.Guidance.IssueEnvironment != AgentTokenEnvironment+"="+AgentTokenIssueValue {
		t.Fatalf("application guidance = %#v, want explicit flag and env issue paths", application.Guidance)
	}
}

func TestExplicitIssueOnlyIssuesAndGuidesRetry(t *testing.T) {
	bootstrap := issueAgentTokenBootstrap(t)
	assertIssuedBootstrap(t, bootstrap)
	assertBootstrapGuidance(t, bootstrap)
	assertBootstrapContinuation(t, bootstrap)
	assertBootstrapTokenFormat(t, bootstrap)
}

func issueAgentTokenBootstrap(t *testing.T) AgentTokenBootstrap {
	t.Helper()
	bootstrap, err := IssueAgentTokenBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	return bootstrap
}

func assertIssuedBootstrap(t *testing.T, bootstrap AgentTokenBootstrap) {
	t.Helper()
	if bootstrap.Phase != AgentTokenPhaseIssued || bootstrap.ExecuteCI {
		t.Fatalf("bootstrap phase/execution = %q/%t, want issued/false", bootstrap.Phase, bootstrap.ExecuteCI)
	}
}

func assertBootstrapGuidance(t *testing.T, bootstrap AgentTokenBootstrap) {
	t.Helper()
	if bootstrap.Guidance.IssueArgument != AgentTokenFlag+"="+AgentTokenIssueValue || bootstrap.Guidance.IssueEnvironment != AgentTokenEnvironment+"="+AgentTokenIssueValue || bootstrap.Guidance.ReuseFlag != AgentTokenFlag || bootstrap.Guidance.ReuseEnvironment != AgentTokenEnvironment || bootstrap.Guidance.RetryArgument != AgentTokenFlag+" <agent-token>" {
		t.Fatalf("bootstrap guidance = %#v, want canonical reuse and retry guidance", bootstrap.Guidance)
	}
}

func assertBootstrapContinuation(t *testing.T, bootstrap AgentTokenBootstrap) {
	t.Helper()
	if err := ValidateAgentTokenContinuation(bootstrap.AgentToken, bootstrap.AgentTokenDigest); err != nil {
		t.Fatalf("bootstrap token/digest must continue the same agent: %v", err)
	}
}

func assertBootstrapTokenFormat(t *testing.T, bootstrap AgentTokenBootstrap) {
	t.Helper()
	if !strings.HasPrefix(bootstrap.AgentToken, "sdci1_") || len(bootstrap.AgentToken) != len("sdci1_")+43 {
		t.Fatalf("bootstrap token format = %q, want sdci1_<base64url 32 bytes>", bootstrap.AgentToken)
	}
}

func TestClassifyAgentTokenRequestThreePhasesAndRejectsDualSources(t *testing.T) {
	issued, err := IssueAgentTokenBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, flag, environment string
		want                    AgentTokenPhase
		wantErr                 bool
	}{
		{name: "no token", want: AgentTokenPhaseApplication},
		{name: "flag issue", flag: AgentTokenIssueValue, want: AgentTokenPhaseIssued},
		{name: "environment issue", environment: AgentTokenIssueValue, want: AgentTokenPhaseIssued},
		{name: "flag token", flag: issued.AgentToken, want: AgentTokenPhaseAuthenticated},
		{name: "environment token", environment: issued.AgentToken, want: AgentTokenPhaseAuthenticated},
		{name: "dual equal issue", flag: AgentTokenIssueValue, environment: AgentTokenIssueValue, wantErr: true},
		{name: "dual equal token", flag: issued.AgentToken, environment: issued.AgentToken, wantErr: true},
		{name: "bad token", flag: "not-a-token", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ClassifyAgentTokenRequest(testCase.flag, testCase.environment)
			if (err != nil) != testCase.wantErr || (!testCase.wantErr && got != testCase.want) {
				t.Fatalf("ClassifyAgentTokenRequest(%q, %q) = %q, %v; want %q, error=%t", testCase.flag, testCase.environment, got, err, testCase.want, testCase.wantErr)
			}
		})
	}
}

func TestValidateGitHookAgentTokenOnlyAcceptsCallerOwnedActualToken(t *testing.T) {
	token, err := GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGitHookAgentToken(token); err != nil {
		t.Fatalf("actual caller-owned token rejected by hook: %v", err)
	}
	for _, value := range []string{"", AgentTokenIssueValue, "not-a-token"} {
		if err := ValidateGitHookAgentToken(value); err == nil {
			t.Fatalf("hook accepted non-executable token value %q", value)
		}
	}
}

func TestIssueAgentTokenBootstrapConcurrentFirstRequestsAreDistinct(t *testing.T) {
	const requests = 32
	tokens := make(chan AgentTokenBootstrap, requests)
	var group errgroup.Group
	for range requests {
		group.Go(func() error {
			issued, err := IssueAgentTokenBootstrap()
			if err != nil {
				return err
			}
			tokens <- issued
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	seenTokens, seenDigests := make(map[string]struct{}, requests), make(map[string]struct{}, requests)
	for range requests {
		issued := <-tokens
		if _, exists := seenTokens[issued.AgentToken]; exists {
			t.Fatal("concurrent first request reused an agent token")
		}
		if _, exists := seenDigests[issued.AgentTokenDigest]; exists {
			t.Fatal("concurrent first request reused an agent token digest")
		}
		seenTokens[issued.AgentToken] = struct{}{}
		seenDigests[issued.AgentTokenDigest] = struct{}{}
	}
}

func TestAgentTokenContinuationAndDigestRejectMismatchAndRawToken(t *testing.T) {
	issued, err := IssueAgentTokenBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	other, err := IssueAgentTokenBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAgentTokenContinuation(other.AgentToken, issued.AgentTokenDigest); err == nil {
		t.Fatal("different token must not continue the same agent")
	}
	if err := ValidateAgentTokenDigest(issued.AgentToken); err == nil {
		t.Fatal("raw token must not cross a digest-only boundary")
	}
	for _, invalid := range []string{"", "sha256:abc", "sha512:" + issued.AgentTokenDigest[len("sha256:"):]} {
		if err := ValidateAgentTokenDigest(invalid); err == nil {
			t.Fatalf("invalid digest %q was accepted", invalid)
		}
	}
}
