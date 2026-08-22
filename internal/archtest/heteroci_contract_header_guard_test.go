package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestHeterociAcceptedContractDefinesFourNodePostgreSQLBoundary(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	for _, marker := range []string{
		"状态：Accepted",
		"契约身份：remote-ci-four-node/v1",
		"请求协议：remote-ci-portable-manifest/v1",
		"结果协议：remote-ci-result/v1",
		"事件协议：remote-ci-event/v1",
		"远程权威：remote-ci-postgresql-primary/v1",
		"唯一执行池：四台节点，Ubuntu-01、Ubuntu-02、Windows-01、Windows-02",
		"协调端：macOS 只编排、提交请求、读取状态和展示结果，不执行任何 workload。",
		"heteroci_owner schema 与 heteroci_owner_writer role",
		"heteroci_receipt schema 与 heteroci_receipt_writer role",
		"SERIALIZABLE transaction",
		"canonical owner scope 的 transaction advisory lock",
		"synchronous_commit=remote_apply",
		"本地 SQLite、JSON 文件和内存状态都不能成为读写权威",
		"不得双写、交叉提交或互相降级",
		"一次请求只允许一次 fresh snapshot、一次 reservation 和一次 executor binding",
		"CapacityBlocker",
		"不得等待空闲容量",
		"不得自动重试",
		"不得迁移、重试、fallback",
		"第二 PostgreSQL writer",
	} {
		if !strings.Contains(contract, marker) {
			t.Errorf("Accepted four-node contract is missing %q", marker)
		}
	}
	for _, retired := range []string{
		"权威远程 ECI 目标平台：",
		"本地非权威异构拓扑平台：",
		"heteroci-postgresql-local-authorities-protocol/v1",
		"NOT_VERIFIED/not_wired",
		"owner_db_path",
		"receipt_db_path",
	} {
		if strings.Contains(contract, retired) {
			t.Errorf("Accepted four-node contract retains retired marker %q", retired)
		}
	}
}

func TestHeterociFixedNodeAndMacOSExecutionBoundary(t *testing.T) {
	root := findRepoRoot(t)
	manifestPath := filepath.Join(root, "internal", "devtools", "heteroci", "manifest.go")
	manifestSource := readRemoteCIContractGuardFile(t, manifestPath)
	manifestFile := parseRemoteCIContractGuardFile(t, manifestPath)
	for _, marker := range []string{
		"AuthorityLocalNonAuthoritative",
		"ResultStatusNotVerified",
		"ValidateFourNodeExecutionPool",
		"expectedFourNodePlatform",
		"Ubuntu-01",
		"Ubuntu-02",
		"Windows-01",
		"Windows-02",
	} {
		if !strings.Contains(manifestSource, marker) {
			t.Errorf("heteroci manifest is missing fixed-topology marker %q", marker)
		}
	}
	if remoteCIFunctionContainsIdentifier(manifestFile, "expectedFourNodePlatform", "PlatformDarwinAMD64") ||
		remoteCIFunctionContainsIdentifier(manifestFile, "expectedFourNodePlatform", "PlatformDarwinARM64") {
		t.Fatal("heteroci fixed execution pool maps a Darwin platform into the executor pool")
	}
	valid := newHeterogeneousCIManifest(4)
	for index := 2; index < 4; index++ {
		valid.Nodes[index].Platform = heteroci.PlatformWindowsAMD64
		valid.Nodes[index].Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}
	}
	if err := valid.ValidateFourNodeExecutionPool(); err != nil {
		t.Fatalf("ValidateFourNodeExecutionPool(valid): %v", err)
	}
	for _, nodeCount := range []int{1, 3, 5} {
		if err := newHeterogeneousCIManifest(nodeCount).ValidateFourNodeExecutionPool(); err == nil {
			t.Fatalf("ValidateFourNodeExecutionPool(%d nodes) unexpectedly succeeded", nodeCount)
		}
	}
	darwin := cloneHeterogeneousCIManifest(valid)
	darwin.Nodes[0].Platform = heteroci.PlatformDarwinAMD64
	darwin.Nodes[0].Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeDarwinNative}}
	if err := darwin.ValidateFourNodeExecutionPool(); err == nil {
		t.Fatal("ValidateFourNodeExecutionPool accepted a Darwin executor node")
	}
}

func TestHeterociCapacitySnapshotReservationAndBindingBoundary(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	for _, marker := range []string{
		"deterministic capacity sharding planner",
		"fresh snapshot、一次 reservation 和一次 executor binding",
		"CapacityBlocker 必须包含稳定 unit_id",
		"required capability、reason code 和可重放的 plan/manifest digest",
		"不得加入隐式队列",
		"不得切换备用节点集合",
		"不得双跑或投机复制",
		"再次执行同一 operation_id",
	} {
		if !strings.Contains(contract, marker) {
			t.Errorf("capacity contract is missing %q", marker)
		}
	}
	sources := map[string][]string{
		"internal/devtools/heteroci/anchor_wave.go": {
			"BuildAnchorWavePlan", "chooseAnchorCandidate", "CapacityBlockerError", "sort.Slice",
		},
		"internal/devtools/heteroci/router.go": {
			"BuildRoutingDecision", "SnapshotCapturedAtUnixMilli", "CapacityBlockerError", "PackingPlanDigest",
		},
		"internal/devtools/heteroci/routing_lease_book.go": {
			"PlanAndReserve", "SourceSnapshot", "EffectiveSnapshot", "commitRoutingLeaseReservation",
		},
		"internal/devtools/heterociowner/postgresql_owner_plan.go": {
			"PlanAndReserve", "request.OperationID", "readPostgreSQLOperation", "planPostgreSQLTopology", "persistPostgreSQLPlan",
		},
	}
	for relative, markers := range sources {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing capacity/binding marker %q", relative, marker)
			}
		}
	}
}

func TestHeterociPostgreSQLOwnerCoreBoundary(t *testing.T) {
	root := findRepoRoot(t)
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner_schema.go"))
	owner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner.go"))
	plan := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner_plan.go"))
	receipt := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "postgresql_receipt_schema.go"))
	assertHeterociSourceMarkers(t, "PostgreSQL receipt authority", receipt, []string{
		"PostgreSQLReceiptSchema",
		"PostgreSQLReceiptWriterRole",
		"CREATE SCHEMA heteroci_receipt AUTHORIZATION heteroci_receipt_writer",
		"CREATE TABLE heteroci_receipt.batch_receipts",
		"PostgreSQLReceiptStore",
		"sql.LevelSerializable",
		"remote_apply",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL owner schema", schema, []string{
		"PostgreSQLOwnerRuntimeSchemaVersion",
		"PostgreSQLOwnerSchema",
		"PostgreSQLOwnerRole",
		"postgresqlOwnerSchemaStatements",
		"REVOKE ALL ON SCHEMA",
		"CREATE TABLE heteroci_owner.operation_journal",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL owner runtime", owner, []string{
		"sql.LevelSerializable",
		"pg_advisory_xact_lock",
		"current_setting('synchronous_commit')",
		"PostgreSQLOwnerRole",
		"remote_apply",
		"ErrPostgreSQLOwnerCommitUncertain",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL plan owner", plan, []string{
		"PlanAndReserve",
		"request.OperationID",
		"readPostgreSQLOperation",
		"planPostgreSQLTopology",
		"persistPostgreSQLPlan",
	})
	assertHeterociNoLocalPostgreSQLAuthority(t, map[string]string{
		"postgresql_owner.go":          owner,
		"postgresql_owner_plan.go":     plan,
		"postgresql_receipt_schema.go": receipt,
	})
}

// assertHeterociSourceMarkers keeps each production boundary tied to its own source file.
func assertHeterociSourceMarkers(t *testing.T, sourceName, source string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(source, marker) {
			t.Errorf("%s is missing %q", sourceName, marker)
		}
	}
}

// assertHeterociNoLocalPostgreSQLAuthority rejects local fallback or second-writer markers.
func assertHeterociNoLocalPostgreSQLAuthority(t *testing.T, sources map[string]string) {
	t.Helper()
	for sourceName, source := range sources {
		for _, forbidden := range []string{"modernc.org/sqlite", "sql.Open(", "PRAGMA ", "SQLiteFallback", "DualWrite"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains forbidden PG-path local authority marker %q", sourceName, forbidden)
			}
		}
	}
}
