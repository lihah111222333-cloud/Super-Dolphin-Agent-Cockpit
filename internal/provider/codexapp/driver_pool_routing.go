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

// poolRoutingEnvVar is the binary-level feature flag that opts
// StartSession into the P21 ServerPool. When unset / falsy the
// driver keeps the legacy ServerManager path exactly as before, so
// deployments upgrade in two steps: ship the code first, flip the
// flag once a real codex run is verified.
const poolRoutingEnvVar = "CODEXAPP_USE_POOL"

// resolveSessionOptions is called by StartSession to decide whether
// the new session should connect through the ServerPool (P21
// multi-provider Codex) or fall back to the legacy ServerManager
// shared-instance URL.
//
// Routing rules (most-specific first):
//
//  1. Pool not wired -> no options (legacy path).
//  2. Feature flag disabled -> no options (legacy path); identity
//     parse errors are warned so compatibility fallbacks remain visible.
//  3. Feature flag enabled + invalid identity -> fail closed. StartSession
//     must not silently fall back to the shared app-server after opt-in.
//  4. Valid identity + available pool -> Acquire a SpawnedServer and
//     attach its URL + release to the session via withPoolServer.
//     ErrPoolExhausted / ErrSpawnBackoff are surfaced to the caller
//     so backpressure is visible at the StartSession seam.
func (d *driver) resolveSessionOptions(ctx context.Context, req dto.StartSessionRequest) ([]sessionOption, error) {
	if d == nil || d.pool == nil {
		return []sessionOption(nil), nil
	}
	identity, err := providershared.ResolveCodexIdentity(req.Config)
	if !poolRoutingEnabled() {
		if err != nil {
			d.warnLegacyIdentityFallback(req.AgentID, err)
		}
		return []sessionOption(nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("codex identity required: %w", err)
	}
	server, release, acquireErr := d.pool.Acquire(ctx, identity)
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
			slog.String("server_url", url),
		)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func (d *driver) resolveResumeOptions(ctx context.Context, req dto.ResumeSessionRequest) ([]sessionOption, error) {
	if d == nil || d.pool == nil || !poolRoutingEnabled() {
		return []sessionOption(nil), nil
	}
	identity, ok := resumeIdentity(req)
	if !ok {
		return nil, errors.New("codex identity required for resume")
	}
	server, release, err := d.pool.Acquire(ctx, identity)
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

// poolRoutingEnabled parses the env flag. Missing / empty / falsy
// all mean "stay on the legacy ServerManager path". Parse errors are
// treated as disabled so a typo never silently turns the pool on.
func poolRoutingEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(poolRoutingEnvVar))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}
