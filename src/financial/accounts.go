// Package financial shares account and transaction persistence across services.
package financial

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
)

var (
	ErrInvalidInput = errors.New("invalid financial input")
	ErrNotFound     = errors.New("account not found")
	ErrStorage      = errors.New("financial storage operation failed")
)

type DB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type Accounts struct {
	db          DB
	identifiers func() (string, string)
}

func NewAccounts(db DB) *Accounts { return &Accounts{db: db, identifiers: newIdentifiers} }

type Metadata struct {
	DisplayName string
	Description string
}

func ValidateMetadata(m Metadata) error {
	for _, field := range []struct {
		value string
		max   int
	}{{m.DisplayName, 100}, {m.Description, 1000}} {
		if !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) || utf8.RuneCountInString(field.value) > field.max {
			return ErrInvalidInput
		}
	}
	return nil
}

// Account has public contract fields and internal scheduler state omitted from
// JSON. HTTP authentication is the caller's responsibility, not a database join.
type Account struct {
	AccountID     string     `json:"-"`
	AccountNumber string     `json:"accountNumber"`
	UserID        string     `json:"userId"`
	DisplayName   string     `json:"displayName"`
	Description   string     `json:"description"`
	Balance       int64      `json:"balance"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"createdAt"`
	Generation    int64      `json:"-"`
	NextSequence  int64      `json:"-"`
	HeadSequence  int64      `json:"-"`
	RetryAfter    *time.Time `json:"-"`
}

const accountColumns = `account_id::text,account_number,owner_id::text,display_name,description,balance,status,created_at,generation,next_sequence,head_sequence,retry_after`

// Create only accepts trusted ownership and metadata. Defaults and a database
// trigger enforce initial financial state. Creation is not idempotent. Bounded
// collision retries use DO NOTHING so caller-owned transactions stay usable.
func (r *Accounts) Create(ctx context.Context, owner string, m Metadata) (Account, error) {
	if !postgres.UUIDKey(owner).Valid || ValidateMetadata(m) != nil {
		return Account{}, ErrInvalidInput
	}
	for range 3 {
		id, number := r.identifiers()
		account, err := scanAccount(r.db.QueryRow(ctx, `INSERT INTO financial.accounts(account_id,account_number,owner_id,display_name,description)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING `+accountColumns, postgres.UUIDKey(id), number, postgres.UUIDKey(owner), m.DisplayName, m.Description))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Account{}, storageError(ctx)
		}
		return account, nil
	}
	return Account{}, storageError(ctx)
}

// ByNumber returns the same not-found outcome for missing and inaccessible rows.
// Account numbers remain opaque: do not parse integers or normalize case/zeros.
func (r *Accounts) ByNumber(ctx context.Context, owner, number string) (Account, error) {
	if number == "" || len(number) > 128 || !utf8.ValidString(number) || strings.ContainsRune(number, 0) {
		return Account{}, ErrNotFound
	}
	account, err := scanAccount(r.db.QueryRow(ctx, `SELECT `+accountColumns+` FROM financial.accounts WHERE owner_id=$1 AND account_number=$2`, postgres.UUIDKey(owner), number))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, storageError(ctx)
	}
	return account, nil
}
func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.AccountID, &a.AccountNumber, &a.UserID, &a.DisplayName, &a.Description, &a.Balance, &a.Status, &a.CreatedAt, &a.Generation, &a.NextSequence, &a.HeadSequence, &a.RetryAfter)
	if err != nil {
		return Account{}, err
	}
	a.CreatedAt = a.CreatedAt.UTC()
	if a.RetryAfter != nil {
		utc := a.RetryAfter.UTC()
		a.RetryAfter = &utc
	}
	return a, nil
}
func newIdentifiers() (string, string) {
	var id, number [16]byte
	_, _ = rand.Read(id[:])
	_, _ = rand.Read(number[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(id[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], hex.EncodeToString(number[:])
}
func storageError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStorage
}
