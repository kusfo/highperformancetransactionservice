package postgres

import "testing"

func TestUUIDKey(t *testing.T) {
	key := UUIDKey("550e8400-e29b-41d4-a716-446655440000")
	if !key.Valid || key.Bytes[0] != 0x55 || key.Bytes[1] != 0x0e || key.Bytes[15] != 0 {
		t.Fatal("UUID not decoded")
	}
	for _, id := range []string{"", "missing", "550E8400-E29B-41D4-A716-446655440000", "550e8400e29b41d4a716446655440000", "550e8400-e29b-41d4-a716-44665544000z", "550e8400_e29b-41d4-a716-446655440000"} {
		if UUIDKey(id).Valid {
			t.Fatal("noncanonical ID accepted")
		}
	}
	if !UUIDKey("00000000-0000-0000-0000-000000000000").Valid {
		t.Fatal("zero UUID rejected")
	}
}
