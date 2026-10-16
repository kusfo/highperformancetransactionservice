package users

import (
	"encoding/json"
	"fmt"
	"os"
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

func TestSharedEmailContractCases(t *testing.T) {
	data, err := os.ReadFile("../../tests/contracts/email_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Email     string `json:"email"`
		Valid     bool   `json:"valid"`
		Canonical string `json:"canonical"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		canonical, err := NormalizeEmail(tc.Email)
		if (err == nil) != tc.Valid || tc.Valid && canonical != tc.Canonical {
			t.Errorf("normalization mismatch for %q", tc.Email)
		}
	}
}
