package metrics

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// CronRecoveryFinalizeConflictTotal exposes fenced recovery finalization
	// conflicts. These are expected only under concurrent/stale recovery races.
	CronRecoveryFinalizeConflictTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "cron_recovery_finalize_conflict_total",
			Help: "Number of cron recovery finalization conflicts detected by fenced writes.",
		},
		func() float64 { return float64(cronmetrics.Read().RecoveryFinalizeConflictTotal) },
	)

	// CronRecoveryFinalizeErrorTotal exposes recovery finalization errors that
	// could not be accepted as an idempotent terminal-state completion.
	CronRecoveryFinalizeErrorTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "cron_recovery_finalize_error_total",
			Help: "Number of cron recovery finalization errors after conflict reconciliation.",
		},
		func() float64 { return float64(cronmetrics.Read().RecoveryFinalizeErrorTotal) },
	)
)
