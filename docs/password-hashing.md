# Password hashing

`src/passwords` implements T09. `Hash(password)` validates the agreed password
policy and generates a fresh cryptographically random salt. `Verify(password,
encoded)` returns a boolean match and an error for invalid input or unsupported
stored credentials. `Validate` is available to future registration/password-change
handlers. No trimming, Unicode normalization or character-category rules apply;
passwords must be valid UTF-8 with 12–128 Unicode code points.

## Algorithm and storage

Use Argon2id version 19, 65,536 KiB memory (64 MiB), three iterations, four lanes,
a 16-byte salt from `crypto/rand`, and a 32-byte derived key. This is the second
recommended profile in [RFC 9106, section 4](https://www.rfc-editor.org/rfc/rfc9106.html#section-4).
The Go implementation is `golang.org/x/crypto/argon2`, pinned in go.mod/go.sum.

Store the complete PHC string in `identity.users.password_hash`:

```text
$argon2id$v=19$m=65536,t=3,p=4$<unpadded-base64-salt>$<unpadded-base64-key>
```

Verification checks the exact supported profile, total length, canonical base64,
and salt/key lengths before running Argon2. It never allocates memory according
to arbitrary stored parameters. Comparison uses `crypto/subtle` constant-time
comparison. Unsupported versions/profiles fail closed; introducing another
profile requires explicit compatibility tests and a credential migration policy.

## Integration responsibilities

Passwords and PHC strings are secrets: never log them or expose stored hashes in
responses. Errors contain fixed messages only. Wrong passwords return `false,
nil`; invalid passwords and malformed hashes return sentinel errors. Login must
map these to generic authentication failure responses. Unknown-user timing,
authentication rate limiting, and endpoint integration belong to later tasks.

Each concurrent hash/verification uses roughly 64 MiB plus runtime overhead;
measure latency and set bounded authentication concurrency before exposing these
calls under load. This component does not implement scheduling, cancellation or
HTTP handlers. Clearing temporary derived-key buffers is best effort; Go strings
and Argon2 internal allocations are not guaranteed to be securely erased.

## Validation

Run `go test -race -count=1 ./src/passwords`, or `make check-all` for the complete
Docker suite. Tests cover correct/wrong passwords, unique salts, Unicode and
length boundaries, preservation of whitespace and normalization differences,
rejection of malformed or unsupported hashes before hashing, and a fixed
interoperability vector generated independently with argon2-cffi 25.1.0.
