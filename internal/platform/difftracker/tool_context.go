package difftracker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type toolCallContext struct {
	ThreadID  string
	Arguments json.RawMessage
	Snapshot  *Snapshot
}

type toolCallContextKey struct{}

type lspEditArgs struct {
	Action   string `json:"action"`
	FilePath string `json:"file_path"`
}

func WithToolCallContext(ctx context.Context, threadID string, arguments json.RawMessage, snapshot *Snapshot) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	copied := append(json.RawMessage(nil), arguments...)
	return context.WithValue(ctx, toolCallContextKey{}, toolCallContext{
		ThreadID:  strings.TrimSpace(threadID),
		Arguments: copied,
		Snapshot:  snapshot,
	})
}

func readToolCallContext(ctx context.Context) toolCallContext {
	if ctx == nil {
		return toolCallContext{}
	}
	value, _ := ctx.Value(toolCallContextKey{}).(toolCallContext)
	return value
}

func buildHookMergeRequest(
	ctx context.Context,
	agentID, callID, toolName string,
	result any,
	resolver WorkDirResolver,
	meta toolCallContext,
) (*MergeRequest, error) {
	args, ok, err := decodeReplaceRangeArgs(meta.Arguments)
	if err != nil || !ok {
		return nil, err
	}
	resultRaw, err := rawToolResult(result)
	if err != nil {
		return nil, err
	}
	patch, files, err := ExtractPatchFromReplaceRange(resultRaw)
	if err != nil {
		return nil, err
	}
	filePath, err := resolveDiffPath(ctx, resolver, agentID, args.FilePath)
	if err != nil {
		return nil, err
	}
	files = mergeHookFiles(files, filePath)
	patch = ensureUnifiedHeaders(patch, files)
	repoRoot, err := resolveRepoRoot(ctx, resolver, agentID, meta)
	if err != nil {
		return nil, err
	}
	return &MergeRequest{
		AgentID:  strings.TrimSpace(agentID),
		ThreadID: meta.ThreadID,
		CallID:   strings.TrimSpace(callID),
		ToolName: strings.TrimSpace(toolName),
		RepoRoot: repoRoot,
		DiffText: patch,
	}, nil
}

func decodeReplaceRangeArgs(arguments json.RawMessage) (lspEditArgs, bool, error) {
	if len(arguments) == 0 {
		return lspEditArgs{}, false, nil
	}
	var args lspEditArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return lspEditArgs{}, false, err
	}
	if strings.TrimSpace(args.Action) != "replace_range" {
		return lspEditArgs{}, false, nil
	}
	return args, true, nil
}

func rawToolResult(result any) (json.RawMessage, error) {
	if raw, ok := result.(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...), nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func mergeHookFiles(files []string, fallback string) []string {
	merged := append([]string{}, files...)
	if fallback != "" {
		merged = append(merged, fallback)
	}
	return uniqueSorted(merged)
}

func buildGitMergeRequest(agentID, callID, toolName string, meta toolCallContext) *MergeRequest {
	if meta.Snapshot == nil {
		return nil
	}
	diffText, _, err := EmitGitDiff(context.Background(), meta.Snapshot)
	if err != nil || strings.TrimSpace(diffText) == "" {
		return nil
	}
	return &MergeRequest{
		AgentID:  strings.TrimSpace(agentID),
		ThreadID: meta.ThreadID,
		CallID:   strings.TrimSpace(callID),
		ToolName: strings.TrimSpace(toolName),
		RepoRoot: meta.Snapshot.RepoRoot,
		DiffText: diffText,
	}
}

func resolveDiffPath(ctx context.Context, resolver WorkDirResolver, agentID, rawPath string) (string, error) {
	path, err := normalizeResolvedPath(rawPath)
	if err != nil {
		return "", err
	}
	if useRawDiffPath(resolver, agentID, path) {
		return path, nil
	}
	cwd, err := resolveAgentCWD(ctx, resolver, agentID)
	if err != nil {
		return "", err
	}
	return relativeDiffPath(path, cwd), nil
}

func normalizeResolvedPath(rawPath string) (string, error) {
	path := normalizeDiffPath(filepath.Clean(strings.TrimSpace(rawPath)))
	if path == "" {
		return "", errors.New("difftracker: empty file path")
	}
	return path, nil
}

func useRawDiffPath(resolver WorkDirResolver, agentID, path string) bool {
	return resolver == nil || strings.TrimSpace(agentID) == "" || !filepath.IsAbs(path)
}

func resolveAgentCWD(ctx context.Context, resolver WorkDirResolver, agentID string) (string, error) {
	cwd, err := resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(cwd)), nil
}

func relativeDiffPath(path, cwd string) string {
	if cwd == "" || cwd == "." {
		return normalizeDiffPath(path)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || outsideBaseDir(rel) {
		return normalizeDiffPath(path)
	}
	return normalizeDiffPath(rel)
}

func outsideBaseDir(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveRepoRoot(ctx context.Context, resolver WorkDirResolver, agentID string, meta toolCallContext) (string, error) {
	if meta.Snapshot != nil {
		return meta.Snapshot.RepoRoot, nil
	}
	if resolver == nil || strings.TrimSpace(agentID) == "" {
		return "", nil
	}
	cwd, err := resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return "", err
	}
	root, err := FindGitRoot(ctx, cwd)
	if err != nil {
		return "", nil
	}
	return root, nil
}
