package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Refresh tokens use the same shape as magic-link tokens — opaque random
// strings hashed before storage. The user receives the plaintext once at
// login and presents it back to refresh the access token; the server only
// ever sees the hash.

type SessionStore struct {
	pool *pgxpool.Pool
}

func NewSessionStore(pool *pgxpool.Pool) *SessionStore {
	return &SessionStore{pool: pool}
}

type CreateSessionArgs struct {
	UserID    string
	UserAgent string
	IPAddress *netip.Addr
	TTL       time.Duration
	Now       time.Time
}

type CreatedSession struct {
	ID           string
	RefreshToken Token
	ExpiresAt    time.Time
}

// Create issues a new refresh token and persists its hash. Always called
// inside the same flow as JWT issuance so a successful login produces both
// tokens or neither (failures cause rollback at the handler level).
func (s *SessionStore) Create(ctx context.Context, args CreateSessionArgs) (CreatedSession, error) {
	if args.Now.IsZero() {
		args.Now = time.Now().UTC()
	}
	token, err := GenerateToken()
	if err != nil {
		return CreatedSession{}, err
	}
	hash := HashToken(token)
	expires := args.Now.Add(args.TTL)

	var ip any
	if args.IPAddress != nil && args.IPAddress.IsValid() {
		ip = args.IPAddress.String()
	}

	var ua any
	if args.UserAgent != "" {
		ua = args.UserAgent
	}

	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, expires_at, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, args.UserID, hash, expires, ua, ip).Scan(&id)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("insert session: %w", err)
	}

	return CreatedSession{
		ID:           id,
		RefreshToken: token,
		ExpiresAt:    expires,
	}, nil
}

// ErrSessionInvalid is returned for any lookup miss — wrong token, expired,
// or revoked. Collapsed deliberately so attackers cannot probe state.
var ErrSessionInvalid = errors.New("session invalid")

type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// LookupByToken returns the active session for the supplied refresh token,
// if it exists and is not expired or revoked.
func (s *SessionStore) LookupByToken(ctx context.Context, token Token) (Session, error) {
	hash := HashToken(token)
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at
		FROM sessions
		WHERE refresh_token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
	`, hash)

	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrSessionInvalid
		}
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}
	return sess, nil
}

// Revoke marks a session as revoked. Idempotent — repeated calls are no-ops.
func (s *SessionStore) Revoke(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
