package config

import "time"

const (
	TurnTimeout            = 10 * time.Minute
	LaunchTimeout          = 30 * time.Second
	ShutdownTimeout        = 15 * time.Second
	HealthCheckPeriod      = 5 * time.Second
	StallDetectDelay       = 90 * time.Second
	DBQueryTimeout         = 10 * time.Second
	RPCRequestTimeout      = 30 * time.Second
	InterruptSettleTimeout = 6 * time.Second
)
