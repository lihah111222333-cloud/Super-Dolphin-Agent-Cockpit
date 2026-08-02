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
	legacy     *remoteLegacyBaselineMigration
	generation uint64
}

func remoteBaselineDatabasePath(path string) string { return path }

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
	if len(record.StateJSON) != 0 {
		digest := sha256.Sum256(record.StateJSON)
		if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(record.StateSHA256)) != 1 {
			return remoteBaselineStoredState{}, errors.New("remote baseline SQLite state digest is invalid")
		}
		var state remoteci.BaselineState
		if err := decodeRemoteBaselineRefreshJSON(record.StateJSON, &state); err != nil {
			return remoteBaselineStoredState{}, err
		}
		if err := state.Validate(); err != nil {
			return remoteBaselineStoredState{}, err
		}
		return remoteBaselineStoredState{state: state, generation: record.Generation}, nil
	}
	var marker remoteBaselineLegacyMarker
	if err := decodeRemoteBaselineRefreshJSON(record.LegacyJSON, &marker); err != nil {
		return remoteBaselineStoredState{}, err
	}
	return remoteBaselineStoredState{legacy: &remoteLegacyBaselineMigration{generation: marker.Generation, references: marker.References}, generation: record.Generation}, nil
}

type remoteBaselineLegacyMarker struct {
	Generation uint64                      `json:"generation"`
	References []remoteci.BaselineCacheRef `json:"references"`
}

func storeRemoteBaselineState(path string, stored remoteBaselineStoredState) error {
	store, err := baselineLedger(path)
	if err != nil {
		return err
	}
	expected := stored.generation
	if stored.legacy != nil {
		payload, err := jsonMarshal(remoteBaselineLegacyMarker{Generation: stored.legacy.generation, References: stored.legacy.references})
		if err != nil {
			return err
		}
		_, err = store.CompareAndSwapRemoteBaselineState(expected, gatecontract.RemoteBaselineStateRecord{Generation: stored.legacy.generation, LegacyJSON: payload})
		return err
	}
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
	if stored.legacy != nil {
		return remoteci.BaselineState{}, errRemoteBaselineStateNotFound
	}
	return stored.state, nil
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
