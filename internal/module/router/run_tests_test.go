package router

import (
	"context"
	"testing"

	routerpkg "github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	rtstore "github.com/anthropic-ai/super-agent-v3/internal/store/routingtest"
)

type fakeRoutingTestReader struct {
	rows []rtstore.RoutingTest
	err  error
}

func (f *fakeRoutingTestReader) ListEnabled(context.Context) ([]rtstore.RoutingTest, error) {
	return f.rows, f.err
}

func TestRunTests_AllPass(t *testing.T) {
	t.Parallel()
	templates := []promptstore.PromptTemplate{
		tpl("main/code-review", "code-reviewer", []string{"review 这段"}),
		tpl("main/default", "main", []string{}), // fallback
	}
	tests := []rtstore.RoutingTest{
		{ID: 1, Input: "帮我 review 这段代码", ExpectedPromptKey: "main/code-review"},
		{ID: 2, Input: "今天天气真好", ExpectedPromptKey: "main/default"},
	}
	svc := NewService(nil, routerpkg.NewRuleRouter(), &fakeReader{rows: templates}, &fakeRoutingTestReader{rows: tests})
	got, err := svc.RunTests(context.Background())
	if err != nil {
		t.Fatalf("RunTests error: %v", err)
	}
	if got.Total != 2 || got.Passed != 2 || got.Failed != 0 {
		t.Fatalf("expected 2/2 pass, got %+v", got)
	}
	if len(got.Failures) != 0 {
		t.Fatalf("expected no failures, got %+v", got.Failures)
	}
}

func TestRunTests_ReportsMismatch(t *testing.T) {
	t.Parallel()
	templates := []promptstore.PromptTemplate{
		tpl("main/debug", "debugger", []string{"panic"}),
		tpl("main/default", "main", []string{}),
	}
	tests := []rtstore.RoutingTest{
		// operator expects this to hit code-review, but only "panic" tag exists,
		// so router will actually fall back to main/default.
		{ID: 1, Input: "帮我 review", ExpectedPromptKey: "main/code-review"},
	}
	svc := NewService(nil, routerpkg.NewRuleRouter(), &fakeReader{rows: templates}, &fakeRoutingTestReader{rows: tests})
	got, _ := svc.RunTests(context.Background())
	if got.Failed != 1 {
		t.Fatalf("expected 1 failure, got %+v", got)
	}
	f := got.Failures[0]
	if f.Expected != "main/code-review" || f.Actual != "main/default" {
		t.Fatalf("failure should record expected + actual, got %+v", f)
	}
}

func TestRunTests_NilReaderIsNoop(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, routerpkg.NewRuleRouter(), &fakeReader{}, nil)
	got, err := svc.RunTests(context.Background())
	if err != nil {
		t.Fatalf("nil routingTestRead must not error: %v", err)
	}
	if got.Total != 0 {
		t.Fatalf("nil reader should return empty result, got %+v", got)
	}
}
