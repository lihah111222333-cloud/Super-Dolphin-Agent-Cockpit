package appupdaterecovery

const artifactProgramSource = `package main

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var release string
var role string

func main() {
	switch os.Getenv("ARTIFACT_MODE") {
	case "crash":
		os.Exit(23)
	case "serve":
		waitForTermination()
	default:
		if token, ok := rollbackLaunchToken(os.Args[1:]); ok {
			serveAuthenticatedTermination(token)
			return
		}
		fmt.Printf("%s:%s\n", release, role)
	}
}

func rollbackLaunchToken(args []string) (string, bool) {
	for _, arg := range args {
		if token, ok := strings.CutPrefix(arg, "--super-dolphin-rollback-launch-token="); ok {
			return token, true
		}
	}
	return "", false
}

func serveAuthenticatedTermination(token string) {
	endpoint := os.Getenv("SUPER_DOLPHIN_UPDATE_TERMINATION_ENDPOINT")
	terminationToken := os.Getenv("SUPER_DOLPHIN_UPDATE_TERMINATION_TOKEN")
	transactionRoot := os.Getenv("SUPER_DOLPHIN_UPDATE_TRANSACTION_ROOT")
	transactionID := os.Getenv("SUPER_DOLPHIN_UPDATE_TRANSACTION_ID")
	if endpoint == "" || terminationToken == "" || terminationToken != token || transactionRoot == "" || transactionID == "" {
		os.Exit(30)
	}
	hold := artifactMarkerPath(transactionRoot, transactionID, "hold")
	for {
		if _, err := os.Stat(hold); os.IsNotExist(err) {
			break
		} else if err != nil {
			os.Exit(34)
		}
		time.Sleep(10 * time.Millisecond)
	}
	oldUmask := syscall.Umask(0177)
	listener, err := net.Listen("unix", endpoint)
	syscall.Umask(oldUmask)
	if err != nil {
		os.Exit(31)
	}
	defer listener.Close()
	defer os.Remove(endpoint)
	if err := os.Chmod(endpoint, 0600); err != nil {
		os.Exit(32)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			os.Exit(33)
		}
		line, readErr := bufio.NewReader(connection).ReadString('\n')
		command, got, found := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		if readErr == nil && found && subtle.ConstantTimeCompare([]byte(got), []byte(terminationToken)) == 1 {
			switch command {
			case "READY":
				_, _ = connection.Write([]byte("READY\n"))
			case "COMMIT":
				_, _ = connection.Write([]byte("COMMITTED\n"))
			case "TERMINATE":
				ignoreTerminate := artifactMarkerPath(transactionRoot, transactionID, "ignore-terminate")
				if _, err := os.Stat(ignoreTerminate); err == nil {
					break
				} else if !os.IsNotExist(err) {
					os.Exit(35)
				}
				_, _ = connection.Write([]byte("ACK\n"))
				_ = connection.Close()
				return
			}
		}
		_ = connection.Close()
	}
}

func artifactMarkerPath(transactionRoot, transactionID, name string) string {
	if !filepath.IsAbs(transactionRoot) {
		os.Exit(36)
	}
	return filepath.Join(filepath.Dir(transactionRoot), ".artifact-e2e-markers-"+transactionID, name)
}

func waitForTermination() {
	terminated := make(chan os.Signal, 1)
	signal.Notify(terminated, syscall.SIGINT, syscall.SIGTERM)
	<-terminated
}
`
