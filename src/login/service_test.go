package login

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct{ found bool }

func (r fakeRow) Scan(dst ...any) error {
	if !r.found {
		return pgx.ErrNoRows
	}
	*(dst[0].(*string)) = "id"
	*(dst[1].(*string)) = "malformed-hash"
	*(dst[2].(*bool)) = true
	return nil
}

type fakeTx struct {
	pgx.Tx
	found  bool
	rolled bool
}

func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{t.found} }
func (t *fakeTx) Rollback(context.Context) error                   { t.rolled = true; return nil }

type fakeDB struct{ tx *fakeTx }

func (d fakeDB) BeginTx(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) { return d.tx, nil }
func TestUnknownAndMalformedCredentialsPerformHashWork(t *testing.T) {
	for _, found := range []bool{false, true} {
		tx := &fakeTx{found: found}
		service := NewService(fakeDB{tx}, nil)
		calls := 0
		service.verify = func(p, h string) (bool, error) {
			calls++
			if h == dummyHash {
				return true, nil
			}
			return false, pgx.ErrNoRows
		}
		issued, err := service.Authenticate(context.Background(), "person@example.com", "example-password")
		if err != ErrInvalidCredentials || issued.AccessToken() != "" || !tx.rolled {
			t.Fatal("unsafe credential failure")
		}
		expected := 1
		if found {
			expected = 2
		}
		if calls != expected {
			t.Fatal("missing hash work")
		}
	}
}

func TestCancellationAfterPasswordVerification(t *testing.T) {
	tx := &fakeTx{found: true}
	service := NewService(fakeDB{tx}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.verify = func(string, string) (bool, error) { cancel(); return true, nil }
	issued, err := service.Authenticate(ctx, "person@example.com", "example-password")
	if err != context.Canceled || issued.AccessToken() != "" || !tx.rolled {
		t.Fatal("cancelled verification issued session")
	}
}
