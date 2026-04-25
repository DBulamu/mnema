package domain

import (
	"net/netip"
	"time"
)

// Session is a long-lived refresh-token-backed session bound to a user.
// The plaintext refresh token is only known to the client; the server
// stores its hash on the row.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// SessionContext is the metadata captured at session creation. Used for
// audit / "your sessions" UI later.
type SessionContext struct {
	UserAgent string
	IPAddress *netip.Addr
}

// MagicLink represents a single-use sign-in token issued for an email
// address. Atomicity of consume is enforced at the repository level.
type MagicLink struct {
	ID    string
	Email string
}
