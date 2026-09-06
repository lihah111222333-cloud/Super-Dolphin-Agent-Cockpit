package resourceadmission

import (
	"reflect"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func testResourceReservationFieldChain(t *testing.T) {
	_, policy := resourcePolicyFixture(t)
	request, authority, snapshot := resourceReservationFixture(t, policy)
	producers := resourceMergeFields(t,
		resourceStructFields(t, "request", request), resourceReservationAuthorityFields(authority),
	)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: producers, consumers: resourceStructFields(t, "snapshot", snapshot),
		edges:              resourceReservationInputSnapshotEdges(),
		producerExemptions: resourceReservationInputExemptions(),
		consumerExemptions: resourceReservationDerivedExemptions(),
	})

	params := reservationParams(snapshot)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: resourceStructFields(t, "snapshot", snapshot), consumers: resourceStructFields(t, "params", params),
		edges:              resourceReservationSnapshotParamEdges(),
		producerExemptions: resourceReservationParamProducerExemptions(), consumerExemptions: map[string]string{},
	})

	_, queries := resourceOpenSchema133(t)
	policyRow, err := queries.CreateResourceEffectivePolicy(t.Context(), policyCreateParams(policy))
	if err != nil {
		t.Fatalf("CreateResourceEffectivePolicy() for reservation chain error = %v", err)
	}
	row, err := queries.CreateResourceReservation(t.Context(), params)
	if err != nil {
		t.Fatalf("CreateResourceReservation() error = %v", err)
	}
	resourceAssertReservationParamRowChain(t, params, row)
	rebuilt, err := reservationFromRow(t.Context(), queries, row)
	if err != nil {
		t.Fatalf("reservationFromRow() error = %v", err)
	}
	resourceAssertReservationRowSnapshotChain(t, row, policyRow, rebuilt)
	if !reflect.DeepEqual(rebuilt, snapshot) {
		t.Fatalf("reservation SQL round trip changed snapshot:\n got=%#v\nwant=%#v", rebuilt, snapshot)
	}
}

func resourceReservationFixture(t *testing.T, policy PolicySnapshot) (ReserveRequest, reservationAuthority, ReservationSnapshot) {
	t.Helper()
	request := ReserveRequest{
		SchemaVersion: SchemaVersion, ReservationID: "reservation-field-guard",
		ResourceSnapshotID: "resource-snapshot-field-guard", CommandID: "request-command-selector",
		CommandVersion: 1, CommandProfileDigest: resourceTestDigest("request-command-profile"),
		IdentityFenceDigest:            resourceTestDigest("reservation-identity-fence"),
		ContentAdmissionSnapshotDigest: resourceTestDigest("content-admission"),
		Requested:                      resourceVectorFromBase(11), IdempotencyNamespace: "resource-reservation-field-guard",
		IdempotencyKeyDigest:   resourceTestDigest("reservation-key"),
		ReservationOperationID: "reservation-operation-field-guard",
		ReservationReceiptRef:  "receipt://reservation-field-guard",
		AdmissionOwner: AdmissionOwnerFence{OwnerID: "admission-owner-field-guard", OwnerGeneration: 311,
			WriterFenceRef: "fence://admission-field-guard", DeploymentFenceRef: "deployment://admission-field-guard",
			SecurityTimeGeneration: 313, OwnerProofRef: "proof://admission-field-guard"},
	}
	finalized, err := FinalizeReserveRequest(request)
	if err != nil {
		t.Fatalf("FinalizeReserveRequest() error = %v", err)
	}
	authority := reservationAuthority{
		command: storedb.OrchestrationCommandIntent{CommandID: "authority-command-field-guard", CommandVersion: 1,
			CallerPrincipalRef: "principal://reservation-field-guard", ScopeRef: "scope://reservation-field-guard"},
		profile: storedb.OrchestrationCommandIntentProfile{CompleteIntentDigest: resourceTestDigest("authority-command-profile")},
		source: storedb.OrchestrationCurrentBinding{AgentID: "source-agent-field-guard", BindingVersion: 317,
			SourceFenceRef: "fence://source-field-guard"},
		target: storedb.OrchestrationCurrentBinding{AgentID: "target-agent-field-guard", BindingVersion: 319,
			SourceFenceRef: "fence://target-field-guard"},
		content: storedb.OrchestrationContentRef{ReferenceID: "reference-field-guard", CanonicalRef: "content://field-guard",
			ObjectID: "object-field-guard", ObjectVersion: 1, RefVersion: 1,
			ProofSnapshotDigest: resourceTestDigest("content-owner-snapshot")},
		policy: policy, created: policy.CreatedAt.Add(10 * time.Minute), expires: policy.CreatedAt.Add(2 * time.Hour),
	}
	snapshot, err := newReservationSnapshot(finalized, authority)
	if err != nil {
		t.Fatalf("newReservationSnapshot() error = %v", err)
	}
	return finalized, authority, snapshot
}

func resourceReservationAuthorityFields(authority reservationAuthority) map[string]any {
	fields := map[string]any{
		"authority.command.CommandID":             authority.command.CommandID,
		"authority.command.CommandVersion":        authority.command.CommandVersion,
		"authority.command.CallerPrincipalRef":    authority.command.CallerPrincipalRef,
		"authority.command.ScopeRef":              authority.command.ScopeRef,
		"authority.profile.CompleteIntentDigest":  authority.profile.CompleteIntentDigest,
		"authority.source.AgentID":                authority.source.AgentID,
		"authority.source.BindingVersion":         authority.source.BindingVersion,
		"authority.source.SourceFenceRef":         authority.source.SourceFenceRef,
		"authority.target.AgentID":                authority.target.AgentID,
		"authority.target.BindingVersion":         authority.target.BindingVersion,
		"authority.target.SourceFenceRef":         authority.target.SourceFenceRef,
		"authority.content.ReferenceID":           authority.content.ReferenceID,
		"authority.content.CanonicalRef":          authority.content.CanonicalRef,
		"authority.content.ObjectID":              authority.content.ObjectID,
		"authority.content.ObjectVersion":         authority.content.ObjectVersion,
		"authority.content.RefVersion":            authority.content.RefVersion,
		"authority.content.ProofSnapshotDigest":   authority.content.ProofSnapshotDigest,
		"authority.policy.SnapshotID":             authority.policy.SnapshotID,
		"authority.policy.Revision":               authority.policy.Revision,
		"authority.policy.PolicySnapshotDigest":   authority.policy.PolicySnapshotDigest,
		"authority.policy.CanonicalPolicyHash":    authority.policy.CanonicalPolicyHash,
		"authority.policy.EffectiveLimitsDigest":  authority.policy.EffectiveLimitsDigest,
		"authority.policy.ReleaseConditionDigest": authority.policy.ReleaseConditionDigest,
		"authority.created":                       resourceComparableValue(authority.created),
		"authority.expires":                       resourceComparableValue(authority.expires),
	}
	vector := authority.policy.EffectiveLimits
	fields["authority.policy.EffectiveLimits.AgentCount"] = vector.AgentCount
	fields["authority.policy.EffectiveLimits.InFlightCount"] = vector.InFlightCount
	fields["authority.policy.EffectiveLimits.FanoutCount"] = vector.FanoutCount
	fields["authority.policy.EffectiveLimits.ConcurrencyCount"] = vector.ConcurrencyCount
	fields["authority.policy.EffectiveLimits.WallTimeMS"] = vector.WallTimeMS
	fields["authority.policy.EffectiveLimits.InputTokens"] = vector.InputTokens
	fields["authority.policy.EffectiveLimits.OutputTokens"] = vector.OutputTokens
	fields["authority.policy.EffectiveLimits.CostMicrounits"] = vector.CostMicrounits
	fields["authority.policy.EffectiveLimits.ContentBytes"] = vector.ContentBytes
	fields["authority.policy.EffectiveLimits.IngressBytes"] = vector.IngressBytes
	return fields
}

func resourceReservationInputSnapshotEdges() map[string]string {
	edges := map[string]string{
		"request.ReservationID": "snapshot.ReservationID", "request.ResourceSnapshotID": "snapshot.ResourceSnapshotID",
		"request.IdentityFenceDigest":                   "snapshot.IdentityFenceDigest",
		"request.ContentAdmissionSnapshotDigest":        "snapshot.Content.AdmissionSnapshotDigest",
		"request.IdempotencyNamespace":                  "snapshot.IdempotencyNamespace",
		"request.IdempotencyKeyDigest":                  "snapshot.IdempotencyKeyDigest",
		"request.ReservationRequestDigest":              "snapshot.ReservationRequestDigest",
		"request.ReservationOperationID":                "snapshot.ReservationOperationID",
		"request.ReservationOperationDigest":            "snapshot.ReservationOperationDigest",
		"request.ReservationReceiptRef":                 "snapshot.ReservationReceiptRef",
		"request.AdmissionOwner.OwnerID":                "snapshot.AdmissionOwner.OwnerID",
		"request.AdmissionOwner.OwnerGeneration":        "snapshot.AdmissionOwner.OwnerGeneration",
		"request.AdmissionOwner.WriterFenceRef":         "snapshot.AdmissionOwner.WriterFenceRef",
		"request.AdmissionOwner.DeploymentFenceRef":     "snapshot.AdmissionOwner.DeploymentFenceRef",
		"request.AdmissionOwner.SecurityTimeGeneration": "snapshot.AdmissionOwner.SecurityTimeGeneration",
		"request.AdmissionOwner.OwnerProofRef":          "snapshot.AdmissionOwner.OwnerProofRef",
		"authority.command.CommandID":                   "snapshot.CommandID", "authority.command.CommandVersion": "snapshot.CommandVersion",
		"authority.command.CallerPrincipalRef":    "snapshot.CallerPrincipalRef",
		"authority.command.ScopeRef":              "snapshot.ScopeRef",
		"authority.profile.CompleteIntentDigest":  "snapshot.CommandProfileDigest",
		"authority.source.AgentID":                "snapshot.Source.AgentID",
		"authority.source.BindingVersion":         "snapshot.Source.BindingVersion",
		"authority.source.SourceFenceRef":         "snapshot.Source.FenceRef",
		"authority.target.AgentID":                "snapshot.Target.AgentID",
		"authority.target.BindingVersion":         "snapshot.Target.BindingVersion",
		"authority.target.SourceFenceRef":         "snapshot.Target.FenceRef",
		"authority.content.ReferenceID":           "snapshot.Content.ReferenceID",
		"authority.content.CanonicalRef":          "snapshot.Content.ContentRef",
		"authority.content.ObjectID":              "snapshot.Content.ObjectID",
		"authority.content.ObjectVersion":         "snapshot.Content.ObjectVersion",
		"authority.content.RefVersion":            "snapshot.Content.RefVersion",
		"authority.content.ProofSnapshotDigest":   "snapshot.Content.OwnerSnapshotDigest",
		"authority.policy.SnapshotID":             "snapshot.PolicySnapshotID",
		"authority.policy.Revision":               "snapshot.PolicyRevision",
		"authority.policy.PolicySnapshotDigest":   "snapshot.PolicySnapshotDigest",
		"authority.policy.CanonicalPolicyHash":    "snapshot.PolicyCanonicalHash",
		"authority.policy.EffectiveLimitsDigest":  "snapshot.EffectiveLimitsDigest",
		"authority.policy.ReleaseConditionDigest": "snapshot.ReleaseConditionDigest",
		"authority.created":                       "snapshot.CreatedAt", "authority.expires": "snapshot.ValidUntil",
	}
	resourceAddVectorEdges(edges, "request.Requested", "snapshot.Requested")
	resourceAddVectorEdges(edges, "authority.policy.EffectiveLimits", "snapshot.EffectiveLimits")
	return edges
}

func resourceReservationInputExemptions() map[string]string {
	return map[string]string{
		"request.SchemaVersion":        "contract owner: the constructor emits the package schema version after request validation",
		"request.CommandID":            "authority owner: command_id is a selector and the re-read command row supplies the snapshot value",
		"request.CommandVersion":       "authority owner: command_version is verified by the join before the command row supplies it",
		"request.CommandProfileDigest": "authority owner: the verified command profile row supplies the persisted digest",
	}
}

func resourceReservationDerivedExemptions() map[string]string {
	return map[string]string{
		"snapshot.SchemaVersion":            "contract owner: reserved snapshots always use the package schema version",
		"snapshot.ReservationVersion":       "contract owner: first immutable reservations always use version one",
		"snapshot.ResourceSnapshotDigest":   "digest owner: FinalizeReservationSnapshot derives the snapshot self digest",
		"snapshot.ResourceStatus":           "lifecycle owner: this slice can only create the non-authorizing reserved state",
		"snapshot.RequestedKind":            "command owner: RESOURCE v1 only accepts the send_message command kind",
		"snapshot.UsageDigestDomain":        "usage owner: reserved snapshots start at the fixed empty-ledger domain",
		"snapshot.UsageWatermark":           "usage owner: reserved snapshots start at the fixed zero watermark",
		"snapshot.UsageDigest":              "usage owner: reserved snapshots start at the fixed empty-ledger digest",
		"snapshot.ReservationReceiptDigest": "receipt owner: FinalizeReservationSnapshot derives the receipt after snapshot digesting",
	}
}

func resourceReservationSnapshotParamEdges() map[string]string {
	edges := map[string]string{
		"snapshot.ReservationID": "params.ReservationID", "snapshot.ResourceSnapshotID": "params.ResourceSnapshotID",
		"snapshot.ResourceSnapshotDigest": "params.ResourceSnapshotDigest", "snapshot.CommandID": "params.CommandID",
		"snapshot.CommandProfileDigest": "params.CommandProfileDigest",
		"snapshot.CallerPrincipalRef":   "params.CallerPrincipalRef", "snapshot.ScopeRef": "params.ScopeRef",
		"snapshot.Source.AgentID": "params.SourceAgentID", "snapshot.Source.BindingVersion": "params.SourceBindingVersion",
		"snapshot.Source.FenceRef": "params.SourceFenceRef", "snapshot.Target.AgentID": "params.TargetAgentID",
		"snapshot.Target.BindingVersion": "params.TargetBindingVersion", "snapshot.Target.FenceRef": "params.TargetFenceRef",
		"snapshot.IdentityFenceDigest": "params.IdentityFenceDigest",
		"snapshot.Content.ReferenceID": "params.ContentReferenceID", "snapshot.Content.ContentRef": "params.ContentRef",
		"snapshot.Content.ObjectID":                "params.ContentObjectID",
		"snapshot.Content.OwnerSnapshotDigest":     "params.ContentOwnerSnapshotDigest",
		"snapshot.Content.AdmissionSnapshotDigest": "params.ContentAdmissionSnapshotDigest",
		"snapshot.PolicySnapshotID":                "params.PolicySnapshotID", "snapshot.PolicyRevision": "params.PolicyRevision",
		"snapshot.PolicySnapshotDigest":                  "params.PolicySnapshotDigest",
		"snapshot.PolicyCanonicalHash":                   "params.PolicyCanonicalHash",
		"snapshot.EffectiveLimitsDigest":                 "params.EffectiveLimitsDigest",
		"snapshot.ReleaseConditionDigest":                "params.ReleaseConditionDigest",
		"snapshot.IdempotencyNamespace":                  "params.IdempotencyNamespace",
		"snapshot.IdempotencyKeyDigest":                  "params.IdempotencyKeyDigest",
		"snapshot.ReservationRequestDigest":              "params.ReservationRequestDigest",
		"snapshot.ReservationOperationID":                "params.ReservationOperationID",
		"snapshot.ReservationOperationDigest":            "params.ReservationOperationDigest",
		"snapshot.ReservationReceiptRef":                 "params.ReservationReceiptRef",
		"snapshot.ReservationReceiptDigest":              "params.ReservationReceiptDigest",
		"snapshot.AdmissionOwner.OwnerID":                "params.AdmissionOwnerID",
		"snapshot.AdmissionOwner.OwnerGeneration":        "params.AdmissionOwnerGeneration",
		"snapshot.AdmissionOwner.WriterFenceRef":         "params.AdmissionWriterFenceRef",
		"snapshot.AdmissionOwner.DeploymentFenceRef":     "params.AdmissionDeploymentFenceRef",
		"snapshot.AdmissionOwner.SecurityTimeGeneration": "params.SecurityTimeGeneration",
		"snapshot.AdmissionOwner.OwnerProofRef":          "params.OwnerProofRef",
		"snapshot.ValidUntil":                            "params.ValidUntil", "snapshot.CreatedAt": "params.CreatedAt",
	}
	resourceAddSQLVectorEdges(edges, "snapshot.Requested", "params.Reserved")
	return edges
}

func resourceReservationParamProducerExemptions() map[string]string {
	exemptions := map[string]string{
		"snapshot.SchemaVersion":         "SQL owner: CreateResourceReservation fixes schema_version to version one",
		"snapshot.ReservationVersion":    "SQL owner: CreateResourceReservation fixes reservation_version to version one",
		"snapshot.ResourceStatus":        "SQL owner: CreateResourceReservation fixes resource_status to reserved",
		"snapshot.RequestedKind":         "SQL owner: CreateResourceReservation fixes requested_kind to send_message",
		"snapshot.CommandVersion":        "SQL owner: CreateResourceReservation fixes command_version to version one",
		"snapshot.Content.ObjectVersion": "SQL owner: CreateResourceReservation fixes content_object_version to version one",
		"snapshot.Content.RefVersion":    "SQL owner: CreateResourceReservation fixes content_ref_version to version one",
		"snapshot.UsageDigestDomain":     "SQL owner: CreateResourceReservation fixes the empty usage domain",
		"snapshot.UsageWatermark":        "SQL owner: CreateResourceReservation fixes the zero usage watermark",
		"snapshot.UsageDigest":           "SQL owner: CreateResourceReservation fixes the empty usage digest",
	}
	for _, field := range []string{"AgentCount", "InFlightCount", "FanoutCount", "ConcurrencyCount", "WallTimeMS", "InputTokens", "OutputTokens", "CostMicrounits", "ContentBytes", "IngressBytes"} {
		exemptions["snapshot.EffectiveLimits."+field] = "policy owner: effective values are reconstructed from the immutable policy join"
	}
	return exemptions
}

func resourceAssertReservationParamRowChain(t *testing.T, params storedb.CreateResourceReservationParams, row storedb.OrchestrationResourceReservation) {
	t.Helper()
	producers := resourceStructFields(t, "params", params)
	consumers := resourceStructFields(t, "row", row)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: producers, consumers: consumers,
		edges: resourceSameNameEdges(producers, consumers, "params", "row"), producerExemptions: map[string]string{},
		consumerExemptions: resourceReservationRowLiteralExemptions(),
	})
}

func resourceReservationRowLiteralExemptions() map[string]string {
	return map[string]string{
		"row.ReservationVersion":   "SQL owner: the INSERT returns its fixed reservation version one",
		"row.ResourceStatus":       "SQL owner: the INSERT returns its fixed reserved state",
		"row.RequestedKind":        "SQL owner: the INSERT returns its fixed send_message discriminator",
		"row.CommandVersion":       "SQL owner: the INSERT returns its fixed command version one",
		"row.ContentObjectVersion": "SQL owner: the INSERT returns its fixed content object version one",
		"row.ContentRefVersion":    "SQL owner: the INSERT returns its fixed content reference version one",
		"row.UsageDigestDomain":    "SQL owner: the INSERT returns its fixed empty-ledger domain",
		"row.UsageWatermark":       "SQL owner: the INSERT returns its fixed zero usage watermark",
		"row.UsageDigest":          "SQL owner: the INSERT returns its fixed empty-ledger digest",
		"row.SchemaVersion":        "SQL owner: the INSERT returns its fixed schema version one",
	}
}

func resourceAssertReservationRowSnapshotChain(t *testing.T, row storedb.OrchestrationResourceReservation, policy storedb.OrchestrationResourceEffectivePolicySnapshot, snapshot ReservationSnapshot) {
	t.Helper()
	edges := make(map[string]string)
	for snapshotPath, paramPath := range resourceReservationSnapshotParamEdges() {
		edges["row."+paramPath[len("params."):]] = snapshotPath
	}
	for field, target := range map[string]string{
		"ReservationVersion": "ReservationVersion", "ResourceStatus": "ResourceStatus",
		"RequestedKind": "RequestedKind", "CommandVersion": "CommandVersion",
		"ContentObjectVersion": "Content.ObjectVersion", "ContentRefVersion": "Content.RefVersion",
		"UsageDigestDomain": "UsageDigestDomain", "UsageWatermark": "UsageWatermark",
		"UsageDigest": "UsageDigest", "SchemaVersion": "SchemaVersion",
	} {
		edges["row."+field] = "snapshot." + target
	}
	resourceAddVectorEdges(edges, "policy", "snapshot.EffectiveLimits")
	producers := resourceMergeFields(t,
		resourceStructFields(t, "row", row), resourceStructFields(t, "policy", effectiveVectorFromPolicyRow(policy)),
	)
	resourceAssertFieldChain(t, resourceFieldChain{
		producers: producers, consumers: resourceStructFields(t, "snapshot", snapshot), edges: edges,
		producerExemptions: map[string]string{}, consumerExemptions: map[string]string{},
	})
}
