package main

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestParseAutoTestRunOptionsRequiresSelectorsAndOwnsScenario(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	options, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--test", "./internal/module/turn#TestRedact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scenario != "test" || options.AgentTokenDigest != wantDigest || len(options.Tests) != 1 {
		t.Fatalf("test options = %#v", options)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
	}); err == nil {
		t.Fatal("test command accepted no selectors")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--scenario", "test",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil {
		t.Fatal("test command accepted a caller-owned scenario")
	}
}
