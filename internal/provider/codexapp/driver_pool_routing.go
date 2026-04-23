package codexapp

import (
	"context"
	"errors"
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
//  1. Pool not wired OR feature flag disabled -> no options (legacy path).
//  2. req.Config cannot be parsed into a valid CodexIdentity -> no
//     options + an error-free fallthrough. This matters because
//     pre-P21 StartSession requests don't set codexHome; surfacing
//     the missing field as a fatal error would break every existing
//     caller the moment the flag flips.
//  3. Valid identity + available pool -> Acquire a SpawnedServer and
//     attach its URL + release to the session via withPoolServer.
//     ErrPoolExhausted / ErrSpawnBackoff are surfaced to the caller
//     so backpressure is visible at the StartSession seam.
func (d *driver) resolveSessionOptions(ctx context.Context, req dto.StartSessionRequest) ([]sessionOption, error) {
	if d == nil || d.pool == nil || !poolRoutingEnabled() {
		return nil, nil
	}
	identity, err := providershared.ResolveCodexIdentity(req.Config)
	if err != nil {
		// Identity not present / incomplete. Fall back to legacy
		// path so single-instance deployments that never opted into
		// multi-provider Codex stay unaffected.
		if d.logger != nil {
			d.logger.Debug("codexapp: pool routing skipped",
				slog.String("agent_id", strings.TrimSpace(req.AgentID)),
				slog.String("reason", err.Error()),
			)
		}
		return nil, nil
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
