package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// poolRoutingEnvVar is the binary-level override for ServerPool routing.
// When unset, routing is enabled and fail-closed: valid codex identity uses the
// pool, while missing identity errors instead of falling back to the legacy
// ServerManager. Explicit false disables the pool for legacy deployments.
const poolRoutingEnvVar = "CODEXAPP_USE_POOL"

// resolveSessionOptions is called by StartSession to decide whether
// the new session should connect through the ServerPool (P21
// multi-provider Codex) or fall back to the legacy ServerManager
// shared-instance URL.
//
// Routing rules (most-specific first):
//
//  1. Pool not wired -> no options (legacy path).
//  2. Pool explicitly disabled -> no options (legacy path); identity
//     parse errors are warned so compatibility fallbacks remain visible.
//  3. Pool enabled + invalid identity -> fail closed. StartSession must not
//     silently fall back to the shared app-server.
//  4. Valid identity + available pool -> Acquire a SpawnedServer and
//     attach its URL + release to the session via withPoolServer.
//     ErrSpawnBackoff is surfaced to the caller so retry pressure is
//     visible at the StartSession seam.
func (d *driver) resolveSessionOptions(ctx context.Context, req dto.StartSessionRequest) ([]sessionOption, error) {
	policy := codexNativeToolPolicyFromConfig(req.Config)
	if d == nil || d.pool == nil {
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	identity, err := providershared.ResolveCodexIdentity(req.Config)
	enabled, strict := poolRoutingDecision()
	if !enabled {
		if err != nil {
			d.warnLegacyIdentityFallback(req.AgentID, err)
		}
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	if err != nil {
		if !strict {
			d.warnLegacyIdentityFallback(req.AgentID, err)
			return []sessionOption(nil), nil
		}
		return nil, fmt.Errorf("codex identity required: %w", err)
	}
	owner := strings.TrimSpace(req.AgentID)
	workDir := strings.TrimSpace(req.CWD)
	spawnCtx := withPoolSpawnNativeToolPolicy(withPoolSpawnWorkDir(ctx, workDir), policy)
	server, release, acquireErr := d.pool.Acquire(spawnCtx, identity, owner)
	if acquireErr != nil {
		return nil, acquireErr
	}
	url := strings.TrimSpace(server.ServerURL())
	if url == "" {
		release()
		return nil, errors.New("codexapp: pool returned server with empty URL")
	}
	if d.logger != nil {
		d.logger.Info("codexapp: start session via pool",
			slog.String("agent_id", strings.TrimSpace(req.AgentID)),
			slog.String("codex_home", identity.Home),
			slog.String("instance_key", identity.InstanceKey),
			slog.String("owner", owner),
			slog.String("work_dir", workDir),
			slog.String("server_url", url),
		)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func legacySessionOptionsForNativeToolPolicy(policy codexNativeToolPolicy) ([]sessionOption, error) {
	if policy.HasProcessFlags() {
		return nil, errors.New("codexapp: native tool policy requires pool-backed app-server")
	}
	return []sessionOption(nil), nil
}

func canonicalStartRuntimeConfig(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	out := make(map[string]any, len(config))
	maps.Copy(out, config)
	identity, err := providershared.ResolveCodexIdentity(config)
	if err != nil {
		return out
	}
	out["codexHome"] = identity.Home
	out["codexInstanceKey"] = identity.InstanceKey
	out["codexModelProvider"] = identity.ModelProvider
	return out
}

func (d *driver) resolveResumeOptions(ctx context.Context, req dto.ResumeSessionRequest) ([]sessionOption, error) {
	policy := codexNativeToolPolicyFromDisabled(req.CodexDisabledNativeTools)
	if d == nil || d.pool == nil {
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	enabled, strict := poolRoutingDecision()
	if !enabled {
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	identity, ok := resumeIdentity(req)
	if !ok {
		if !strict {
			d.warnLegacyIdentityFallback(req.AgentID, errors.New("codex identity required for resume"))
			return []sessionOption(nil), nil
		}
		return nil, errors.New("codex identity required for resume")
	}
	owner := strings.TrimSpace(req.AgentID)
	workDir := strings.TrimSpace(req.CWD)
	spawnCtx := withPoolSpawnNativeToolPolicy(withPoolSpawnWorkDir(ctx, workDir), policy)
	server, release, err := d.pool.Acquire(spawnCtx, identity, owner)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSpace(server.ServerURL())
	if url == "" {
		release()
		return nil, errors.New("codexapp: pool returned server with empty URL")
	}
	if d.logger != nil {
		d.logger.Info("codexapp: resume session via pool",
			slog.String("agent_id", strings.TrimSpace(req.AgentID)),
			slog.String("codex_home", identity.Home),
			slog.String("instance_key", identity.InstanceKey),
			slog.String("owner", owner),
			slog.String("work_dir", workDir),
			slog.String("server_url", url),
		)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func resumeIdentity(req dto.ResumeSessionRequest) (providershared.CodexIdentity, bool) {
	identity := providershared.CodexIdentity{
		Home:          strings.TrimSpace(req.CodexHome),
		InstanceKey:   strings.TrimSpace(req.CodexInstanceKey),
		ModelProvider: strings.TrimSpace(req.CodexModelProvider),
	}
	if identity.Home == "" || identity.InstanceKey == "" || identity.ModelProvider == "" {
		return providershared.CodexIdentity{}, false
	}
	return identity, true
}

func (d *driver) warnLegacyIdentityFallback(agentID string, err error) {
	if d == nil || d.logger == nil || err == nil {
		return
	}
	d.logger.Warn("codexapp: legacy shared app-server fallback after identity error",
		slog.String("agent_id", strings.TrimSpace(agentID)),
		slog.String("reason", err.Error()),
	)
}

// poolRoutingEnabled parses the env override. Missing / empty means enabled
// and strict so valid identity uses the ServerPool by default. Parse errors are
// treated as disabled so a typo never silently turns the pool on.
func poolRoutingEnabled() bool {
	enabled, _ := poolRoutingDecision()
	return enabled
}

func poolRoutingDecision() (enabled bool, strict bool) {
	raw := strings.TrimSpace(os.Getenv(poolRoutingEnvVar))
	if raw == "" {
		return true, true
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return enabled, enabled
}

const codexDisabledNativeToolsConfigKey = "codexDisabledNativeTools"

type codexNativeToolPolicy struct {
	contract.CodexNativeToolPolicy
}

func codexNativeToolPolicyFromConfig(cfg map[string]any) codexNativeToolPolicy {
	return codexNativeToolPolicy{CodexNativeToolPolicy: contract.NewCodexNativeToolPolicy(codexDisabledNativeToolIDs(cfg))}
}

func codexNativeToolPolicyFromDisabled(ids []string) codexNativeToolPolicy {
	return codexNativeToolPolicy{CodexNativeToolPolicy: contract.NewCodexNativeToolPolicy(cleanCodexNativeToolIDs(ids))}
}

func codexDisabledNativeToolIDs(cfg map[string]any) []string {
	if len(cfg) == 0 {
		return nil
	}
	return cleanCodexNativeToolIDs(rawStringList(cfg[codexDisabledNativeToolsConfigKey]))
}

func rawStringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		return stringListFromAny(v)
	case string:
		return []string{v}
	default:
		return nil
	}
}

func stringListFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		out = append(out, text)
	}
	return out
}

func cleanCodexNativeToolIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id != "" {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (p codexNativeToolPolicy) ApplyThreadStartParams(params *threadStartParams) {
	if params == nil || !p.RequiresReadOnlySandbox() {
		return
	}
	params.Sandbox = codexReadOnlySandbox(params.Sandbox)
	params.ApprovalPolicy = "never"
}

func (p codexNativeToolPolicy) ApplyThreadResumeParams(params *threadResumeParams) {
	if params == nil || !p.RequiresReadOnlySandbox() {
		return
	}
	params.Sandbox = "read-only"
	params.ApprovalPolicy = "never"
}

func applyResumeNativeToolRuntimePolicy(s *session, disabled []string) {
	if s == nil || !codexNativeToolPolicyFromDisabled(disabled).RequiresReadOnlySandbox() {
		return
	}
	s.setApprovalPolicy("never")
	s.setRuntimeConfigValue("approvalPolicy", "never")
	s.setRuntimeConfigValue("sandbox", "read-only")
}

func codexReadOnlySandbox(raw json.RawMessage) json.RawMessage {
	if codexSandboxIsReadOnly(raw) {
		return append(json.RawMessage(nil), raw...)
	}
	return json.RawMessage(`{"mode":"read-only"}`)
}

func codexSandboxIsReadOnly(raw json.RawMessage) bool {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.EqualFold(strings.TrimSpace(text), "read-only")
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	mode, _ := obj["mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "read-only")
}
