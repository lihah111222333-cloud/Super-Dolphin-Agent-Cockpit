package eventcore

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidState  = errors.New("invalid state transition")
	ErrRequired      = errors.New("required field missing")
)
