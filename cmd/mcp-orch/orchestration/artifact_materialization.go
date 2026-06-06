package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

const artifactUpdatedBy = "dag-artifact"

type artifactMaterialization struct {
	Params sharedfilestore.ImportLocalFileParams
}

func agentNodeUsesArtifactResult(rawConfig json.RawMessage) bool {
	cfg, err := nodeexec.ParseAgentConfig(rawConfig)
	return err == nil && cfg != nil && cfg.Outputs.ToArtifact != nil
}

func prepareArtifactTurnCompletedResult(node *taskdag.Node, target *nodeexec.ArtifactTarget, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	plan, err := nodeexec.BuildArtifactImportPlan(target, rawResult, taskNodeRunID(node))
	if err != nil {
		return turnOutputMaterialization{}, validationMaterializationFailure("outputs.to_artifact: " + err.Error())
	}
	params := sharedfilestore.ImportLocalFileParams{SourcePath: plan.SourcePath, TargetPath: plan.TargetPath, ContentType: plan.ContentType, AllowedExtensions: plan.AllowedExtensions, AllowedSourceRoots: plan.AllowedSourceRoots, MaxBytes: plan.MaxBytes, Overwrite: plan.Overwrite, UpdatedBy: artifactUpdatedBy}
	return turnOutputMaterialization{Result: encodeSharedfileResultRef(plan.TargetPath), Artifact: &artifactMaterialization{Params: params}}, nil
}

func materializeArtifactAfterClaim(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, materialized turnOutputMaterialization) (json.RawMessage, bool) {
	if materialized.Artifact == nil {
		return materialized.Result, true
	}
	if deps.ArtifactImporter == nil {
		handleMaterializationFailure(ctx, deps, logger, node, infrastructureMaterializationFailure("outputs.to_artifact: ArtifactImporter not wired"))
		return nil, false
	}
	if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, materialized.Result) {
		return nil, false
	}
	if _, err := deps.ArtifactImporter.ImportLocalFile(ctx, materialized.Artifact.Params); err != nil {
		handleMaterializationFailure(ctx, deps, logger, node, artifactImportFailure(materialized.Artifact.Params.TargetPath, err))
		return nil, false
	}
	return materialized.Result, true
}

func artifactImportFailure(targetPath string, err error) *turnOutputMaterializationFailure {
	reason := "outputs.to_artifact[" + targetPath + "]: " + err.Error()
	if errors.Is(err, sharedfilestore.ErrImportValidation) {
		return validationMaterializationFailure(reason)
	}
	return infrastructureMaterializationFailure(reason)
}
