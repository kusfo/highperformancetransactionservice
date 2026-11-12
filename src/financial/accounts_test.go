package financial

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
)

func TestMetadata(t *testing.T) {
	for _, m := range []Metadata{{}, {"  名字  ", "e\u0301"}, {strings.Repeat("界", 100), strings.Repeat("😀", 1000)}} {
		if ValidateMetadata(m) != nil {
			t.Fatal("valid metadata rejected")
		}
	}
	for _, m := range []Metadata{{strings.Repeat("界", 101), ""}, {"", strings.Repeat("😀", 1001)}, {"a\x00b", ""}, {"", "\xff"}} {
		if !errors.Is(ValidateMetadata(m), ErrInvalidInput) {
			t.Fatal("invalid metadata accepted")
		}
	}
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

type fakeDB struct {
	calls int
	errs  []error
}

func (d *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	err := d.errs[d.calls]
	d.calls++
	return errorRow{err}
}
func TestAccountFailuresAndCollisionRetry(t *testing.T) {
	owner := "01234567-89ab-cdef-0123-456789abcdef"
	for _, tc := range []struct {
		errs  []error
		want  error
		calls int
	}{
		{[]error{pgx.ErrNoRows, nil}, nil, 2},
		{[]error{pgx.ErrNoRows, pgx.ErrNoRows, pgx.ErrNoRows}, ErrStorage, 3},
		{[]error{errors.New("private-storage-detail")}, ErrStorage, 1},
	} {
		db := &fakeDB{errs: tc.errs}
		r := NewAccounts(db)
		ids := map[string]bool{}
		r.identifiers = func() (string, string) {
			id, number := newIdentifiers()
			if !postgres.UUIDKey(id).Valid || ids[number] {
				t.Fatal("invalid/duplicate generated identifier")
			}
			ids[number] = true
			return id, number
		}
		_, err := r.Create(context.Background(), owner, Metadata{})
		if !errors.Is(err, tc.want) || db.calls != tc.calls {
			t.Fatal("incorrect retry/error outcome")
		}
	}
	if _, err := NewAccounts(nil).Create(context.Background(), "invalid", Metadata{}); err != ErrInvalidInput {
		t.Fatal(err)
	}
	if _, err := NewAccounts(nil).Create(context.Background(), owner, Metadata{DisplayName: "\x00"}); err != ErrInvalidInput {
		t.Fatal(err)
	}
	db := &fakeDB{errs: []error{pgx.ErrNoRows, errors.New("private")}}
	if _, err := NewAccounts(db).ByNumber(context.Background(), owner, "missing"); err != ErrNotFound {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewAccounts(db).ByNumber(ctx, owner, "number"); err != context.Canceled {
		t.Fatal(err)
	}
	data, err := json.Marshal(Account{AccountID: "internal", Generation: 1, NextSequence: 2, HeadSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil || len(fields) != 7 || strings.Contains(string(data), "internal") {
		t.Fatal("internal state serialized")
	}
}
