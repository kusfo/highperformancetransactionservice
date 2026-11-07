package userapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type unicodeBackend struct{ values []string }

func (b *unicodeBackend) Create(_ context.Context, email, hash string, name *string) (users.User, error) {
	if name != nil {
		b.values = append(b.values, *name)
	}
	return users.User{UserID: "owner"}, nil
}
func (b *unicodeBackend) Authenticate(_ context.Context, email, password string) (sessions.Issued, error) {
	b.values = append(b.values, password)
	return sessions.Issued{}, nil
}
func (b *unicodeBackend) Update(_ context.Context, id, name string) (users.User, error) {
	b.values = append(b.values, name)
	return users.User{UserID: id, DisplayName: name}, nil
}
func (b *unicodeBackend) ChangePassword(_ context.Context, id, current, next string) error {
	b.values = append(b.values, current, next)
	return nil
}

func TestUserEndpointsUnicode(t *testing.T) {
	for _, tc := range []struct {
		escape, decoded string
		valid           bool
	}{
		{`\ud800`, "", false}, {`\udfff`, "", false}, {`\ud800\ud800`, "", false},
		{`\ud83d\ude00`, "😀", true}, {`\ufffd`, "�", true}, {`�`, "�", true},
		{`\\ud800`, `\ud800`, true},
	} {
		for _, endpoint := range []struct {
			method, path, body string
			status             int
		}{
			{"POST", "/users", `{"email":"unicode@example.com","password":"password-prefix-ESC"}`, 201},
			{"POST", "/users", `{"email":"unicode@example.com","password":"valid-password","displayName":"prefix-ESC"}`, 201},
			{"POST", "/sessions", `{"email":"unicode@example.com","password":"password-prefix-ESC"}`, 200},
			{"PATCH", "/users/owner", `{"displayName":"prefix-ESC"}`, 200},
			{"PUT", "/users/owner/password", `{"currentPassword":"password-prefix-ESC","newPassword":"valid-password"}`, 204},
			{"PUT", "/users/owner/password", `{"currentPassword":"valid-password","newPassword":"password-prefix-ESC"}`, 204},
		} {
			t.Run(endpoint.method+endpoint.path+tc.escape+endpoint.body, func(t *testing.T) {
				b := &unicodeBackend{}
				h := NewHandlerWithProfile(b, b, profileChecker{}, b)
				h.hash = func(password string) (string, error) {
					b.values = append(b.values, password)
					return "fixture-hash", nil
				}
				r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(strings.ReplaceAll(endpoint.body, "ESC", tc.escape)))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer fixture")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if !tc.valid {
					if w.Code != 400 || len(b.values) != 0 {
						t.Fatalf("malformed Unicode reached backend: status=%d calls=%d", w.Code, len(b.values))
					}
					return
				}
				if w.Code != endpoint.status {
					t.Fatalf("valid Unicode status=%d", w.Code)
				}
				found := false
				for _, value := range b.values {
					if value == "password-prefix-"+tc.decoded || value == "prefix-"+tc.decoded {
						found = true
					}
				}
				if !found {
					t.Fatal("decoded input not preserved")
				}
			})
		}
	}
}
