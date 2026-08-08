package main

import (
	"errors"
	"flag"
	"io"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// runRemoteLedgerInit 只创建或校验本地 SQLite schema，不写 accepted baseline。
func runRemoteLedgerInit(args []string) error {
	flags := flag.NewFlagSet("remote init-ledger", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var ledgerPath string
	flags.StringVar(&ledgerPath, "ledger", "", "remote baseline and duration ledger SQLite authority path")
	if err := flags.Parse(args); err != nil {
		return protocolError("parse remote init-ledger flags: %v", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(ledgerPath) == "" {
		return protocolError("remote init-ledger requires --ledger and no positional arguments")
	}
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return protocolError("configure remote SQLite authority: %v", err)
	}
	_, err = store.LoadRemoteBaselineState()
	if err == nil || errors.Is(err, gatecontract.ErrRemoteBaselineStateNotFound) {
		return nil
	}
	return infrastructureError("initialize remote SQLite authority: %v", err)
}
