package main

import (
	"errors"
	"strings"
	"testing"
)

func TestOperatorInput(t *testing.T) {
	token := strings.Repeat("A", 43)
	id := "01234567-89ab-cdef-0123-456789abcdef"
	for _, tc := range []struct {
		args  []string
		input string
		ok    bool
	}{
		{[]string{"revoke-session"}, token, true},
		{[]string{"revoke-session"}, token + "\n", true},
		{[]string{"revoke-session"}, token + "\r\n", true},
		{[]string{"revoke-session"}, token + "\r", false},
		{[]string{"revoke-session"}, token + "\nextra", false},
		{[]string{"revoke-session"}, token + " ", false},
		{[]string{"revoke-session"}, strings.Repeat("A", 42) + "B", false},
		{[]string{"revoke-session"}, "", false},
		{[]string{"revoke-session", token}, "", false},
		{[]string{"enable-user", id}, "", true},
		{[]string{"revoke-user-sessions", id}, "", true},
		{[]string{"enable-user", "private-invalid-id"}, "", false},
		{[]string{"enable-user", id, "extra"}, "", false},
		{nil, "", false},
		{[]string{"unknown"}, "", false},
	} {
		_, _, err := parse(tc.args, strings.NewReader(tc.input))
		if (err == nil) != tc.ok {
			t.Fatalf("unexpected parse success=%t", err == nil)
		}
		if err != nil && !errors.Is(err, errInput) {
			t.Fatal("unsafe error")
		}
	}
}
