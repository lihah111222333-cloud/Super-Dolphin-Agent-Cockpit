package archtest

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestRemoteCIPortableExecutionProtocolGuards 聚合动态字段与未接线架构守卫。
func TestRemoteCIPortableExecutionProtocolGuards(t *testing.T) {
	t.Run("field_registry", TestRemoteCIPortableExecutionProtocolFieldRegistry)
	t.Run("runtime_unwired", TestRemoteCIPortableExecutionProtocolRuntimeRemainsUnwired)
	t.Run("existing_future_boundary", TestRemoteCIFuturePortableEngineBoundaryContract)
}

// TestRemoteCIPortableExecutionProtocolFieldRegistry 动态枚举 execute DTO 并校验全部消费登记。
func TestRemoteCIPortableExecutionProtocolFieldRegistry(t *testing.T) {
	t.Parallel()
	producer := collectPortableExecutionJSONLeafPaths(t, reflect.TypeFor[remoteci.PortableExecutionRequest](), "")
	consumer := portableExecutionFieldConsumerRegistry()
	assertPortableExecutionFieldRegistryEqual(t, producer, consumer)
}

// TestRemoteCIPortableExecutionProtocolRuntimeRemainsUnwired 锁定本卡只冻结协议而不接执行 side effect。
func TestRemoteCIPortableExecutionProtocolRuntimeRemainsUnwired(t *testing.T) {
	t.Parallel()
	root := findRepoRoot(t)
	contract := readPortableExecutionGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	for _, marker := range []string{
		remoteci.PortableExecutionRequestSchema,
		remoteci.PortableExecutionRuntimeArtifactContractID,
		remoteci.PortableExecutionRuntimeArtifactKind,
		remoteci.PortableExecutionRuntimeArtifactIdentityDomain,
		"`mount_path`",
		"`config_path`/`ledger_path`",
		"协议已冻结、runtime 仍未接线",
		"`execute` 仍保持 fail-fast sentinel",
	} {
		if !strings.Contains(contract, marker) {
			t.Errorf("remote CI contract is missing portable execution marker %q", marker)
		}
	}
	dispatch := readPortableExecutionGuardFile(t, filepath.Join(root, "internal", "devtools", "remotecicli", "portable_validate.go"))
	for _, marker := range []string{`case "execute":`, "return ErrExecutionContractUpgradeRequired"} {
		if !strings.Contains(dispatch, marker) {
			t.Errorf("standalone execute sentinel is missing %q", marker)
		}
	}
	if strings.Contains(dispatch, "DecodePortableExecutionRequest") {
		t.Error("standalone execute must not decode or run the frozen normal protocol before runtime wiring")
	}
	protocol := readPortableExecutionGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteci", "portable_execution_protocol.go"))
	for _, forbidden := range []string{"NewCoordinator", "CreateContainerGroup", "DurationLedgerStore", "internal/devtools/alicloud"} {
		if strings.Contains(protocol, forbidden) {
			t.Errorf("portable execution protocol contains forbidden runtime marker %q", forbidden)
		}
	}
}

func portableExecutionFieldConsumerRegistry() map[string]string {
	return map[string]string{
		"schema_version": "request header validation", "contract_id": "request header validation", "execution_provider": "request header validation",
		"engine.repository_id": "engine repository validation", "engine.object_format": "engine repository validation",
		"engine.commit_sha": "engine repository validation", "engine.tree_sha": "engine repository validation",
		"engine.artifact.contract_id": "runtime artifact type validation", "engine.artifact.kind": "runtime artifact type validation",
		"engine.artifact.artifact_key": "artifact reference validation", "engine.artifact.artifact_sha256": "artifact identity validation",
		"engine.artifact.artifact_size_bytes": "artifact identity validation", "engine.artifact.receipt_key": "artifact reference validation",
		"engine.artifact.receipt_sha256": "artifact reference validation", "engine.artifact.identity_digest": "artifact identity validation",
		"engine.artifact.builder_digest": "artifact identity validation", "engine.artifact.toolchain_digest": "artifact identity validation",
		"engine.artifact.protocol_abi_digest": "artifact ABI validation", "engine.artifact.workload_manifest_abi_digest": "artifact ABI validation",
		"target.repository_id": "target repository validation", "target.object_format": "target repository validation",
		"target.commit_sha": "target repository validation", "target.tree_sha": "target repository validation", "target.mount_path": "target mount validation",
		"target.workload_manifest.repo_relative_path": "target manifest validation", "target.workload_manifest.sha256": "target manifest validation",
		"target.workload_manifest.abi_digest": "target manifest validation", "target.workload_manifest.schema_version": "target manifest validation",
		"target.workload_manifest.repository_id": "target manifest validation",
		"authority.config_path":                  "global authority path validation", "authority.config_sha256": "global authority validation",
		"authority.ledger_path": "global authority path validation", "authority.ledger_identity": "global authority validation",
		"authority.accepted_generation": "global authority validation", "authority.accepted_authority_sha256": "global authority validation",
		"audit.agent_token_digest": "audit validation", "audit.profile": "audit validation",
		"audit.entrypoint": "audit validation", "audit.force": "canonical presence and audit projection",
	}
}

func collectPortableExecutionJSONLeafPaths(t *testing.T, value reflect.Type, prefix string) map[string]bool {
	t.Helper()
	if value.Kind() != reflect.Struct {
		t.Fatalf("portable execution producer root %s is not a struct", value)
	}
	paths := make(map[string]bool)
	for field := range value.Fields() {
		fieldPath := portableExecutionJSONFieldPath(t, field, prefix)
		if field.Type.Kind() == reflect.Struct {
			mergePortableExecutionJSONLeafPaths(t, paths, collectPortableExecutionJSONLeafPaths(t, field.Type, fieldPath))
			continue
		}
		mergePortableExecutionJSONLeafPaths(t, paths, map[string]bool{fieldPath: true})
	}
	return paths
}

func portableExecutionJSONFieldPath(t *testing.T, field reflect.StructField, prefix string) string {
	t.Helper()
	jsonName, options, _ := strings.Cut(field.Tag.Get("json"), ",")
	if field.PkgPath != "" || field.Anonymous || jsonName == "" || jsonName == "-" || options != "" {
		t.Fatalf("portable execution producer field %s has unsupported JSON shape", field.Name)
	}
	if prefix == "" {
		return jsonName
	}
	return prefix + "." + jsonName
}

func mergePortableExecutionJSONLeafPaths(t *testing.T, destination, source map[string]bool) {
	t.Helper()
	for fieldPath := range source {
		if destination[fieldPath] {
			t.Fatalf("portable execution producer duplicates JSON path %q", fieldPath)
		}
		destination[fieldPath] = true
	}
}

func assertPortableExecutionFieldRegistryEqual(t *testing.T, producer map[string]bool, consumer map[string]string) {
	t.Helper()
	var missing, stale []string
	for field := range producer {
		if strings.TrimSpace(consumer[field]) == "" {
			missing = append(missing, field)
		}
	}
	for field, reason := range consumer {
		if strings.TrimSpace(reason) == "" || !producer[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("portable execution field registry drifted: missing=%v stale=%v", missing, stale)
	}
}

func readPortableExecutionGuardFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read portable execution guard file: %v", err)
	}
	return string(data)
}
