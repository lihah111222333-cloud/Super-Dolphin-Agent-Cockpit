//go:build linux && e2e

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const (
	multilangLifecycleSoakDuration = 15 * time.Minute
	multilangLifecycleProbeEvery   = 30 * time.Second
	multilangLifecycleTestTimeout  = 30 * time.Minute
)

type multilangLifecycleGeneration struct {
	cases      []binaryColdStartLanguageCase
	targets    map[string]string
	sessions   map[string]multilangLifecycleIdentity
	identities []multilangLifecycleIdentity
	entries    []fakeMultilangLifecycleJournalEntry
}

// TestMcpLSPBinaryAllLanguageLifecycleSingleSidecar_E2E 验证一个 Linux sidecar
// 同时持有 registry 中全部 RequiresLSPClient 语言的 client，并覆盖局部 child
// 恢复、sidecar 进程树回收、重启身份和 MCP shutdown/exit 唯一清理。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryAllLanguageLifecycleSingleSidecar_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping all-language lifecycle E2E in short mode")
	}

	root := t.TempDir()
	binary := buildMcpLSPBinaryForTest(t)
	fakeServers := writeFakeMultilangDiagnosticsLangservers(t)
	fakeBundle := writeFakeAllLanguagesProtocolBundle(t, fakeServers)
	journalPath := filepath.Join(t.TempDir(), "multilang-lifecycle.jsonl")
	if multilangLifecycleTestTimeout <= multilangLifecycleSoakDuration {
		t.Fatalf("lifecycle test timeout = %s, must exceed soak duration %s", multilangLifecycleTestTimeout, multilangLifecycleSoakDuration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), multilangLifecycleTestTimeout)
	t.Cleanup(cancel)

	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServers, []string{
		fakeMultilangLifecycleJournalEnv + "=" + journalPath,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + fakeBundle,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + filepath.Join(fakeBundle, "manifest.json"),
	})
	defer forceStopMultilangLifecycleSidecar(t, &client)
	firstSidecarPID := client.cmd.Process.Pid
	firstSidecarStart := requireMultilangLifecycleStartIdentity(t, firstSidecarPID)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	cases := binaryColdStartLanguageCases(t)
	targets := make(map[string]string, len(cases))
	for _, tc := range cases {
		languageRoot := filepath.Join(root, "languages", tc.languageID)
		targets[tc.languageID] = tc.write(t, languageRoot)
	}

	for _, tc := range cases {
		result := callMultilangLifecycleLanguage(t, client, targets[tc.languageID])
		requireMCPToolSuccess(t, client, result, "lifecycle initial "+tc.languageID)
	}
	initialEntries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEvents(entries, "initialize") == len(cases)
	}, 90*time.Second)
	initialSessions := requireMultilangLifecycleSessions(t, initialEntries, cases)
	initialIdentities := requireMultilangLifecycleIdentities(t, initialSessions)
	if len(initialIdentities) != len(cases) {
		t.Fatalf("initial lifecycle identities = %d, want %d; journal=%#v", len(initialIdentities), len(cases), initialEntries)
	}

	// A healthy client must be reused by repeated requests; no extra initialize or PID
	// may appear for any language in the same sidecar.
	for _, tc := range cases {
		result := callMultilangLifecycleLanguage(t, client, targets[tc.languageID])
		requireMCPToolSuccess(t, client, result, "lifecycle healthy reuse "+tc.languageID)
	}
	reusedEntries := readMultilangLifecycleJournal(t, journalPath)
	if countMultilangLifecycleEvents(reusedEntries, "initialize") != len(cases) {
		t.Fatalf("healthy reuse created a new LSP initialize: entries=%#v", reusedEntries)
	}
	for _, identity := range initialIdentities {
		requireMultilangLifecycleProcessAlive(t, identity.PID, "healthy "+identity.LanguageID)
	}
	soakMultilangLifecycleGeneration(t, client, targets, cases, journalPath, initialIdentities)

	recovery := recoverMultilangLifecyclePython(t, client, targets, journalPath, initialSessions, initialIdentities)

	// An abrupt sidecar kill must remove every child from this sidecar's process tree.
	forceStopMultilangLifecycleSidecar(t, &client)
	for pid := range recovery.firstGenerationPIDs {
		waitMultilangLifecycleProcessDead(t, pid, 15*time.Second)
	}
	restartMultilangLifecycleSidecar(t, ctx, binary, root, fakeServers, fakeBundle, journalPath, cases, targets,
		firstSidecarPID, firstSidecarStart, recovery, &client)
}

type multilangLifecycleRecovery struct {
	currentIdentities   map[string]multilangLifecycleIdentity
	firstGenerationPIDs map[int]struct{}
	entries             []fakeMultilangLifecycleJournalEntry
}

func recoverMultilangLifecyclePython(t *testing.T, client *mcpLSPBinaryClient, targets map[string]string, journalPath string, initialSessions map[string]multilangLifecycleIdentity, initialIdentities []multilangLifecycleIdentity) multilangLifecycleRecovery {
	t.Helper()
	python := initialSessions["python"]
	if err := killMultilangLifecycleChild(python.PID); err != nil {
		t.Fatalf("kill python LSP child pid=%d: %v", python.PID, err)
	}
	waitMultilangLifecycleProcessDead(t, python.PID, 10*time.Second)
	result := callMultilangLifecycleLanguage(t, client, targets["python"])
	requireMCPToolSuccess(t, client, result, "lifecycle python child recovery")
	entries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleInitializesForRoot(entries, python.RootURI) == 2
	}, 45*time.Second)
	recovered := requireLatestMultilangLifecycleSession(t, entries, python.RootURI)
	if recovered.PID == python.PID || recovered.StartIdentity == python.StartIdentity {
		t.Fatalf("python child recovery reused identity %d/%q; entries=%#v", python.PID, python.StartIdentity, entries)
	}
	current := make(map[string]multilangLifecycleIdentity, len(initialIdentities))
	for _, identity := range initialIdentities {
		current[identity.LanguageID] = identity
		if identity.LanguageID != "python" {
			requireMultilangLifecycleProcessAlive(t, identity.PID, "unrelated after python recovery "+identity.LanguageID)
		}
	}
	current["python"] = recovered
	pids := lifecycleIdentityPIDSet(initialIdentities)
	for _, identity := range current {
		pids[identity.PID] = struct{}{}
	}
	return multilangLifecycleRecovery{currentIdentities: current, firstGenerationPIDs: pids, entries: entries}
}

func restartMultilangLifecycleSidecar(t *testing.T, ctx context.Context, binary, root, fakeServers, fakeBundle, journalPath string, cases []binaryColdStartLanguageCase, targets map[string]string, firstPID int, firstStart string, recovery multilangLifecycleRecovery, client **mcpLSPBinaryClient) {
	t.Helper()
	*client = startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServers, []string{
		fakeMultilangLifecycleJournalEnv + "=" + journalPath,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + fakeBundle,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + filepath.Join(fakeBundle, "manifest.json"),
	})
	secondPID := (*client).cmd.Process.Pid
	secondStart := requireMultilangLifecycleStartIdentity(t, secondPID)
	if secondPID == firstPID || secondStart == firstStart {
		t.Fatalf("sidecar restart reused identity: first=%d/%q second=%d/%q", firstPID, firstStart, secondPID, secondStart)
	}
	(*client).call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	for _, tc := range cases {
		result := callMultilangLifecycleLanguage(t, *client, targets[tc.languageID])
		requireMCPToolSuccess(t, *client, result, "lifecycle sidecar restart "+tc.languageID)
	}
	initialCount := countMultilangLifecycleEvents(recovery.entries, "initialize")
	entries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEvents(entries, "initialize") == initialCount+len(cases)
	}, 90*time.Second)
	identities := multilangLifecycleGenerationIdentities(t, entries, cases, initialCount)
	requireRestartedMultilangLifecycleIdentities(t, identities, recovery.currentIdentities, cases, entries)
	finishMultilangLifecycleMCPShutdownExit(t, *client)
	*client = nil
	requireUniqueMultilangLifecycleCleanup(t, journalPath, identities, len(cases))
}

func requireRestartedMultilangLifecycleIdentities(t *testing.T, identities, previous map[string]multilangLifecycleIdentity, cases []binaryColdStartLanguageCase, entries []fakeMultilangLifecycleJournalEntry) {
	t.Helper()
	if len(identities) != len(cases) {
		t.Fatalf("second sidecar lifecycle identities = %d, want %d; journal=%#v", len(identities), len(cases), entries)
	}
	for languageID, identity := range identities {
		old, ok := previous[languageID]
		if !ok || identity.PID == old.PID || identity.StartIdentity == old.StartIdentity {
			t.Fatalf("second sidecar reused or introduced %s child identity: old=%#v new=%#v", languageID, old, identity)
		}
	}
}

func requireUniqueMultilangLifecycleCleanup(t *testing.T, journalPath string, identities map[string]multilangLifecycleIdentity, languageCount int) {
	t.Helper()
	entries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEvents(entries, "exit") == languageCount
	}, 45*time.Second)
	for _, identity := range identities {
		shutdowns := countMultilangLifecycleEventsForPID(entries, identity.PID, "shutdown")
		exits := countMultilangLifecycleEventsForPID(entries, identity.PID, "exit")
		if shutdowns != 1 || exits != 1 {
			t.Fatalf("child pid=%d language=%s cleanup events shutdown=%d exit=%d; journal=%#v", identity.PID, identity.LanguageID, shutdowns, exits, entries)
		}
		waitMultilangLifecycleProcessDead(t, identity.PID, 15*time.Second)
	}
}

// soakMultilangLifecycleGeneration 在不少于十五分钟的真实墙钟窗口内周期性访问
// 每个语言 client，并锁定期间没有隐式重启、PID 漂移或提前 idle 回收。
func soakMultilangLifecycleGeneration(
	t *testing.T,
	client *mcpLSPBinaryClient,
	targets map[string]string,
	cases []binaryColdStartLanguageCase,
	journalPath string,
	identities []multilangLifecycleIdentity,
) {
	t.Helper()
	if multilangLifecycleSoakDuration < 15*time.Minute {
		t.Fatalf("multilang lifecycle soak = %s, want at least 15m", multilangLifecycleSoakDuration)
	}
	started := time.Now()
	deadline := time.NewTimer(multilangLifecycleSoakDuration)
	defer deadline.Stop()
	ticker := time.NewTicker(multilangLifecycleProbeEvery)
	defer ticker.Stop()
	probes := 0
	for {
		select {
		case <-deadline.C:
			if elapsed := time.Since(started); elapsed < 15*time.Minute {
				t.Fatalf("lifecycle soak elapsed = %s, want at least 15m", elapsed)
			}
			entries := readMultilangLifecycleJournal(t, journalPath)
			if countMultilangLifecycleEvents(entries, "initialize") != len(cases) {
				t.Fatalf("lifecycle soak restarted an LSP client: entries=%#v", entries)
			}
			for _, identity := range identities {
				requireMultilangLifecycleProcessAlive(t, identity.PID, "15m soak "+identity.LanguageID)
			}
			t.Logf("15m lifecycle soak completed: elapsed=%s probe_rounds=%d languages=%d", time.Since(started), probes, len(cases))
			return
		case <-ticker.C:
			for _, tc := range cases {
				result := callMultilangLifecycleLanguage(t, client, targets[tc.languageID])
				requireMCPToolSuccess(t, client, result, "15m lifecycle soak "+tc.languageID)
			}
			probes++
		}
	}
}

type multilangLifecycleIdentity struct {
	LanguageID    string
	RootURI       string
	PID           int
	StartIdentity string
	Server        string
}

func requireMultilangLifecycleSessions(t *testing.T, entries []fakeMultilangLifecycleJournalEntry, cases []binaryColdStartLanguageCase) map[string]multilangLifecycleIdentity {
	t.Helper()
	sessions := make(map[string]multilangLifecycleIdentity, len(cases))
	initializes := multilangLifecycleInitializeEntries(entries)
	if len(initializes) != len(cases) {
		t.Fatalf("lifecycle initialize entries = %d, want %d; entries=%#v", len(initializes), len(cases), entries)
	}
	for index, entry := range initializes {
		languageID := cases[index].languageID
		startIdentity, err := hiddenexec.ProcessStartIdentity(entry.PID)
		if err != nil {
			t.Fatalf("capture lifecycle child start identity pid=%d: %v", entry.PID, err)
		}
		sessions[languageID] = multilangLifecycleIdentity{
			LanguageID: languageID, RootURI: entry.RootURI, PID: entry.PID,
			StartIdentity: startIdentity, Server: entry.Server,
		}
	}
	return sessions
}

func requireLatestMultilangLifecycleSession(t *testing.T, entries []fakeMultilangLifecycleJournalEntry, rootURI string) multilangLifecycleIdentity {
	t.Helper()
	var found *fakeMultilangLifecycleJournalEntry
	for index := range entries {
		entry := &entries[index]
		if entry.Event == "initialize" && entry.RootURI == rootURI {
			found = entry
		}
	}
	if found == nil {
		t.Fatalf("no lifecycle replacement initialize for root %q; entries=%#v", rootURI, entries)
	}
	startIdentity, err := hiddenexec.ProcessStartIdentity(found.PID)
	if err != nil {
		t.Fatalf("capture replacement child start identity pid=%d: %v", found.PID, err)
	}
	languageID := filepath.Base(strings.TrimRight(rootURI, "/"))
	return multilangLifecycleIdentity{LanguageID: languageID, RootURI: found.RootURI, PID: found.PID, StartIdentity: startIdentity, Server: found.Server}
}

func multilangLifecycleGenerationIdentities(t *testing.T, entries []fakeMultilangLifecycleJournalEntry, cases []binaryColdStartLanguageCase, startOrdinal int) map[string]multilangLifecycleIdentity {
	t.Helper()
	initializes := multilangLifecycleInitializeEntries(entries)
	if startOrdinal < 0 || startOrdinal+len(cases) > len(initializes) {
		t.Fatalf("lifecycle generation range start=%d count=%d total=%d", startOrdinal, len(cases), len(initializes))
	}
	latest := make(map[string]multilangLifecycleIdentity, len(cases))
	for index, entry := range initializes[startOrdinal : startOrdinal+len(cases)] {
		languageID := cases[index].languageID
		startIdentity, err := hiddenexec.ProcessStartIdentity(entry.PID)
		if err != nil {
			t.Fatalf("capture latest lifecycle child start identity pid=%d: %v", entry.PID, err)
		}
		latest[languageID] = multilangLifecycleIdentity{LanguageID: languageID, RootURI: entry.RootURI, PID: entry.PID, StartIdentity: startIdentity, Server: entry.Server}
	}
	return latest
}

func multilangLifecycleInitializeEntries(entries []fakeMultilangLifecycleJournalEntry) []fakeMultilangLifecycleJournalEntry {
	initializes := make([]fakeMultilangLifecycleJournalEntry, 0)
	for _, entry := range entries {
		if entry.Event == "initialize" {
			initializes = append(initializes, entry)
		}
	}
	return initializes
}

func callMultilangLifecycleLanguage(t *testing.T, client *mcpLSPBinaryClient, target string) mcpLSPBinaryResponse {
	t.Helper()
	return client.callTool(t, "inspect", map[string]any{
		"action": "hover",
		"pos":    target + ":1:1",
	})
}

// TestMcpLSPBinaryMultilangIdleLeaseIsolationPrecheck_E2E 验证不同 adapter 的
// workspace 生命周期相互隔离。它显式使用 short-idle tagged binary，只是
// NON_PASS 快速预检；正式生命周期证据仍由同文件的 15 分钟测试提供。
func TestMcpLSPBinaryMultilangIdleLeaseIsolationPrecheck_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multilang idle lease isolation E2E in short mode")
	}
	binary := buildMcpLSPShortIdlePrecheckBinaryForTest(t)
	fakeServers := writeFakeMultilangDiagnosticsLangservers(t)
	cases := []binaryColdStartLanguageCase{
		{languageID: "python", write: writeBinaryColdStartPythonFixture},
		{languageID: "typescript", write: writeBinaryColdStartTypeScriptFixture},
	}
	root := t.TempDir()
	targets := make(map[string]string, len(cases))
	for _, tc := range cases {
		targets[tc.languageID] = tc.write(t, filepath.Join(root, "languages", tc.languageID))
	}
	journalPath := filepath.Join(t.TempDir(), "idle-isolation.jsonl")
	gatePath := filepath.Join(t.TempDir(), "pending-release")
	if err := os.WriteFile(gatePath, []byte("armed"), 0o600); err != nil {
		t.Fatalf("arm pending request gate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServers, []string{
		fakeMultilangLifecycleJournalEnv + "=" + journalPath,
		fakeMultilangPendingRequestGateEnv + "=" + gatePath,
		"MCP_LSP_IDLE_TIMEOUT=1s",
	})
	defer forceStopMultilangLifecycleSidecar(t, &client)
	sidecarPID := client.cmd.Process.Pid
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	for _, tc := range cases {
		result := callMultilangLifecycleLanguage(t, client, targets[tc.languageID])
		requireMCPToolSuccess(t, client, result, "idle isolation warmup "+tc.languageID)
	}
	initialEntries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEvents(entries, "initialize") == len(cases)
	}, 20*time.Second)
	sessions := requireMultilangLifecycleSessions(t, initialEntries, cases)
	exerciseMultilangIdleLeaseIsolation(t, client, targets, journalPath, gatePath, sidecarPID, sessions)
	finishMultilangLifecycleMCPShutdownExit(t, client)
	client = nil
}

func exerciseMultilangIdleLeaseIsolation(t *testing.T, client *mcpLSPBinaryClient, targets map[string]string, journalPath, gatePath string, sidecarPID int, sessions map[string]multilangLifecycleIdentity) {
	t.Helper()
	python := requireMultilangLifecycleSession(t, sessions, "python")
	typescript := requireMultilangLifecycleSession(t, sessions, "typescript")
	if err := os.Remove(gatePath); err != nil {
		t.Fatalf("arm long pending Python request: %v", err)
	}
	pendingID := writeMultilangLifecycleToolCall(t, client, targets["python"])
	pendingEntries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEventsForPID(entries, python.PID, "pending") >= 1
	}, 15*time.Second)
	if countMultilangLifecycleEventsForPID(pendingEntries, python.PID, "pending") != 1 {
		t.Fatalf("Python pending request was not unique: entries=%#v", pendingEntries)
	}
	typescriptID := writeMultilangLifecycleToolCall(t, client, targets["typescript"])
	typescriptResult := readMultilangLifecycleToolResponse(t, client, typescriptID, "idle TypeScript request")
	requireMCPToolSuccess(t, client, typescriptResult, "idle isolation TypeScript release")
	idleEntries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEventsForPID(entries, typescript.PID, "shutdown") >= 1 &&
			countMultilangLifecycleEventsForPID(entries, typescript.PID, "exit") >= 1
	}, 65*time.Second)
	requireMultilangLifecycleEventCount(t, idleEntries, typescript, "shutdown", 1, "idle TypeScript")
	requireMultilangLifecycleEventCount(t, idleEntries, typescript, "exit", 1, "idle TypeScript")
	waitMultilangLifecycleProcessDead(t, typescript.PID, 10*time.Second)
	requireMultilangLifecycleProcessAlive(t, sidecarPID, "sidecar while idle child is reclaimed")
	requireMultilangLifecycleProcessAlive(t, python.PID, "active Python child while idle child is reclaimed")
	if err := os.WriteFile(gatePath, []byte("released"), 0o600); err != nil {
		t.Fatalf("release long pending Python request: %v", err)
	}
	pendingResult := readMultilangLifecycleToolResponse(t, client, pendingID, "released Python request")
	requireMCPToolSuccess(t, client, pendingResult, "active Python request release")
	finalEntries := waitForMultilangLifecycleEntries(t, journalPath, func(entries []fakeMultilangLifecycleJournalEntry) bool {
		return countMultilangLifecycleEventsForPID(entries, python.PID, "shutdown") >= 1 &&
			countMultilangLifecycleEventsForPID(entries, python.PID, "exit") >= 1
	}, 65*time.Second)
	requireMultilangLifecycleEventCount(t, finalEntries, python, "shutdown", 1, "released Python")
	requireMultilangLifecycleEventCount(t, finalEntries, python, "exit", 1, "released Python")
	requireMultilangLifecycleProcessAlive(t, sidecarPID, "sidecar after active request release")
}

func requireMultilangLifecycleSession(t *testing.T, sessions map[string]multilangLifecycleIdentity, languageID string) multilangLifecycleIdentity {
	t.Helper()
	session, ok := sessions[languageID]
	if !ok {
		t.Fatalf("idle isolation missing %s session: %#v", languageID, sessions)
	}
	return session
}

func requireMultilangLifecycleEventCount(t *testing.T, entries []fakeMultilangLifecycleJournalEntry, identity multilangLifecycleIdentity, event string, want int, label string) {
	t.Helper()
	if got := countMultilangLifecycleEventsForPID(entries, identity.PID, event); got != want {
		t.Fatalf("%s %s count = %d, want %d; entries=%#v", label, event, got, want, entries)
	}
}

func writeMultilangLifecycleToolCall(t *testing.T, client *mcpLSPBinaryClient, target string) int64 {
	t.Helper()
	id := time.Now().UnixNano()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":            "inspect",
			"arguments":       map[string]any{"action": "hover", "pos": target + ":1:1"},
			"_cwd":            client.cmd.Dir,
			"_workspaceRoots": []string{client.cmd.Dir},
		},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal pending lifecycle request: %v", err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write pending lifecycle request: %v; stderr=%s", err, client.stderrString())
	}
	return id
}

func readMultilangLifecycleToolResponse(t *testing.T, client *mcpLSPBinaryClient, expectedID int64, label string) mcpLSPBinaryResponse {
	t.Helper()
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v; stderr=%s", label, err, client.stderrString())
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode %s response: %v; raw=%s", label, err, line)
	}
	if response.ID != int(expectedID) {
		t.Fatalf("%s response id=%d, want %d; raw=%s", label, response.ID, expectedID, line)
	}
	if response.Error != nil {
		t.Fatalf("%s returned JSON-RPC error %d: %s; stderr=%s", label, response.Error.Code, response.Error.Message, client.stderrString())
	}
	return response
}

func requireMultilangLifecycleIdentities(t *testing.T, sessions map[string]multilangLifecycleIdentity) []multilangLifecycleIdentity {
	t.Helper()
	identities := make([]multilangLifecycleIdentity, 0, len(sessions))
	for _, identity := range sessions {
		identities = append(identities, identity)
	}
	return identities
}

func readMultilangLifecycleJournal(t *testing.T, path string) []fakeMultilangLifecycleJournalEntry {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open lifecycle journal %s: %v", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close lifecycle journal %s: %v", path, err)
		}
	}()
	var entries []fakeMultilangLifecycleJournalEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry fakeMultilangLifecycleJournalEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("decode lifecycle journal entry: %v; line=%q", err, scanner.Text())
		}
		if entry.PID <= 1 || entry.Server == "" || entry.Event == "" {
			t.Fatalf("incomplete lifecycle journal entry: %#v", entry)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan lifecycle journal: %v", err)
	}
	return entries
}

func waitForMultilangLifecycleEntries(t *testing.T, path string, ready func([]fakeMultilangLifecycleJournalEntry) bool, timeout time.Duration) []fakeMultilangLifecycleJournalEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := tryReadMultilangLifecycleJournal(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read lifecycle journal while waiting: %v", err)
		}
		if err == nil && ready(entries) {
			return entries
		}
		time.Sleep(25 * time.Millisecond)
	}
	entries, err := tryReadMultilangLifecycleJournal(path)
	if err != nil {
		t.Fatalf("lifecycle journal readiness timed out: %v", err)
	}
	t.Fatalf("lifecycle journal readiness timed out after %s: entries=%#v", timeout, entries)
	return nil
}

func tryReadMultilangLifecycleJournal(path string) ([]fakeMultilangLifecycleJournalEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []fakeMultilangLifecycleJournalEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry fakeMultilangLifecycleJournalEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func countMultilangLifecycleEvents(entries []fakeMultilangLifecycleJournalEntry, event string) int {
	count := 0
	for _, entry := range entries {
		if entry.Event == event {
			count++
		}
	}
	return count
}

func countMultilangLifecycleEventsForPID(entries []fakeMultilangLifecycleJournalEntry, pid int, event string) int {
	count := 0
	for _, entry := range entries {
		if entry.PID == pid && entry.Event == event {
			count++
		}
	}
	return count
}

func countMultilangLifecycleInitializesForRoot(entries []fakeMultilangLifecycleJournalEntry, rootURI string) int {
	count := 0
	for _, entry := range entries {
		if entry.Event == "initialize" && entry.RootURI == rootURI {
			count++
		}
	}
	return count
}

func lifecycleIdentityPIDSet(identities []multilangLifecycleIdentity) map[int]struct{} {
	pids := make(map[int]struct{}, len(identities))
	for _, identity := range identities {
		pids[identity.PID] = struct{}{}
	}
	return pids
}

func requireMultilangLifecycleStartIdentity(t *testing.T, pid int) string {
	t.Helper()
	identity, err := hiddenexec.ProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("capture process start identity pid=%d: %v", pid, err)
	}
	return identity
}

func requireMultilangLifecycleProcessAlive(t *testing.T, pid int, label string) {
	t.Helper()
	alive, err := hiddenexec.ProcessAlive(pid)
	if err != nil {
		t.Fatalf("probe %s pid=%d: %v", label, pid, err)
	}
	if !alive {
		t.Fatalf("%s pid=%d is not alive", label, pid)
	}
}

func waitMultilangLifecycleProcessDead(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := hiddenexec.ProcessAlive(pid)
		if err != nil {
			t.Fatalf("probe dead lifecycle pid=%d: %v", pid, err)
		}
		if !alive {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("lifecycle process pid=%d remained alive after %s", pid, timeout)
}

func killMultilangLifecycleChild(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}
	return process.Kill()
}

func forceStopMultilangLifecycleSidecar(t *testing.T, client **mcpLSPBinaryClient) {
	t.Helper()
	if client == nil || *client == nil || (*client).cmd == nil {
		return
	}
	current := *client
	*client = nil
	if err := current.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Errorf("close abrupt sidecar stdin: %v", err)
	}
	if err := current.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill abrupt sidecar pid=%d: %v", current.cmd.Process.Pid, err)
	}
	if err := current.cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Logf("abrupt sidecar pid=%d exited with %v", current.cmd.Process.Pid, err)
	}
}

func finishMultilangLifecycleMCPShutdownExit(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	if client == nil || client.cmd == nil {
		t.Fatal("MCP shutdown/exit requires a live sidecar")
	}
	cmd := client.cmd
	client.call(t, "shutdown", map[string]any{})
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if err != nil {
		t.Fatalf("marshal MCP exit: %v", err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write MCP exit: %v", err)
	}
	if err := client.stdin.Close(); err != nil {
		t.Fatalf("close MCP stdin after exit: %v", err)
	}
	if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("MCP shutdown→exit sidecar exit = %v; stderr=%s", err, client.stderrString())
	}
	client.cmd = nil
}
