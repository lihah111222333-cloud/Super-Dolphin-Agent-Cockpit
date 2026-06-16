package kernel

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/repofingerprint"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	"github.com/anthropic-ai/super-agent-v3/internal/util/toolresults"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	// LaunchTimeout bounds provider launch operations.
	LaunchTimeout = ctxutil.LaunchTimeout
	// StartupTimeout bounds service startup operations.
	StartupTimeout = ctxutil.StartupTimeout
	// ShutdownTimeout bounds service shutdown operations.
	ShutdownTimeout = ctxutil.ShutdownTimeout
	// InitialThreadIDTimeout bounds initial thread identity lookup.
	InitialThreadIDTimeout = ctxutil.InitialThreadIDTimeout
	// SessionCloseTimeout bounds provider session close operations.
	SessionCloseTimeout = ctxutil.SessionCloseTimeout
	// HealthCheckPeriod is the default service health polling period.
	HealthCheckPeriod = ctxutil.HealthCheckPeriod
	// StallDetectDelay is the default turn stall detection delay.
	StallDetectDelay = ctxutil.StallDetectDelay
	// DBQueryTimeout bounds default database query operations.
	DBQueryTimeout = ctxutil.DBQueryTimeout
	// TxCleanupTimeout bounds transaction cleanup operations.
	TxCleanupTimeout = ctxutil.TxCleanupTimeout
	// RPCRequestTimeout bounds local RPC request handling.
	RPCRequestTimeout = ctxutil.RPCRequestTimeout
	// InterruptSettleTimeout gives interrupted turns time to settle.
	InterruptSettleTimeout = ctxutil.InterruptSettleTimeout
	// AsyncLaunchTimeout bounds async launch work.
	AsyncLaunchTimeout = ctxutil.AsyncLaunchTimeout
	// DreamConsolidationTimeout bounds Dream-backed consolidation.
	DreamConsolidationTimeout = ctxutil.DreamConsolidationTimeout
	// PromptIntentDraftTimeout bounds prompt intent draft generation.
	PromptIntentDraftTimeout = ctxutil.PromptIntentDraftTimeout
)

// WithTimeout returns a context with timeout unless timeout is non-positive.
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone returns a timeout context only when ctx has no deadline.
func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, timeout)
}

// WithInitialThreadIDTimeout applies the initial thread id timeout.
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithInitialThreadIDTimeout(ctx)
}

// WithSessionCloseTimeout applies the session close timeout.
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithSessionCloseTimeout(ctx)
}

// WithDBQueryTimeout applies the default database query timeout.
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithDBQueryTimeout(ctx)
}

// WithTxCleanupTimeout applies the transaction cleanup timeout.
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTxCleanupTimeout(ctx)
}

// WithRPCRequestTimeout applies the local RPC request timeout.
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithRPCRequestTimeout(ctx)
}

// WithPeerTimeout applies a peer operation timeout.
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithPeerTimeout(ctx, timeout)
}

// SafeGoContext launches fn with panic recovery and a stable log label.
func SafeGoContext(ctx context.Context, logger *pkglogger.Logger, label string, fn func(context.Context)) {
	safego.Go(ctx, logger, label, fn)
}

// MustComputeRepoFingerprint returns a repository fingerprint or an empty fallback.
func MustComputeRepoFingerprint(cwd string) string {
	return repofingerprint.MustCompute(cwd)
}

// ToolResultsCacheDir returns the persisted tool result cache directory.
func ToolResultsCacheDir() string {
	return toolresults.CacheDir()
}
