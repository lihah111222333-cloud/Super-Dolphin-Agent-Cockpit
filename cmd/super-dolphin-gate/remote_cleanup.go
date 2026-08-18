package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// runRemoteCleanup 是显式 cleanup-only 入口：不申请 token、不执行 workload、也不最终化 run。
func runRemoteCleanup(args []string, stdout io.Writer) error {
	options, err := parseRemoteCleanupOptions(args)
	if err != nil {
		return err
	}
	config, err := loadRemoteRunConfig(options.configPath)
	if err != nil {
		return err
	}
	ledger, err := gatecontract.NewDurationLedgerStore(options.ledgerPath)
	if err != nil {
		return protocolError("open remote cleanup ledger: %v", err)
	}
	objectStore, runtime, err := newRemoteCleanupClients(config)
	if err != nil {
		return err
	}
	if err := remoteci.CleanupOrphanedRemoteJob(context.Background(), options.jobID, remoteci.CleanupJournalConfig{PollInterval: 2 * time.Second, Timeout: 2 * time.Minute}, ledger, objectStore, runtime); err != nil {
		return infrastructureError("cleanup remote job %s: %v", options.jobID, err)
	}
	_, err = fmt.Fprintf(stdout, "job_id=%s\ncleanup_complete=true\n", options.jobID)
	return err
}

type remoteCleanupOptions struct {
	jobID, configPath, ledgerPath string
}

// parseRemoteCleanupOptions 只接受 cleanup-only 所需的三个显式 authority 参数。
func parseRemoteCleanupOptions(args []string) (remoteCleanupOptions, error) {
	if err := rejectDuplicateAuthorityFlags(args); err != nil {
		return remoteCleanupOptions{}, err
	}
	flags := flag.NewFlagSet("remote cleanup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options remoteCleanupOptions
	flags.StringVar(&options.jobID, "job-id", "", "remote job ID to clean")
	flags.StringVar(&options.configPath, "config", "", "remote CI config path")
	flags.StringVar(&options.ledgerPath, "ledger", "", "duration ledger SQLite authority path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return remoteCleanupOptions{}, protocolError("remote cleanup requires only --job-id --config --ledger")
	}
	if strings.TrimSpace(options.jobID) == "" || strings.TrimSpace(options.configPath) == "" || strings.TrimSpace(options.ledgerPath) == "" {
		return remoteCleanupOptions{}, protocolError("remote cleanup requires --job-id --config --ledger")
	}
	return options, nil
}

func newRemoteCleanupClients(config remoteRunConfig) (remoteci.CleanupJournalObjectStore, remoteci.CleanupJournalRuntime, error) {
	objectStore, err := oss.NewCLI(oss.Config{Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint, Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix})
	if err != nil {
		return nil, nil, infrastructureError("create remote cleanup OSS client: %v", err)
	}
	runtime, err := eci.New(eci.Config{Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitches: config.VSwitches, SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile, Deadline: 2 * time.Minute, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1, RegistryCredentialLoader: loadRemoteRegistryCredential})
	if err != nil {
		return nil, nil, infrastructureError("create remote cleanup ECI client: %v", err)
	}
	return objectStore, runtime, nil
}
