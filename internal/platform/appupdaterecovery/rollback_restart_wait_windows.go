//go:build windows

package appupdaterecovery

import (
	"context"
	"errors"
)

func waitRollbackRestartChild(context.Context, int) error {
	return errors.New("bounded rollback child wait is unsupported on windows")
}
