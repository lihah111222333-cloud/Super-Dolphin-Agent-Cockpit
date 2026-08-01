package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
)

const productionCurrentReuseSnapshotAttempts = 3

// reuseProductionCurrentDuringUpdate 只在其他进程持有更新锁时复用稳定且已验证的 current。
// state 与 current 均通过 rename 原子发布；state 先发布时，旧 current 必须是 state 绑定的上一代。
func reuseProductionCurrentDuringUpdate(
	ctx context.Context,
	session productionSelfUpdateSession,
	run productionSelfUpdateDepsRun,
) error {
	metadata := productionSelfUpdateState{
		Remote: session.root.RemoteURL, TrustedRef: session.root.TrustedRef,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	previous := filepath.Join(filepath.Dir(session.current), productionPreviousGateCLI)
	for range productionCurrentReuseSnapshotAttempts {
		if err := verifyProductionCurrentCLI(session.current); err != nil {
			return err
		}
		before, err := productionBinaryDigest(session.current)
		if err != nil {
			return fmt.Errorf("digest current production gate CLI: %w", err)
		}
		if err := verifyOptionalProductionPrevious(previous); err != nil {
			return err
		}
		state, err := loadProductionCurrentUpdateState(
			session.statePath,
			before,
			session.config.BootstrapControllerFile,
		)
		if err != nil {
			return fmt.Errorf("load production update state: %w", err)
		}
		if state != nil {
			if err := validateProductionCurrentStateMetadata(*state, metadata); err != nil {
				return err
			}
			if before == state.PreviousBinaryDigest && before != state.BinaryDigest {
				if _, err := verifyInterruptedProductionSwitch(
					ctx, session.current, session.config.BootstrapControllerFile, before, *state, run,
				); err != nil {
					return err
				}
			} else if err := verifyProductionCurrentStateIdentity(ctx, session.current, before, *state, run); err != nil {
				return err
			}
		}
		after, err := productionBinaryDigest(session.current)
		if err != nil {
			return fmt.Errorf("redigest current production gate CLI: %w", err)
		}
		if before == after {
			return nil
		}
	}
	return errors.New("production current CLI changed while verifying update reuse")
}

// inspectProductionCurrentHeadState 验证已安装 CLI，并在可信提交未变时跳过编译闭包和 Go 探测。
func inspectProductionCurrentHeadState(
	ctx context.Context,
	session productionSelfUpdateSession,
	head productionSelfUpdateHead,
	run productionSelfUpdateDepsRun,
) (*productionSelfUpdateState, bool, error) {
	currentDigest, err := productionBinaryDigest(session.current)
	if err != nil {
		return nil, false, fmt.Errorf("digest current production gate CLI: %w", err)
	}
	state, err := loadProductionCurrentUpdateState(
		session.statePath,
		currentDigest,
		session.config.BootstrapControllerFile,
	)
	if err != nil || state == nil {
		return state, false, err
	}
	metadata := productionSelfUpdateState{
		Remote: session.root.RemoteURL, TrustedRef: session.root.TrustedRef,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if err := validateProductionCurrentStateMetadata(*state, metadata); err != nil {
		return nil, false, err
	}
	if currentDigest == state.PreviousBinaryDigest && currentDigest != state.BinaryDigest {
		previous, verifyErr := verifyInterruptedProductionSwitch(
			ctx,
			session.current,
			session.config.BootstrapControllerFile,
			currentDigest,
			*state,
			run,
		)
		if verifyErr != nil {
			return nil, false, verifyErr
		}
		if err := validateProductionSelfUpdateProgress(ctx, session, head.commit, previous, run); err != nil {
			return nil, false, err
		}
		return previous, false, nil
	}
	if err := verifyProductionCurrentStateIdentity(
		ctx, session.current, currentDigest, *state, run,
	); err != nil {
		return nil, false, err
	}
	if err := validateProductionSelfUpdateProgress(ctx, session, head.commit, state, run); err != nil {
		return nil, false, err
	}
	if state.Commit != head.commit {
		return state, false, nil
	}
	if state.Tree != head.tree {
		return nil, false, errors.New("production update state tree does not match its immutable commit")
	}
	return state, true, nil
}
