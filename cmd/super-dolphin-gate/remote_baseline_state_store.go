package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteBaselineStoredState struct {
	state remoteci.BaselineState
}

func remoteBaselineSQLiteAuthorityPath(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".baseline-state.sqlite"
}

// normalizeRemoteSQLiteAuthority 仅接受远程基准与时长账本共享的 SQLite 真相源。
func normalizeRemoteSQLiteAuthority(configPath string, ledgerPath *string) error {
	authorityPath := remoteBaselineSQLiteAuthorityPath(configPath)
	if strings.TrimSpace(*ledgerPath) == "" {
		*ledgerPath = authorityPath
		return nil
	}
	given, err := filepath.Abs(*ledgerPath)
	if err != nil {
		return protocolError("resolve remote SQLite authority: %v", err)
	}
	want, err := filepath.Abs(authorityPath)
	if err != nil {
		return protocolError("resolve remote SQLite authority: %v", err)
	}
	if filepath.Clean(given) != filepath.Clean(want) {
		return protocolError("remote baseline and duration ledger must use the SQLite authority %q", want)
	}
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
	return remoteBaselineStoredState{state: state}, nil
}

func loadRemoteBaselineState(path string) (remoteci.BaselineState, error) {
	stored, err := loadStoredRemoteBaselineState(path)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	return stored.state, nil
}

// loadAcceptedRemoteBaseline reads the accepted OCI-only baseline from the
// normalized SQLite authority.
func loadAcceptedRemoteBaseline(ledgerPath string) (remoteci.BaselineState, error) {
	if strings.TrimSpace(ledgerPath) == "" {
		return remoteci.BaselineState{}, errors.New("remote OCI baseline state store is required")
	}
	state, err := loadRemoteBaselineState(ledgerPath)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	if state.OCIProjectCache == nil {
		return remoteci.BaselineState{}, errors.New("accepted baseline must use OCI project cache")
	}
	return state, state.OCIProjectCache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage)
}
