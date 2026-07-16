package main

import (
	"fmt"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
)

const maxHelperStderrBytes = 16 * 1024

func main() {
	if err := schema.ServeOneShot(os.Stdin, os.Stdout); err != nil {
		message := fmt.Sprintf("%v", err)
		if len(message) > maxHelperStderrBytes {
			message = "schema helper failed with oversized diagnostic"
		}
		_, _ = fmt.Fprint(os.Stderr, message)
		os.Exit(1)
	}
}
