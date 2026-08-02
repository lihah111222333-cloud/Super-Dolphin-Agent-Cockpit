package main

import "testing"

func TestParseAutoTestRunOptionsRequiresSelectorsAndOwnsScenario(t *testing.T) {
	options, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--test", "./internal/module/turn#TestRedact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scenario != "test" || len(options.Tests) != 1 {
		t.Fatalf("test options = %#v", options)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
	}); err == nil {
		t.Fatal("test command accepted no selectors")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--scenario", "test",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil {
		t.Fatal("test command accepted a caller-owned scenario")
	}
}
