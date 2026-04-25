// Package domain holds entities, value objects, and domain errors.
//
// Nothing in this package may import other internal packages — the
// dependency direction is domain ← usecase ← adapter/transport. Domain
// errors are sentinel values; transport maps them to HTTP codes, usecase
// returns them, adapters wrap their failures into them.
package domain

import "errors"

var (
	// ErrLinkInvalid covers all magic-link consume failures: wrong token,
	// expired, already used. Collapsed deliberately so callers cannot leak
	// which check failed.
	ErrLinkInvalid = errors.New("magic link invalid")

	// ErrSessionInvalid covers all session lookup failures: missing,
	// expired, revoked.
	ErrSessionInvalid = errors.New("session invalid")

	// ErrTokenInvalid covers JWT verification failures: bad signature,
	// expired, malformed, wrong issuer/algorithm.
	ErrTokenInvalid = errors.New("token invalid")

	// ErrUserNotFound is returned when a user lookup misses or the row
	// has been soft-deleted.
	ErrUserNotFound = errors.New("user not found")

	// ErrInvalidArgument is for caller-side validation failures (empty
	// email, malformed input). Transport maps it to 400.
	ErrInvalidArgument = errors.New("invalid argument")
)
