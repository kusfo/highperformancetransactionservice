// Package sessions persists opaque bearer sessions without storing raw tokens.
package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const Lifetime = 10 * time.Minute

var (
	ErrInvalidSession = errors.New("invalid session")
	ErrStorage        = errors.New("session storage operation failed")
)

// DB also permits a caller-owned pgx transaction, so future lifecycle changes
// and revocation can commit atomically. Do not share a connection concurrently.
type DB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type Repository struct {
	db  DB
	now func() time.Time
}

func NewRepository(db DB, now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{db: db, now: now}
}

// Session contains public identity/timing metadata, never token hashes.
type Session struct {
	UserID              string
	IssuedAt, ExpiresAt time.Time
}

// Issued deliberately cannot serialize or format its credential. A login handler
// must explicitly construct the approved response using AccessToken().
type Issued struct {
	Session Session `json:"-"`
	token   string
}

func (i Issued) AccessToken() string { return i.token }
func (i Issued) String() string      { return "Issued{redacted}" }
func (i Issued) GoString() string    { return i.String() }

// Issue is for trusted authentication code after credential verification. It
// checks active status but does not verify passwords. Lifecycle/login code must
// serialize user changes using a user-row lock in its caller-owned transaction.
func (r *Repository) Issue(ctx context.Context, userID string) (Issued, error) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256(raw)
	now := r.now().UTC().Truncate(time.Microsecond)
	var s Session
	err := r.db.QueryRow(ctx, `INSERT INTO identity.sessions(token_hash,user_id,issued_at,expires_at)
 SELECT $1,user_id,$3,$4 FROM identity.users WHERE user_id::text=$2 AND active
 RETURNING user_id::text,issued_at,expires_at`, hash[:], userID, now, now.Add(Lifetime)).Scan(&s.UserID, &s.IssuedAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Issued{}, ErrInvalidSession
	}
	if err != nil {
		return Issued{}, storageError(ctx)
	}
	return Issued{Session: s, token: token}, nil
}

// Authenticate accepts only canonical 256-bit tokens issued by this service.
// Expiry is exclusive: the exact expiry instant is no longer valid. Fail closed
// for inactive users, revoked/unknown tokens, and timestamps before issuance.
func (r *Repository) Authenticate(ctx context.Context, token string) (Session, error) {
	hash, err := tokenHash(token)
	if err != nil {
		return Session{}, err
	}
	var s Session
	err = r.db.QueryRow(ctx, `SELECT s.user_id::text,s.issued_at,s.expires_at
 FROM identity.sessions s JOIN identity.users u ON u.user_id=s.user_id
 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND u.active
 AND s.issued_at <= $2 AND s.expires_at > $2`, hash[:], r.now().UTC()).Scan(&s.UserID, &s.IssuedAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, storageError(ctx)
	}
	return s, nil
}

// Revoke is idempotent, including well-formed unknown or expired tokens.
func (r *Repository) Revoke(ctx context.Context, token string) error {
	hash, err := tokenHash(token)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `UPDATE identity.sessions SET revoked_at=$2 WHERE token_hash=$1 AND revoked_at IS NULL`, hash[:], r.now().UTC())
	if err != nil {
		return storageError(ctx)
	}
	return nil
}

// RevokeAll is intended to share a transaction with a password change or user
// deactivation. The caller must serialize issuance using the same user-row lock.
func (r *Repository) RevokeAll(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `UPDATE identity.sessions SET revoked_at=$2 WHERE user_id::text=$1 AND revoked_at IS NULL`, userID, r.now().UTC())
	if err != nil {
		return storageError(ctx)
	}
	return nil
}
func tokenHash(token string) ([32]byte, error) {
	if len(token) != 43 {
		return [32]byte{}, ErrInvalidSession
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return [32]byte{}, ErrInvalidSession
	}
	return sha256.Sum256(raw), nil
}
func storageError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStorage
}
