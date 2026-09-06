package securitytime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Observe 记录一次有效时钟样本并返回新的 high-water 快照；异常会持久化熔断。
func (o *ClockOwner) Observe(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.observeLocked(ctx)
}

// Allocate 推进 owner 并分配同一 boot 内的 monotonic lease；非法 TTL 直接拒绝。
func (o *ClockOwner) Allocate(ctx context.Context, ttl time.Duration) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	if ttl <= 0 {
		return Snapshot{}, fmt.Errorf("security time allocate: positive ttl required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	snapshot, err := o.observeLocked(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	ttlMS := ttl.Milliseconds()
	if ttlMS <= 0 {
		return Snapshot{}, fmt.Errorf("security time allocate: ttl below millisecond precision")
	}
	snapshot.IssuedAtMS = snapshot.WallHighWaterMS
	snapshot.LeaseExpiresAtMS = snapshot.WallHighWaterMS + ttlMS
	state, err := o.store.Load(ctx, o.cfg.SecurityDomainID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.MonoIssuedNS = state.LastMonoNS
	snapshot.MonoDeadlineNS = state.LastMonoNS + ttl.Nanoseconds()
	return snapshot, nil
}

// Validate 用当前持久 owner 与时钟源复核 lease；boot 或 clock epoch 变化会拒绝旧快照。
func (o *ClockOwner) Validate(ctx context.Context, snapshot Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	state, err := o.store.Load(ctx, o.cfg.SecurityDomainID)
	if err != nil {
		return err
	}
	return o.validateLocked(ctx, state, snapshot)
}

// validateLocked 在 owner 锁内完成 snapshot、样本和 expiry 的联合校验。
func (o *ClockOwner) validateLocked(ctx context.Context, state State, snapshot Snapshot) error {
	if state.StickyUnavailable {
		return ErrUnavailable
	}
	if err := validateIdentity(o.cfg, state, snapshot); err != nil {
		return err
	}
	sample, err := o.clock.Sample(ctx)
	if err != nil {
		return fmt.Errorf("security time validate sample: %w", err)
	}
	if err := validateLeaseSample(state, sample, o.cfg.MaxSkewMS); err != nil {
		return o.fenceLocked(ctx, state, err)
	}
	if sample.MonoNS > snapshot.MonoDeadlineNS || sample.WallMS > snapshot.LeaseExpiresAtMS {
		return ErrSnapshotExpired
	}
	return nil
}

// validateLeaseSample 校验 lease 验证期间的 freshness 与当前时钟顺序。
func validateLeaseSample(state State, sample Sample, maxSkewMS int64) error {
	if err := validateSample(sample); err != nil {
		return err
	}
	return validateCurrentSample(state, sample, maxSkewMS)
}

// observeLocked 在 owner 锁内读取样本、推进状态并执行持久 CAS。
func (o *ClockOwner) observeLocked(ctx context.Context) (Snapshot, error) {
	state, err := o.store.Load(ctx, o.cfg.SecurityDomainID)
	if err != nil && !isNotFound(err) {
		return Snapshot{}, err
	}
	sample, sampleErr := o.clock.Sample(ctx)
	if sampleErr != nil {
		return Snapshot{}, fmt.Errorf("security time observe sample: %w", sampleErr)
	}
	if err := validateSample(sample); err != nil {
		return o.fenceOrReturn(ctx, state, err)
	}
	if isNotFound(err) {
		return o.initializeLocked(ctx, sample)
	}
	if state.StickyUnavailable {
		return Snapshot{}, ErrUnavailable
	}
	if err := validateStateIdentity(o.cfg, state); err != nil {
		return Snapshot{}, err
	}
	if err := o.advanceState(ctx, &state, sample); err != nil {
		return Snapshot{}, err
	}
	if err := o.store.CompareAndSwap(ctx, o.cfg.SecurityDomainID, state.Version-1, state); err != nil {
		return Snapshot{}, staleOrStoreError(err)
	}
	return snapshotFromState(state), nil
}

// initializeLocked 以首个样本建立持久 owner；boot epoch 不匹配直接拒绝。
func (o *ClockOwner) initializeLocked(ctx context.Context, sample Sample) (Snapshot, error) {
	if sample.BootEpoch != o.cfg.BootEpoch {
		return Snapshot{}, ErrInvalidSample
	}
	state := State{Version: 1, SecurityDomainID: o.cfg.SecurityDomainID, LineageDigest: o.cfg.LineageDigest,
		SourceID: sample.SourceID, BootEpoch: sample.BootEpoch, ClockEpoch: 1, Generation: 1, Revision: 1,
		WallHighWaterMS: sample.WallMS, LastWallMS: sample.WallMS, LastMonoNS: sample.MonoNS, MaxSkewMS: o.cfg.MaxSkewMS}
	if err := o.store.CompareAndSwap(ctx, o.cfg.SecurityDomainID, 0, state); err != nil {
		return Snapshot{}, staleOrStoreError(err)
	}
	return snapshotFromState(state), nil
}

// advanceState 校验 source/epoch/偏差并推进安全时间版本。
func (o *ClockOwner) advanceState(ctx context.Context, state *State, sample Sample) error {
	if sample.SourceID != state.SourceID {
		return o.fenceLocked(ctx, *state, ErrInvalidSample)
	}
	if sample.BootEpoch != state.BootEpoch {
		if sample.WallMS < state.WallHighWaterMS {
			return o.fenceLocked(ctx, *state, ErrInvalidSample)
		}
		state.BootEpoch, state.ClockEpoch = sample.BootEpoch, state.ClockEpoch+1
		state.Generation++
		state.LastMonoNS, state.LastWallMS = sample.MonoNS, sample.WallMS
	}
	if err := validateCurrentSample(*state, sample, o.cfg.MaxSkewMS); err != nil {
		return o.fenceLocked(ctx, *state, err)
	}
	if sample.WallMS > state.WallHighWaterMS {
		state.WallHighWaterMS = sample.WallMS
	}
	state.LastWallMS, state.LastMonoNS = sample.WallMS, sample.MonoNS
	state.Revision++
	state.Version++
	return nil
}

func (o *ClockOwner) fenceOrReturn(ctx context.Context, state State, cause error) (Snapshot, error) {
	if state.SecurityDomainID == "" {
		return Snapshot{}, cause
	}
	return Snapshot{}, o.fenceLocked(ctx, state, cause)
}

// fenceLocked 把不可证明的时钟事实写成 sticky unavailable，并拒绝后续授权。
func (o *ClockOwner) fenceLocked(ctx context.Context, state State, cause error) error {
	if state.StickyUnavailable {
		return ErrUnavailable
	}
	state.StickyUnavailable = true
	state.ClockEpoch++
	state.Generation++
	state.Revision++
	state.Version++
	if err := o.store.CompareAndSwap(ctx, o.cfg.SecurityDomainID, state.Version-1, state); err != nil {
		return staleOrStoreError(err)
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, cause)
}

// snapshotFromState 将内部状态投影为不可变传输快照。
func snapshotFromState(state State) Snapshot {
	return Snapshot{SecurityDomainID: state.SecurityDomainID, LineageDigest: state.LineageDigest,
		SourceID: state.SourceID, BootEpoch: state.BootEpoch, ClockEpoch: state.ClockEpoch,
		Generation: state.Generation, Revision: state.Revision, WallHighWaterMS: state.WallHighWaterMS,
		MaxSkewMS: state.MaxSkewMS}
}

// validateCurrentSample 检查 monotonic 顺序以及 wall/mono 的最大偏差。
func validateCurrentSample(state State, sample Sample, maxSkewMS int64) error {
	if sample.MonoNS < state.LastMonoNS || sample.WallMS < state.LastWallMS-maxSkewMS {
		return ErrInvalidSample
	}
	wallDelta := sample.WallMS - state.LastWallMS
	monoDelta := (sample.MonoNS - state.LastMonoNS) / int64(time.Millisecond)
	if wallDelta-monoDelta > maxSkewMS {
		return ErrInvalidSample
	}
	return nil
}

// validateSample 拒绝缺失来源、非法 epoch、非正 wall 时间和未知 freshness。
func validateSample(sample Sample) error {
	if !validText(sample.SourceID, 128) || sample.BootEpoch <= 0 || sample.WallMS <= 0 || sample.MonoNS < 0 || !sample.Fresh {
		return ErrInvalidSample
	}
	return nil
}

// validateStateIdentity 校验持久状态仍属于当前 security domain/lineage。
func validateStateIdentity(cfg Config, state State) error {
	if state.SecurityDomainID != cfg.SecurityDomainID || state.LineageDigest != cfg.LineageDigest || !validText(state.SourceID, 128) {
		return ErrSnapshotMismatch
	}
	if !validStateVersions(state) || !validStateClock(state) {
		return ErrSnapshotMismatch
	}
	return nil
}

// validStateVersions 检查持久 owner 的版本和 epoch 必须为正。
func validStateVersions(state State) bool {
	return state.Version > 0 && state.BootEpoch > 0 && state.ClockEpoch > 0 && state.Generation > 0 && state.Revision > 0
}

// validStateClock 检查持久时钟 high-water、样本和 skew 配置完整。
func validStateClock(state State) bool {
	return state.WallHighWaterMS > 0 && state.LastWallMS > 0 && state.LastMonoNS >= 0 && state.MaxSkewMS > 0
}

// validateIdentity 校验快照的 domain、source、boot、clock 和 generation fence。
func validateIdentity(cfg Config, state State, snapshot Snapshot) error {
	if err := validateStateIdentity(cfg, state); err != nil {
		return err
	}
	if snapshot.SecurityDomainID != cfg.SecurityDomainID || snapshot.LineageDigest != cfg.LineageDigest ||
		snapshot.SourceID != state.SourceID || snapshot.BootEpoch != state.BootEpoch || snapshot.ClockEpoch != state.ClockEpoch ||
		snapshot.Generation != state.Generation {
		return ErrSnapshotMismatch
	}
	return nil
}

// validateSnapshot 拒绝空 expiry/deadline、非正版本和跨域快照。
func validateSnapshot(snapshot Snapshot) error {
	if !validSnapshotIdentity(snapshot) || !validSnapshotVersions(snapshot) || !validSnapshotExpiry(snapshot) {
		return ErrSnapshotMismatch
	}
	return nil
}

// validSnapshotIdentity 检查快照的域、lineage 和来源字段。
func validSnapshotIdentity(snapshot Snapshot) bool {
	return validText(snapshot.SecurityDomainID, 256) && validText(snapshot.LineageDigest, 512) && validText(snapshot.SourceID, 128)
}

// validSnapshotVersions 检查快照 epoch、generation、revision 和 skew。
func validSnapshotVersions(snapshot Snapshot) bool {
	return snapshot.BootEpoch > 0 && snapshot.ClockEpoch > 0 && snapshot.Generation > 0 && snapshot.Revision > 0 && snapshot.MaxSkewMS > 0
}

// validSnapshotExpiry 检查 wall/monotonic expiry 必须严格晚于签发点。
func validSnapshotExpiry(snapshot Snapshot) bool {
	return snapshot.WallHighWaterMS > 0 && snapshot.IssuedAtMS > 0 && snapshot.LeaseExpiresAtMS > snapshot.IssuedAtMS &&
		snapshot.MonoIssuedNS >= 0 && snapshot.MonoDeadlineNS > snapshot.MonoIssuedNS
}

// validText 检查外部标识长度、空白和控制字符。
func validText(value string, max int) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > max {
		return false
	}
	for _, char := range trimmed {
		if char == 0 || char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	return ctx.Err()
}

func isNotFound(err error) bool {
	return err != nil && (err == ErrStateNotFound || errorsIs(err, ErrStateNotFound))
}
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
func staleOrStoreError(err error) error {
	if err == ErrStaleWriter || errorsIs(err, ErrStaleWriter) {
		return ErrStaleWriter
	}
	return err
}
