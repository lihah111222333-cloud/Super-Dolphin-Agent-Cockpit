package heterociowner

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestPostgreSQLOwnerProtocol 聚合本卡全部 strict protocol 与字段守卫证据。
func TestPostgreSQLOwnerProtocol(t *testing.T) {
	t.Run("fenced failover", TestPostgreSQLOwnerProtocolAcceptsOnlyFencedFailoverModes)
	t.Run("unsafe semantics", TestPostgreSQLOwnerProtocolRejectsUnsafeSemantics)
	t.Run("strict decoder", TestPostgreSQLOwnerProtocolStrictDecoder)
	t.Run("field guard", TestPostgreSQLOwnerProtocolFieldGuard)
}

// TestPostgreSQLOwnerProtocolAcceptsOnlyFencedFailoverModes 锁定人工隔离与第三见证两种合法形态。
func TestPostgreSQLOwnerProtocolAcceptsOnlyFencedFailoverModes(t *testing.T) {
	for _, mode := range []struct {
		name          string
		failoverMode  string
		witnessVoters int
	}{
		{name: "manual fenced promotion", failoverMode: PostgreSQLOwnerFailoverManualFencedPromotion},
		{name: "third witness fenced promotion", failoverMode: PostgreSQLOwnerFailoverQuorumWitnessFencedPromotion, witnessVoters: 1},
	} {
		t.Run(mode.name, func(t *testing.T) {
			config := validPostgreSQLOwnerProtocolConfig()
			config.Failover.Mode = mode.failoverMode
			config.Failover.WitnessVoters = mode.witnessVoters
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatalf("marshal protocol config: %v", err)
			}
			decoded, err := DecodePostgreSQLOwnerProtocolConfig(encoded)
			if err != nil {
				t.Fatalf("DecodePostgreSQLOwnerProtocolConfig: %v", err)
			}
			if !reflect.DeepEqual(decoded, config) {
				t.Fatalf("decoded config drifted:\n got %#v\nwant %#v", decoded, config)
			}
		})
	}
}

// TestPostgreSQLOwnerProtocolRejectsUnsafeSemantics 锁定会破坏 fence 与唯一 authority 的非法组合。
func TestPostgreSQLOwnerProtocolRejectsUnsafeSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PostgreSQLOwnerProtocolConfig)
	}{
		{name: "authoritative status", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Status = "VERIFIED" }},
		{name: "remote authority", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Authority = "REMOTE_AUTHORITATIVE" }},
		{name: "local database placement", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Placement = "local_postgresql" }},
		{name: "one database node", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Failover.DatabaseNodes = 1 }},
		{name: "automatic two node failover", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Failover.AutomaticTwoNodeFailover = "allowed" }},
		{name: "promotion without fencing", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Failover.OldPrimaryFencing = "best_effort" }},
		{name: "manual mode with witness", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Failover.WitnessVoters = 1 }},
		{name: "quorum mode without witness", mutate: func(config *PostgreSQLOwnerProtocolConfig) {
			config.Failover.Mode = PostgreSQLOwnerFailoverQuorumWitnessFencedPromotion
			config.Failover.WitnessVoters = 0
		}},
		{name: "read from standby", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.ReadPolicy = "read_prefer_standby" }},
		{name: "async commit", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.SynchronousCommit = "local" }},
		{name: "async downgrade", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.AsyncDowngrade = "allowed" }},
		{name: "weak isolation", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.IsolationLevel = "read_committed" }},
		{name: "missing scope lock", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.ScopeSerialization = "none" }},
		{name: "cross authority transaction", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Transaction.CrossAuthorityTransaction = "allowed" }},
		{name: "new operation after ambiguous commit", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Recovery.AmbiguousCommit = "new_operation_id" }},
		{name: "local operation lookup", mutate: func(config *PostgreSQLOwnerProtocolConfig) {
			config.Recovery.AmbiguousCommit = "local_replica_operation_lookup"
		}},
		{name: "new receipt after ambiguous commit", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Recovery.ReceiptAmbiguousCommit = "new_claim" }},
		{name: "reuse old epoch after promotion", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Recovery.PromotionEpoch = "reuse_current_epoch" }},
		{name: "shared schema", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Receipt.Schema = config.Owner.Schema }},
		{name: "shared role", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Receipt.Role = config.Owner.Role }},
		{name: "owner sqlite", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.OwnerSQLite = "allowed" }},
		{name: "receipt sqlite", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.ReceiptSQLite = "allowed" }},
		{name: "local replica read", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.LocalReplicaRead = "allowed" }},
		{name: "local replica mutation", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.LocalReplicaMutation = "allowed" }},
		{name: "sqlite fallback", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.SQLiteFallback = "allowed" }},
		{name: "dual write", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.DualWrite = "allowed" }},
		{name: "network fallback", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.NetworkFailure = "fallback" }},
		{name: "remote ledger access", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.RemoteDurationLedger = "allowed" }},
		{name: "runtime wired", mutate: func(config *PostgreSQLOwnerProtocolConfig) { config.Boundary.RuntimeWiring = "wired" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validPostgreSQLOwnerProtocolConfig()
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatalf("Validate() accepted unsafe config: %#v", config)
			}
		})
	}
}

// TestPostgreSQLOwnerProtocolStrictDecoder 拒绝非 canonical、重复、未知、null 与尾随输入。
func TestPostgreSQLOwnerProtocolStrictDecoder(t *testing.T) {
	valid := marshalPostgreSQLOwnerProtocolConfig(t, validPostgreSQLOwnerProtocolConfig())
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown root field", raw: addPostgreSQLProtocolRootField(t, valid, "unknown", json.RawMessage(`true`))},
		{name: "unknown nested field", raw: addPostgreSQLProtocolNestedField(t, valid, "transaction", "unknown", json.RawMessage(`true`))},
		{name: "duplicate root field", raw: append([]byte(`{"schema_version":"heteroci-postgresql-local-authorities-protocol/v1",`), valid[1:]...)},
		{name: "null", raw: []byte(`null`)},
		{name: "trailing", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "not canonical", raw: append([]byte("\n"), valid...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if decoded, err := DecodePostgreSQLOwnerProtocolConfig(test.raw); err == nil {
				t.Fatalf("DecodePostgreSQLOwnerProtocolConfig accepted %#v", decoded)
			}
		})
	}
}

// TestPostgreSQLOwnerProtocolFieldGuard 动态枚举生产字段并证明任一字段缺失都会 fail-fast。
func TestPostgreSQLOwnerProtocolFieldGuard(t *testing.T) {
	config := validPostgreSQLOwnerProtocolConfig()
	encoded := marshalPostgreSQLOwnerProtocolConfig(t, config)
	producerPaths := postgreSQLProtocolJSONLeafPaths(reflect.TypeOf(config), "")
	if len(producerPaths) == 0 {
		t.Fatal("protocol producer field enumeration returned no fields")
	}
	for _, fieldPath := range producerPaths {
		t.Run(strings.ReplaceAll(fieldPath, ".", "_"), func(t *testing.T) {
			withoutField := removePostgreSQLProtocolField(t, encoded, strings.Split(fieldPath, "."))
			if decoded, err := DecodePostgreSQLOwnerProtocolConfig(withoutField); err == nil {
				t.Fatalf("decoder accepted missing producer field %s: %#v", fieldPath, decoded)
			}
		})
	}
}

func validPostgreSQLOwnerProtocolConfig() PostgreSQLOwnerProtocolConfig {
	return PostgreSQLOwnerProtocolConfig{
		SchemaVersion: PostgreSQLOwnerProtocolSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Status:        heteroci.ResultStatusNotVerified,
		Placement:     PostgreSQLOwnerPlacementRemotePrimaryOnly,
		Owner: PostgreSQLAuthorityBinding{
			Schema:               PostgreSQLOwnerSchema,
			Role:                 PostgreSQLOwnerRole,
			TransactionAuthority: PostgreSQLTransactionAuthorityIndependent,
		},
		Receipt: PostgreSQLAuthorityBinding{
			Schema:               PostgreSQLReceiptSchema,
			Role:                 PostgreSQLReceiptRole,
			TransactionAuthority: PostgreSQLTransactionAuthorityIndependent,
		},
		Failover: PostgreSQLOwnerFailoverConfig{
			Mode:                     PostgreSQLOwnerFailoverManualFencedPromotion,
			DatabaseNodes:            2,
			WitnessVoters:            0,
			OldPrimaryFencing:        PostgreSQLOwnerOldPrimaryFencingRequired,
			AutomaticTwoNodeFailover: PostgreSQLOwnerPolicyForbidden,
		},
		Transaction: PostgreSQLOwnerTransactionConfig{
			ReadPolicy:                PostgreSQLOwnerReadPrimaryOnly,
			SynchronousCommit:         PostgreSQLOwnerSynchronousCommitRemoteApply,
			AsyncDowngrade:            PostgreSQLOwnerPolicyForbidden,
			IsolationLevel:            PostgreSQLOwnerIsolationSerializable,
			ScopeSerialization:        PostgreSQLOwnerScopeTransactionAdvisoryLock,
			CrossAuthorityTransaction: PostgreSQLOwnerPolicyForbidden,
		},
		Recovery: PostgreSQLOwnerRecoveryConfig{
			AmbiguousCommit:        PostgreSQLOwnerAmbiguousCommitExactReplay,
			ReceiptAmbiguousCommit: PostgreSQLReceiptAmbiguousCommitExactReplay,
			PromotionEpoch:         PostgreSQLOwnerPromotionAcquireNewEpoch,
		},
		Boundary: PostgreSQLOwnerBoundaryConfig{
			OwnerSQLite:          PostgreSQLOwnerPolicyForbidden,
			ReceiptSQLite:        PostgreSQLOwnerPolicyForbidden,
			LocalReplicaRead:     PostgreSQLOwnerPolicyForbidden,
			LocalReplicaMutation: PostgreSQLOwnerPolicyForbidden,
			SQLiteFallback:       PostgreSQLOwnerPolicyForbidden,
			DualWrite:            PostgreSQLOwnerPolicyForbidden,
			NetworkFailure:       PostgreSQLNetworkFailureFailFast,
			RemoteDurationLedger: PostgreSQLOwnerPolicyForbidden,
			RuntimeWiring:        PostgreSQLOwnerRuntimeNotWired,
		},
	}
}

func marshalPostgreSQLOwnerProtocolConfig(t *testing.T, config PostgreSQLOwnerProtocolConfig) []byte {
	t.Helper()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal PostgreSQL owner protocol config: %v", err)
	}
	return encoded
}

func postgreSQLProtocolJSONLeafPaths(typ reflect.Type, prefix string) []string {
	var paths []string
	for index := range typ.NumField() {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if field.Type.Kind() == reflect.Struct {
			paths = append(paths, postgreSQLProtocolJSONLeafPaths(field.Type, path)...)
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func removePostgreSQLProtocolField(t *testing.T, encoded []byte, path []string) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatalf("decode protocol fixture: %v", err)
	}
	cursor := root
	for _, component := range path[:len(path)-1] {
		next, ok := cursor[component].(map[string]any)
		if !ok {
			t.Fatalf("field path %v does not resolve through %s", path, component)
		}
		cursor = next
	}
	delete(cursor, path[len(path)-1])
	mutated, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal protocol fixture without %v: %v", path, err)
	}
	return mutated
}

func addPostgreSQLProtocolRootField(t *testing.T, encoded []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatalf("decode protocol root: %v", err)
	}
	root[name] = value
	mutated, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal protocol root: %v", err)
	}
	return mutated
}

func addPostgreSQLProtocolNestedField(t *testing.T, encoded []byte, objectName, fieldName string, value json.RawMessage) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatalf("decode protocol root: %v", err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(root[objectName], &nested); err != nil {
		t.Fatalf("decode protocol object %s: %v", objectName, err)
	}
	nested[fieldName] = value
	updated, err := json.Marshal(nested)
	if err != nil {
		t.Fatalf("marshal protocol object %s: %v", objectName, err)
	}
	root[objectName] = updated
	mutated, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal protocol root: %v", err)
	}
	return mutated
}
