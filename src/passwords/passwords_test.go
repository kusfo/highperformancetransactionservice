package passwords

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	password := " example-password界 "
	first, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	a := strings.Split(first, "$")
	b := strings.Split(second, "$")
	if first == second || a[4] == b[4] {
		t.Fatal("reused salt")
	}
	if a[1] != "argon2id" || a[2] != "v=19" || a[3] != "m=65536,t=3,p=4" {
		t.Fatal("unexpected hash profile")
	}
	for _, encoded := range []string{first, second} {
		ok, err := Verify(password, encoded)
		if err != nil || !ok {
			t.Fatal("correct password rejected")
		}
	}
	for _, wrong := range []string{"different-password", strings.TrimSpace(password), " example-password界\t"} {
		ok, err := Verify(wrong, first)
		if err != nil || ok {
			t.Fatal("wrong or altered password accepted")
		}
	}
}

func TestPasswordBoundaries(t *testing.T) {
	for _, password := range []string{strings.Repeat("界", 12), strings.Repeat("😀", 128), strings.Repeat(" ", 12)} {
		encoded, err := Hash(password)
		if err != nil {
			t.Fatal("valid boundary rejected")
		}
		if ok, err := Verify(password, encoded); err != nil || !ok {
			t.Fatal("boundary round trip failed")
		}
	}
	for _, password := range []string{"", strings.Repeat("界", 11), strings.Repeat("a", 129), strings.Repeat("😀", 129), "example-password\xff"} {
		if encoded, err := Hash(password); !errors.Is(err, ErrInvalidPassword) || encoded != "" {
			t.Fatal("invalid password hashed")
		}
		if ok, err := Verify(password, "secret-hash"); ok || !errors.Is(err, ErrInvalidPassword) {
			t.Fatal("invalid password verified")
		}
	}
	composed := "example-pass-é"
	encoded, err := Hash(composed)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify("example-pass-e\u0301", encoded); ok || err != nil {
		t.Fatal("password was normalized")
	}
}

func TestMalformedHashes(t *testing.T) {
	valid := prefix + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43)
	for _, encoded := range []string{
		"", "secret-hash", valid + "$", valid[:len(valid)-1],
		strings.Replace(valid, "argon2id", "argon2i", 1),
		strings.Replace(valid, "v=19", "v=16", 1),
		strings.Replace(valid, "m=65536", "m=99999", 1),
		strings.Replace(valid, "t=3", "t=0", 1),
		strings.Replace(valid, "p=4", "p=0", 1),
		strings.Replace(valid, "m=65536,t=3,p=4", "p=4,t=3,m=65536", 1),
		prefix + strings.Repeat("A", 21) + "B$" + strings.Repeat("A", 43),
		prefix + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 42) + "B",
		strings.Replace(valid, "A", "!", 1), strings.Repeat("x", 10000),
	} {
		ok, err := Verify("example-password", encoded)
		if ok || !errors.Is(err, ErrInvalidHash) {
			t.Fatal("malformed hash accepted")
		}
		if err.Error() != "invalid or unsupported password hash" {
			t.Fatal("unsafe error")
		}
	}
}

func TestIndependentArgon2idVector(t *testing.T) {
	// Generated with argon2-cffi 25.1.0 hash_secret(Type.ID), password below,
	// salt bytes 0..15, time_cost=3, memory_cost=65536, parallelism=4, hash_len=32.
	const encoded = "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBgcICQoLDA0ODw$L2bNOu+kWgz1kesottHdO/wBExFrlfXYrTqxoNSqVew"
	if ok, err := Verify("example-password", encoded); err != nil || !ok {
		t.Fatal("independent Argon2id vector failed")
	}
}
