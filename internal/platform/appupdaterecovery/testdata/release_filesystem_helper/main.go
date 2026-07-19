package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func main() {
	handled, err := appupdaterecovery.RunReleaseFilesystemHelperIfRequested(os.Stdin, os.Stdout)
	if !handled {
		exitWithError(errors.New("release filesystem helper mode is required"))
	}
	if err != nil {
		exitWithError(err)
	}
}

func exitWithError(err error) {
	slog.Error("release filesystem helper failed", "error", err)
	os.Exit(2)
}
