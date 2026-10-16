// Package users persists user identity separately from public profile data.
package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidInput     = errors.New("invalid user input")
	ErrEmailAlreadyUsed = errors.New("email already used")
	ErrNotFound         = errors.New("user not found")
	ErrStorage          = errors.New("user storage operation failed")
)

// DB is implemented by pgx connections, transactions and pools. A single pgx
// connection must not be shared concurrently; use separate connections or a pool.
type DB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type Repository struct{ db DB }

func NewRepository(db DB) *Repository { return &Repository{db: db} }

// User is safe for profile responses: it never contains credential data.
type User struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

// Credentials are for authentication only, not profile serialization or logs.
// Hash material is intentionally inaccessible to JSON and fmt printers.
type Credentials struct {
	User         User `json:"-"`
	passwordHash string
}

func (c Credentials) PasswordHash() string { return c.passwordHash }
func (c Credentials) String() string       { return "Credentials{redacted}" }
func (c Credentials) GoString() string     { return c.String() }

// NormalizeEmail follows the confirmed ASCII, case-insensitive identity policy.
func NormalizeEmail(email string) (string, error) {
	email = strings.Trim(email, " \t\r\n\v\f")
	if len(email) < 3 || len(email) > 254 {
		return "", ErrInvalidInput
	}
	for _, r := range email {
		if r > 127 || r <= 32 || r == 127 {
			return "", ErrInvalidInput
		}
	}
	email = strings.ToLower(email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", ErrInvalidInput
	}
	return email, nil
}

// Create expects a server-generated password hash; password hashing is T09.
// Optional displayName defaults to empty when omitted by the caller.
func (r *Repository) Create(ctx context.Context, email, passwordHash string, displayName *string) (User, error) {
	canonical, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	name := ""
	if displayName != nil {
		name = *displayName
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
			return User{}, ErrInvalidInput
		}
	}
	if passwordHash == "" || strings.ContainsRune(passwordHash, 0) {
		return User{}, ErrInvalidInput
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return User{}, ErrStorage
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	id := hex.EncodeToString(bytes)
	id = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	var user User
	err = r.db.QueryRow(ctx, `INSERT INTO identity.users (user_id,email,password_hash,display_name)
 VALUES ($1,$2,$3,$4) RETURNING user_id::text,email,display_name,active`, id, canonical, passwordHash, name).Scan(&user.UserID, &user.Email, &user.DisplayName, &user.Active)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_unique" {
			return User{}, ErrEmailAlreadyUsed
		}
		return User{}, storageError(ctx, err)
	}
	return user, nil
}

func (r *Repository) ByID(ctx context.Context, id string) (User, error) {
	// Compare as text so malformed IDs are not echoed by PostgreSQL UUID errors.
	var user User
	err := r.db.QueryRow(ctx, `SELECT user_id::text,email,display_name,active FROM identity.users WHERE user_id::text=$1`, id).Scan(&user.UserID, &user.Email, &user.DisplayName, &user.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, storageError(ctx, err)
	}
	return user, nil
}

func (r *Repository) CredentialsByEmail(ctx context.Context, email string) (Credentials, error) {
	canonical, err := NormalizeEmail(email)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	err = r.db.QueryRow(ctx, `SELECT user_id::text,email,display_name,active,password_hash FROM identity.users WHERE email=$1`, canonical).Scan(&c.User.UserID, &c.User.Email, &c.User.DisplayName, &c.User.Active, &c.passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, storageError(ctx, err)
	}
	return c, nil
}

// SetActive retains the row, email reservation and credential data. Session
// revocation and HTTP authorization are deliberately handled by later tasks.
func (r *Repository) SetActive(ctx context.Context, id string, active bool) error {
	tag, err := r.db.Exec(ctx, `UPDATE identity.users SET active=$2 WHERE user_id::text=$1`, id, active)
	if err != nil {
		return storageError(ctx, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func storageError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Do not expose driver error details: they can include hashes or input values.
	return ErrStorage
}
