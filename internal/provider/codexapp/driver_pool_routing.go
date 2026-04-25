package codexapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// poolRoutingEnvVar is the binary-level override for ServerPool routing.
// When unset, routing runs in auto mode: valid codex identity uses the pool,
// while missing identity keeps the legacy ServerManager path for old bindings.
// Explicit true is fail-closed, explicit false disables the pool.
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
//  3. Explicit true + invalid identity -> fail closed. StartSession must not
//     silently fall back to the shared app-server after strict opt-in.
//  4. Auto mode + invalid identity -> warn and use legacy path. This keeps old
//     persisted bindings resumable while new starts carry explicit identity.
//  5. Valid identity + available pool -> Acquire a SpawnedServer and
//     attach its URL + release to the session via withPoolServer.
//     ErrSpawnBackoff is surfaced to the caller so retry pressure is
//     visible at the StartSession seam.
func (d *driver) resolveSessionOptions(ctx context.Context, req dto.StartSessionRequest) ([]sessionOption, error) {
	if d == nil || d.pool == nil {
		return []sessionOption(nil), nil
	}
	identity, err := providershared.ResolveCodexIdentity(req.Config)
	enabled, strict := poolRoutingDecision()
	if !enabled {
		if err != nil {
			d.warnLegacyIdentityFallback(req.AgentID, err)
		}
		return []sessionOption(nil), nil
	}
	if err != nil {
		if !strict {
			d.warnLegacyIdentityFallback(req.AgentID, err)
			return []sessionOption(nil), nil
		}
		return nil, fmt.Errorf("codex identity required: %w", err)
	}
	owner := strings.TrimSpace(req.AgentID)
	server, release, acquireErr := d.pool.Acquire(ctx, identity, owner)
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
			slog.String("server_url", url),
		)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func (d *driver) resolveResumeOptions(ctx context.Context, req dto.ResumeSessionRequest) ([]sessionOption, error) {
	if d == nil || d.pool == nil {
		return []sessionOption(nil), nil
	}
	enabled, strict := poolRoutingDecision()
	if !enabled {
		return []sessionOption(nil), nil
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
	server, release, err := d.pool.Acquire(ctx, identity, owner)
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

// poolRoutingEnabled parses the env override. Missing / empty means
// auto-enabled so valid identity uses the ServerPool by default. Parse errors
// are treated as disabled so a typo never silently turns the pool on.
func poolRoutingEnabled() bool {
	enabled, _ := poolRoutingDecision()
	return enabled
}

func poolRoutingDecision() (enabled bool, strict bool) {
	raw := strings.TrimSpace(os.Getenv(poolRoutingEnvVar))
	if raw == "" {
		return true, false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return enabled, enabled
}
