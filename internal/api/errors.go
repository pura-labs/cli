package api

import (
	"errors"
	"fmt"
)

// Error is a typed failure from the Pura API. It preserves the HTTP status
// so callers (notably the CLI's exit-code layer) can route on it without
// string matching.
//
// Callers that want the raw envelope fields do:
//
//	var e *api.Error
//	if errors.As(err, &e) { … e.Status … }
type Error struct {
	// Status is the HTTP response code. When the failure happened before
	// we got a well-formed response (transport error, etc.) the concrete
	// error returned by the client will not be *Error at all.
	Status int

	// Code is the server-assigned error code (e.g. "unauthorized",
	// "not_found", "rate_limit"). Stable across Pura versions.
	Code string

	// Message is a human-readable summary, always present.
	Message string

	// Hint is an optional suggested fix.
	Hint string
}

func (e *Error) Error() string {
	msg := e.Message
	if e.Hint != "" {
		msg += "\nHint: " + e.Hint
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, msg)
	}
	return msg
}

// AsError unwraps err into *Error if possible. Returns nil when err isn't
// an *Error anywhere in the chain — callers can treat that as "transport /
// decode / unknown" and map it to a generic exit code.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// Predicates — cheap checks without the caller needing errors.As.
func IsNotFound(err error) bool     { return matchStatus(err, 404) }
func IsUnauthorized(err error) bool { return matchStatus(err, 401) }
func IsForbidden(err error) bool    { return matchStatus(err, 403) }
func IsRateLimited(err error) bool  { return matchStatus(err, 429) }
func IsConflict(err error) bool     { return matchStatus(err, 409) }
func IsValidation(err error) bool   { return matchStatus(err, 400) }

func matchStatus(err error, status int) bool {
	e := AsError(err)
	return e != nil && e.Status == status
}
