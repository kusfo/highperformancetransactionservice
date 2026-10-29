// Package authn authenticates protected HTTP entry points using opaque sessions.
package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

type Checker interface {
	Authenticate(context.Context, string) (sessions.Session, error)
}

// Identity contains no bearer credential or stored digest. Authorization of
// individual resources remains the protected handler's responsibility.
type Identity struct{ UserID string }
type identityKey struct{}

func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok && identity.UserID != ""
}

// Require must run inside the shared HTTPS Runtime, which supplies request
// deadlines and safe access logging. The checker performs a fresh session lookup
// on every entry; workers must not revalidate previously accepted operations.
func Require(checker Checker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			unauthorized(w)
			return
		}
		scheme, token, ok := strings.Cut(headers[0], " ")
		token = strings.TrimLeft(token, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n,") {
			unauthorized(w)
			return
		}
		if r.Context().Err() != nil {
			cancelled(w)
			return
		}
		session, err := checker.Authenticate(r.Context(), token)
		if r.Context().Err() != nil {
			cancelled(w)
			return
		}
		if errors.Is(err, sessions.ErrInvalidSession) {
			unauthorized(w)
			return
		}
		if err != nil || session.UserID == "" {
			httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, Identity{UserID: session.UserID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpapi.WriteError(w, 401, "UNAUTHORIZED", "Authentication required.")
}
func cancelled(w http.ResponseWriter) {
	httpapi.WriteError(w, 503, "REQUEST_CANCELLED", "Request was cancelled.")
}
