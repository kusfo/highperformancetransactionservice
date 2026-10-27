// Package login verifies credentials and commits a session before returning it.
package login

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

var (
	ErrInvalidInput       = errors.New("invalid login input")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrStorage            = errors.New("login storage operation failed")
)

type DB interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}
type Service struct {
	db     DB
	now    func() time.Time
	verify func(string, string) (bool, error)
}

func NewService(db DB, now func() time.Time) *Service {
	return &Service{db: db, now: now, verify: passwords.Verify}
}

// A valid public fixture hash incurs the same Argon2 profile for unknown users.
// Matching this dummy credential never permits authentication.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBgcICQoLDA0ODw$L2bNOu+kWgz1kesottHdO/wBExFrlfXYrTqxoNSqVew"

func Validate(email, password string) (string, error) {
	canonical, err := users.NormalizeEmail(email)
	if err != nil || len(password) > 128*utf8.UTFMax || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 1 || utf8.RuneCountInString(password) > 128 {
		return "", ErrInvalidInput
	}
	return canonical, nil
}

// Authenticate serializes with user updates. Callers must bound concurrency:
// password verification costs 64 MiB per active call and is not interruptible.
func (s *Service) Authenticate(ctx context.Context, email, password string) (sessions.Issued, error) {
	canonical, err := Validate(email, password)
	if err != nil {
		return sessions.Issued{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return sessions.Issued{}, storageError(ctx)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var userID, hash string
	var active bool
	err = tx.QueryRow(ctx, `SELECT user_id::text,password_hash,active FROM identity.users WHERE email=$1 FOR UPDATE`, canonical).Scan(&userID, &hash, &active)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return sessions.Issued{}, storageError(ctx)
	}
	if !found {
		hash = dummyHash
	}
	candidate := password
	short := utf8.RuneCountInString(password) < 12
	if short {
		candidate = "dummy-short-password"
	}
	if ctx.Err() != nil {
		return sessions.Issued{}, ctx.Err()
	}
	matched, verifyErr := s.verify(candidate, hash)
	if verifyErr != nil {
		// Malformed persisted credentials also fail generically, with real hash work.
		_, _ = s.verify(candidate, dummyHash)
	}
	if ctx.Err() != nil {
		return sessions.Issued{}, ctx.Err()
	}
	if !found || !active || short || verifyErr != nil || !matched {
		return sessions.Issued{}, ErrInvalidCredentials
	}
	issued, err := sessions.NewRepository(tx, s.now).Issue(ctx, userID)
	if err != nil {
		return sessions.Issued{}, storageError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return sessions.Issued{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return sessions.Issued{}, storageError(ctx)
	}
	return issued, nil
}
func storageError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStorage
}
