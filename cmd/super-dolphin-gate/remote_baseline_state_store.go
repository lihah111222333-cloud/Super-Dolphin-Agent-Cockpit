package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

var errRemoteBaselineStateNotFound = gatecontract.ErrRemoteBaselineStateNotFound

type remoteBaselineStoredState struct {
	state      remoteci.BaselineState
	generation uint64
}

func remoteBaselineDatabasePath(path string) string { return path }

func remoteBaselineStatePath(configPath, configuredPath string) string {
	if strings.TrimSpace(configuredPath) != "" {
		return configuredPath
	}
	return filepath.Join(filepath.Dir(configPath), "remote-oci-baseline-state.json")
}

// normalizeRemoteSQLiteAuthority 将远程基准状态和耗时账本收敛到同一个 SQLite 真相源。
func normalizeRemoteSQLiteAuthority(configPath string, statePath *string, ledgerPath *string) error {
	resolvedState := remoteBaselineStatePath(configPath, *statePath)
	extension := filepath.Ext(resolvedState)
	authorityPath := strings.TrimSuffix(resolvedState, extension) + ".sqlite"
	if strings.TrimSpace(*ledgerPath) != "" {
		given, err := filepath.Abs(*ledgerPath)
		if err != nil {
			return protocolError("resolve remote SQLite authority: %v", err)
		}
		want, err := filepath.Abs(authorityPath)
		if err != nil {
			return protocolError("resolve remote SQLite authority: %v", err)
		}
		if filepath.Clean(given) != filepath.Clean(want) {
			return protocolError("remote baseline state and duration ledger must use the same SQLite authority %q", want)
		}
	}
	*statePath = resolvedState
	*ledgerPath = authorityPath
	return nil
}

func baselineLedger(path string) (*gatecontract.DurationLedgerStore, error) {
	return gatecontract.NewDurationLedgerStore(path)
}
func loadStoredRemoteBaselineState(path string) (remoteBaselineStoredState, error) {
	store, err := baselineLedger(path)
	if err != nil {
		return remoteBaselineStoredState{}, err
	}
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		return remoteBaselineStoredState{}, err
	}
	digest := sha256.Sum256(record.StateJSON)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(record.StateSHA256)) != 1 {
		return remoteBaselineStoredState{}, errors.New("remote baseline SQLite state digest is invalid")
	}
	var state remoteci.BaselineState
	if err := gatecontract.DecodeStrictJSON(record.StateJSON, &state); err != nil {
		return remoteBaselineStoredState{}, err
	}
	if err := state.Validate(); err != nil {
		return remoteBaselineStoredState{}, err
	}
	return remoteBaselineStoredState{state: state, generation: record.Generation}, nil
}

func storeRemoteBaselineState(path string, stored remoteBaselineStoredState) error {
	store, err := baselineLedger(path)
	if err != nil {
		return err
	}
	expected := stored.generation
	payload, err := jsonMarshal(stored.state)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	_, err = store.CompareAndSwapRemoteBaselineState(expected, gatecontract.RemoteBaselineStateRecord{Generation: stored.state.Generation, StateJSON: payload, StateSHA256: hex.EncodeToString(digest[:])})
	return err
}
func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }
func loadRemoteBaselineState(path string, allowMissing bool) (remoteci.BaselineState, error) {
	stored, err := loadStoredRemoteBaselineState(path)
	if errors.Is(err, errRemoteBaselineStateNotFound) && allowMissing {
		return remoteci.BaselineState{}, nil
	}
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	return stored.state, nil
}

// loadAcceptedRemoteBaseline reads the accepted OCI-only baseline from the
// normalized SQLite authority and rejects legacy DataCache state.
func loadAcceptedRemoteBaseline(configPath, statePath, ledgerPath string) (remoteci.BaselineState, error) {
	if strings.TrimSpace(ledgerPath) == "" {
		return remoteci.BaselineState{}, errors.New("remote OCI baseline state store is required")
	}
	if strings.TrimSpace(statePath) != "" && remoteBaselineStatePath(configPath, statePath) != statePath {
		return remoteci.BaselineState{}, errors.New("remote OCI baseline state path is invalid")
	}
	state, err := loadRemoteBaselineState(remoteBaselineDatabasePath(ledgerPath), false)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	if state.OCIProjectCache == nil {
		return remoteci.BaselineState{}, errors.New("accepted baseline must use OCI project cache")
	}
	return state, state.OCIProjectCache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage)
}

func writeRemoteBaselineState(path string, state remoteci.BaselineState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	previous, err := loadStoredRemoteBaselineState(path)
	if errors.Is(err, errRemoteBaselineStateNotFound) || errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	return storeRemoteBaselineState(path, remoteBaselineStoredState{state: state, generation: previous.generation})
}
