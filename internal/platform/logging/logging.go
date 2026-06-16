package logging

import pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

type (
	Logger  = pkglogger.Logger
	Attr    = pkglogger.Attr
	Sampler = pkglogger.Sampler
)

const (
	FieldError     = pkglogger.FieldError
	FieldLatencyMS = pkglogger.FieldLatencyMS
)

// Get returns the process logger.
func Get() *Logger { return pkglogger.Get() }

// Info writes an info log entry.
func Info(msg string, args ...any) { pkglogger.Info(msg, args...) }

// Warn writes a warning log entry.
func Warn(msg string, args ...any) { pkglogger.Warn(msg, args...) }

// Error writes an error log entry.
func Error(msg string, args ...any) { pkglogger.Error(msg, args...) }

// Debug writes a debug log entry.
func Debug(msg string, args ...any) { pkglogger.Debug(msg, args...) }

// CurrentLogFilePath returns the active log file path.
func CurrentLogFilePath() string { return pkglogger.CurrentLogFilePath() }

// String creates a string log field.
func String(key, value string) Attr { return pkglogger.String(key, value) }

// Int creates an int log field.
func Int(key string, value int) Attr { return pkglogger.Int(key, value) }

// Int64 creates an int64 log field.
func Int64(key string, value int64) Attr { return pkglogger.Int64(key, value) }

// NewEverySampler creates a sampler that emits every Nth hit per key.
func NewEverySampler(everyM int) *Sampler { return pkglogger.NewEverySampler(everyM) }
