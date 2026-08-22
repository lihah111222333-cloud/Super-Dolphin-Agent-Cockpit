package heterociowner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// validatePostgreSQLBindingArguments 校验 PG owner、scope fence 与 binding plan 的入口身份。
func validatePostgreSQLBindingArguments(owner *PostgreSQLOwner, mutation heteroci.ExecutorBindingMutation) error {
	if owner == nil || owner.db == nil {
		return fmt.Errorf("heteroci PostgreSQL owner is nil")
	}
	if err := mutation.Fence.Validate(); err != nil || mutation.Fence.Scope != owner.scope {
		return fmt.Errorf("heteroci PostgreSQL binding fence is invalid")
	}
	return validatePostgreSQLBindingPlan(mutation.Plan, mutation.Fence.OwnerEpoch)
}
func validatePostgreSQLBindingPlan(plan heteroci.ExecutorBindingPlan, epoch heteroci.RoutingOwnerEpoch) error {
	if err := validatePostgreSQLBindingIdentity(plan, epoch); err != nil {
		return err
	}
	if err := validatePostgreSQLBindingDigests(plan); err != nil {
		return err
	}
	return validatePostgreSQLBindingItems(plan.Bindings)
}
func validatePostgreSQLBindingIdentity(plan heteroci.ExecutorBindingPlan, epoch heteroci.RoutingOwnerEpoch) error {
	if plan.SchemaVersion != "remote-ci-executor-binding/v1" || plan.AuthorityGeneration != epoch {
		return fmt.Errorf("heteroci PostgreSQL binding plan schema or generation is invalid")
	}
	if err := plan.ExternalOperationID.Validate(); err != nil {
		return err
	}
	return plan.DurableRunID.Validate()
}
func validatePostgreSQLBindingDigests(plan heteroci.ExecutorBindingPlan) error {
	for _, digest := range []string{plan.RequestDigest, plan.ManifestDigest, plan.TopologyManifestDigest, plan.PlanDigest, plan.BindingDigest} {
		if !validPostgreSQLDigest(digest) {
			return fmt.Errorf("heteroci PostgreSQL binding digest is invalid")
		}
	}
	return nil
}
func validatePostgreSQLBindingItems(bindings []heteroci.ExecutorBinding) error {
	if len(bindings) == 0 {
		return fmt.Errorf("heteroci PostgreSQL binding plan is empty")
	}
	previous := ""
	for index, binding := range bindings {
		if binding.NodeID == "" || binding.ExecutorKind != heteroci.ExecutorKindLocalNode || len(binding.BatchIDs) == 0 || (index > 0 && binding.NodeID <= previous) {
			return fmt.Errorf("heteroci PostgreSQL binding node ordering is invalid")
		}
		previous = binding.NodeID
		if err := validatePostgreSQLBindingItem(binding); err != nil {
			return err
		}
	}
	return nil
}
func validatePostgreSQLBindingItem(binding heteroci.ExecutorBinding) error {
	if !validPostgreSQLDigest(binding.NodeIDDigest) || !validPostgreSQLDigest(binding.ShardDigest) || !validPostgreSQLDigest(binding.BatchSetDigest) || !validPostgreSQLDigest(binding.UnitSetDigest) || !validPostgreSQLDigest(binding.NodeFactsDigest) {
		return fmt.Errorf("heteroci PostgreSQL binding item digest is invalid")
	}
	for index, batchID := range binding.BatchIDs {
		if batchID == "" || (index > 0 && binding.BatchIDs[index-1] >= batchID) {
			return fmt.Errorf("heteroci PostgreSQL binding batch ordering is invalid")
		}
	}
	return nil
}
func validPostgreSQLDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
func validatePostgreSQLBindingAgainstPlan(ctx context.Context, queryer postgresqlOwnerQueryRower, mutation heteroci.ExecutorBindingMutation) error {
	planOperationID, err := heteroci.DeriveTopologyOperationID(mutation.Plan.DurableRunID, heteroci.TopologyOperationPhasePlan, "")
	if err != nil {
		return err
	}
	record, err := readPostgreSQLPlanRecord(ctx, queryer, mutation.Fence.Scope, planOperationID)
	if err != nil {
		return err
	}
	envelope, persisted, err := replayPersistedPostgreSQLPlan(record)
	if err != nil {
		return err
	}
	if persisted.OperationID != planOperationID || envelope.Request.RunIdentity.DurableRunID != mutation.Plan.DurableRunID || persisted.Fence.OwnerEpoch != mutation.Fence.OwnerEpoch {
		return fmt.Errorf("persisted topology plan identity drifted")
	}
	if err := validatePersistedPostgreSQLPlanDigests(envelope.Request.Manifest, persisted, mutation.Plan); err != nil {
		return err
	}
	return validatePostgreSQLBindingCoverage(mutation.Plan, persisted)
}
func readPostgreSQLPlanRecord(ctx context.Context, queryer postgresqlOwnerQueryRower, scope heteroci.RoutingOwnerScope, operationID heteroci.OperationID) (postgresqlOperationRecord, error) {
	record, found, err := readPostgreSQLOperation(ctx, queryer, scope, operationID)
	if err != nil {
		return postgresqlOperationRecord{}, err
	}
	if !found {
		return postgresqlOperationRecord{}, fmt.Errorf("persisted topology plan is missing")
	}
	return record, nil
}
func replayPersistedPostgreSQLPlan(record postgresqlOperationRecord) (postgresqlPlanEnvelope, heteroci.TopologyReservedPlan, error) {
	var envelope postgresqlPlanEnvelope
	if err := json.Unmarshal(record.RequestBytes, &envelope); err != nil {
		return postgresqlPlanEnvelope{}, heteroci.TopologyReservedPlan{}, fmt.Errorf("decode persisted topology plan request: %w", err)
	}
	persisted, err := replayPostgreSQLPlan(record, envelope.Session, envelope.Request, record.RequestBytes)
	if err != nil {
		return postgresqlPlanEnvelope{}, heteroci.TopologyReservedPlan{}, fmt.Errorf("replay persisted topology plan journal: %w", err)
	}
	return envelope, persisted, nil
}
func validatePersistedPostgreSQLPlanDigests(manifest heteroci.Manifest, persisted heteroci.TopologyReservedPlan, plan heteroci.ExecutorBindingPlan) error {
	manifestDigest, err := heteroci.DeriveTopologyManifestDigest(manifest)
	if err != nil || manifestDigest != plan.TopologyManifestDigest {
		return fmt.Errorf("persisted topology manifest digest drifted")
	}
	planDigest, err := heteroci.DeriveTopologyReservedPlanDigest(persisted)
	if err != nil || planDigest != plan.PlanDigest {
		return fmt.Errorf("persisted topology plan digest drifted")
	}
	return nil
}
func validatePostgreSQLBindingCoverage(plan heteroci.ExecutorBindingPlan, persisted heteroci.TopologyReservedPlan) error {
	return heteroci.ValidateExecutorBindingPlanAgainstReservedPlan(plan, persisted)
}
