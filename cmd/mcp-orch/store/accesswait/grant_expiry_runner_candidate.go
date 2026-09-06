package accesswait

import (
	"context"
	"errors"
	"time"
)

// DefaultGrantExpiryRunnerInterval is deliberately conservative. It controls
// wake-up frequency only; grant eligibility always comes from the injected
// security clock and the bounded SQL candidate query.
const DefaultGrantExpiryRunnerInterval = 30 * time.Second

// GrantExpiryRunnerConfig controls one candidate pass and the runner period.
// Zero values receive bounded, deterministic defaults; negative values are
// rejected rather than silently converted into an unsafe scan or busy loop.
type GrantExpiryRunnerConfig struct {
	TickInterval time.Duration
	BatchLimit   int64
}

// GrantExpiryOwner is the narrow write-owner port used by the candidate
// runner. The runner must not reach into Store internals or acquire a second
// database/clock dependency; the owner keeps candidate selection and CAS
// lifecycle writes together.
type GrantExpiryOwner interface {
	ExpireDueGrants(context.Context, time.Time, int64) (int64, error)
}

// ConfigOrDefaults returns a config with finite defaults. No clock or database
// dependency is created here.
// ConfigOrDefaults 为 runner 配置补齐有限默认值，不创建隐式依赖。
func (c GrantExpiryRunnerConfig) ConfigOrDefaults() GrantExpiryRunnerConfig {
	out := c
	if out.TickInterval == 0 {
		out.TickInterval = DefaultGrantExpiryRunnerInterval
	}
	if out.BatchLimit == 0 {
		out.BatchLimit = DefaultGrantExpiryBatchLimit
	}
	return out
}

// GrantExpiryRunner is an explicit, candidate-only background runner. It is
// not registered in Fx or unified_v2: production needs an authenticated,
// durable security-time owner before this seam can be enabled.
type GrantExpiryRunner struct {
	owner         GrantExpiryOwner
	securityClock Clock
	cfg           GrantExpiryRunnerConfig
}

// NewGrantExpiryRunner constructs the runner only when both the AccessWait
// owner and an injected security clock are present. The clock is required even
// though Store has a legacy wall-clock compatibility default for older callers.
// NewGrantExpiryRunner 构造只依赖 owner 端口和显式安全时间的候选 runner。
func NewGrantExpiryRunner(owner GrantExpiryOwner, securityClock Clock, configs ...GrantExpiryRunnerConfig) (*GrantExpiryRunner, error) {
	if isNilInterface(owner) {
		return nil, errors.New("access grant expiry runner requires expiry owner")
	}
	if securityClock == nil {
		return nil, errors.New("access grant expiry runner requires security clock")
	}
	if len(configs) > 1 {
		return nil, errors.New("access grant expiry runner accepts at most one config")
	}
	cfg := GrantExpiryRunnerConfig{}.ConfigOrDefaults()
	if len(configs) == 1 {
		cfg = configs[0].ConfigOrDefaults()
	}
	if err := validateGrantExpiryRunnerConfig(cfg); err != nil {
		return nil, err
	}
	return &GrantExpiryRunner{owner: owner, securityClock: securityClock, cfg: cfg}, nil
}

// RunOnce obtains one security-time snapshot and executes one bounded expiry
// pass. It returns the number of grants this invocation won by CAS.
// RunOnce 获取一次安全时间快照并执行一个有界到期 CAS 批次。
func (r *GrantExpiryRunner) RunOnce(ctx context.Context) (int64, error) {
	if r == nil || isNilInterface(r.owner) || r.securityClock == nil {
		return 0, errors.New("access grant expiry runner dependencies are missing")
	}
	if err := validateGrantExpiryRunnerConfig(r.cfg); err != nil {
		return 0, err
	}
	if ctx == nil {
		return 0, errors.New("access grant expiry runner context is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := r.securityClock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.owner.ExpireDueGrants(ctx, now, r.cfg.BatchLimit)
}

// Run repeatedly executes bounded passes until ctx is cancelled. Any
// non-context error is returned to the owner; silently swallowing a failed
// expiry pass could leave active grants indefinitely usable.
// Run 持续执行有界到期批次，直到取消或遇到必须阻断的错误。
func (r *GrantExpiryRunner) Run(ctx context.Context) error {
	if r == nil || isNilInterface(r.owner) || r.securityClock == nil {
		return errors.New("access grant expiry runner dependencies are missing")
	}
	if err := validateGrantExpiryRunnerConfig(r.cfg); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("access grant expiry runner context is nil")
	}
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func validateGrantExpiryRunnerConfig(cfg GrantExpiryRunnerConfig) error {
	if cfg.TickInterval <= 0 {
		return errors.New("access grant expiry runner tick interval must be positive")
	}
	return validateGrantExpiryLimit(cfg.BatchLimit)
}
