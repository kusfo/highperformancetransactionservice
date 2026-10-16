package users

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	email, err := NormalizeEmail(" \tPerson+Tag@EXAMPLE.COM\r\n")
	if err != nil || email != "person+tag@example.com" {
		t.Fatalf("normalization: %q %v", email, err)
	}
	for _, invalid := range []string{"", "no-at", "person@", "é@example.com", "K@example.com", "Display <person@example.com>", "a b@example.com", strings.Repeat("a", 255) + "@example.com"} {
		if _, err := NormalizeEmail(invalid); err != ErrInvalidInput {
			t.Fatalf("invalid input accepted: %q", invalid)
		}
	}
}
func TestCredentialsDoNotSerializeOrPrintHashes(t *testing.T) {
	c := Credentials{passwordHash: "secret-stored-hash", User: User{Email: "person@example.com"}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(data), fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(out, c.passwordHash) {
			t.Fatal("credential material leaked")
		}
	}
	if c.PasswordHash() != "secret-stored-hash" {
		t.Fatal("authentication cannot access hash")
	}
}
