// Package profile applies owner profile and credential changes.
package profile

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

var ErrCurrentPassword = errors.New("invalid current password")

type DB interface {
	users.DB
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}
type Service struct{ db DB }

func NewService(db DB) *Service { return &Service{db: db} }

// Update changes only display_name. The UPDATE also serializes with lifecycle
// writes and does not revive a user deactivated since the entry check.
func (s *Service) Update(ctx context.Context, id, name string) (users.User, error) {
	if err := users.ValidateDisplayName(&name); err != nil {
		return users.User{}, err
	}
	var u users.User
	err := s.db.QueryRow(ctx, `UPDATE identity.users SET display_name=$2 WHERE user_id::text=$1 AND active RETURNING user_id::text,email,display_name,active`, id, name).Scan(&u.UserID, &u.Email, &u.DisplayName, &u.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, users.ErrNotFound
	}
	if err != nil {
		return users.User{}, storageError(ctx)
	}
	return u, nil
}
func ValidatePasswordChange(current, next string) error {
	if !utf8.ValidString(current) || len(current) > 128*utf8.UTFMax || utf8.RuneCountInString(current) < 1 || utf8.RuneCountInString(current) > 128 || passwords.Validate(next) != nil {
		return users.ErrInvalidInput
	}
	return nil
}

// ChangePassword shares login's user-row lock. Hash replacement and permanent
// revocation commit together; no successful response precedes that commit.
// HTTP callers must bound concurrent hashing/verification.
func (s *Service) ChangePassword(ctx context.Context, id, current, next string) error {
	if err := ValidatePasswordChange(current, next); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return storageError(ctx)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var hash string
	err = tx.QueryRow(ctx, `SELECT password_hash FROM identity.users WHERE user_id::text=$1 AND active FOR UPDATE`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.ErrNotFound
	}
	if err != nil {
		return storageError(ctx)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	match, err := passwords.Verify(current, hash)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || !match {
		return ErrCurrentPassword
	}
	hash, err = passwords.Hash(next)
	if err != nil {
		return storageError(ctx)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err = tx.Exec(ctx, `UPDATE identity.users SET password_hash=$2 WHERE user_id::text=$1`, id, hash); err != nil {
		return storageError(ctx)
	}
	if err = sessions.NewRepository(tx, nil).RevokeAll(ctx, id); err != nil {
		return storageError(ctx)
	}
	if err = tx.Commit(ctx); err != nil {
		return storageError(ctx)
	}
	return nil
}
func storageError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return users.ErrStorage
}
