package main

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type configuredRemotePassHistoryIdentity struct {
	acceptedGeneration uint64
	identityDigest     string
	executionDigest    string
	inputDigest        string
	environmentDigest  string
}

// auditConfiguredRemotePassMisses 对 Prepare 的严格 MISS 做只读历史归因，并拒绝漏复用 retained exact identity。
func auditConfiguredRemotePassMisses(t *testing.T, ledgerPath string, currentGeneration uint64, prepared *remoteci.PreparedRun) {
	t.Helper()
	misses := configuredRemoteMissIdentities(t, prepared)
	history, generations := loadConfiguredRemotePassHistory(t, ledgerPath, misses)
	retained := configuredRetainedPassGenerations(generations, currentGeneration)
	counts := make(map[string]int)
	scopes := make(map[string]int)
	samples := make(map[string]string)
	for workloadID, identity := range misses {
		classification := classifyConfiguredRemotePassMiss(identity, history[workloadID], retained)
		counts[classification]++
		scope := configuredRemoteMissScope(t, workloadID)
		scopes[scope]++
		if samples[scope] == "" || workloadID < samples[scope] {
			samples[scope] = workloadID
		}
	}
	parts := configuredRemoteMissAuditParts(counts)
	t.Logf("configured PASS miss audit total=%d %s", len(misses), strings.Join(parts, " "))
	t.Logf("configured PASS miss scopes %s", strings.Join(configuredRemoteMissAuditParts(scopes), " "))
	t.Logf("configured PASS miss samples %s", strings.Join(configuredRemoteMissAuditSamples(samples), " "))
	if counts["false_miss_exact_retained_identity"] != 0 {
		t.Fatalf("configured PASS preflight left %d retained exact identities in MISS", counts["false_miss_exact_retained_identity"])
	}
}

// configuredRemoteMissScope 将 workload ID 归并到稳定 parent 与 target kind。
func configuredRemoteMissScope(t *testing.T, workloadID string) string {
	t.Helper()
	parent, kind, _, targeted, err := gatecontract.ParseWorkloadID(workloadID)
	if err != nil {
		t.Fatalf("parse configured PASS MISS %q: %v", workloadID, err)
	}
	if !targeted {
		return string(parent)
	}
	return string(parent) + "::" + string(kind)
}

// configuredRemoteMissIdentities 返回严格 MISS 对应的完整当前 identity。
func configuredRemoteMissIdentities(t *testing.T, prepared *remoteci.PreparedRun) map[string]gatecontract.WorkloadPassIdentity {
	t.Helper()
	identities, misses := prepared.WorkloadReuseDecision()
	byID := make(map[string]gatecontract.WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		byID[string(identity.WorkloadID)] = identity
	}
	wanted := make(map[string]gatecontract.WorkloadPassIdentity, len(misses))
	for _, workloadID := range misses {
		key := string(workloadID)
		identity, ok := byID[key]
		if !ok {
			t.Fatalf("configured PASS MISS %q has no current identity", workloadID)
		}
		if _, duplicate := wanted[key]; duplicate {
			t.Fatalf("configured PASS MISS %q is duplicated", workloadID)
		}
		wanted[key] = identity
	}
	return wanted
}

// loadConfiguredRemotePassHistory 只读加载 MISS workload 的历史身份和全局证据代际。
func loadConfiguredRemotePassHistory(
	t *testing.T,
	ledgerPath string,
	wanted map[string]gatecontract.WorkloadPassIdentity,
) (map[string][]configuredRemotePassHistoryIdentity, map[uint64]struct{}) {
	t.Helper()
	database, err := sql.Open("sqlite", ledgerPath)
	if err != nil {
		t.Fatalf("open configured PASS audit ledger: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close configured PASS audit ledger: %v", err)
		}
	})
	if _, err := database.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatalf("set configured PASS audit ledger query-only: %v", err)
	}
	generations := loadConfiguredRemotePassGenerations(t, database)
	return loadConfiguredRemotePassHistoryRows(t, database, wanted), generations
}

// loadConfiguredRemotePassGenerations 读取有 PASS evidence 的全部 accepted generation。
func loadConfiguredRemotePassGenerations(t *testing.T, database *sql.DB) map[uint64]struct{} {
	t.Helper()
	rows, err := database.Query("SELECT DISTINCT accepted_generation FROM ci_workload_pass_evidence")
	if err != nil {
		t.Fatalf("query configured PASS generations: %v", err)
	}
	defer rows.Close()
	generations := make(map[uint64]struct{})
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			t.Fatalf("scan configured PASS generation: %v", err)
		}
		generation, err := strconv.ParseUint(encoded, 10, 64)
		if err != nil || generation == 0 {
			t.Fatalf("configured PASS generation %q is invalid", encoded)
		}
		generations[generation] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate configured PASS generations: %v", err)
	}
	return generations
}

// loadConfiguredRemotePassHistoryRows 读取当前 MISS workload 的历史身份材料。
func loadConfiguredRemotePassHistoryRows(
	t *testing.T,
	database *sql.DB,
	wanted map[string]gatecontract.WorkloadPassIdentity,
) map[string][]configuredRemotePassHistoryIdentity {
	t.Helper()
	rows, err := database.Query(`SELECT workload_id, accepted_generation, identity_digest,
		execution_digest, input_digest, environment_digest FROM ci_workload_pass_evidence`)
	if err != nil {
		t.Fatalf("query configured PASS identity history: %v", err)
	}
	defer rows.Close()
	history := make(map[string][]configuredRemotePassHistoryIdentity)
	for rows.Next() {
		var workloadID, encodedGeneration string
		var identity configuredRemotePassHistoryIdentity
		if err := rows.Scan(&workloadID, &encodedGeneration, &identity.identityDigest,
			&identity.executionDigest, &identity.inputDigest, &identity.environmentDigest); err != nil {
			t.Fatalf("scan configured PASS identity history: %v", err)
		}
		if _, ok := wanted[workloadID]; !ok {
			continue
		}
		generation, err := strconv.ParseUint(encodedGeneration, 10, 64)
		if err != nil || generation == 0 {
			t.Fatalf("configured PASS history generation %q is invalid", encodedGeneration)
		}
		identity.acceptedGeneration = generation
		history[workloadID] = append(history[workloadID], identity)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate configured PASS identity history: %v", err)
	}
	return history
}

// configuredRetainedPassGenerations 返回不超过 current 的最近三个有数据代际。
func configuredRetainedPassGenerations(generations map[uint64]struct{}, current uint64) map[uint64]struct{} {
	ordered := make([]uint64, 0, len(generations))
	for generation := range generations {
		if generation <= current {
			ordered = append(ordered, generation)
		}
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] > ordered[right] })
	if len(ordered) > 3 {
		ordered = ordered[:3]
	}
	retained := make(map[uint64]struct{}, len(ordered))
	for _, generation := range ordered {
		retained[generation] = struct{}{}
	}
	return retained
}

// classifyConfiguredRemotePassMiss 选择最接近的历史身份并给出确定性变化归因。
func classifyConfiguredRemotePassMiss(
	current gatecontract.WorkloadPassIdentity,
	history []configuredRemotePassHistoryIdentity,
	retained map[uint64]struct{},
) string {
	if len(history) == 0 {
		return "no_history"
	}
	exactRetired := false
	bestMatches, bestReason := -1, ""
	for _, candidate := range history {
		if candidate.identityDigest == current.IdentityDigest {
			if _, ok := retained[candidate.acceptedGeneration]; ok {
				return "false_miss_exact_retained_identity"
			}
			exactRetired = true
		}
		matches, reason := configuredRemotePassIdentityDifference(current, candidate)
		if matches > bestMatches || matches == bestMatches && reason < bestReason {
			bestMatches, bestReason = matches, reason
		}
	}
	if exactRetired {
		return "exact_identity_retired_generation"
	}
	return bestReason
}

// configuredRemotePassIdentityDifference 返回三段摘要的相同数量和变化标签。
func configuredRemotePassIdentityDifference(
	current gatecontract.WorkloadPassIdentity,
	history configuredRemotePassHistoryIdentity,
) (int, string) {
	matches := 0
	changed := make([]string, 0, 3)
	for _, component := range []struct {
		name          string
		current, past string
	}{
		{name: "execution", current: current.ExecutionDigest, past: history.executionDigest},
		{name: "input", current: current.InputDigest, past: history.inputDigest},
		{name: "environment", current: current.EnvironmentDigest, past: history.environmentDigest},
	} {
		if component.current == component.past {
			matches++
		} else {
			changed = append(changed, component.name)
		}
	}
	if len(changed) == 0 {
		return matches, "legacy_or_domain_identity"
	}
	return matches, strings.Join(changed, "+") + "_changed"
}

// configuredRemoteMissAuditParts 生成稳定排序的聚合审计字段。
func configuredRemoteMissAuditParts(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Itoa(counts[key]))
	}
	return parts
}

// configuredRemoteMissAuditSamples 生成按 scope 排序的代表 workload。
func configuredRemoteMissAuditSamples(samples map[string]string) []string {
	keys := make([]string, 0, len(samples))
	for key := range samples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+samples[key])
	}
	return parts
}
