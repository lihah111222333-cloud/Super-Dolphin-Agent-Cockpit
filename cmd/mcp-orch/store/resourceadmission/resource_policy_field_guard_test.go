package resourceadmission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	_ "modernc.org/sqlite"
)

func testResourcePolicyFieldChain(t *testing.T) {
	request, snapshot := resourcePolicyFixture(t)
	createdAt := snapshot.CreatedAt
	requestProducers := resourceMergeFields(t,
		resourceStructFields(t, "request", request),
		resourceStructFields(t, "clock", struct{ CreatedAt time.Time }{CreatedAt: createdAt}),
	)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: requestProducers, consumers: resourceStructFields(t, "snapshot", snapshot),
		edges: resourcePolicyRequestSnapshotEdges(), producerExemptions: map[string]string{},
		consumerExemptions: resourcePolicySnapshotDerivedExemptions(),
	})

	params := policyCreateParams(snapshot)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: resourceStructFields(t, "snapshot", snapshot), consumers: resourceStructFields(t, "params", params),
		edges: resourcePolicySnapshotParamEdges(), producerExemptions: resourcePolicyParamProducerExemptions(),
		consumerExemptions: map[string]string{},
	})

	db, queries := resourceOpenSchema133(t)
	row, err := queries.CreateResourceEffectivePolicy(t.Context(), params)
	if err != nil {
		t.Fatalf("CreateResourceEffectivePolicy() error = %v", err)
	}
	resourceAssertPolicyParamRowChain(t, params, row)
	rebuilt, err := policyFromRow(row)
	if err != nil {
		t.Fatalf("policyFromRow() error = %v", err)
	}
	resourceAssertPolicyRowSnapshotChain(t, row, rebuilt)
	if !reflect.DeepEqual(rebuilt, snapshot) {
		t.Fatalf("policy SQL round trip changed snapshot:\n got=%#v\nwant=%#v", rebuilt, snapshot)
	}
	_ = db
}

func resourcePolicyFixture(t *testing.T) (PublishEffectivePolicyRequest, PolicySnapshot) {
	t.Helper()
	createdAt := time.Date(2026, 1, 2, 4, 5, 6, 700, time.UTC)
	request := PublishEffectivePolicyRequest{
		SchemaVersion: SchemaVersion, SnapshotID: "policy-snapshot-field-guard", LineageID: "policy-lineage-field-guard",
		Revision: 1, RequestedKind: RequestedKindSendMessage,
		CallerPrincipalRef: "principal://resource-field-guard", ScopeRef: "scope://resource-field-guard",
		SourcePolicySetRef: "policy-set://resource-field-guard", SourcePolicySetDigest: resourceTestDigest("source-policy-set"),
		PrecedenceTraceRef: "precedence://resource-field-guard", PrecedenceTraceDigest: resourceTestDigest("precedence"),
		EffectiveLimits: resourceVectorFromBase(101), ReleaseConditionDigest: resourceTestDigest("release-condition"),
		IdempotencyNamespace: "resource-policy-field-guard", IdempotencyKeyDigest: resourceTestDigest("policy-key"),
		PublishOperationID: "publish-operation-field-guard", PublishReceiptRef: "receipt://policy-field-guard",
		PolicyOwner: PolicyOwnerFence{OwnerID: "policy-owner-field-guard", OwnerGeneration: 211,
			WriterFenceRef: "fence://policy-field-guard", OwnerProofRef: "proof://policy-field-guard",
			DeploymentFenceRef: "deployment://policy-field-guard", SecurityTimeGeneration: 223},
		EffectiveAt: createdAt.Add(-time.Hour), ValidUntil: createdAt.Add(4 * time.Hour),
	}
	finalized, err := FinalizePublishEffectivePolicyRequest(request)
	if err != nil {
		t.Fatalf("FinalizePublishEffectivePolicyRequest() error = %v", err)
	}
	snapshot, err := newPolicySnapshot(finalized, createdAt)
	if err != nil {
		t.Fatalf("newPolicySnapshot() error = %v", err)
	}
	return finalized, snapshot
}

func resourcePolicyRequestSnapshotEdges() map[string]string {
	edges := map[string]string{
		"request.SchemaVersion": "snapshot.SchemaVersion", "request.SnapshotID": "snapshot.SnapshotID",
		"request.LineageID": "snapshot.LineageID", "request.Revision": "snapshot.Revision",
		"request.RequestedKind": "snapshot.RequestedKind", "request.CallerPrincipalRef": "snapshot.CallerPrincipalRef",
		"request.ScopeRef": "snapshot.ScopeRef", "request.PolicyApplicabilityDigest": "snapshot.PolicyApplicabilityDigest",
		"request.SourcePolicySetRef": "snapshot.SourcePolicySetRef", "request.SourcePolicySetDigest": "snapshot.SourcePolicySetDigest",
		"request.PrecedenceTraceRef": "snapshot.PrecedenceTraceRef", "request.PrecedenceTraceDigest": "snapshot.PrecedenceTraceDigest",
		"request.EffectiveLimitsDigest": "snapshot.EffectiveLimitsDigest", "request.CanonicalPolicyHash": "snapshot.CanonicalPolicyHash",
		"request.ReleaseConditionDigest": "snapshot.ReleaseConditionDigest",
		"request.IdempotencyNamespace":   "snapshot.IdempotencyNamespace", "request.IdempotencyKeyDigest": "snapshot.IdempotencyKeyDigest",
		"request.PublishRequestDigest": "snapshot.PublishRequestDigest", "request.PublishOperationID": "snapshot.PublishOperationID",
		"request.PublishOperationDigest": "snapshot.PublishOperationDigest", "request.PublishReceiptRef": "snapshot.PublishReceiptRef",
		"request.PolicyOwner.OwnerID":                "snapshot.PolicyOwner.OwnerID",
		"request.PolicyOwner.OwnerGeneration":        "snapshot.PolicyOwner.OwnerGeneration",
		"request.PolicyOwner.WriterFenceRef":         "snapshot.PolicyOwner.WriterFenceRef",
		"request.PolicyOwner.OwnerProofRef":          "snapshot.PolicyOwner.OwnerProofRef",
		"request.PolicyOwner.DeploymentFenceRef":     "snapshot.PolicyOwner.DeploymentFenceRef",
		"request.PolicyOwner.SecurityTimeGeneration": "snapshot.PolicyOwner.SecurityTimeGeneration",
		"request.EffectiveAt":                        "snapshot.EffectiveAt", "request.ValidUntil": "snapshot.ValidUntil",
		"clock.CreatedAt": "snapshot.CreatedAt",
	}
	resourceAddVectorEdges(edges, "request.EffectiveLimits", "snapshot.EffectiveLimits")
	return edges
}

func resourcePolicySnapshotDerivedExemptions() map[string]string {
	return map[string]string{
		"snapshot.DimensionSet":            "contract owner: the RESOURCE v1 dimension discriminator is fixed by this package",
		"snapshot.CanonicalizationProfile": "digest owner: the policy canonicalization profile is a fixed domain discriminator",
		"snapshot.PolicySnapshotDigest":    "digest owner: FinalizePolicySnapshot derives the self digest after projection",
		"snapshot.PublishReceiptDigest":    "receipt owner: FinalizePolicySnapshot derives the receipt digest after the snapshot digest",
	}
}

func resourcePolicySnapshotParamEdges() map[string]string {
	edges := map[string]string{
		"snapshot.SnapshotID": "params.PolicySnapshotID", "snapshot.LineageID": "params.PolicyLineageID",
		"snapshot.Revision": "params.PolicyRevision", "snapshot.CallerPrincipalRef": "params.CallerPrincipalRef",
		"snapshot.ScopeRef": "params.ScopeRef", "snapshot.PolicyApplicabilityDigest": "params.PolicyApplicabilityDigest",
		"snapshot.SourcePolicySetRef": "params.SourcePolicySetRef", "snapshot.SourcePolicySetDigest": "params.SourcePolicySetDigest",
		"snapshot.PrecedenceTraceRef": "params.PrecedenceTraceRef", "snapshot.PrecedenceTraceDigest": "params.PrecedenceTraceDigest",
		"snapshot.CanonicalPolicyHash": "params.CanonicalPolicyHash", "snapshot.PolicySnapshotDigest": "params.PolicySnapshotDigest",
		"snapshot.EffectiveLimitsDigest":  "params.EffectiveLimitsDigest",
		"snapshot.ReleaseConditionDigest": "params.ReleaseConditionDigest",
		"snapshot.IdempotencyNamespace":   "params.IdempotencyNamespace", "snapshot.IdempotencyKeyDigest": "params.IdempotencyKeyDigest",
		"snapshot.PublishRequestDigest": "params.PublishRequestDigest", "snapshot.PublishOperationID": "params.PublishOperationID",
		"snapshot.PublishOperationDigest": "params.PublishOperationDigest", "snapshot.PublishReceiptRef": "params.PublishReceiptRef",
		"snapshot.PublishReceiptDigest": "params.PublishReceiptDigest", "snapshot.PolicyOwner.OwnerID": "params.PolicyOwnerID",
		"snapshot.PolicyOwner.OwnerGeneration":        "params.PolicyOwnerGeneration",
		"snapshot.PolicyOwner.WriterFenceRef":         "params.PolicyWriterFenceRef",
		"snapshot.PolicyOwner.OwnerProofRef":          "params.PolicyOwnerProofRef",
		"snapshot.PolicyOwner.DeploymentFenceRef":     "params.DeploymentFenceRef",
		"snapshot.PolicyOwner.SecurityTimeGeneration": "params.SecurityTimeGeneration",
		"snapshot.EffectiveAt":                        "params.EffectiveAt", "snapshot.ValidUntil": "params.ValidUntil",
		"snapshot.CreatedAt": "params.CreatedAt",
	}
	resourceAddSQLVectorEdges(edges, "snapshot.EffectiveLimits", "params.Limit")
	return edges
}

func resourcePolicyParamProducerExemptions() map[string]string {
	return map[string]string{
		"snapshot.SchemaVersion":           "SQL owner: CreateResourceEffectivePolicy fixes schema_version to version one",
		"snapshot.RequestedKind":           "SQL owner: CreateResourceEffectivePolicy fixes requested_kind to send_message",
		"snapshot.DimensionSet":            "SQL owner: CreateResourceEffectivePolicy fixes the RESOURCE dimension set",
		"snapshot.CanonicalizationProfile": "SQL owner: CreateResourceEffectivePolicy fixes the policy canonicalization profile",
	}
}

func resourceAssertPolicyParamRowChain(t *testing.T, params storedb.CreateResourceEffectivePolicyParams, row storedb.OrchestrationResourceEffectivePolicySnapshot) {
	t.Helper()
	producers := resourceStructFields(t, "params", params)
	consumers := resourceStructFields(t, "row", row)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: producers, consumers: consumers,
		edges: resourceSameNameEdges(producers, consumers, "params", "row"), producerExemptions: map[string]string{},
		consumerExemptions: map[string]string{
			"row.RequestedKind":           "SQL owner: the INSERT returns its fixed send_message discriminator",
			"row.DimensionSet":            "SQL owner: the INSERT returns its fixed RESOURCE dimension discriminator",
			"row.CanonicalizationProfile": "SQL owner: the INSERT returns its fixed canonicalization discriminator",
			"row.SchemaVersion":           "SQL owner: the INSERT returns its fixed schema version one",
		},
	})
}

func resourceAssertPolicyRowSnapshotChain(t *testing.T, row storedb.OrchestrationResourceEffectivePolicySnapshot, snapshot PolicySnapshot) {
	t.Helper()
	edges := make(map[string]string)
	for snapshotPath, paramPath := range resourcePolicySnapshotParamEdges() {
		edges["row."+paramPath[len("params."):]] = snapshotPath
	}
	edges["row.RequestedKind"] = "snapshot.RequestedKind"
	edges["row.DimensionSet"] = "snapshot.DimensionSet"
	edges["row.CanonicalizationProfile"] = "snapshot.CanonicalizationProfile"
	edges["row.SchemaVersion"] = "snapshot.SchemaVersion"
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: resourceStructFields(t, "row", row), consumers: resourceStructFields(t, "snapshot", snapshot),
		edges: edges, producerExemptions: map[string]string{}, consumerExemptions: map[string]string{},
	})
}

func resourceAddVectorEdges(edges map[string]string, source, target string) {
	for _, field := range []string{"AgentCount", "InFlightCount", "FanoutCount", "ConcurrencyCount", "WallTimeMS", "InputTokens", "OutputTokens", "CostMicrounits", "ContentBytes", "IngressBytes"} {
		edges[source+"."+field] = target + "." + field
	}
}

func resourceAddSQLVectorEdges(edges map[string]string, source, target string) {
	names := map[string]string{
		"AgentCount": "AgentCount", "InFlightCount": "InFlightCount", "FanoutCount": "FanoutCount",
		"ConcurrencyCount": "ConcurrencyCount", "WallTimeMS": "WallTimeMs", "InputTokens": "InputTokens",
		"OutputTokens": "OutputTokens", "CostMicrounits": "CostMicrounit", "ContentBytes": "ContentBytes",
		"IngressBytes": "IngressBytes",
	}
	for sourceName, targetName := range names {
		edges[source+"."+sourceName] = target + targetName
	}
}

func resourceVectorFromBase(base int64) ResourceVector {
	return ResourceVector{AgentCount: base, InFlightCount: base + 1, FanoutCount: base + 2,
		ConcurrencyCount: base + 3, WallTimeMS: base + 4, InputTokens: base + 5,
		OutputTokens: base + 6, CostMicrounits: base + 7, ContentBytes: base + 8, IngressBytes: base + 9}
}

func resourceTestDigest(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func resourceOpenSchema133(t *testing.T) (*sql.DB, *storedb.Queries) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve resource field guard path")
	}
	migrationPath := filepath.Join(filepath.Dir(sourceFile), "../../../../internal/platform/db/sqlite/migrations/133_orchestration_resource_reserved.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read schema 133: %v", err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open resource field guard database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close resource field guard database: %v", closeErr)
		}
	})
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable fixture foreign keys: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(migration)); err != nil {
		t.Fatalf("apply schema 133: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER trg_orchestration_resource_reservation_exact_relation"); err != nil {
		t.Fatalf("drop out-of-scope relation trigger from mapper fixture: %v", err)
	}
	return db, storedb.New(db)
}
