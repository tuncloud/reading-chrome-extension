// Package apperr is the use cases' error vocabulary. The HTTP adapter maps each Kind
// to a spec §2.0 status and code; use cases never know about HTTP.
package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	Invalid Kind = iota + 1
	InvalidGrant
	Unauthorized
	NotFound
	Conflict
	NoInstallation
	TooLarge
	Capacity
)

type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// As unwraps err into *Error.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

func NewInvalid(format string, a ...any) *Error { return &Error{Invalid, fmt.Sprintf(format, a...)} }
func NewInvalidGrant() *Error {
	return &Error{InvalidGrant, "authorization code is invalid, expired or already used"}
}
func NewUnauthorized() *Error {
	return &Error{Unauthorized, "missing, unknown or revoked device token"}
}
func NewNotFound(msg string) *Error { return &Error{NotFound, msg} }
func NewConflict(msg string) *Error { return &Error{Conflict, msg} }
func NewNoInstallation() *Error     { return &Error{NoInstallation, "connect Notion first"} }
func NewTooLarge(msg string) *Error { return &Error{TooLarge, msg} }
func NewCapacity() *Error           { return &Error{Capacity, "storage is busy; retry with backoff"} }
