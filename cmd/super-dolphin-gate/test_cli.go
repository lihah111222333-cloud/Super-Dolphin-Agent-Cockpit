package main

import "io"

// runTestInvocation 固定 test 场景，并把所有工作负载交给权威远程 ECI 协调器。
func runTestInvocation(args []string, stdout io.Writer) error {
	options, err := parseAutoTestRunOptions(args)
	if err != nil {
		return err
	}
	result, input, runErr := executeRemoteRun(options)
	return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}

// parseAutoTestRunOptions 只接受测试选择器并固定 test 场景。
func parseAutoTestRunOptions(args []string) (remoteRunOptions, error) {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return remoteRunOptions{}, err
	}
	if options.Scenario != "" || options.Profile != "" || options.Entrypoint != "" ||
		options.LocalRef != "" || options.RemoteRef != "" || options.ObservedRemote != "" ||
		options.UpdateKind != "" {
		return remoteRunOptions{}, protocolError("test command does not accept scenario, profile, entrypoint, or push flags")
	}
	if len(options.Tests) == 0 {
		return remoteRunOptions{}, protocolError("test command requires at least one --test selector")
	}
	options.Scenario = "test"
	return options, nil
}
