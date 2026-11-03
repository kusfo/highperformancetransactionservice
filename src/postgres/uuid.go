package postgres

import (
	"encoding/hex"

	"github.com/jackc/pgx/v5/pgtype"
)

// UUIDKey converts a canonical lowercase UUID to a typed query parameter.
// Invalid IDs become SQL NULL: equality matches no rows, preserving the old
// text-comparison behavior without exposing input in PostgreSQL cast errors.
// Only predicates use this helper; returned UUIDs may still be cast to text.
func UUIDKey(id string) pgtype.UUID {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return pgtype.UUID{}
	}
	compact := id[:8] + id[9:13] + id[14:18] + id[19:23] + id[24:]
	for _, c := range compact {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return pgtype.UUID{}
		}
	}
	var key pgtype.UUID
	if _, err := hex.Decode(key.Bytes[:], []byte(compact)); err != nil {
		return pgtype.UUID{}
	}
	key.Valid = true
	return key
}
