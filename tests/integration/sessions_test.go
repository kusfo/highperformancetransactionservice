//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

func TestSessionIssuanceExpiryAndRevocation(t *testing.T) {
	conn, users, ctx := userFixture(t)
	user, err := users.Create(ctx, "session@example.com", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
	now := start
	repo := sessions.NewRepository(conn, func() time.Time { return now })
	first, err := repo.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken() == second.AccessToken() || len(first.AccessToken()) != 43 {
		t.Fatal("token entropy/uniqueness")
	}
	if !first.Session.IssuedAt.Equal(start) || !first.Session.ExpiresAt.Equal(start.Add(10*time.Minute)) {
		t.Fatal("incorrect lifetime")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.AccessToken())
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity.sessions WHERE token_hash=$1 AND user_id::text=$2)`, hash[:], user.UserID).Scan(&exists); err != nil || !exists {
		t.Fatal("digest not stored")
	}
	var rowText string
	if err := conn.QueryRow(ctx, `SELECT row_to_json(s)::text FROM identity.sessions s WHERE token_hash=$1`, hash[:]).Scan(&rowText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rowText, first.AccessToken()) {
		t.Fatal("raw token stored")
	}
	for _, offset := range []time.Duration{0, 10*time.Minute - time.Microsecond} {
		now = start.Add(offset)
		s, err := repo.Authenticate(ctx, first.AccessToken())
		if err != nil || s.UserID != user.UserID {
			t.Fatal("valid session rejected")
		}
	}
	for _, offset := range []time.Duration{-time.Microsecond, 10 * time.Minute, 11 * time.Minute} {
		now = start.Add(offset)
		if _, err := repo.Authenticate(ctx, first.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
			t.Fatal("invalid time accepted")
		}
	}
	now = start
	forged := []byte(first.AccessToken())
	if forged[0] == 'A' {
		forged[0] = 'B'
	} else {
		forged[0] = 'A'
	}
	for _, token := range []string{string(forged), strings.Repeat("A", 43), "malformed-token"} {
		if _, err := repo.Authenticate(ctx, token); !errors.Is(err, sessions.ErrInvalidSession) {
			t.Fatal("forged token accepted")
		}
	}
	for i := 0; i < 2; i++ {
		if err := repo.Revoke(ctx, first.AccessToken()); err != nil {
			t.Fatal(err)
		}
	}
	// A fresh DB connection/repository proves revocation is durable, not cached.
	other, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	fresh := sessions.NewRepository(other, func() time.Time { return now })
	if _, err := fresh.Authenticate(ctx, first.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("revoked session accepted")
	}
	if _, err := fresh.Authenticate(ctx, second.AccessToken()); err != nil {
		t.Fatal("single revoke affected another session")
	}
	if err := users.SetActive(ctx, user.UserID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Authenticate(ctx, second.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("inactive user accepted")
	}
	if _, err := fresh.Issue(ctx, user.UserID); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("issued to inactive user")
	}
	if err := fresh.RevokeAll(ctx, user.UserID); err != nil {
		t.Fatal(err)
	}
	if err := users.SetActive(ctx, user.UserID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Authenticate(ctx, second.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("reenablement revived revoked token")
	}
	if _, err := fresh.Issue(ctx, "missing"); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("issued to missing user")
	}
}
func TestSessionTransactionRollbackAndStorageErrors(t *testing.T) {
	conn, users, ctx := userFixture(t)
	user, err := users.Create(ctx, "rollback@example.com", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := sessions.NewRepository(conn, nil)
	issued, err := repo.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transactional := sessions.NewRepository(tx, nil)
	if err := transactional.RevokeAll(ctx, user.UserID); err != nil {
		t.Fatal(err)
	}
	rolled, err := transactional.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Authenticate(ctx, issued.AccessToken()); err != nil {
		t.Fatal("rollback retained revocation")
	}
	if _, err := repo.Authenticate(ctx, rolled.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("rollback retained issuance")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.Issue(cancelled, user.UserID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Issue(ctx, user.UserID); !errors.Is(err, sessions.ErrStorage) || got.AccessToken() != "" {
		t.Fatal("unsafe failed issuance")
	}
	if _, err := repo.Authenticate(ctx, issued.AccessToken()); !errors.Is(err, sessions.ErrStorage) {
		t.Fatal("unsafe lookup failure")
	}
	if err := repo.Revoke(ctx, issued.AccessToken()); !errors.Is(err, sessions.ErrStorage) {
		t.Fatal("unsafe revoke failure")
	}
	if err := repo.RevokeAll(ctx, user.UserID); !errors.Is(err, sessions.ErrStorage) {
		t.Fatal("unsafe bulk revoke failure")
	}
}
