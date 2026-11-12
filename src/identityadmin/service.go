// Package identityadmin provides trusted operator-only identity mutations.
package identityadmin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type DB interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}
type Service struct{ db DB }

func NewService(db DB) *Service { return &Service{db: db} }

// Enable restores active state after revoking any remaining sessions on an
// inactive user. Existing revoked sessions stay revoked.
// Calling Enable on an active user is a no-op and does not revoke fresh sessions.
func (s *Service) Enable(ctx context.Context, id string) error { return s.mutate(ctx, id, true) }

// RevokeUserSessions serializes with login using its user-row lock. It revokes
// sessions committed before this lock is acquired; later logins are allowed.
func (s *Service) RevokeUserSessions(ctx context.Context, id string) error {
	return s.mutate(ctx, id, false)
}

func (s *Service) mutate(ctx context.Context, id string, enable bool) error {
	if !postgres.UUIDKey(id).Valid {
		return users.ErrInvalidInput
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
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM identity.users WHERE user_id=$1 FOR UPDATE`, postgres.UUIDKey(id)).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.ErrNotFound
	}
	if err != nil {
		return storageError(ctx)
	}
	if enable {
		if !active {
			if err = sessions.NewRepository(tx, nil).RevokeAll(ctx, id); err != nil {
				return storageError(ctx)
			}
			if _, err = tx.Exec(ctx, `UPDATE identity.users SET active=true WHERE user_id=$1`, postgres.UUIDKey(id)); err != nil {
				return storageError(ctx)
			}
		}
	} else if err = sessions.NewRepository(tx, nil).RevokeAll(ctx, id); err != nil {
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
